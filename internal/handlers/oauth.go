package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

var (
	ErrInstallationNotFoundAfterRetries = fmt.Errorf("installation not found after retries")
)

// OAuthHandler handles GitHub and Slack OAuth endpoints.
type OAuthHandler struct {
	githubAuthService     *services.GitHubAuthService
	firestoreService      *services.FirestoreService
	slackService          *services.SlackService
	slackWorkspaceService *services.SlackWorkspaceService
	config                *config.Config
	httpClient            *http.Client
}

// NewOAuthHandler creates a new OAuth handler.
func NewOAuthHandler(
	githubAuthService *services.GitHubAuthService,
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
	slackWorkspaceService *services.SlackWorkspaceService,
	config *config.Config,
	httpClient *http.Client,
) *OAuthHandler {
	return &OAuthHandler{
		githubAuthService:     githubAuthService,
		firestoreService:      firestoreService,
		slackService:          slackService,
		slackWorkspaceService: slackWorkspaceService,
		config:                config,
		httpClient:            httpClient,
	}
}

// HandleGitHubLink initiates the GitHub OAuth flow.
// GET /auth/github/link?state=<state_id>.
func (h *OAuthHandler) HandleGitHubLink(c *gin.Context) {
	ctx := c.Request.Context()
	traceID := c.GetString("trace_id")

	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id": traceID,
		"handler":  "oauth_github_link",
	})

	stateID := c.Query("state")
	if stateID == "" {
		log.Error(ctx, "Missing state parameter in OAuth link request")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Request",
			"message": "Missing required state parameter",
		})
		return
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"state_id": stateID,
	})

	// Validate state exists and is not expired (don't consume yet)
	state, err := h.firestoreService.GetOAuthState(ctx, stateID)
	if err != nil {
		log.Error(ctx, "Invalid OAuth state in link request", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Request",
			"message": "Invalid or expired authorization request",
		})
		return
	}

	// Check if state is expired
	if time.Now().After(state.ExpiresAt) {
		log.Warn(ctx, "Expired OAuth state in link request")
		_ = h.firestoreService.DeleteOAuthState(ctx, stateID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Expired Request",
			"message": "Authorization request has expired. Please try again from Slack",
		})
		return
	}

	// Generate OAuth URL and redirect
	oauthURL := h.githubAuthService.GetOAuthURL(stateID)

	log.Info(ctx, "Redirecting to GitHub OAuth", "slack_user_id", state.SlackUserID)
	c.Redirect(http.StatusFound, oauthURL)
}

// validateGitHubCallbackParams validates and extracts GitHub OAuth callback parameters.
// Returns code, stateID, and success status. Handles both regular OAuth and combined OAuth + installation flows.
func (h *OAuthHandler) validateGitHubCallbackParams(ctx context.Context, c *gin.Context) (string, string, bool) {
	code := c.Query("code")
	stateID := c.Query("state")
	installationID := c.Query("installation_id")
	setupAction := c.Query("setup_action")

	// Check for error from GitHub
	if errorParam := c.Query("error"); errorParam != "" {
		errorDesc := c.Query("error_description")
		log.Warn(ctx, "GitHub OAuth error", "error", errorParam, "description", errorDesc)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Authorization Failed",
			"message": fmt.Sprintf("GitHub authorization failed: %s", errorDesc),
		})
		return "", "", false
	}

	// Handle GitHub App installation callback (installation_id + setup_action)
	// This indicates a combined OAuth + installation flow
	if installationID != "" && setupAction == "install" {
		log.Info(ctx, "GitHub App combined OAuth + installation callback received",
			"installation_id", installationID,
			"setup_action", setupAction,
			"has_code", code != "",
			"has_state", stateID != "")

		// For combined flow, we need both code and state to process properly
		if code == "" || stateID == "" {
			log.Error(ctx, "Missing required parameters for combined OAuth + installation flow")
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "Invalid Callback",
				"message": "Missing required parameters for GitHub App installation",
			})
			return "", "", false
		}

		// Return parameters to be processed by the combined flow handler
		// The installationID will be available in the query parameters
		return code, stateID, true
	}

	// Validate required parameters for OAuth flow
	if code == "" || stateID == "" {
		log.Error(ctx, "Missing required parameters in OAuth callback")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Callback",
			"message": "Missing required parameters from GitHub",
		})
		return "", "", false
	}

	return code, stateID, true
}

// createOrUpdateUserFromGitHub creates or updates a user record after successful GitHub authentication.
// Preserves existing user preferences while updating GitHub credentials and display name.
func (h *OAuthHandler) createOrUpdateUserFromGitHub(
	ctx context.Context,
	state *models.OAuthState,
	githubUser *services.GitHubUser,
) (*models.User, error) {
	user := &models.User{
		ID:                   state.SlackUserID, // Use Slack user ID as document ID
		SlackUserID:          state.SlackUserID, // Set the slack_user_id field
		GitHubUsername:       githubUser.Login,
		GitHubUserID:         githubUser.ID,
		Verified:             true,
		SlackTeamID:          state.SlackTeamID,
		NotificationsEnabled: true,             // Default to enabled for new users
		TaggingEnabled:       true,             // Default to enabled for new users
		ImpersonationEnabled: &[]bool{true}[0], // Default to enabled for new users
		UpdatedAt:            time.Now(),
	}

	// Try to fetch Slack display name for debugging
	slackUser, err := h.slackService.GetUserInfo(ctx, state.SlackTeamID, state.SlackUserID)
	if err != nil {
		log.Warn(ctx, "Failed to fetch Slack user info for display name", "error", err)
	} else if slackUser != nil {
		if slackUser.Profile.DisplayName != "" {
			user.SlackDisplayName = slackUser.Profile.DisplayName
		} else if slackUser.RealName != "" {
			user.SlackDisplayName = slackUser.RealName
		} else {
			user.SlackDisplayName = slackUser.Name
		}
	}

	// Check if user already exists
	existingUser, err := h.firestoreService.GetUser(ctx, state.SlackUserID)
	if err == nil && existingUser != nil {
		// Update existing user - preserve user preferences but update GitHub data
		user.DefaultChannel = existingUser.DefaultChannel
		user.CreatedAt = existingUser.CreatedAt
		user.NotificationsEnabled = existingUser.NotificationsEnabled
		user.TaggingEnabled = existingUser.TaggingEnabled
		// Preserve display name if we didn't get a new one
		if user.SlackDisplayName == "" && existingUser.SlackDisplayName != "" {
			user.SlackDisplayName = existingUser.SlackDisplayName
		}
	} else {
		// New user
		user.CreatedAt = time.Now()
	}

	if err := h.firestoreService.SaveUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to save user: %w", err)
	}

	return user, nil
}

// handlePostOAuthActions handles post-OAuth actions including App Home refresh and success notifications.
// Sends ephemeral success message to channel or refreshes App Home based on OAuth initiation context.
func (h *OAuthHandler) handlePostOAuthActions(
	ctx context.Context,
	state *models.OAuthState,
	user *models.User,
	githubUsername string,
) {
	// If this was initiated from App Home, refresh the home view
	if state.ReturnToHome {
		// Get GitHub installations for this workspace
		installations, err := h.firestoreService.GetGitHubInstallationsByWorkspace(ctx, state.SlackTeamID)
		if err != nil {
			log.Error(ctx, "Failed to get GitHub installations for OAuth refresh", "error", err)
			installations = nil
		}
		hasInstallations := len(installations) > 0

		homeView := h.slackService.BuildHomeView(user, hasInstallations, installations)
		err = h.slackService.PublishHomeViewAndCloseModals(ctx, state.SlackTeamID, state.SlackUserID, homeView)
		if err != nil {
			log.Warn(ctx, "Failed to refresh App Home after OAuth success",
				"error", err,
				"user_id", state.SlackUserID)
		} else {
			log.Info(ctx, "App Home refreshed after successful GitHub OAuth - modals closed")
		}
	}

	// Send ephemeral success message to the channel where OAuth was initiated (if not from App Home)
	if state.SlackChannel != "" && !state.ReturnToHome {
		successMessage := fmt.Sprintf(
			"✅ *GitHub Account Linked!*\n\nYour GitHub account `@%s` has been successfully connected. "+
				"You'll now receive personalized PR notifications!", githubUsername)

		err := h.slackService.SendEphemeralMessage(ctx, state.SlackTeamID, state.SlackChannel, state.SlackUserID, successMessage)
		if err != nil {
			log.Warn(ctx, "Failed to send OAuth success message to channel",
				"error", err,
				"channel", state.SlackChannel,
				"user_id", state.SlackUserID)
		}
	}
}

// HandleGitHubCallback handles the GitHub OAuth callback.
// GET /auth/github/callback?code=<code>&state=<state_id>.
func (h *OAuthHandler) HandleGitHubCallback(c *gin.Context) {
	ctx := c.Request.Context()
	traceID := c.GetString("trace_id")

	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id": traceID,
		"handler":  "oauth_github_callback",
	})

	// Validate callback parameters
	code, stateID, ok := h.validateGitHubCallbackParams(ctx, c)
	if !ok {
		return
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"state_id": stateID,
		"has_code": code != "",
	})

	log.Info(ctx, "GitHub OAuth callback received")

	// Validate and consume OAuth state
	state, err := h.githubAuthService.ValidateAndConsumeState(ctx, stateID)
	if err != nil {
		log.Error(ctx, "OAuth state validation failed", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Request",
			"message": "Invalid or expired authorization request",
		})
		return
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"slack_user_id": state.SlackUserID,
		"slack_team_id": state.SlackTeamID,
	})

	// Check if this is a combined OAuth + installation flow
	installationID := c.Query("installation_id")
	if installationID != "" {
		log.Info(ctx, "Processing combined OAuth + installation flow", "installation_id", installationID)
		err := h.processGitHubAppInstallation(ctx, code, stateID, installationID, state)
		if err != nil {
			log.Error(ctx, "Failed to process GitHub App installation", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "Installation Failed",
				"message": "Failed to complete GitHub App installation",
			})
			return
		}

		// Redirect to success page for installation
		h.redirectToInstallationSuccessPage(c, state.SlackTeamID, installationID)
		return
	}

	// Process user OAuth only flow
	githubUsername, err := h.processUserOAuth(ctx, code, stateID, state)
	if err != nil {
		log.Error(ctx, "Failed to process user OAuth", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Authentication Failed",
			"message": "Failed to authenticate with GitHub",
		})
		return
	}

	// Redirect to success page for user OAuth
	h.redirectToSuccessPage(c, state.SlackTeamID, githubUsername)
}

// processUserOAuth processes user-only OAuth flow without GitHub App installation.
// Exchanges OAuth code for user info, creates/updates user record, and handles post-OAuth actions.
func (h *OAuthHandler) processUserOAuth(ctx context.Context, code, _ string, state *models.OAuthState) (string, error) {
	// Exchange code for GitHub user info
	githubUser, err := h.githubAuthService.ExchangeCodeForUser(ctx, code)
	if err != nil {
		return "", fmt.Errorf("failed to exchange OAuth code for user info: %w", err)
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"github_user_id":  githubUser.ID,
		"github_username": githubUser.Login,
	})

	// Create or update user
	user, err := h.createOrUpdateUserFromGitHub(ctx, state, githubUser)
	if err != nil {
		return "", fmt.Errorf("failed to save user after OAuth: %w", err)
	}

	log.Info(ctx, "GitHub account successfully linked to Slack user")

	// Handle post-OAuth actions (Slack notifications, App Home refresh)
	h.handlePostOAuthActions(ctx, state, user, githubUser.Login)

	return githubUser.Login, nil
}

// processGitHubAppInstallation processes combined OAuth + GitHub App installation flow.
// Associates GitHub installation with Slack workspace and creates/updates user record.
func (h *OAuthHandler) processGitHubAppInstallation(
	ctx context.Context, code, _, installationID string, state *models.OAuthState,
) error {
	// Parse installation ID
	installationIDInt, err := strconv.ParseInt(installationID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid installation ID: %w", err)
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"installation_id": installationIDInt,
	})

	// Exchange code for GitHub user info
	// Note: We only need user info for workspace association, not for installation access verification
	githubUser, err := h.githubAuthService.ExchangeCodeForUser(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange OAuth code for user info: %w", err)
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"github_user_id":  githubUser.ID,
		"github_username": githubUser.Login,
	})

	// Note: We skip user installation access verification for the combined flow
	// because the user is coming directly from the GitHub App installation process,
	// which means they have the authority to associate this installation with their workspace.
	// The OAuth token may not have installation access scopes, which would cause verification to fail.

	// Look up the installation from our database (it should exist from webhook)
	// Use retry logic to handle race condition with installation webhook
	installation, err := h.waitForInstallationInDatabase(ctx, installationIDInt)
	if err != nil {
		return fmt.Errorf("installation not found in database after retries: %w", err)
	}

	// Update installation with workspace association
	installation.SlackWorkspaceID = state.SlackTeamID
	installation.InstalledBySlackUser = state.SlackUserID
	installation.InstalledByGitHubUser = githubUser.ID
	installation.UpdatedAt = time.Now()

	// Save updated installation
	err = h.firestoreService.UpdateGitHubInstallation(ctx, installation)
	if err != nil {
		return fmt.Errorf("failed to update installation with workspace association: %w", err)
	}

	// Create or update user
	user, err := h.createOrUpdateUserFromGitHub(ctx, state, githubUser)
	if err != nil {
		return fmt.Errorf("failed to save user after installation: %w", err)
	}

	log.Info(ctx, "GitHub App installation successfully associated with workspace",
		"installation_id", installationIDInt,
		"workspace_id", state.SlackTeamID,
		"account_login", installation.AccountLogin,
		"repository_selection", installation.RepositorySelection,
	)

	// Handle post-OAuth actions (Slack notifications, App Home refresh)
	h.handlePostOAuthActions(ctx, state, user, githubUser.Login)

	return nil
}

// waitForInstallationInDatabase waits for GitHub installation to be created by webhook with exponential backoff.
// Uses retry logic to handle race condition between OAuth callback and installation webhook.
func (h *OAuthHandler) waitForInstallationInDatabase(ctx context.Context, installationID int64) (*models.GitHubInstallation, error) {
	const (
		maxRetries    = 8
		initialDelay  = 50 * time.Millisecond
		maxDelay      = 2 * time.Second
		backoffFactor = 2
	)

	delay := initialDelay
	for i := range maxRetries {
		installation, err := h.firestoreService.GetGitHubInstallationByID(ctx, installationID)
		if err == nil {
			log.Info(ctx, "Installation found in database",
				"installation_id", installationID,
				"retry_attempt", i+1,
				"total_wait_ms", int64((initialDelay*time.Duration(1<<i))/time.Millisecond))
			return installation, nil
		}

		if i < maxRetries-1 {
			log.Debug(ctx, "Installation not yet available, retrying",
				"installation_id", installationID,
				"retry_attempt", i+1,
				"delay_ms", delay.Milliseconds(),
				"error", err)

			time.Sleep(delay)
			if delay < maxDelay {
				delay *= backoffFactor
				if delay > maxDelay {
					delay = maxDelay
				}
			}
		}
	}

	log.Warn(ctx, "Installation not found after all retries",
		"installation_id", installationID,
		"max_retries", maxRetries)
	return nil, fmt.Errorf("%w: installation %d not found after %d retries", ErrInstallationNotFoundAfterRetries, installationID, maxRetries)
}

// redirectToInstallationSuccessPage creates and returns HTML success page for GitHub App installation flow.
// Includes automatic redirect to Slack App Home after 2 seconds.
func (h *OAuthHandler) redirectToInstallationSuccessPage(c *gin.Context, teamID, _ string) {
	slackDeepLink := fmt.Sprintf("slack://app?team=%s&id=%s&tab=home", teamID, h.config.SlackAppID)
	successHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>GitHub App Installed!</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 500px;
            margin: 50px auto;
            padding: 20px;
            text-align: center;
            background-color: #f8f9fa;
        }
        .success-icon { font-size: 48px; margin-bottom: 20px; }
        .success-message { color: #28a745; font-size: 20px; margin-bottom: 15px; }
        .details { color: #6c757d; margin-bottom: 30px; }
        .btn {
            background-color: #611f69;
            color: white;
            padding: 12px 24px;
            text-decoration: none;
            border-radius: 6px;
            font-weight: bold;
            display: inline-block;
            margin: 0 10px;
        }
        .btn:hover { background-color: #4a154b; }
        .auto-redirect { margin-top: 20px; color: #6c757d; font-size: 14px; }
    </style>
    <script>
        // Try to redirect to Slack after 2 seconds
        setTimeout(function() {
            window.location.href = '%s';
        }, 2000);
    </script>
</head>
<body>
    <div class="success-icon">🎉</div>
    <div class="success-message">GitHub App Installed!</div>
    <div class="details">
        Successfully installed PR Bot and linked your GitHub account.
        Your Slack workspace can now receive GitHub PR notifications!
    </div>
    <a href="%s" class="btn">Return to Slack</a>
    <div class="auto-redirect">Automatically redirecting to Slack in 2 seconds...</div>
</body>
</html>`, slackDeepLink, slackDeepLink)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(successHTML))
}

// redirectToSuccessPage creates and returns HTML success page for GitHub OAuth flow.
// Displays linked GitHub username and includes automatic redirect to Slack App Home after 2 seconds.
func (h *OAuthHandler) redirectToSuccessPage(c *gin.Context, teamID, githubUsername string) {
	slackDeepLink := fmt.Sprintf("slack://app?team=%s&id=%s&tab=home", teamID, h.config.SlackAppID)
	successHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>GitHub Account Linked!</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 500px;
            margin: 50px auto;
            padding: 20px;
            text-align: center;
            background-color: #f8f9fa;
        }
        .success-icon { font-size: 48px; margin-bottom: 20px; }
        .success-message { color: #28a745; font-size: 20px; margin-bottom: 15px; }
        .details { color: #6c757d; margin-bottom: 30px; }
        .btn {
            background-color: #611f69;
            color: white;
            padding: 12px 24px;
            text-decoration: none;
            border-radius: 6px;
            font-weight: bold;
            display: inline-block;
            margin: 0 10px;
        }
        .btn:hover { background-color: #4a154b; }
        .auto-redirect { margin-top: 20px; color: #6c757d; font-size: 14px; }
    </style>
    <script>
        // Try to redirect to Slack after 2 seconds
        setTimeout(function() {
            window.location.href = '%s';
        }, 2000);
    </script>
</head>
<body>
    <div class="success-icon">✅</div>
    <div class="success-message">GitHub Account Linked!</div>
    <div class="details">
        Successfully linked <strong>@%s</strong> to your Slack account.
        You can now receive personalized PR notifications.
    </div>
    <a href="%s" class="btn">Return to Slack</a>
    <div class="auto-redirect">Automatically redirecting to Slack in 2 seconds...</div>
</body>
</html>`, slackDeepLink, githubUsername, slackDeepLink)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(successHTML))
}

// HandleSlackInstall initiates Slack OAuth installation flow.
// GET /auth/slack/install.
func (h *OAuthHandler) HandleSlackInstall(c *gin.Context) {
	ctx := c.Request.Context()
	traceID := c.GetString("trace_id")

	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id": traceID,
		"handler":  "slack_oauth_install",
	})

	if !h.config.IsSlackOAuthEnabled() {
		log.Error(ctx, "Slack OAuth not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "OAuth Not Available",
			"message": "Slack OAuth installation is not configured",
		})
		return
	}

	// Build OAuth URL
	oauthURL := fmt.Sprintf(
		"https://slack.com/oauth/v2/authorize?client_id=%s&scope=%s&redirect_uri=%s",
		url.QueryEscape(h.config.SlackClientID),
		url.QueryEscape("channels:read,chat:write,links:read,channels:history"),
		url.QueryEscape(h.config.SlackRedirectURL()),
	)

	log.Info(ctx, "Redirecting to Slack OAuth installation")
	c.Redirect(http.StatusFound, oauthURL)
}

// HandleSlackOAuthCallback handles Slack OAuth callback after workspace installation.
// Exchanges authorization code for workspace access token and saves workspace configuration.
// GET /auth/slack/callback?code=<code>&state=<state>.
func (h *OAuthHandler) HandleSlackOAuthCallback(c *gin.Context) {
	ctx := c.Request.Context()
	traceID := c.GetString("trace_id")

	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id": traceID,
		"handler":  "slack_oauth_callback",
	})

	code := c.Query("code")

	ctx = log.WithFields(ctx, log.LogFields{
		"has_code": code != "",
	})

	log.Info(ctx, "Slack OAuth callback received")

	// Check for error from Slack
	if errorParam := c.Query("error"); errorParam != "" {
		log.Warn(ctx, "Slack OAuth error", "error", errorParam)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Installation Failed",
			"message": fmt.Sprintf("Slack installation failed: %s", errorParam),
		})
		return
	}

	if code == "" {
		log.Error(ctx, "Missing authorization code in Slack OAuth callback")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Callback",
			"message": "Missing authorization code from Slack",
		})
		return
	}

	// Exchange code for access token
	token, err := h.exchangeSlackOAuthCode(ctx, code)
	if err != nil {
		log.Error(ctx, "Failed to exchange Slack OAuth code", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Installation Failed",
			"message": "Failed to complete Slack installation",
		})
		return
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"team_id":   token.Team.ID,
		"team_name": token.Team.Name,
	})

	// Save workspace installation
	workspace := &models.SlackWorkspace{
		ID:           token.Team.ID,
		TeamName:     token.Team.Name,
		AccessToken:  token.AccessToken,
		Scope:        token.Scope,
		InstalledBy:  token.AuthedUser.ID,
		InstalledAt:  time.Now(),
		UpdatedAt:    time.Now(),
		AppID:        token.AppID,
		BotUserID:    token.BotUserID,
		EnterpriseID: token.Enterprise.ID,
	}

	if err := h.slackWorkspaceService.SaveWorkspace(ctx, workspace); err != nil {
		log.Error(ctx, "Failed to save Slack workspace", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Installation Failed",
			"message": "Failed to save workspace installation",
		})
		return
	}

	log.Info(ctx, "Slack workspace installed successfully")

	// Create success page with deep link to app home
	slackDeepLink := fmt.Sprintf("slack://app?team=%s&id=%s&tab=home", token.Team.ID, h.config.SlackAppID)
	successHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>PR Bot Installed!</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 500px;
            margin: 50px auto;
            padding: 20px;
            text-align: center;
            background-color: #f8f9fa;
        }
        .success-icon { font-size: 48px; margin-bottom: 20px; }
        .success-message { color: #28a745; font-size: 20px; margin-bottom: 15px; }
        .details { color: #6c757d; margin-bottom: 30px; }
        .btn {
            background-color: #611f69;
            color: white;
            padding: 12px 24px;
            text-decoration: none;
            border-radius: 6px;
            font-weight: bold;
            display: inline-block;
            margin: 0 10px;
        }
        .btn:hover { background-color: #4a154b; }
        .auto-redirect { margin-top: 20px; color: #6c757d; font-size: 14px; }
    </style>
    <script>
        // Try to redirect to Slack after 2 seconds
        setTimeout(function() {
            window.location.href = '%s';
        }, 2000);
    </script>
</head>
<body>
    <div class="success-icon">🎉</div>
    <div class="success-message">PR Bot Installed!</div>
    <div class="details">
        Successfully installed PR Bot in <strong>%s</strong> workspace.<br>
        You can now receive GitHub PR notifications in Slack!
    </div>
    <a href="%s" class="btn">Open Slack</a>
    <div class="auto-redirect">Automatically redirecting to Slack in 2 seconds...</div>
</body>
</html>`, slackDeepLink, token.Team.Name, slackDeepLink)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(successHTML))
}

// exchangeSlackOAuthCode exchanges Slack OAuth authorization code for workspace access token.
// Uses slack-go library to perform the token exchange with Slack's OAuth v2 endpoint.
func (h *OAuthHandler) exchangeSlackOAuthCode(ctx context.Context, code string) (*slack.OAuthV2Response, error) {
	// Use slack-go library to exchange code for access token
	resp, err := slack.GetOAuthV2ResponseContext(
		ctx,
		h.httpClient,
		h.config.SlackClientID,
		h.config.SlackClientSecret,
		code,
		h.config.SlackRedirectURL(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", models.ErrSlackOAuthFailed, err.Error())
	}

	return resp, nil
}
