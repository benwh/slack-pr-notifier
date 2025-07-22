package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/tests/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMixedWorkflow tests complex scenarios mixing Slack events and GitHub webhooks.
func TestMixedWorkflow(t *testing.T) {
	app, ctx, cleanup := testutil.SetupTestApp(t)
	defer cleanup()

	constants := testutil.NewTestConstants()
	constantsPtr := &constants
	helpers := testutil.HTTPTestHelpers{}

	t.Run("manual PR link followed by GitHub webhooks", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))
		app.MockSlackService.ClearCalls()

		// Setup test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		prNumber := 42
		slackMessageTS := "1234567890.123456"

		// Step 1: User posts PR link in Slack
		messageText := fmt.Sprintf("Please review: https://github.com/%s/pull/%d", constants.DefaultRepoFullName, prNumber)
		slackReq := helpers.CreateSlackMessageEventRequest(
			messageText,
			constants.DefaultSlackChannel,
			constants.DefaultSlackUserID,
			slackMessageTS,
			constants.DefaultSlackTeamID,
			app.Config.SlackSigningSecret,
		)

		// Process Slack event
		slackResponse := helpers.ProcessHTTPRequest(slackReq, app.SlackHandler.HandleEvent)
		assert.Equal(t, http.StatusOK, slackResponse.Code)

		// Verify Slack job was queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 Slack job to be queued")
		assert.Equal(t, models.JobTypeManualPRLink, queuedJobs[0].Type)

		// Process the Slack job
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		assert.Empty(t, errors)

		// Verify manual PR link was tracked
		trackedMessages, err := app.FirestoreService.GetTrackedMessages(
			ctx,
			constants.DefaultRepoFullName,
			prNumber,
			constants.DefaultSlackChannel,
			constants.DefaultSlackTeamID,
			"manual",
		)
		require.NoError(t, err)
		require.Len(t, trackedMessages, 1)
		assert.Equal(t, slackMessageTS, trackedMessages[0].SlackMessageTS)

		// Step 2: GitHub PR opened webhook arrives
		githubReq := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			prNumber,
			"opened",
			"Add new feature",
			"This PR adds an awesome new feature",
			constants.DefaultGitHubUsername,
			app.Config.GitHubWebhookSecret,
		)

		// Process GitHub webhook
		githubResponse := helpers.ProcessHTTPRequest(githubReq, app.GitHubHandler.HandleWebhook)
		assert.Equal(t, http.StatusOK, githubResponse.Code)

		// Verify GitHub job was queued
		queuedJobs = app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 GitHub job to be queued")
		assert.Equal(t, models.JobTypeGitHubWebhook, queuedJobs[0].Type)

		// Process the GitHub job (should succeed with mock)
		processedCount, errors = app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		assert.Empty(t, errors, "GitHub job processing should succeed with mock services")

		// Verify that Slack service was called to post PR message
		slackCalls := app.MockSlackService.GetCallsByMethod("PostPRMessage")
		assert.Len(t, slackCalls, 1, "Expected PostPRMessage to be called once")
		if len(slackCalls) > 0 {
			// For now, check that the mock was called with expected test data
			assert.Equal(t, "Test PR", slackCalls[0].Args["prTitle"])
			assert.Equal(t, "test-author", slackCalls[0].Args["prAuthor"])
		}

		// Step 3: PR review webhook arrives
		reviewReq := helpers.CreateGitHubReviewWebhookRequest(
			constants.DefaultRepoFullName,
			prNumber,
			"approved",
			"reviewer-alice",
			app.Config.GitHubWebhookSecret,
		)

		// Process GitHub review webhook
		reviewResponse := helpers.ProcessHTTPRequest(reviewReq, app.GitHubHandler.HandleWebhook)
		assert.Equal(t, http.StatusOK, reviewResponse.Code)

		// Verify review job was queued
		queuedJobs = app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 1, "Expected 1 GitHub review job to be queued")
		assert.Equal(t, models.JobTypeGitHubWebhook, queuedJobs[0].Type)

		// Process the review job (should succeed with mock)
		processedCount, errors = app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 1, processedCount)
		assert.Empty(t, errors, "Review job processing should succeed with mock services")

		// Verify that Slack service was called to add reaction
		reactionCalls := app.MockSlackService.GetCallsByMethod("AddReaction")
		assert.NotEmpty(t, reactionCalls, "Expected AddReaction to be called for approved review")
		if len(reactionCalls) > 0 {
			assert.Equal(t, "white_check_mark", reactionCalls[0].Emoji, "Expected approved reaction emoji")
		}

		// Verify final state: Manual PR link still tracked
		finalTrackedMessages, err := app.FirestoreService.GetTrackedMessages(
			ctx,
			constants.DefaultRepoFullName,
			prNumber,
			constants.DefaultSlackChannel,
			constants.DefaultSlackTeamID,
			"manual",
		)
		require.NoError(t, err)
		assert.Len(t, finalTrackedMessages, 1, "Manual PR link should still be tracked")
	})

	t.Run("multiple jobs processing with errors", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))
		app.MockSlackService.ClearCalls()

		// Setup test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create multiple jobs: some valid, some invalid

		// Valid Slack message with PR link
		validSlackReq := helpers.CreateSlackMessageEventRequest(
			fmt.Sprintf("Review: https://github.com/%s/pull/100", constants.DefaultRepoFullName),
			constants.DefaultSlackChannel,
			constants.DefaultSlackUserID,
			"1234567890.100",
			constants.DefaultSlackTeamID,
			app.Config.SlackSigningSecret,
		)

		// Valid GitHub webhook
		validGitHubReq := helpers.CreateGitHubPRWebhookRequest(
			constants.DefaultRepoFullName,
			200,
			"opened",
			"Valid PR",
			"This is a valid PR",
			constants.DefaultGitHubUsername,
			app.Config.GitHubWebhookSecret,
		)

		// Invalid GitHub webhook (malformed JSON will be caught at HTTP level)
		// Instead, we'll create a job with invalid payload manually

		// Process valid requests
		slackResponse := helpers.ProcessHTTPRequest(validSlackReq, app.SlackHandler.HandleEvent)
		assert.Equal(t, http.StatusOK, slackResponse.Code)

		githubResponse := helpers.ProcessHTTPRequest(validGitHubReq, app.GitHubHandler.HandleWebhook)
		assert.Equal(t, http.StatusOK, githubResponse.Code)

		// Manually add an invalid job to test error handling
		invalidJob := &models.Job{
			ID:      "invalid-job",
			Type:    models.JobTypeGitHubWebhook,
			TraceID: "trace-invalid",
			Payload: []byte("invalid json payload"),
		}
		if err := app.CloudTasksService.EnqueueJob(ctx, invalidJob); err != nil {
			t.Errorf("failed to enqueue invalid job: %v", err)
		}

		// Verify all jobs are queued (2 valid + 1 invalid)
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 3, "Expected 3 jobs to be queued")

		// Process all queued jobs
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 3, processedCount, "All jobs should be processed")
		assert.Len(t, errors, 1, "Expected 1 error from invalid job")

		// Verify the valid jobs succeeded by checking database state
		trackedMessages, err := app.FirestoreService.GetTrackedMessages(
			ctx,
			constants.DefaultRepoFullName,
			100, // First PR from Slack message
			constants.DefaultSlackChannel,
			constants.DefaultSlackTeamID,
			"manual",
		)
		require.NoError(t, err)
		assert.Len(t, trackedMessages, 1, "Valid Slack job should have created tracked message")
	})

	t.Run("multi-tenancy with concurrent processing", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))
		app.MockSlackService.ClearCalls()

		// Setup data for two different Slack workspaces
		team1ID := "T111111111"
		team2ID := "T222222222"
		channel1ID := "C111111111"
		channel2ID := "C222222222"
		user1ID := "U111111111"
		user2ID := "U222222222"

		// Create users and repos for both workspaces
		setupUserAndRepo(t, app, ctx, user1ID, team1ID, channel1ID,
			constants.DefaultRepoFullName, constants.DefaultGitHubUsername, constants.DefaultGitHubUserID)
		setupUserAndRepo(t, app, ctx, user2ID, team2ID, channel2ID, constants.DefaultRepoFullName, "different-github-user", 987654321)

		prNumber := 123

		// Create Slack messages from both workspaces for the same PR
		messageText := fmt.Sprintf("Check this PR: https://github.com/%s/pull/%d", constants.DefaultRepoFullName, prNumber)

		req1 := helpers.CreateSlackMessageEventRequest(
			messageText,
			channel1ID,
			user1ID,
			"1111111111.111111",
			team1ID,
			app.Config.SlackSigningSecret,
		)

		req2 := helpers.CreateSlackMessageEventRequest(
			messageText,
			channel2ID,
			user2ID,
			"2222222222.222222",
			team2ID,
			app.Config.SlackSigningSecret,
		)

		// Process both Slack events
		response1 := helpers.ProcessHTTPRequest(req1, app.SlackHandler.HandleEvent)
		response2 := helpers.ProcessHTTPRequest(req2, app.SlackHandler.HandleEvent)

		assert.Equal(t, http.StatusOK, response1.Code)
		assert.Equal(t, http.StatusOK, response2.Code)

		// Verify both jobs were queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 2, "Expected 2 Slack jobs to be queued")

		// Process all jobs
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 2, processedCount)
		assert.Empty(t, errors)

		// Verify workspace isolation: each workspace has its own tracked message
		team1Messages, err := app.FirestoreService.GetTrackedMessages(
			ctx, constants.DefaultRepoFullName, prNumber, channel1ID, team1ID, "manual",
		)
		require.NoError(t, err)
		assert.Len(t, team1Messages, 1, "Team 1 should have 1 tracked message")

		team2Messages, err := app.FirestoreService.GetTrackedMessages(
			ctx, constants.DefaultRepoFullName, prNumber, channel2ID, team2ID, "manual",
		)
		require.NoError(t, err)
		assert.Len(t, team2Messages, 1, "Team 2 should have 1 tracked message")

		// Verify messages are isolated by workspace
		assert.Equal(t, team1ID, team1Messages[0].SlackTeamID)
		assert.Equal(t, team2ID, team2Messages[0].SlackTeamID)
		assert.Equal(t, channel1ID, team1Messages[0].SlackChannel)
		assert.Equal(t, channel2ID, team2Messages[0].SlackChannel)
	})
}

// setupUserAndRepo creates a user and repository for testing multi-tenancy.
func setupUserAndRepo(
	t *testing.T, app *testutil.TestApp, ctx context.Context,
	userID, teamID, channelID, repoFullName, githubUsername string, githubUserID int64,
) {
	t.Helper()
	// Create user
	user := &models.User{
		ID:             userID,
		SlackUserID:    userID,
		SlackTeamID:    teamID,
		GitHubUsername: githubUsername,
		GitHubUserID:   githubUserID,
		DefaultChannel: channelID,
		Verified:       true,
	}
	require.NoError(t, app.FirestoreService.SaveUser(ctx, user))

	// Create repository
	repo := &models.Repo{
		ID:             repoFullName,
		SlackTeamID:    teamID,
		DefaultChannel: channelID,
	}
	require.NoError(t, app.FirestoreService.CreateRepo(ctx, repo))
}
