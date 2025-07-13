package models

import (
	"errors"
	"time"
)

var (
	ErrJobIDRequired     = errors.New("job ID is required")
	ErrEventTypeRequired = errors.New("event type is required")
	ErrPayloadRequired   = errors.New("payload is required")
)

type User struct {
	ID             string    `firestore:"id"`
	GitHubUsername string    `firestore:"github_username"`
	SlackUserID    string    `firestore:"slack_user_id"`
	SlackTeamID    string    `firestore:"slack_team_id"`
	DefaultChannel string    `firestore:"default_channel"`
	CreatedAt      time.Time `firestore:"created_at"`
	UpdatedAt      time.Time `firestore:"updated_at"`
}

// TrackedMessage represents a tracked PR message in Slack (replaces old Message model).
type TrackedMessage struct {
	ID             string    `firestore:"id"`               // Auto-generated document ID
	PRNumber       int       `firestore:"pr_number"`        // GitHub PR number
	RepoFullName   string    `firestore:"repo_full_name"`   // e.g., "owner/repo"
	SlackChannel   string    `firestore:"slack_channel"`    // Slack channel ID
	SlackMessageTS string    `firestore:"slack_message_ts"` // Slack message timestamp
	MessageSource  string    `firestore:"message_source"`   // "bot" or "manual"
	CreatedAt      time.Time `firestore:"created_at"`       // When we started tracking this message
}

// Message (deprecated - replaced by TrackedMessage but kept for migration compatibility).
type Message struct {
	ID                   string    `firestore:"id"`
	PRNumber             int       `firestore:"pr_number"`
	RepoFullName         string    `firestore:"repo_full_name"`
	SlackChannel         string    `firestore:"slack_channel"`
	SlackMessageTS       string    `firestore:"slack_message_ts"`
	GitHubPRURL          string    `firestore:"github_pr_url"`
	AuthorGitHubUsername string    `firestore:"author_github_username"`
	CreatedAt            time.Time `firestore:"created_at"`
	LastStatus           string    `firestore:"last_status"`
}

type Repo struct {
	ID             string    `firestore:"id"`
	DefaultChannel string    `firestore:"default_channel"`
	WebhookSecret  string    `firestore:"webhook_secret"`
	Enabled        bool      `firestore:"enabled"`
	CreatedAt      time.Time `firestore:"created_at"`
}

type WebhookJob struct {
	ID          string     `firestore:"id"                     json:"id"`
	EventType   string     `firestore:"event_type"             json:"event_type"`
	DeliveryID  string     `firestore:"delivery_id"            json:"delivery_id"`
	TraceID     string     `firestore:"trace_id"               json:"trace_id"`
	Payload     []byte     `firestore:"payload"                json:"payload"`
	ReceivedAt  time.Time  `firestore:"received_at"            json:"received_at"`
	ProcessedAt *time.Time `firestore:"processed_at,omitempty" json:"processed_at,omitempty"`
	Status      string     `firestore:"status"                 json:"status"`
	RetryCount  int        `firestore:"retry_count"            json:"retry_count"`
	LastError   string     `firestore:"last_error,omitempty"   json:"last_error,omitempty"`
}

// ManualLinkJob represents a job to process manually detected PR links.
type ManualLinkJob struct {
	ID             string `json:"id"`
	PRNumber       int    `json:"pr_number"`
	RepoFullName   string `json:"repo_full_name"`
	SlackChannel   string `json:"slack_channel"`
	SlackMessageTS string `json:"slack_message_ts"`
	TraceID        string `json:"trace_id"`
}

func (wj *WebhookJob) Validate() error {
	if wj.ID == "" {
		return ErrJobIDRequired
	}
	if wj.EventType == "" {
		return ErrEventTypeRequired
	}
	if len(wj.Payload) == 0 {
		return ErrPayloadRequired
	}
	return nil
}
