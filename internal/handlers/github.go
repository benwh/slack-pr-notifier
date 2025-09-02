package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github-slack-notifier/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v74/github"
	"github.com/google/uuid"
)

var (
	ErrUnsupportedEventType = errors.New("unsupported event type")
	ErrMissingAction        = errors.New("missing required field: action")
	ErrMissingRepository    = errors.New("missing required field: repository")
	ErrMissingInstallation  = errors.New("missing required field: installation")
)

const (
	PRActionOpened                        = "opened"
	PRActionEdited                        = "edited"
	PRActionClosed                        = "closed"
	PRActionReopened                      = "reopened"
	PRActionReadyForReview                = "ready_for_review"
	PRReviewActionSubmitted               = "submitted"
	PRReviewActionDismissed               = "dismissed"
	InstallationActionCreated             = "created"
	InstallationActionDeleted             = "deleted"
	InstallationActionSuspend             = "suspend"
	InstallationActionUnsuspend           = "unsuspend"
	InstallationActionNewPermissions      = "new_permissions_accepted"
	InstallationRepositoriesActionAdded   = "added"
	InstallationRepositoriesActionRemoved = "removed"
	EventTypePullRequest                  = "pull_request"
	EventTypePullRequestReview            = "pull_request_review"
	EventTypeInstallation                 = "installation"
	EventTypeInstallationRepositories     = "installation_repositories"
	EventTypeGitHubAppAuth                = "github_app_authorization"
	RepositorySelectionSelected           = "selected"
)

// PRUpdateChanges tracks what has changed in a PR edit that needs to be reflected in Slack messages.
type PRUpdateChanges struct {
	TitleChanged      bool
	CCChanged         bool
	DirectivesChanged bool
	OldTitle          string
	NewTitle          string
	OldCC             string
	NewCC             string
	OldHasDirective   bool
	NewHasDirective   bool
}

// Channel utility functions

// isChannelID checks if a string looks like a Slack channel ID (e.g., "C0964H95F6C").
func isChannelID(s string) bool {
	return len(s) >= 9 && s[0] == 'C' && strings.ToUpper(s) == s
}

// getChannelNameForStorage determines what channel name to store (never store IDs as names).
func getChannelNameForStorage(targetChannel, annotatedChannel string) string {
	if !isChannelID(targetChannel) {
		return targetChannel
	}
	if annotatedChannel != "" && !isChannelID(annotatedChannel) {
		return annotatedChannel
	}
	return "" // Can't determine name from ID
}

// channelsMatch checks if a stored channel matches a new channel reference.
func channelsMatch(storedName, storedID, newChannel string) bool {
	// Prefer name comparison when available
	if storedName != "" && !isChannelID(storedName) && !isChannelID(newChannel) {
		return storedName == newChannel
	}
	// Fall back to ID comparison
	if isChannelID(newChannel) && storedID != "" {
		return storedID == newChannel
	}
	return false
}

// CloudTasksServiceInterface defines the interface for cloud tasks operations.
type CloudTasksServiceInterface interface {
	EnqueueJob(ctx context.Context, job *models.Job) error
}

type GitHubHandler struct {
	cloudTasksService CloudTasksServiceInterface
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	githubService     *services.GitHubService
	webhookSecret     string
	emojiConfig       config.EmojiConfig
}

// NewGitHubHandler creates a new GitHubHandler with the provided services and configuration.
// Initializes handler with dependencies for processing GitHub webhooks and managing PR notifications.
func NewGitHubHandler(
	cloudTasksService CloudTasksServiceInterface,
	firestoreService *services.FirestoreService,
	slackService *services.SlackService,
	githubService *services.GitHubService,
	webhookSecret string,
	emojiConfig config.EmojiConfig,
) *GitHubHandler {
	return &GitHubHandler{
		cloudTasksService: cloudTasksService,
		firestoreService:  firestoreService,
		slackService:      slackService,
		githubService:     githubService,
		webhookSecret:     webhookSecret,
		emojiConfig:       emojiConfig,
	}
}

// HandleWebhook processes incoming GitHub webhook events.
// Validates payload signature, creates webhook jobs, and enqueues them for async processing.
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

// validateWebhookPayload validates GitHub webhook payload structure based on event type.
// Ensures required fields are present for each supported webhook event type.
func (h *GitHubHandler) validateWebhookPayload(eventType string, payload []byte) error {
	switch eventType {
	case "pull_request", "pull_request_review":
		return h.validateGitHubPayload(payload)
	case "installation":
		return h.validateInstallationPayload(payload)
	case "installation_repositories":
		return h.validateInstallationRepositoriesPayload(payload)
	case "github_app_authorization":
		// GitHub app authorization events don't need special validation
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventType, eventType)
	}
}

// validateGitHubPayload validates basic GitHub webhook payload structure.
// Checks for required fields like action and repository in PR-related events.
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
		return h.processPullRequestReviewEvent(ctx, webhookJob.Payload, webhookJob.TraceID)
	case EventTypeInstallation:
		return h.processInstallationEvent(ctx, webhookJob.Payload)
	case EventTypeInstallationRepositories:
		return h.processInstallationRepositoriesEvent(ctx, webhookJob.Payload)
	case EventTypeGitHubAppAuth:
		return h.processGitHubAppAuthEvent(ctx, webhookJob.Payload)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventType, webhookJob.EventType)
	}
}

// ProcessWorkspacePRJob processes a workspace-specific PR job from the job system.
// This method handles PR notifications for a single workspace.
func (h *GitHubHandler) ProcessWorkspacePRJob(ctx context.Context, job *models.Job) error {
	var workspacePRJob models.WorkspacePRJob
	if err := json.Unmarshal(job.Payload, &workspacePRJob); err != nil {
		return fmt.Errorf("failed to unmarshal workspace PR job: %w", err)
	}

	// Add job metadata to context for all log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number":    workspacePRJob.PRNumber,
		"repo":         workspacePRJob.RepoFullName,
		"workspace_id": workspacePRJob.WorkspaceID,
		"pr_action":    workspacePRJob.PRAction,
		"author":       workspacePRJob.GitHubUsername,
	})

	log.Debug(ctx, "Processing workspace PR job")

	// Unmarshal the GitHub payload
	var githubPayload github.PullRequestEvent
	if err := json.Unmarshal(workspacePRJob.PRPayload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal GitHub payload from workspace PR job",
			"error", err,
			"payload_size", len(workspacePRJob.PRPayload),
		)
		return fmt.Errorf("failed to unmarshal GitHub payload from workspace PR job: %w", err)
	}

	// Get user information
	var user *models.User
	var err error
	if workspacePRJob.GitHubUserID > 0 {
		user, err = h.firestoreService.GetUserByGitHubUserID(ctx, workspacePRJob.GitHubUserID)
		if err != nil {
			log.Error(ctx, "Failed to lookup user by GitHub user ID",
				"error", err,
				"github_user_id", workspacePRJob.GitHubUserID,
			)
			return err
		}
	}

	// Get workspace repository configuration
	repo, err := h.firestoreService.GetRepo(ctx, workspacePRJob.RepoFullName, workspacePRJob.WorkspaceID)
	if err != nil {
		log.Error(ctx, "Failed to get repository configuration",
			"error", err,
			"workspace_id", workspacePRJob.WorkspaceID,
			"repo", workspacePRJob.RepoFullName,
		)
		return err
	}

	if repo == nil {
		log.Warn(ctx, "Repository configuration not found for workspace",
			"workspace_id", workspacePRJob.WorkspaceID,
			"repo", workspacePRJob.RepoFullName,
		)
		return fmt.Errorf("%w for workspace %s, repo %s", models.ErrRepoConfigNotFound, workspacePRJob.WorkspaceID, workspacePRJob.RepoFullName)
	}

	// Parse directives from the original payload
	_, directives := h.slackService.ExtractChannelAndDirectives(githubPayload.GetPullRequest().GetBody())

	// Process the notification for this specific workspace
	return h.processWorkspaceNotification(ctx, &githubPayload, repo, user, workspacePRJob.AnnotatedChannel, directives)
}

// processPullRequestEvent processes pull request webhook events.
// Handles PR opened, edited, ready_for_review, and closed actions with appropriate notifications.
func (h *GitHubHandler) processPullRequestEvent(ctx context.Context, payload []byte) error {
	var githubPayload github.PullRequestEvent
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal pull request payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number": githubPayload.GetPullRequest().GetNumber(),
		"repo":      githubPayload.GetRepo().GetFullName(),
		"author":    githubPayload.GetPullRequest().GetUser().GetLogin(),
		"pr_action": githubPayload.GetAction(),
	})

	log.Info(ctx, "Handling pull request event",
		"is_draft", githubPayload.GetPullRequest().GetDraft(),
	)

	switch githubPayload.GetAction() {
	case PRActionOpened:
		return h.handlePROpened(ctx, &githubPayload)
	case PRActionEdited:
		return h.handlePREdited(ctx, &githubPayload)
	case PRActionReadyForReview:
		return h.handlePRReadyForReview(ctx, &githubPayload)
	case PRActionClosed:
		return h.handlePRClosed(ctx, &githubPayload)
	case PRActionReopened:
		return h.handlePRReopened(ctx, &githubPayload)
	default:
		log.Warn(ctx, "Pull request action not handled")
		return nil
	}
}

// processPullRequestReviewEvent processes pull request review webhook events.
// Handles review submitted and dismissed actions by enqueuing reaction sync jobs.
func (h *GitHubHandler) processPullRequestReviewEvent(ctx context.Context, payload []byte, traceID string) error {
	var githubPayload github.PullRequestReviewEvent
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal pull request review payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal pull request review payload: %w", err)
	}

	// Add PR metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"pr_number":     githubPayload.GetPullRequest().GetNumber(),
		"repo":          githubPayload.GetRepo().GetFullName(),
		"author":        githubPayload.GetPullRequest().GetUser().GetLogin(),
		"reviewer":      githubPayload.GetReview().GetUser().GetLogin(),
		"review_state":  githubPayload.GetReview().GetState(),
		"review_action": githubPayload.GetAction(),
	})

	if githubPayload.GetAction() != PRReviewActionSubmitted && githubPayload.GetAction() != PRReviewActionDismissed {
		return nil
	}

	// Create ReactionSyncJob to handle reaction syncing asynchronously
	reactionSyncJobID := uuid.New().String()
	reactionSyncJob := &models.ReactionSyncJob{
		ID:           reactionSyncJobID,
		PRNumber:     githubPayload.GetPullRequest().GetNumber(),
		RepoFullName: githubPayload.GetRepo().GetFullName(),
		TraceID:      traceID,
	}

	// Marshal the ReactionSyncJob as the payload for the Job
	jobPayload, err := json.Marshal(reactionSyncJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal reaction sync job", "error", err)
		return fmt.Errorf("failed to marshal reaction sync job: %w", err)
	}

	// Create Job
	job := &models.Job{
		ID:      reactionSyncJobID,
		Type:    models.JobTypeReactionSync,
		TraceID: reactionSyncJob.TraceID,
		Payload: jobPayload,
	}

	// Enqueue the reaction sync job
	if err := h.cloudTasksService.EnqueueJob(ctx, job); err != nil {
		log.Error(ctx, "Failed to enqueue reaction sync job", "error", err)
		return fmt.Errorf("failed to enqueue reaction sync job: %w", err)
	}

	log.Info(ctx, "Enqueued reaction sync job for PR review",
		"job_id", reactionSyncJobID,
		"review_action", githubPayload.Action)

	return nil
}

// handlePROpened handles pull request opened events.
// Skips draft PRs and delegates to postPRToAllWorkspaces for notification processing.
func (h *GitHubHandler) handlePROpened(ctx context.Context, payload *github.PullRequestEvent) error {
	if payload.GetPullRequest().GetDraft() {
		log.Debug(ctx, "Skipping draft PR")
		return nil
	}

	log.Debug(ctx, "Processing PR opened",
		"title", payload.GetPullRequest().GetTitle(),
	)

	return h.postPRToAllWorkspaces(ctx, payload)
}

// getTraceIDFromContext extracts trace ID from context or returns empty string if not found.
func getTraceIDFromContext(ctx context.Context) string {
	if traceID, ok := ctx.Value(log.TraceIDKey).(string); ok {
		return traceID
	}
	return ""
}

// enqueueWorkspacePRJobs creates and enqueues WorkspacePR jobs for each workspace.
// Enables proper error handling and retries by processing each workspace independently.
func (h *GitHubHandler) enqueueWorkspacePRJobs(
	ctx context.Context,
	payload *github.PullRequestEvent,
	repos []*models.Repo,
	annotatedChannel string,
	prAction string,
) error {
	if len(repos) == 0 {
		log.Info(ctx, "No workspaces to process")
		return nil
	}

	log.Info(ctx, "Enqueuing workspace PR jobs",
		"workspace_count", len(repos),
		"pr_action", prAction)

	// Marshal the GitHub payload once for all jobs
	githubPayloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal GitHub payload: %w", err)
	}

	// Track job enqueue failures
	var enqueueErrors []error
	enqueuedCount := 0

	// Create and enqueue a job for each workspace
	for _, repo := range repos {
		workspacePRJobID := uuid.New().String()
		workspacePRJob := &models.WorkspacePRJob{
			ID:               workspacePRJobID,
			PRNumber:         payload.GetPullRequest().GetNumber(),
			RepoFullName:     payload.GetRepo().GetFullName(),
			WorkspaceID:      repo.WorkspaceID,
			PRAction:         prAction,
			GitHubUserID:     payload.GetPullRequest().GetUser().GetID(),
			GitHubUsername:   payload.GetPullRequest().GetUser().GetLogin(),
			AnnotatedChannel: annotatedChannel,
			TraceID:          getTraceIDFromContext(ctx),
			PRPayload:        githubPayloadBytes,
		}

		// Marshal the WorkspacePR job as the payload for the Job
		jobPayload, err := json.Marshal(workspacePRJob)
		if err != nil {
			log.Error(ctx, "Failed to marshal workspace PR job",
				"error", err,
				"workspace_id", repo.WorkspaceID,
				"job_id", workspacePRJobID)
			enqueueErrors = append(enqueueErrors, fmt.Errorf("failed to marshal workspace PR job for workspace %s: %w", repo.WorkspaceID, err))
			continue
		}

		// Create Job wrapper
		job := &models.Job{
			ID:      workspacePRJobID,
			Type:    models.JobTypeWorkspacePR,
			TraceID: workspacePRJob.TraceID,
			Payload: jobPayload,
		}

		// Enqueue the job
		if err := h.cloudTasksService.EnqueueJob(ctx, job); err != nil {
			log.Error(ctx, "Failed to enqueue workspace PR job",
				"error", err,
				"workspace_id", repo.WorkspaceID,
				"job_id", workspacePRJobID)
			enqueueErrors = append(enqueueErrors, fmt.Errorf("failed to enqueue workspace PR job for workspace %s: %w", repo.WorkspaceID, err))
		} else {
			log.Debug(ctx, "Enqueued workspace PR job",
				"workspace_id", repo.WorkspaceID,
				"job_id", workspacePRJobID)
			enqueuedCount++
		}
	}

	// Return error only if ALL enqueue operations failed
	if len(enqueueErrors) == len(repos) {
		return fmt.Errorf("%w: %v", models.ErrWorkspaceJobsEnqueueFailed, enqueueErrors)
	}

	// Log partial failures but don't fail the entire operation
	if len(enqueueErrors) > 0 {
		log.Warn(ctx, "Some workspace PR job enqueues failed",
			"failed_count", len(enqueueErrors),
			"total_count", len(repos),
			"enqueued_count", enqueuedCount)
	}

	log.Info(ctx, "Successfully enqueued workspace PR jobs",
		"enqueued_count", enqueuedCount,
		"total_count", len(repos))

	return nil
}

// postPRToAllWorkspaces handles the core logic of posting PR notifications to all configured workspaces.
// Shared between handlePROpened, handlePREdited, and handlePRReadyForReview. Supports auto-registration for verified users.
// Uses fan-out approach by enqueuing individual workspace jobs.
func (h *GitHubHandler) postPRToAllWorkspaces(ctx context.Context, payload *github.PullRequestEvent) error {
	authorUserID := payload.GetPullRequest().GetUser().GetID()
	authorUsername := payload.GetPullRequest().GetUser().GetLogin()
	log.Debug(ctx, "Looking up user by GitHub user ID",
		"github_user_id", authorUserID,
		"github_username", authorUsername)
	user, err := h.firestoreService.GetUserByGitHubUserID(ctx, authorUserID)
	if err != nil {
		log.Error(ctx, "Failed to lookup user by GitHub user ID",
			"error", err,
			"github_user_id", authorUserID,
			"github_username", authorUsername,
		)
		return err
	}
	log.Debug(ctx, "User lookup result", "user_found", user != nil)

	// Parse PR directives from description
	annotatedChannel, directives := h.slackService.ExtractChannelAndDirectives(payload.GetPullRequest().GetBody())
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
	repos, err := h.firestoreService.GetReposForAllWorkspaces(ctx, payload.GetRepo().GetFullName())
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

	// Fan-out approach: enqueue individual workspace PR jobs
	// The PR action is extracted from the payload - "opened", "edited", etc.
	return h.enqueueWorkspacePRJobs(ctx, payload, repos, annotatedChannel, payload.GetAction())
}

// determineTargetChannel determines the target Slack channel for PR notifications.
// Priority order: annotated channel from PR description -> user's default channel (if same workspace and notifications enabled).
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

// checkForDuplicateBotMessage checks if bot notification already exists for this PR in the target channel.
// Prevents duplicate notifications by using robust channel comparison that handles both names and IDs.
func (h *GitHubHandler) checkForDuplicateBotMessage(
	ctx context.Context,
	payload *github.PullRequestEvent,
	targetChannel string,
	workspaceID string,
) (bool, error) {
	// Get all bot messages for this PR in the workspace (don't filter by channel initially)
	allBotMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), "", workspaceID, models.MessageSourceBot)
	if err != nil {
		log.Error(ctx, "Failed to check for existing bot messages",
			"error", err,
			"slack_team_id", workspaceID)
		return false, err
	}

	// Check if any existing bot message is in the same channel as targetChannel
	for _, msg := range allBotMessages {
		if channelsMatch(msg.SlackChannelName, msg.SlackChannel, targetChannel) {
			log.Info(ctx, "Bot message already exists for this PR in target channel, skipping duplicate notification",
				"target_channel", targetChannel,
				"existing_channel_name", msg.SlackChannelName,
				"existing_channel_id", msg.SlackChannel,
				"slack_team_id", workspaceID)
			return true, nil
		}
	}

	log.Debug(ctx, "No duplicate bot message found for target channel",
		"target_channel", targetChannel,
		"slack_team_id", workspaceID,
		"total_bot_messages_in_workspace", len(allBotMessages))
	return false, nil
}

// postAndTrackPRMessage posts PR notification to Slack and creates tracked message record.
// Handles user preferences for tagging and impersonation, then saves tracking data to database.
func (h *GitHubHandler) postAndTrackPRMessage(
	ctx context.Context,
	payload *github.PullRequestEvent,
	repo *models.Repo,
	user *models.User,
	targetChannel string,
	annotatedChannel string,
	directives *services.PRDirectives,
) error {
	log.Info(ctx, "Posting PR message to Slack workspace",
		"channel", targetChannel,
		"slack_team_id", repo.WorkspaceID)

	// Calculate PR size (additions + deletions)
	prSize := payload.GetPullRequest().GetAdditions() + payload.GetPullRequest().GetDeletions()

	// Get author's Slack user ID if they're in the same workspace and verified
	var authorSlackUserID string
	if user != nil && user.SlackTeamID == repo.WorkspaceID && user.Verified {
		authorSlackUserID = user.SlackUserID
	}

	// Determine user tagging preference - disabled by default and null treated as disabled
	userTaggingEnabled := user != nil && user.TaggingEnabled

	// Determine impersonation preference - default to enabled if user not found
	impersonationEnabled := true
	if user != nil {
		impersonationEnabled = user.GetImpersonationEnabled()
	}

	// Resolve UserToCC GitHub username to Slack user ID if possible
	var userToCCSlackID string
	if directives.UserToCC != "" {
		userToCCSlackID = h.resolveUserMention(ctx, directives.UserToCC, repo.WorkspaceID)
	}

	timestamp, resolvedChannelID, err := h.slackService.PostPRMessage(
		ctx,
		repo.WorkspaceID,
		targetChannel,
		payload.GetRepo().GetName(),
		payload.GetPullRequest().GetTitle(),
		payload.GetPullRequest().GetUser().GetLogin(),
		payload.GetPullRequest().GetBody(),
		payload.GetPullRequest().GetHTMLURL(),
		prSize,
		authorSlackUserID,
		directives.UserToCC,
		userToCCSlackID,
		directives.CustomEmoji,
		impersonationEnabled,
		userTaggingEnabled,
		user,
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack workspace",
			"error", err,
			"channel", targetChannel,
			"slack_team_id", repo.WorkspaceID,
			"repo_name", payload.GetRepo().GetName(),
			"pr_title", payload.GetPullRequest().GetTitle(),
		)
		return err
	}
	log.Info(ctx, "Posted PR notification to Slack workspace",
		"channel", targetChannel,
		"slack_team_id", repo.WorkspaceID,
	)

	// Determine the proper channel name to store (never store channel IDs in name field)
	originalChannelName := getChannelNameForStorage(targetChannel, annotatedChannel)

	// Create TrackedMessage for the bot notification
	hasDirective := directives.HasReviewDirective
	prAuthorID := payload.GetPullRequest().GetUser().GetID()
	trackedMessage := &models.TrackedMessage{
		PRNumber:           payload.GetPullRequest().GetNumber(),
		RepoFullName:       payload.GetRepo().GetFullName(),
		PRTitle:            payload.GetPullRequest().GetTitle(), // Store title for change detection
		SlackChannel:       resolvedChannelID,
		SlackChannelName:   originalChannelName, // Store original channel name, never ID
		SlackMessageTS:     timestamp,
		SlackTeamID:        repo.WorkspaceID,
		MessageSource:      models.MessageSourceBot,
		PRAuthorGitHubID:   &prAuthorID,         // Store PR author GitHub ID for deletion authorization
		UserToCC:           directives.UserToCC, // Store CC info for future updates
		HasReviewDirective: &hasDirective,       // Track whether directive existed when message was created
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

// processWorkspaceNotification handles PR notification processing for a specific workspace.
// Determines target channel, checks for duplicates, posts message, and syncs reactions with manual messages.
func (h *GitHubHandler) processWorkspaceNotification(
	ctx context.Context,
	payload *github.PullRequestEvent,
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
	if err := h.postAndTrackPRMessage(ctx, payload, repo, user, targetChannel, annotatedChannel, directives); err != nil {
		return err
	}

	// After posting, synchronize reactions with any existing manual messages for this PR in this workspace
	allMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), targetChannel, repo.WorkspaceID, "")
	if err != nil {
		log.Error(ctx, "Failed to get all tracked messages for reaction sync", "error", err)
	} else if len(allMessages) > 1 {
		// There are manual messages to sync with - we don't have current PR status yet, so we'll just log
		log.Info(ctx, "Multiple tracked messages found for PR, reactions will be synced when status updates arrive",
			"total_messages", len(allMessages))
	}

	return nil
}

// handlePREdited handles pull request edited events.
// Processes skip directive changes, channel changes, and re-posting logic.
func (h *GitHubHandler) handlePREdited(ctx context.Context, payload *github.PullRequestEvent) error {
	// Parse directives from PR description
	directives := h.slackService.ParsePRDirectives(payload.GetPullRequest().GetBody())

	log.Info(ctx, "Processing PR edit directives",
		"skip", directives.Skip,
		"channel", directives.Channel,
		"user_to_cc", directives.UserToCC,
		"pr_body", payload.GetPullRequest().GetBody(),
	)

	// If skip directive is found, delete all tracked messages for this PR
	if directives.Skip {
		log.Info(ctx, "Skip directive found, processing skip")
		return h.processSkipDirective(ctx, payload)
	}

	// Check if channel has changed - only for bot messages, not manual ones
	if directives.Channel != "" {
		log.Info(ctx, "Channel directive found, checking for changes",
			"new_channel", directives.Channel,
		)
		channelChanged, err := h.hasChannelChanged(ctx, payload, directives.Channel)
		if err != nil {
			log.Error(ctx, "Failed to check channel changes", "error", err)
			return err
		}
		if channelChanged {
			log.Info(ctx, "Channel change detected, processing migration",
				"new_channel", directives.Channel,
			)
			return h.handleChannelChange(ctx, payload, directives)
		}
		log.Info(ctx, "No channel change detected")
	} else {
		log.Info(ctx, "No channel directive found")
	}

	// Detect what has changed and update existing messages
	changes := h.detectPRChanges(ctx, payload, directives)
	if err := h.updateMessagesForPRChanges(ctx, payload, changes, directives); err != nil {
		log.Error(ctx, "Failed to handle PR changes", "error", err)
		return err
	}

	// No skip directive and no channel change - check if we need to re-post the PR
	log.Info(ctx, "Processing unskip directive")
	return h.handleUnskipDirective(ctx, payload)
}

// getAllTrackedMessagesForPRDirect gets all tracked messages for a PR without retry logic.
func (h *GitHubHandler) getAllTrackedMessagesForPRDirect(
	ctx context.Context, repoFullName string, prNumber int,
) ([]*models.TrackedMessage, error) {
	return h.getAllTrackedMessagesForPR(ctx, repoFullName, prNumber)
}

// compareChannelsForChange compares bot messages with new channel to detect changes.
func (h *GitHubHandler) compareChannelsForChange(ctx context.Context, botMessages []*models.TrackedMessage, newChannel string) bool {
	for _, msg := range botMessages {
		if !channelsMatch(msg.SlackChannelName, msg.SlackChannel, newChannel) {
			log.Info(ctx, "Channel change detected",
				"stored_name", msg.SlackChannelName,
				"stored_id", msg.SlackChannel,
				"new_channel", newChannel,
				"workspace_id", msg.SlackTeamID,
			)
			return true
		}
	}

	log.Info(ctx, "No channel change detected - messages already in target channel",
		"channel", newChannel,
		"message_count", len(botMessages),
	)
	return false
}

// hasChannelChanged checks if the channel directive has changed from where bot messages are currently posted.
// Only considers bot messages, ignoring manual messages.
func (h *GitHubHandler) hasChannelChanged(ctx context.Context, payload *github.PullRequestEvent, newChannel string) (bool, error) {
	log.Info(ctx, "Checking for channel changes",
		"pr_number", payload.GetPullRequest().GetNumber(),
		"repo", payload.GetRepo().GetFullName(),
		"new_channel", newChannel,
	)

	// Get all tracked messages for the PR
	allMessages, err := h.getAllTrackedMessagesForPRDirect(ctx, payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber())

	if err == nil {
		log.Info(ctx, "Found ALL tracked messages for PR",
			"total_messages", len(allMessages),
		)
		for i, msg := range allMessages {
			log.Info(ctx, "Tracked message details",
				"index", i,
				"message_source", msg.MessageSource,
				"channel_name", msg.SlackChannelName,
				"channel_id", msg.SlackChannel,
				"team_id", msg.SlackTeamID,
			)
		}
	}

	// Get all bot messages for this PR across all workspaces
	// Use the already fetched messages if available, otherwise query again
	var botMessages []*models.TrackedMessage
	if len(allMessages) > 0 {
		// Filter for bot messages from what we already have
		for _, msg := range allMessages {
			if msg.MessageSource == models.MessageSourceBot {
				botMessages = append(botMessages, msg)
			}
		}
	} else {
		// Fallback to direct query (shouldn't be needed if retry worked above)
		botMessages, err = h.firestoreService.GetTrackedMessages(ctx,
			payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), "", "", models.MessageSourceBot)
		if err != nil {
			log.Error(ctx, "Failed to get bot tracked messages for channel change check",
				"error", err,
			)
			return false, err
		}
	}

	log.Info(ctx, "Found bot messages for channel comparison",
		"message_count", len(botMessages),
	)

	if len(botMessages) == 0 {
		// No existing bot messages, so this would be posting to a new channel
		log.Info(ctx, "No existing bot messages found, treating as new channel posting")
		return false, nil
	}

	// Check if any bot message is in a different channel
	return h.compareChannelsForChange(ctx, botMessages, newChannel), nil
}

// handleChannelChange handles migration of PR notifications when channel directive changes.
// Deletes bot messages from old channels and posts new message to the specified channel.
func (h *GitHubHandler) handleChannelChange(
	ctx context.Context, payload *github.PullRequestEvent, directives *services.PRDirectives,
) error {
	log.Info(ctx, "Processing channel change - migrating PR notifications",
		"new_channel", directives.Channel,
	)

	// Get all bot messages for this PR across all workspaces
	botMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), "", "", models.MessageSourceBot)
	if err != nil {
		log.Error(ctx, "Failed to get bot tracked messages for channel change",
			"error", err,
		)
		return err
	}

	if len(botMessages) == 0 {
		log.Info(ctx, "No existing bot messages found for channel change - posting to new channel")
		return h.postPRToAllWorkspaces(ctx, payload)
	}

	// Group messages by workspace for deletion
	messagesByWorkspace := make(map[string][]services.MessageRef)
	messageIDs := make([]string, 0, len(botMessages))

	for _, msg := range botMessages {
		messagesByWorkspace[msg.SlackTeamID] = append(messagesByWorkspace[msg.SlackTeamID], services.MessageRef{
			Channel:   msg.SlackChannel,
			Timestamp: msg.SlackMessageTS,
		})
		messageIDs = append(messageIDs, msg.ID)
	}

	// Delete old bot messages from Slack
	for workspaceID, messages := range messagesByWorkspace {
		err := h.slackService.DeleteMultipleMessages(ctx, workspaceID, messages)
		if err != nil {
			log.Error(ctx, "Failed to delete bot messages during channel change",
				"error", err,
				"workspace_id", workspaceID,
				"message_count", len(messages),
			)
			// Continue with other workspaces even if one fails
		} else {
			log.Info(ctx, "Successfully deleted bot messages for channel change",
				"workspace_id", workspaceID,
				"message_count", len(messages),
			)
		}
	}

	// Remove old tracking records from Firestore
	err = h.firestoreService.DeleteTrackedMessages(ctx, messageIDs)
	if err != nil {
		log.Error(ctx, "Failed to delete tracked messages from Firestore during channel change",
			"error", err,
			"message_count", len(messageIDs),
		)
		// Continue with posting new message even if cleanup failed
	}

	// Post new message to the specified channel across all workspaces
	err = h.postPRToAllWorkspaces(ctx, payload)
	if err != nil {
		log.Error(ctx, "Failed to post PR to new channel after migration",
			"error", err,
			"new_channel", directives.Channel,
		)
		return err
	}

	log.Info(ctx, "Successfully processed channel change",
		"deleted_messages", len(botMessages),
		"new_channel", directives.Channel,
	)
	return nil
}

// processSkipDirective handles retroactive deletion of tracked messages when skip directive is added.
// Removes all tracked messages for the PR from Slack and database across all workspaces.
func (h *GitHubHandler) processSkipDirective(ctx context.Context, payload *github.PullRequestEvent) error {
	log.Info(ctx, "Processing skip directive - deleting tracked messages")

	// Get all tracked messages for this PR across all workspaces and channels
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber())
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

	log.Info(ctx, "Successfully processed skip directive",
		"deleted_messages", len(trackedMessages),
	)
	return nil
}

// handleUnskipDirective handles re-posting PRs when skip directive is removed from description.
// Re-posts PR if no tracked messages exist or all existing messages have been deleted by user.
// Special handling for directive changes: if directive status changes, re-post even for deleted messages.
func (h *GitHubHandler) handleUnskipDirective(ctx context.Context, payload *github.PullRequestEvent) error {
	log.Debug(ctx, "No skip directive found, checking if PR needs to be re-posted")

	// Parse current directives to check for directive status changes
	currentDirectives := h.slackService.ParsePRDirectives(payload.GetPullRequest().GetBody())

	// Get all tracked messages for this PR to see if it's already posted
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber())
	if err != nil {
		log.Error(ctx, "Failed to get tracked messages for re-post check",
			"error", err,
		)
		return err
	}

	if len(trackedMessages) == 0 {
		log.Info(ctx, "No tracked messages found - re-posting PR after skip directive removal")

		// Skip draft PRs (same logic as handlePROpened)
		if payload.GetPullRequest().GetDraft() {
			log.Debug(ctx, "Skipping draft PR for re-posting")
			return nil
		}

		// Re-post the PR using the shared logic
		return h.postPRToAllWorkspaces(ctx, payload)
	}

	// Check if all existing bot messages have been deleted by user
	botMessages := make([]*models.TrackedMessage, 0)
	activeBotMessages := make([]*models.TrackedMessage, 0)

	for _, msg := range trackedMessages {
		if msg.MessageSource == models.MessageSourceBot {
			botMessages = append(botMessages, msg)
			if !msg.DeletedByUser {
				activeBotMessages = append(activeBotMessages, msg)
			}
		}
	}

	// If we have bot messages but all are deleted by user, allow re-posting
	if len(botMessages) > 0 && len(activeBotMessages) == 0 {
		// Check if directive status has changed for deleted messages
		firstBotMessage := botMessages[0]
		oldHasDirective := firstBotMessage.HasReviewDirective != nil && *firstBotMessage.HasReviewDirective

		if oldHasDirective != currentDirectives.HasReviewDirective {
			log.Info(ctx, "Directive status changed for deleted messages - re-posting PR",
				"old_has_directive", oldHasDirective,
				"new_has_directive", currentDirectives.HasReviewDirective,
				"total_bot_messages", len(botMessages),
			)
		} else {
			log.Info(ctx, "All bot messages have been deleted by user - re-posting PR",
				"total_bot_messages", len(botMessages),
				"active_bot_messages", len(activeBotMessages),
			)
		}

		// Skip draft PRs (same logic as handlePROpened)
		if payload.GetPullRequest().GetDraft() {
			log.Debug(ctx, "Skipping draft PR for re-posting")
			return nil
		}

		// Re-post the PR using the shared logic
		return h.postPRToAllWorkspaces(ctx, payload)
	}

	log.Debug(ctx, "PR already has active tracked messages, no re-posting needed",
		"total_messages", len(trackedMessages),
		"bot_messages", len(botMessages),
		"active_bot_messages", len(activeBotMessages),
	)
	return nil
}

// detectPRChanges analyzes what has changed in a PR edit that needs to be reflected in Slack messages.
func (h *GitHubHandler) detectPRChanges(
	ctx context.Context, payload *github.PullRequestEvent, directives *services.PRDirectives,
) *PRUpdateChanges {
	changes := &PRUpdateChanges{
		NewTitle:        payload.GetPullRequest().GetTitle(),
		NewCC:           directives.UserToCC,
		NewHasDirective: directives.HasReviewDirective,
	}

	// Check if title changed
	if payload.GetChanges().GetTitle().GetFrom() != "" && payload.GetChanges().GetTitle().GetFrom() != payload.GetPullRequest().GetTitle() {
		changes.TitleChanged = true
		changes.OldTitle = payload.GetChanges().GetTitle().GetFrom()
		log.Info(ctx, "Title change detected",
			"old_title", changes.OldTitle,
			"new_title", changes.NewTitle,
		)
	}

	// For CC and directive changes, we need to get existing bot messages to determine what was stored previously
	// This is more complex because we need to check what the existing messages had
	botMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), "", "", models.MessageSourceBot)
	if err != nil {
		log.Error(ctx, "Failed to get bot messages for change detection", "error", err)
		// Continue without CC change detection if we can't get messages
		return changes
	}

	if len(botMessages) > 0 {
		// Use the first bot message as representative of what was previously stored
		// (all bot messages for the same PR should have the same CC/directive state)
		firstMsg := botMessages[0]

		// Check if CC changed
		if firstMsg.UserToCC != directives.UserToCC {
			changes.CCChanged = true
			changes.OldCC = firstMsg.UserToCC
			log.Info(ctx, "CC change detected",
				"old_cc", changes.OldCC,
				"new_cc", changes.NewCC,
			)
		}

		// Check if directive status changed
		oldHasDirective := firstMsg.HasReviewDirective != nil && *firstMsg.HasReviewDirective
		if oldHasDirective != directives.HasReviewDirective {
			changes.DirectivesChanged = true
			changes.OldHasDirective = oldHasDirective
			log.Info(ctx, "Directive status change detected",
				"old_has_directive", changes.OldHasDirective,
				"new_has_directive", changes.NewHasDirective,
			)
		}
	} else if directives.HasReviewDirective {
		// No existing bot messages, so any directive presence is a change
		changes.DirectivesChanged = true
		changes.OldHasDirective = false
		log.Info(ctx, "New directive detected on PR without existing bot messages")
	}

	return changes
}

// updateMessagesForPRChanges handles updating Slack messages when PR content changes.
// This unified function replaces separate handleTitleChanges and handleCCChanges functions.
func (h *GitHubHandler) updateMessagesForPRChanges(
	ctx context.Context, payload *github.PullRequestEvent, changes *PRUpdateChanges, directives *services.PRDirectives,
) error {
	// If nothing changed, skip
	if !changes.TitleChanged && !changes.CCChanged && !changes.DirectivesChanged {
		log.Debug(ctx, "No relevant changes detected, skipping message updates")
		return nil
	}

	// Get all bot messages for this PR across all workspaces
	botMessages, err := h.firestoreService.GetTrackedMessages(ctx,
		payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber(), "", "", models.MessageSourceBot)
	if err != nil {
		log.Error(ctx, "Failed to get bot messages for PR changes", "error", err)
		return err
	}

	if len(botMessages) == 0 {
		log.Debug(ctx, "No bot messages found to update for PR changes")
		return nil
	}

	// Filter messages that need updating
	messagesToUpdate, messagesToUpdateInDB := h.filterMessagesForPRUpdates(ctx, botMessages, changes)

	if len(messagesToUpdate) == 0 {
		log.Debug(ctx, "No messages need updating based on PR changes")
		return nil
	}

	return h.performPRMessageUpdates(ctx, payload, messagesToUpdate, messagesToUpdateInDB, changes, directives)
}

// filterMessagesForPRUpdates determines which messages need updating based on detected changes.
func (h *GitHubHandler) filterMessagesForPRUpdates(
	ctx context.Context, botMessages []*models.TrackedMessage, changes *PRUpdateChanges,
) ([]*models.TrackedMessage, []*models.TrackedMessage) {
	var messagesToUpdate []*models.TrackedMessage
	var messagesToUpdateInDB []*models.TrackedMessage

	for _, msg := range botMessages {
		// Skip messages that have been deleted by user
		if msg.DeletedByUser {
			log.Debug(ctx, "Skipping message update for deleted message",
				"message_id", msg.ID,
				"message_ts", msg.SlackMessageTS,
				"channel_id", msg.SlackChannel,
			)
			continue
		}

		needsUpdate, changeReasons := h.messageNeedsUpdate(msg, changes)

		if needsUpdate {
			log.Info(ctx, "PR changes detected for message",
				"message_ts", msg.SlackMessageTS,
				"channel_id", msg.SlackChannel,
				"workspace_id", msg.SlackTeamID,
				"old_title", msg.PRTitle,
				"new_title", changes.NewTitle,
				"old_cc", msg.UserToCC,
				"new_cc", changes.NewCC,
				"old_has_directive", msg.HasReviewDirective != nil && *msg.HasReviewDirective,
				"new_has_directive", changes.NewHasDirective,
				"change_reasons", strings.Join(changeReasons, ","),
			)
			messagesToUpdate = append(messagesToUpdate, msg)

			// Create updated message record for database
			updatedMsg := h.createUpdatedMessage(msg, changes)
			messagesToUpdateInDB = append(messagesToUpdateInDB, updatedMsg)
		}
	}

	return messagesToUpdate, messagesToUpdateInDB
}

// messageNeedsUpdate checks if a specific message needs updating based on changes.
func (h *GitHubHandler) messageNeedsUpdate(
	msg *models.TrackedMessage, changes *PRUpdateChanges,
) (bool, []string) {
	needsUpdate := false
	changeReasons := []string{}

	// Check if title needs updating
	if changes.TitleChanged && (msg.PRTitle == "" || msg.PRTitle == changes.OldTitle) {
		needsUpdate = true
		changeReasons = append(changeReasons, "title")
	}

	// Check if CC needs updating
	if changes.CCChanged || changes.DirectivesChanged {
		// Case 1: Old message without directive support, now has a directive (even if empty)
		if (msg.HasReviewDirective == nil || !*msg.HasReviewDirective) && changes.NewHasDirective {
			needsUpdate = true
			changeReasons = append(changeReasons, "directive_added")
		}
		// Case 2: Message had directive, CC content changed
		if msg.HasReviewDirective != nil && *msg.HasReviewDirective && msg.UserToCC != changes.NewCC {
			needsUpdate = true
			changeReasons = append(changeReasons, "cc_changed")
		}
		// Case 3: Message had directive, directive removed
		if msg.HasReviewDirective != nil && *msg.HasReviewDirective && !changes.NewHasDirective {
			needsUpdate = true
			changeReasons = append(changeReasons, "directive_removed")
		}
	}

	return needsUpdate, changeReasons
}

// createUpdatedMessage creates an updated TrackedMessage with new field values.
func (h *GitHubHandler) createUpdatedMessage(msg *models.TrackedMessage, changes *PRUpdateChanges) *models.TrackedMessage {
	updatedMsg := *msg // Copy the struct

	if changes.TitleChanged {
		updatedMsg.PRTitle = changes.NewTitle
	}

	if changes.CCChanged || changes.DirectivesChanged {
		updatedMsg.UserToCC = changes.NewCC
		hasDirective := changes.NewHasDirective
		updatedMsg.HasReviewDirective = &hasDirective
	}

	return &updatedMsg
}

// performPRMessageUpdates executes the actual Slack and database updates.
func (h *GitHubHandler) performPRMessageUpdates(
	ctx context.Context, payload *github.PullRequestEvent,
	messagesToUpdate, messagesToUpdateInDB []*models.TrackedMessage,
	changes *PRUpdateChanges, directives *services.PRDirectives,
) error {
	log.Info(ctx, "Updating messages due to PR changes",
		"message_count", len(messagesToUpdate),
		"title_changed", changes.TitleChanged,
		"cc_changed", changes.CCChanged,
		"new_title", changes.NewTitle,
		"new_cc", changes.NewCC,
	)

	// Get user information once (shared across all messages)
	var user *models.User
	if payload.GetPullRequest().GetUser().GetID() > 0 {
		var err error
		user, err = h.firestoreService.GetUserByGitHubUserID(ctx, payload.GetPullRequest().GetUser().GetID())
		if err != nil {
			log.Error(ctx, "Failed to lookup user for PR update", "error", err)
		}
	}

	prSize := payload.GetPullRequest().GetAdditions() + payload.GetPullRequest().GetDeletions()

	// Update each message in Slack and database
	for i, msg := range messagesToUpdate {
		err := h.updateSingleMessageForPRChanges(ctx, payload, msg, directives, user, prSize)
		if err != nil {
			log.Error(ctx, "Failed to update message for PR changes", "error", err)
			continue
		}

		// Update the message record in database
		err = h.firestoreService.UpdateTrackedMessage(ctx, messagesToUpdateInDB[i])
		if err != nil {
			log.Error(ctx, "Failed to update tracked message with PR changes",
				"error", err, "message_id", msg.ID)
		}
	}

	log.Info(ctx, "Completed PR change updates for bot messages",
		"total_messages", len(messagesToUpdate),
		"new_title", changes.NewTitle,
		"new_cc", changes.NewCC,
	)

	return nil
}

// updateSingleMessageForPRChanges updates a single message with the PR changes.
func (h *GitHubHandler) updateSingleMessageForPRChanges(
	ctx context.Context, payload *github.PullRequestEvent, msg *models.TrackedMessage,
	directives *services.PRDirectives, user *models.User, prSize int,
) error {
	// Resolve CC username to Slack user ID if possible
	var userToCCSlackID string
	if directives.UserToCC != "" {
		userToCCSlackID = h.resolveUserMention(ctx, directives.UserToCC, msg.SlackTeamID)
	}

	// Get author's Slack user ID if they're in the same workspace and verified
	var authorSlackUserID string
	if user != nil && user.SlackTeamID == msg.SlackTeamID && user.Verified {
		authorSlackUserID = user.SlackUserID
	}

	// Determine user tagging preference
	userTaggingEnabled := user != nil && user.TaggingEnabled

	// Update the message in Slack with all changes
	return h.slackService.UpdatePRMessage(
		ctx,
		msg.SlackTeamID,
		msg.SlackChannel,
		msg.SlackMessageTS,
		payload.GetRepo().GetFullName(),     // Use full name for consistency
		payload.GetPullRequest().GetTitle(), // Use current title
		payload.GetPullRequest().GetUser().GetLogin(),
		payload.GetPullRequest().GetBody(),
		payload.GetPullRequest().GetHTMLURL(),
		prSize,
		authorSlackUserID,
		directives.UserToCC, // Use current CC
		userToCCSlackID,
		directives.CustomEmoji,
		userTaggingEnabled,
		user,
	)
}

// handlePRClosed handles pull request closed events.
// Adds appropriate emoji reactions (merged/closed) to all tracked messages across workspaces.
func (h *GitHubHandler) handlePRClosed(ctx context.Context, payload *github.PullRequestEvent) error {
	// Get all tracked messages for this PR across all workspaces and channels
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, payload.GetRepo().GetFullName(), payload.GetPullRequest().GetNumber())
	if err != nil {
		log.Error(ctx, "Failed to get tracked messages for PR closed reaction",
			"error", err,
			"merged", payload.GetPullRequest().GetMerged(),
		)
		return err
	}
	if len(trackedMessages) == 0 {
		log.Warn(ctx, "No tracked messages found for PR closed reaction",
			"merged", payload.GetPullRequest().GetMerged(),
		)
		return nil
	}

	// Add reaction to all tracked messages
	emoji := utils.GetEmojiForPRState(PRActionClosed, payload.GetPullRequest().GetMerged(), h.emojiConfig)
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
					"merged", payload.GetPullRequest().GetMerged(),
				)
				// Continue with other teams even if one fails
			}
		}
	}

	log.Info(ctx, "PR closed reactions synchronized across tracked messages",
		"merged", payload.GetPullRequest().GetMerged(),
		"emoji", emoji,
		"message_count", len(trackedMessages),
	)
	return nil
}

// handlePRReopened handles pull request reopened events.
// Triggers a reaction sync job to remove closed reactions and update with current state.
func (h *GitHubHandler) handlePRReopened(ctx context.Context, payload *github.PullRequestEvent) error {
	log.Info(ctx, "Processing PR reopened event")

	// Create ReactionSyncJob to handle reaction syncing asynchronously
	reactionSyncJobID := uuid.New().String()
	reactionSyncJob := &models.ReactionSyncJob{
		ID:           reactionSyncJobID,
		PRNumber:     payload.GetPullRequest().GetNumber(),
		RepoFullName: payload.GetRepo().GetFullName(),
		TraceID:      getTraceIDFromContext(ctx),
	}

	// Marshal the ReactionSyncJob as the payload for the Job
	jobPayload, err := json.Marshal(reactionSyncJob)
	if err != nil {
		log.Error(ctx, "Failed to marshal reaction sync job", "error", err)
		return fmt.Errorf("failed to marshal reaction sync job: %w", err)
	}

	// Create Job
	job := &models.Job{
		ID:      reactionSyncJobID,
		Type:    models.JobTypeReactionSync,
		TraceID: reactionSyncJob.TraceID,
		Payload: jobPayload,
	}

	// Enqueue the reaction sync job
	if err := h.cloudTasksService.EnqueueJob(ctx, job); err != nil {
		log.Error(ctx, "Failed to enqueue reaction sync job", "error", err)
		return fmt.Errorf("failed to enqueue reaction sync job: %w", err)
	}

	log.Info(ctx, "Enqueued reaction sync job for PR reopened",
		"job_id", reactionSyncJobID)

	return nil
}

// handlePRReadyForReview handles pull request ready_for_review events.
// Processes draft PRs that become ready for review by posting notifications to all workspaces.
func (h *GitHubHandler) handlePRReadyForReview(ctx context.Context, payload *github.PullRequestEvent) error {
	log.Debug(ctx, "Processing PR ready for review",
		"title", payload.GetPullRequest().GetTitle(),
	)

	// Delegate to shared logic using fan-out approach
	return h.postPRToAllWorkspaces(ctx, payload)
}

// getAllTrackedMessagesForPR retrieves all tracked messages for a specific PR across all configured workspaces.
// Queries each workspace where the repository is configured and aggregates results.
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

// attemptAutoRegistration attempts automatic repository registration for verified users.
// Validates workspace membership and GitHub installation access before creating repository configuration.
// Returns created repo on success, nil if not possible, or error if registration fails.
func (h *GitHubHandler) attemptAutoRegistration(
	ctx context.Context, payload *github.PullRequestEvent, user *models.User,
) (*models.Repo, error) {
	if user != nil && user.Verified && user.NotificationsEnabled {
		log.Info(ctx, "Attempting auto-registration for verified user's workspace",
			"github_username", user.GitHubUsername,
			"github_user_id", user.GitHubUserID,
			"slack_team_id", user.SlackTeamID,
			"repo", payload.GetRepo().GetFullName())

		// Verify workspace membership: ensure PR author belongs to the workspace being registered to
		if !h.verifyWorkspaceMembership(ctx, user, payload) {
			log.Warn(ctx, "Cannot auto-register repository - PR author not member of target workspace",
				"github_user_id", user.GitHubUserID,
				"github_username", user.GitHubUsername,
				"user_workspace", user.SlackTeamID,
				"repo", payload.GetRepo().GetFullName())
			return nil, nil // Not an error, just means auto-registration is not possible
		}

		// Validate that the workspace has a GitHub installation for this repository
		_, err := h.githubService.ValidateWorkspaceInstallationAccess(ctx, payload.GetRepo().GetFullName(), user.SlackTeamID)
		if err != nil {
			log.Warn(ctx, "Cannot auto-register repository - workspace lacks GitHub installation",
				"error", err,
				"repo", payload.GetRepo().GetFullName(),
				"workspace_id", user.SlackTeamID)
			return nil, nil // Not an error, just means auto-registration is not possible
		}

		log.Info(ctx, "Auto-registering repository for verified user's workspace",
			"github_username", user.GitHubUsername,
			"slack_team_id", user.SlackTeamID,
			"repo", payload.GetRepo().GetFullName())

		repo := &models.Repo{
			ID:           user.SlackTeamID + "#" + payload.GetRepo().GetFullName(), // Correct format: {workspace_id}#{repo_full_name}
			RepoFullName: payload.GetRepo().GetFullName(),
			WorkspaceID:  user.SlackTeamID,
			Enabled:      true,
		}

		err = h.firestoreService.CreateRepoIfNotExists(ctx, repo)
		if err != nil {
			// Check if error is due to repository already existing
			if errors.Is(err, services.ErrRepoAlreadyExists) {
				log.Info(ctx, "Repository already registered during concurrent auto-registration attempt",
					"repo", repo.ID,
					"slack_team_id", repo.WorkspaceID)
				// Return the repo struct even though we didn't create it, so notification can proceed
				return repo, nil
			}
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

// verifyWorkspaceMembership ensures PR author belongs to target workspace for auto-registration.
// Prevents unauthorized cross-workspace repository registration by validating GitHub user ID match.
func (h *GitHubHandler) verifyWorkspaceMembership(
	ctx context.Context, user *models.User, payload *github.PullRequestEvent,
) bool {
	// Extract PR author's numeric GitHub user ID from webhook payload
	prAuthorUserID := payload.GetPullRequest().GetUser().GetID()

	// Verify the user record's GitHub ID matches the PR author's ID
	if user.GitHubUserID != prAuthorUserID {
		log.Warn(ctx, "User record GitHub ID mismatch with PR author",
			"user_github_id", user.GitHubUserID,
			"pr_author_github_id", prAuthorUserID,
			"user_slack_team", user.SlackTeamID,
		)
		return false
	}

	// Additional defensive check: user should belong to the workspace being registered to
	// (This is somewhat redundant since auto-registration uses user.SlackTeamID as target workspace,
	//  but it's good defensive programming)
	if user.SlackTeamID == "" {
		log.Warn(ctx, "User has no associated Slack workspace",
			"github_user_id", user.GitHubUserID,
			"github_username", user.GitHubUsername,
		)
		return false
	}

	log.Debug(ctx, "Workspace membership verified",
		"github_user_id", user.GitHubUserID,
		"slack_team_id", user.SlackTeamID,
	)

	return true
}

// validateInstallationPayload validates GitHub App installation webhook payload structure.
// Ensures required fields like action and installation are present in installation events.
func (h *GitHubHandler) validateInstallationPayload(payload []byte) error {
	var installationPayload map[string]interface{}
	if err := json.Unmarshal(payload, &installationPayload); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}

	if _, exists := installationPayload["action"]; !exists {
		return ErrMissingAction
	}

	if _, exists := installationPayload["installation"]; !exists {
		return ErrMissingInstallation
	}

	return nil
}

// validateInstallationRepositoriesPayload validates installation_repositories webhook payload structure.
// Ensures required fields for repository addition/removal events are present.
func (h *GitHubHandler) validateInstallationRepositoriesPayload(payload []byte) error {
	var installationReposPayload map[string]interface{}
	if err := json.Unmarshal(payload, &installationReposPayload); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}

	if _, exists := installationReposPayload["action"]; !exists {
		return ErrMissingAction
	}

	if _, exists := installationReposPayload["installation"]; !exists {
		return ErrMissingInstallation
	}

	return nil
}

// processInstallationEvent processes GitHub App installation webhook events.
// Handles installation created, deleted, suspended, unsuspended, and new_permissions_accepted actions.
func (h *GitHubHandler) processInstallationEvent(ctx context.Context, payload []byte) error {
	var githubPayload github.InstallationEvent
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal installation payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal installation payload: %w", err)
	}

	// Add installation metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"installation_id":     githubPayload.Installation.ID,
		"account_login":       githubPayload.Installation.Account.Login,
		"account_type":        githubPayload.Installation.Account.Type,
		"installation_action": githubPayload.Action,
	})

	log.Info(ctx, "Handling installation event")

	switch githubPayload.GetAction() {
	case InstallationActionCreated:
		return h.handleInstallationCreated(ctx, &githubPayload)
	case InstallationActionDeleted:
		return h.handleInstallationDeleted(ctx, &githubPayload)
	case InstallationActionSuspend:
		return h.handleInstallationSuspend(ctx, &githubPayload)
	case InstallationActionUnsuspend:
		return h.handleInstallationUnsuspend(ctx, &githubPayload)
	case InstallationActionNewPermissions:
		return h.handleInstallationNewPermissions(ctx, &githubPayload)
	default:
		log.Warn(ctx, "Installation action not handled")
		return nil
	}
}

// handleInstallationCreated handles GitHub App installation created events.
// Creates orphaned installation records for direct GitHub installs without workspace association.
func (h *GitHubHandler) handleInstallationCreated(ctx context.Context, payload *github.InstallationEvent) error {
	log.Info(ctx, "Processing installation created event")

	// Check if installation already exists and has workspace association
	existingInstallation, err := h.firestoreService.GetGitHubInstallationByID(ctx, payload.GetInstallation().GetID())
	if err != nil && !errors.Is(err, services.ErrGitHubInstallationNotFound) {
		log.Error(ctx, "Failed to check existing installation", "error", err)
		return fmt.Errorf("failed to check existing installation: %w", err)
	}

	// If installation exists and has workspace association, we're done
	// This happens when the installation was created via the combined OAuth + installation flow
	if existingInstallation != nil && existingInstallation.SlackWorkspaceID != "" {
		log.Info(ctx, "Installation already exists with workspace association, skipping webhook processing",
			"installation_id", existingInstallation.ID,
			"workspace_id", existingInstallation.SlackWorkspaceID,
			"account_login", existingInstallation.AccountLogin)
		return nil
	}

	// Extract repository list for selected repositories
	var repositories []string
	if payload.GetInstallation().GetRepositorySelection() == RepositorySelectionSelected {
		for _, repo := range payload.Repositories {
			repositories = append(repositories, repo.GetFullName())
		}
	}

	// Create or update GitHubInstallation record without workspace association
	// These are orphaned installations that came from direct GitHub installs
	installation := &models.GitHubInstallation{
		ID:                  payload.GetInstallation().GetID(),
		AccountLogin:        payload.GetInstallation().GetAccount().GetLogin(),
		AccountType:         payload.GetInstallation().GetAccount().GetType(),
		AccountID:           payload.GetInstallation().GetAccount().GetID(),
		RepositorySelection: payload.GetInstallation().GetRepositorySelection(),
		Repositories:        repositories,
		InstalledAt:         time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Delete existing installation record if it exists (handles reinstalls from orphaned installations)
	// This prevents duplicates since we don't listen to installation.deleted events
	if existingInstallation != nil {
		log.Info(ctx, "Deleting orphaned installation record before creating new one",
			"installation_id", payload.GetInstallation().GetID(),
			"account_login", existingInstallation.AccountLogin)

		err = h.firestoreService.DeleteGitHubInstallation(ctx, payload.GetInstallation().GetID())
		if err != nil {
			// Check if the error is "not found" - this is actually success for our purposes
			if errors.Is(err, services.ErrGitHubInstallationNotFound) {
				log.Debug(ctx, "Installation record already removed during cleanup",
					"installation_id", payload.GetInstallation().GetID())
			} else {
				// This is a real deletion failure (database connectivity, etc.)
				log.Error(ctx, "Failed to delete existing installation record",
					"error", err,
					"installation_id", payload.GetInstallation().GetID())
				return fmt.Errorf("failed to clean up existing installation record: %w", err)
			}
		} else {
			log.Info(ctx, "Successfully cleaned up existing installation record",
				"installation_id", payload.GetInstallation().GetID())
		}
	}

	// Save installation to database
	err = h.firestoreService.CreateGitHubInstallation(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to save GitHub installation",
			"error", err,
		)
		return fmt.Errorf("failed to save GitHub installation: %w", err)
	}

	log.Warn(ctx, "Created orphaned GitHub installation without workspace association",
		"installation_id", installation.ID,
		"account_login", installation.AccountLogin,
		"repository_selection", installation.RepositorySelection,
		"repository_count", len(repositories),
		"repository_list", strings.Join(repositories, ","),
		"note", "Installation will not be usable until associated with a Slack workspace via the Install GitHub App flow")

	return nil
}

// handleInstallationDeleted handles GitHub App installation deleted events.
// Removes installation record from database when GitHub App is uninstalled.
func (h *GitHubHandler) handleInstallationDeleted(ctx context.Context, payload *github.InstallationEvent) error {
	log.Info(ctx, "Processing installation deleted event")

	// Delete the installation record from Firestore
	err := h.firestoreService.DeleteGitHubInstallation(ctx, payload.GetInstallation().GetID())
	if err != nil {
		// Check if the error is "not found" - this might happen if the installation
		// was already cleaned up or never existed in our database
		if errors.Is(err, services.ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Installation record not found during deletion - may have been already cleaned up",
				"installation_id", payload.GetInstallation().GetID())
			return nil
		}
		log.Error(ctx, "Failed to delete GitHub installation record",
			"error", err,
			"installation_id", payload.GetInstallation().GetID())
		return fmt.Errorf("failed to delete GitHub installation: %w", err)
	}

	log.Info(ctx, "Successfully processed installation deletion",
		"installation_id", payload.GetInstallation().GetID(),
		"account_login", payload.GetInstallation().GetAccount().GetLogin())

	return nil
}

// handleInstallationSuspend handles GitHub App installation suspended events.
// Updates installation record with suspension timestamp when access is suspended.
func (h *GitHubHandler) handleInstallationSuspend(ctx context.Context, payload *github.InstallationEvent) error {
	log.Info(ctx, "Processing installation suspend event")

	// Get existing installation to update it
	installation, err := h.firestoreService.GetGitHubInstallationByID(ctx, payload.GetInstallation().GetID())
	if err != nil {
		if errors.Is(err, services.ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Installation not found for suspend event - creating suspended installation record",
				"installation_id", payload.GetInstallation().GetID())
			// Create a new installation record in suspended state
			installation = &models.GitHubInstallation{
				ID:                  payload.GetInstallation().GetID(),
				AccountLogin:        payload.GetInstallation().GetAccount().GetLogin(),
				AccountType:         payload.GetInstallation().GetAccount().GetType(),
				AccountID:           payload.GetInstallation().GetAccount().GetID(),
				RepositorySelection: payload.GetInstallation().GetRepositorySelection(),
				InstalledAt:         time.Now(), // We don't have the original install time
				UpdatedAt:           time.Now(),
			}
		} else {
			log.Error(ctx, "Failed to get installation for suspend", "error", err)
			return fmt.Errorf("failed to get installation for suspend: %w", err)
		}
	}

	// Set suspended status
	suspendedAt := time.Now()
	installation.SuspendedAt = &suspendedAt
	installation.UpdatedAt = time.Now()

	// Update or create the installation record
	if err = h.firestoreService.UpdateGitHubInstallation(ctx, installation); err != nil {
		// If update fails, try to create (in case it's a new record)
		if err = h.firestoreService.CreateGitHubInstallation(ctx, installation); err != nil {
			log.Error(ctx, "Failed to update/create suspended installation", "error", err)
			return fmt.Errorf("failed to save suspended installation: %w", err)
		}
	}

	log.Info(ctx, "Successfully processed installation suspension",
		"installation_id", payload.GetInstallation().GetID(),
		"account_login", payload.GetInstallation().GetAccount().GetLogin(),
		"suspended_at", suspendedAt)

	return nil
}

// handleInstallationUnsuspend handles GitHub App installation unsuspended events.
// Clears suspension timestamp when installation access is restored.
func (h *GitHubHandler) handleInstallationUnsuspend(ctx context.Context, payload *github.InstallationEvent) error {
	log.Info(ctx, "Processing installation unsuspend event")

	// Get existing installation to update it
	installation, err := h.firestoreService.GetGitHubInstallationByID(ctx, payload.GetInstallation().GetID())
	if err != nil {
		if errors.Is(err, services.ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Installation not found for unsuspend event - this shouldn't normally happen",
				"installation_id", payload.GetInstallation().GetID())
			return nil
		}
		log.Error(ctx, "Failed to get installation for unsuspend", "error", err)
		return fmt.Errorf("failed to get installation for unsuspend: %w", err)
	}

	// Clear suspended status
	installation.SuspendedAt = nil
	installation.UpdatedAt = time.Now()

	err = h.firestoreService.UpdateGitHubInstallation(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to update unsuspended installation", "error", err)
		return fmt.Errorf("failed to save unsuspended installation: %w", err)
	}

	log.Info(ctx, "Successfully processed installation unsuspension",
		"installation_id", payload.GetInstallation().GetID(),
		"account_login", payload.GetInstallation().GetAccount().GetLogin())

	return nil
}

// handleInstallationNewPermissions handles installation new_permissions_accepted events.
// Logs permission changes for audit purposes when new permissions are granted to the app.
func (h *GitHubHandler) handleInstallationNewPermissions(ctx context.Context, payload *github.InstallationEvent) error {
	log.Info(ctx, "Processing installation new permissions accepted event",
		"installation_id", payload.GetInstallation().GetID(),
		"account_login", payload.GetInstallation().GetAccount().GetLogin())

	// For now, we just log this event for audit purposes
	// In the future, we could track permission changes or update stored scopes

	return nil
}

// processInstallationRepositoriesEvent processes installation_repositories webhook events.
// Handles repository additions and removals from GitHub App installations.
func (h *GitHubHandler) processInstallationRepositoriesEvent(ctx context.Context, payload []byte) error {
	var githubPayload github.InstallationRepositoriesEvent
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		log.Error(ctx, "Failed to unmarshal installation_repositories payload",
			"error", err,
			"payload_size", len(payload),
		)
		return fmt.Errorf("failed to unmarshal installation_repositories payload: %w", err)
	}

	// Add installation metadata to context for all subsequent log calls
	ctx = log.WithFields(ctx, log.LogFields{
		"installation_id":     githubPayload.Installation.ID,
		"account_login":       githubPayload.Installation.Account.Login,
		"account_type":        githubPayload.Installation.Account.Type,
		"repositories_action": githubPayload.Action,
	})

	log.Info(ctx, "Handling installation_repositories event")

	switch githubPayload.GetAction() {
	case InstallationRepositoriesActionAdded:
		return h.handleInstallationRepositoriesAdded(ctx, &githubPayload)
	case InstallationRepositoriesActionRemoved:
		return h.handleInstallationRepositoriesRemoved(ctx, &githubPayload)
	default:
		log.Warn(ctx, "Installation repositories action not handled")
		return nil
	}
}

// handleInstallationRepositoriesAdded handles repositories added to GitHub App installation.
// Updates installation record with newly added repositories for selected repository installations.
func (h *GitHubHandler) handleInstallationRepositoriesAdded(ctx context.Context, payload *github.InstallationRepositoriesEvent) error {
	log.Info(ctx, "Processing installation repositories added event")

	// Get existing installation to update repository list
	installation, err := h.firestoreService.GetGitHubInstallationByID(ctx, payload.GetInstallation().GetID())
	if err != nil {
		if errors.Is(err, services.ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Installation not found for repositories added event",
				"installation_id", payload.GetInstallation().GetID())
			return nil
		}
		log.Error(ctx, "Failed to get installation for repositories update", "error", err)
		return fmt.Errorf("failed to get installation for repositories update: %w", err)
	}

	// Extract added repositories from payload
	addedRepos := make([]string, 0, len(payload.RepositoriesAdded))
	for _, repo := range payload.RepositoriesAdded {
		addedRepos = append(addedRepos, repo.GetFullName())
	}

	// Update installation's repository list (add new repos to existing list)
	if installation.RepositorySelection == RepositorySelectionSelected {
		existingRepos := make(map[string]bool)
		for _, repo := range installation.Repositories {
			existingRepos[repo] = true
		}

		// Add new repositories to the list
		for _, repo := range addedRepos {
			if !existingRepos[repo] {
				installation.Repositories = append(installation.Repositories, repo)
			}
		}
	}

	installation.UpdatedAt = time.Now()

	err = h.firestoreService.UpdateGitHubInstallation(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to update installation with added repositories", "error", err)
		return fmt.Errorf("failed to update installation repositories: %w", err)
	}

	log.Info(ctx, "Successfully processed installation repositories added event",
		"installation_id", payload.GetInstallation().GetID(),
		"added_repositories", addedRepos,
		"total_repositories", len(installation.Repositories))

	return nil
}

// handleInstallationRepositoriesRemoved handles repositories removed from GitHub App installation.
// Updates installation record by removing specified repositories from the repository list.
func (h *GitHubHandler) handleInstallationRepositoriesRemoved(ctx context.Context, payload *github.InstallationRepositoriesEvent) error {
	log.Info(ctx, "Processing installation repositories removed event")

	// Get existing installation to update repository list
	installation, err := h.firestoreService.GetGitHubInstallationByID(ctx, payload.GetInstallation().GetID())
	if err != nil {
		if errors.Is(err, services.ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Installation not found for repositories removed event",
				"installation_id", payload.GetInstallation().GetID())
			return nil
		}
		log.Error(ctx, "Failed to get installation for repositories update", "error", err)
		return fmt.Errorf("failed to get installation for repositories update: %w", err)
	}

	// Extract removed repositories from payload
	removedRepos := make([]string, 0, len(payload.RepositoriesRemoved))
	for _, repo := range payload.RepositoriesRemoved {
		removedRepos = append(removedRepos, repo.GetFullName())
	}

	// Update installation's repository list (remove repos from existing list)
	if installation.RepositorySelection == RepositorySelectionSelected {
		removedReposMap := make(map[string]bool)
		for _, repo := range removedRepos {
			removedReposMap[repo] = true
		}

		// Filter out removed repositories
		var updatedRepos []string
		for _, repo := range installation.Repositories {
			if !removedReposMap[repo] {
				updatedRepos = append(updatedRepos, repo)
			}
		}
		installation.Repositories = updatedRepos
	}

	installation.UpdatedAt = time.Now()

	err = h.firestoreService.UpdateGitHubInstallation(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to update installation with removed repositories", "error", err)
		return fmt.Errorf("failed to update installation repositories: %w", err)
	}

	log.Info(ctx, "Successfully processed installation repositories removed event",
		"installation_id", payload.GetInstallation().GetID(),
		"removed_repositories", removedRepos,
		"remaining_repositories", len(installation.Repositories))

	return nil
}

// processGitHubAppAuthEvent processes GitHub App authorization webhook events.
// Currently logs events for audit purposes as OAuth flow is handled via callback endpoints.
func (h *GitHubHandler) processGitHubAppAuthEvent(ctx context.Context, payload []byte) error {
	log.Info(ctx, "Processing GitHub App authorization event")

	// For now, we just log the event. The actual OAuth flow is handled
	// via the OAuth callback endpoint, not through webhooks.
	// This webhook confirms that the authorization happened.

	return nil
}

// resolveUserMention attempts to resolve a GitHub username to a Slack user ID.
// Returns the Slack user ID if the user is found, verified, and in the target workspace.
// Returns empty string if no mapping is found, allowing fallback to plain text mention.
func (h *GitHubHandler) resolveUserMention(ctx context.Context, githubUsername, workspaceID string) string {
	if githubUsername == "" || workspaceID == "" {
		return ""
	}

	// Look up user by GitHub username and workspace ID
	user, err := h.firestoreService.GetUserByGitHubUsernameAndWorkspace(ctx, githubUsername, workspaceID)
	if err != nil {
		log.Debug(ctx, "Failed to find user by GitHub username and workspace for mention",
			"github_username", githubUsername,
			"workspace_id", workspaceID,
			"error", err,
		)
		return ""
	}

	// Ensure user is not nil and verified
	if user == nil {
		log.Debug(ctx, "No user found for GitHub username in workspace",
			"github_username", githubUsername,
			"workspace_id", workspaceID,
		)
		return ""
	}

	if !user.Verified {
		log.Debug(ctx, "User found but not verified",
			"github_username", githubUsername,
			"workspace_id", workspaceID,
			"verified", user.Verified,
		)
		return ""
	}

	log.Debug(ctx, "Resolved GitHub username to Slack user ID for mention",
		"github_username", githubUsername,
		"slack_user_id", user.SlackUserID,
		"workspace_id", workspaceID,
	)
	return user.SlackUserID
}
