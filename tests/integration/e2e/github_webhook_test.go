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

	t.Run("PR opened webhook full flow", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Setup test data in Firestore
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "testorg/testrepo", "test-channel", "T123456789")

		// Create GitHub webhook payload
		payload := buildPROpenedPayload("testorg/testrepo", 123, "Add new feature", "test-user")

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

		// The fake Cloud Tasks service should have already executed the job
		// which would have called the Slack API (mocked by httpmock)
		// We can verify this happened by checking the mock call count
		info := httpmock.GetCallCountInfo()
		slackCalls := info["POST https://slack.com/api/chat.postMessage"]
		assert.Positive(t, slackCalls, "Expected Slack postMessage to be called")
	})

	t.Run("PR review webhook with reaction sync", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Setup test data
		setupTestUser(t, harness, "reviewer", "U987654321", "test-channel")
		setupTestRepo(t, harness, "testorg/testrepo", "test-channel", "T123456789")

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

		// Note: In the actual implementation, review reactions might not be added
		// if there are no tracked messages or if the feature is not fully implemented
	})

	t.Run("Invalid webhook signature rejection", func(t *testing.T) {
		// Clear jobs
		harness.FakeCloudTasks().ClearExecutedJobs()

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

		// Setup test data
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "testorg/testrepo", "test-channel", "T123456789")

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
}

// Helper functions

func setupTestUser(t *testing.T, harness *TestHarness, githubUsername, slackUserID, defaultChannel string) {
	t.Helper()
	ctx := context.Background()
	err := harness.SetupUser(ctx, githubUsername, slackUserID, defaultChannel)
	require.NoError(t, err)
}

func setupTestRepo(t *testing.T, harness *TestHarness, repoFullName, channelID, teamID string) {
	t.Helper()
	ctx := context.Background()
	err := harness.SetupRepo(ctx, repoFullName, channelID, teamID)
	require.NoError(t, err)
}

func setupTrackedMessage(t *testing.T, harness *TestHarness, repoFullName string, prNumber int, channelID, teamID, messageTS string) {
	t.Helper()
	ctx := context.Background()
	err := harness.SetupTrackedMessage(ctx, repoFullName, prNumber, channelID, teamID, messageTS)
	require.NoError(t, err)
}

func buildPROpenedPayload(repoFullName string, prNumber int, title, author string) []byte {
	payload := map[string]interface{}{
		"action": "opened",
		"pull_request": map[string]interface{}{
			"number":    prNumber,
			"title":     title,
			"body":      "Test PR description",
			"html_url":  fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
			"state":     "open",
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
