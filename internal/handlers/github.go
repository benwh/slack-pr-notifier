package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github-slack-notifier/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v73/github"
	"github.com/google/uuid"
)

var (
	ErrUnsupportedEventType = errors.New("unsupported event type")
	ErrMissingAction        = errors.New("missing required field: action")
	ErrMissingRepository    = errors.New("missing required field: repository")
)

const (
	PRActionOpened             = "opened"
	PRActionEdited             = "edited"
	PRActionClosed             = "closed"
	PRActionReadyForReview     = "ready_for_review"
	PRReviewActionSubmitted    = "submitted"
	PRReviewActionDismissed    = "dismissed"
	EventTypePullRequest       = "pull_request"
	EventTypePullRequestReview = "pull_request_review"
)

// GitHubWebhookPayload represents the structure of GitHub webhook events.
type GitHubWebhookPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		Draft     bool   `json:"draft"`
		HTMLURL   string `json:"html_url"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		User      struct {
			ID    int    `json:"id"`
			Login string `json:"login"`
		} `json:"user"`
		Merged bool `json:"merged"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
	} `json:"repository"`
	Review struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"review"`
}

// CloudTasksServiceInterface defines the interface for cloud tasks operations.
type CloudTasksServiceInterface interface {
	EnqueueJob(ctx context.Context, job *models.Job) error
}

type GitHubHandler struct {
	cloudTasksService CloudTasksServiceInterface
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	webhookSecret     string
	emojiConfig       config.EmojiConfig
}

func NewGitHubHandler(
	cloudTasksService CloudTasksServiceInterface,
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
	webhookSecret string,
	emojiConfig config.EmojiConfig,
) *GitHubHandler {
	return &GitHubHandler{
		cloudTasksService: cloudTasksService,
		firestoreService:  firestoreService,
		slackService:      slackService,
		webhookSecret:     webhookSecret,
		emojiConfig:       emojiConfig,
	}
}

func (h *GitHubHandler) HandleWebhook(c *gin.Context) {
	startTime := time.Now()
	traceID := c.GetString("trace_id")

	eventType := c.GetHeader("X-Github-Event")
	deliveryID := c.GetHeader("X-Github-Delivery")

	ctx := c.Request.Context()
	// Add request metadata to context for all log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"trace_id":        traceID,
		"remote_addr":     c.ClientIP(),
		"user_agent":      c.Request.UserAgent(),
		"github_event":    eventType,
		"github_delivery": deliveryID,
	})

	if eventType == "" || deliveryID == "" {
		log.Error(ctx, "Missing required headers")
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required headers"})
		return
	}

	// Use go-github library to validate payload and signature
	var secretToken []byte
	if h.webhookSecret != "" {
		secretToken = []byte(h.webhookSecret)
	}

	payload, err := github.ValidatePayload(c.Request, secretToken)
	if err != nil {
		log.Error(ctx, "Invalid webhook payload or signature", "error", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid payload or signature"})
		return
	}

	if err := h.validateWebhookPayload(eventType, payload); err != nil {
		log.Error(ctx, "Invalid webhook payload", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Create WebhookJob for the payload
	webhookJob := &models.WebhookJob{
		ID:         uuid.New().String(),
		EventType:  eventType,
		DeliveryID: deliveryID,
		TraceID:    traceID,
		Payload:    payload,
		ReceivedAt: time.Now(),
		Status:     "queued",
		RetryCount: 0,
	}

	// Marshal the WebhookJob as the payload for the Job
	jobPayload, err := json.Marshal(webhookJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal webhook job", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal job"})
		return
	}

	// Create Job
	job := &models.Job{
		ID:      webhookJob.ID,
		Type:    models.JobTypeGitHubWebhook,
		TraceID: traceID,
		Payload: jobPayload,
	}

	if err := h.cloudTasksService.EnqueueJob(ctx, job); err != nil {
		log.Error(ctx, "Failed to enqueue webhook", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to queue webhook"})
		return
	}

	processingTime := time.Since(startTime)
	log.Info(ctx, "Webhook queued successfully",
		"job_id", webhookJob.ID,
		"processing_time_ms", processingTime.Milliseconds(),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":             "queued",
		"job_id":             webhookJob.ID,
		"processing_time_ms": processingTime.Milliseconds(),
	})
}

func (h *GitHubHandler) validateWebhookPayload(eventType string, payload []byte) error {
	switch eventType {
	case "pull_request", "pull_request_review":
		return h.validateGitHubPayload(payload)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventType, eventType)
	}
}

func (h *GitHubHandler) validateGitHubPayload(payload []byte) error {
	var githubPayload map[string]interface{}
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}

	if _, exists := githubPayload["action"]; !exists {
		return ErrMissingAction
	}

	if _, exists := githubPayload["repository"]; !exists {
		return ErrMissingRepository
	}

	return nil
}

// ProcessWebhookJob processes a GitHub webhook job from the job system.
func (h *GitHubHandler) ProcessWebhookJob(ctx context.Context, job *models.Job) error {
	var webhookJob models.WebhookJob
	if err := json.Unmarshal(job.Payload, &webhookJob); err != nil {
		return fmt.Errorf("failed to unmarshal webhook job: %w", err)
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"event_type":     webhookJob.EventType,
		"delivery_id":    webhookJob.DeliveryID,
		"webhook_job_id": webhookJob.ID,
	})

	log.Debug(ctx, "Processing GitHub webhook job")

	switch webhookJob.EventType {
	case EventTypePullRequest:
		return h.processPullRequestEvent(ctx, webhookJob.Payload)
	case EventTypePullRequestReview:
		return h.processPullRequestReviewEvent(ctx, webhookJob.Payload)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventType, webhookJob.EventType)
	}
}

func (h *GitHubHandler) processPullRequestEvent(ctx context.Context, payload []byte) error {
	var githubPayload GitHubWebhookPayload
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal pull request payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number": githubPayload.PullRequest.Number,
		"repo":      githubPayload.Repository.FullName,
		"author":    githubPayload.PullRequest.User.Login,
		"pr_action": githubPayload.Action,
	})

	log.Info(ctx, "Handling pull request event",
		"is_draft", githubPayload.PullRequest.Draft,
	)

	switch githubPayload.Action {
	case PRActionOpened:
		return h.handlePROpened(ctx, &githubPayload)
	case PRActionEdited:
		return h.handlePREdited(ctx, &githubPayload)
	case PRActionReadyForReview:
		return h.handlePRReadyForReview(ctx, &githubPayload)
	case PRActionClosed:
		return h.handlePRClosed(ctx, &githubPayload)
	default:
		log.Warn(ctx, "Pull request action not handled")
		return nil
	}
}

func (h *GitHubHandler) processPullRequestReviewEvent(ctx context.Context, payload []byte) error {
	var githubPayload GitHubWebhookPayload
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request review payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal pull request review payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number":     githubPayload.PullRequest.Number,
		"repo":          githubPayload.Repository.FullName,
		"author":        githubPayload.PullRequest.User.Login,
		"reviewer":      githubPayload.Review.User.Login,
		"review_state":  githubPayload.Review.State,
		"review_action": githubPayload.Action,
	})

	if githubPayload.Action != PRReviewActionSubmitted && githubPayload.Action != PRReviewActionDismissed {
		return nil
	}

	// Get all tracked messages for this PR across all workspaces and channels
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, githubPayload.Repository.FullName, githubPayload.PullRequest.Number)
	if err != nil {
		log.Error(ctx, "Failed to get tracked messages for PR review reaction",
			"error", err,
		)
		return err
	}
	if len(trackedMessages) == 0 {
		log.Warn(ctx, "No tracked messages found for PR review reaction")
		return nil
	}

	// Convert tracked messages to message refs
	messageRefs := make([]services.MessageRef, len(trackedMessages))
	for i, msg := range trackedMessages {
		messageRefs[i] = services.MessageRef{
			Channel:   msg.SlackChannel,
			Timestamp: msg.SlackMessageTS,
		}
	}

	// Determine the current review state - for dismissed reviews, we remove all reactions
	var currentState string
	if githubPayload.Action == PRReviewActionDismissed {
		currentState = "" // Empty state will remove all review reactions
	} else {
		currentState = githubPayload.Review.State
	}

	// Sync all review reactions
	// Group message refs by team ID for proper team-scoped API calls
	messagesByTeam := make(map[string][]services.MessageRef)
	for i, msg := range trackedMessages {
		messagesByTeam[msg.SlackTeamID] = append(messagesByTeam[msg.SlackTeamID], messageRefs[i])
	}

	// Sync reactions for each team separately
	for teamID, teamMessageRefs := range messagesByTeam {
		err = h.slackService.SyncAllReviewReactions(ctx, teamID, teamMessageRefs, currentState)
		if err != nil {
			log.Error(ctx, "Failed to sync review reactions for team",
				"error", err,
				"team_id", teamID,
				"review_state", currentState,
				"review_action", githubPayload.Action,
				"message_count", len(teamMessageRefs),
			)
			// Continue with other teams even if one fails
		}
	}
	return nil
}

func (h *GitHubHandler) handlePROpened(ctx context.Context, payload *GitHubWebhookPayload) error {
	if payload.PullRequest.Draft {
		log.Debug(ctx, "Skipping draft PR")
		return nil
	}

	log.Debug(ctx, "Processing PR opened",
		"title", payload.PullRequest.Title,
	)

	authorUsername := payload.PullRequest.User.Login
	log.Debug(ctx, "Looking up user by GitHub username", "github_username", authorUsername)
	user, err := h.firestoreService.GetUserByGitHubUsername(ctx, authorUsername)
	if err != nil {
		log.Error(ctx, "Failed to lookup user by GitHub username",
			"error", err,
			"github_username", authorUsername,
		)
		return err
	}
	log.Debug(ctx, "User lookup result", "user_found", user != nil)

	// Parse PR directives from description
	annotatedChannel, directives := h.slackService.ExtractChannelAndDirectives(payload.PullRequest.Body)
	log.Debug(ctx, "Channel and directive determination",
		"annotated_channel", annotatedChannel,
		"skip", directives.Skip,
		"user_to_cc", directives.UserToCC)

	// Check if PR should be skipped
	if directives.Skip {
		log.Info(ctx, "Skipping PR notification due to skip directive")
		return nil
	}

	// Get all workspace configurations for this repository
	log.Debug(ctx, "Looking up repository configurations across all workspaces")
	repos, err := h.firestoreService.GetReposForAllWorkspaces(ctx, payload.Repository.FullName)
	if err != nil {
		log.Error(ctx, "Failed to lookup repository configurations",
			"error", err,
		)
		return err
	}

	if len(repos) == 0 {
		autoRegisteredRepo, err := h.attemptAutoRegistration(ctx, payload, user)
		if err != nil {
			return err
		}
		if autoRegisteredRepo == nil {
			return nil // Skip notification - no auto-registration possible
		}
		repos = []*models.Repo{autoRegisteredRepo}
	}

	log.Info(ctx, "Found repository configurations in workspace(s)",
		"workspace_count", len(repos))

	// Process notifications for each workspace
	for _, repo := range repos {
		err := h.processWorkspaceNotification(ctx, payload, repo, user, annotatedChannel, directives)
		if err != nil {
			log.Error(ctx, "Failed to process notification for workspace",
				"error", err,
				"slack_team_id", repo.WorkspaceID,
			)
			// Continue processing other workspaces even if one fails
		}
	}

	return nil
}

// determineTargetChannel determines the target Slack channel for notifications.
// Channel priority: annotated channel -> user default (if user is in same workspace and notifications enabled).
func (h *GitHubHandler) determineTargetChannel(
	ctx context.Context,
	repo *models.Repo,
	user *models.User,
	annotatedChannel string,
) string {
	if annotatedChannel != "" {
		log.Debug(ctx, "Using annotated channel from PR description",
			"channel", annotatedChannel,
			"slack_team_id", repo.WorkspaceID)
		return annotatedChannel
	}

	if user != nil && user.SlackTeamID == repo.WorkspaceID && user.DefaultChannel != "" && user.NotificationsEnabled {
		log.Debug(ctx, "Using user default channel",
			"channel", user.DefaultChannel,
			"slack_team_id", repo.WorkspaceID)
		return user.DefaultChannel
	}

	return ""
}

// checkForDuplicateBotMessage checks if a bot message already exists for this PR.
func (h *GitHubHandler) checkForDuplicateBotMessage(
	ctx context.Context,
	payload *GitHubWebhookPayload,
	targetChannel string,
	workspaceID string,
) (bool, error) {
	botMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.Repository.FullName, payload.PullRequest.Number, targetChannel, workspaceID, "bot")
	if err != nil {
		log.Error(ctx, "Failed to check for existing bot messages",
			"error", err,
			"slack_team_id", workspaceID)
		return false, err
	}

	if len(botMessages) > 0 {
		log.Info(ctx, "Bot message already exists for this PR in workspace channel, skipping duplicate notification",
			"channel", targetChannel,
			"slack_team_id", workspaceID,
			"existing_message_count", len(botMessages))
		return true, nil
	}

	return false, nil
}

// postAndTrackPRMessage posts a PR message to Slack and tracks it in the database.
func (h *GitHubHandler) postAndTrackPRMessage(
	ctx context.Context,
	payload *GitHubWebhookPayload,
	repo *models.Repo,
	user *models.User,
	targetChannel string,
	directives *services.PRDirectives,
) error {
	log.Info(ctx, "Posting PR message to Slack workspace",
		"channel", targetChannel,
		"slack_team_id", repo.WorkspaceID)

	// Calculate PR size (additions + deletions)
	prSize := payload.PullRequest.Additions + payload.PullRequest.Deletions

	// Get author's Slack user ID if they're in the same workspace
	var authorSlackUserID string
	if user != nil && user.SlackTeamID == repo.WorkspaceID && user.Verified {
		authorSlackUserID = user.ID
	}

	timestamp, err := h.slackService.PostPRMessage(
		ctx,
		repo.WorkspaceID,
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
		prSize,
		authorSlackUserID,
		directives.UserToCC,
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack workspace",
			"error", err,
			"channel", targetChannel,
			"slack_team_id", repo.WorkspaceID,
			"repo_name", payload.Repository.Name,
			"pr_title", payload.PullRequest.Title,
		)
		return err
	}
	log.Info(ctx, "Posted PR notification to Slack workspace",
		"channel", targetChannel,
		"slack_team_id", repo.WorkspaceID,
	)

	// Create TrackedMessage for the bot notification
	trackedMessage := &models.TrackedMessage{
		PRNumber:       payload.PullRequest.Number,
		RepoFullName:   payload.Repository.FullName,
		SlackChannel:   targetChannel,
		SlackMessageTS: timestamp,
		SlackTeamID:    repo.WorkspaceID,
		MessageSource:  "bot",
	}

	log.Debug(ctx, "Saving tracked message to database",
		"channel", trackedMessage.SlackChannel,
		"slack_team_id", repo.WorkspaceID)
	err = h.firestoreService.CreateTrackedMessage(ctx, trackedMessage)
	if err != nil {
		log.Error(ctx, "Failed to save tracked message to database",
			"error", err,
			"channel", trackedMessage.SlackChannel,
			"slack_team_id", repo.WorkspaceID,
			"message_ts", trackedMessage.SlackMessageTS,
		)
		return err
	}
	log.Debug(ctx, "Successfully saved tracked message to database")

	return nil
}

// processWorkspaceNotification handles PR notification for a specific workspace.
func (h *GitHubHandler) processWorkspaceNotification(
	ctx context.Context,
	payload *GitHubWebhookPayload,
	repo *models.Repo,
	user *models.User,
	annotatedChannel string,
	directives *services.PRDirectives,
) error {
	targetChannel := h.determineTargetChannel(ctx, repo, user, annotatedChannel)
	if targetChannel == "" {
		log.Debug(ctx, "No target channel determined for workspace, skipping",
			"slack_team_id", repo.WorkspaceID)
		return nil
	}

	// Check for duplicate bot messages
	isDuplicate, err := h.checkForDuplicateBotMessage(ctx, payload, targetChannel, repo.WorkspaceID)
	if err != nil {
		return err
	}
	if isDuplicate {
		return nil
	}

	// Post message and track it
	if err := h.postAndTrackPRMessage(ctx, payload, repo, user, targetChannel, directives); err != nil {
		return err
	}

	// After posting, synchronize reactions with any existing manual messages for this PR in this workspace
	allMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.Repository.FullName, payload.PullRequest.Number, targetChannel, repo.WorkspaceID, "")
	if err != nil {
		log.Error(ctx, "Failed to get all tracked messages for reaction sync", "error", err)
	} else if len(allMessages) > 1 {
		// There are manual messages to sync with - we don't have current PR status yet, so we'll just log
		log.Info(ctx, "Multiple tracked messages found for PR, reactions will be synced when status updates arrive",
			"total_messages", len(allMessages))
	}

	return nil
}

func (h *GitHubHandler) handlePREdited(ctx context.Context, payload *GitHubWebhookPayload) error {
	// Parse directives from PR description
	directives := h.slackService.ParsePRDirectives(payload.PullRequest.Body)

	// If !review-skip is found, delete all tracked messages for this PR
	if directives.RetroSkip {
		log.Info(ctx, "Processing !review-skip directive - deleting tracked messages")

		// Get all tracked messages for this PR across all workspaces and channels
		trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, payload.Repository.FullName, payload.PullRequest.Number)
		if err != nil {
			log.Error(ctx, "Failed to get tracked messages for retroactive deletion",
				"error", err,
			)
			return err
		}

		if len(trackedMessages) == 0 {
			log.Info(ctx, "No tracked messages found to delete")
			return nil
		}

		log.Info(ctx, "Found tracked messages to delete",
			"message_count", len(trackedMessages),
		)

		// Group messages by workspace for deletion
		messagesByWorkspace := make(map[string][]services.MessageRef)
		messageIDs := make([]string, 0, len(trackedMessages))

		for _, msg := range trackedMessages {
			messagesByWorkspace[msg.SlackTeamID] = append(messagesByWorkspace[msg.SlackTeamID], services.MessageRef{
				Channel:   msg.SlackChannel,
				Timestamp: msg.SlackMessageTS,
			})
			messageIDs = append(messageIDs, msg.ID)
		}

		// Delete messages from Slack in each workspace
		for workspaceID, messages := range messagesByWorkspace {
			err := h.slackService.DeleteMultipleMessages(ctx, workspaceID, messages)
			if err != nil {
				log.Error(ctx, "Failed to delete some messages in workspace",
					"error", err,
					"workspace_id", workspaceID,
					"message_count", len(messages),
				)
				// Continue with other workspaces even if one fails
			}
		}

		// Remove tracked messages from Firestore
		err = h.firestoreService.DeleteTrackedMessages(ctx, messageIDs)
		if err != nil {
			log.Error(ctx, "Failed to delete tracked messages from Firestore",
				"error", err,
				"message_count", len(messageIDs),
			)
			return err
		}

		log.Info(ctx, "Successfully processed !review-skip directive",
			"deleted_messages", len(trackedMessages),
		)
	}

	return nil
}

func (h *GitHubHandler) handlePRClosed(ctx context.Context, payload *GitHubWebhookPayload) error {
	// Get all tracked messages for this PR across all workspaces and channels
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		log.Error(ctx, "Failed to get tracked messages for PR closed reaction",
			"error", err,
			"merged", payload.PullRequest.Merged,
		)
		return err
	}
	if len(trackedMessages) == 0 {
		log.Warn(ctx, "No tracked messages found for PR closed reaction",
			"merged", payload.PullRequest.Merged,
		)
		return nil
	}

	// Add reaction to all tracked messages
	emoji := utils.GetEmojiForPRState(PRActionClosed, payload.PullRequest.Merged, h.emojiConfig)
	if emoji != "" {
		var messageRefs []services.MessageRef
		for _, msg := range trackedMessages {
			messageRefs = append(messageRefs, services.MessageRef{
				Channel:   msg.SlackChannel,
				Timestamp: msg.SlackMessageTS,
			})
		}

		// Group message refs by team ID for proper team-scoped API calls
		messagesByTeam := make(map[string][]services.MessageRef)
		for i, msg := range trackedMessages {
			messagesByTeam[msg.SlackTeamID] = append(messagesByTeam[msg.SlackTeamID], messageRefs[i])
		}

		// Add reactions for each team separately
		for teamID, teamMessageRefs := range messagesByTeam {
			err = h.slackService.AddReactionToMultipleMessages(ctx, teamID, teamMessageRefs, emoji)
			if err != nil {
				log.Error(ctx, "Failed to add PR closed reactions for team",
					"error", err,
					"team_id", teamID,
					"emoji", emoji,
					"message_count", len(teamMessageRefs),
					"merged", payload.PullRequest.Merged,
				)
				// Continue with other teams even if one fails
			}
		}
	}

	log.Info(ctx, "PR closed reactions synchronized across tracked messages",
		"merged", payload.PullRequest.Merged,
		"emoji", emoji,
		"message_count", len(trackedMessages),
	)
	return nil
}

func (h *GitHubHandler) handlePRReadyForReview(ctx context.Context, payload *GitHubWebhookPayload) error {
	log.Debug(ctx, "Processing PR ready for review",
		"title", payload.PullRequest.Title,
	)

	authorUsername := payload.PullRequest.User.Login
	log.Debug(ctx, "Looking up user by GitHub username", "github_username", authorUsername)

	user, err := h.firestoreService.GetUserByGitHubUsername(ctx, authorUsername)
	if err != nil {
		log.Error(ctx, "Failed to lookup user by GitHub username",
			"error", err,
			"github_username", authorUsername,
		)
		return err
	}

	log.Debug(ctx, "User lookup result", "user_found", user != nil)

	// Parse PR directives from description
	annotatedChannel, directives := h.slackService.ExtractChannelAndDirectives(payload.PullRequest.Body)
	log.Debug(ctx, "Channel and directive determination",
		"annotated_channel", annotatedChannel,
		"skip", directives.Skip,
		"user_to_cc", directives.UserToCC)

	// Check if PR should be skipped
	if directives.Skip {
		log.Info(ctx, "Skipping PR notification due to skip directive")
		return nil
	}

	// Get all workspace configurations for this repository
	log.Debug(ctx, "Looking up repository configurations across all workspaces")
	repos, err := h.firestoreService.GetReposForAllWorkspaces(ctx, payload.Repository.FullName)
	if err != nil {
		log.Error(ctx, "Failed to lookup repository configurations",
			"error", err,
		)
		return err
	}

	if len(repos) == 0 {
		autoRegisteredRepo, err := h.attemptAutoRegistration(ctx, payload, user)
		if err != nil {
			return err
		}
		if autoRegisteredRepo == nil {
			return nil // Skip notification - no auto-registration possible
		}
		repos = []*models.Repo{autoRegisteredRepo}
	}

	log.Info(ctx, "Found repository configurations in workspace(s)",
		"workspace_count", len(repos))

	// Process notifications for each workspace
	for _, repo := range repos {
		err := h.processWorkspaceNotification(ctx, payload, repo, user, annotatedChannel, directives)
		if err != nil {
			log.Error(ctx, "Failed to process notification for workspace",
				"error", err,
				"slack_team_id", repo.WorkspaceID,
			)
			// Continue processing other workspaces even if one fails
		}
	}

	return nil
}

// getAllTrackedMessagesForPR gets all tracked messages for a PR across all workspaces.
func (h *GitHubHandler) getAllTrackedMessagesForPR(
	ctx context.Context, repoFullName string, prNumber int,
) ([]*models.TrackedMessage, error) {
	// Get all workspace configurations for this repository to know which workspaces we need to query
	repos, err := h.firestoreService.GetReposForAllWorkspaces(ctx, repoFullName)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository configurations: %w", err)
	}

	var allMessages []*models.TrackedMessage

	// Get tracked messages from each workspace
	for _, repo := range repos {
		messages, err := h.firestoreService.GetTrackedMessages(ctx,
			repoFullName, prNumber, "", repo.WorkspaceID, "")
		if err != nil {
			log.Error(ctx, "Failed to get tracked messages for workspace",
				"error", err,
				"slack_team_id", repo.WorkspaceID,
			)
			continue // Continue with other workspaces
		}
		allMessages = append(allMessages, messages...)
	}

	return allMessages, nil
}

// attemptAutoRegistration tries to automatically register a repository for a verified user.
// Returns the created repository on success, nil if registration is not possible, or error if registration fails.
func (h *GitHubHandler) attemptAutoRegistration(
	ctx context.Context, payload *GitHubWebhookPayload, user *models.User,
) (*models.Repo, error) {
	if user != nil && user.Verified && user.NotificationsEnabled {
		log.Info(ctx, "Auto-registering repository for verified user's workspace",
			"github_username", user.GitHubUsername,
			"slack_team_id", user.SlackTeamID)

		repo := &models.Repo{
			ID:           payload.Repository.FullName,
			RepoFullName: payload.Repository.FullName,
			WorkspaceID:  user.SlackTeamID,
			Enabled:      true,
		}

		err := h.firestoreService.CreateRepo(ctx, repo)
		if err != nil {
			log.Error(ctx, "Failed to auto-register repository", "error", err)
			return nil, fmt.Errorf("failed to auto-register repository: %w", err)
		}

		log.Info(ctx, "Successfully auto-registered repository",
			"repo", repo.ID,
			"slack_team_id", repo.WorkspaceID,
			"registration_type", "automatic",
			"trigger_event", "pr_opened")

		return repo, nil
	}

	// Log detailed reason for skipping auto-registration
	skipReason := "unknown"
	if user == nil {
		skipReason = "no_user_found"
	} else if !user.Verified {
		skipReason = "user_not_verified"
	} else if !user.NotificationsEnabled {
		skipReason = "notifications_disabled"
	}

	log.Info(ctx, "No repository configurations and cannot auto-register",
		"skip_reason", skipReason,
		"user_found", user != nil,
		"user_verified", user != nil && user.Verified,
		"notifications_enabled", user != nil && user.NotificationsEnabled)

	return nil, nil // No auto-registration possible
}
