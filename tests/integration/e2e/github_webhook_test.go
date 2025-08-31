package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github-slack-notifier/internal/models"
	firestoreTesting "github-slack-notifier/internal/testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: Mock setup is now per-test-harness to ensure proper isolation.

// TestMain manages the global emulator lifecycle for all e2e tests.
func TestMain(m *testing.M) {
	// Start global emulator
	_, err := firestoreTesting.GetGlobalEmulator()
	if err != nil {
		log.Fatalf("Failed to start global emulator: %v", err)
	}

	// Run all tests
	code := m.Run()

	// Cleanup global emulator
	if err := firestoreTesting.StopGlobalEmulator(); err != nil {
		log.Printf("Error stopping emulator: %v", err)
	}

	os.Exit(code)
}

func TestGitHubWebhookIntegration(t *testing.T) {
	// Setup test harness - this starts the real application
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Setup mock responses for external APIs
	harness.SetupMockResponses()

	// Context for database operations
	ctx := context.Background()

	// Helper function to test PR webhook flow
	testPRWebhookFlow := func(
		t *testing.T,
		userID, userName, channel, repoName string,
		prNumber int,
		title string,
		buildPayload func(string, int, string, string) []byte,
	) {
		t.Helper()
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, userID)

		// Setup test data in Firestore
		setupTestUser(t, harness, userName, userID, channel)
		setupTestRepo(t, harness, channel)
		setupGitHubInstallation(t, harness)

		// Create GitHub webhook payload
		payload := buildPayload(repoName, prNumber, title, userName)

		// Send webhook to application
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were queued and executed (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// First job should be the original GitHub webhook job
		githubWebhookJob := jobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, githubWebhookJob.Type)

		// Second job should be the workspace PR job
		workspacePRJob := jobs[1]
		assert.Equal(t, models.JobTypeWorkspacePR, workspacePRJob.Type)

		// Verify the webhook job payload
		var webhookJob models.WebhookJob
		require.NoError(t, json.Unmarshal(githubWebhookJob.Payload, &webhookJob))
		assert.Equal(t, "pull_request", webhookJob.EventType)

		// Verify Slack API was called to post the notification
		info := httpmock.GetCallCountInfo()
		slackCalls := info["POST https://slack.com/api/chat.postMessage"]
		assert.Positive(t, slackCalls, "Expected Slack postMessage to be called")

		// Verify we sent exactly one Slack message
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)

		// The PR has 50 additions + 30 deletions = 80 lines, which should be a cat emoji
		message := slackRequests[0]
		// Channel names are now resolved to IDs
		expectedChannelID := "C987654321" // test-channel -> C987654321
		if channel == "pr-channel" {
			expectedChannelID = "C111111111"
		}
		assert.Equal(t, expectedChannelID, message.Channel)
		assert.Contains(t, message.Text, "🐱") // cat emoji for 80 lines
		assert.Contains(t, message.Text, fmt.Sprintf("https://github.com/%s/pull/%d", repoName, prNumber))
		assert.Contains(t, message.Text, title)

		// Check author formatting
		if userID != "" {
			assert.Contains(t, message.Text, fmt.Sprintf("<@%s>", userID))
		} else {
			assert.Contains(t, message.Text, userName)
		}
	}

	t.Run("PR opened webhook full flow", func(t *testing.T) {
		testPRWebhookFlow(t, "U123456789", "test-user", "test-channel",
			"testorg/testrepo", 123, "Add new feature", buildPROpenedPayload)
	})

	t.Run("Draft PR promoted to ready for review", func(t *testing.T) {
		testPRWebhookFlow(t, "U123456789", "draft-user", "pr-channel",
			"testorg/draftrepo", 555, "Draft PR now ready", buildPRReadyForReviewPayload)
	})

	t.Run("Draft PR opened should not post to Slack", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data in Firestore
		setupTestUser(t, harness, "draft-author", "U123456789", "draft-channel")
		setupTestRepo(t, harness, "draft-channel")
		setupGitHubInstallation(t, harness)

		// Create GitHub webhook payload for draft PR
		payload := buildDraftPROpenedPayload("testorg/draftrepo", 999, "WIP: Draft PR", "draft-author")

		// Send webhook to application
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was queued and executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		job := jobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, job.Type)

		// Verify NO Slack API call was made
		info := httpmock.GetCallCountInfo()
		slackCalls := info["POST https://slack.com/api/chat.postMessage"]
		assert.Equal(t, 0, slackCalls, "Expected no Slack postMessage calls for draft PR")

		// Also verify using request capture
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, slackRequests)
	})

	t.Run("PR review webhook with reaction sync", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U987654321")

		// Setup test data
		setupTestUser(t, harness, "reviewer", "U987654321", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Create a tracked message (simulating a previous PR notification)
		setupTrackedMessage(t, harness, "testorg/testrepo", 456, "test-channel", "T123456789", "1234567890.123456")

		// Wait a moment to ensure the data is persisted
		time.Sleep(10 * time.Millisecond)

		// Create review webhook payload
		payload := buildReviewSubmittedPayload("testorg/testrepo", 456, "reviewer", "approved")

		// Send webhook to application
		resp := sendGitHubWebhook(t, harness, "pull_request_review", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed (github_webhook + reaction_sync)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Verify reaction was added for approved review
		reactionRequests := harness.SlackRequestCapture().GetReactionRequests()
		if len(reactionRequests) > 0 {
			// If reactions were added, verify the correct emoji
			assert.Equal(t, "test-channel", reactionRequests[0].Channel)
			assert.Equal(t, "1234567890.123456", reactionRequests[0].Timestamp)
			assert.Equal(t, "white_check_mark", reactionRequests[0].Name)
		}
	})

	t.Run("Invalid webhook signature rejection", func(t *testing.T) {
		// Clear jobs
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Create payload with invalid signature
		payload := buildPROpenedPayload("testorg/testrepo", 789, "Test PR", "test-user")

		// Send with wrong signature
		req := buildWebhookRequest(t, harness.BaseURL()+"/webhooks/github", "pull_request", payload, "wrong-signature")
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Should be rejected
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		// No jobs should be executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Empty(t, jobs)
	})

	t.Run("Concurrent webhook processing", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Configure fake Cloud Tasks to execute asynchronously
		harness.FakeCloudTasks().SetAsync(true, 10*time.Millisecond)

		// Send multiple webhooks concurrently
		numWebhooks := 5
		done := make(chan bool, numWebhooks)

		for i := range numWebhooks {
			go func(prNum int) {
				payload := buildPROpenedPayload("testorg/testrepo", prNum, fmt.Sprintf("PR %d", prNum), "test-user")
				resp := sendGitHubWebhook(t, harness, "pull_request", payload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				done <- true
			}(1000 + i) // Use 1000+ to avoid conflicts with other tests
		}

		// Wait for all webhooks to be sent
		for range numWebhooks {
			<-done
		}

		// Wait for all jobs to be executed
		err := harness.FakeCloudTasks().WaitForJobs(numWebhooks, 5*time.Second)
		require.NoError(t, err)

		// CRITICAL: Switch back to synchronous execution to ensure all HTTP requests complete
		// before this test finishes (prevents pollution of subsequent tests)
		harness.FakeCloudTasks().SetAsync(false, 0)

		// Additional safety: small delay to ensure any in-flight HTTP requests complete
		time.Sleep(50 * time.Millisecond)

		// Verify all jobs were executed (each webhook produces 2 jobs: github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Len(t, jobs, numWebhooks*2)
	})

	t.Run("PR size emoji is included", func(t *testing.T) {
		// Test a few key thresholds to ensure emojis are working
		testCases := []struct {
			name      string
			additions int
			deletions int
			wantEmoji string
		}{
			{"tiny PR", 2, 1, "🐜"},        // ant for very small
			{"medium PR", 30, 15, "🐰"},    // rabbit for medium
			{"large PR", 500, 200, "🐻"},   // bear for large
			{"whale PR", 1500, 1000, "🐋"}, // whale for huge
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Reset all test state for proper isolation
				require.NoError(t, harness.ResetForTest(ctx))

				// Setup OAuth workspace
				setupTestWorkspace(t, harness, "U123456789")

				// Setup test data
				setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
				setupTestRepo(t, harness, "test-channel")
				setupGitHubInstallation(t, harness)

				// Create PR webhook payload with specific line counts
				payload := buildPRPayloadWithSize("testorg/testrepo", 2000, tc.name, "test-user",
					"opened", false, tc.additions, tc.deletions)

				// Send webhook
				resp := sendGitHubWebhook(t, harness, "pull_request", payload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Wait for job execution (github_webhook + workspace_pr)
				jobs := harness.FakeCloudTasks().GetExecutedJobs()
				require.Len(t, jobs, 2)

				// Verify the message contains the expected emoji
				slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
				require.Len(t, slackRequests, 1)
				assert.Contains(t, slackRequests[0].Text, tc.wantEmoji)
			})
		}
	})

	t.Run("Review state emoji mappings", func(t *testing.T) {
		testCases := []struct {
			name        string
			reviewState string
			wantEmoji   string
			emojiName   string
		}{
			{"approved review", "approved", "✅", "white_check_mark"},
			{"changes requested", "changes_requested", "🔄", "arrows_counterclockwise"},
			{"commented", "commented", "💬", "speech_balloon"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Reset all test state for proper isolation
				require.NoError(t, harness.ResetForTest(ctx))

				// Setup OAuth workspace
				setupTestWorkspace(t, harness, "U987654321")

				// Setup test data
				setupTestUser(t, harness, "reviewer", "U987654321", "test-channel")
				setupTestRepo(t, harness, "test-channel")
				setupGitHubInstallation(t, harness)

				// Create a tracked message
				const messageTS = "1234567890.123456"
				setupTrackedMessage(t, harness, "testorg/testrepo", 3000, "test-channel", "T123456789", messageTS)

				// Wait for data persistence
				time.Sleep(10 * time.Millisecond)

				// Create review webhook payload
				payload := buildReviewSubmittedPayload("testorg/testrepo", 3000, "reviewer", tc.reviewState)

				// Send webhook
				resp := sendGitHubWebhook(t, harness, "pull_request_review", payload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Wait for job execution (github_webhook + reaction_sync)
				jobs := harness.FakeCloudTasks().GetExecutedJobs()
				require.Len(t, jobs, 2)

				// Verify the correct reaction was added
				reactionRequests := harness.SlackRequestCapture().GetReactionRequests()
				if len(reactionRequests) > 0 {
					require.Len(t, reactionRequests, 1)
					assert.Equal(t, "test-channel", reactionRequests[0].Channel)
					assert.Equal(t, messageTS, reactionRequests[0].Timestamp)
					assert.Equal(t, tc.emojiName, reactionRequests[0].Name)
				}
			})
		}
	})

	// PR Directive Tests
	t.Run("PR directives - skip notification", func(t *testing.T) {
		// Use comprehensive reset for better test isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Create payload with skip directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 500, "PR with skip", "test-user", "!review: skip")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify NO Slack message was sent
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, slackRequests, "Expected no Slack message due to skip directive")

		// Also verify using httpmock
		info := httpmock.GetCallCountInfo()
		slackCalls := info["POST https://slack.com/api/chat.postMessage"]
		assert.Equal(t, 0, slackCalls, "Expected no Slack postMessage calls due to skip directive")
	})

	t.Run("PR directives - channel override", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")
		setupGitHubInstallation(t, harness)

		// Create payload with channel override directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 501, "PR with channel override", "test-user", "!review: #override-channel")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Verify message was sent to override channel
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "C222222222", slackRequests[0].Channel) // override-channel -> C222222222
		assert.Contains(t, slackRequests[0].Text, "PR with channel override")
	})

	t.Run("PR directives - user CC", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Create payload with user CC directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 502, "PR with user CC", "test-user", "!review: @jane.smith")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Verify message was sent with CC
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Contains(t, slackRequests[0].Text, "(cc: @jane.smith)")
		assert.Contains(t, slackRequests[0].Text, "PR with user CC")
	})

	t.Run("PR directives - combined channel and user CC", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")
		setupGitHubInstallation(t, harness)

		// Create payload with combined directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 503, "PR with combined directives", "test-user",
			"!review: #combined-channel @tech.lead")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Verify message was sent to override channel with CC
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "C333333333", slackRequests[0].Channel) // combined-channel -> C333333333
		assert.Contains(t, slackRequests[0].Text, "(cc: @tech.lead)")
		assert.Contains(t, slackRequests[0].Text, "PR with combined directives")
	})

	t.Run("PR directives - last directive wins", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")
		setupGitHubInstallation(t, harness)

		// Create payload with multiple directives - last one should win
		body := "!review: #first-channel @first.user\n!review: #final-channel @final.user"
		payload := buildPRPayloadWithDirective("testorg/testrepo", 504, "PR with multiple directives", "test-user", body)

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Verify message was sent to final channel with final user (last directive wins)
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "C444444444", slackRequests[0].Channel) // final-channel -> C444444444
		assert.Contains(t, slackRequests[0].Text, "(cc: @final.user)")
		assert.Contains(t, slackRequests[0].Text, "PR with multiple directives")
	})

	t.Run("PR directives - retroactive message deletion with !review-skip", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// First, create a PR (which will post a message)
		payload := buildPRPayloadWithDirective("testorg/testrepo", 505, "PR to be deleted", "test-user", "Initial PR description")

		// Send webhook for PR opened
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and message was posted (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be posted initially")

		// Clear for the edit event
		harness.ResetForNextStep()

		// Now edit the PR with !review-skip directive
		editedPayload := buildPREditedPayloadWithDirective("testorg/testrepo", 505, "PR to be deleted", "test-user", "!review-skip")

		// Send webhook for PR edited
		resp = sendGitHubWebhook(t, harness, "pull_request", editedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed for the edit
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify NO new Slack messages were posted (we're only deleting)
		slackRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, slackRequests, "Expected no new messages to be posted during edit")
	})

	t.Run("PR skip/unskip sequence - removing skip directive re-posts PR", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Step 1: Create a PR (which will post a message)
		payload := buildPRPayloadWithDirective("testorg/testrepo", 506, "Test skip/unskip sequence", "test-user", "Initial PR description")

		// Send webhook for PR opened
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and message was posted initially (github_webhook + workspace_pr)
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be posted initially")
		initialMessage := slackRequests[0]
		assert.Equal(t, "C987654321", initialMessage.Channel) // test-channel -> C987654321
		assert.Contains(t, initialMessage.Text, "Test skip/unskip sequence")

		// Step 2: Edit the PR to add !review-skip directive (should delete message)
		harness.ResetForNextStep()

		editedPayload := buildPREditedPayloadWithDirective("testorg/testrepo", 506, "Test skip/unskip sequence", "test-user", "!review-skip")

		// Send webhook for PR edited with skip
		resp = sendGitHubWebhook(t, harness, "pull_request", editedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed for the edit
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify NO new Slack messages were posted (only deleting)
		slackRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, slackRequests, "Expected no new messages to be posted during skip")

		// Step 3: Edit the PR to remove !review-skip directive (should re-post message)
		harness.ResetForNextStep()

		unskipPayload := buildPREditedPayloadWithDirective("testorg/testrepo", 506, "Test skip/unskip sequence", "test-user",
			"Regular PR description without skip")

		// Send webhook for PR edited without skip
		resp = sendGitHubWebhook(t, harness, "pull_request", unskipPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed for the unskip edit (github_webhook + workspace_pr)
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// THIS IS THE KEY ASSERTION: Verify the PR message was re-posted to Slack
		slackRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be re-posted after removing skip directive")

		repostedMessage := slackRequests[0]
		assert.Equal(t, "C987654321", repostedMessage.Channel) // test-channel -> C987654321
		assert.Contains(t, repostedMessage.Text, "Test skip/unskip sequence")
		assert.Contains(t, repostedMessage.Text, "https://github.com/testorg/testrepo/pull/506")
	})

	t.Run("Fan-out architecture error handling", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Create GitHub webhook payload
		payload := buildPROpenedPayload("testorg/testrepo", 123, "Test fan-out error handling", "test-user")

		// Send webhook to application
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify the fan-out architecture creates exactly 2 jobs
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2, "Fan-out should create github_webhook + workspace_pr jobs")

		// Verify job types are correct
		githubWebhookJob := jobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, githubWebhookJob.Type)

		workspacePRJob := jobs[1]
		assert.Equal(t, models.JobTypeWorkspacePR, workspacePRJob.Type)

		// Verify the WorkspacePR job payload contains expected fields
		var workspacePRJobData models.WorkspacePRJob
		require.NoError(t, json.Unmarshal(workspacePRJob.Payload, &workspacePRJobData))
		assert.Equal(t, 123, workspacePRJobData.PRNumber)
		assert.Equal(t, "testorg/testrepo", workspacePRJobData.RepoFullName)
		assert.Equal(t, "T123456789", workspacePRJobData.WorkspaceID)
		assert.Equal(t, "opened", workspacePRJobData.PRAction)
		assert.Equal(t, int64(100001), workspacePRJobData.GitHubUserID)
		assert.Equal(t, "test-user", workspacePRJobData.GitHubUsername)
		assert.NotEmpty(t, workspacePRJobData.TraceID)
		assert.NotEmpty(t, workspacePRJobData.PRPayload, "PR payload should be embedded in workspace job")

		// Verify Slack message was posted successfully
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Exactly one Slack message should be posted")

		message := slackRequests[0]
		assert.Equal(t, "C987654321", message.Channel) // test-channel -> C987654321
		assert.Contains(t, message.Text, "Test fan-out error handling")
		assert.Contains(t, message.Text, "testorg/testrepo")

		t.Logf("✅ Fan-out architecture test passed: GitHub webhook successfully fanned out to workspace-specific jobs")
	})

	t.Run("PR_edited_with_channel_change_migrates_messages", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "channel-test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Step 1: Create initial PR with !review directive pointing to #test-channel
		initialPayload := buildPRPayloadWithDirective(
			"testorg/testrepo", 789, "PR with channel change", "channel-test-user",
			"Initial PR description\n\n!review: #test-channel",
		)
		resp := sendGitHubWebhook(t, harness, "pull_request", initialPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and initial message was posted
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2) // github_webhook + workspace_pr

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected initial PR message to be posted")
		initialMessage := slackRequests[0]
		// dev-team should resolve to a channel ID - let's check it was posted
		assert.Contains(t, initialMessage.Text, "PR with channel change")

		// Wait for tracked message to be persisted to the database
		waitForTrackedMessage(t, harness, "testorg/testrepo", 789)

		// Step 2: Reset for the edit event
		harness.ResetForNextStep()

		// Edit PR to change channel to #pr-channel
		editedPayload := buildPREditedPayloadWithDirective(
			"testorg/testrepo", 789, "PR with channel change", "channel-test-user",
			"Updated PR description\n\n!review: #pr-channel",
		)
		resp = sendGitHubWebhook(t, harness, "pull_request", editedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify edit job was executed and channel change created additional workspace job
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2, "Expected edit webhook job + workspace PR job for new channel")

		// Verify new message was posted to pr-channel
		slackRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected new PR message to be posted to new channel")
		newMessage := slackRequests[0]
		assert.Contains(t, newMessage.Text, "PR with channel change")

		t.Logf("✅ Channel change test passed: PR notification migrated from #dev-team to #qa-team")
	})
}

// waitForTrackedMessage polls the database until a tracked message appears for the given PR.
// We need to do this because we fan-out, and therefore return a success to the client before the inner
// job is actually completed.
func waitForTrackedMessage(t *testing.T, harness *TestHarness, repoFullName string, prNumber int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("Timeout waiting for tracked message to appear in database")
		case <-ticker.C:
			iter := harness.FirestoreClient().Collection("trackedmessages").
				Where("repo_full_name", "==", repoFullName).
				Where("pr_number", "==", prNumber).
				Documents(ctx)

			docs, err := iter.GetAll()
			if err != nil {
				continue
			}

			if len(docs) > 0 {
				// Found tracked message(s)
				return
			}
		}
	}
}

// Helper functions

func buildPROpenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "opened", false)
}

func buildDraftPROpenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "opened", true)
}

func buildPRPayload(repoFullName string, prNumber int, title, author, action string, draft bool) []byte {
	// Map GitHub usernames to consistent numeric IDs for testing (same as harness.go)
	githubUserIDMap := map[string]int64{
		"test-user":         100001,
		"draft-user":        100002,
		"draft-author":      100003,
		"channel-test-user": 100004,
	}

	githubUserID, exists := githubUserIDMap[author]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	payload := map[string]interface{}{
		"action": action,
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      "Test PR description",
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     "open",
			"draft":     draft,
			"additions": 50,
			"deletions": 30,
			"user": map[string]interface{}{
				"id":    githubUserID, // Add numeric GitHub user ID
				"login": author,
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func buildReviewSubmittedPayload(repoFullName string, prNumber int, reviewer, state string) []byte {
	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": state,
			"user": map[string]interface{}{
				"login": reviewer,
			},
		},
		"pull_request": map[string]interface{}{
			"number":   prNumber,
			"title":    "Test PR",
			"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func buildPRReadyForReviewPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "ready_for_review", false)
}

func buildPRPayloadWithSize(repoFullName string, prNumber int, title, author, action string, draft bool, additions, deletions int) []byte {
	// Map GitHub usernames to consistent numeric IDs for testing (same as harness.go)
	githubUserIDMap := map[string]int64{
		"test-user":         100001,
		"draft-user":        100002,
		"draft-author":      100003,
		"channel-test-user": 100004,
	}

	githubUserID, exists := githubUserIDMap[author]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	payload := map[string]interface{}{
		"action": action,
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      "Test PR description",
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     "open",
			"draft":     draft,
			"additions": additions,
			"deletions": deletions,
			"user": map[string]interface{}{
				"id":    githubUserID, // Add numeric GitHub user ID
				"login": author,
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

// buildPRPayloadWithDirective creates a PR payload with a specific directive in the body.
func buildPRPayloadWithDirective(repoFullName string, prNumber int, title, author, body string) []byte {
	// Map GitHub usernames to consistent numeric IDs for testing (same as harness.go)
	githubUserIDMap := map[string]int64{
		"test-user":         100001,
		"draft-user":        100002,
		"draft-author":      100003,
		"channel-test-user": 100004,
	}

	githubUserID, exists := githubUserIDMap[author]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	payload := map[string]interface{}{
		"action": "opened",
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      body,
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     "open",
			"draft":     false,
			"additions": 50,
			"deletions": 30,
			"user": map[string]interface{}{
				"id":    githubUserID, // Add numeric GitHub user ID
				"login": author,
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

// buildPREditedPayloadWithDirective creates a PR edited payload with a specific directive in the body.
func buildPREditedPayloadWithDirective(repoFullName string, prNumber int, title, author, body string) []byte {
	// Map GitHub usernames to consistent numeric IDs for testing (same as harness.go)
	githubUserIDMap := map[string]int64{
		"test-user":         100001,
		"draft-user":        100002,
		"draft-author":      100003,
		"channel-test-user": 100004,
	}

	githubUserID, exists := githubUserIDMap[author]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	payload := map[string]interface{}{
		"action": "edited",
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      body,
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     "open",
			"draft":     false,
			"additions": 50,
			"deletions": 30,
			"user": map[string]interface{}{
				"id":    githubUserID, // Add numeric GitHub user ID
				"login": author,
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func sendGitHubWebhook(t *testing.T, harness *TestHarness, eventType string, payload []byte) *http.Response {
	t.Helper()

	signature := generateWebhookSignature(payload, harness.Config().GitHubWebhookSecret)
	req := buildWebhookRequest(t, harness.BaseURL()+"/webhooks/github", eventType, payload, signature)

	// Use a regular HTTP client for requests to our test server, not the mocked one
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp
}

func buildWebhookRequest(t *testing.T, url, eventType string, payload []byte, signature string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-Github-Delivery", fmt.Sprintf("test-delivery-%d", time.Now().UnixNano()))

	return req
}

func generateWebhookSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
