package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

const (
	jobRetryCountWarningThreshold = 5
)

type JobProcessor struct {
	githubHandler *GitHubHandler
	slackHandler  *SlackHandler
	config        *config.Config
}

func NewJobProcessor(
	githubHandler *GitHubHandler,
	slackHandler *SlackHandler,
	cfg *config.Config,
) *JobProcessor {
	return &JobProcessor{
		githubHandler: githubHandler,
		slackHandler:  slackHandler,
		config:        cfg,
	}
}

func (jp *JobProcessor) ProcessJob(c *gin.Context) {
	startTime := time.Now()
	ctx := c.Request.Context()

	var job models.Job
	if err := c.ShouldBindJSON(&job); err != nil {
		log.Error(ctx, "Invalid job payload - JSON binding failed",
			"error", err,
			"content_type", c.ContentType(),
			"content_length", c.Request.ContentLength,
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job payload"})
		return
	}

	// Get actual retry count from Cloud Tasks headers
	actualRetryCount := c.GetHeader("X-Cloudtasks-Taskretrycount")
	if actualRetryCount == "" {
		actualRetryCount = "0"
	}

	// Parse retry count for warning logic
	retryCountInt := 0
	if actualRetryCount != "" {
		if parsed, err := strconv.Atoi(actualRetryCount); err == nil {
			retryCountInt = parsed
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), jp.config.WebhookProcessingTimeout)
	defer cancel()

	// Add job metadata to context for all log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"job_id":               job.ID,
		"job_type":             job.Type,
		"trace_id":             job.TraceID,
		"retry_count":          actualRetryCount,
		"task_execution_count": c.GetHeader("X-Cloudtasks-Taskexecutioncount"),
	})

	log.Debug(ctx, "Processing job")

	// Check if we've exceeded the configured max retries
	if int32(retryCountInt) >= jp.config.CloudTasksMaxAttempts {
		log.Error(ctx, "Maximum retry attempts exceeded, failing task permanently",
			"max_retries_configured", jp.config.CloudTasksMaxAttempts,
		)
		c.JSON(http.StatusOK, gin.H{
			"status":      "max_retries_exceeded",
			"error":       "Task has been retried too many times",
			"retry_count": retryCountInt,
			"max_retries": jp.config.CloudTasksMaxAttempts,
		})
		return
	}

	// Warn if retry count is getting high
	if retryCountInt > jobRetryCountWarningThreshold {
		log.Warn(ctx, "High retry count for job",
			"retry_threshold", jobRetryCountWarningThreshold,
			"max_retries_configured", jp.config.CloudTasksMaxAttempts,
		)
	}

	if err := jp.routeJob(ctx, &job); err != nil {
		processingTime := time.Since(startTime)
		log.Error(ctx, "Failed to process job",
			"error", err,
			"processing_time_ms", processingTime.Milliseconds(),
		)

		if isJobRetryableError(err) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":              "processing failed",
				"retryable":          true,
				"processing_time_ms": processingTime.Milliseconds(),
			})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":              "processing failed",
				"retryable":          false,
				"processing_time_ms": processingTime.Milliseconds(),
			})
		}
		return
	}

	processingTime := time.Since(startTime)
	log.Info(ctx, "Job processed successfully",
		"processing_time_ms", processingTime.Milliseconds(),
	)

	c.JSON(http.StatusOK, gin.H{
		"status":             "processed",
		"processing_time_ms": processingTime.Milliseconds(),
	})
}

func (jp *JobProcessor) routeJob(ctx context.Context, job *models.Job) error {
	switch job.Type {
	case models.JobTypeGitHubWebhook:
		return jp.githubHandler.ProcessWebhookJob(ctx, job)
	case models.JobTypeManualPRLink:
		return jp.slackHandler.ProcessManualPRLinkJob(ctx, job)
	default:
		return models.ErrUnsupportedJobType
	}
}

func isJobRetryableError(err error) bool {
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
