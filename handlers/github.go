package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github-slack-notifier/models"
	"github-slack-notifier/services"
)

type GitHubHandler struct {
	firestoreService *services.FirestoreService
	slackService     *services.SlackService
	webhookSecret    string
}

func NewGitHubHandler(fs *services.FirestoreService, slack *services.SlackService, secret string) *GitHubHandler {
	return &GitHubHandler{
		firestoreService: fs,
		slackService:     slack,
		webhookSecret:    secret,
	}
}

type GitHubWebhookPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Draft  bool   `json:"draft"`
		HTMLURL string `json:"html_url"`
		User   struct {
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

	switch eventType {
	case "pull_request":
		err = gh.handlePullRequestEvent(ctx, &payload)
	case "pull_request_review":
		err = gh.handlePullRequestReviewEvent(ctx, &payload)
	default:
		c.JSON(http.StatusOK, gin.H{"message": "Event type not handled"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Event processed"})
}

func (gh *GitHubHandler) handlePullRequestEvent(ctx context.Context, payload *GitHubWebhookPayload) error {
	switch payload.Action {
	case "opened":
		return gh.handlePROpened(ctx, payload)
	case "closed":
		return gh.handlePRClosed(ctx, payload)
	}
	return nil
}

func (gh *GitHubHandler) handlePROpened(ctx context.Context, payload *GitHubWebhookPayload) error {
	if payload.PullRequest.Draft {
		return nil
	}

	authorID := strconv.Itoa(payload.PullRequest.User.ID)
	user, err := gh.firestoreService.GetUserByGitHubID(ctx, authorID)
	if err != nil {
		return err
	}

	var targetChannel string
	if annotatedChannel := gh.slackService.ExtractChannelFromDescription(payload.PullRequest.Body); annotatedChannel != "" {
		targetChannel = annotatedChannel
	} else if user != nil && user.DefaultChannel != "" {
		targetChannel = user.DefaultChannel
	} else {
		repo, err := gh.firestoreService.GetRepo(ctx, payload.Repository.FullName)
		if err != nil {
			return err
		}
		if repo != nil {
			targetChannel = repo.DefaultChannel
		}
	}

	if targetChannel == "" {
		return nil
	}

	timestamp, err := gh.slackService.PostPRMessage(
		targetChannel,
		payload.Repository.Name,
		payload.PullRequest.Title,
		payload.PullRequest.User.Login,
		payload.PullRequest.Body,
		payload.PullRequest.HTMLURL,
	)
	if err != nil {
		return err
	}

	message := &models.Message{
		PRNumber:       payload.PullRequest.Number,
		RepoFullName:   payload.Repository.FullName,
		SlackChannel:   targetChannel,
		SlackMessageTS: timestamp,
		GitHubPRURL:    payload.PullRequest.HTMLURL,
		AuthorGitHubID: authorID,
		LastStatus:     "opened",
	}

	return gh.firestoreService.CreateMessage(ctx, message)
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

	message.LastStatus = fmt.Sprintf("review_%s", payload.Review.State)
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