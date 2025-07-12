package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github-slack-notifier/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSlackHandler_verifySignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		signingSecret  string
		body           string
		timestamp      string
		signature      string
		maxAge         time.Duration
		expectedResult bool
	}{
		{
			name:           "Valid signature",
			signingSecret:  "test-secret",
			body:           "command=/notify-channel&user_id=U123&text=test",
			timestamp:      strconv.FormatInt(time.Now().Unix(), 10),
			signature:      "", // Will be calculated
			maxAge:         5 * time.Minute,
			expectedResult: true,
		},
		{
			name:           "Invalid signature",
			signingSecret:  "test-secret",
			body:           "command=/notify-channel&user_id=U123&text=test",
			timestamp:      strconv.FormatInt(time.Now().Unix(), 10),
			signature:      "v0=invalid-signature",
			maxAge:         5 * time.Minute,
			expectedResult: false,
		},
		{
			name:           "Timestamp too old",
			signingSecret:  "test-secret",
			body:           "command=/notify-channel&user_id=U123&text=test",
			timestamp:      strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10),
			signature:      "", // Will be calculated
			maxAge:         5 * time.Minute,
			expectedResult: false,
		},
		{
			name:           "Invalid timestamp format",
			signingSecret:  "test-secret",
			body:           "command=/notify-channel&user_id=U123&text=test",
			timestamp:      "invalid-timestamp",
			signature:      "v0=some-signature",
			maxAge:         5 * time.Minute,
			expectedResult: false,
		},
		{
			name:           "Empty signing secret allows any signature",
			signingSecret:  "",
			body:           "command=/notify-channel&user_id=U123&text=test",
			timestamp:      strconv.FormatInt(time.Now().Unix(), 10),
			signature:      "v0=any-signature",
			maxAge:         5 * time.Minute,
			expectedResult: true,
		},
		{
			name:           "Empty body",
			signingSecret:  "test-secret",
			body:           "",
			timestamp:      strconv.FormatInt(time.Now().Unix(), 10),
			signature:      "", // Will be calculated
			maxAge:         5 * time.Minute,
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler
			cfg := &config.Config{
				SlackSigningSecret:   tt.signingSecret,
				SlackTimestampMaxAge: tt.maxAge,
			}
			handler := NewSlackHandler(nil, nil, cfg)

			// Calculate correct signature if not provided
			if tt.signature == "" && tt.signingSecret != "" {
				basestring := fmt.Sprintf("v0:%s:%s", tt.timestamp, tt.body)
				mac := hmac.New(sha256.New, []byte(tt.signingSecret))
				mac.Write([]byte(basestring))
				tt.signature = "v0=" + hex.EncodeToString(mac.Sum(nil))
			}

			// Test signature verification
			result := handler.verifySignature(tt.signature, tt.timestamp, []byte(tt.body))
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSlackHandler_HandleWebhook_Security(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		body           string
		headers        map[string]string
		signingSecret  string
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Missing signature header",
			body: "command=/notify-channel&user_id=U123&text=test",
			headers: map[string]string{
				"X-Slack-Request-Timestamp": strconv.FormatInt(time.Now().Unix(), 10),
			},
			signingSecret:  "test-secret",
			expectedStatus: 401,
			expectedError:  "Missing signature or timestamp",
		},
		{
			name: "Missing timestamp header",
			body: "command=/notify-channel&user_id=U123&text=test",
			headers: map[string]string{
				"X-Slack-Signature": "v0=some-signature",
			},
			signingSecret:  "test-secret",
			expectedStatus: 401,
			expectedError:  "Missing signature or timestamp",
		},
		{
			name: "Invalid signature",
			body: "command=/notify-channel&user_id=U123&text=test",
			headers: map[string]string{
				"X-Slack-Signature":         "v0=invalid-signature",
				"X-Slack-Request-Timestamp": strconv.FormatInt(time.Now().Unix(), 10),
			},
			signingSecret:  "test-secret",
			expectedStatus: 401,
			expectedError:  "Invalid signature",
		},
		{
			name: "Timestamp too old",
			body: "command=/notify-channel&user_id=U123&text=test",
			headers: map[string]string{
				"X-Slack-Signature":         "", // Will be calculated
				"X-Slack-Request-Timestamp": strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10),
			},
			signingSecret:  "test-secret",
			expectedStatus: 401,
			expectedError:  "Invalid signature",
		},
		{
			name: "Invalid form data",
			body: "invalid%form%data",
			headers: map[string]string{
				"X-Slack-Signature":         "", // Will be calculated
				"X-Slack-Request-Timestamp": strconv.FormatInt(time.Now().Unix(), 10),
			},
			signingSecret:  "test-secret",
			expectedStatus: 400,
			expectedError:  "Failed to parse form data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler
			cfg := &config.Config{
				SlackSigningSecret:   tt.signingSecret,
				SlackTimestampMaxAge: 5 * time.Minute,
			}
			handler := NewSlackHandler(nil, nil, cfg)

			// Calculate correct signature if needed
			if signature, exists := tt.headers["X-Slack-Signature"]; exists && signature == "" {
				timestamp := tt.headers["X-Slack-Request-Timestamp"]
				basestring := fmt.Sprintf("v0:%s:%s", timestamp, tt.body)
				mac := hmac.New(sha256.New, []byte(tt.signingSecret))
				mac.Write([]byte(basestring))
				tt.headers["X-Slack-Signature"] = "v0=" + hex.EncodeToString(mac.Sum(nil))
			}

			// Create request
			req, _ := http.NewRequest(http.MethodPost, "/slack", bytes.NewBufferString(tt.body))
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			// Create response recorder and context
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

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

func TestSlackHandler_TimestampValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		timestamp string
		maxAge    time.Duration
		valid     bool
	}{
		{
			name:      "Current timestamp",
			timestamp: strconv.FormatInt(time.Now().Unix(), 10),
			maxAge:    5 * time.Minute,
			valid:     true,
		},
		{
			name:      "Old timestamp within limit",
			timestamp: strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10),
			maxAge:    5 * time.Minute,
			valid:     true,
		},
		{
			name:      "Old timestamp beyond limit",
			timestamp: strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10),
			maxAge:    5 * time.Minute,
			valid:     false,
		},
		{
			name:      "Future timestamp",
			timestamp: strconv.FormatInt(time.Now().Add(1*time.Minute).Unix(), 10),
			maxAge:    5 * time.Minute,
			valid:     true,
		},
		{
			name:      "Invalid timestamp format",
			timestamp: "not-a-number",
			maxAge:    5 * time.Minute,
			valid:     false,
		},
		{
			name:      "Negative timestamp",
			timestamp: "-1",
			maxAge:    5 * time.Minute,
			valid:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SlackSigningSecret:   "test-secret",
				SlackTimestampMaxAge: tt.maxAge,
			}
			handler := NewSlackHandler(nil, nil, cfg)

			body := "command=/notify-channel&user_id=U123&text=test"

			// Calculate signature based on timestamp
			basestring := fmt.Sprintf("v0:%s:%s", tt.timestamp, body)
			mac := hmac.New(sha256.New, []byte("test-secret"))
			mac.Write([]byte(basestring))
			signature := "v0=" + hex.EncodeToString(mac.Sum(nil))

			result := handler.verifySignature(signature, tt.timestamp, []byte(body))
			assert.Equal(t, tt.valid, result)
		})
	}
}
