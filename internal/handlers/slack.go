package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github-slack-notifier/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// SlackHandler handles Slack webhook events and slash commands.
type SlackHandler struct {
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	cloudTasksService *services.CloudTasksService
	githubAuthService *services.GitHubAuthService
	signingSecret     string
	config            *config.Config
}

// NewSlackHandler creates a new SlackHandler with the provided services and configuration.
func NewSlackHandler(
	fs *services.FirestoreService,
	slack *services.SlackService,
	cloudTasks *services.CloudTasksService,
	githubAuth *services.GitHubAuthService,
	cfg *config.Config,
) *SlackHandler {
	return &SlackHandler{
		firestoreService:  fs,
		slackService:      slack,
		cloudTasksService: cloudTasks,
		githubAuthService: githubAuth,
		signingSecret:     cfg.SlackSigningSecret,
		config:            cfg,
	}
}

// HandleSlashCommand processes incoming Slack slash commands.
func (sh *SlackHandler) HandleSlashCommand(c *gin.Context) {
	signature := c.GetHeader("X-Slack-Signature")
	timestamp := c.GetHeader("X-Slack-Request-Timestamp")

	if signature == "" || timestamp == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing signature or timestamp"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	if err := sh.verifySignature(c.Request.Header, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse form data"})
		return
	}

	command := values.Get("command")
	userID := values.Get("user_id")
	teamID := values.Get("team_id")
	text := values.Get("text")

	ctx := c.Request.Context()
	var response string

	switch command {
	case "/notify-channel":
		response, err = sh.handleNotifyChannel(ctx, userID, teamID, text)
	case "/notify-link":
		response, err = sh.handleNotifyLink(ctx, userID, teamID, text)
	case "/notify-unlink":
		response, err = sh.handleNotifyUnlink(ctx, userID)
	case "/notify-status":
		response, err = sh.handleNotifyStatus(ctx, userID)
	default:
		c.JSON(http.StatusOK, gin.H{"text": "Unknown command"})
		return
	}

	if err != nil {
		// Log the actual error for debugging
		log.Error(ctx, "Slack command failed",
			"command", command,
			"user_id", userID,
			"error", err,
		)

		// Return user-friendly error message (always HTTP 200 per Slack docs)
		c.JSON(http.StatusOK, gin.H{
			"response_type": "ephemeral",
			"text":          "❌ Something went wrong. Please try again later.",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"text": response})
}

// HandleEvent processes incoming Slack Events API events.
func (sh *SlackHandler) HandleEvent(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	if err := sh.verifySignature(c.Request.Header, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse event"})
		return
	}

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var r *slackevents.ChallengeResponse
		err := json.Unmarshal(body, &r)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse challenge"})
			return
		}
		c.String(http.StatusOK, r.Challenge)
		return
	}

	// Handle events
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		if ev, ok := innerEvent.Data.(*slackevents.MessageEvent); ok {
			sh.handleMessageEvent(c.Request.Context(), ev)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleMessageEvent processes message events to detect and track GitHub PR links.
func (sh *SlackHandler) handleMessageEvent(ctx context.Context, event *slackevents.MessageEvent) {
	// Skip bot messages, edited messages, and messages without text
	if event.BotID != "" || event.SubType == "message_changed" || event.Text == "" {
		return
	}

	// Extract PR links from message text
	prLinks := utils.ExtractPRLinks(event.Text)
	if len(prLinks) == 0 {
		return
	}

	// Process each PR link found (though we expect only one based on our utility logic)
	for _, prLink := range prLinks {
		jobID := uuid.New().String()
		traceID := uuid.New().String()

		job := &models.ManualLinkJob{
			ID:             jobID,
			PRNumber:       prLink.PRNumber,
			RepoFullName:   prLink.FullRepoName,
			SlackChannel:   event.Channel,
			SlackMessageTS: event.TimeStamp,
			TraceID:        traceID,
		}

		// Queue for async processing
		err := sh.cloudTasksService.EnqueueManualLinkProcessing(ctx, job)
		if err != nil {
			log.Error(ctx, "Failed to enqueue manual link processing",
				"error", err,
				"repo", prLink.FullRepoName,
				"pr_number", prLink.PRNumber,
				"slack_channel", event.Channel,
				"slack_message_ts", event.TimeStamp,
			)
		} else {
			log.Info(ctx, "Manual PR link detected and queued for processing",
				"repo", prLink.FullRepoName,
				"pr_number", prLink.PRNumber,
				"slack_channel", event.Channel,
				"job_id", jobID,
			)
		}
	}
}

func (sh *SlackHandler) handleNotifyChannel(ctx context.Context, userID, teamID, text string) (string, error) {
	if text == "" {
		return "📝 *Usage:* `/notify-channel #channel-name`\n\n" +
			"Set your default channel for GitHub PR notifications. Example: `/notify-channel #engineering`", nil
	}

	channel, displayName := parseChannelFromText(text)
	if channel == "" {
		return "❌ Please provide a valid channel name. Example: `/notify-channel #engineering`", nil
	}

	err := sh.slackService.ValidateChannel(ctx, channel)
	if err != nil {
		// Log the actual Slack API error for debugging
		log.Error(ctx, "Channel validation failed",
			"channel", channel,
			"slack_api_error", err,
		)

		// Return user-friendly message for channel validation errors (user input error, not system error)
		return fmt.Sprintf("❌ Channel `#%s` not found or bot doesn't have access. "+
			"Make sure the channel exists and the bot has been invited to it.", displayName), nil
	}

	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		return "", err
	}

	if user == nil {
		user = &models.User{
			ID:          userID,
			SlackUserID: userID,
			SlackTeamID: teamID,
		}
	}

	user.DefaultChannel = channel
	err = sh.firestoreService.CreateOrUpdateUser(ctx, user)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("✅ Default notification channel set to <#%s|%s>", channel, displayName), nil
}

func (sh *SlackHandler) handleNotifyLink(ctx context.Context, userID, teamID, text string) (string, error) {
	if text != "" {
		return "🔗 *New OAuth Flow Available!*\n\n" +
			"We've upgraded to secure GitHub OAuth authentication. " +
			"The `/notify-link` command no longer requires a username.\n\n" +
			"Simply run `/notify-link` to get your personalized OAuth link!", nil
	}

	// Create OAuth state for this user
	state, err := sh.githubAuthService.CreateOAuthState(ctx, userID, teamID)
	if err != nil {
		log.Error(ctx, "Failed to create OAuth state", "error", err, "user_id", userID)
		return "", services.ErrAuthLinkGeneration
	}

	// Generate OAuth link
	oauthURL := fmt.Sprintf("%s/auth/github/link?state=%s", sh.config.BaseURL, state.ID)

	return fmt.Sprintf("🔗 *Link Your GitHub Account*\n\n"+
		"Click this link to securely connect your GitHub account:\n"+
		"<%s|Connect GitHub Account>\n\n"+
		"This link expires in 15 minutes for security.", oauthURL), nil
}

func (sh *SlackHandler) handleNotifyStatus(ctx context.Context, userID string) (string, error) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		return "", err
	}

	if user == nil {
		return "❌ No configuration found. Use /notify-link to connect your GitHub account " +
			"and /notify-channel to set your default channel.", nil
	}

	status := "📊 *Your Configuration:*\n"
	if user.GitHubUsername != "" {
		verificationStatus := "✅ Verified"
		if !user.Verified {
			verificationStatus = "⚠️ Unverified (legacy)"
		}
		status += fmt.Sprintf("• GitHub: %s (%s)\n", user.GitHubUsername, verificationStatus)
	} else {
		status += "• GitHub: Not linked\n"
	}

	if user.DefaultChannel != "" {
		status += fmt.Sprintf("• Default Channel: <#%s>\n", user.DefaultChannel)
	} else {
		status += "• Default Channel: Not set\n"
	}

	return status, nil
}

func (sh *SlackHandler) handleNotifyUnlink(ctx context.Context, userID string) (string, error) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		return "", err
	}

	if user == nil || user.GitHubUsername == "" {
		return "❌ No GitHub account is currently linked to your Slack account.", nil
	}

	// Remove GitHub connection but keep other settings like default channel
	user.GitHubUsername = ""
	user.GitHubUserID = 0
	user.Verified = false

	err = sh.firestoreService.SaveUser(ctx, user)
	if err != nil {
		return "", err
	}

	return "✅ Your GitHub account has been disconnected. You can use `/notify-link` to connect a different account.", nil
}

// parseChannelFromText extracts channel ID and display name from various Slack channel formats:
// - "#channel-name" -> ("channel-name", "channel-name")
// - "channel-name" -> ("channel-name", "channel-name") - automatically adds # prefix
// - "<#C1234567890|channel-name>" -> ("C1234567890", "channel-name")
// - "C1234567890" -> ("C1234567890", "C1234567890").
func parseChannelFromText(text string) (string, string) {
	text = strings.TrimSpace(text)

	// Handle Slack's channel mention format: <#C1234567890|channel-name>
	if strings.HasPrefix(text, "<#") && strings.HasSuffix(text, ">") {
		// Extract the channel ID from <#C1234567890|channel-name>
		content := strings.TrimPrefix(text, "<#")
		content = strings.TrimSuffix(content, ">")
		if idx := strings.Index(content, "|"); idx != -1 {
			channelID := content[:idx]
			displayName := content[idx+1:]
			return channelID, displayName // Return both ID and display name
		}
		return content, content
	}

	// Handle simple channel name format: #channel-name
	if strings.HasPrefix(text, "#") {
		channelName := strings.TrimPrefix(text, "#")
		return channelName, channelName
	}

	// Handle direct channel ID (starts with C and is alphanumeric)
	if len(text) > 1 && strings.HasPrefix(text, "C") && isAlphanumeric(text) {
		return text, text
	}

	// Handle plain channel name without # prefix - automatically add it
	return text, text
}

// isAlphanumeric checks if a string contains only alphanumeric characters (for channel ID detection).
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func (sh *SlackHandler) verifySignature(header http.Header, body []byte) error {
	if sh.signingSecret == "" {
		return nil
	}

	sv, err := slack.NewSecretsVerifier(header, sh.signingSecret)
	if err != nil {
		return fmt.Errorf("failed to create secrets verifier: %w", err)
	}

	if _, err := sv.Write(body); err != nil {
		return fmt.Errorf("failed to write body to verifier: %w", err)
	}

	if err := sv.Ensure(); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}
