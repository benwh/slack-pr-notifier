package e2e

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/middleware"
	"github-slack-notifier/internal/services"
	firestoreTesting "github-slack-notifier/internal/testing"
	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
)

// TestHarness provides an end-to-end testing environment for the application.
type TestHarness struct {
	// Server details
	baseURL string

	// Configuration
	config *config.Config

	// Services (exposed for testing)
	SlackWorkspaceService *services.SlackWorkspaceService
	Router                *gin.Engine

	// Firestore emulator for cleanup
	firestoreEmulator *firestoreTesting.FirestoreEmulator

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

	// Setup Firestore emulator
	emulator, emulatorCtx := firestoreTesting.SetupFirestoreEmulator(t)

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "Failed to get TCP address")
	port := tcpAddr.Port
	require.NoError(t, listener.Close())

	// Create test configuration
	cfg := &config.Config{
		Port:                  fmt.Sprintf("%d", port),
		GinMode:               "test",
		LogLevel:              "error", // Keep logs quiet during tests
		FirestoreProjectID:    "test-project",
		FirestoreDatabaseID:   "(default)",
		SlackSigningSecret:    "test-signing-secret",
		SlackClientID:         "test_client_id",
		SlackClientSecret:     "test_client_secret",
		BaseURL:               fmt.Sprintf("http://localhost:%d", port),
		GitHubWebhookSecret:   "test-webhook-secret",
		GitHubClientID:        "test-github-client-id",
		GitHubClientSecret:    "test-github-client-secret",
		GoogleCloudProject:    "test-project",
		GCPRegion:             "us-central1",
		CloudTasksQueue:       "test-queue",
		CloudTasksSecret:      "test-cloud-tasks-secret",
		CloudTasksMaxAttempts: 3, // Allow retries in tests
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

	// Create HTTP client with mocking
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Activate httpmock for this client
	httpmock.ActivateNonDefault(httpClient)

	// Create fake Cloud Tasks service
	fakeCloudTasks := NewFakeCloudTasksService(
		fmt.Sprintf("http://127.0.0.1:%d", port),
		cfg.CloudTasksSecret,
	)

	// Create a channel to receive services from the application startup
	servicesChan := make(chan *appServices, 1)

	// Start the application in a goroutine
	go startApplication(emulatorCtx, cfg, emulator.Client, httpClient, fakeCloudTasks, servicesChan)

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL)

	// Wait for services to be available
	services := <-servicesChan

	// Create request capture
	slackCapture := NewSlackRequestCapture()

	return &TestHarness{
		baseURL:               baseURL,
		config:                cfg,
		SlackWorkspaceService: services.slackWorkspaceService,
		Router:                services.router,
		firestoreEmulator:     emulator,
		fakeCloudTasks:        fakeCloudTasks,
		httpClient:            httpClient,
		cancel:                cancel,
		slackRequestCapture:   slackCapture,
	}
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

	// Create handlers
	githubHandler := handlers.NewGitHubHandler(
		fakeCloudTasks,
		firestoreService,
		slackService,
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

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error(ctx, "Server failed", "error", err)
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
	// Deactivate httpmock for this client
	httpmock.Deactivate()

	// Cancel context to trigger shutdown
	h.cancel()

	// Give the server a moment to shut down gracefully
	time.Sleep(50 * time.Millisecond)

	// Cleanup Firestore emulator
	h.firestoreEmulator.Cleanup()
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

// ClearFirestore clears all data from the Firestore emulator.
func (h *TestHarness) ClearFirestore(ctx context.Context) error {
	return h.firestoreEmulator.ClearData(ctx)
}

// SetupUser creates a test user in Firestore.
func (h *TestHarness) SetupUser(ctx context.Context, githubUsername, slackUserID, defaultChannel string) error {
	user := map[string]interface{}{
		"id":                    githubUsername, // Use github username as ID for simplicity
		"github_username":       githubUsername,
		"slack_user_id":         slackUserID,
		"default_channel":       defaultChannel,
		"verified":              true,         // Mark test users as verified
		"slack_team_id":         "T123456789", // Default test team ID
		"notifications_enabled": true,         // Enable notifications for test users
	}
	_, err := h.firestoreEmulator.Client.Collection("users").Doc(githubUsername).Set(ctx, user)
	return err
}

// SetupRepo creates a test repository in Firestore.
func (h *TestHarness) SetupRepo(ctx context.Context, repoFullName, channelID, teamID string) error {
	// Encode the repo name the same way the real app does
	encodedRepoName := url.QueryEscape(repoFullName)
	docID := teamID + "#" + encodedRepoName

	repo := map[string]interface{}{
		"id":             repoFullName, // The real repo stores the unencoded name as 'id'
		"full_name":      repoFullName,
		"slack_channels": []string{channelID},
		"slack_team_id":  teamID,
	}
	_, err := h.firestoreEmulator.Client.Collection("repos").Doc(docID).Set(ctx, repo)
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
	_, _, err := h.firestoreEmulator.Client.Collection("tracked_messages").Add(ctx, msg)
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

// SetupMockResponses sets up common mock responses for GitHub and Slack APIs.
func (h *TestHarness) SetupMockResponses() {
	// Mock Slack API responses with request capture
	httpmock.RegisterResponder("POST", "https://slack.com/api/chat.postMessage",
		func(req *http.Request) (*http.Response, error) {
			// Capture the request
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
			// Capture the request
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
			// Capture the request
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
			// Capture the request
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
			"name": "projects/test-project/locations/us-central1/queues/test-queue/tasks/test-task",
		}))
}
