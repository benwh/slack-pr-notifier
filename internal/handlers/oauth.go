package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github.com/gin-gonic/gin"
)

// OAuthHandler handles GitHub OAuth endpoints.
type OAuthHandler struct {
	githubAuthService *services.GitHubAuthService
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
}

// NewOAuthHandler creates a new OAuth handler.
func NewOAuthHandler(
	githubAuthService *services.GitHubAuthService,
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
) *OAuthHandler {
	return &OAuthHandler{
		githubAuthService: githubAuthService,
		firestoreService:  firestoreService,
		slackService:      slackService,
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

// HandleGitHubCallback handles the GitHub OAuth callback.
// GET /auth/github/callback?code=<code>&state=<state_id>.
func (h *OAuthHandler) HandleGitHubCallback(c *gin.Context) {
	ctx := c.Request.Context()
	traceID := c.GetString("trace_id")

	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id": traceID,
		"handler":  "oauth_github_callback",
	})

	code := c.Query("code")
	stateID := c.Query("state")

	ctx = log.WithFields(ctx, log.LogFields{
		"state_id": stateID,
		"has_code": code != "",
	})

	log.Info(ctx, "GitHub OAuth callback received")

	// Check for error from GitHub
	if errorParam := c.Query("error"); errorParam != "" {
		errorDesc := c.Query("error_description")
		log.Warn(ctx, "GitHub OAuth error", "error", errorParam, "description", errorDesc)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Authorization Failed",
			"message": fmt.Sprintf("GitHub authorization failed: %s", errorDesc),
		})
		return
	}

	// Validate required parameters
	if code == "" || stateID == "" {
		log.Error(ctx, "Missing required parameters in OAuth callback")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid Callback",
			"message": "Missing required parameters from GitHub",
		})
		return
	}

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

	// Create or update user with verified GitHub account
	user := &models.User{
		ID:             state.SlackUserID, // Use Slack user ID as document ID
		GitHubUsername: githubUser.Login,
		GitHubUserID:   githubUser.ID,
		Verified:       true,
		SlackUserID:    state.SlackUserID,
		SlackTeamID:    state.SlackTeamID,
		UpdatedAt:      time.Now(),
	}

	// Check if user already exists
	existingUser, err := h.firestoreService.GetUser(ctx, state.SlackUserID)
	if err == nil && existingUser != nil {
		// Update existing user
		user.DefaultChannel = existingUser.DefaultChannel
		user.CreatedAt = existingUser.CreatedAt
	} else {
		// New user
		user.CreatedAt = time.Now()
	}

	if err := h.firestoreService.SaveUser(ctx, user); err != nil {
		log.Error(ctx, "Failed to save user after OAuth", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Save Failed",
			"message": "Failed to save your account information",
		})
		return
	}

	log.Info(ctx, "GitHub account successfully linked to Slack user")

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
				"You'll now receive personalized PR notifications!", githubUser.Login)

		err := h.slackService.SendEphemeralMessage(ctx, state.SlackTeamID, state.SlackChannel, state.SlackUserID, successMessage)
		if err != nil {
			log.Warn(ctx, "Failed to send OAuth success message to channel",
				"error", err,
				"channel", state.SlackChannel,
				"user_id", state.SlackUserID)
		}
	}

	// Redirect back to Slack with success message
	slackDeepLink := fmt.Sprintf("slack://channel?team=%s&id=general", state.SlackTeamID)

	// Create a simple HTML page that shows success and auto-redirects to Slack
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
</html>`, slackDeepLink, githubUser.Login, slackDeepLink)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(successHTML))
}
