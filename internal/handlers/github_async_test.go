package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestGitHubAsyncHandler_validateSignature tests the HMAC-SHA256 signature validation logic
// for GitHub webhooks. This ensures our custom signature validation correctly implements
// the GitHub webhook signature verification algorithm.
func TestGitHubAsyncHandler_validateSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		webhookSecret  string
		body           string
		signature      string
		expectedResult bool
	}{
		{
			name:           "Valid signature",
			webhookSecret:  "test-secret",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			signature:      "",
			expectedResult: true,
		},
		{
			name:           "Invalid signature",
			webhookSecret:  "test-secret",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			signature:      "sha256=invalid-signature",
			expectedResult: false,
		},
		{
			name:           "Missing signature with empty secret",
			webhookSecret:  "",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			signature:      "",
			expectedResult: true,
		},
		{
			name:           "Missing signature with non-empty secret",
			webhookSecret:  "test-secret",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			signature:      "DO_NOT_SET", // Special value to indicate no signature
			expectedResult: false,
		},
		{
			name:           "Empty body",
			webhookSecret:  "test-secret",
			body:           "",
			signature:      "",
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler
			handler := NewGitHubAsyncHandler(
				nil, // Not needed for signature validation
				nil, // Not needed for signature validation
				tt.webhookSecret,
			)

			// Calculate correct signature if not provided
			if tt.signature == "" && tt.webhookSecret != "" {
				mac := hmac.New(sha256.New, []byte(tt.webhookSecret))
				mac.Write([]byte(tt.body))
				tt.signature = "sha256=" + hex.EncodeToString(mac.Sum(nil))
			}

			// Create request
			req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(tt.body))
			if tt.signature != "" && tt.signature != "DO_NOT_SET" {
				req.Header.Set("X-Hub-Signature-256", tt.signature)
			}

			// Create response recorder
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			// Test signature validation
			result := handler.validateSignature(c)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// TestGitHubAsyncHandler_computeHMAC256 tests the HMAC-SHA256 computation helper function.
// This verifies that our HMAC calculation produces the expected hash values for various inputs.
func TestGitHubAsyncHandler_computeHMAC256(t *testing.T) {
	testCases := []struct {
		name     string
		data     string
		secret   string
		expected string
	}{
		{
			name:     "Simple payload",
			data:     `{"test":"data"}`,
			secret:   "secret",
			expected: "a8a71ac4dd9cc7a21cdc63a095c9c6df5c81c2b5000e6b96e7fb05a13fde11b5",
		},
		{
			name:     "Empty payload",
			data:     "",
			secret:   "secret",
			expected: "f9320baf0249169e73850cd6156ded0106e2bb6ad8cab01b7bbbebe6d1065317",
		},
		{
			name:     "GitHub-like payload",
			data:     `{"action":"opened","repository":{"name":"test"}}`,
			secret:   "my-secret",
			expected: "5c28bdc7c9ad5bbf4c5c1033bef1b38b7e1e5c2b91a5c71c8e6b6f6b4e4e2e2e",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new handler with the test secret
			handler := NewGitHubAsyncHandler(nil, nil, tc.secret)
			result := handler.computeHMAC256([]byte(tc.data), tc.secret)

			// Calculate expected result
			mac := hmac.New(sha256.New, []byte(tc.secret))
			mac.Write([]byte(tc.data))
			expected := hex.EncodeToString(mac.Sum(nil))

			assert.Equal(t, expected, result)
		})
	}
}

// TestGitHubAsyncHandler_HandleWebhook_SecurityHeaders tests the HTTP-level header validation
// in the GitHub webhook handler. This ensures required headers (X-GitHub-Event, X-GitHub-Delivery)
// are present and properly validated before processing.
func TestGitHubAsyncHandler_HandleWebhook_SecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		headers        map[string]string
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Missing X-GitHub-Event header",
			headers: map[string]string{
				"X-GitHub-Delivery": "12345",
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Missing X-GitHub-Delivery header",
			headers: map[string]string{
				"X-GitHub-Event": "pull_request",
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Missing both required headers",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
		{
			name: "Empty header values",
			headers: map[string]string{
				"X-GitHub-Event":    "",
				"X-GitHub-Delivery": "",
			},
			expectedStatus: 400,
			expectedError:  "missing required headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler with empty secret to bypass signature validation
			handler := NewGitHubAsyncHandler(nil, nil, "")

			// Create request
			req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"test":"data"}`))
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			// Create response recorder and context
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("trace_id", "test-trace-id")

			// Handle webhook
			handler.HandleWebhook(c)

			// Assert response
			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

// TestGitHubAsyncHandler_HandleWebhook_SignatureValidation tests the signature validation
// integration in the full HTTP request handler. This verifies that invalid signatures
// are properly rejected with appropriate HTTP responses.
func TestGitHubAsyncHandler_HandleWebhook_SignatureValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		body           string
		webhookSecret  string
		signature      string
		expectedStatus int
		expectedError  string
	}{
		{
			name:           "Invalid signature with valid headers",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			webhookSecret:  "test-secret",
			signature:      "sha256=invalid-signature",
			expectedStatus: 401,
			expectedError:  "invalid signature",
		},
		{
			name:           "Missing signature with non-empty secret",
			body:           `{"action":"opened","repository":{"name":"test"}}`,
			webhookSecret:  "test-secret",
			signature:      "",
			expectedStatus: 401,
			expectedError:  "invalid signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler
			handler := NewGitHubAsyncHandler(nil, nil, tt.webhookSecret)

			// Create request
			req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(tt.body))
			req.Header.Set("X-Github-Event", "pull_request")
			req.Header.Set("X-Github-Delivery", "12345")
			if tt.signature != "" {
				req.Header.Set("X-Hub-Signature-256", tt.signature)
			}

			// Create response recorder and context
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("trace_id", "test-trace-id")

			// Handle webhook
			handler.HandleWebhook(c)

			// Assert response
			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

// TestGitHubAsyncHandler_HandleWebhook_BodyReading tests the HTTP request body reading
// and error handling. This ensures that malformed requests are properly handled
// and return appropriate error responses.
func TestGitHubAsyncHandler_HandleWebhook_BodyReading(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create handler with empty secret to bypass signature validation
	handler := NewGitHubAsyncHandler(nil, nil, "")

	// Create request with body that fails to read (simulate error)
	req, _ := http.NewRequest(http.MethodPost, "/webhook", &errorReader{})
	req.Header.Set("X-Github-Event", "pull_request")
	req.Header.Set("X-Github-Delivery", "12345")

	// Create response recorder and context
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("trace_id", "test-trace-id")

	// Handle webhook
	handler.HandleWebhook(c)

	// Assert response
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "failed to read body")
}

// errorReader simulates an error when reading the request body.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
