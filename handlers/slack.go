package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github-slack-notifier/models"
	"github-slack-notifier/services"

	"github.com/gin-gonic/gin"
)

// SlackHandler handles Slack webhook events and slash commands.
type SlackHandler struct {
	firestoreService *services.FirestoreService
	slackService     *services.SlackService
	signingSecret    string
}

// NewSlackHandler creates a new SlackHandler with the provided services and signing secret.
func NewSlackHandler(fs *services.FirestoreService, slack *services.SlackService, secret string) *SlackHandler {
	return &SlackHandler{
		firestoreService: fs,
		slackService:     slack,
		signingSecret:    secret,
	}
}

// HandleWebhook processes incoming Slack webhook events and slash commands.
func (sh *SlackHandler) HandleWebhook(c *gin.Context) {
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

	if !sh.verifySignature(signature, timestamp, body) {
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

	ctx := context.Background()
	var response string

	switch command {
	case "/notify-channel":
		response, err = sh.handleNotifyChannel(ctx, userID, teamID, text)
	case "/notify-link":
		response, err = sh.handleNotifyLink(ctx, userID, teamID, text)
	case "/notify-status":
		response, err = sh.handleNotifyStatus(ctx, userID)
	default:
		c.JSON(http.StatusOK, gin.H{"text": "Unknown command"})
		return
	}

	if err != nil {
		// Log the actual error for debugging
		correlationID := c.GetString("correlation_id")
		slog.Error("Slack command failed",
			"correlation_id", correlationID,
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

func (sh *SlackHandler) handleNotifyChannel(ctx context.Context, userID, teamID, text string) (string, error) {
	if text == "" {
		return "📝 **Usage:** `/notify-channel #channel-name`\n\n" +
			"Set your default channel for GitHub PR notifications. Example: `/notify-channel #engineering`", nil
	}

	channel := strings.TrimPrefix(text, "#")
	if channel == "" {
		return "❌ Please provide a valid channel name. Example: `/notify-channel #engineering`", nil
	}

	err := sh.slackService.ValidateChannel(channel)
	if err != nil {
		// Return user-friendly message for channel validation errors (user input error, not system error)
		//nolint:nilerr
		return fmt.Sprintf("❌ Channel #%s not found or bot doesn't have access. "+
			"Make sure the channel exists and the bot has been invited to it.", channel), nil
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

	return "✅ Default notification channel set to #" + channel, nil
}

func (sh *SlackHandler) handleNotifyLink(ctx context.Context, userID, teamID, text string) (string, error) {
	if text == "" {
		return "🔗 **Usage:** `/notify-link github-username`\n\n" +
			"Link your GitHub account to receive personalized PR notifications. Example: `/notify-link octocat`", nil
	}

	githubUsername := strings.TrimSpace(text)
	if githubUsername == "" {
		return "❌ Please provide a valid GitHub username. Example: `/notify-link octocat`", nil
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

	user.GitHubUsername = githubUsername
	err = sh.firestoreService.CreateOrUpdateUser(ctx, user)
	if err != nil {
		return "", err
	}

	return "✅ GitHub username linked: " + githubUsername, nil
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
		status += fmt.Sprintf("• GitHub: %s\n", user.GitHubUsername)
	} else {
		status += "• GitHub: Not linked\n"
	}

	if user.DefaultChannel != "" {
		status += fmt.Sprintf("• Default Channel: #%s\n", user.DefaultChannel)
	} else {
		status += "• Default Channel: Not set\n"
	}

	return status, nil
}

func (sh *SlackHandler) verifySignature(signature, timestamp string, body []byte) bool {
	if sh.signingSecret == "" {
		return true
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	const maxTimestampAge = 300 // 5 minutes
	if time.Now().Unix()-ts > maxTimestampAge {
		return false
	}

	basestring := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(sh.signingSecret))
	mac.Write([]byte(basestring))
	expectedSignature := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
