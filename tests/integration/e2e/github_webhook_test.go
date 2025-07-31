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
	"net/http"
	"testing"
	"time"

	"github-slack-notifier/internal/models"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

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

		// Verify job was queued and executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		job := jobs[0]
		assert.Equal(t, models.JobTypeGitHubWebhook, job.Type)

		// Verify the webhook job payload
		var webhookJob models.WebhookJob
		require.NoError(t, json.Unmarshal(job.Payload, &webhookJob))
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
		assert.Equal(t, channel, message.Channel)
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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data in Firestore
		setupTestUser(t, harness, "draft-author", "U123456789", "draft-channel")
		setupTestRepo(t, harness, "draft-channel")

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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U987654321")

		// Setup test data
		setupTestUser(t, harness, "reviewer", "U987654321", "test-channel")
		setupTestRepo(t, harness, "test-channel")

		// Create a tracked message (simulating a previous PR notification)
		setupTrackedMessage(t, harness, "testorg/testrepo", 456, "test-channel", "T123456789", "1234567890.123456")

		// Wait a moment to ensure the data is persisted
		time.Sleep(10 * time.Millisecond)

		// Create review webhook payload
		payload := buildReviewSubmittedPayload("testorg/testrepo", 456, "reviewer", "approved")

		// Send webhook to application
		resp := sendGitHubWebhook(t, harness, "pull_request_review", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

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

		// Verify all jobs were executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Len(t, jobs, numWebhooks)
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
				// Clear any existing data
				require.NoError(t, harness.ClearFirestore(ctx))
				harness.FakeCloudTasks().ClearExecutedJobs()
				harness.SlackRequestCapture().Clear()

				// Setup OAuth workspace
				setupTestWorkspace(t, harness, "U123456789")

				// Setup test data
				setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
				setupTestRepo(t, harness, "test-channel")

				// Create PR webhook payload with specific line counts
				payload := buildPRPayloadWithSize("testorg/testrepo", 2000, tc.name, "test-user",
					"opened", false, tc.additions, tc.deletions)

				// Send webhook
				resp := sendGitHubWebhook(t, harness, "pull_request", payload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Wait for job execution
				jobs := harness.FakeCloudTasks().GetExecutedJobs()
				require.Len(t, jobs, 1)

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
				// Clear any existing data
				require.NoError(t, harness.ClearFirestore(ctx))
				harness.FakeCloudTasks().ClearExecutedJobs()
				harness.SlackRequestCapture().Clear()

				// Setup OAuth workspace
				setupTestWorkspace(t, harness, "U987654321")

				// Setup test data
				setupTestUser(t, harness, "reviewer", "U987654321", "test-channel")
				setupTestRepo(t, harness, "test-channel")

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

				// Wait for job execution
				jobs := harness.FakeCloudTasks().GetExecutedJobs()
				require.Len(t, jobs, 1)

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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")

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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")

		// Create payload with channel override directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 501, "PR with channel override", "test-user", "!review: #override-channel")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify message was sent to override channel
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "override-channel", slackRequests[0].Channel)
		assert.Contains(t, slackRequests[0].Text, "PR with channel override")
	})

	t.Run("PR directives - user CC", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")

		// Create payload with user CC directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 502, "PR with user CC", "test-user", "!review: @jane.smith")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify message was sent with CC
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Contains(t, slackRequests[0].Text, "(cc: @jane.smith)")
		assert.Contains(t, slackRequests[0].Text, "PR with user CC")
	})

	t.Run("PR directives - combined channel and user CC", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")

		// Create payload with combined directive
		payload := buildPRPayloadWithDirective("testorg/testrepo", 503, "PR with combined directives", "test-user",
			"!review: #combined-channel @tech.lead")

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify message was sent to override channel with CC
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "combined-channel", slackRequests[0].Channel)
		assert.Contains(t, slackRequests[0].Text, "(cc: @tech.lead)")
		assert.Contains(t, slackRequests[0].Text, "PR with combined directives")
	})

	t.Run("PR directives - last directive wins", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "default-channel")
		setupTestRepo(t, harness, "default-channel")

		// Create payload with multiple directives - last one should win
		body := "!review: #first-channel @first.user\n!review: #final-channel @final.user"
		payload := buildPRPayloadWithDirective("testorg/testrepo", 504, "PR with multiple directives", "test-user", body)

		// Send webhook
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// Verify message was sent to final channel with final user (last directive wins)
		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1)
		assert.Equal(t, "final-channel", slackRequests[0].Channel)
		assert.Contains(t, slackRequests[0].Text, "(cc: @final.user)")
		assert.Contains(t, slackRequests[0].Text, "PR with multiple directives")
	})

	t.Run("PR directives - retroactive message deletion with !review-skip", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")

		// First, create a PR (which will post a message)
		payload := buildPRPayloadWithDirective("testorg/testrepo", 505, "PR to be deleted", "test-user", "Initial PR description")

		// Send webhook for PR opened
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed and message was posted
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be posted initially")

		// Clear for the edit event
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()
		httpmock.Reset()
		harness.SetupMockResponses()

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
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// Reset httpmock call count
		httpmock.Reset()

		// Re-setup mock responses
		harness.SetupMockResponses()

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")

		// Step 1: Create a PR (which will post a message)
		payload := buildPRPayloadWithDirective("testorg/testrepo", 506, "Test skip/unskip sequence", "test-user", "Initial PR description")

		// Send webhook for PR opened
		resp := sendGitHubWebhook(t, harness, "pull_request", payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed and message was posted initially
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be posted initially")
		initialMessage := slackRequests[0]
		assert.Equal(t, "test-channel", initialMessage.Channel)
		assert.Contains(t, initialMessage.Text, "Test skip/unskip sequence")

		// Step 2: Edit the PR to add !review-skip directive (should delete message)
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()
		httpmock.Reset()
		harness.SetupMockResponses()

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
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()
		httpmock.Reset()
		harness.SetupMockResponses()

		unskipPayload := buildPREditedPayloadWithDirective("testorg/testrepo", 506, "Test skip/unskip sequence", "test-user",
			"Regular PR description without skip")

		// Send webhook for PR edited without skip
		resp = sendGitHubWebhook(t, harness, "pull_request", unskipPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify job was executed for the unskip edit
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1)

		// THIS IS THE KEY ASSERTION: Verify the PR message was re-posted to Slack
		slackRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected PR message to be re-posted after removing skip directive")

		repostedMessage := slackRequests[0]
		assert.Equal(t, "test-channel", repostedMessage.Channel)
		assert.Contains(t, repostedMessage.Text, "Test skip/unskip sequence")
		assert.Contains(t, repostedMessage.Text, "https://github.com/testorg/testrepo/pull/506")
	})
}

// Helper functions

func buildPROpenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "opened", false)
}

func buildDraftPROpenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "opened", true)
}

func buildPRPayload(repoFullName string, prNumber int, title, author, action string, draft bool) []byte {
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
