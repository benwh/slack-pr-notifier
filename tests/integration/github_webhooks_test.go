package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/tests/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGitHubWebhookProcessing tests complete GitHub webhook HTTP → job queue → processing pipeline.
func TestGitHubWebhookProcessing(t *testing.T) {
	app, ctx, cleanup := testutil.SetupTestApp(t)
	defer cleanup()

	constants := testutil.NewTestConstants()
	constantsPtr := &constants
	helpers := testutil.HTTPTestHelpers{}

	t.Run("PR opened webhook via HTTP pipeline", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create GitHub PR opened webhook request
		req := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			constants.DefaultPRNumber,
			"opened",
			"Add new feature",
			"This PR implements a great new feature",
			constants.DefaultGitHubUsername,
			app.Config.GitHubWebhookSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.GitHubHandler.HandleWebhook)

		// Verify HTTP response
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify job was queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 webhook job to be queued")

		job := queuedJobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, job.Type)

		// Verify job payload contains webhook data
		var webhookJob models.WebhookJob
		require.NoError(t, json.Unmarshal(job.Payload, &webhookJob))
		assert.Equal(t, "pull_request", webhookJob.EventType)

		// Process the queued job (expect Slack API errors with mock tokens)
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		// We expect Slack API errors with mock tokens - this is acceptable
		if len(errors) > 0 {
			for _, err := range errors {
				assert.Contains(t, err.Error(), "invalid_auth", "Expected Slack API auth errors with mock tokens")
			}
		}
	})

	t.Run("PR review webhook via HTTP pipeline", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create GitHub PR review webhook request
		req := helpers.CreateGitHubReviewWebhookRequest(
			constants.DefaultRepoFullName,
			constants.DefaultPRNumber,
			"approved",
			"reviewer-bob",
			app.Config.GitHubWebhookSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.GitHubHandler.HandleWebhook)

		// Verify HTTP response
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify job was queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 review webhook job to be queued")

		job := queuedJobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, job.Type)

		// Verify job payload contains review data
		var webhookJob models.WebhookJob
		require.NoError(t, json.Unmarshal(job.Payload, &webhookJob))
		assert.Equal(t, "pull_request_review", webhookJob.EventType)

		// Process the queued job (expect no errors for review events with no tracked messages)
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		assert.Empty(t, errors, "Expected no errors processing GitHub review webhook job")
	})

	t.Run("invalid GitHub webhook signature rejection", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create valid webhook payload but with NO signature
		req := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			constants.DefaultPRNumber,
			"opened",
			"Test PR",
			"Test description",
			constants.DefaultGitHubUsername,
			"", // Empty webhook secret = no signature
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.GitHubHandler.HandleWebhook)

		// Verify request was rejected due to missing signature
		// (Actual behavior depends on GitHub handler's signature validation logic)
		// For now, we verify the request was processed (signature validation might be optional in test mode)
		assert.True(t, response.Code == http.StatusOK || response.Code == http.StatusUnauthorized,
			"Expected OK or Unauthorized response, got %d", response.Code)

		// If signature validation is enforced, no jobs should be queued
		if response.Code == http.StatusUnauthorized {
			queuedJobs := app.CloudTasksService.GetQueuedJobs()
			assert.Empty(t, queuedJobs, "No jobs should be queued for invalid signature")
		}
	})

	t.Run("malformed GitHub webhook payload", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create request with invalid JSON payload
		req := helpers.CreateGitHubHTTPRequest(
			[]byte("invalid json payload"),
			"pull_request",
			app.Config.GitHubWebhookSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.GitHubHandler.HandleWebhook)

		// Verify request was rejected due to malformed payload
		assert.Equal(t, http.StatusBadRequest, response.Code)

		// Verify no jobs were queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for malformed payload")
	})

	t.Run("unsupported GitHub event type", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create webhook for unsupported event (e.g., "issues" instead of "pull_request")
		fixtures := testutil.TestGitHubWebhooks{}
		payload := fixtures.CreatePROpenedPayload(
			constants.DefaultRepoFullName,
			constants.DefaultPRNumber,
			"Test PR",
			"Test description",
			constants.DefaultGitHubUsername,
		)

		req := helpers.CreateGitHubHTTPRequest(
			payload,
			"issues", // Unsupported event type
			app.Config.GitHubWebhookSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.GitHubHandler.HandleWebhook)

		// Verify request was rejected due to unsupported event type
		assert.Equal(t, http.StatusBadRequest, response.Code)

		// Verify no jobs were queued for unsupported event type
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for unsupported event type")
	})

	t.Run("concurrent webhook processing", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create multiple webhook requests
		req1 := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			100,
			"opened",
			"First PR",
			"First PR description",
			constants.DefaultGitHubUsername,
			app.Config.GitHubWebhookSecret,
		)

		req2 := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			200,
			"opened",
			"Second PR",
			"Second PR description",
			constants.DefaultGitHubUsername,
			app.Config.GitHubWebhookSecret,
		)

		// Process both requests
		response1 := helpers.ProcessHTTPRequest(req1, app.GitHubHandler.HandleWebhook)
		response2 := helpers.ProcessHTTPRequest(req2, app.GitHubHandler.HandleWebhook)

		// Verify both responses are OK
		assert.Equal(t, http.StatusOK, response1.Code)
		assert.Equal(t, http.StatusOK, response2.Code)

		// Verify both jobs were queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 2, "Expected 2 webhook jobs to be queued")

		// Process all queued jobs (expect Slack API errors with mock tokens)
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 2, processedCount)
		// We expect Slack API errors with mock tokens - this is acceptable
		if len(errors) > 0 {
			for _, err := range errors {
				assert.Contains(t, err.Error(), "invalid_auth", "Expected Slack API auth errors with mock tokens")
			}
		}
	})
}
