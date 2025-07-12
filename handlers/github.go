// Package handlers provides HTTP handlers for GitHub and Slack webhooks.
package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github-slack-notifier/log"
	"github-slack-notifier/models"
	"github-slack-notifier/services"

	"github.com/gin-gonic/gin"
)

// GitHubHandler handles GitHub webhook events.
type GitHubHandler struct {
	firestoreService *services.FirestoreService
	slackService     *services.SlackService
	webhookSecret    string
}

// NewGitHubHandler creates a new GitHubHandler with the provided services and webhook secret.
func NewGitHubHandler(fs *services.FirestoreService, slack *services.SlackService, secret string) *GitHubHandler {
	return &GitHubHandler{
		firestoreService: fs,
		slackService:     slack,
		webhookSecret:    secret,
	}
}

// GitHubWebhookPayload represents the structure of GitHub webhook events.
type GitHubWebhookPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Draft   bool   `json:"draft"`
		HTMLURL string `json:"html_url"`
		User    struct {
			ID    int    `json:"id"`
			Login string `json:"login"`
		} `json:"user"`
		Merged bool `json:"merged"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
	} `json:"repository"`
	Review struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"review"`
}

// HandleWebhook processes incoming GitHub webhook events.
func (gh *GitHubHandler) HandleWebhook(c *gin.Context) {
	signature := c.GetHeader("X-Hub-Signature-256")
	if signature == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing signature"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read body"})
		return
	}

	if !gh.verifySignature(signature, body) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	var payload GitHubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	eventType := c.GetHeader("X-GitHub-Event")
	ctx := context.Background()

	log.Debug(ctx, "Processing GitHub webhook",
		"event_type", eventType,
		"repository", payload.Repository.FullName,
		"action", payload.Action,
	)

	switch eventType {
	case "pull_request":
		err = gh.handlePullRequestEvent(ctx, &payload)
	case "pull_request_review":
		err = gh.handlePullRequestReviewEvent(ctx, &payload)
	default:
		log.Debug(ctx, "Event type not handled", "event_type", eventType)
		c.JSON(http.StatusOK, gin.H{"message": "Event type not handled"})
		return
	}

	if err != nil {
		log.Error(ctx, "GitHub webhook processing failed",
			"event_type", eventType,
			"repository", payload.Repository.FullName,
			"action", payload.Action,
			"error", err,
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process webhook event"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Event processed"})
}

func (gh *GitHubHandler) handlePullRequestEvent(ctx context.Context, payload *GitHubWebhookPayload) error {
	log.Info(ctx, "Handling pull request event",
		"action", payload.Action,
		"pr_number", payload.PullRequest.Number,
		"is_draft", payload.PullRequest.Draft,
	)

	switch payload.Action {
	case "opened":
		return gh.handlePROpened(ctx, payload)
	case "closed":
		return gh.handlePRClosed(ctx, payload)
	default:
		log.Warn(ctx, "Pull request action not handled", "action", payload.Action)
	}
	return nil
}

func (gh *GitHubHandler) handlePROpened(ctx context.Context, payload *GitHubWebhookPayload) error {
	if payload.PullRequest.Draft {
		log.Debug(ctx, "Skipping draft PR", "pr_number", payload.PullRequest.Number)
		return nil
	}

	log.Debug(ctx, "Processing PR opened",
		"pr_number", payload.PullRequest.Number,
		"author", payload.PullRequest.User.Login,
		"title", payload.PullRequest.Title,
	)

	authorUsername := payload.PullRequest.User.Login
	log.Debug(ctx, "Looking up user by GitHub username", "github_username", authorUsername)
	user, err := gh.firestoreService.GetUserByGitHubUsername(ctx, authorUsername)
	if err != nil {
		log.Error(ctx, "Failed to lookup user", "github_username", authorUsername, "error", err)
		return err
	}
	log.Debug(ctx, "User lookup result", "user_found", user != nil)

	var targetChannel string
	annotatedChannel := gh.slackService.ExtractChannelFromDescription(payload.PullRequest.Body)
	log.Debug(ctx, "Channel determination", "annotated_channel", annotatedChannel)
	if annotatedChannel != "" {
		targetChannel = annotatedChannel
	} else if user != nil && user.DefaultChannel != "" {
		targetChannel = user.DefaultChannel
		log.Debug(ctx, "Using user default channel", "channel", targetChannel)
	} else {
		log.Debug(ctx, "Looking up repo default channel", "repo", payload.Repository.FullName)
		repo, err := gh.firestoreService.GetRepo(ctx, payload.Repository.FullName)
		if err != nil {
			log.Error(ctx, "Failed to lookup repo", "repo", payload.Repository.FullName, "error", err)
			return err
		}
		if repo != nil {
			targetChannel = repo.DefaultChannel
			log.Debug(ctx, "Using repo default channel", "channel", targetChannel)
		} else {
			log.Debug(ctx, "No repo found in database", "repo", payload.Repository.FullName)
		}
	}

	if targetChannel == "" {
		log.Info(ctx, "No target channel determined, skipping notification")
		return nil
	}

	log.Info(ctx, "Posting PR message to Slack", "channel", targetChannel)

	timestamp, err := gh.slackService.PostPRMessage(
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack", "channel", targetChannel, "error", err)
		return err
	}
	log.Info(ctx, "Posted PR notification to Slack", "channel", targetChannel, "pr_number", payload.PullRequest.Number)

	message := &models.Message{
		PRNumber:             payload.PullRequest.Number,
		RepoFullName:         payload.Repository.FullName,
		SlackChannel:         targetChannel,
		SlackMessageTS:       timestamp,
		GitHubPRURL:          payload.PullRequest.HTMLURL,
		AuthorGitHubUsername: authorUsername,
		LastStatus:           "opened",
	}

	log.Debug(ctx, "Saving message to database", "pr_number", message.PRNumber, "channel", message.SlackChannel)
	err = gh.firestoreService.CreateMessage(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to save message to database", "error", err)
		return err
	}
	log.Debug(ctx, "Successfully saved message to database")
	return nil
}

func (gh *GitHubHandler) handlePRClosed(ctx context.Context, payload *GitHubWebhookPayload) error {
	message, err := gh.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil || message == nil {
		return err
	}

	emoji := gh.slackService.GetEmojiForPRState("closed", payload.PullRequest.Merged)
	if emoji != "" {
		err = gh.slackService.AddReaction(message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			return err
		}
	}

	message.LastStatus = "closed"
	return gh.firestoreService.UpdateMessage(ctx, message)
}

func (gh *GitHubHandler) handlePullRequestReviewEvent(ctx context.Context, payload *GitHubWebhookPayload) error {
	if payload.Action != "submitted" {
		return nil
	}

	message, err := gh.firestoreService.GetMessage(ctx, payload.Repository.FullName, payload.PullRequest.Number)
	if err != nil || message == nil {
		return err
	}

	emoji := gh.slackService.GetEmojiForReviewState(payload.Review.State)
	if emoji != "" {
		err = gh.slackService.AddReaction(message.SlackChannel, message.SlackMessageTS, emoji)
		if err != nil {
			return err
		}
	}

	message.LastStatus = "review_" + payload.Review.State
	return gh.firestoreService.UpdateMessage(ctx, message)
}

func (gh *GitHubHandler) verifySignature(signature string, body []byte) bool {
	if gh.webhookSecret == "" {
		return true
	}

	mac := hmac.New(sha256.New, []byte(gh.webhookSecret))
	mac.Write(body)
	expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
