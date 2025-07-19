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
	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/handlers"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/middleware"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

// App represents the main application structure with all services and handlers.
type App struct {
	config               *config.Config
	firestoreService     *services.FirestoreService
	slackService         *services.SlackService
	cloudTasksService    *services.CloudTasksService
	githubAuthService    *services.GitHubAuthService
	githubHandler        *handlers.GitHubHandler
	webhookWorkerHandler *handlers.WebhookWorkerHandler
	slackHandler         *handlers.SlackHandler
	oauthHandler         *handlers.OAuthHandler
}

func main() {
	// Load configuration (panics on invalid config)
	cfg := config.Load()

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

	log.Info(ctx, "Connecting to Firestore", "project_id", cfg.FirestoreProjectID, "database_id", cfg.FirestoreDatabaseID)
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, cfg.FirestoreProjectID, cfg.FirestoreDatabaseID)
	if err != nil {
		log.Error(ctx, "Failed to create Firestore client", "component", "startup", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := firestoreClient.Close(); err != nil {
			log.Error(context.Background(), "Error closing Firestore client", "component", "shutdown", "error", err)
		}
	}()

	firestoreService := services.NewFirestoreService(firestoreClient)
	slackService := services.NewSlackService(slack.New(cfg.SlackBotToken), cfg.Emoji)

	// Initialize Cloud Tasks service
	cloudTasksConfig := services.CloudTasksConfig{
		ProjectID: cfg.GoogleCloudProject,
		Location:  cfg.GCPRegion,
		QueueName: cfg.CloudTasksQueue,
		Config:    cfg,
	}

	cloudTasksService, err := services.NewCloudTasksService(cloudTasksConfig)
	if err != nil {
		log.Error(ctx, "Failed to create Cloud Tasks service", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := cloudTasksService.Close(); err != nil {
			log.Error(context.Background(), "Error closing Cloud Tasks client", "error", err)
		}
	}()

	githubHandler := handlers.NewGitHubHandler(
		cloudTasksService,
		cfg.GitHubWebhookSecret,
	)
	webhookWorkerHandler := handlers.NewWebhookWorkerHandler(firestoreService, slackService, cfg)
	githubAuthService := services.NewGitHubAuthService(cfg, firestoreService)
	oauthHandler := handlers.NewOAuthHandler(githubAuthService, firestoreService)

	app := &App{
		config:               cfg,
		firestoreService:     firestoreService,
		slackService:         slackService,
		cloudTasksService:    cloudTasksService,
		githubAuthService:    githubAuthService,
		githubHandler:        githubHandler,
		webhookWorkerHandler: webhookWorkerHandler,
		slackHandler: handlers.NewSlackHandler(
			firestoreService, slackService, cloudTasksService, githubAuthService, cfg,
		),
		oauthHandler: oauthHandler,
	}

	router := gin.Default()

	// Add middleware
	router.Use(middleware.LoggingMiddleware())

	// Configure webhook routes
	router.POST("/webhooks/github", app.githubHandler.HandleWebhook)
	router.POST("/process-webhook", app.webhookWorkerHandler.ProcessWebhook)
	router.POST("/process-manual-link", app.webhookWorkerHandler.ProcessManualLink)

	// Configure OAuth routes
	router.GET("/auth/github/link", app.oauthHandler.HandleGitHubLink)
	router.GET("/auth/github/callback", app.oauthHandler.HandleGitHubCallback)

	router.POST("/webhooks/slack/slash-command", app.slackHandler.HandleSlashCommand)
	router.POST("/webhooks/slack/events", app.slackHandler.HandleEvent)
	router.POST("/api/repos", app.handleRepoRegistration)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	log.Info(ctx, "Starting server", "component", "server", "port", cfg.Port)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(context.Background(), "Server failed to start", "component", "server", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info(context.Background(), "Shutting down server...", "component", "server")

	// Give outstanding requests time to complete
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error(ctx, "Server forced to shutdown", "component", "server", "error", err)
		os.Exit(1)
	}

	log.Info(context.Background(), "Server exited gracefully", "component", "server")
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
		ctx := c.Request.Context()
		log.Error(ctx, "Invalid JSON in repository registration request",
			"error", err,
			"content_type", c.ContentType(),
			"content_length", c.Request.ContentLength,
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	repo := &models.Repo{
		ID:             req.RepoFullName,
		DefaultChannel: req.DefaultChannel,
		WebhookSecret:  req.WebhookSecret,
		Enabled:        true,
	}

	ctx := c.Request.Context()
	err := app.firestoreService.CreateRepo(ctx, repo)
	if err != nil {
		log.Error(ctx, "Failed to create repository registration",
			"error", err,
			"repo_full_name", req.RepoFullName,
			"default_channel", req.DefaultChannel,
			"operation", "register_repository",
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Repository registered successfully"})
}
