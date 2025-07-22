package testutil

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack/slackevents"
)

const testEventTimestampSuffix = 123

// HTTPTestHelpers provides utilities for creating HTTP requests for integration tests.
type HTTPTestHelpers struct{}

// SlackEventPayload represents a complete Slack Events API payload.
type SlackEventPayload struct {
	Token        string      `json:"token"`
	TeamID       string      `json:"team_id"`
	APIAppID     string      `json:"api_app_id"`
	Event        interface{} `json:"event"`
	Type         string      `json:"type"`
	EventID      string      `json:"event_id"`
	EventTime    int64       `json:"event_time"`
	Authed_users []string    `json:"authed_users,omitempty"`
}

// CreateSlackEventAPIPayload creates a complete Slack Events API payload.
func (HTTPTestHelpers) CreateSlackEventAPIPayload(eventType string, innerEvent interface{}, teamID string) []byte {
	payload := SlackEventPayload{
		Token:     "test-verification-token",
		TeamID:    teamID,
		APIAppID:  "TEST123456",
		Event:     innerEvent,
		Type:      "event_callback",
		EventID:   "Ev" + fmt.Sprintf("%010d", time.Now().Unix()),
		EventTime: time.Now().Unix(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic("failed to marshal test payload: " + err.Error())
	}
	return data
}

// CreateSlackSignature creates a valid Slack request signature.
func (HTTPTestHelpers) CreateSlackSignature(payload []byte, secret string, timestamp string) string {
	if timestamp == "" {
		timestamp = strconv.FormatInt(time.Now().Unix(), 10)
	}

	baseString := "v0:" + timestamp + ":" + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(baseString))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// CreateSlackHTTPRequest creates an HTTP request for Slack Events API.
func (h HTTPTestHelpers) CreateSlackHTTPRequest(payload []byte, signingSecret string) *http.Request {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := h.CreateSlackSignature(payload, signingSecret, timestamp)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/events", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("User-Agent", "Slackbot 1.0 (+https://api.slack.com/robots)")

	return req
}

// CreateSlackMessageEventRequest creates a complete HTTP request for a Slack message event.
func (h HTTPTestHelpers) CreateSlackMessageEventRequest(text, channel, userID, timestamp, teamID, signingSecret string) *http.Request {
	messageEvent := &slackevents.MessageEvent{
		Type:           "message",
		Channel:        channel,
		User:           userID,
		Text:           text,
		TimeStamp:      timestamp,
		EventTimeStamp: fmt.Sprintf("%d.%03d", time.Now().Unix(), testEventTimestampSuffix),
	}

	payload := h.CreateSlackEventAPIPayload("event_callback", messageEvent, teamID)
	return h.CreateSlackHTTPRequest(payload, signingSecret)
}

// CreateInvalidSlackSignatureRequest creates a Slack request with invalid signature for security testing.
func (h HTTPTestHelpers) CreateInvalidSlackSignatureRequest(payload []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack/events", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Signature", "v0=invalid_signature_here")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("User-Agent", "Slackbot 1.0 (+https://api.slack.com/robots)")

	return req
}

// CreateGitHubSignature creates a valid GitHub webhook signature.
func (HTTPTestHelpers) CreateGitHubSignature(payload []byte, secret string) string {
	if secret == "" {
		return ""
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// CreateGitHubHTTPRequest creates an HTTP request for GitHub webhooks.
func (h HTTPTestHelpers) CreateGitHubHTTPRequest(payload []byte, eventType, webhookSecret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", eventType)
	req.Header.Set("X-Github-Delivery", "test-delivery-"+fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("User-Agent", "GitHub-Hookshot/test")

	if webhookSecret != "" {
		signature := h.CreateGitHubSignature(payload, webhookSecret)
		req.Header.Set("X-Hub-Signature-256", signature)
	}

	return req
}

// CreateGitHubPRWebhookRequest creates a complete GitHub PR webhook request.
func (h HTTPTestHelpers) CreateGitHubPRWebhookRequest(
	repoFullName string, prNumber int, action, title, body, author, webhookSecret string,
) *http.Request {
	fixtures := TestGitHubWebhooks{}
	payload := fixtures.CreatePROpenedPayload(repoFullName, prNumber, title, body, author)

	// Modify action if not "opened"
	if action != "opened" {
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(payload, &payloadMap); err != nil {
			panic("failed to unmarshal test payload: " + err.Error())
		}
		payloadMap["action"] = action
		var err error
		payload, err = json.Marshal(payloadMap)
		if err != nil {
			panic("failed to marshal test payload: " + err.Error())
		}
	}

	return h.CreateGitHubHTTPRequest(payload, "pull_request", webhookSecret)
}

// CreateGitHubReviewWebhookRequest creates a GitHub PR review webhook request.
func (h HTTPTestHelpers) CreateGitHubReviewWebhookRequest(
	repoFullName string, prNumber int, reviewState, reviewer, webhookSecret string,
) *http.Request {
	fixtures := TestGitHubWebhooks{}
	payload := fixtures.CreatePRReviewPayload(repoFullName, prNumber, reviewState, reviewer)

	return h.CreateGitHubHTTPRequest(payload, "pull_request_review", webhookSecret)
}

// ProcessHTTPRequest processes an HTTP request through a Gin handler and returns the response.
func (HTTPTestHelpers) ProcessHTTPRequest(req *http.Request, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler(c)

	return w
}

// CreateGinContext creates a Gin context for testing.
func (HTTPTestHelpers) CreateGinContext(req *http.Request) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	return c, w
}
