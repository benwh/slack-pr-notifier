package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	ErrUnsupportedEventType = errors.New("unsupported event type")
	ErrMissingAction        = errors.New("missing required field: action")
	ErrMissingRepository    = errors.New("missing required field: repository")
)

type GitHubHandler struct {
	cloudTasksService *services.CloudTasksService
	webhookSecret     string
}

func NewGitHubHandler(
	cloudTasksService *services.CloudTasksService,
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

	logger := slog.With(
		"trace_id", traceID,
		"remote_addr", c.ClientIP(),
		"user_agent", c.Request.UserAgent(),
	)

	eventType := c.GetHeader("X-GitHub-Event")
	deliveryID := c.GetHeader("X-GitHub-Delivery")

	if eventType == "" || deliveryID == "" {
		logger.Error("Missing required headers")
		c.JSON(400, gin.H{"error": "missing required headers"})
		return
	}

	if !h.validateSignature(c) {
		logger.Error("Invalid webhook signature")
		c.JSON(401, gin.H{"error": "invalid signature"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Failed to read request body", "error", err)
		c.JSON(400, gin.H{"error": "failed to read body"})
		return
	}

	if err := h.validateWebhookPayload(eventType, body); err != nil {
		logger.Error("Invalid webhook payload", "error", err, "event_type", eventType)
		c.JSON(400, gin.H{"error": "invalid payload"})
		return
	}

	job := &models.WebhookJob{
		ID:         uuid.New().String(),
		EventType:  eventType,
		DeliveryID: deliveryID,
		TraceID:    traceID,
		Payload:    body,
		ReceivedAt: time.Now(),
		Status:     "queued",
		RetryCount: 0,
	}

	if err := h.cloudTasksService.EnqueueWebhook(c.Request.Context(), job); err != nil {
		logger.Error("Failed to enqueue webhook", "error", err)
		c.JSON(500, gin.H{"error": "failed to queue webhook"})
		return
	}

	processingTime := time.Since(startTime)
	logger.Info("Webhook queued successfully",
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

func (h *GitHubHandler) validateSignature(c *gin.Context) bool {
	signature := c.GetHeader("X-Hub-Signature-256")
	if signature == "" {
		return h.webhookSecret == ""
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	expectedSignature := "sha256=" + h.computeHMAC256(body, h.webhookSecret)
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

func (h *GitHubHandler) computeHMAC256(data []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
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
