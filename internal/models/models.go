package models

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrJobIDRequired          = errors.New("job ID is required")
	ErrEventTypeRequired      = errors.New("event type is required")
	ErrPayloadRequired        = errors.New("payload is required")
	ErrJobTypeRequired        = errors.New("job type is required")
	ErrUnsupportedJobType     = errors.New("unsupported job type")
	ErrPRNumberRequired       = errors.New("PR number is required")
	ErrRepoFullNameRequired   = errors.New("repository full name is required")
	ErrSlackChannelRequired   = errors.New("Slack channel is required")
	ErrSlackMessageTSRequired = errors.New("Slack message timestamp is required")
	ErrSlackTeamIDRequired    = errors.New("Slack team ID is required")
	ErrTraceIDRequired        = errors.New("trace ID is required")
)

type User struct {
	ID                   string    `firestore:"id"`
	GitHubUsername       string    `firestore:"github_username"`
	GitHubUserID         int64     `firestore:"github_user_id"` // GitHub numeric ID
	Verified             bool      `firestore:"verified"`       // OAuth verification status
	SlackUserID          string    `firestore:"slack_user_id"`
	SlackTeamID          string    `firestore:"slack_team_id"`
	DefaultChannel       string    `firestore:"default_channel"`
	NotificationsEnabled bool      `firestore:"notifications_enabled"` // Whether to post PRs for this user
	CreatedAt            time.Time `firestore:"created_at"`
	UpdatedAt            time.Time `firestore:"updated_at"`
}

// OAuthState represents temporary OAuth state for CSRF protection.
type OAuthState struct {
	ID           string    `firestore:"id"`             // Random UUID
	SlackUserID  string    `firestore:"slack_user_id"`  // Slack user initiating OAuth
	SlackTeamID  string    `firestore:"slack_team_id"`  // Slack team ID
	SlackChannel string    `firestore:"slack_channel"`  // Channel where OAuth was initiated
	ReturnToHome bool      `firestore:"return_to_home"` // Whether to refresh App Home after OAuth
	CreatedAt    time.Time `firestore:"created_at"`     // When state was created
	ExpiresAt    time.Time `firestore:"expires_at"`     // When state expires (15 minutes)
}

// TrackedMessage represents a tracked PR message in Slack (replaces old Message model).
type TrackedMessage struct {
	ID             string    `firestore:"id"`               // Auto-generated document ID
	PRNumber       int       `firestore:"pr_number"`        // GitHub PR number
	RepoFullName   string    `firestore:"repo_full_name"`   // e.g., "owner/repo"
	SlackChannel   string    `firestore:"slack_channel"`    // Slack channel ID
	SlackMessageTS string    `firestore:"slack_message_ts"` // Slack message timestamp
	SlackTeamID    string    `firestore:"slack_team_id"`    // Slack workspace/team ID
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
	ID          string    `firestore:"id"`
	SlackTeamID string    `firestore:"slack_team_id"` // Slack workspace/team ID
	Enabled     bool      `firestore:"enabled"`
	CreatedAt   time.Time `firestore:"created_at"`
}

// RepoWorkspaceMapping represents a single repo-workspace relationship.
// Each document represents one workspace that has registered a specific repository.
type RepoWorkspaceMapping struct {
	ID           string    `firestore:"id"`             // Format: {repo_full_name}#{workspace_id}
	RepoFullName string    `firestore:"repo_full_name"` // Repository full name (e.g., "owner/repo")
	WorkspaceID  string    `firestore:"workspace_id"`   // Slack team ID that has this repo registered
	CreatedAt    time.Time `firestore:"created_at"`     // When this mapping was created
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
	SlackTeamID    string `json:"slack_team_id"` // Slack workspace/team ID
	TraceID        string `json:"trace_id"`
}

// Validate validates required fields for ManualLinkJob.
func (mlj *ManualLinkJob) Validate() error {
	if mlj.ID == "" {
		return ErrJobIDRequired
	}
	if mlj.PRNumber <= 0 {
		return ErrPRNumberRequired
	}
	if mlj.RepoFullName == "" {
		return ErrRepoFullNameRequired
	}
	if mlj.SlackChannel == "" {
		return ErrSlackChannelRequired
	}
	if mlj.SlackMessageTS == "" {
		return ErrSlackMessageTSRequired
	}
	if mlj.SlackTeamID == "" {
		return ErrSlackTeamIDRequired
	}
	if mlj.TraceID == "" {
		return ErrTraceIDRequired
	}
	return nil
}

// Job types for the job processing system.
const (
	JobTypeGitHubWebhook = "github_webhook"
	JobTypeManualPRLink  = "manual_pr_link"
)

// Job represents a job structure for all async processing.
type Job struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	TraceID string          `json:"trace_id"`
	Payload json.RawMessage `json:"payload"`
}

// ChannelConfig represents per-channel configuration for manual PR tracking.
type ChannelConfig struct {
	ID                    string    `firestore:"id"`                      // Document ID: {slack_team_id}#{channel_id}
	SlackTeamID           string    `firestore:"slack_team_id"`           // Slack workspace ID
	SlackChannelID        string    `firestore:"slack_channel_id"`        // Slack channel ID
	SlackChannelName      string    `firestore:"slack_channel_name"`      // Cached channel name for display
	ManualTrackingEnabled bool      `firestore:"manual_tracking_enabled"` // Whether to track manual PR links
	ConfiguredBy          string    `firestore:"configured_by"`           // Slack user ID who last updated
	CreatedAt             time.Time `firestore:"created_at"`
	UpdatedAt             time.Time `firestore:"updated_at"`
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

func (j *Job) Validate() error {
	if j.ID == "" {
		return ErrJobIDRequired
	}
	if j.Type == "" {
		return ErrJobTypeRequired
	}
	if len(j.Payload) == 0 {
		return ErrPayloadRequired
	}
	return nil
}
