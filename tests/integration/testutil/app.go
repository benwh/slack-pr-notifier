package testutil

import (
	"context"
	"testing"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	firestoreTesting "github-slack-notifier/internal/testing"
)

// TestApp represents a test application instance with all services and handlers.
type TestApp struct {
	Config                *config.Config
	FirestoreService      *services.FirestoreService
	SlackService          *services.SlackService // Real service (but with mock token)
	MockSlackService      *MockSlackService      // Mock service for testing assertions
	CloudTasksService     *MockCloudTasksService // Mock service for testing
	GitHubAuthService     *services.GitHubAuthService
	MockGitHubAuthService *MockGitHubAuthService // For specialized tests
	GitHubHandler         *TestGitHubHandler     // Test-specific GitHubHandler that uses mock
	SlackHandler          *TestSlackHandler      // Test-specific SlackHandler that uses mock
	jobProcessor          *handlers.JobProcessor // Real job processor (private - for routing only)
	emulator              *firestoreTesting.FirestoreEmulator
}

// SetupTestApp creates a test application instance with mocked external services.
func SetupTestApp(t *testing.T) (*TestApp, context.Context, func()) {
	t.Helper()

	// Setup Firestore emulator
	emulator, ctx := firestoreTesting.SetupFirestoreEmulator(t)

	// Create test configuration
	cfg := &config.Config{
		FirestoreProjectID:  "test-project",
		FirestoreDatabaseID: "(default)",
		SlackSigningSecret:  "test-signing-secret",
		SlackClientID:       "test_client_id",
		SlackClientSecret:   "test_client_secret",
		GitHubWebhookSecret: "test-webhook-secret",
		GitHubClientID:      "test-github-client-id",
		GitHubClientSecret:  "test-github-client-secret",
		GoogleCloudProject:  "test-project",
		BaseURL:             "http://localhost:8080",
		GCPRegion:           "us-central1",
		CloudTasksQueue:     "test-queue",
		CloudTasksSecret:    "test-cloud-tasks-secret",
		Emoji: config.EmojiConfig{
			Approved:         "white_check_mark",
			ChangesRequested: "arrows_counterclockwise",
			Commented:        "speech_balloon",
			Merged:           "purple_heart",
			Closed:           "x",
		},
	}

	// Create services
	firestoreService := services.NewFirestoreService(emulator.Client)

	// Real Slack service - will fail API calls without valid workspace tokens
	slackWorkspaceService := services.NewSlackWorkspaceService(emulator.Client)
	realSlackService := services.NewSlackService(slackWorkspaceService, cfg.Emoji, cfg)

	// Mock Slack service for testing assertions
	mockSlackService := NewMockSlackService()

	// Mock Cloud Tasks service that processes jobs in-memory
	mockCloudTasksService := NewMockCloudTasksService()

	// Mock GitHub Auth service
	mockGitHubAuthService := NewMockGitHubAuthService()

	// For integration tests, we'll use mock Cloud Tasks service
	// to capture jobs and process them in-memory for testing
	realGitHubAuthService := services.NewGitHubAuthService(cfg, firestoreService)

	// Create handlers with services for testing
	githubHandler := NewTestGitHubHandler(
		mockCloudTasksService, // ← Use mock for testing (implements interface)
		firestoreService,
		realSlackService, // Real service for non-API methods
		mockSlackService, // Mock service for API call recording
		cfg.GitHubWebhookSecret,
	)

	// Create a custom SlackHandler for testing that uses our mock
	slackHandler := NewTestSlackHandler(
		firestoreService,
		realSlackService,      // Use real Slack service (will fail API calls)
		mockCloudTasksService, // Use our mock directly
		realGitHubAuthService,
		cfg,
	)

	// Create a JobProcessor with the embedded real handlers for routing
	// Note: We can't easily create a real SlackHandler with our mock CloudTasksService,
	// so we'll create a minimal one just for the routing logic
	jobProcessor := handlers.NewJobProcessor(
		githubHandler.GitHubHandler, // Embedded real handler
		nil,                         // SlackHandler can be nil - we override in processJob
		cfg,
	)

	app := &TestApp{
		Config:                cfg,
		FirestoreService:      firestoreService,
		SlackService:          realSlackService,      // Real service (will fail API calls)
		MockSlackService:      mockSlackService,      // Mock service for testing assertions
		CloudTasksService:     mockCloudTasksService, // Mock service for testing
		GitHubAuthService:     realGitHubAuthService,
		MockGitHubAuthService: mockGitHubAuthService,
		GitHubHandler:         githubHandler,
		SlackHandler:          slackHandler,
		jobProcessor:          jobProcessor,
		emulator:              emulator,
	}

	cleanup := func() {
		emulator.Cleanup()
	}

	return app, ctx, cleanup
}

// ClearData clears all data from the test database.
func (app *TestApp) ClearData(ctx context.Context) error {
	return app.emulator.ClearData(ctx)
}

// ProcessQueuedJobs processes all jobs that have been queued in the mock Cloud Tasks service.
func (app *TestApp) ProcessQueuedJobs(ctx context.Context) (int, []error) {
	jobs := app.CloudTasksService.GetQueuedJobs()
	var errs []error

	for _, job := range jobs {
		if err := app.processJob(ctx, job); err != nil {
			errs = append(errs, err)
		}
	}

	app.CloudTasksService.ClearQueuedJobs()
	return len(jobs), errs
}

// processJob processes a single job using the real JobProcessor's routing logic,
// but we intercept and route to our test handlers instead.
func (app *TestApp) processJob(ctx context.Context, job *models.Job) error {
	// We can't use app.jobProcessor.RouteJob directly because it would route to the
	// real handlers. Instead, we use the same routing logic but with our test handlers.
	switch job.Type {
	case models.JobTypeGitHubWebhook:
		return app.GitHubHandler.ProcessWebhookJob(ctx, job)
	case models.JobTypeManualPRLink:
		return app.SlackHandler.ProcessManualPRLinkJob(ctx, job)
	default:
		return models.ErrUnsupportedJobType
	}
}
