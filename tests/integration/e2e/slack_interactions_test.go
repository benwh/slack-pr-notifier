package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackInteractionsIntegration(t *testing.T) {
	// Setup test harness - this starts the real application
	harness := NewTestHarness(t)
	defer harness.Cleanup()

	// Setup mock responses for external APIs
	harness.SetupMockResponses()

	// Context for database operations
	ctx := context.Background()

	t.Run("App Home channel selection", func(t *testing.T) {
		// Clear any existing data
		require.NoError(t, harness.ClearFirestore(ctx))

		// Setup test user
		setupTestUser(t, harness, "test-user", "U123456789", "")

		// Create channel selection view submission payload
		payload := createChannelSelectionViewSubmissionPayload("U123456789", "C987654321", "T123456789")

		// Send interaction to application
		resp := sendSlackInteraction(t, harness, payload)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Parse response to check for expected action
		var response map[string]interface{}
		err := json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.Equal(t, "clear", response["response_action"])
	})

	t.Run("Invalid interaction signature rejection", func(t *testing.T) {
		// Create valid payload
		payload := createChannelSelectionViewSubmissionPayload("U123456789", "C987654321", "T123456789")

		// Send with invalid signature
		req := createSlackInteractionRequest(t, harness.BaseURL()+"/webhooks/slack/interactions", payload, "invalid-signature", time.Now())
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Should be rejected
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// Helper functions

func createChannelSelectionViewSubmissionPayload(userID, channelID, teamID string) string {
	interaction := map[string]interface{}{
		"type": "view_submission",
		"user": map[string]interface{}{
			"id": userID,
		},
		"team": map[string]interface{}{
			"id": teamID,
		},
		"view": map[string]interface{}{
			"callback_id": "channel_selector",
			"state": map[string]interface{}{
				"values": map[string]interface{}{
					"channel_input": map[string]interface{}{
						"channel_select": map[string]interface{}{
							"type":             "channels_select",
							"selected_channel": channelID,
						},
					},
				},
			},
		},
		"response_urls": []interface{}{},
	}

	data, err := json.Marshal(interaction)
	if err != nil {
		panic(err) // Test helper, panic is acceptable
	}
	return string(data)
}

func sendSlackInteraction(t *testing.T, harness *TestHarness, payload string) *http.Response {
	t.Helper()

	timestamp := time.Now()

	// URL encode the payload as it would be sent in the form data
	formData := url.Values{}
	formData.Set("payload", payload)
	body := formData.Encode()

	signature := generateSlackSignature([]byte(body), harness.Config().SlackSigningSecret, timestamp)
	req := createSlackInteractionRequest(t, harness.BaseURL()+"/webhooks/slack/interactions", payload, signature, timestamp)

	// Use a regular HTTP client for requests to our test server, not the mocked one
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)

	return resp
}

func createSlackInteractionRequest(t *testing.T, targetURL, payload, signature string, timestamp time.Time) *http.Request {
	t.Helper()

	// URL encode the payload
	formData := url.Values{}
	formData.Set("payload", payload)
	body := formData.Encode()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, targetURL, bytes.NewBufferString(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	req.Header.Set("X-Slack-Signature", signature)

	return req
}
