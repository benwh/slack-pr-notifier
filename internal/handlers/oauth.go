package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github.com/gin-gonic/gin"
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

// validateGitHubCallbackParams validates GitHub OAuth callback parameters.
func (h *OAuthHandler) validateGitHubCallbackParams(ctx context.Context, c *gin.Context) (string, string, bool) {
	code := c.Query("code")
	stateID := c.Query("state")

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

	// Validate required parameters
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

// createOrUpdateUserFromGitHub creates or updates a user after successful GitHub authentication.
func (h *OAuthHandler) createOrUpdateUserFromGitHub(
	ctx context.Context,
	state *models.OAuthState,
	githubUser *services.GitHubUser,
) (*models.User, error) {
	user := &models.User{
		ID:                   state.SlackUserID, // Use Slack user ID as document ID
		GitHubUsername:       githubUser.Login,
		GitHubUserID:         githubUser.ID,
		Verified:             true,
		SlackTeamID:          state.SlackTeamID,
		NotificationsEnabled: true, // Default to enabled for new users
		TaggingEnabled:       true, // Default to enabled for new users
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
		// Update existing user
		user.DefaultChannel = existingUser.DefaultChannel
		user.CreatedAt = existingUser.CreatedAt
		// Preserve existing notification preference
		user.NotificationsEnabled = existingUser.NotificationsEnabled
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

// handlePostOAuthActions handles actions after successful OAuth (Slack notifications, App Home refresh).
func (h *OAuthHandler) handlePostOAuthActions(
	ctx context.Context,
	state *models.OAuthState,
	user *models.User,
	githubUsername string,
) {
	// If this was initiated from App Home, refresh the home view
	if state.ReturnToHome {
		homeView := h.slackService.BuildHomeView(user)
		err := h.slackService.PublishHomeView(ctx, state.SlackTeamID, state.SlackUserID, homeView)
		if err != nil {
			log.Warn(ctx, "Failed to refresh App Home after OAuth success",
				"error", err,
				"user_id", state.SlackUserID)
		} else {
			log.Info(ctx, "App Home refreshed after successful GitHub OAuth")
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

	// Exchange code for GitHub user info
	githubUser, err := h.githubAuthService.ExchangeCodeForUser(ctx, code)
	if err != nil {
		log.Error(ctx, "Failed to exchange OAuth code for user info", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Authentication Failed",
			"message": "Failed to authenticate with GitHub",
		})
		return
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"github_user_id":  githubUser.ID,
		"github_username": githubUser.Login,
	})

	// Create or update user
	user, err := h.createOrUpdateUserFromGitHub(ctx, state, githubUser)
	if err != nil {
		log.Error(ctx, "Failed to save user after OAuth", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Save Failed",
			"message": "Failed to save your account information",
		})
		return
	}

	log.Info(ctx, "GitHub account successfully linked to Slack user")

	// Handle post-OAuth actions (Slack notifications, App Home refresh)
	h.handlePostOAuthActions(ctx, state, user, githubUser.Login)

	// Redirect to success page
	h.redirectToSuccessPage(c, state.SlackTeamID, githubUser.Login)
}

// redirectToSuccessPage creates and returns the OAuth success HTML page.
func (h *OAuthHandler) redirectToSuccessPage(c *gin.Context, teamID, githubUsername string) {
	slackDeepLink := fmt.Sprintf("slack://channel?team=%s&id=general", teamID)
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

// SlackOAuthResponse represents the response from Slack's oauth.v2.access endpoint.
type SlackOAuthResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	AuthedUser struct {
		ID string `json:"id"`
	} `json:"authed_user"`
}

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
		ID:          token.Team.ID,
		TeamName:    token.Team.Name,
		AccessToken: token.AccessToken,
		Scope:       token.Scope,
		InstalledBy: token.AuthedUser.ID,
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
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

	// Create success page
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
    </style>
</head>
<body>
    <div class="success-icon">🎉</div>
    <div class="success-message">PR Bot Installed!</div>
    <div class="details">
        Successfully installed PR Bot in <strong>%s</strong> workspace.<br>
        You can now receive GitHub PR notifications in Slack!
    </div>
    <a href="slack://open" class="btn">Open Slack</a>
</body>
</html>`, token.Team.Name)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(successHTML))
}

// exchangeSlackOAuthCode exchanges an OAuth authorization code for an access token.
func (h *OAuthHandler) exchangeSlackOAuthCode(ctx context.Context, code string) (*SlackOAuthResponse, error) {
	// Prepare the request to Slack's oauth.v2.access endpoint
	data := url.Values{}
	data.Set("client_id", h.config.SlackClientID)
	data.Set("client_secret", h.config.SlackClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", h.config.SlackRedirectURL())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/oauth.v2.access", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OAuth request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var oauthResp SlackOAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&oauthResp); err != nil {
		return nil, fmt.Errorf("failed to decode OAuth response: %w", err)
	}

	if !oauthResp.OK {
		return nil, fmt.Errorf("%w: %s", models.ErrSlackOAuthFailed, oauthResp.Error)
	}

	return &oauthResp, nil
}
