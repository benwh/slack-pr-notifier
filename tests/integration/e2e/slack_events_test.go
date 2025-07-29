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
	"strconv"
	"testing"
	"time"

	"github-slack-notifier/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackEventsIntegration(t *testing.T) {
	// Setup test harness - this starts the real application
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Setup mock responses for external APIs
	harness.SetupMockResponses()

	// Context for database operations
	ctx := context.Background()

	t.Run("Manual PR link detection", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "C1234567890")

		// Create Slack event payload with PR link
		messageText := "Hey team, please review this PR: https://github.com/testorg/testrepo/pull/123"
		payload := buildSlackMessageEvent(messageText, "C1234567890", "U123456789", "1234567890.123456", "T123456789")

		// Send event to application
		resp := sendSlackEvent(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify jobs were queued and executed
		// We expect 2 jobs: manual link job + reaction sync job
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		require.Len(t, jobs, 2)

		// Find the manual link job
		var manualLinkJob *models.Job
		var reactionSyncJob *models.Job
		for _, j := range jobs {
			if j.Type == models.JobTypeManualPRLink {
				manualLinkJob = j
			} else if j.Type == models.JobTypeReactionSync {
				reactionSyncJob = j
			}
		}

		require.NotNil(t, manualLinkJob, "Expected a manual link job")
		require.NotNil(t, reactionSyncJob, "Expected a reaction sync job")

		// Verify the manual link job payload
		var linkJob models.ManualLinkJob
		require.NoError(t, json.Unmarshal(manualLinkJob.Payload, &linkJob))
		assert.Equal(t, "testorg/testrepo", linkJob.RepoFullName)
		assert.Equal(t, 123, linkJob.PRNumber)
		assert.Equal(t, "C1234567890", linkJob.SlackChannel)
		assert.Equal(t, "1234567890.123456", linkJob.SlackMessageTS)
		assert.Equal(t, "T123456789", linkJob.SlackTeamID)

		// Verify the reaction sync job payload
		var syncJob models.ReactionSyncJob
		require.NoError(t, json.Unmarshal(reactionSyncJob.Payload, &syncJob))
		assert.Equal(t, "testorg/testrepo", syncJob.RepoFullName)
		assert.Equal(t, 123, syncJob.PRNumber)

		// Note: Manual PR link detection only creates a tracked message
		// It doesn't fetch PR details from GitHub - that happens when
		// GitHub sends webhook events for the PR
	})

	t.Run("Multiple PR links in one message are ignored", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test data
		setupTestUser(t, harness, "test-user", "U123456789", "test-channel")
		setupTestRepo(t, harness, "C1234567890")

		// Create message with multiple PR links
		messageText := `Check these PRs:
		- https://github.com/testorg/testrepo/pull/100
		- https://github.com/testorg/testrepo/pull/200
		- https://github.com/testorg/testrepo/pull/300`

		payload := buildSlackMessageEvent(messageText, "C1234567890", "U123456789", "1234567890.123456", "T123456789")

		// Send event to application
		resp := sendSlackEvent(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify NO jobs were queued - multiple PR links are ignored by design
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Empty(t, jobs, "Expected no jobs - multiple PR links in one message are ignored")
	})

	t.Run("Invalid Slack signature rejection", func(t *testing.T) {
		// Clear jobs
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Create valid payload
		payload := buildSlackMessageEvent("Test message", "C1234567890", "U123456789", "1234567890.123456", "T123456789")

		// Send with invalid signature
		req := buildSlackEventRequest(t, harness.BaseURL()+"/webhooks/slack/events", payload, "invalid-signature", time.Now())
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

	t.Run("URL verification challenge", func(t *testing.T) {
		// Create URL verification payload
		challenge := "test-challenge-string"
		payload := buildSlackURLVerification(challenge)

		// Send to application
		resp := sendSlackEvent(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify response contains just the challenge string (not JSON)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, challenge, string(body))

		// No jobs should be queued for URL verification
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Empty(t, jobs)
	})

	t.Run("Bot message ignored", func(t *testing.T) {
		// Clear jobs
		harness.FakeCloudTasks().ClearExecutedJobs()

		// Create bot message event
		payload := buildSlackBotMessageEvent(
			"Bot message with https://github.com/test/repo/pull/123",
			"C1234567890", "B123456789", "1234567890.123456", "T123456789",
		)

		// Send event to application
		resp := sendSlackEvent(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// No jobs should be queued for bot messages
		jobs := harness.FakeCloudTasks().GetExecutedJobs()
		assert.Empty(t, jobs)
	})

	t.Run("App Home opened event", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))

		// Setup OAuth workspace first (required for multi-workspace support)
		setupTestWorkspace(t, harness, "U123456789")

		// Setup test user with default channel
		setupTestUser(t, harness, "test-user", "U123456789", "C1234567890")

		// Create app home opened event
		payload := createAppHomeOpenedEvent("U123456789", "T123456789")

		// Send event to application
		resp := sendSlackEvent(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// The app home opened event should trigger a views.publish call
		// but we can't easily verify that in an e2e test without
		// checking the actual Slack API calls
	})
}

// Helper functions

func buildSlackMessageEvent(text, channel, user, ts, teamID string) []byte {
	payload := map[string]interface{}{
		"type": "event_callback",
		"event": map[string]interface{}{
			"type":     "message",
			"channel":  channel,
			"user":     user,
			"text":     text,
			"ts":       ts,
			"event_ts": ts,
		},
		"team_id": teamID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func buildSlackBotMessageEvent(text, channel, botID, ts, teamID string) []byte {
	payload := map[string]interface{}{
		"type": "event_callback",
		"event": map[string]interface{}{
			"type":     "message",
			"channel":  channel,
			"bot_id":   botID,
			"text":     text,
			"ts":       ts,
			"event_ts": ts,
		},
		"team_id": teamID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func buildSlackURLVerification(challenge string) []byte {
	payload := map[string]interface{}{
		"type":      "url_verification",
		"challenge": challenge,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func createAppHomeOpenedEvent(userID, teamID string) []byte {
	payload := map[string]interface{}{
		"type": "event_callback",
		"event": map[string]interface{}{
			"type": "app_home_opened",
			"user": userID,
			"tab":  "home",
		},
		"team_id": teamID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return data
}

func sendSlackEvent(t *testing.T, harness *TestHarness, payload []byte) *http.Response {
	t.Helper()

	timestamp := time.Now()
	signature := generateSlackSignature(payload, harness.Config().SlackSigningSecret, timestamp)
	req := buildSlackEventRequest(t, harness.BaseURL()+"/webhooks/slack/events", payload, signature, timestamp)

	// Use a regular HTTP client for requests to our test server, not the mocked one
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)

	return resp
}

func buildSlackEventRequest(t *testing.T, url string, payload []byte, signature string, timestamp time.Time) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	req.Header.Set("X-Slack-Signature", signature)

	return req
}

func generateSlackSignature(body []byte, secret string, timestamp time.Time) string {
	baseString := fmt.Sprintf("v0:%d:%s", timestamp.Unix(), string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(baseString))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}
