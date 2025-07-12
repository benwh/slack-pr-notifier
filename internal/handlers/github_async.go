package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"time"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type GitHubAsyncHandler struct {
	cloudTasksService *services.CloudTasksService
	validationService *services.ValidationService
	webhookSecret     string
}

func NewGitHubAsyncHandler(
	cloudTasksService *services.CloudTasksService,
	validationService *services.ValidationService,
	webhookSecret string,
) *GitHubAsyncHandler {
	return &GitHubAsyncHandler{
		cloudTasksService: cloudTasksService,
		validationService: validationService,
		webhookSecret:     webhookSecret,
	}
}

func (h *GitHubAsyncHandler) HandleWebhook(c *gin.Context) {
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

	if err := h.validationService.ValidateWebhookPayload(eventType, body); err != nil {
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

func (h *GitHubAsyncHandler) validateSignature(c *gin.Context) bool {
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

func (h *GitHubAsyncHandler) computeHMAC256(data []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
