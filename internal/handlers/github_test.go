package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github-slack-notifier/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCloudTasksService is a mock implementation for testing.
type mockCloudTasksService struct{}

func (m *mockCloudTasksService) EnqueueWebhook(ctx context.Context, job *models.WebhookJob) error {
	return nil
}

// TestGitHubHandler_HandleWebhook_GitHubLibraryIntegration tests our integration with the go-github library.
// We focus on testing that our code correctly passes requests to the library and handles responses,
// rather than re-testing the library's internal validation logic.
func TestGitHubHandler_HandleWebhook_GitHubLibraryIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		webhookSecret  string
		body           string
		setupHeaders   func() http.Header
		expectedStatus int
		expectError    bool
	}{
		{
			name:          "Valid webhook with proper signature",
			webhookSecret: "test-secret",
			body:          `{"action":"opened","repository":{"name":"test"}}`,
			setupHeaders: func() http.Header {
				body := `{"action":"opened","repository":{"name":"test"}}`
				mac := hmac.New(sha256.New, []byte("test-secret"))
				mac.Write([]byte(body))
				signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

				header := http.Header{}
				header.Set("X-Hub-Signature-256", signature)
				header.Set("X-GitHub-Event", "pull_request")
				header.Set("X-GitHub-Delivery", "test-delivery-id")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 200,
			expectError:    false,
		},
		{
			name:          "Invalid signature",
			webhookSecret: "test-secret",
			body:          `{"action":"opened","repository":{"name":"test"}}`,
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Hub-Signature-256", "sha256=invalid-signature")
				header.Set("X-GitHub-Event", "pull_request")
				header.Set("X-GitHub-Delivery", "test-delivery-id")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 401,
			expectError:    true,
		},
		{
			name:          "Missing signature with webhook secret",
			webhookSecret: "test-secret",
			body:          `{"action":"opened","repository":{"name":"test"}}`,
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-GitHub-Event", "pull_request")
				header.Set("X-GitHub-Delivery", "test-delivery-id")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 401,
			expectError:    true,
		},
		{
			name:          "Empty webhook secret bypasses signature validation",
			webhookSecret: "",
			body:          `{"action":"opened","repository":{"name":"test"}}`,
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-GitHub-Event", "pull_request")
				header.Set("X-GitHub-Delivery", "test-delivery-id")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 200,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use mock service for success case, nil for error cases to test early validation
			var cloudTasksService CloudTasksServiceInterface
			if !tt.expectError {
				cloudTasksService = &mockCloudTasksService{}
			}
			handler := NewGitHubHandler(cloudTasksService, tt.webhookSecret)

			req, _ := http.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(tt.body))
			for key, values := range tt.setupHeaders() {
				for _, value := range values {
					req.Header.Set(key, value)
				}
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("trace_id", "test-trace-id")

			handler.HandleWebhook(c)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectError {
				assert.Contains(t, w.Body.String(), "error")
			}
		})
	}
}

// TestGitHubHandler_HandleWebhook_SecurityHeaders tests the HTTP-level header validation
// in the GitHub webhook handler. This ensures required headers (X-GitHub-Event, X-GitHub-Delivery)
// are present and properly validated before processing.
func TestGitHubHandler_HandleWebhook_SecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		setupHeaders   func() http.Header
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Missing X-GitHub-Event header",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-GitHub-Delivery", "test-delivery-id")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Missing X-GitHub-Delivery header",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-GitHub-Event", "pull_request")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Missing both required headers",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Empty header values",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-GitHub-Event", "")
				header.Set("X-GitHub-Delivery", "")
				header.Set("Content-Type", "application/json")
				return header
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewGitHubHandler(nil, "")

			body := `{"action":"opened","repository":{"name":"test"}}`
			req, _ := http.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
			for key, values := range tt.setupHeaders() {
				for _, value := range values {
					req.Header.Set(key, value)
				}
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("trace_id", "test-trace-id")

			handler.HandleWebhook(c)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedError)
		})
	}
}

// TestGitHubHandler_HandleWebhook_BodyReading tests the HTTP request body reading
// and error handling. This ensures that malformed requests are properly handled
// and return appropriate error responses.
func TestGitHubHandler_HandleWebhook_BodyReading(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewGitHubHandler(nil, "")

	// Create request with body that causes read error
	req, _ := http.NewRequest(http.MethodPost, "/webhooks/github", &errorReader{})
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-id")
	req.Header.Set("Content-Type", "application/json")

	// Create response recorder and context
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("trace_id", "test-trace-id")

	// Handle webhook
	handler.HandleWebhook(c)

	// Assert response
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "error")
}

// errorReader simulates an error when reading the request body.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// TestGitHubHandler_validateWebhookPayload tests the webhook payload validation logic
// for GitHub events. This verifies that we properly validate event types, JSON structure,
// and required fields in webhook payloads.
func TestGitHubHandler_validateWebhookPayload(t *testing.T) {
	handler := &GitHubHandler{}

	tests := []struct {
		name        string
		eventType   string
		payload     []byte
		expectedErr string
	}{
		{
			name:        "Valid pull_request event",
			eventType:   "pull_request",
			payload:     []byte(`{"action":"opened","repository":{"name":"test"}}`),
			expectedErr: "",
		},
		{
			name:        "Valid pull_request_review event",
			eventType:   "pull_request_review",
			payload:     []byte(`{"action":"submitted","repository":{"name":"test"}}`),
			expectedErr: "",
		},
		{
			name:        "Unsupported event type",
			eventType:   "push",
			payload:     []byte(`{"ref":"refs/heads/main"}`),
			expectedErr: "unsupported event type: push",
		},
		{
			name:        "Invalid JSON payload",
			eventType:   "pull_request",
			payload:     []byte(`{"invalid":"json"`),
			expectedErr: "invalid JSON payload",
		},
		{
			name:        "Missing action field",
			eventType:   "pull_request",
			payload:     []byte(`{"repository":{"name":"test"}}`),
			expectedErr: "missing required field: action",
		},
		{
			name:        "Missing repository field",
			eventType:   "pull_request",
			payload:     []byte(`{"action":"opened"}`),
			expectedErr: "missing required field: repository",
		},
		{
			name:        "Empty payload",
			eventType:   "pull_request",
			payload:     []byte(`{}`),
			expectedErr: "missing required field: action",
		},
		{
			name:        "Null action field",
			eventType:   "pull_request",
			payload:     []byte(`{"action":null,"repository":{"name":"test"}}`),
			expectedErr: "",
		},
		{
			name:        "Null repository field",
			eventType:   "pull_request",
			payload:     []byte(`{"action":"opened","repository":null}`),
			expectedErr: "",
		},
		{
			name:        "Very large payload",
			eventType:   "pull_request",
			payload:     []byte(`{"action":"opened","repository":{"name":"` + strings.Repeat("a", 1000) + `"}}`),
			expectedErr: "",
		},
		{
			name:        "Empty event type",
			eventType:   "",
			payload:     []byte(`{"action":"opened","repository":{"name":"test"}}`),
			expectedErr: "unsupported event type: ",
		},
		{
			name:        "Whitespace in event type",
			eventType:   " pull_request ",
			payload:     []byte(`{"action":"opened","repository":{"name":"test"}}`),
			expectedErr: "unsupported event type:  pull_request ",
		},
		{
			name:        "Case sensitive event type",
			eventType:   "Pull_Request",
			payload:     []byte(`{"action":"opened","repository":{"name":"test"}}`),
			expectedErr: "unsupported event type: Pull_Request",
		},
		{
			name:        "Nil payload",
			eventType:   "pull_request",
			payload:     nil,
			expectedErr: "invalid JSON payload",
		},
		{
			name:        "Empty payload bytes",
			eventType:   "pull_request",
			payload:     []byte{},
			expectedErr: "invalid JSON payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateWebhookPayload(tt.eventType, tt.payload)

			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}
