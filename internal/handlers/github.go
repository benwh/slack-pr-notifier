package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v73/github"
	"github.com/google/uuid"
)

var (
	ErrUnsupportedEventType = errors.New("unsupported event type")
	ErrMissingAction        = errors.New("missing required field: action")
	ErrMissingRepository    = errors.New("missing required field: repository")
)

// CloudTasksServiceInterface defines the interface for cloud tasks operations.
type CloudTasksServiceInterface interface {
	EnqueueWebhook(ctx context.Context, job *models.WebhookJob) error
}

type GitHubHandler struct {
	cloudTasksService CloudTasksServiceInterface
	webhookSecret     string
}

func NewGitHubHandler(
	cloudTasksService CloudTasksServiceInterface,
	webhookSecret string,
) *GitHubHandler {
	return &GitHubHandler{
		cloudTasksService: cloudTasksService,
		webhookSecret:     webhookSecret,
	}
}

func (h *GitHubHandler) HandleWebhook(c *gin.Context) {
	startTime := time.Now()
	traceID := c.GetString("trace_id")

	eventType := c.GetHeader("X-GitHub-Event")
	deliveryID := c.GetHeader("X-GitHub-Delivery")

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
		c.JSON(400, gin.H{"error": "missing required headers"})
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
		c.JSON(401, gin.H{"error": "invalid payload or signature"})
		return
	}

	if err := h.validateWebhookPayload(eventType, payload); err != nil {
		log.Error(ctx, "Invalid webhook payload", "error", err, "event_type", eventType)
		c.JSON(400, gin.H{"error": "invalid payload"})
		return
	}

	job := &models.WebhookJob{
		ID:         uuid.New().String(),
		EventType:  eventType,
		DeliveryID: deliveryID,
		TraceID:    traceID,
		Payload:    payload,
		ReceivedAt: time.Now(),
		Status:     "queued",
		RetryCount: 0,
	}

	if err := h.cloudTasksService.EnqueueWebhook(ctx, job); err != nil {
		log.Error(ctx, "Failed to enqueue webhook", "error", err)
		c.JSON(500, gin.H{"error": "failed to queue webhook"})
		return
	}

	processingTime := time.Since(startTime)
	log.Info(ctx, "Webhook queued successfully",
		"job_id", job.ID,
		"event_type", eventType,
		"processing_time_ms", processingTime.Milliseconds(),
	)

	c.JSON(200, gin.H{
		"status":             "queued",
		"job_id":             job.ID,
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
