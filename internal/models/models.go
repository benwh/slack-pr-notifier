package models

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrJobIDRequired               = errors.New("job ID is required")
	ErrEventTypeRequired           = errors.New("event type is required")
	ErrPayloadRequired             = errors.New("payload is required")
	ErrJobTypeRequired             = errors.New("job type is required")
	ErrUnsupportedJobType          = errors.New("unsupported job type")
	ErrPRNumberRequired            = errors.New("PR number is required")
	ErrRepoFullNameRequired        = errors.New("repository full name is required")
	ErrSlackChannelRequired        = errors.New("slack channel is required")
	ErrSlackMessageTSRequired      = errors.New("slack message timestamp is required")
	ErrSlackTeamIDRequired         = errors.New("slack team ID is required")
	ErrTraceIDRequired             = errors.New("trace ID is required")
	ErrAccessTokenRequired         = errors.New("access token is required")
	ErrTeamNameRequired            = errors.New("team name is required")
	ErrSlackOAuthFailed            = errors.New("slack OAuth failed")
	ErrInstallationIDRequired      = errors.New("installation ID is required")
	ErrAccountLoginRequired        = errors.New("account login is required")
	ErrAccountTypeRequired         = errors.New("account type is required")
	ErrRepositorySelectionRequired = errors.New("repository selection is required")
	ErrInvalidRepositorySelection  = errors.New("repository selection must be 'all' or 'selected'")
	ErrUserInstallationAccess      = errors.New("user does not have access to installation")
	ErrWorkspaceNoInstallation     = errors.New("workspace has no GitHub installation")
	ErrRepositoryNotIncluded       = errors.New("repository not included in installation")
	ErrPRActionRequired            = errors.New("PR action is required")
	ErrRepoConfigNotFound          = errors.New("repository configuration not found")
	ErrWorkspaceJobsEnqueueFailed  = errors.New("failed to enqueue workspace PR jobs")
)

type User struct {
	ID                   string    `firestore:"id"`
	GitHubUsername       string    `firestore:"github_username"`
	GitHubUserID         int64     `firestore:"github_user_id"` // GitHub numeric ID
	Verified             bool      `firestore:"verified"`       // OAuth verification status
	SlackUserID          string    `firestore:"slack_user_id"`  // Slack user ID
	SlackTeamID          string    `firestore:"slack_team_id"`
	SlackDisplayName     string    `firestore:"slack_display_name"` // Slack display name for debugging
	DefaultChannel       string    `firestore:"default_channel"`
	NotificationsEnabled bool      `firestore:"notifications_enabled"`           // Whether to post PRs for this user
	TaggingEnabled       bool      `firestore:"tagging_enabled"`                 // Whether to tag user in PR messages
	ImpersonationEnabled *bool     `firestore:"impersonation_enabled,omitempty"` // Whether to post PRs appearing from the user
	CreatedAt            time.Time `firestore:"created_at"`
	UpdatedAt            time.Time `firestore:"updated_at"`
}

// GetImpersonationEnabled returns the impersonation preference, defaulting to true if not set.
func (u *User) GetImpersonationEnabled() bool {
	if u.ImpersonationEnabled == nil {
		return true // Default to enabled for backwards compatibility
	}
	return *u.ImpersonationEnabled
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

// SlackWorkspace represents a Slack workspace installation with OAuth tokens.
type SlackWorkspace struct {
	ID           string    `firestore:"id"`                      // Slack team ID (primary key)
	TeamName     string    `firestore:"team_name"`               // Workspace name
	AccessToken  string    `firestore:"access_token"`            // OAuth access token for this workspace
	Scope        string    `firestore:"scope"`                   // Granted scopes
	InstalledBy  string    `firestore:"installed_by"`            // Slack user ID who installed the app
	InstalledAt  time.Time `firestore:"installed_at"`            // Installation timestamp
	UpdatedAt    time.Time `firestore:"updated_at"`              // Last update timestamp
	AppID        string    `firestore:"app_id"`                  // Slack app ID from installation
	BotUserID    string    `firestore:"bot_user_id"`             // Bot user ID in workspace
	EnterpriseID string    `firestore:"enterprise_id,omitempty"` // Enterprise Grid ID
}

// Validate validates required fields for SlackWorkspace.
func (sw *SlackWorkspace) Validate() error {
	if sw.ID == "" {
		return ErrSlackTeamIDRequired
	}
	if sw.TeamName == "" {
		return ErrTeamNameRequired
	}
	if sw.AccessToken == "" {
		return ErrAccessTokenRequired
	}
	return nil
}

// GitHubInstallation represents a GitHub App installation.
type GitHubInstallation struct {
	ID                  int64     `firestore:"id"`                     // GitHub installation ID
	AccountLogin        string    `firestore:"account_login"`          // Organization or user login
	AccountType         string    `firestore:"account_type"`           // "Organization" or "User" (for debugging)
	AccountID           int64     `firestore:"account_id"`             // GitHub account ID
	RepositorySelection string    `firestore:"repository_selection"`   // "all" or "selected"
	Repositories        []string  `firestore:"repositories,omitempty"` // List of selected repos (if "selected")
	InstalledAt         time.Time `firestore:"installed_at"`
	UpdatedAt           time.Time `firestore:"updated_at"`

	// Workspace association fields
	SlackWorkspaceID      string `firestore:"slack_workspace_id,omitempty"`       // Slack workspace that owns this installation
	InstalledBySlackUser  string `firestore:"installed_by_slack_user,omitempty"`  // Slack user ID who installed it
	InstalledByGitHubUser int64  `firestore:"installed_by_github_user,omitempty"` // GitHub user ID who installed it

	// Fields below reserved for future implementation
	SuspendedAt *time.Time `firestore:"suspended_at,omitempty"`
	SuspendedBy string     `firestore:"suspended_by,omitempty"`
}

// Validate validates required fields for GitHubInstallation.
func (gi *GitHubInstallation) Validate() error {
	if gi.ID <= 0 {
		return ErrInstallationIDRequired
	}
	if gi.AccountLogin == "" {
		return ErrAccountLoginRequired
	}
	if gi.AccountType == "" {
		return ErrAccountTypeRequired
	}
	if gi.RepositorySelection == "" {
		return ErrRepositorySelectionRequired
	}
	if gi.RepositorySelection != "all" && gi.RepositorySelection != "selected" {
		return ErrInvalidRepositorySelection
	}
	return nil
}

// TrackedMessage represents a tracked PR message in Slack (replaces old Message model).
type TrackedMessage struct {
	ID                 string    `firestore:"id"`                             // Auto-generated document ID
	PRNumber           int       `firestore:"pr_number"`                      // GitHub PR number
	RepoFullName       string    `firestore:"repo_full_name"`                 // e.g., "owner/repo"
	SlackChannel       string    `firestore:"slack_channel"`                  // Slack channel ID
	SlackChannelName   string    `firestore:"slack_channel_name,omitempty"`   // Channel name for logging (optional)
	SlackMessageTS     string    `firestore:"slack_message_ts"`               // Slack message timestamp
	SlackTeamID        string    `firestore:"slack_team_id"`                  // Slack workspace/team ID
	MessageSource      string    `firestore:"message_source"`                 // "bot" or "manual"
	UserToCC           string    `firestore:"user_to_cc,omitempty"`           // GitHub username mentioned in CC directive
	HasReviewDirective *bool     `firestore:"has_review_directive,omitempty"` // Whether message had directive
	CreatedAt          time.Time `firestore:"created_at"`                     // When we started tracking this message
}

type Repo struct {
	ID           string    `firestore:"id"`             // {workspace_id}#{repo_full_name} (for backward compatibility)
	RepoFullName string    `firestore:"repo_full_name"` // e.g., "owner/repo" (denormalized for queries)
	WorkspaceID  string    `firestore:"workspace_id"`   // Slack team ID (denormalized for queries)
	Enabled      bool      `firestore:"enabled"`        // Used in GetReposForAllWorkspaces() query (no UI to disable yet)
	CreatedAt    time.Time `firestore:"created_at"`
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

// ReactionSyncJob represents a job to sync reactions for a PR.
type ReactionSyncJob struct {
	ID           string `json:"id"`
	PRNumber     int    `json:"pr_number"`
	RepoFullName string `json:"repo_full_name"`
	TraceID      string `json:"trace_id"`
}

// WorkspacePRJob represents a job to process PR notification for a single workspace.
type WorkspacePRJob struct {
	ID               string `json:"id"`
	PRNumber         int    `json:"pr_number"`
	RepoFullName     string `json:"repo_full_name"`
	WorkspaceID      string `json:"workspace_id"`
	PRAction         string `json:"pr_action"` // "opened", "edited", "ready_for_review", "closed"
	GitHubUserID     int64  `json:"github_user_id"`
	GitHubUsername   string `json:"github_username"`
	AnnotatedChannel string `json:"annotated_channel"` // Channel from PR description
	TraceID          string `json:"trace_id"`
	// PR payload will be stored as base64-encoded JSON to avoid nested JSON issues
	PRPayload []byte `json:"pr_payload"`
}

// Validate validates required fields for ReactionSyncJob.
func (rsj *ReactionSyncJob) Validate() error {
	if rsj.ID == "" {
		return ErrJobIDRequired
	}
	if rsj.PRNumber <= 0 {
		return ErrPRNumberRequired
	}
	if rsj.RepoFullName == "" {
		return ErrRepoFullNameRequired
	}
	if rsj.TraceID == "" {
		return ErrTraceIDRequired
	}
	return nil
}

// Validate validates required fields for WorkspacePRJob.
func (wpj *WorkspacePRJob) Validate() error {
	if wpj.ID == "" {
		return ErrJobIDRequired
	}
	if wpj.PRNumber <= 0 {
		return ErrPRNumberRequired
	}
	if wpj.RepoFullName == "" {
		return ErrRepoFullNameRequired
	}
	if wpj.WorkspaceID == "" {
		return ErrSlackTeamIDRequired
	}
	if wpj.PRAction == "" {
		return ErrPRActionRequired
	}
	if wpj.TraceID == "" {
		return ErrTraceIDRequired
	}
	if len(wpj.PRPayload) == 0 {
		return ErrPayloadRequired
	}
	return nil
}

// ReviewState represents the state of a GitHub pull request review.
type ReviewState string

// GitHub PR review states.
const (
	ReviewStateApproved         ReviewState = "approved"
	ReviewStateChangesRequested ReviewState = "changes_requested"
	ReviewStateCommented        ReviewState = "commented"
	ReviewStateDismissed        ReviewState = "dismissed"
)

// Job types for the job processing system.
const (
	JobTypeGitHubWebhook = "github_webhook"
	JobTypeManualPRLink  = "manual_pr_link"
	JobTypeReactionSync  = "reaction_sync"
	JobTypeWorkspacePR   = "workspace_pr"
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
