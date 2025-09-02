package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
		case *slackevents.ReactionAddedEvent:
			sh.handleReactionAddedEvent(ctx, ev, eventsAPIEvent.TeamID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleMessageEvent processes Slack message events to detect and track GitHub PR links.
// Skips bot messages, edited messages, and channels with disabled tracking. Queues manual PR link jobs for processing.
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

// handleReactionAddedEvent processes reaction_added events to detect wastebasket emoji for message deletion.
// Only processes wastebasket reactions on bot messages from tracked PR notifications.
func (sh *SlackHandler) handleReactionAddedEvent(ctx context.Context, event *slackevents.ReactionAddedEvent, teamID string) {
	// Only handle wastebasket emoji reactions
	if event.Reaction != "wastebasket" {
		return
	}

	log.Info(ctx, "Wastebasket reaction detected",
		"user", event.User,
		"channel", event.Item.Channel,
		"message_ts", event.Item.Timestamp)

	// Look up the tracked message to see if this is a bot message we should handle
	trackedMessage, err := sh.firestoreService.GetTrackedMessageBySlackMessage(ctx, teamID, event.Item.Channel, event.Item.Timestamp)
	if err != nil {
		log.Error(ctx, "Failed to lookup tracked message for wastebasket reaction",
			"error", err,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	if trackedMessage == nil {
		log.Debug(ctx, "Wastebasket reaction not on tracked message, ignoring",
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	// Only handle bot messages
	if trackedMessage.MessageSource != models.MessageSourceBot {
		log.Debug(ctx, "Wastebasket reaction on manual message, ignoring",
			"message_source", trackedMessage.MessageSource,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	// Skip if already deleted by user
	if trackedMessage.DeletedByUser {
		log.Debug(ctx, "Message already deleted by user",
			"tracked_message_id", trackedMessage.ID,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	// Check if PR author GitHub ID is set (should be for bot messages)
	if trackedMessage.PRAuthorGitHubID == nil {
		log.Warn(ctx, "Bot message missing PR author GitHub ID, cannot authorize deletion",
			"tracked_message_id", trackedMessage.ID,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	// Check if the user who added the reaction is the PR author
	user, err := sh.firestoreService.GetUserBySlackID(ctx, event.User)
	if err != nil {
		log.Error(ctx, "Failed to lookup user for wastebasket reaction authorization",
			"error", err,
			"slack_user_id", event.User,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	if user == nil {
		log.Info(ctx, "User not found for wastebasket reaction, deletion denied",
			"slack_user_id", event.User,
			"pr_author_github_id", *trackedMessage.PRAuthorGitHubID,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	// Check if the user's GitHub ID matches the PR author
	if user.GitHubUserID != *trackedMessage.PRAuthorGitHubID {
		log.Info(ctx, "User is not PR author, deletion denied",
			"slack_user_id", event.User,
			"user_github_id", user.GitHubUserID,
			"pr_author_github_id", *trackedMessage.PRAuthorGitHubID,
			"channel", event.Item.Channel,
			"message_ts", event.Item.Timestamp)
		return
	}

	log.Info(ctx, "PR author authorized for message deletion",
		"slack_user_id", event.User,
		"github_user_id", user.GitHubUserID,
		"tracked_message_id", trackedMessage.ID)

	// Queue deletion job
	jobID := uuid.New().String()
	traceID := uuid.New().String()

	deleteJob := &models.DeleteTrackedMessageJob{
		ID:               jobID,
		TrackedMessageID: trackedMessage.ID,
		SlackChannel:     event.Item.Channel,
		SlackMessageTS:   event.Item.Timestamp,
		SlackTeamID:      teamID,
		TraceID:          traceID,
	}

	// Marshal the DeleteTrackedMessageJob as the payload for the Job
	jobPayload, err := json.Marshal(deleteJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal delete tracked message job", "error", err)
		return
	}

	// Create Job
	job := &models.Job{
		ID:      jobID,
		Type:    models.JobTypeDeleteTrackedMessage,
		TraceID: traceID,
		Payload: jobPayload,
	}

	// Queue for async processing
	err = sh.cloudTasksService.EnqueueJob(ctx, job)
	if err != nil {
		log.Error(ctx, "Failed to enqueue message deletion job", "error", err)
	} else {
		log.Info(ctx, "Message deletion job queued",
			"job_id", jobID,
			"tracked_message_id", trackedMessage.ID,
			"repo", trackedMessage.RepoFullName,
			"pr_number", trackedMessage.PRNumber)
	}
}

// HandleInteraction processes interactive component actions from Slack.
// Handles block actions, view submissions, and other interaction types from App Home and modals.
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

// handleBlockAction processes block action interactions from Slack UI components.
// Routes different action types to appropriate handler methods based on action_id.
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
	case "install_github_app":
		sh.handleInstallGitHubAppFromHomeAction(ctx, userID, teamID, interaction.TriggerID, c)
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
	case "toggle_impersonation":
		sh.handleToggleImpersonationAction(ctx, userID, c)
	case "manage_github_installations":
		sh.handleManageGitHubInstallationsAction(ctx, userID, teamID, interaction.TriggerID, c)
	case "add_github_installation":
		sh.handleAddGitHubInstallationFromModalAction(ctx, userID, teamID, interaction.TriggerID, c)
	case "configure_pr_size_emojis":
		sh.handleConfigurePRSizeEmojisAction(ctx, userID, teamID, interaction.TriggerID, c)
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleViewSubmission processes view submission interactions from Slack modals.
// Routes submissions to appropriate handlers based on callback_id.
func (sh *SlackHandler) handleViewSubmission(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	switch interaction.View.CallbackID {
	case "channel_selector":
		sh.handleChannelSelection(ctx, interaction, c)
	case "channel_tracking_selector":
		sh.handleChannelTrackingSelection(ctx, interaction, c)
	case "save_channel_tracking":
		sh.handleSaveChannelTracking(ctx, interaction, c)
	case "pr_size_config":
		sh.handlePRSizeConfigSubmission(ctx, interaction, c)
	default:
		log.Warn(ctx, "Unknown view submission callback ID",
			"callback_id", interaction.View.CallbackID)
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleAppHomeOpened processes app_home_opened events when users visit the App Home tab.
// Fetches user data and GitHub installations, then builds and publishes the home view.
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

	// Get GitHub installations for this workspace
	installations, err := sh.firestoreService.GetGitHubInstallationsByWorkspace(ctx, teamID)
	if err != nil {
		log.Error(ctx, "Failed to get GitHub installations for App Home", "error", err)
		installations = nil
	}
	hasInstallations := len(installations) > 0

	// Build and publish home view
	view := sh.slackService.BuildHomeView(user, hasInstallations, installations)
	err = sh.slackService.PublishHomeView(ctx, teamID, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to publish App Home view", "error", err)
	}
}

// handleConnectGitHubAction handles the "Connect GitHub Account" button from App Home.
// Creates OAuth state, marks it for home return, and opens OAuth modal with GitHub link.
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

// handleInstallGitHubAppFromHomeAction handles the "Install GitHub App" button from App Home.
// Delegates to shared GitHub App installation handler with appropriate context.
func (sh *SlackHandler) handleInstallGitHubAppFromHomeAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	sh.handleGitHubAppInstallation(ctx, userID, teamID, triggerID, false, "install_github_app", c)
}

// handleAddGitHubInstallationFromModalAction handles the "Add new installation" button from within a modal.
// Delegates to shared GitHub App installation handler with modal context.
func (sh *SlackHandler) handleAddGitHubInstallationFromModalAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	sh.handleGitHubAppInstallation(ctx, userID, teamID, triggerID, true, "add_github_installation", c)
}

// handleGitHubAppInstallation provides shared implementation for GitHub App installation flows.
// Creates OAuth state, generates installation URL, and opens appropriate modal based on context.
func (sh *SlackHandler) handleGitHubAppInstallation(
	ctx context.Context, userID, teamID, triggerID string, fromModal bool, errorKey string, c *gin.Context,
) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
		"team_id": teamID,
	})

	if fromModal {
		log.Info(ctx, "User initiated GitHub App installation from modal")
	} else {
		log.Info(ctx, "User initiated GitHub App installation from Slack")
	}

	// Create OAuth state for combined OAuth + installation flow
	state, err := sh.githubAuthService.CreateOAuthState(ctx, userID, teamID, "")
	if err != nil {
		log.Error(ctx, "Failed to create OAuth state for GitHub App installation", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				errorKey: "Failed to initiate GitHub App installation. Please try again.",
			},
		})
		return
	}

	// Set return to home for App Home refresh after installation
	state.ReturnToHome = true
	err = sh.githubAuthService.SaveOAuthState(ctx, state)
	if err != nil {
		log.Error(ctx, "Failed to update OAuth state for GitHub App installation", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				errorKey: "Failed to initiate GitHub App installation. Please try again.",
			},
		})
		return
	}

	// Generate GitHub App installation URL (will trigger combined OAuth + installation flow)
	oauthURL := sh.githubAuthService.GetAppInstallationURL(state.ID)

	if fromModal {
		log.Info(ctx, "Generated GitHub OAuth URL for App installation from modal", "state_id", state.ID)
	} else {
		log.Info(ctx, "Generated GitHub OAuth URL for App installation", "state_id", state.ID)
	}

	// Open a modal with the GitHub installation link
	modalView := sh.slackService.BuildGitHubInstallationModal(oauthURL)

	if fromModal {
		// Push a new view onto the modal stack
		_, err = sh.slackService.PushView(ctx, teamID, triggerID, modalView)
		if err != nil {
			log.Error(ctx, "Failed to push GitHub installation modal", "error", err)
		}
	} else {
		// Open a modal from App Home
		_, err = sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
		if err != nil {
			log.Error(ctx, "Failed to open GitHub installation modal", "error", err)
		}
	}

	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				errorKey: "Failed to open installation modal. Please try again.",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleManageGitHubInstallationsAction handles the "Manage GitHub Installations" button.
// Fetches workspace installations and opens management modal with installation list.
func (sh *SlackHandler) handleManageGitHubInstallationsAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
		"team_id": teamID,
	})

	log.Info(ctx, "User opened GitHub installations management modal")

	// Get GitHub installations for this workspace
	installations, err := sh.firestoreService.GetGitHubInstallationsByWorkspace(ctx, teamID)
	if err != nil {
		log.Error(ctx, "Failed to get GitHub installations for modal", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"github_installations": "Failed to load GitHub installations. Please try again.",
			},
		})
		return
	}

	// Build and open the installations management modal
	modalView := sh.slackService.BuildGitHubInstallationsModal(installations, sh.config.BaseURL, sh.config.GitHubAppSlug)

	_, err = sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open GitHub installations modal", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"github_installations": "Failed to open installations modal. Please try again.",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDisconnectGitHubAction handles the "Disconnect GitHub Account" button.
// Removes GitHub connection from user record and refreshes App Home view.
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

// handleSelectChannelAction opens a modal for default channel selection.
// Creates and displays channel selector modal for user's notification preferences.
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

// extractChannelSelection extracts the selected channel ID from modal interaction state.
// Returns empty string if no valid channel selection is found.
func (sh *SlackHandler) extractChannelSelection(interaction *slack.InteractionCallback) string {
	if values, ok := interaction.View.State.Values["channel_input"]; ok {
		if channelSelect, ok := values["channel_select"]; ok {
			return channelSelect.SelectedChannel
		}
	}
	return ""
}

// validateChannelSelection validates the selected channel and returns user-friendly error message.
// Checks channel accessibility, type restrictions, and bot permissions.
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

// createOrGetUserWithDisplayName creates new user or retrieves existing one with Slack display name.
// Fetches display name from Slack API for new users and sets default preferences.
func (sh *SlackHandler) createOrGetUserWithDisplayName(ctx context.Context, userID, teamID string) (*models.User, error) {
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if user == nil {
		user = &models.User{
			ID:                   userID,
			SlackTeamID:          teamID,
			NotificationsEnabled: true,             // Default to enabled for new users
			TaggingEnabled:       true,             // Default to enabled for new users
			ImpersonationEnabled: &[]bool{true}[0], // Default to enabled for new users
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

// handleChannelSelection processes channel selection submission from modal.
// Validates selected channel, updates user's default channel preference, and refreshes App Home.
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

// handleRefreshViewAction handles the refresh button action from App Home.
// Triggers immediate refresh of the user's App Home view with current data.
func (sh *SlackHandler) handleRefreshViewAction(ctx context.Context, userID string, c *gin.Context) {
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// refreshHomeView refreshes the App Home view for a specific user.
// Fetches current user data and GitHub installations, then publishes updated home view.
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

	// Get GitHub installations for this workspace
	installations, err := sh.firestoreService.GetGitHubInstallationsByWorkspace(ctx, user.SlackTeamID)
	if err != nil {
		log.Error(ctx, "Failed to get GitHub installations for refresh", "error", err)
		installations = nil
	}
	hasInstallations := len(installations) > 0

	view := sh.slackService.BuildHomeView(user, hasInstallations, installations)
	err = sh.slackService.PublishHomeView(ctx, user.SlackTeamID, userID, view)
	if err != nil {
		log.Error(ctx, "Failed to refresh App Home view", "error", err)
	}
}

// handleToggleNotificationsAction handles the notifications enable/disable toggle.
// Updates user's notification preferences and refreshes App Home view.
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
// Updates user's tagging preferences for PR notifications and refreshes App Home view.
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

// handleToggleImpersonationAction handles the impersonation enable/disable toggle.
// Updates user's impersonation preferences for PR notifications and refreshes App Home view.
func (sh *SlackHandler) handleToggleImpersonationAction(ctx context.Context, userID string, c *gin.Context) {
	sh.handleUserSettingToggle(ctx, userID, c, "impersonation", func(user *models.User) {
		currentValue := user.GetImpersonationEnabled()
		newValue := !currentValue
		user.ImpersonationEnabled = &newValue
	}, func(user *models.User) map[string]interface{} {
		return map[string]interface{}{
			"impersonation_enabled": user.GetImpersonationEnabled(),
			"github_username":       user.GitHubUsername,
		}
	})
}

// handleUserSettingToggle provides common implementation for user setting toggles.
// Applies toggle function, saves user changes, logs update, and refreshes App Home view.
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
// Fetches current channel configurations and displays tracking management interface.
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
// Extracts selected channel, gets current config, and pushes configuration modal to stack.
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

// handleSaveChannelTracking saves the channel tracking configuration from modal submission.
// Creates or updates channel config with tracking preference and closes modal with success.
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

// verifySignature verifies Slack request signature using HMAC-SHA256.
// Validates X-Slack-Signature and X-Slack-Request-Timestamp headers against signing secret.
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
// Creates tracked message for manual PR link and enqueues reaction sync job for initial state.
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

	// Resolve channel name to ID if needed (though should already be ID from Slack events)
	channelID, err := sh.slackService.ResolveChannelID(ctx, manualLinkJob.SlackTeamID, manualLinkJob.SlackChannel)
	if err != nil {
		log.Error(ctx, "Failed to resolve channel for manual PR link",
			"error", err,
			"channel", manualLinkJob.SlackChannel,
			"team_id", manualLinkJob.SlackTeamID)
		return fmt.Errorf("failed to resolve channel %s: %w", manualLinkJob.SlackChannel, err)
	}

	// Create TrackedMessage for this manual PR link
	trackedMessage := &models.TrackedMessage{
		PRNumber:         manualLinkJob.PRNumber,
		RepoFullName:     manualLinkJob.RepoFullName,
		SlackChannel:     channelID,
		SlackChannelName: manualLinkJob.SlackChannel, // Store original for logging if it was a name
		SlackMessageTS:   manualLinkJob.SlackMessageTS,
		SlackTeamID:      manualLinkJob.SlackTeamID,
		MessageSource:    models.MessageSourceManual,
	}

	log.Debug(ctx, "Creating tracked message for manual PR link")
	err = sh.firestoreService.CreateTrackedMessage(ctx, trackedMessage)
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

// handleConfigurePRSizeEmojisAction handles the "Configure PR emojis" button.
// Opens the PR size emoji configuration modal.
func (sh *SlackHandler) handleConfigurePRSizeEmojisAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context) {
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
		"team_id": teamID,
	})

	log.Info(ctx, "User opened PR size emoji configuration modal")

	// Get user data to populate current configuration
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user data for PR size config modal", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"pr_size_config": "Failed to load your settings. Please try again.",
			},
		})
		return
	}

	// Build and open the PR size configuration modal
	modalView := sh.slackService.BuildPRSizeConfigModal(user)

	_, err = sh.slackService.OpenView(ctx, teamID, triggerID, modalView)
	if err != nil {
		log.Error(ctx, "Failed to open PR size config modal", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"pr_size_config": "Failed to open configuration modal. Please try again.",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handlePRSizeConfigSubmission handles the submission of PR size emoji configuration modal.
// Parses and validates the configuration, then saves it to the user's settings.
func (sh *SlackHandler) handlePRSizeConfigSubmission(ctx context.Context, interaction *slack.InteractionCallback, c *gin.Context) {
	userID := interaction.User.ID
	ctx = log.WithFields(ctx, log.LogFields{
		"user_id": userID,
	})

	log.Info(ctx, "Processing PR size emoji configuration submission")

	// Extract the configuration text from the modal
	configText := extractTextInput(interaction, "pr_size_config_input", "pr_size_config_text")
	if configText == "" {
		log.Info(ctx, "Empty configuration submitted - disabling custom emoji config")
	}

	// Parse and validate the configuration
	prSizeConfig, errors := sh.parsePRSizeConfig(configText)
	if len(errors) > 0 {
		log.Warn(ctx, "Invalid PR size configuration submitted",
			"errors", errors,
			"config_text", configText)
		c.JSON(http.StatusOK, map[string]interface{}{
			"response_action": "errors",
			"errors":          errors,
		})
		return
	}

	// Get user data
	user, err := sh.firestoreService.GetUserBySlackID(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get user for PR size config save", "error", err)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	if user == nil {
		log.Error(ctx, "User not found for PR size config save")
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// Update user's PR size configuration
	user.PRSizeConfig = prSizeConfig
	err = sh.firestoreService.SaveUser(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to save PR size configuration", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"response_action": "errors",
			"errors": map[string]string{
				"pr_size_config_input": "Failed to save configuration. Please try again.",
			},
		})
		return
	}

	if prSizeConfig != nil && prSizeConfig.Enabled {
		log.Info(ctx, "Saved custom PR size emoji configuration",
			"threshold_count", len(prSizeConfig.Thresholds))
	} else {
		log.Info(ctx, "Disabled custom PR size emoji configuration")
	}

	// Refresh the home view to show updated configuration
	sh.refreshHomeView(ctx, userID)
	c.JSON(http.StatusOK, gin.H{})
}

// parsePRSizeConfig parses and validates PR size emoji configuration from text input.
// Returns the parsed configuration or validation errors.
func (sh *SlackHandler) parsePRSizeConfig(configText string) (*models.PRSizeConfiguration, map[string]string) {
	configText = strings.TrimSpace(configText)

	// If empty, disable custom configuration
	if configText == "" {
		return &models.PRSizeConfiguration{Enabled: false}, nil
	}

	lines := strings.Split(configText, "\n")
	const maxExpectedThresholds = 10
	thresholds := make([]models.PRSizeThreshold, 0, maxExpectedThresholds) // Pre-allocate with reasonable capacity
	errors := make(map[string]string)

	lineNum := 0
	lastMaxLines := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // Skip empty lines
		}

		lineNum++
		parts := strings.Fields(line)
		const expectedParts = 2
		if len(parts) != expectedParts {
			errors["pr_size_config_input"] = fmt.Sprintf("Line %d: Format must be 'emoji max_lines' (e.g., ':ant: 5')", lineNum)
			return nil, errors
		}

		emoji := parts[0]
		maxLinesStr := parts[1]

		// Validate emoji format
		if !sh.isValidEmoji(emoji) {
			errors["pr_size_config_input"] = fmt.Sprintf("Line %d: Invalid emoji format. Use ':emoji_name:' or Unicode emoji", lineNum)
			return nil, errors
		}

		// Parse and validate max lines
		maxLines, err := strconv.Atoi(maxLinesStr)
		if err != nil || maxLines <= 0 {
			errors["pr_size_config_input"] = fmt.Sprintf("Line %d: Max lines must be a positive number", lineNum)
			return nil, errors
		}

		// Validate ascending order
		if maxLines <= lastMaxLines {
			errors["pr_size_config_input"] = fmt.Sprintf(
				"Line %d: Max lines (%d) must be greater than previous (%d)",
				lineNum, maxLines, lastMaxLines,
			)
			return nil, errors
		}

		thresholds = append(thresholds, models.PRSizeThreshold{
			MaxLines: maxLines,
			Emoji:    emoji,
		})

		lastMaxLines = maxLines
	}

	if len(thresholds) == 0 {
		return &models.PRSizeConfiguration{Enabled: false}, nil
	}

	return &models.PRSizeConfiguration{
		Enabled:    true,
		Thresholds: thresholds,
	}, nil
}

// isValidEmoji checks if a string is a valid emoji (either :emoji_name: format or Unicode emoji).
func (sh *SlackHandler) isValidEmoji(emoji string) bool {
	// Check for :emoji_name: format
	if strings.HasPrefix(emoji, ":") && strings.HasSuffix(emoji, ":") && len(emoji) > 2 {
		emojiName := strings.Trim(emoji, ":")
		if emojiName != "" {
			return true
		}
	}

	// Check for Unicode emoji using a simple regex
	// This matches most common Unicode emoji ranges
	emojiRegex := regexp.MustCompile(
		`^[\x{1F600}-\x{1F64F}]|[\x{1F300}-\x{1F5FF}]|[\x{1F680}-\x{1F6FF}]|` +
			`[\x{1F1E0}-\x{1F1FF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]$`,
	)
	return emojiRegex.MatchString(emoji)
}

// ProcessDeleteTrackedMessageJob processes a job to delete a tracked message.
// Deletes the Slack message and marks the tracked message as deleted by user.
func (sh *SlackHandler) ProcessDeleteTrackedMessageJob(ctx context.Context, job *models.Job) error {
	// Parse the DeleteTrackedMessageJob from the job payload
	var deleteJob models.DeleteTrackedMessageJob
	if err := json.Unmarshal(job.Payload, &deleteJob); err != nil {
		log.Error(ctx, "Failed to unmarshal delete tracked message job from job payload",
			"error", err,
			"job_id", job.ID,
		)
		return fmt.Errorf("failed to unmarshal delete tracked message job: %w", err)
	}

	// Validate the delete job
	if err := deleteJob.Validate(); err != nil {
		log.Error(ctx, "Invalid delete tracked message job payload",
			"error", err,
			"job_id", job.ID,
		)
		return fmt.Errorf("invalid delete tracked message job: %w", err)
	}

	// Add context for logging
	ctx = log.WithFields(ctx, log.LogFields{
		"tracked_message_id": deleteJob.TrackedMessageID,
		"slack_channel":      deleteJob.SlackChannel,
		"slack_message_ts":   deleteJob.SlackMessageTS,
		"slack_team_id":      deleteJob.SlackTeamID,
	})

	log.Info(ctx, "Processing tracked message deletion job")

	// Delete the Slack message
	err := sh.slackService.DeleteMessage(ctx, deleteJob.SlackTeamID, deleteJob.SlackChannel, deleteJob.SlackMessageTS)
	if err != nil {
		log.Error(ctx, "Failed to delete Slack message", "error", err)
		return fmt.Errorf("failed to delete Slack message: %w", err)
	}

	// Mark the tracked message as deleted by user
	err = sh.firestoreService.MarkTrackedMessageDeleted(ctx, deleteJob.TrackedMessageID)
	if err != nil {
		log.Error(ctx, "Failed to mark tracked message as deleted", "error", err)
		return fmt.Errorf("failed to mark tracked message as deleted: %w", err)
	}

	log.Info(ctx, "Successfully processed message deletion job")
	return nil
}

// extractTextInput extracts text input from modal interaction state.
// Returns empty string if no valid text input is found.
func extractTextInput(interaction *slack.InteractionCallback, blockID, actionID string) string {
	if values, ok := interaction.View.State.Values[blockID]; ok {
		if textInput, ok := values[actionID]; ok {
			return textInput.Value
		}
	}
	return ""
}
