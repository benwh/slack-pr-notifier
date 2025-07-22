package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

// This is a simplified version that implements the core functionality we need for testing.
type TestSlackHandler struct {
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	cloudTasksService CloudTasksServiceInterface // Use our interface
	githubAuthService *services.GitHubAuthService
	signingSecret     string
	config            *config.Config
}

// NewTestSlackHandler creates a test-specific SlackHandler that uses the mock CloudTasksService.
func NewTestSlackHandler(
	fs *services.FirestoreService,
	slackSrv *services.SlackService,
	cloudTasks CloudTasksServiceInterface,
	githubAuth *services.GitHubAuthService,
	cfg *config.Config,
) *TestSlackHandler {
	return &TestSlackHandler{
		firestoreService:  fs,
		slackService:      slackSrv,
		cloudTasksService: cloudTasks,
		githubAuthService: githubAuth,
		signingSecret:     cfg.SlackSigningSecret,
		config:            cfg,
	}
}

// HandleEvent processes incoming Slack Events API events - same as real handler.
func (sh *TestSlackHandler) HandleEvent(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	if err := sh.verifySignature(c.Request.Header, body); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse event"})
		return
	}

	// Handle URL verification challenge
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var r *slackevents.ChallengeResponse
		err := json.Unmarshal(body, &r)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse challenge"})
			return
		}
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

		if ev, ok := innerEvent.Data.(*slackevents.MessageEvent); ok {
			sh.handleMessageEvent(ctx, ev, eventsAPIEvent.TeamID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleMessageEvent processes message events to detect and track GitHub PR links.
func (sh *TestSlackHandler) handleMessageEvent(ctx context.Context, event *slackevents.MessageEvent, teamID string) {
	// Skip bot messages, edited messages, and messages without text
	if event.BotID != "" || event.SubType == "message_changed" || event.Text == "" {
		return
	}

	// Extract PR links from message text
	prLinks := utils.ExtractPRLinks(event.Text)
	if len(prLinks) == 0 {
		return
	}

	// Process each PR link found
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

		// Queue for processing using our mock
		err = sh.cloudTasksService.EnqueueJob(linkCtx, job)
		if err != nil {
			log.Error(linkCtx, "Failed to enqueue manual link processing", "error", err)
		} else {
			log.Info(linkCtx, "Manual PR link detected and queued for processing")
		}
	}
}

// ProcessManualPRLinkJob processes a manual PR link job - same as real handler.
func (sh *TestSlackHandler) ProcessManualPRLinkJob(ctx context.Context, job *models.Job) error {
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
	return nil
}

// verifySignature verifies Slack request signature.
func (sh *TestSlackHandler) verifySignature(header http.Header, body []byte) error {
	if sh.signingSecret == "" {
		return nil
	}

	sv, err := slack.NewSecretsVerifier(header, sh.signingSecret)
	if err != nil {
		return fmt.Errorf("failed to create secrets verifier: %w", err)
	}

	if _, err := sv.Write(body); err != nil {
		return fmt.Errorf("failed to write body to verifier: %w", err)
	}

	if err := sv.Ensure(); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// Add other methods from handlers.SlackHandler as needed for testing
// For now, we'll implement just the core methods we need
