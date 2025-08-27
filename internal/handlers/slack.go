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

var (
	// ErrMissingSlackSignature indicates the X-Slack-Signature header is missing.
	ErrMissingSlackSignature = fmt.Errorf("missing X-Slack-Signature header")
	// ErrMissingSlackTimestamp indicates the X-Slack-Request-Timestamp header is missing.
	ErrMissingSlackTimestamp = fmt.Errorf("missing X-Slack-Request-Timestamp header")
)

// SlackHandler handles Slack webhook events.
type SlackHandler struct {
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	cloudTasksService CloudTasksServiceInterface
	githubAuthService *services.GitHubAuthService
	signingSecret     string
	config            *config.Config
}

// NewSlackHandler creates a new SlackHandler with the provided services and configuration.
func NewSlackHandler(
	fs *services.FirestoreService,
	slack *services.SlackService,
	cloudTasks CloudTasksServiceInterface,
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
	ctx := c.Request.Context()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Error(ctx, "Failed to read request body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	log.Debug(ctx, "Received Slack event", "body_length", len(body))

	if err := sh.verifySignature(c.Request.Header, body); err != nil {
		log.Error(ctx, "Signature verification failed for Slack event",
			"error", err,
			"user_agent", c.Request.UserAgent(),
			"remote_addr", c.ClientIP(),
		)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
	if err != nil {
		log.Error(ctx, "Failed to parse Slack event", "error", err, "body_length", len(body))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse event"})
		return
	}

	log.Info(ctx, "Parsed Slack event", "event_type", eventsAPIEvent.Type)

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		log.Info(ctx, "Received URL verification challenge from Slack")

		var r *slackevents.ChallengeResponse
		err := json.Unmarshal(body, &r)
		if err != nil {
			log.Error(ctx, "Failed to parse URL verification challenge", "error", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse challenge"})
			return
		}

		log.Info(ctx, "Responding to URL verification challenge", "challenge_length", len(r.Challenge))
		c.String(http.StatusOK, r.Challenge)
		return
	}

	// Handle events
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		// Log the event type for debugging
		ctx := c.Request.Context()
		log.Info(ctx, "Processing Slack event",
			"event_type", innerEvent.Type,
			"team_id", eventsAPIEvent.TeamID)

		switch ev := innerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			sh.handleMessageEvent(ctx, ev, eventsAPIEvent.TeamID)
		case *slackevents.AppHomeOpenedEvent:
			sh.handleAppHomeOpened(ctx, ev, eventsAPIEvent.TeamID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleMessageEvent processes message events to detect and track GitHub PR links.
func (sh *SlackHandler) handleMessageEvent(ctx context.Context, event *slackevents.MessageEvent, teamID string) {
	// Skip bot messages, edited messages, and messages without text
	if event.BotID != "" || event.SubType == "message_changed" || event.Text == "" {
		return
	}

	// Check if manual tracking is enabled for this channel
	channelConfig, err := sh.firestoreService.GetChannelConfig(ctx, teamID, event.Channel)
	if err != nil {
		log.Error(ctx, "Failed to get channel config", "error", err)
		// Continue with default behavior on error
	}

	// Default to enabled if no config exists
	trackingEnabled := true
	if channelConfig != nil {
		trackingEnabled = channelConfig.ManualTrackingEnabled
	}

	if !trackingEnabled {
		log.Info(ctx, "Manual PR tracking disabled for channel",
			"channel", event.Channel,
			"message_ts", event.TimeStamp)
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

		// Add PR and Slack context for this iteration
		linkCtx := log.WithFields(ctx, log.LogFields{
			"repo":             prLink.FullRepoName,
			"pr_number":        prLink.PRNumber,
			"slack_channel":    event.Channel,
			"slack_message_ts": event.TimeStamp,
			"job_id":           jobID,
		})

		manualLinkJob := &models.ManualLinkJob{
			ID:             jobID,
			PRNumber:       prLink.PRNumber,
			RepoFullName:   prLink.FullRepoName,
			SlackChannel:   event.Channel,
			SlackMessageTS: event.TimeStamp,
			SlackTeamID:    teamID,
			TraceID:        traceID,
		}

		// Marshal the ManualLinkJob as the payload for the Job
		jobPayload, err := json.Marshal(manualLinkJob)
		if err != nil {
			log.Error(linkCtx, "Failed to marshal manual link job", "error", err)
			continue
		}

		// Create Job
		job := &models.Job{
			ID:      jobID,
			Type:    models.JobTypeManualPRLink,
			TraceID: traceID,
			Payload: jobPayload,
		}

		// Queue for async processing
		err = sh.cloudTasksService.EnqueueJob(linkCtx, job)
		if err != nil {
			log.Error(linkCtx, "Failed to enqueue manual link processing", "error", err)
		} else {
			log.Info(linkCtx, "Manual PR link detected and queued for processing")
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
	case "manage_channel_tracking":
		sh.handleManageChannelTrackingAction(ctx, userID, teamID, interaction.TriggerID, c)
	case "toggle_notifications":
		sh.handleToggleNotificationsAction(ctx, userID, c)
	case "toggle_user_tagging":
		sh.handleToggleUserTaggingAction(ctx, userID, c)
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleViewSubmission processes view submission interactions.
func (sh *SlackHandler) handleViewSubmission(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	switch interaction.View.CallbackID {
	case "channel_selector":
		sh.handleChannelSelection(ctx, interaction, c)
	case "channel_tracking_selector":
		sh.handleChannelTrackingSelection(ctx, interaction, c)
	case "save_channel_tracking":
		sh.handleSaveChannelTracking(ctx, interaction, c)
	default:
		log.Warn(ctx, "Unknown view submission callback ID",
			"callback_id", interaction.View.CallbackID)
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleAppHomeOpened processes app_home_opened events.
func (sh *SlackHandler) handleAppHomeOpened(ctx context.Context, event *slackevents.AppHomeOpenedEvent, teamID string) {
	if event.Tab != "home" {
		return
	}

	userID := event.User
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	log.Info(ctx, "App Home opened")

	// Get user data
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user data for App Home", "error", err)
		return
	}

	// Build and publish home view
	view := sh.slackService.BuildHomeView(user)
	err = sh.slackService.PublishHomeView(ctx, teamID, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to publish App Home view", "error", err)
	}
}

// handleConnectGitHubAction handles the "Connect GitHub Account" button.
func (sh *SlackHandler) handleConnectGitHubAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	state, err := sh.githubAuthService.CreateOAuthState(ctx, userID, teamID, "")
	if err != nil {
		log.Error(ctx, "Failed to create OAuth state for App Home", "error", err)
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
		log.Error(ctx, "Failed to update OAuth state", "error", err)
	}

	oauthURL := fmt.Sprintf("%s/auth/github/link?state=%s", sh.config.BaseURL, state.ID)

	log.Info(ctx, "Generated OAuth URL for App Home", "oauth_url", oauthURL)

	// Open a modal with the OAuth link
	modalView := sh.slackService.BuildOAuthModal(oauthURL)

	_, err = sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open OAuth modal", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDisconnectGitHubAction handles the "Disconnect GitHub Account" button.
func (sh *SlackHandler) handleDisconnectGitHubAction(ctx context.Context, userID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user for disconnect", "error", err)
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
		log.Error(ctx, "Failed to disconnect GitHub account", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Refresh the home view to show disconnected state
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// handleSelectChannelAction opens a modal for channel selection.
func (sh *SlackHandler) handleSelectChannelAction(ctx context.Context, userID, teamID string, triggerID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	modalView := sh.slackService.BuildChannelSelectorModal()

	_, err := sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open channel selection modal", "error", err)
	}

	c.JSON(http.StatusOK, gin.H{})
}

// extractChannelSelection extracts the selected channel from the interaction.
func (sh *SlackHandler) extractChannelSelection(interaction *slack.InteractionCallback) string {
	if values, ok := interaction.View.State.Values["channel_input"]; ok {
		if channelSelect, ok := values["channel_select"]; ok {
			return channelSelect.SelectedChannel
		}
	}
	return ""
}

// validateChannelSelection validates the selected channel and returns appropriate error message.
func (sh *SlackHandler) validateChannelSelection(ctx context.Context, teamID, channelID string) (string, error) {
	err := sh.slackService.ValidateChannel(ctx, teamID, channelID)
	if err == nil {
		return "", nil
	}

	errorMsg := "Channel not found or bot doesn't have access."

	// Check for specific error types
	if errors.Is(err, services.ErrPrivateChannelNotSupported) {
		errorMsg = "Private channels are not supported for PR notifications. Please select a public channel."
	} else if errors.Is(err, services.ErrCannotJoinChannel) {
		// Get channel name for better error message
		channelName, nameErr := sh.slackService.GetChannelName(ctx, teamID, channelID)
		if nameErr == nil {
			errorMsg = fmt.Sprintf("Cannot join #%s. The channel may be archived or have restrictions.", channelName)
		} else {
			errorMsg = "Cannot join this channel. It may be archived or have restrictions."
		}
	}

	return errorMsg, err
}

// createOrGetUserWithDisplayName creates a new user or gets existing one, fetching display name if needed.
func (sh *SlackHandler) createOrGetUserWithDisplayName(ctx context.Context, userID, teamID string) (*models.User, error) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if user == nil {
		user = &models.User{
			ID:                   userID,
			SlackTeamID:          teamID,
			NotificationsEnabled: true, // Default to enabled for new users
			TaggingEnabled:       true, // Default to enabled for new users
		}

		// Try to fetch Slack display name for new user
		slackUser, err := sh.slackService.GetUserInfo(ctx, teamID, userID)
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
	}

	return user, nil
}

// handleChannelSelection processes channel selection from the modal.
func (sh *SlackHandler) handleChannelSelection(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	userID := interaction.User.ID
	teamID := interaction.Team.ID

	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	// Extract selected channel
	channelID := sh.extractChannelSelection(interaction)
	if channelID == "" {
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"channel_input": "Please select a channel.",
			},
		})
		return
	}

	// Validate the channel
	if errorMsg, err := sh.validateChannelSelection(ctx, teamID, channelID); err != nil {
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"channel_input": errorMsg,
			},
		})
		return
	}

	// Get or create user
	user, err := sh.createOrGetUserWithDisplayName(ctx, userID, teamID)
	if err != nil {
		log.Error(ctx, "Failed to get user for channel update", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Update user's default channel
	user.DefaultChannel = channelID
	err = sh.firestoreService.CreateOrUpdateUser(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to update user channel", "error", err)
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
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user data for refresh", "error", err)
		return
	}

	if user == nil || user.SlackTeamID == "" {
		log.Error(ctx, "User not found or missing team ID for home view refresh")
		return
	}

	view := sh.slackService.BuildHomeView(user)
	err = sh.slackService.PublishHomeView(ctx, user.SlackTeamID, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to refresh App Home view", "error", err)
	}
}

// handleToggleNotificationsAction handles the notifications enable/disable toggle.
func (sh *SlackHandler) handleToggleNotificationsAction(ctx context.Context, userID string, c *gin.Context) {
	sh.handleUserSettingToggle(ctx, userID, c, "notifications", func(user *models.User) {
		user.NotificationsEnabled = !user.NotificationsEnabled
	}, func(user *models.User) map[string]interface{} {
		return map[string]interface{}{
			"notifications_enabled": user.NotificationsEnabled,
			"github_username":       user.GitHubUsername,
		}
	})
}

// handleToggleUserTaggingAction handles the user tagging enable/disable toggle.
func (sh *SlackHandler) handleToggleUserTaggingAction(ctx context.Context, userID string, c *gin.Context) {
	sh.handleUserSettingToggle(ctx, userID, c, "user tagging", func(user *models.User) {
		user.TaggingEnabled = !user.TaggingEnabled
	}, func(user *models.User) map[string]interface{} {
		return map[string]interface{}{
			"tagging_enabled": user.TaggingEnabled,
			"github_username": user.GitHubUsername,
		}
	})
}

// handleUserSettingToggle provides common logic for toggling user settings.
func (sh *SlackHandler) handleUserSettingToggle(
	ctx context.Context,
	userID string,
	c *gin.Context,
	settingName string,
	toggleFunc func(*models.User),
	logFieldsFunc func(*models.User) map[string]interface{},
) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, fmt.Sprintf("Failed to get user for %s toggle", settingName), "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	if user == nil {
		log.Warn(ctx, fmt.Sprintf("User not found for %s toggle", settingName))
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Apply the toggle function
	toggleFunc(user)

	err = sh.firestoreService.CreateOrUpdateUser(ctx, user)
	if err != nil {
		log.Error(ctx, fmt.Sprintf("Failed to update user %s settings", settingName), "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Build log fields using the provided function
	logFields := logFieldsFunc(user)
	log.Info(ctx, fmt.Sprintf("User %s settings updated", settingName), logFields)

	// Refresh the home view to show the updated state
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// handleManageChannelTrackingAction opens the channel tracking management modal.
func (sh *SlackHandler) handleManageChannelTrackingAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
		"team_id": teamID,
	})

	// Get current channel configurations for the workspace
	configs, err := sh.firestoreService.ListChannelConfigs(ctx, teamID)
	if err != nil {
		log.Error(ctx, "Failed to list channel configs", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Build the tracking modal with current configs
	modalView := sh.slackService.BuildChannelTrackingModal(configs)

	_, err = sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open channel tracking modal", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleChannelTrackingSelection processes channel selection from the tracking modal.
func (sh *SlackHandler) handleChannelTrackingSelection(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	userID := interaction.User.ID
	teamID := interaction.Team.ID

	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
		"team_id": teamID,
	})

	// Extract selected channel from the view submission
	channelID := ""
	if values, ok := interaction.View.State.Values["channel_tracking_input"]; ok {
		if channelSelect, ok := values["tracking_channel_select"]; ok {
			channelID = channelSelect.SelectedChannel
		}
	}

	if channelID == "" {
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"channel_tracking_input": "Please select a channel.",
			},
		})
		return
	}

	// Get channel name
	channelName, err := sh.slackService.GetChannelName(ctx, teamID, channelID)
	if err != nil {
		log.Error(ctx, "Failed to get channel name", "error", err, "channel_id", channelID)
		channelName = channelID // Fallback to ID if name lookup fails
	}

	// Get current config for this channel if it exists
	currentConfig, err := sh.firestoreService.GetChannelConfig(ctx, teamID, channelID)
	if err != nil {
		log.Error(ctx, "Failed to get channel config", "error", err)
	}

	// Default to enabled if no config exists
	currentlyEnabled := true
	if currentConfig != nil {
		currentlyEnabled = currentConfig.ManualTrackingEnabled
	}

	// Build the configuration modal for the selected channel
	configModal := sh.slackService.BuildChannelTrackingConfigModal(channelID, channelName, currentlyEnabled)

	// Push the configuration modal as a new view
	c.JSON(http.StatusOK, map[string]interface{}{
		"response_action": "push",
		"view":            configModal,
	})
}

// handleSaveChannelTracking saves the channel tracking configuration.
func (sh *SlackHandler) handleSaveChannelTracking(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	userID := interaction.User.ID
	teamID := interaction.Team.ID
	channelID := interaction.View.PrivateMetadata // Channel ID stored in private metadata

	ctx = log.WithFields(ctx, log.LogFields{
		"user_id":    userID,
		"team_id":    teamID,
		"channel_id": channelID,
	})

	// Extract tracking enabled setting
	trackingEnabled := true // Default to enabled
	if values, ok := interaction.View.State.Values["tracking_enabled_input"]; ok {
		if radioButtons, ok := values["tracking_enabled_radio"]; ok {
			if radioButtons.SelectedOption.Value != "" {
				trackingEnabled = radioButtons.SelectedOption.Value == "true"
			}
		}
	}

	// Get channel name for the config
	channelName, err := sh.slackService.GetChannelName(ctx, teamID, channelID)
	if err != nil {
		log.Error(ctx, "Failed to get channel name", "error", err)
		channelName = channelID // Fallback to ID
	}

	// Create or update the channel config
	config := &models.ChannelConfig{
		ID:                    teamID + "#" + channelID,
		SlackTeamID:           teamID,
		SlackChannelID:        channelID,
		SlackChannelName:      channelName,
		ManualTrackingEnabled: trackingEnabled,
		ConfiguredBy:          userID,
	}

	err = sh.firestoreService.SaveChannelConfig(ctx, config)
	if err != nil {
		log.Error(ctx, "Failed to save channel config", "error", err)
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors": map[string]string{
				"save_error": "Failed to save configuration. Please try again.",
			},
		})
		return
	}

	log.Info(ctx, "Channel tracking configuration saved",
		"tracking_enabled", trackingEnabled,
		"channel_name", channelName)

	// Close the modal with success
	c.JSON(http.StatusOK, gin.H{
		"response_action": "clear",
	})

	// Refresh the home view to show updated state
	sh.refreshHomeView(ctx, userID)
}

func (sh *SlackHandler) verifySignature(header http.Header, body []byte) error {
	if sh.signingSecret == "" {
		log.Warn(context.Background(), "Slack signing secret is empty, skipping signature verification")
		return nil
	}

	// Check for required headers
	signature := header.Get("X-Slack-Signature")
	timestamp := header.Get("X-Slack-Request-Timestamp")

	if signature == "" {
		log.Error(context.Background(), "Missing X-Slack-Signature header")
		return ErrMissingSlackSignature
	}

	if timestamp == "" {
		log.Error(context.Background(), "Missing X-Slack-Request-Timestamp header")
		return ErrMissingSlackTimestamp
	}

	sv, err := slack.NewSecretsVerifier(header, sh.signingSecret)
	if err != nil {
		log.Error(context.Background(), "Failed to create secrets verifier",
			"error", err,
			"has_signature", signature != "",
			"has_timestamp", timestamp != "",
		)
		return fmt.Errorf("failed to create secrets verifier: %w", err)
	}

	if _, err := sv.Write(body); err != nil {
		log.Error(context.Background(), "Failed to write body to verifier",
			"error", err,
			"body_length", len(body),
		)
		return fmt.Errorf("failed to write body to verifier: %w", err)
	}

	if err := sv.Ensure(); err != nil {
		log.Error(context.Background(), "Signature verification failed",
			"error", err,
			"signature", signature, // Full signature is safe to log
			"timestamp", timestamp,
			"body_length", len(body),
		)
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// ProcessManualPRLinkJob processes a manual PR link job from the job system.
func (sh *SlackHandler) ProcessManualPRLinkJob(ctx context.Context, job *models.Job) error {
	// Parse the ManualLinkJob from the job payload
	var manualLinkJob models.ManualLinkJob
	if err := json.Unmarshal(job.Payload, &manualLinkJob); err != nil {
		log.Error(ctx, "Failed to unmarshal manual link job from job payload",
			"error", err,
			"job_id", job.ID,
		)
		return fmt.Errorf("failed to unmarshal manual link job: %w", err)
	}

	// Validate the manual link job
	if err := manualLinkJob.Validate(); err != nil {
		log.Error(ctx, "Invalid manual link job payload",
			"error", err,
			"job_id", job.ID,
		)
		return fmt.Errorf("invalid manual link job: %w", err)
	}

	// Create TrackedMessage for this manual PR link
	trackedMessage := &models.TrackedMessage{
		PRNumber:       manualLinkJob.PRNumber,
		RepoFullName:   manualLinkJob.RepoFullName,
		SlackChannel:   manualLinkJob.SlackChannel,
		SlackMessageTS: manualLinkJob.SlackMessageTS,
		SlackTeamID:    manualLinkJob.SlackTeamID,
		MessageSource:  "manual",
	}

	log.Debug(ctx, "Creating tracked message for manual PR link")
	err := sh.firestoreService.CreateTrackedMessage(ctx, trackedMessage)
	if err != nil {
		log.Error(ctx, "Failed to create tracked message for manual PR link", "error", err)
		return err
	}

	log.Info(ctx, "Manual PR link tracked successfully",
		"repo", manualLinkJob.RepoFullName,
		"pr_number", manualLinkJob.PRNumber,
		"slack_channel", manualLinkJob.SlackChannel,
		"slack_team_id", manualLinkJob.SlackTeamID,
		"message_ts", manualLinkJob.SlackMessageTS)

	// Enqueue a reaction sync job to sync initial reactions for this PR
	reactionSyncJobID := uuid.New().String()
	reactionSyncJob := &models.ReactionSyncJob{
		ID:           reactionSyncJobID,
		PRNumber:     manualLinkJob.PRNumber,
		RepoFullName: manualLinkJob.RepoFullName,
		TraceID:      manualLinkJob.TraceID,
	}

	// Marshal the ReactionSyncJob as the payload for the Job
	jobPayload, err := json.Marshal(reactionSyncJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal reaction sync job", "error", err)
		// Don't fail the manual link job - reactions are a best-effort feature
		return nil
	}

	// Create Job
	syncJob := &models.Job{
		ID:      reactionSyncJobID,
		Type:    models.JobTypeReactionSync,
		TraceID: manualLinkJob.TraceID,
		Payload: jobPayload,
	}

	// Enqueue the reaction sync job
	if err := sh.cloudTasksService.EnqueueJob(ctx, syncJob); err != nil {
		log.Error(ctx, "Failed to enqueue reaction sync job for manual PR link", "error", err)
		// Don't fail the manual link job - reactions are a best-effort feature
		return nil
	}

	log.Info(ctx, "Enqueued reaction sync job for manual PR link",
		"reaction_sync_job_id", reactionSyncJobID)

	return nil
}
