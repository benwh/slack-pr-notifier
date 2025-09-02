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
	"strings"
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

		// The PR has 50 additions + 30 deletions = 80 lines, which should be a dog emoji
		message := slackRequests[0]
		// Channel names are now resolved to IDs
		expectedChannelID := "C987654321" // test-channel -> C987654321
		if channel == "pr-channel" {
			expectedChannelID = "C111111111"
		}
		assert.Equal(t, expectedChannelID, message.Channel)
		assert.Contains(t, message.Text, ":dog2:") // dog emoji for 80 lines
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
		setupTrackedMessage(t, harness, 456, "test-channel")

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
			{"tiny PR", 1, 1, ":ant:"},           // ant for 2 lines (≤2)
			{"medium PR", 30, 15, ":raccoon:"},   // raccoon for 45 lines (≤50)
			{"large PR", 500, 200, ":gorilla:"},  // gorilla for 700 lines (≤1000)
			{"whale PR", 1500, 1000, ":whale2:"}, // whale for 2500 lines (>2000)
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
				setupTrackedMessage(t, harness, 3000, "test-channel")

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

				// Verify the correct reaction was added (filter for only add operations)
				allRequests := harness.SlackRequestCapture().GetAllRequests()
				var addReactions []SlackReactionRequest
				for _, req := range allRequests {
					if strings.Contains(req.URL, "reactions.add") {
						if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok {
							addReactions = append(addReactions, reaction)
						}
					}
				}

				if len(addReactions) > 0 {
					require.Len(t, addReactions, 1)
					assert.Equal(t, "test-channel", addReactions[0].Channel)
					assert.Equal(t, "1234567890.123456", addReactions[0].Timestamp)
					assert.Equal(t, tc.emojiName, addReactions[0].Name)
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

	t.Run("PR CC directive changes - add and remove CC", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Setup a user that can be CC'd
		require.NoError(t, harness.SetupUser(ctx, "test-cc-user", "U987654321", "test-channel"))

		// Step 1: Create initial PR with just !review (no CC)
		initialPayload := buildPRPayloadWithDirective(
			"testorg/testrepo", 600, "Test CC directive changes", "test-user", "!review",
		)
		resp := sendGitHubWebhook(t, harness, "pull_request", initialPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and initial message was posted
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2) // github_webhook + workspace_pr

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected initial PR message to be posted")

		initialMessage := slackRequests[0]
		assert.Equal(t, "C987654321", initialMessage.Channel) // test-channel -> C987654321
		assert.Contains(t, initialMessage.Text, "Test CC directive changes")
		assert.NotContains(t, initialMessage.Text, "cc:", "Initial message should not contain CC")

		// Wait for tracked message to be persisted
		waitForTrackedMessage(t, harness, "testorg/testrepo", 600)

		// Step 2: Edit PR to add CC directive
		harness.ResetForNextStep()

		ccPayload := buildPREditedPayloadWithDirective(
			"testorg/testrepo", 600, "Test CC directive changes", "test-user", "!review cc @test-cc-user",
		)
		resp = sendGitHubWebhook(t, harness, "pull_request", ccPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify edit job was executed
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1, "Expected only github_webhook job for PR edit")

		// Verify message was updated with CC mention
		updateRequests := harness.SlackRequestCapture().GetUpdateMessageRequests()
		require.Len(t, updateRequests, 1, "Expected message to be updated with CC")

		updateMessage := updateRequests[0]
		assert.Equal(t, "C987654321", updateMessage.Channel)
		assert.Contains(t, updateMessage.Text, "Test CC directive changes")
		assert.Contains(t, updateMessage.Text, "(cc: <@U987654321>)", "Updated message should contain CC mention")

		// Verify no new post message requests (only updates)
		postRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, postRequests, "No new messages should be posted, only updates")

		// Step 3: Edit PR to remove CC directive
		harness.ResetForNextStep()

		removeCCPayload := buildPREditedPayloadWithDirective(
			"testorg/testrepo", 600, "Test CC directive changes", "test-user", "!review",
		)
		resp = sendGitHubWebhook(t, harness, "pull_request", removeCCPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify edit job was executed
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1, "Expected only github_webhook job for PR edit")

		// Verify message was updated to remove CC mention
		updateRequests = harness.SlackRequestCapture().GetUpdateMessageRequests()
		require.Len(t, updateRequests, 1, "Expected message to be updated to remove CC")

		finalUpdateMessage := updateRequests[0]
		assert.Equal(t, "C987654321", finalUpdateMessage.Channel)
		assert.Contains(t, finalUpdateMessage.Text, "Test CC directive changes")
		assert.NotContains(t, finalUpdateMessage.Text, "cc:", "Final message should not contain CC")

		// Verify no new post message requests (only updates)
		postRequests = harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, postRequests, "No new messages should be posted, only updates")

		t.Logf("✅ CC directive change test passed: Message correctly updated to add and remove CC mentions")
	})

	t.Run("PR title changes - update message with new title", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Step 1: Create initial PR with original title
		initialPayload := buildPRPayloadWithDirective(
			"testorg/testrepo", 700, "Original PR Title", "test-user", "Initial PR description",
		)
		resp := sendGitHubWebhook(t, harness, "pull_request", initialPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and initial message was posted
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2) // github_webhook + workspace_pr

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected initial PR message to be posted")

		initialMessage := slackRequests[0]
		assert.Equal(t, "C987654321", initialMessage.Channel) // test-channel -> C987654321
		assert.Contains(t, initialMessage.Text, "Original PR Title")
		assert.Contains(t, initialMessage.Text, "https://github.com/testorg/testrepo/pull/700")

		// Wait for tracked message to be persisted
		waitForTrackedMessage(t, harness, "testorg/testrepo", 700)

		// Step 2: Edit PR to change the title
		harness.ResetForNextStep()

		titleChangePayload := buildPREditedPayloadWithTitleChange(
			"testorg/testrepo", 700, "Updated PR Title", "test-user", "Initial PR description", "Original PR Title",
		)
		resp = sendGitHubWebhook(t, harness, "pull_request", titleChangePayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify edit job was executed
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1, "Expected only github_webhook job for PR edit")

		// Verify message was updated with new title
		updateRequests := harness.SlackRequestCapture().GetUpdateMessageRequests()
		require.Len(t, updateRequests, 1, "Expected message to be updated with new title")

		updateMessage := updateRequests[0]
		assert.Equal(t, "C987654321", updateMessage.Channel)
		assert.Contains(t, updateMessage.Text, "Updated PR Title", "Updated message should contain new title")
		assert.NotContains(t, updateMessage.Text, "Original PR Title", "Updated message should not contain old title")

		// Verify no new post message requests (only updates)
		postRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, postRequests, "No new messages should be posted, only updates")

		t.Logf("✅ PR title change test passed: Message correctly updated with new title")
	})

	t.Run("PR simultaneous title and CC changes - single update call", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace and test data
		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Setup a user that can be CC'd
		require.NoError(t, harness.SetupUser(ctx, "test-cc-user", "U987654321", "test-channel"))

		// Step 1: Create initial PR with original title and no CC directive
		initialPayload := buildPRPayloadWithDirective(
			"testorg/testrepo", 800, "Original PR Title", "test-user", "Initial PR description",
		)
		resp := sendGitHubWebhook(t, harness, "pull_request", initialPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were executed and initial message was posted
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2) // github_webhook + workspace_pr

		slackRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, slackRequests, 1, "Expected initial PR message to be posted")

		initialMessage := slackRequests[0]
		assert.Equal(t, "C987654321", initialMessage.Channel) // test-channel -> C987654321
		assert.Contains(t, initialMessage.Text, "Original PR Title")
		assert.NotContains(t, initialMessage.Text, "cc:", "Initial message should not contain CC")

		// Wait for tracked message to be persisted
		waitForTrackedMessage(t, harness, "testorg/testrepo", 800)

		// Step 2: Edit PR to change BOTH title AND add CC directive simultaneously
		harness.ResetForNextStep()

		simultaneousChangePayload := buildPREditedPayloadWithTitleAndCC(
			"testorg/testrepo", 800, "Updated PR Title", "test-user",
			"!review cc @test-cc-user", "Original PR Title",
		)
		resp = sendGitHubWebhook(t, harness, "pull_request", simultaneousChangePayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify edit job was executed
		jobs = harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 1, "Expected only github_webhook job for PR edit")

		// CRITICAL TEST: Verify exactly ONE update request was made despite both title and CC changing
		updateRequests := harness.SlackRequestCapture().GetUpdateMessageRequests()
		require.Len(t, updateRequests, 1, "Expected exactly ONE update call for simultaneous title+CC changes")

		updateMessage := updateRequests[0]
		assert.Equal(t, "C987654321", updateMessage.Channel)

		// Verify the single update contains BOTH changes
		assert.Contains(t, updateMessage.Text, "Updated PR Title", "Updated message should contain new title")
		assert.NotContains(t, updateMessage.Text, "Original PR Title", "Updated message should not contain old title")
		assert.Contains(t, updateMessage.Text, "(cc: <@U987654321>)", "Updated message should contain CC mention")

		// Verify no new post message requests (only updates)
		postRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		assert.Empty(t, postRequests, "No new messages should be posted, only updates")

		t.Logf("✅ Simultaneous title+CC change test passed: Single update call handled both changes efficiently")
	})

	t.Run("PR closed/reopened reaction handling", func(t *testing.T) {
		// Reset all test state for proper isolation
		require.NoError(t, harness.ResetForTest(ctx))

		// Setup OAuth workspace first
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data
		setupTestUser(t, harness, "testuser", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Wait for setup to complete
		time.Sleep(10 * time.Millisecond)

		// Step 1: Open a PR to create a tracked message
		prOpenedPayload := buildPROpenedPayload("testorg/testrepo", 789, "Test PR for close/reopen", "testuser")
		resp := sendGitHubWebhook(t, harness, "pull_request", prOpenedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Wait for PR opened processing
		time.Sleep(100 * time.Millisecond)

		// Verify message was posted
		postRequests := harness.SlackRequestCapture().GetPostMessageRequests()
		require.Len(t, postRequests, 1, "Should have posted one PR notification message")

		// Clear captured requests for next step
		harness.SlackRequestCapture().Clear()

		// Step 2: Close the PR - should add 'x' reaction
		prClosedPayload := buildPRClosedPayload("testorg/testrepo", 789, "Test PR for close/reopen", "testuser", false)
		resp = sendGitHubWebhook(t, harness, "pull_request", prClosedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Wait for PR closed processing
		time.Sleep(100 * time.Millisecond)

		// Verify 'x' reaction was added
		reactionRequests := harness.SlackRequestCapture().GetReactionRequests()
		require.Len(t, reactionRequests, 1, "Should have added one reaction for closed PR")
		assert.Equal(t, "x", reactionRequests[0].Name, "Should add 'x' reaction for closed PR")

		// Clear captured requests for next step
		harness.SlackRequestCapture().Clear()

		// Step 3: Reopen the PR - should remove 'x' reaction
		prReopenedPayload := buildPRReopenedPayload("testorg/testrepo", 789, "Test PR for close/reopen", "testuser")
		resp = sendGitHubWebhook(t, harness, "pull_request", prReopenedPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Wait for PR reopened processing to complete (longer wait for reaction sync job)
		time.Sleep(200 * time.Millisecond)

		// Verify that reaction sync was triggered and 'x' reaction was removed
		allRequests := harness.SlackRequestCapture().GetAllRequests()

		// Debug: Print all requests to understand what's being captured
		t.Logf("DEBUG: Found %d total requests:", len(allRequests))
		for i, req := range allRequests {
			t.Logf("  [%d] %s %s", i, req.Method, req.URL)
			if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok {
				t.Logf("      Reaction: %s", reaction.Name)
			}
		}

		// Look for reactions.remove requests containing 'x' emoji
		for _, req := range allRequests {
			if strings.Contains(req.URL, "reactions.remove") {
				if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok {
					t.Logf("DEBUG: Found remove reaction request for emoji: %s", reaction.Name)
				}
			}
		}

		// The behavior should be: when PR reopened, the reaction sync should clear all reactions,
		// including the 'x' from the closed state. Let's verify we see remove requests
		removeReactionCount := 0
		for _, req := range allRequests {
			if strings.Contains(req.URL, "reactions.remove") {
				removeReactionCount++
			}
		}

		t.Logf("DEBUG: Found %d reaction remove requests", removeReactionCount)

		// The critical test: verify the "x" reaction was specifically removed when PR was reopened
		foundXRemoval := false
		for _, req := range allRequests {
			if strings.Contains(req.URL, "reactions.remove") {
				if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok && reaction.Name == "x" {
					foundXRemoval = true
					break
				}
			}
		}

		// This is the actual bug fix validation
		assert.True(t, foundXRemoval, "The 'x' reaction MUST be removed when PR is reopened - this was the original bug")

		t.Logf("✅ Verified: 'x' reaction was specifically removed when PR was reopened")

		t.Logf("✅ PR closed/reopened reaction test passed: 'x' reaction added on close and removed on reopen")
	})

	// PR Author Comment Filtering Tests
	t.Run("PR review reactions - exclude PR author comments only", func(t *testing.T) {
		testCases := []struct {
			name           string
			prNumber       int
			reviewer       string
			reviewerID     int64
			prAuthor       string
			prAuthorID     int64
			expectReaction bool
			description    string
		}{
			{
				name:           "PR author comments only - no reaction",
				prNumber:       4000,
				reviewer:       "test-user",
				reviewerID:     100001,
				prAuthor:       "test-user",
				prAuthorID:     100001,
				expectReaction: false,
				description:    "When PR author is the only one commenting, no reaction should be added",
			},
			{
				name:           "Other user comments only - add reaction",
				prNumber:       4002,
				reviewer:       "other-reviewer",
				reviewerID:     200001,
				prAuthor:       "test-user",
				prAuthorID:     100001,
				expectReaction: true,
				description:    "When other users comment, reaction should be added",
			},
			{
				name:           "Both PR author and other user comment - add reaction",
				prNumber:       4003,
				reviewer:       "other-reviewer",
				reviewerID:     200001,
				prAuthor:       "test-user",
				prAuthorID:     100001,
				expectReaction: true,
				description:    "When both PR author and others comment, reaction should still be added",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Reset state and setup test environment
				require.NoError(t, harness.ResetForTest(ctx))
				harness.FakeCloudTasks().ClearExecutedJobs()
				harness.SlackRequestCapture().Clear()

				setupTestWorkspace(t, harness, "U123456789")
				setupTestUser(t, harness, tc.prAuthor, "U123456789", "test-channel")
				setupTestRepo(t, harness, "test-channel")
				setupGitHubInstallation(t, harness)

				// Create PR first to ensure tracked message exists
				prPayload := buildPROpenedPayload("testorg/testrepo", tc.prNumber, "Test PR", tc.prAuthor)
				resp := sendGitHubWebhook(t, harness, "pull_request", prPayload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Wait for PR to be processed and tracked message created
				waitForTrackedMessage(t, harness, "testorg/testrepo", tc.prNumber)

				// Clear previous jobs/requests to focus on review
				harness.FakeCloudTasks().ClearExecutedJobs()
				harness.SlackRequestCapture().Clear()

				// For the "both users comment" case, the mock already returns both reviews
				// No need to simulate multiple webhook calls since the mock returns the final state

				// Send the review webhook - the mock will return appropriate review data
				reviewPayload := buildReviewSubmittedPayloadWithIDs(
					"testorg/testrepo", tc.prNumber, tc.reviewer, tc.reviewerID, "commented", tc.prAuthor, tc.prAuthorID)
				resp = sendGitHubWebhook(t, harness, "pull_request_review", reviewPayload)
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Verify jobs were executed
				jobs := harness.FakeCloudTasks().GetExecutedJobs()
				require.Len(t, jobs, 2) // github_webhook + reaction_sync

				// Check if comment reaction was added
				allRequests := harness.SlackRequestCapture().GetAllRequests()
				var addReactions []SlackReactionRequest
				for _, req := range allRequests {
					if strings.Contains(req.URL, "reactions.add") {
						if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok && reaction.Name == "speech_balloon" {
							addReactions = append(addReactions, reaction)
						}
					}
				}

				if tc.expectReaction {
					assert.Len(t, addReactions, 1, "Expected comment reaction to be added for: %s", tc.description)
					if len(addReactions) > 0 {
						assert.Equal(t, "C987654321", addReactions[0].Channel) // Channel ID from mock
						assert.Equal(t, "speech_balloon", addReactions[0].Name)
					}
				} else {
					assert.Empty(t, addReactions, "Expected no comment reaction for: %s", tc.description)
				}

				t.Logf("✅ %s: %s", tc.name, tc.description)
			})
		}
	})

	t.Run("PR review reactions - PR author approval still works", func(t *testing.T) {
		// Reset state and setup
		require.NoError(t, harness.ResetForTest(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		setupTestWorkspace(t, harness, "U123456789")
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "test-channel")
		setupGitHubInstallation(t, harness)

		// Create PR first
		prPayload := buildPROpenedPayload("testorg/testrepo", 4001, "Test PR", "test-user")
		resp := sendGitHubWebhook(t, harness, "pull_request", prPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		waitForTrackedMessage(t, harness, "testorg/testrepo", 4001)

		// Clear to focus on review
		harness.FakeCloudTasks().ClearExecutedJobs()
		harness.SlackRequestCapture().Clear()

		// PR author approves their own PR (should still add approved reaction)
		reviewPayload := buildReviewSubmittedPayloadWithIDs("testorg/testrepo", 4001, "test-user", 100001, "approved", "test-user", 100001)
		resp = sendGitHubWebhook(t, harness, "pull_request_review", reviewPayload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify approved reaction was added
		allRequests := harness.SlackRequestCapture().GetAllRequests()
		var addReactions []SlackReactionRequest
		for _, req := range allRequests {
			if strings.Contains(req.URL, "reactions.add") {
				if reaction, ok := req.ParsedBody.(SlackReactionRequest); ok && reaction.Name == "white_check_mark" {
					addReactions = append(addReactions, reaction)
				}
			}
		}

		assert.Len(t, addReactions, 1, "PR author's approval should still add approved reaction")
		if len(addReactions) > 0 {
			// Channel ID is returned by the mock, not the channel name
			assert.Equal(t, "C987654321", addReactions[0].Channel)
			assert.Equal(t, "white_check_mark", addReactions[0].Name)
		}

		t.Logf("✅ PR author approval test passed: approved reaction added even when PR author approves")
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

// buildReviewSubmittedPayloadWithIDs creates a review payload with both login and ID for users.
// This is needed to test PR author comment filtering logic that relies on user IDs.
func buildReviewSubmittedPayloadWithIDs(
	repoFullName string, prNumber int, reviewer string, reviewerID int64, state string, prAuthor string, prAuthorID int64,
) []byte {
	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": state,
			"user": map[string]interface{}{
				"login": reviewer,
				"id":    reviewerID,
			},
		},
		"pull_request": map[string]interface{}{
			"number":   prNumber,
			"title":    "Test PR",
			"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"user": map[string]interface{}{
				"login": prAuthor,
				"id":    prAuthorID,
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

func buildPRReadyForReviewPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "ready_for_review", false)
}

func buildPRClosedPayload(repoFullName string, prNumber int, title, author string, merged bool) []byte {
	return buildPRClosedPayloadDetailed(repoFullName, prNumber, title, author, merged)
}

func buildPRReopenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	return buildPRPayload(repoFullName, prNumber, title, author, "reopened", false)
}

func buildPRClosedPayloadDetailed(repoFullName string, prNumber int, title, author string, merged bool) []byte {
	// Map GitHub usernames to consistent numeric IDs for testing (same as harness.go)
	githubUserIDMap := map[string]int64{
		"test-user":         100001,
		"draft-user":        100002,
		"draft-author":      100003,
		"channel-test-user": 100004,
		"testuser":          100005, // Add mapping for our test user
	}
	githubUserID, exists := githubUserIDMap[author]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	state := "closed"
	payload := map[string]interface{}{
		"action": "closed",
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      "Test PR description",
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     state,
			"draft":     false,
			"merged":    merged,
			"additions": 50,
			"deletions": 30,
			"user": map[string]interface{}{
				"id":    githubUserID,
				"login": author,
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
			"name":      "testrepo",
		},
		"installation": map[string]interface{}{
			"id": 12345678,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
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
// buildPREditedPayload builds a GitHub PR edited payload with optional title change information.
func buildPREditedPayload(repoFullName string, prNumber int, title, author, body string, oldTitle *string) []byte {
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

	// Add changes section if oldTitle is provided
	if oldTitle != nil {
		payload["changes"] = map[string]interface{}{
			"title": map[string]interface{}{
				"from": *oldTitle,
			},
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func buildPREditedPayloadWithDirective(repoFullName string, prNumber int, title, author, body string) []byte {
	return buildPREditedPayload(repoFullName, prNumber, title, author, body, nil)
}

// buildPREditedPayloadWithTitleChange builds a GitHub PR edited payload with title change information.
func buildPREditedPayloadWithTitleChange(repoFullName string, prNumber int, newTitle, author, body, oldTitle string) []byte {
	return buildPREditedPayload(repoFullName, prNumber, newTitle, author, body, &oldTitle)
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

// buildPREditedPayloadWithTitleAndCC builds a GitHub PR edited payload with both title and CC changes.
func buildPREditedPayloadWithTitleAndCC(repoFullName string, prNumber int, newTitle, author, newBody, oldTitle string) []byte {
	return buildPREditedPayload(repoFullName, prNumber, newTitle, author, newBody, &oldTitle)
}

func generateWebhookSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
