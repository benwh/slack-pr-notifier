package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

const (
	// GitHub PR action types.
	PRActionOpened = "opened"
	PRActionClosed = "closed"

	// GitHub PR review action types.
	PRReviewActionSubmitted = "submitted"

	// GitHub event types.
	EventTypePullRequest       = "pull_request"
	EventTypePullRequestReview = "pull_request_review"
)

// GitHubWebhookPayload represents the structure of GitHub webhook events.
type GitHubWebhookPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Draft   bool   `json:"draft"`
		HTMLURL string `json:"html_url"`
		User    struct {
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

var ErrUnsupportedJobEventType = errors.New("unsupported event type")

type WebhookWorkerHandler struct {
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	maxProcessingTime time.Duration
}

func NewWebhookWorkerHandler(
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
	cfg *config.Config,
) *WebhookWorkerHandler {
	return &WebhookWorkerHandler{
		firestoreService:  firestoreService,
		slackService:      slackService,
		maxProcessingTime: cfg.WebhookProcessingTimeout,
	}
}

func (h *WebhookWorkerHandler) ProcessWebhook(c *gin.Context) {
	startTime := time.Now()
	ctx := c.Request.Context()

	var job models.WebhookJob
	if err := c.ShouldBindJSON(&job); err != nil {
		log.Error(ctx, "Invalid job payload - JSON binding failed",
			"error", err,
			"content_type", c.ContentType(),
			"content_length", c.Request.ContentLength,
		)
		c.JSON(400, gin.H{"error": "invalid job payload"})
		return
	}

	// Get actual retry count from Cloud Tasks headers
	actualRetryCount := c.GetHeader("X-Cloudtasks-Taskretrycount")
	if actualRetryCount == "" {
		actualRetryCount = "0"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.maxProcessingTime)
	defer cancel()

	// Add job metadata to context for all log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"job_id":               job.ID,
		"event_type":           job.EventType,
		"trace_id":             job.TraceID,
		"retry_count":          actualRetryCount,
		"task_execution_count": c.GetHeader("X-Cloudtasks-Taskexecutioncount"),
	})

	log.Debug(ctx, "Processing webhook job")

	if err := h.processWebhookPayload(ctx, &job); err != nil {
		processingTime := time.Since(startTime)
		// Check if this is an "already_reacted" error that escaped the service layer
		if strings.Contains(err.Error(), "already_reacted") {
			log.Info(ctx, "Webhook processing completed (reaction already exists)",
				"processing_time_ms", processingTime.Milliseconds(),
			)
			c.JSON(200, gin.H{
				"status":             "completed",
				"note":               "reaction already exists",
				"processing_time_ms": processingTime.Milliseconds(),
			})
			return
		}

		log.Error(ctx, "Failed to process webhook",
			"error", err,
			"processing_time_ms", processingTime.Milliseconds(),
		)

		if isRetryableError(err) {
			c.JSON(500, gin.H{
				"error":              "processing failed",
				"retryable":          true,
				"processing_time_ms": processingTime.Milliseconds(),
			})
		} else {
			c.JSON(400, gin.H{
				"error":              "processing failed",
				"retryable":          false,
				"processing_time_ms": processingTime.Milliseconds(),
			})
		}
		return
	}

	processingTime := time.Since(startTime)
	log.Info(ctx, "Webhook processed successfully",
		"processing_time_ms", processingTime.Milliseconds(),
	)

	c.JSON(200, gin.H{
		"status":             "processed",
		"processing_time_ms": processingTime.Milliseconds(),
	})
}

func (h *WebhookWorkerHandler) processWebhookPayload(ctx context.Context, job *models.WebhookJob) error {
	switch job.EventType {
	case EventTypePullRequest:
		return h.processPullRequestEvent(ctx, job)
	case EventTypePullRequestReview:
		return h.processPullRequestReviewEvent(ctx, job)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedJobEventType, job.EventType)
	}
}

func (h *WebhookWorkerHandler) processPullRequestEvent(ctx context.Context, job *models.WebhookJob) error {
	var payload GitHubWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request payload",
			"error", err,
			"payload_size", len(job.Payload),
		)
		return fmt.Errorf("failed to unmarshal pull request payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number": payload.PullRequest.Number,
		"repo":      payload.Repository.FullName,
		"author":    payload.PullRequest.User.Login,
		"pr_action": payload.Action,
	})

	log.Info(ctx, "Handling pull request event",
		"is_draft", payload.PullRequest.Draft,
	)

	switch payload.Action {
	case PRActionOpened:
		return h.handlePROpened(ctx, &payload)
	case PRActionClosed:
		return h.handlePRClosed(ctx, &payload)
	default:
		log.Warn(ctx, "Pull request action not handled")
	}
	return nil
}

func (h *WebhookWorkerHandler) processPullRequestReviewEvent(ctx context.Context, job *models.WebhookJob) error {
	var payload GitHubWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request review payload",
			"error", err,
			"payload_size", len(job.Payload),
		)
		return fmt.Errorf("failed to unmarshal pull request review payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number":     payload.PullRequest.Number,
		"repo":          payload.Repository.FullName,
		"author":        payload.PullRequest.User.Login,
		"reviewer":      payload.Review.User.Login,
		"review_state":  payload.Review.State,
		"review_action": payload.Action,
	})

	if payload.Action != PRReviewActionSubmitted {
		return nil
	}

	message, err := h.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		log.Error(ctx, "Failed to get message for PR review reaction",
			"error", err,
		)
		return err
	}
	if message == nil {
		log.Warn(ctx, "No message found for PR review reaction")
		return nil
	}

	emoji := h.slackService.GetEmojiForReviewState(payload.Review.State)
	if emoji != "" {
		err = h.slackService.AddReaction(ctx, message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			log.Error(ctx, "Failed to add review reaction to Slack message",
				"error", err,
				"channel", message.SlackChannel,
				"message_ts", message.SlackMessageTS,
				"emoji", emoji,
			)
			return err
		}
	}

	message.LastStatus = "review_" + payload.Review.State
	err = h.firestoreService.UpdateMessage(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to update message status after review reaction",
			"error", err,
			"message_id", message.ID,
			"new_status", message.LastStatus,
		)
		return err
	}
	return nil
}

func (h *WebhookWorkerHandler) handlePROpened(ctx context.Context, payload *GitHubWebhookPayload) error {
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

	log.Info(ctx, "Posting PR message to Slack", "channel", targetChannel)

	timestamp, err := h.slackService.PostPRMessage(
		ctx,
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
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

	message := &models.Message{
		PRNumber:             payload.PullRequest.Number,
		RepoFullName:         payload.Repository.FullName,
		SlackChannel:         targetChannel,
		SlackMessageTS:       timestamp,
		GitHubPRURL:          payload.PullRequest.HTMLURL,
		AuthorGitHubUsername: authorUsername,
		LastStatus:           PRActionOpened,
	}

	log.Debug(ctx, "Saving message to database", "channel", message.SlackChannel)
	err = h.firestoreService.CreateMessage(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to save message to database",
			"error", err,
			"channel", message.SlackChannel,
			"message_ts", message.SlackMessageTS,
		)
		return err
	}
	log.Debug(ctx, "Successfully saved message to database")
	return nil
}

func (h *WebhookWorkerHandler) handlePRClosed(ctx context.Context, payload *GitHubWebhookPayload) error {
	message, err := h.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil {
		log.Error(ctx, "Failed to get message for PR closed reaction",
			"error", err,
			"merged", payload.PullRequest.Merged,
		)
		return err
	}
	if message == nil {
		log.Warn(ctx, "No message found for PR closed reaction",
			"merged", payload.PullRequest.Merged,
		)
		return nil
	}

	emoji := h.slackService.GetEmojiForPRState(PRActionClosed, payload.PullRequest.Merged)
	if emoji != "" {
		err = h.slackService.AddReaction(ctx, message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			log.Error(ctx, "Failed to add PR closed reaction to Slack message",
				"error", err,
				"channel", message.SlackChannel,
				"message_ts", message.SlackMessageTS,
				"emoji", emoji,
				"merged", payload.PullRequest.Merged,
			)
			return err
		}
	}

	message.LastStatus = PRActionClosed
	err = h.firestoreService.UpdateMessage(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to update message status after PR closed",
			"error", err,
			"message_id", message.ID,
			"new_status", message.LastStatus,
			"merged", payload.PullRequest.Merged,
		)
		return err
	}
	return nil
}

func isRetryableError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var slackErr *slack.RateLimitedError
	if errors.As(err, &slackErr) {
		return true
	}

	// Check for specific Slack errors that should be retried
	var slackErrorResp *slack.SlackErrorResponse
	if errors.As(err, &slackErrorResp) {
		switch slackErrorResp.Err {
		case "already_reacted":
			// This should have been handled in SlackService, but if it gets here, don't retry
			return false
		case "channel_not_found", "invalid_channel", "invalid_auth", "account_inactive":
			// These are permanent errors, don't retry
			return false
		case "internal_error", "service_unavailable":
			// These are temporary Slack issues, retry them
			return true
		default:
			// For unknown Slack errors, err on the side of not retrying to avoid infinite loops
			return false
		}
	}

	// Check for network/connection errors (should be retried)
	errStr := err.Error()
	if strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "dial") {
		return true
	}

	// Default to not retrying for unknown errors
	return false
}
