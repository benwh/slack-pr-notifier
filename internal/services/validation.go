package services

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrUnsupportedEventType = errors.New("unsupported event type")
	ErrMissingAction        = errors.New("missing required field: action")
	ErrMissingRepository    = errors.New("missing required field: repository")
)

type ValidationService struct{}

func NewValidationService() *ValidationService {
	return &ValidationService{}
}

func (vs *ValidationService) ValidateWebhookPayload(eventType string, payload []byte) error {
	switch eventType {
	case "pull_request", "pull_request_review":
		return vs.validateGitHubPayload(payload)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedEventType, eventType)
	}
}

func (vs *ValidationService) validateGitHubPayload(payload []byte) error {
	var githubPayload map[string]interface{}
	if err := json.Unmarshal(payload, &githubPayload); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}

	if _, exists := githubPayload["action"]; !exists {
		return ErrMissingAction
	}

	if _, exists := githubPayload["repository"]; !exists {
		return ErrMissingRepository
	}

	return nil
}
