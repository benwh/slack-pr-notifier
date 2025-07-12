package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidationService_ValidateWebhookPayload tests the webhook payload validation logic
// for GitHub events. This verifies that we properly validate event types, JSON structure,
// and required fields in webhook payloads.
func TestValidationService_ValidateWebhookPayload(t *testing.T) {
	vs := NewValidationService()

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vs.ValidateWebhookPayload(tt.eventType, tt.payload)

			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}

// TestValidationService_validateGitHubPayload tests the internal GitHub payload validation
// logic. This focuses on JSON parsing, required field validation, and proper error handling
// for malformed or incomplete payloads.
func TestValidationService_validateGitHubPayload(t *testing.T) {
	vs := NewValidationService()

	tests := []struct {
		name        string
		payload     []byte
		expectedErr error
	}{
		{
			name:        "Valid payload with both fields",
			payload:     []byte(`{"action":"opened","repository":{"name":"test","full_name":"user/test"}}`),
			expectedErr: nil,
		},
		{
			name:        "Valid payload with minimal fields",
			payload:     []byte(`{"action":"opened","repository":{}}`),
			expectedErr: nil,
		},
		{
			name:        "Invalid JSON",
			payload:     []byte(`{"action":"opened","repository"`),
			expectedErr: nil, // Should contain "invalid JSON payload"
		},
		{
			name:        "Missing action",
			payload:     []byte(`{"repository":{"name":"test"}}`),
			expectedErr: ErrMissingAction,
		},
		{
			name:        "Missing repository",
			payload:     []byte(`{"action":"opened"}`),
			expectedErr: ErrMissingRepository,
		},
		{
			name:        "Empty JSON object",
			payload:     []byte(`{}`),
			expectedErr: ErrMissingAction,
		},
		{
			name:        "Action is empty string",
			payload:     []byte(`{"action":"","repository":{"name":"test"}}`),
			expectedErr: nil,
		},
		{
			name:        "Repository is empty object",
			payload:     []byte(`{"action":"opened","repository":{}}`),
			expectedErr: nil,
		},
		{
			name: "Complex valid payload",
			payload: []byte(`{"action":"opened","number":123,"pull_request":{"id":456},` +
				`"repository":{"name":"test","owner":{"login":"user"}}}`),
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vs.validateGitHubPayload(tt.payload)

			if tt.expectedErr == nil {
				if tt.name == "Invalid JSON" {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "invalid JSON payload")
				} else {
					require.NoError(t, err)
				}
			} else {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			}
		})
	}
}

// TestValidationService_EdgeCases tests edge cases and boundary conditions
// for webhook payload validation. This includes large payloads, empty data,
// case sensitivity, and other unusual but valid input scenarios.
func TestValidationService_EdgeCases(t *testing.T) {
	vs := NewValidationService()

	tests := []struct {
		name        string
		eventType   string
		payload     []byte
		expectedErr string
	}{
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
			err := vs.ValidateWebhookPayload(tt.eventType, tt.payload)

			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}
