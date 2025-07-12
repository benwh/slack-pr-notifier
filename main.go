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
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"

	"github-slack-notifier/handlers"
	"github-slack-notifier/middleware"
	"github-slack-notifier/models"
	"github-slack-notifier/services"
)

// App represents the main application structure with all services and handlers.
type App struct {
	firestoreService *services.FirestoreService
	slackService     *services.SlackService
	githubHandler    *handlers.GitHubHandler
	slackHandler     *handlers.SlackHandler
	adminAPIKey      string
}

func main() {
	// Setup structured logging
	var logger *slog.Logger
	isDev := os.Getenv("GIN_MODE") != "release"
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
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

	ctx := context.Background()

	projectID := os.Getenv("FIRESTORE_PROJECT_ID")
	if projectID == "" {
		slog.Error("FIRESTORE_PROJECT_ID environment variable is required", "component", "startup")
		os.Exit(1)
	}

	slackToken := os.Getenv("SLACK_BOT_TOKEN")
	if slackToken == "" {
		slog.Error("SLACK_BOT_TOKEN environment variable is required", "component", "startup")
		os.Exit(1)
	}

	firestoreClient, err := firestore.NewClient(ctx, projectID)
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
	slackService := services.NewSlackService(slack.New(slackToken))

	app := &App{
		firestoreService: firestoreService,
		slackService:     slackService,
		githubHandler:    handlers.NewGitHubHandler(firestoreService, slackService, os.Getenv("GITHUB_WEBHOOK_SECRET")),
		slackHandler:     handlers.NewSlackHandler(firestoreService, slackService, os.Getenv("SLACK_SIGNING_SECRET")),
		adminAPIKey:      os.Getenv("API_ADMIN_KEY"),
	}

	router := gin.Default()

	// Add middleware
	router.Use(middleware.LoggingMiddleware())

	router.POST("/webhooks/github", app.githubHandler.HandleWebhook)
	router.POST("/webhooks/slack", app.slackHandler.HandleWebhook)
	router.POST("/api/repos", app.handleRepoRegistration)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("Starting server", "component", "server", "port", port)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
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

	// Give outstanding requests 30 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "component", "server", "error", err)
		os.Exit(1)
	}

	slog.Info("Server exited gracefully", "component", "server")
}

func (app *App) handleRepoRegistration(c *gin.Context) {
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" || apiKey != app.adminAPIKey {
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
