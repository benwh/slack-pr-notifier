package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/tests/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSlackEventProcessing tests complete HTTP → job queue → processing pipeline.
func TestSlackEventProcessing(t *testing.T) {
	app, ctx, cleanup := testutil.SetupTestApp(t)
	defer cleanup()

	constants := testutil.NewTestConstants()
	constantsPtr := &constants
	helpers := testutil.HTTPTestHelpers{}

	t.Run("manual PR link detection via HTTP pipeline", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test user and repo
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create HTTP request with PR link in message
		messageText := fmt.Sprintf("Hey team, please review this PR: https://github.com/%s/pull/%d",
			constants.DefaultRepoFullName, constants.DefaultPRNumber)
		req := helpers.CreateSlackMessageEventRequest(
			messageText,
			constants.DefaultSlackChannel,
			constants.DefaultSlackUserID,
			"1234567890.123456",
			constants.DefaultSlackTeamID,
			app.Config.SlackSigningSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.SlackHandler.HandleEvent)

		// Verify HTTP response
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify job was queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 job to be queued")

		job := queuedJobs[0]
		assert.Equal(t, models.JobTypeManualPRLink, job.Type)

		// Process the queued job
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		assert.Empty(t, errors)

		// Verify database state - tracked message should exist
		trackedMessages, err := app.FirestoreService.GetTrackedMessages(
			ctx,
			constants.DefaultRepoFullName,
			constants.DefaultPRNumber,
			constants.DefaultSlackChannel,
			constants.DefaultSlackTeamID,
			"manual",
		)
		require.NoError(t, err)
		assert.Len(t, trackedMessages, 1)

		trackedMsg := trackedMessages[0]
		assert.Equal(t, constants.DefaultRepoFullName, trackedMsg.RepoFullName)
		assert.Equal(t, constants.DefaultPRNumber, trackedMsg.PRNumber)
		assert.Equal(t, constants.DefaultSlackChannel, trackedMsg.SlackChannel)
		assert.Equal(t, "manual", trackedMsg.MessageSource)
	})

	t.Run("invalid Slack signature rejection", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create message with valid PR link
		messageText := "Check this PR: https://github.com/" + constants.DefaultRepoFullName + "/pull/123"
		payload := helpers.CreateSlackEventAPIPayload(
			"event_callback",
			map[string]interface{}{
				"type":     "message",
				"channel":  constants.DefaultSlackChannel,
				"user":     constants.DefaultSlackUserID,
				"text":     messageText,
				"ts":       "1234567890.123456",
				"event_ts": "1234567890.123456",
			},
			constants.DefaultSlackTeamID,
		)

		// Create request with INVALID signature
		req := helpers.CreateInvalidSlackSignatureRequest(payload)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.SlackHandler.HandleEvent)

		// Verify request was rejected due to invalid signature
		assert.Equal(t, http.StatusUnauthorized, response.Code)

		// Verify no jobs were queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for invalid signature")
	})

	t.Run("message without PR links ignored", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create request with message that has no PR links
		messageText := "Just a regular message about work stuff"
		req := helpers.CreateSlackMessageEventRequest(
			messageText,
			constants.DefaultSlackChannel,
			constants.DefaultSlackUserID,
			"1234567890.123456",
			constants.DefaultSlackTeamID,
			app.Config.SlackSigningSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.SlackHandler.HandleEvent)

		// Verify HTTP response is OK
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify no jobs were queued (message had no PR links)
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for messages without PR links")
	})

	t.Run("bot messages are ignored", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create message event from a bot (has bot_id field)
		messageText := "Bot message with PR: https://github.com/" + constants.DefaultRepoFullName + "/pull/456"
		payload := helpers.CreateSlackEventAPIPayload(
			"event_callback",
			map[string]interface{}{
				"type":     "message",
				"channel":  constants.DefaultSlackChannel,
				"user":     constants.DefaultSlackUserID,
				"bot_id":   "B123456789", // This marks it as a bot message
				"text":     messageText,
				"ts":       "1234567890.123456",
				"event_ts": "1234567890.123456",
			},
			constants.DefaultSlackTeamID,
		)

		req := helpers.CreateSlackHTTPRequest(payload, app.Config.SlackSigningSecret)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.SlackHandler.HandleEvent)

		// Verify HTTP response is OK
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify no jobs were queued (bot messages are ignored)
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for bot messages")
	})

	t.Run("multiple PR links in single message ignored", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Create message with multiple PR links (should be ignored per implementation)
		messageText := "Review these PRs: " +
			"https://github.com/" + constants.DefaultRepoFullName + "/pull/111 " +
			"and https://github.com/" + constants.DefaultRepoFullName + "/pull/222"

		req := helpers.CreateSlackMessageEventRequest(
			messageText,
			constants.DefaultSlackChannel,
			constants.DefaultSlackUserID,
			"1234567890.123456",
			constants.DefaultSlackTeamID,
			app.Config.SlackSigningSecret,
		)

		// Process request through HTTP handler
		response := helpers.ProcessHTTPRequest(req, app.SlackHandler.HandleEvent)

		// Verify HTTP response is OK
		assert.Equal(t, http.StatusOK, response.Code)

		// Verify no jobs were queued (multiple PR links are ignored)
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "No jobs should be queued for messages with multiple PR links")
	})
}
