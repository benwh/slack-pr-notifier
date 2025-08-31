package e2e

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/middleware"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	firestoreTesting "github-slack-notifier/internal/testing"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
)

//go:embed test-private-key.pem
var testPrivateKeyPEM []byte

// TestHarness provides an end-to-end testing environment for the application.
type TestHarness struct {
	// Server details
	baseURL string

	// Configuration
	config *config.Config

	// Services (exposed for testing)
	SlackWorkspaceService *services.SlackWorkspaceService
	Router                *gin.Engine

	// Test database with isolation
	testDB *firestoreTesting.TestDatabase

	// Fake Cloud Tasks service
	fakeCloudTasks *FakeCloudTasksService

	// HTTP client with mocking enabled
	httpClient *http.Client

	// Context for shutdown
	cancel context.CancelFunc

	// Request capture for assertions
	slackRequestCapture *SlackRequestCapture
}

// NewTestHarness creates a new test harness that runs the real application.
func NewTestHarness(t *testing.T) *TestHarness {
	t.Helper()

	// Setup test context
	_, cancel := context.WithCancel(context.Background())

	// Use shared emulator instead of per-test emulator
	emulator, err := firestoreTesting.GetGlobalEmulator()
	require.NoError(t, err)

	testDB, err := firestoreTesting.NewTestDatabase(t)
	require.NoError(t, err)

	// Find an available port
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "Failed to get TCP address")
	port := tcpAddr.Port
	require.NoError(t, listener.Close())

	// Create test configuration
	cfg := &config.Config{
		Port:                   fmt.Sprintf("%d", port),
		GinMode:                "test",
		LogLevel:               "error", // Keep logs quiet during tests
		FirestoreProjectID:     emulator.ProjectID,
		FirestoreDatabaseID:    "(default)",
		SlackSigningSecret:     "test-signing-secret",
		SlackClientID:          "test_client_id",
		SlackClientSecret:      "test_client_secret",
		BaseURL:                fmt.Sprintf("http://localhost:%d", port),
		GitHubWebhookSecret:    "test-webhook-secret",
		GitHubClientID:         "test-github-client-id",
		GitHubClientSecret:     "test-github-client-secret",
		GitHubAppID:            12345,
		GitHubPrivateKeyBase64: loadTestPrivateKey(), // Load test private key from file
		GoogleCloudProject:     emulator.ProjectID,
		GCPRegion:              "us-central1",
		CloudTasksQueue:        "test-queue",
		CloudTasksSecret:       "test-cloud-tasks-secret",
		CloudTasksMaxAttempts:  3, // Allow retries in tests
		Emoji: config.EmojiConfig{
			Approved:         "white_check_mark",
			ChangesRequested: "arrows_counterclockwise",
			Commented:        "speech_balloon",
			Merged:           "purple_heart",
			Closed:           "x",
		},
		ServerReadTimeout:        30 * time.Second,
		ServerWriteTimeout:       30 * time.Second,
		ServerShutdownTimeout:    10 * time.Second,
		WebhookProcessingTimeout: 5 * time.Second, // Reasonable timeout for tests
	}

	// Create per-test HTTP client with isolated mocking
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Activate httpmock for this specific client (isolated per test)
	httpmock.ActivateNonDefault(httpClient)

	// Create fake Cloud Tasks service
	fakeCloudTasks := NewFakeCloudTasksService(
		fmt.Sprintf("http://127.0.0.1:%d", port),
		cfg.CloudTasksSecret,
	)

	// Create a channel to receive services from the application startup
	servicesChan := make(chan *appServices, 1)

	// Start the application in a goroutine
	go startApplication(context.Background(), cfg, testDB.Client(), httpClient, fakeCloudTasks, servicesChan)

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL)

	// Wait for services to be available
	services := <-servicesChan

	// Create per-test request capture for isolation
	slackCapture := NewSlackRequestCapture()

	harness := &TestHarness{
		baseURL:               baseURL,
		config:                cfg,
		SlackWorkspaceService: services.slackWorkspaceService,
		Router:                services.router,
		testDB:                testDB,
		fakeCloudTasks:        fakeCloudTasks,
		httpClient:            httpClient,
		cancel:                cancel,
		slackRequestCapture:   slackCapture,
	}

	// Register cleanup for this specific test
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = testDB.Cleanup(ctx)
		_ = testDB.Close()
		harness.Cleanup()
	})

	return harness
}

// appServices holds services that need to be exposed to tests.
type appServices struct {
	slackWorkspaceService *services.SlackWorkspaceService
	router                *gin.Engine
}

// startApplication starts the real application with injected dependencies.
func startApplication(
	ctx context.Context, cfg *config.Config, firestoreClient *firestore.Client,
	httpClient *http.Client, fakeCloudTasks *FakeCloudTasksService,
	servicesChan chan<- *appServices,
) {
	// Disable structured logging for tests
	gin.SetMode(gin.TestMode)
	gin.DefaultWriter = os.Stderr

	// Create services
	firestoreService := services.NewFirestoreService(firestoreClient)

	// Create Slack service with OAuth support
	slackWorkspaceService := services.NewSlackWorkspaceService(firestoreClient)
	slackService := services.NewSlackService(slackWorkspaceService, cfg.Emoji, cfg, httpClient)

	// Create GitHub API service with mocked transport
	githubService, err := services.NewGitHubServiceWithTransport(cfg, firestoreService, httpClient.Transport)
	if err != nil {
		panic(fmt.Sprintf("failed to create GitHub service: %v", err))
	}

	// Create handlers
	githubHandler := handlers.NewGitHubHandler(
		fakeCloudTasks,
		firestoreService,
		slackService,
		githubService,
		cfg.GitHubWebhookSecret,
		cfg.Emoji,
	)

	githubAuthService := services.NewGitHubAuthService(cfg, firestoreService)
	oauthHandler := handlers.NewOAuthHandler(githubAuthService, firestoreService, slackService, slackWorkspaceService, cfg, httpClient)

	slackHandler := handlers.NewSlackHandler(
		firestoreService, slackService, fakeCloudTasks, githubAuthService, cfg,
	)

	jobProcessor := handlers.NewJobProcessor(githubHandler, slackHandler, cfg)

	// Setup routes
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.LoggingMiddleware())

	// Configure routes
	router.POST("/webhooks/github", githubHandler.HandleWebhook)
	router.POST("/jobs/process", middleware.CloudTasksAuthMiddleware(cfg), jobProcessor.ProcessJob)
	router.GET("/auth/github/link", oauthHandler.HandleGitHubLink)
	router.GET("/auth/github/callback", oauthHandler.HandleGitHubCallback)

	// Configure Slack OAuth routes (if enabled)
	if cfg.IsSlackOAuthEnabled() {
		router.GET("/auth/slack/install", oauthHandler.HandleSlackInstall)
		router.GET("/auth/slack/callback", oauthHandler.HandleSlackOAuthCallback)
	}

	router.POST("/webhooks/slack/events", slackHandler.HandleEvent)
	router.POST("/webhooks/slack/interactions", slackHandler.HandleInteraction)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Send services to the test harness
	servicesChan <- &appServices{
		slackWorkspaceService: slackWorkspaceService,
		router:                router,
	}

	// Start server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(ctx, "Server failed", "error", err)
		}
	}()

	// Wait for context cancellation and then shutdown
	<-ctx.Done()

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error(ctx, "Server shutdown failed", "error", err)
	}
}

// waitForServer waits for the server to be ready.
func waitForServer(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 1 * time.Second}
	healthURL := baseURL + "/health"

	for range 30 {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("Server failed to start within timeout")
}

// Cleanup shuts down the test harness.
func (h *TestHarness) Cleanup() {
	// Deactivate httpmock for this specific client to avoid interference
	httpmock.DeactivateAndReset()

	// Cancel the server context
	h.cancel()

	// Give the server a moment to shut down gracefully
	time.Sleep(100 * time.Millisecond)

	// Note: TestDatabase cleanup is handled in the test cleanup function
}

// BaseURL returns the base URL of the test server.
func (h *TestHarness) BaseURL() string {
	return h.baseURL
}

// Config returns the test configuration.
func (h *TestHarness) Config() *config.Config {
	return h.config
}

// HTTPClient returns the HTTP client with mocking enabled.
func (h *TestHarness) HTTPClient() *http.Client {
	return h.httpClient
}

// FirestoreClient returns the firestore client for database operations in tests.
func (h *TestHarness) FirestoreClient() *firestore.Client {
	return h.testDB.Client()
}

// ClearFirestore clears all data from the test database.
func (h *TestHarness) ClearFirestore(ctx context.Context) error {
	return h.testDB.Cleanup(ctx)
}

// ResetForTest provides comprehensive cleanup between tests to ensure isolation.
func (h *TestHarness) ResetForTest(ctx context.Context) error {
	// Clear Firestore data
	if err := h.ClearFirestore(ctx); err != nil {
		return fmt.Errorf("failed to clear Firestore: %w", err)
	}

	// Clear fake service state
	h.FakeCloudTasks().ClearExecutedJobs()
	h.SlackRequestCapture().Clear()

	// Reset httpmock and restore mock responses
	httpmock.Reset()
	h.SetupMockResponses()
	httpmock.ZeroCallCounters()

	return nil
}

// ResetForNextStep provides mid-test cleanup for multi-step tests.
func (h *TestHarness) ResetForNextStep() {
	// Clear fake service state but keep Firestore data
	h.FakeCloudTasks().ClearExecutedJobs()
	h.SlackRequestCapture().Clear()

	// Reset httpmock and restore mock responses
	httpmock.Reset()
	h.SetupMockResponses()
	httpmock.ZeroCallCounters()
}

// SetupUser creates a test user in Firestore.
func (h *TestHarness) SetupUser(ctx context.Context, githubUsername, slackUserID, defaultChannel string) error {
	// Map GitHub usernames to consistent numeric IDs for testing
	githubUserIDMap := map[string]int64{
		"test-user":    100001,
		"draft-user":   100002,
		"draft-author": 100003,
	}

	githubUserID, exists := githubUserIDMap[githubUsername]
	if !exists {
		githubUserID = 999999 // Default fallback ID for unmapped users
	}

	user := map[string]interface{}{
		"id":                    githubUsername, // Use github username as ID for simplicity
		"github_username":       githubUsername,
		"github_user_id":        githubUserID, // Add numeric GitHub user ID
		"slack_user_id":         slackUserID,
		"default_channel":       defaultChannel,
		"verified":              true,         // Mark test users as verified
		"slack_team_id":         "T123456789", // Default test team ID
		"notifications_enabled": true,         // Enable notifications for test users
		"tagging_enabled":       true,         // Enable tagging for test users
	}
	_, err := h.testDB.Collection("users").Doc(githubUsername).Set(ctx, user)
	return err
}

// SetupRepo creates a test repository in Firestore.
func (h *TestHarness) SetupRepo(ctx context.Context, repoFullName, channelID, teamID string) error {
	// Encode the repo name the same way the real app does
	encodedRepoName := url.QueryEscape(repoFullName)
	docID := teamID + "#" + encodedRepoName

	repo := map[string]interface{}{
		"id":             teamID + "#" + repoFullName, // {workspace_id}#{repo_full_name} format
		"repo_full_name": repoFullName,                // Denormalized field for queries
		"workspace_id":   teamID,                      // Denormalized field for queries
		"enabled":        true,                        // Used in GetReposForAllWorkspaces() query
		"created_at":     time.Now(),
		// Legacy fields for backward compatibility
		"full_name":      repoFullName,
		"slack_channels": []string{channelID},
		"slack_team_id":  teamID,
	}
	_, err := h.testDB.Collection("repos").Doc(docID).Set(ctx, repo)
	return err
}

// SetupTrackedMessage creates a test tracked message in Firestore.
func (h *TestHarness) SetupTrackedMessage(
	ctx context.Context, repoFullName string, prNumber int, channelID, teamID, messageTS string,
) error {
	msg := map[string]interface{}{
		"repo_full_name": repoFullName,
		"pr_number":      prNumber,
		"slack_channel":  channelID,
		"slack_team_id":  teamID,
		"message_ts":     messageTS,
		"message_source": "webhook",
	}
	_, _, err := h.testDB.Collection("tracked_messages").Add(ctx, msg)
	return err
}

// SetupGitHubInstallation creates a test GitHub installation in Firestore.
func (h *TestHarness) SetupGitHubInstallation(ctx context.Context, installationID int64, accountLogin, accountType string) error {
	return h.SetupGitHubInstallationWithWorkspace(ctx, installationID, accountLogin, accountType, "T123456789", "U123456789")
}

// SetupGitHubInstallationWithWorkspace creates a test GitHub installation in Firestore with workspace association.
func (h *TestHarness) SetupGitHubInstallationWithWorkspace(
	ctx context.Context, installationID int64, accountLogin, accountType, workspaceID, slackUserID string,
) error {
	testTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	installation := &models.GitHubInstallation{
		ID:                  installationID,
		AccountLogin:        accountLogin,
		AccountType:         accountType,
		AccountID:           12345, // Test account ID
		RepositorySelection: "all",
		Repositories:        []string{},
		InstalledAt:         testTime,
		UpdatedAt:           testTime,

		// Workspace association fields
		SlackWorkspaceID:      workspaceID,
		InstalledBySlackUser:  slackUserID,
		InstalledByGitHubUser: 123456, // Test GitHub user ID
	}
	docID := fmt.Sprintf("%d", installationID)
	_, err := h.testDB.Collection("github_installations").Doc(docID).Set(ctx, installation)
	return err
}

// FakeCloudTasks returns the fake Cloud Tasks service for test assertions.
func (h *TestHarness) FakeCloudTasks() *FakeCloudTasksService {
	return h.fakeCloudTasks
}

// SlackRequestCapture returns the Slack request capture for test assertions.
func (h *TestHarness) SlackRequestCapture() *SlackRequestCapture {
	return h.slackRequestCapture
}

// SetupMockResponses sets up mock responses for this test harness's HTTP client.
//
//nolint:funlen,maintidx // Test setup function with many mock responses
func (h *TestHarness) SetupMockResponses() {
	// Mock Slack API responses with per-test request capture
	httpmock.RegisterResponder("POST", "https://slack.com/api/chat.postMessage",
		func(req *http.Request) (*http.Response, error) {
			// Capture the request using this test's capture
			if err := h.slackRequestCapture.CaptureRequest(req); err != nil {
				return nil, err
			}

			// Return standard response
			resp, err := httpmock.NewJsonResponse(200, map[string]interface{}{
				"ok":      true,
				"channel": "C1234567890",
				"ts":      "1234567890.123456",
				"message": map[string]interface{}{
					"text": "Test message",
					"ts":   "1234567890.123456",
				},
			})
			return resp, err
		})

	httpmock.RegisterResponder("POST", "https://slack.com/api/chat.delete",
		func(req *http.Request) (*http.Response, error) {
			// Capture the request using this test's capture
			if err := h.slackRequestCapture.CaptureRequest(req); err != nil {
				return nil, err
			}

			// Return standard response
			resp, err := httpmock.NewJsonResponse(200, map[string]interface{}{
				"ok":      true,
				"channel": "C1234567890",
				"ts":      "1234567890.123456",
			})
			return resp, err
		})

	httpmock.RegisterResponder("POST", "https://slack.com/api/reactions.add",
		func(req *http.Request) (*http.Response, error) {
			// Capture the request using this test's capture
			if err := h.slackRequestCapture.CaptureRequest(req); err != nil {
				return nil, err
			}

			// Return standard response
			resp, err := httpmock.NewJsonResponse(200, map[string]interface{}{
				"ok": true,
			})
			return resp, err
		})

	httpmock.RegisterResponder("POST", "https://slack.com/api/reactions.remove",
		func(req *http.Request) (*http.Response, error) {
			// Capture the request using this test's capture
			if err := h.slackRequestCapture.CaptureRequest(req); err != nil {
				return nil, err
			}

			// Return standard response
			resp, err := httpmock.NewJsonResponse(200, map[string]interface{}{
				"ok": true,
			})
			return resp, err
		})

	httpmock.RegisterResponder("GET", "https://slack.com/api/reactions.get",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok": true,
			"message": map[string]interface{}{
				"reactions": []map[string]interface{}{},
			},
		}))

	httpmock.RegisterResponder("POST", "https://slack.com/api/views.publish",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok": true,
		}))

	httpmock.RegisterResponder("POST", "https://slack.com/api/conversations.info",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok": true,
			"channel": map[string]interface{}{
				"id":          "C987654321",
				"name":        "test-channel",
				"is_channel":  true,
				"is_archived": false,
				"is_private":  false,
				"is_im":       false,
				"is_mpim":     false,
				"is_group":    false,
				"is_member":   true,
			},
		}))

	httpmock.RegisterResponder("POST", "https://slack.com/api/conversations.list",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok": true,
			"channels": []map[string]interface{}{
				{
					"id":              "C987654321",
					"name":            "test-channel",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     10,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "test-channel",
				},
				{
					"id":              "C123456789",
					"name":            "general",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     50,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "general",
				},
				{
					"id":              "C111111111",
					"name":            "pr-channel",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     15,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "pr-channel",
				},
				{
					"id":              "C222222222",
					"name":            "override-channel",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     8,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "override-channel",
				},
				{
					"id":              "C333333333",
					"name":            "combined-channel",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     12,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "combined-channel",
				},
				{
					"id":              "C444444444",
					"name":            "final-channel",
					"is_channel":      true,
					"is_archived":     false,
					"is_private":      false,
					"is_im":           false,
					"is_mpim":         false,
					"is_group":        false,
					"is_member":       true,
					"num_members":     6,
					"topic":           map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"purpose":         map[string]interface{}{"value": "", "creator": "", "last_set": 0},
					"created":         1234567890,
					"creator":         "U1234567890",
					"unlinked":        0,
					"name_normalized": "final-channel",
				},
			},
			"response_metadata": map[string]interface{}{
				"next_cursor": "",
			},
		}))

	httpmock.RegisterResponder("POST", "https://slack.com/api/users.info",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok": true,
			"user": map[string]interface{}{
				"id":        "U123456789",
				"name":      "test-user",
				"real_name": "Test User",
				"profile": map[string]interface{}{
					"display_name": "Test User",
					"real_name":    "Test User",
					"email":        "test@example.com",
				},
			},
		}))

	// Mock GitHub API responses
	httpmock.RegisterResponder("GET", `=~^https://api\.github\.com/repos/[^/]+/[^/]+/pulls/\d+$`,
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"number":    123,
			"title":     "Test PR",
			"body":      "Test description",
			"html_url":  "https://github.com/test/repo/pull/123",
			"state":     "open",
			"additions": 50,
			"deletions": 30,
			"user": map[string]interface{}{
				"login": "test-user",
			},
		}))

	// Mock GitHub App installation access token endpoint
	httpmock.RegisterResponder("POST", `=~^https://api\.github\.com/app/installations/\d+/access_tokens$`,
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"token":      "ghs_test_installation_token",
			"expires_at": "2025-12-31T23:59:59Z",
		}))

	// Mock GitHub PR reviews endpoint
	httpmock.RegisterResponder("GET", `=~^https://api\.github\.com/repos/[^/]+/[^/]+/pulls/\d+/reviews`,
		httpmock.NewJsonResponderOrPanic(200, []interface{}{
			map[string]interface{}{
				"id":     12345,
				"state":  "APPROVED",
				"author": map[string]interface{}{"login": "reviewer"},
			},
		}))

	// Mock Slack OAuth endpoint
	httpmock.RegisterResponder("POST", "https://slack.com/api/oauth.v2.access",
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"ok":           true,
			"access_token": "xoxb-test-token",
			"scope":        "channels:read,chat:write,links:read,channels:history",
			"team": map[string]interface{}{
				"id":   "T12345TEST",
				"name": "Test Workspace",
			},
			"authed_user": map[string]interface{}{
				"id": "U12345USER",
			},
		}))

	// Mock Cloud Tasks API responses (for job queueing)
	httpmock.RegisterResponder("POST", `=~^https://cloudtasks\.googleapis\.com/`,
		httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
			"name": fmt.Sprintf("projects/%s/locations/us-central1/queues/test-queue/tasks/test-task", h.config.GoogleCloudProject),
		}))
}

// loadTestPrivateKey loads the test private key from embedded data and returns it as base64.
func loadTestPrivateKey() string {
	return base64.StdEncoding.EncodeToString(testPrivateKeyPEM)
}
