package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// EmojiConfig holds Slack emoji configuration for different PR states.
type EmojiConfig struct {
	Approved         string
	ChangesRequested string
	Commented        string
	Merged           string
	Closed           string
}

// Config holds all application configuration.
type Config struct {
	// Core settings
	FirestoreProjectID  string
	FirestoreDatabaseID string
	GitHubWebhookSecret string
	SlackSigningSecret  string

	// Slack OAuth settings (required)
	SlackClientID     string
	SlackClientSecret string

	// GitHub OAuth settings
	GitHubClientID     string
	GitHubClientSecret string
	GitHubAppToken     string // GitHub App installation token for API access

	// Cloud Tasks settings
	GoogleCloudProject string
	BaseURL            string
	GCPRegion          string
	CloudTasksQueue    string
	CloudTasksSecret   string

	// Cloud Tasks retry configuration
	CloudTasksMaxAttempts int32

	// Server settings
	Port                  string
	GinMode               string
	LogLevel              string
	ServerReadTimeout     time.Duration
	ServerWriteTimeout    time.Duration
	ServerShutdownTimeout time.Duration

	// Processing settings
	WebhookProcessingTimeout time.Duration

	// Emoji settings
	Emoji EmojiConfig
}

// JobProcessorURL returns the full URL for the job processor endpoint.
func (c *Config) JobProcessorURL() string {
	return c.BaseURL + "/jobs/process"
}

// SlackRedirectURL returns the full URL for the Slack OAuth callback endpoint.
func (c *Config) SlackRedirectURL() string {
	return c.BaseURL + "/auth/slack/callback"
}

// GitHubOAuthRedirectURL returns the full URL for the GitHub OAuth callback endpoint.
func (c *Config) GitHubOAuthRedirectURL() string {
	return c.BaseURL + "/auth/github/callback"
}

// IsSlackOAuthEnabled returns true since Slack OAuth is now always enabled.
func (c *Config) IsSlackOAuthEnabled() bool {
	return true
}

// Load reads configuration from environment variables.
// Panics if any required configuration is missing or invalid.
func Load() *Config {
	cfg := &Config{
		// Core settings (required)
		FirestoreProjectID:  getEnvRequired("FIRESTORE_PROJECT_ID"),
		FirestoreDatabaseID: getEnvRequired("FIRESTORE_DATABASE_ID"),
		GitHubWebhookSecret: getEnvRequired("GITHUB_WEBHOOK_SECRET"),
		SlackSigningSecret:  getEnvRequired("SLACK_SIGNING_SECRET"),

		// Slack OAuth settings (required)
		SlackClientID:     getEnvRequired("SLACK_CLIENT_ID"),
		SlackClientSecret: getEnvRequired("SLACK_CLIENT_SECRET"),

		// GitHub OAuth settings (required)
		GitHubClientID:     getEnvRequired("GITHUB_CLIENT_ID"),
		GitHubClientSecret: getEnvRequired("GITHUB_CLIENT_SECRET"),
		GitHubAppToken:     getEnvRequired("GITHUB_APP_TOKEN"),

		// Cloud Tasks settings
		GoogleCloudProject: getEnvRequired("GOOGLE_CLOUD_PROJECT"),
		BaseURL:            getEnvRequired("BASE_URL"),
		GCPRegion:          getEnvDefault("GCP_REGION", "europe-west1"),
		CloudTasksQueue:    getEnvDefault("CLOUD_TASKS_QUEUE", "webhook-processing"),
		CloudTasksSecret:   getEnvRequired("CLOUD_TASKS_SECRET"),

		// Server settings
		Port:     getEnvDefault("PORT", "8080"),
		GinMode:  getEnvDefault("GIN_MODE", "release"),
		LogLevel: getEnvDefault("LOG_LEVEL", "info"),
	}

	// Parse duration values
	cfg.ServerReadTimeout = getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second)
	cfg.ServerWriteTimeout = getEnvDuration("SERVER_WRITE_TIMEOUT", 30*time.Second)
	cfg.ServerShutdownTimeout = getEnvDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second)
	cfg.WebhookProcessingTimeout = getEnvDuration("WEBHOOK_PROCESSING_TIMEOUT", 5*time.Minute)

	// Parse Cloud Tasks retry configuration
	cfg.CloudTasksMaxAttempts = getEnvInt32("CLOUD_TASKS_MAX_ATTEMPTS", 100)

	// Parse emoji configuration
	cfg.Emoji = EmojiConfig{
		Approved:         getEnvDefault("EMOJI_APPROVED", "approved_gh"),
		ChangesRequested: getEnvDefault("EMOJI_CHANGES_REQUESTED", "question"),
		Commented:        getEnvDefault("EMOJI_COMMENTED", "speech_balloon"),
		Merged:           getEnvDefault("EMOJI_MERGED", "merged"),
		Closed:           getEnvDefault("EMOJI_CLOSED", "pr-closed"),
	}

	// Validate configuration
	cfg.validate()

	return cfg
}

// validate checks that all required configuration is present and valid.
// Panics if any validation fails.
func (c *Config) validate() {
	c.validateRequiredFields()
	c.validateGinMode()
	c.validateLogLevel()
	c.validateTimeouts()
	c.validateCloudTasksRetryConfig()
}

// validateRequiredFields checks that all required fields are set.
func (c *Config) validateRequiredFields() {
	required := map[string]string{
		"FIRESTORE_PROJECT_ID":  c.FirestoreProjectID,
		"FIRESTORE_DATABASE_ID": c.FirestoreDatabaseID,
		"GITHUB_WEBHOOK_SECRET": c.GitHubWebhookSecret,
		"SLACK_SIGNING_SECRET":  c.SlackSigningSecret,
		"SLACK_CLIENT_ID":       c.SlackClientID,
		"SLACK_CLIENT_SECRET":   c.SlackClientSecret,
		"GITHUB_CLIENT_ID":      c.GitHubClientID,
		"GITHUB_CLIENT_SECRET":  c.GitHubClientSecret,
		"GITHUB_APP_TOKEN":      c.GitHubAppToken,
		"GOOGLE_CLOUD_PROJECT":  c.GoogleCloudProject,
		"BASE_URL":              c.BaseURL,
		"CLOUD_TASKS_SECRET":    c.CloudTasksSecret,
	}

	for name, value := range required {
		if value == "" {
			panic(fmt.Sprintf("required environment variable %s is not set", name))
		}
	}

	// Slack OAuth is now required - validation happens in the required fields check above
}

// validateGinMode validates the GIN_MODE setting.
func (c *Config) validateGinMode() {
	if c.GinMode != "debug" && c.GinMode != "release" && c.GinMode != "test" {
		panic(fmt.Sprintf("invalid GIN_MODE: %s (must be debug, release, or test)", c.GinMode))
	}
}

// validateLogLevel validates the LOG_LEVEL setting.
func (c *Config) validateLogLevel() {
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		panic(fmt.Sprintf("invalid LOG_LEVEL: %s (must be debug, info, warn, or error)", c.LogLevel))
	}
}

// validateTimeouts validates timeout settings.
func (c *Config) validateTimeouts() {
	if c.ServerReadTimeout <= 0 {
		panic("SERVER_READ_TIMEOUT must be positive")
	}
	if c.ServerWriteTimeout <= 0 {
		panic("SERVER_WRITE_TIMEOUT must be positive")
	}
	if c.ServerShutdownTimeout <= 0 {
		panic("SERVER_SHUTDOWN_TIMEOUT must be positive")
	}
	if c.WebhookProcessingTimeout <= 0 {
		panic("WEBHOOK_PROCESSING_TIMEOUT must be positive")
	}
}

// validateCloudTasksRetryConfig validates Cloud Tasks retry configuration.
func (c *Config) validateCloudTasksRetryConfig() {
	if c.CloudTasksMaxAttempts < 1 {
		panic("CLOUD_TASKS_MAX_ATTEMPTS must be at least 1")
	}
}

// getEnvRequired gets an environment variable or returns empty string if not set.
// The validate() function will panic if required values are missing.
func getEnvRequired(key string) string {
	return os.Getenv(key)
}

// getEnvDefault gets an environment variable with a default value.
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvDuration gets a duration environment variable with a default value.
// Panics if the value cannot be parsed as a duration.
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		panic(fmt.Sprintf("invalid duration value for %s: %s", key, value))
	}
	return d
}

// getEnvInt32 gets an int32 environment variable with a default value.
// Panics if the value cannot be parsed as an int32.
func getEnvInt32(key string, defaultValue int32) int32 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	i, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		panic(fmt.Sprintf("invalid int32 value for %s: %s", key, value))
	}
	return int32(i)
}
