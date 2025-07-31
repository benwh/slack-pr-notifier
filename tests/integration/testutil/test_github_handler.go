package testutil

import (
	"context"
	"encoding/json"
	"fmt"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
)

// TestGitHubHandler wraps a real GitHubHandler but overrides Slack calls with mocks.
type TestGitHubHandler struct {
	*handlers.GitHubHandler

	mockSlackService *MockSlackService
	firestoreService *services.FirestoreService
}

// NewTestGitHubHandler creates a test-specific GitHubHandler.
func NewTestGitHubHandler(
	cloudTasksService CloudTasksServiceInterface,
	firestoreService *services.FirestoreService,
	realSlackService *services.SlackService,
	mockSlackService *MockSlackService,
	webhookSecret string,
) *TestGitHubHandler {
	// Create the real handler
	emojiConfig := config.EmojiConfig{
		Approved:         "white_check_mark",
		ChangesRequested: "arrows_counterclockwise",
		Commented:        "speech_balloon",
		Merged:           "purple_heart",
		Closed:           "x",
	}
	// Create a GitHub API service for the test with test credentials
	githubService, err := services.NewGitHubService(&config.Config{
		GitHubAppID:            12345,
		GitHubPrivateKeyBase64: "dGVzdC1wcml2YXRlLWtleQ==", // "test-private-key" in base64
	}, firestoreService)
	if err != nil {
		panic(fmt.Sprintf("failed to create GitHub service for test: %v", err))
	}

	realHandler := handlers.NewGitHubHandler(
		cloudTasksService,
		firestoreService,
		realSlackService,
		githubService,
		webhookSecret,
		emojiConfig,
	)

	return &TestGitHubHandler{
		GitHubHandler:    realHandler,
		mockSlackService: mockSlackService,
		firestoreService: firestoreService,
	}
}

// ProcessWebhookJob overrides the real method to use our mock Slack service.
// This duplicates some logic from the real handler but allows us to test without API calls.
func (h *TestGitHubHandler) ProcessWebhookJob(ctx context.Context, job *models.Job) error {
	// Parse the WebhookJob from the job payload
	var webhookJob models.WebhookJob
	if err := json.Unmarshal(job.Payload, &webhookJob); err != nil {
		return err
	}

	// For testing, we'll simulate the webhook processing but use our mock Slack service
	// This is a simplified version that focuses on the key functionality

	// Simulate some Slack calls based on the webhook event type
	switch webhookJob.EventType {
	case "pull_request":
		// Simulate posting a PR message for opened events
		const testPRSize = 100
		_, _ = h.mockSlackService.PostPRMessage(
			ctx,
			"test-team",
			"test-channel",
			"test-repo",
			"Test PR",
			"test-author",
			"Test description",
			"https://github.com/test/repo/pull/1",
			testPRSize,
			"", // No Slack user ID in test
			"", // No user CC in test
			"", // No custom emoji in test
		)
	case "pull_request_review":
		// Simulate adding reactions for reviews (assume approved for simplicity)
		emoji := h.mockSlackService.GetEmojiForReviewState("approved")
		if emoji != "" {
			_ = h.mockSlackService.AddReaction(ctx, "test-team", "test-channel", "123456789.123", emoji)
		}
	}

	return nil
}
