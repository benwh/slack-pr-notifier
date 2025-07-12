package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/middleware"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
)

// App represents the main application structure with all services and handlers.
type App struct {
	config               *config.Config
	firestoreService     *services.FirestoreService
	slackService         *services.SlackService
	cloudTasksService    *services.CloudTasksService
	validationService    *services.ValidationService
	githubAsyncHandler   *handlers.GitHubAsyncHandler
	webhookWorkerHandler *handlers.WebhookWorkerHandler
	slackHandler         *handlers.SlackHandler
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Setup structured logging
	var logger *slog.Logger
	isDev := cfg.GinMode != "release"
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	if isDev {
		// Use text format for development
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	} else {
		// Use JSON format for production
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	}
	slog.SetDefault(logger)

	// Set Gin mode
	gin.SetMode(cfg.GinMode)

	ctx := context.Background()

	slog.Info("Connecting to Firestore", "project_id", cfg.FirestoreProjectID, "database_id", cfg.FirestoreDatabaseID)
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, cfg.FirestoreProjectID, cfg.FirestoreDatabaseID)
	if err != nil {
		slog.Error("Failed to create Firestore client", "component", "startup", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := firestoreClient.Close(); err != nil {
			slog.Error("Error closing Firestore client", "component", "shutdown", "error", err)
		}
	}()

	firestoreService := services.NewFirestoreService(firestoreClient)
	slackService := services.NewSlackService(slack.New(cfg.SlackBotToken))

	// Initialize Cloud Tasks service
	cloudTasksConfig := services.CloudTasksConfig{
		ProjectID: cfg.GoogleCloudProject,
		Location:  cfg.GCPRegion,
		QueueName: cfg.CloudTasksQueue,
		WorkerURL: cfg.WebhookWorkerURL,
	}

	cloudTasksService, err := services.NewCloudTasksService(cloudTasksConfig)
	if err != nil {
		slog.Error("Failed to create Cloud Tasks service", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := cloudTasksService.Close(); err != nil {
			slog.Error("Error closing Cloud Tasks client", "error", err)
		}
	}()

	validationService := services.NewValidationService()
	githubAsyncHandler := handlers.NewGitHubAsyncHandler(
		cloudTasksService,
		validationService,
		cfg.GitHubWebhookSecret,
	)
	webhookWorkerHandler := handlers.NewWebhookWorkerHandler(firestoreService, slackService, cfg)

	app := &App{
		config:               cfg,
		firestoreService:     firestoreService,
		slackService:         slackService,
		cloudTasksService:    cloudTasksService,
		validationService:    validationService,
		githubAsyncHandler:   githubAsyncHandler,
		webhookWorkerHandler: webhookWorkerHandler,
		slackHandler:         handlers.NewSlackHandler(firestoreService, slackService, cfg),
	}

	router := gin.Default()

	// Add middleware
	router.Use(middleware.LoggingMiddleware())

	// Configure webhook routes
	router.POST("/webhooks/github", app.githubAsyncHandler.HandleWebhook)
	router.POST("/process-webhook", app.webhookWorkerHandler.ProcessWebhook)

	router.POST("/webhooks/slack", app.slackHandler.HandleWebhook)
	router.POST("/api/repos", app.handleRepoRegistration)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	slog.Info("Starting server", "component", "server", "port", cfg.Port)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server failed to start", "component", "server", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...", "component", "server")

	// Give outstanding requests time to complete
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "component", "server", "error", err)
		os.Exit(1)
	}

	slog.Info("Server exited gracefully", "component", "server")
}

func (app *App) handleRepoRegistration(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" || apiKey != app.config.APIAdminKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
		return
	}

	var req struct {
		RepoFullName   string `json:"repo_full_name"`
		DefaultChannel string `json:"default_channel"`
		WebhookSecret  string `json:"webhook_secret"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	repo := &models.Repo{
		ID:             req.RepoFullName,
		DefaultChannel: req.DefaultChannel,
		WebhookSecret:  req.WebhookSecret,
		Enabled:        true,
	}

	ctx := context.Background()
	err := app.firestoreService.CreateRepo(ctx, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Repository registered successfully"})
}
