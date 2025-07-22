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
	"github-slack-notifier/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

// App represents the main application structure with all services and handlers.
type App struct {
	config            *config.Config
	firestoreService  *services.FirestoreService
	slackService      *services.SlackService
	cloudTasksService *services.CloudTasksService
	githubAuthService *services.GitHubAuthService
	githubHandler     *handlers.GitHubHandler
	slackHandler      *handlers.SlackHandler
	jobProcessor      *handlers.JobProcessor
	oauthHandler      *handlers.OAuthHandler
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

	slackClient := slack.New(cfg.SlackBotToken)
	slackService := services.NewSlackService(slackClient, cfg.Emoji)

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
		firestoreService,
		slackService,
		cfg.GitHubWebhookSecret,
	)
	githubAuthService := services.NewGitHubAuthService(cfg, firestoreService)
	oauthHandler := handlers.NewOAuthHandler(githubAuthService, firestoreService, slackService)

	slackHandler := handlers.NewSlackHandler(
		firestoreService, slackService, cloudTasksService, githubAuthService, cfg,
	)

	jobProcessor := handlers.NewJobProcessor(githubHandler, slackHandler, cfg)

	app := &App{
		config:            cfg,
		firestoreService:  firestoreService,
		slackService:      slackService,
		cloudTasksService: cloudTasksService,
		githubAuthService: githubAuthService,
		githubHandler:     githubHandler,
		slackHandler:      slackHandler,
		jobProcessor:      jobProcessor,
		oauthHandler:      oauthHandler,
	}

	router := gin.Default()

	// Add middleware
	router.Use(middleware.LoggingMiddleware())

	// Configure webhook routes
	router.POST("/webhooks/github", app.githubHandler.HandleWebhook)

	// Configure unified job processing route with Cloud Tasks authentication
	router.POST("/jobs/process", middleware.CloudTasksAuthMiddleware(cfg), app.jobProcessor.ProcessJob)

	// Configure OAuth routes
	router.GET("/auth/github/link", app.oauthHandler.HandleGitHubLink)
	router.GET("/auth/github/callback", app.oauthHandler.HandleGitHubCallback)

	router.POST("/webhooks/slack/events", app.slackHandler.HandleEvent)
	router.POST("/webhooks/slack/interactions", app.slackHandler.HandleInteraction)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Setup server logging context
	serverCtx := log.WithFields(ctx, log.LogFields{
		"component": "server",
	})

	log.Info(serverCtx, "Starting server", "port", cfg.Port)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(serverCtx, "Server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info(serverCtx, "Shutting down server...")

	// Give outstanding requests time to complete
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error(serverCtx, "Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	log.Info(serverCtx, "Server exited gracefully")
}
