package models

import "time"

type User struct {
	ID             string    `firestore:"id"`
	GitHubUsername string    `firestore:"github_username"`
	SlackUserID    string    `firestore:"slack_user_id"`
	SlackTeamID    string    `firestore:"slack_team_id"`
	DefaultChannel string    `firestore:"default_channel"`
	CreatedAt      time.Time `firestore:"created_at"`
	UpdatedAt      time.Time `firestore:"updated_at"`
}

type Message struct {
	ID             string    `firestore:"id"`
	PRNumber       int       `firestore:"pr_number"`
	RepoFullName   string    `firestore:"repo_full_name"`
	SlackChannel   string    `firestore:"slack_channel"`
	SlackMessageTS string    `firestore:"slack_message_ts"`
	GitHubPRURL    string    `firestore:"github_pr_url"`
	AuthorGitHubID string    `firestore:"author_github_id"`
	CreatedAt      time.Time `firestore:"created_at"`
	LastStatus     string    `firestore:"last_status"`
}

type Repo struct {
	ID             string    `firestore:"id"`
	DefaultChannel string    `firestore:"default_channel"`
	WebhookSecret  string    `firestore:"webhook_secret"`
	Enabled        bool      `firestore:"enabled"`
	CreatedAt      time.Time `firestore:"created_at"`
}
