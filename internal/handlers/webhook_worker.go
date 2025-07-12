package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
) *WebhookWorkerHandler {
	return &WebhookWorkerHandler{
		firestoreService:  firestoreService,
		slackService:      slackService,
		maxProcessingTime: 5 * time.Minute,
	}
}

func (h *WebhookWorkerHandler) ProcessWebhook(c *gin.Context) {
	startTime := time.Now()

	var job models.WebhookJob
	if err := c.ShouldBindJSON(&job); err != nil {
		slog.Error("Invalid job payload", "error", err)
		c.JSON(400, gin.H{"error": "invalid job payload"})
		return
	}

	// Get actual retry count from Cloud Tasks headers
	actualRetryCount := c.GetHeader("X-Cloudtasks-Taskretrycount")
	if actualRetryCount == "" {
		actualRetryCount = "0"
	}

	logger := slog.With(
		"job_id", job.ID,
		"event_type", job.EventType,
		"trace_id", job.TraceID,
		"retry_count", actualRetryCount,
		"task_execution_count", c.GetHeader("X-Cloudtasks-Taskexecutioncount"),
	)

	logger.Info("Processing webhook job")

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.maxProcessingTime)
	defer cancel()

	if err := h.processWebhookPayload(ctx, &job); err != nil {
		processingTime := time.Since(startTime)
		// Check if this is an "already_reacted" error that escaped the service layer
		if strings.Contains(err.Error(), "already_reacted") {
			logger.Info("Webhook processing completed (reaction already exists)",
				"processing_time_ms", processingTime.Milliseconds(),
			)
			c.JSON(200, gin.H{
				"status":             "completed",
				"note":               "reaction already exists",
				"processing_time_ms": processingTime.Milliseconds(),
			})
			return
		}

		logger.Error("Failed to process webhook",
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
	logger.Info("Webhook processed successfully",
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
		return fmt.Errorf("failed to unmarshal pull request payload: %w", err)
	}

	slog.Info("Handling pull request event",
		"action", payload.Action,
		"pr_number", payload.PullRequest.Number,
		"is_draft", payload.PullRequest.Draft,
	)

	switch payload.Action {
	case PRActionOpened:
		return h.handlePROpened(ctx, &payload)
	case PRActionClosed:
		return h.handlePRClosed(ctx, &payload)
	default:
		slog.Warn("Pull request action not handled", "action", payload.Action)
	}
	return nil
}

func (h *WebhookWorkerHandler) processPullRequestReviewEvent(ctx context.Context, job *models.WebhookJob) error {
	var payload GitHubWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal pull request review payload: %w", err)
	}

	if payload.Action != PRReviewActionSubmitted {
		return nil
	}

	message, err := h.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil || message == nil {
		return err
	}

	emoji := h.slackService.GetEmojiForReviewState(payload.Review.State)
	if emoji != "" {
		err = h.slackService.AddReaction(message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			return err
		}
	}

	message.LastStatus = "review_" + payload.Review.State
	return h.firestoreService.UpdateMessage(ctx, message)
}

func (h *WebhookWorkerHandler) handlePROpened(ctx context.Context, payload *GitHubWebhookPayload) error {
	if payload.PullRequest.Draft {
		slog.Debug("Skipping draft PR", "pr_number", payload.PullRequest.Number)
		return nil
	}

	slog.Debug("Processing PR opened",
		"pr_number", payload.PullRequest.Number,
		"author", payload.PullRequest.User.Login,
		"title", payload.PullRequest.Title,
	)

	authorUsername := payload.PullRequest.User.Login
	slog.Debug("Looking up user by GitHub username", "github_username", authorUsername)
	user, err := h.firestoreService.GetUserByGitHubUsername(ctx, authorUsername)
	if err != nil {
		slog.Error("Failed to lookup user", "github_username", authorUsername, "error", err)
		return err
	}
	slog.Debug("User lookup result", "user_found", user != nil)

	var targetChannel string
	annotatedChannel := h.slackService.ExtractChannelFromDescription(payload.PullRequest.Body)
	slog.Debug("Channel determination", "annotated_channel", annotatedChannel)
	if annotatedChannel != "" {
		targetChannel = annotatedChannel
	} else if user != nil && user.DefaultChannel != "" {
		targetChannel = user.DefaultChannel
		slog.Debug("Using user default channel", "channel", targetChannel)
	} else {
		slog.Debug("Looking up repo default channel", "repo", payload.Repository.FullName)
		repo, err := h.firestoreService.GetRepo(ctx, payload.Repository.FullName)
		if err != nil {
			slog.Error("Failed to lookup repo", "repo", payload.Repository.FullName, "error", err)
			return err
		}
		if repo != nil {
			targetChannel = repo.DefaultChannel
			slog.Debug("Using repo default channel", "channel", targetChannel)
		} else {
			slog.Debug("No repo found in database", "repo", payload.Repository.FullName)
		}
	}

	if targetChannel == "" {
		slog.Info("No target channel determined, skipping notification")
		return nil
	}

	slog.Info("Posting PR message to Slack", "channel", targetChannel)

	timestamp, err := h.slackService.PostPRMessage(
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
	)
	if err != nil {
		slog.Error("Failed to post PR message to Slack", "channel", targetChannel, "error", err)
		return err
	}
	slog.Info("Posted PR notification to Slack", "channel", targetChannel, "pr_number", payload.PullRequest.Number)

	message := &models.Message{
		PRNumber:             payload.PullRequest.Number,
		RepoFullName:         payload.Repository.FullName,
		SlackChannel:         targetChannel,
		SlackMessageTS:       timestamp,
		GitHubPRURL:          payload.PullRequest.HTMLURL,
		AuthorGitHubUsername: authorUsername,
		LastStatus:           PRActionOpened,
	}

	slog.Debug("Saving message to database", "pr_number", message.PRNumber, "channel", message.SlackChannel)
	err = h.firestoreService.CreateMessage(ctx, message)
	if err != nil {
		slog.Error("Failed to save message to database", "error", err)
		return err
	}
	slog.Debug("Successfully saved message to database")
	return nil
}

func (h *WebhookWorkerHandler) handlePRClosed(ctx context.Context, payload *GitHubWebhookPayload) error {
	message, err := h.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil || message == nil {
		return err
	}

	emoji := h.slackService.GetEmojiForPRState(PRActionClosed, payload.PullRequest.Merged)
	if emoji != "" {
		err = h.slackService.AddReaction(message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			return err
		}
	}

	message.LastStatus = PRActionClosed
	return h.firestoreService.UpdateMessage(ctx, message)
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
