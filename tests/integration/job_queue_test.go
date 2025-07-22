package integration

import (
	"encoding/json"
	"fmt"
	"testing"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/tests/integration/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobQueueManagement tests job queue inspection, error handling, and edge cases.
func TestJobQueueManagement(t *testing.T) {
	app, ctx, cleanup := testutil.SetupTestApp(t)
	defer cleanup()

	constants := testutil.NewTestConstants()
	constantsPtr := &constants

	t.Run("job queue inspection and management", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Initially, queue should be empty
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "Queue should be empty initially")

		// Add multiple jobs to the queue
		job1 := &models.Job{
			ID:      "job-1",
			Type:    models.JobTypeManualPRLink,
			TraceID: "trace-1",
			Payload: []byte(`{"id":"job-1","pr_number":100}`),
		}

		job2 := &models.Job{
			ID:      "job-2",
			Type:    models.JobTypeGitHubWebhook,
			TraceID: "trace-2",
			Payload: []byte(`{"id":"job-2","event_type":"pull_request"}`),
		}

		job3 := &models.Job{
			ID:      "job-3",
			Type:    models.JobTypeManualPRLink,
			TraceID: "trace-3",
			Payload: []byte(`{"id":"job-3","pr_number":200}`),
		}

		// Enqueue jobs
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, job1))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, job2))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, job3))

		// Verify all jobs are in queue
		queuedJobs = app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 3, "Expected 3 jobs in queue")

		// Verify job details
		jobTypes := make(map[string]int)
		for _, job := range queuedJobs {
			jobTypes[job.Type]++
		}
		assert.Equal(t, 2, jobTypes[models.JobTypeManualPRLink], "Expected 2 manual PR link jobs")
		assert.Equal(t, 1, jobTypes[models.JobTypeGitHubWebhook], "Expected 1 GitHub webhook job")

		// Clear queue without processing
		app.CloudTasksService.ClearQueuedJobs()
		queuedJobs = app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "Queue should be empty after clearing")
	})

	t.Run("error handling during job processing", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test data for valid job
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create jobs with various error scenarios

		// 1. Valid job that should succeed
		validManualLinkJob := &models.ManualLinkJob{
			ID:             "valid-manual-job",
			PRNumber:       100,
			RepoFullName:   constants.DefaultRepoFullName,
			SlackChannel:   constants.DefaultSlackChannel,
			SlackMessageTS: "1234567890.123456",
			SlackTeamID:    constants.DefaultSlackTeamID,
			TraceID:        "trace-valid",
		}
		validPayload, _ := json.Marshal(validManualLinkJob)
		validJob := &models.Job{
			ID:      validManualLinkJob.ID,
			Type:    models.JobTypeManualPRLink,
			TraceID: validManualLinkJob.TraceID,
			Payload: validPayload,
		}

		// 2. Job with invalid JSON payload
		invalidJsonJob := &models.Job{
			ID:      "invalid-json-job",
			Type:    models.JobTypeManualPRLink,
			TraceID: "trace-invalid-json",
			Payload: []byte("invalid json data {{{"),
		}

		// 3. Job with valid JSON but invalid data structure
		// This will now fail validation due to missing required fields
		invalidStructJob := &models.Job{
			ID:      "invalid-struct-job",
			Type:    models.JobTypeManualPRLink,
			TraceID: "trace-invalid-struct",
			Payload: []byte(`{"wrong_field": "value", "missing_required": null}`),
		}

		// 4. Job with unsupported job type
		unsupportedJob := &models.Job{
			ID:      "unsupported-job",
			Type:    "unsupported_job_type",
			TraceID: "trace-unsupported",
			Payload: []byte(`{"id": "test"}`),
		}

		// Enqueue all jobs
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, validJob))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, invalidJsonJob))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, invalidStructJob))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, unsupportedJob))

		// Verify all jobs are queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 4, "Expected 4 jobs in queue")

		// Process all jobs
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 4, processedCount, "All jobs should be processed")
		assert.Len(t, errors, 3, "Expected 3 errors from invalid jobs (invalid JSON, invalid struct, and unsupported type)")

		// Verify queue is cleared after processing
		queuedJobs = app.CloudTasksService.GetQueuedJobs()
		assert.Empty(t, queuedJobs, "Queue should be empty after processing")

		// Check that the valid job succeeded (would have created tracked message)
		// Note: This assumes the valid job would create a TrackedMessage
		// The actual verification depends on the implementation of ProcessManualPRLinkJob
	})

	t.Run("concurrent job processing simulation", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup test data for successful processing
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Create multiple valid manual link jobs simulating concurrent Slack messages
		for i := 1; i <= 5; i++ {
			manualLinkJob := &models.ManualLinkJob{
				ID:             fmt.Sprintf("concurrent-job-%d", i),
				PRNumber:       100 + i,
				RepoFullName:   constants.DefaultRepoFullName,
				SlackChannel:   constants.DefaultSlackChannel,
				SlackMessageTS: fmt.Sprintf("123456789%d.123456", i),
				SlackTeamID:    constants.DefaultSlackTeamID,
				TraceID:        fmt.Sprintf("trace-concurrent-%d", i),
			}

			payload, err := json.Marshal(manualLinkJob)
			if err != nil {
				panic("failed to marshal test job: " + err.Error())
			}
			job := &models.Job{
				ID:      manualLinkJob.ID,
				Type:    models.JobTypeManualPRLink,
				TraceID: manualLinkJob.TraceID,
				Payload: payload,
			}

			require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, job))
		}

		// Verify all jobs are queued
		queuedJobs := app.CloudTasksService.GetQueuedJobs()
		require.Len(t, queuedJobs, 5, "Expected 5 concurrent jobs in queue")

		// Process all jobs at once
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 5, processedCount, "All 5 jobs should be processed")
		assert.Empty(t, errors, "No errors expected for valid jobs")

		// Verify all jobs created their respective tracked messages
		for i := 1; i <= 5; i++ {
			trackedMessages, err := app.FirestoreService.GetTrackedMessages(
				ctx,
				constants.DefaultRepoFullName,
				100+i,
				constants.DefaultSlackChannel,
				constants.DefaultSlackTeamID,
				"manual",
			)
			require.NoError(t, err)
			assert.Len(t, trackedMessages, 1, "Expected 1 tracked message for PR %d", 100+i)
		}
	})

	t.Run("job processing with partial failures", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, app.ClearData(ctx))

		// Setup minimal test data
		testutil.SetupTestUserAndRepo(t, app, ctx, constantsPtr)

		// Mix of valid and invalid jobs
		validJob := createValidManualLinkJob("valid-job", 300)
		invalidJob1 := &models.Job{
			ID:      "invalid-job-1",
			Type:    models.JobTypeManualPRLink,
			TraceID: "trace-invalid-1",
			Payload: []byte("corrupted payload"),
		}
		validJob2 := createValidManualLinkJob("valid-job-2", 400)
		invalidJob2 := &models.Job{
			ID:      "invalid-job-2",
			Type:    "unknown_type",
			TraceID: "trace-invalid-2",
			Payload: []byte(`{"valid": "json", "but": "wrong type"}`),
		}

		// Enqueue all jobs
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, validJob))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, invalidJob1))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, validJob2))
		require.NoError(t, app.CloudTasksService.EnqueueJob(ctx, invalidJob2))

		// Process all jobs
		processedCount, errors := app.ProcessQueuedJobs(ctx)
		assert.Equal(t, 4, processedCount, "All 4 jobs should be attempted")
		assert.Len(t, errors, 2, "Expected 2 errors from invalid jobs")

		// Verify the valid jobs succeeded
		trackedMessages300, err := app.FirestoreService.GetTrackedMessages(
			ctx, constants.DefaultRepoFullName, 300, constants.DefaultSlackChannel, constants.DefaultSlackTeamID, "manual",
		)
		require.NoError(t, err)
		assert.Len(t, trackedMessages300, 1, "Valid job 1 should have created tracked message")

		trackedMessages400, err := app.FirestoreService.GetTrackedMessages(
			ctx, constants.DefaultRepoFullName, 400, constants.DefaultSlackChannel, constants.DefaultSlackTeamID, "manual",
		)
		require.NoError(t, err)
		assert.Len(t, trackedMessages400, 1, "Valid job 2 should have created tracked message")
	})
}

// createValidManualLinkJob creates a valid ManualLinkJob wrapped in a Job.
func createValidManualLinkJob(jobID string, prNumber int) *models.Job {
	constants := testutil.NewTestConstants()

	manualLinkJob := &models.ManualLinkJob{
		ID:             jobID,
		PRNumber:       prNumber,
		RepoFullName:   constants.DefaultRepoFullName,
		SlackChannel:   constants.DefaultSlackChannel,
		SlackMessageTS: "1234567890.123456",
		SlackTeamID:    constants.DefaultSlackTeamID,
		TraceID:        "trace-" + jobID,
	}

	payload, err := json.Marshal(manualLinkJob)
	if err != nil {
		panic("failed to marshal test job: " + err.Error())
	}
	return &models.Job{
		ID:      jobID,
		Type:    models.JobTypeManualPRLink,
		TraceID: manualLinkJob.TraceID,
		Payload: payload,
	}
}
