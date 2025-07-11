package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
	"google.golang.org/api/option"

	"github-slack-notifier/handlers"
	"github-slack-notifier/models"
	"github-slack-notifier/services"
)

type App struct {
	firestoreService *services.FirestoreService
	slackService     *services.SlackService
	githubHandler    *handlers.GitHubHandler
	slackHandler     *handlers.SlackHandler
	adminAPIKey      string
}

func main() {
	ctx := context.Background()

	projectID := os.Getenv("FIRESTORE_PROJECT_ID")
	if projectID == "" {
		log.Fatal("FIRESTORE_PROJECT_ID environment variable is required")
	}

	slackToken := os.Getenv("SLACK_BOT_TOKEN")
	if slackToken == "" {
		log.Fatal("SLACK_BOT_TOKEN environment variable is required")
	}

	firestoreClient, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create Firestore client: %v", err)
	}
	defer firestoreClient.Close()

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

	log.Printf("Starting server on port %s", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), router))
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