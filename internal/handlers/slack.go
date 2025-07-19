package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

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

// SlackHandler handles Slack webhook events.
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
		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			sh.handleMessageEvent(c.Request.Context(), ev)
		case *slackevents.AppHomeOpenedEvent:
			sh.handleAppHomeOpened(c.Request.Context(), ev)
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

// HandleInteraction processes interactive component actions from Slack App Home.
func (sh *SlackHandler) HandleInteraction(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	if err := sh.verifySignature(c.Request.Header, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	// Parse the form-encoded interaction payload
	values, err := url.ParseQuery(string(body))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse form data"})
		return
	}

	payloadJSON := values.Get("payload")
	if payloadJSON == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing payload"})
		return
	}

	var interaction slack.InteractionCallback
	if err := json.Unmarshal([]byte(payloadJSON), &interaction); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload JSON"})
		return
	}

	ctx := c.Request.Context()

	log.Info(ctx, "Processing Slack interaction",
		"type", interaction.Type,
		"action_id", func() string {
			if len(interaction.ActionCallback.BlockActions) > 0 {
				return interaction.ActionCallback.BlockActions[0].ActionID
			}
			return ""
		}(),
		"user_id", interaction.User.ID,
	)

	switch interaction.Type {
	case slack.InteractionTypeBlockActions:
		sh.handleBlockAction(ctx, &interaction, c)
	case slack.InteractionTypeViewSubmission:
		sh.handleViewSubmission(ctx, &interaction, c)
	case slack.InteractionTypeDialogCancellation,
		slack.InteractionTypeDialogSubmission,
		slack.InteractionTypeDialogSuggestion,
		slack.InteractionTypeInteractionMessage,
		slack.InteractionTypeMessageAction,
		slack.InteractionTypeBlockSuggestion,
		slack.InteractionTypeViewClosed,
		slack.InteractionTypeShortcut,
		slack.InteractionTypeWorkflowStepEdit:
		// Not handled for App Home implementation
		c.JSON(http.StatusOK, gin.H{})
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleBlockAction processes block action interactions.
func (sh *SlackHandler) handleBlockAction(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	if len(interaction.ActionCallback.BlockActions) == 0 {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	action := interaction.ActionCallback.BlockActions[0]
	userID := interaction.User.ID
	teamID := interaction.Team.ID

	switch action.ActionID {
	case "connect_github":
		sh.handleConnectGitHubAction(ctx, userID, teamID, interaction.TriggerID, c)
	case "disconnect_github":
		sh.handleDisconnectGitHubAction(ctx, userID, c)
	case "select_channel":
		sh.handleSelectChannelAction(ctx, userID, teamID, interaction.TriggerID, c)
	case "refresh_view":
		sh.handleRefreshViewAction(ctx, userID, c)
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleViewSubmission processes view submission interactions.
func (sh *SlackHandler) handleViewSubmission(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	if interaction.View.CallbackID == "channel_selector" {
		sh.handleChannelSelection(ctx, interaction, c)
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// handleAppHomeOpened processes app_home_opened events.
func (sh *SlackHandler) handleAppHomeOpened(ctx context.Context, event *slackevents.AppHomeOpenedEvent) {
	if event.Tab != "home" {
		return
	}

	userID := event.User
	log.Info(ctx, "App Home opened", "user_id", userID)

	// Get user data
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user data for App Home", "error", err, "user_id", userID)
		return
	}

	// Build and publish home view
	view := sh.slackService.BuildHomeView(user)
	err = sh.slackService.PublishHomeView(ctx, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to publish App Home view", "error", err, "user_id", userID)
	}
}

// handleConnectGitHubAction handles the "Connect GitHub Account" button.
func (sh *SlackHandler) handleConnectGitHubAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	state, err := sh.githubAuthService.CreateOAuthState(ctx, userID, teamID, "")
	if err != nil {
		log.Error(ctx, "Failed to create OAuth state for App Home", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"oauth_error": "Failed to generate OAuth link. Please try again.",
			},
		})
		return
	}

	// Mark this state as returning to App Home
	state.ReturnToHome = true
	err = sh.githubAuthService.SaveOAuthState(ctx, state)
	if err != nil {
		log.Error(ctx, "Failed to update OAuth state", "error", err, "user_id", userID)
	}

	oauthURL := fmt.Sprintf("%s/auth/github/link?state=%s", sh.config.BaseURL, state.ID)

	log.Info(ctx, "Generated OAuth URL for App Home", "oauth_url", oauthURL, "user_id", userID)

	// Open a modal with the OAuth link
	modalView := sh.slackService.BuildOAuthModal(oauthURL)

	_, err = sh.slackService.OpenView(ctx, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open OAuth modal", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDisconnectGitHubAction handles the "Disconnect GitHub Account" button.
func (sh *SlackHandler) handleDisconnectGitHubAction(ctx context.Context, userID string, c *gin.Context) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user for disconnect", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	if user == nil || user.GitHubUsername == "" {
		// User already disconnected, refresh the view
		sh.refreshHomeView(ctx, userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Remove GitHub connection
	user.GitHubUsername = ""
	user.GitHubUserID = 0
	user.Verified = false

	err = sh.firestoreService.SaveUser(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to disconnect GitHub account", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Refresh the home view to show disconnected state
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// handleSelectChannelAction opens a modal for channel selection.
func (sh *SlackHandler) handleSelectChannelAction(ctx context.Context, userID, _ string, triggerID string, c *gin.Context) {
	modalView := sh.slackService.BuildChannelSelectorModal()

	_, err := sh.slackService.OpenView(ctx, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open channel selection modal", "error", err, "user_id", userID)
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleChannelSelection processes channel selection from the modal.
func (sh *SlackHandler) handleChannelSelection(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	userID := interaction.User.ID
	teamID := interaction.Team.ID

	// Extract selected channel from the view submission
	channelID := ""
	if values, ok := interaction.View.State.Values["channel_input"]; ok {
		if channelSelect, ok := values["channel_select"]; ok {
			channelID = channelSelect.SelectedChannel
		}
	}

	if channelID == "" {
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"channel_input": "Please select a channel.",
			},
		})
		return
	}

	// Validate the channel and check bot access
	err := sh.slackService.ValidateChannel(ctx, channelID)
	if err != nil {
		errorMsg := "Channel not found or bot doesn't have access."

		// Check for specific error types
		if errors.Is(err, services.ErrPrivateChannelNotSupported) {
			errorMsg = "Private channels are not supported for PR notifications. Please select a public channel."
		} else if errors.Is(err, services.ErrCannotJoinChannel) {
			// Get channel name for better error message
			channelName, nameErr := sh.slackService.GetChannelName(ctx, channelID)
			if nameErr == nil {
				errorMsg = fmt.Sprintf("Cannot join #%s. The channel may be archived or have restrictions.", channelName)
			} else {
				errorMsg = "Cannot join this channel. It may be archived or have restrictions."
			}
		}

		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"channel_input": errorMsg,
			},
		})
		return
	}

	// Update user's default channel
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user for channel update", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	if user == nil {
		user = &models.User{
			ID:          userID,
			SlackUserID: userID,
			SlackTeamID: teamID,
		}
	}

	user.DefaultChannel = channelID
	err = sh.firestoreService.CreateOrUpdateUser(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to update user channel", "error", err, "user_id", userID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Close modal and refresh home view
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{"response_action": "clear"})
}

// handleRefreshViewAction refreshes the App Home view.
func (sh *SlackHandler) handleRefreshViewAction(ctx context.Context, userID string, c *gin.Context) {
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// refreshHomeView refreshes the App Home view for a user.
func (sh *SlackHandler) refreshHomeView(ctx context.Context, userID string) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user data for refresh", "error", err, "user_id", userID)
		return
	}

	view := sh.slackService.BuildHomeView(user)
	err = sh.slackService.PublishHomeView(ctx, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to refresh App Home view", "error", err, "user_id", userID)
	}
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
