package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
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
	PRActionClosed             = "closed"
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
}

func NewGitHubHandler(
	cloudTasksService CloudTasksServiceInterface,
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
	webhookSecret string,
) *GitHubHandler {
	return &GitHubHandler{
		cloudTasksService: cloudTasksService,
		firestoreService:  firestoreService,
		slackService:      slackService,
		webhookSecret:     webhookSecret,
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

	// Marshal the WebhookJob as the payload for the unified Job
	jobPayload, err := json.Marshal(webhookJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal webhook job", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal job"})
		return
	}

	// Create unified Job
	unifiedJob := &models.Job{
		ID:      webhookJob.ID,
		Type:    models.JobTypeGitHubWebhook,
		TraceID: traceID,
		Payload: jobPayload,
	}

	if err := h.cloudTasksService.EnqueueJob(ctx, unifiedJob); err != nil {
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

// ProcessWebhookJob processes a GitHub webhook job from the unified job system.
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

	// Get all tracked messages for this PR across all channels
	trackedMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		githubPayload.Repository.FullName, githubPayload.PullRequest.Number, "", "")
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
	err = h.slackService.SyncAllReviewReactions(ctx, messageRefs, currentState)
	if err != nil {
		log.Error(ctx, "Failed to sync review reactions to tracked messages",
			"error", err,
			"review_state", currentState,
			"review_action", githubPayload.Action,
			"message_count", len(messageRefs),
		)
		return err
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

	var targetChannel string
	annotatedChannel := h.slackService.ExtractChannelFromDescription(payload.PullRequest.Body)
	log.Debug(ctx, "Channel determination", "annotated_channel", annotatedChannel)
	if annotatedChannel != "" {
		targetChannel = annotatedChannel
	} else if user != nil && user.DefaultChannel != "" {
		targetChannel = user.DefaultChannel
		log.Debug(ctx, "Using user default channel", "channel", targetChannel)
	} else {
		log.Debug(ctx, "Looking up repo default channel")
		repo, err := h.firestoreService.GetRepo(ctx, payload.Repository.FullName)
		if err != nil {
			log.Error(ctx, "Failed to lookup repository configuration",
				"error", err,
			)
			return err
		}
		if repo != nil {
			targetChannel = repo.DefaultChannel
			log.Debug(ctx, "Using repo default channel", "channel", targetChannel)
		} else {
			log.Debug(ctx, "No repo found in database")
		}
	}

	if targetChannel == "" {
		log.Info(ctx, "No target channel determined, skipping notification")
		return nil
	}

	// Check if we already have a bot message for this PR in this channel
	botMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.Repository.FullName, payload.PullRequest.Number, targetChannel, "bot")
	if err != nil {
		log.Error(ctx, "Failed to check for existing bot messages", "error", err)
		return err
	}

	if len(botMessages) > 0 {
		log.Info(ctx, "Bot message already exists for this PR in channel, skipping duplicate notification",
			"channel", targetChannel,
			"existing_message_count", len(botMessages))
		return nil
	}

	log.Info(ctx, "Posting PR message to Slack", "channel", targetChannel)

	// Calculate PR size (additions + deletions)
	prSize := payload.PullRequest.Additions + payload.PullRequest.Deletions

	timestamp, err := h.slackService.PostPRMessage(
		ctx,
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
		prSize,
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack",
			"error", err,
			"channel", targetChannel,
			"repo_name", payload.Repository.Name,
			"pr_title", payload.PullRequest.Title,
		)
		return err
	}
	log.Info(ctx, "Posted PR notification to Slack",
		"channel", targetChannel,
	)

	// Create TrackedMessage for the bot notification
	trackedMessage := &models.TrackedMessage{
		PRNumber:       payload.PullRequest.Number,
		RepoFullName:   payload.Repository.FullName,
		SlackChannel:   targetChannel,
		SlackMessageTS: timestamp,
		MessageSource:  "bot",
	}

	log.Debug(ctx, "Saving tracked message to database", "channel", trackedMessage.SlackChannel)
	err = h.firestoreService.CreateTrackedMessage(ctx, trackedMessage)
	if err != nil {
		log.Error(ctx, "Failed to save tracked message to database",
			"error", err,
			"channel", trackedMessage.SlackChannel,
			"message_ts", trackedMessage.SlackMessageTS,
		)
		return err
	}
	log.Debug(ctx, "Successfully saved tracked message to database")

	// After posting, synchronize reactions with any existing manual messages for this PR
	allMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.Repository.FullName, payload.PullRequest.Number, targetChannel, "")
	if err != nil {
		log.Error(ctx, "Failed to get all tracked messages for reaction sync", "error", err)
	} else if len(allMessages) > 1 {
		// There are manual messages to sync with - we don't have current PR status yet, so we'll just log
		log.Info(ctx, "Multiple tracked messages found for PR, reactions will be synced when status updates arrive",
			"total_messages", len(allMessages))
	}

	return nil
}

func (h *GitHubHandler) handlePRClosed(ctx context.Context, payload *GitHubWebhookPayload) error {
	// Get all tracked messages for this PR across all channels
	trackedMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.Repository.FullName, payload.PullRequest.Number, "", "")
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
	emoji := h.slackService.GetEmojiForPRState(PRActionClosed, payload.PullRequest.Merged)
	if emoji != "" {
		var messageRefs []services.MessageRef
		for _, msg := range trackedMessages {
			messageRefs = append(messageRefs, services.MessageRef{
				Channel:   msg.SlackChannel,
				Timestamp: msg.SlackMessageTS,
			})
		}

		err = h.slackService.AddReactionToMultipleMessages(ctx, messageRefs, emoji)
		if err != nil {
			log.Error(ctx, "Failed to add PR closed reactions to tracked messages",
				"error", err,
				"emoji", emoji,
				"message_count", len(messageRefs),
				"merged", payload.PullRequest.Merged,
			)
			return err
		}
	}

	log.Info(ctx, "PR closed reactions synchronized across tracked messages",
		"merged", payload.PullRequest.Merged,
		"emoji", emoji,
		"message_count", len(trackedMessages),
	)
	return nil
}
