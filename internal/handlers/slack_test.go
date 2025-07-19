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

const (
	testSlackBody  = "command=/notify-channel&user_id=U123&text=test"
	testSigningKey = "test-secret"
)

// TestSlackHandler_verifySignature tests our integration with the slack-go library's signature verification.
// We focus on testing that our code correctly passes headers and handles the library's responses,
// rather than re-testing the library's internal signature validation logic.
func TestSlackHandler_verifySignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		signingSecret string
		body          string
		setupHeaders  func() http.Header
		expectError   bool
	}{
		{
			name:          "Valid signature with proper headers",
			signingSecret: testSigningKey,
			body:          testSlackBody,
			setupHeaders: func() http.Header {
				timestamp := strconv.FormatInt(time.Now().Unix(), 10)
				basestring := fmt.Sprintf("v0:%s:%s", timestamp, testSlackBody)
				mac := hmac.New(sha256.New, []byte(testSigningKey))
				mac.Write([]byte(basestring))
				signature := "v0=" + hex.EncodeToString(mac.Sum(nil))

				header := http.Header{}
				header.Set("X-Slack-Signature", signature)
				header.Set("X-Slack-Request-Timestamp", timestamp)
				return header
			},
			expectError: false,
		},
		{
			name:          "Invalid signature fails validation",
			signingSecret: testSigningKey,
			body:          testSlackBody,
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Slack-Signature", "v0=invalid-signature")
				header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
				return header
			},
			expectError: true,
		},
		{
			name:          "Missing headers fail validation",
			signingSecret: testSigningKey,
			body:          testSlackBody,
			setupHeaders: func() http.Header {
				return http.Header{} // Empty headers
			},
			expectError: true,
		},
		{
			name:          "Empty signing secret bypasses validation",
			signingSecret: "",
			body:          testSlackBody,
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Slack-Signature", "v0=any-signature")
				header.Set("X-Slack-Request-Timestamp", "invalid-timestamp")
				return header
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SlackSigningSecret: tt.signingSecret,
			}
			handler := NewSlackHandler(nil, nil, nil, cfg)

			err := handler.verifySignature(tt.setupHeaders(), []byte(tt.body))
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSlackHandler_HandleSlashCommand_Security tests the HTTP-level security validation in HandleSlashCommand.
// This focuses on the gin request handling and header extraction, ensuring proper HTTP responses
// for security failures. The actual signature validation logic is tested in TestSlackHandler_verifySignature.
func TestSlackHandler_HandleSlashCommand_Security(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		setupHeaders   func() http.Header
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Missing signature header returns 401",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
				return header
			},
			expectedStatus: 401,
			expectedError:  "Missing signature or timestamp",
		},
		{
			name: "Missing timestamp header returns 401",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Slack-Signature", "v0=some-signature")
				return header
			},
			expectedStatus: 401,
			expectedError:  "Missing signature or timestamp",
		},
		{
			name: "Invalid signature returns 401",
			setupHeaders: func() http.Header {
				header := http.Header{}
				header.Set("X-Slack-Signature", "v0=invalid-signature")
				header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
				return header
			},
			expectedStatus: 401,
			expectedError:  "Invalid signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				SlackSigningSecret: testSigningKey,
			}
			handler := NewSlackHandler(nil, nil, nil, cfg)

			req, _ := http.NewRequest(http.MethodPost, "/slack", bytes.NewBufferString(testSlackBody))
			for key, values := range tt.setupHeaders() {
				for _, value := range values {
					req.Header.Set(key, value)
				}
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			handler.HandleSlashCommand(c)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedError)
		})
	}
}
