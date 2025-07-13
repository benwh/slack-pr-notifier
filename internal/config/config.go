package config

import (
	"fmt"
	"os"
	"time"
)

// EmojiConfig holds Slack emoji configuration for different PR states.
type EmojiConfig struct {
	Approved         string
	ChangesRequested string
	Commented        string
	Merged           string
	Closed           string
	Dismissed        string
}

// Config holds all application configuration.
type Config struct {
	// Core settings
	FirestoreProjectID  string
	FirestoreDatabaseID string
	SlackBotToken       string
	GitHubWebhookSecret string
	SlackSigningSecret  string
	APIAdminKey         string

	// Cloud Tasks settings
	GoogleCloudProject string
	BaseURL            string
	GCPRegion          string
	CloudTasksQueue    string

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

// WebhookWorkerURL returns the full URL for the webhook worker endpoint.
func (c *Config) WebhookWorkerURL() string {
	return c.BaseURL + "/process-webhook"
}

// ManualLinkWorkerURL returns the full URL for the manual link worker endpoint.
func (c *Config) ManualLinkWorkerURL() string {
	return c.BaseURL + "/process-manual-link"
}

// Load reads configuration from environment variables.
// Panics if any required configuration is missing or invalid.
func Load() *Config {
	cfg := &Config{
		// Core settings (required)
		FirestoreProjectID:  getEnvRequired("FIRESTORE_PROJECT_ID"),
		FirestoreDatabaseID: getEnvRequired("FIRESTORE_DATABASE_ID"),
		SlackBotToken:       getEnvRequired("SLACK_BOT_TOKEN"),
		GitHubWebhookSecret: getEnvRequired("GITHUB_WEBHOOK_SECRET"),
		SlackSigningSecret:  getEnvRequired("SLACK_SIGNING_SECRET"),
		APIAdminKey:         getEnvRequired("API_ADMIN_KEY"),

		// Cloud Tasks settings
		GoogleCloudProject: getEnvRequired("GOOGLE_CLOUD_PROJECT"),
		BaseURL:            getEnvRequired("BASE_URL"),
		GCPRegion:          getEnvDefault("GCP_REGION", "europe-west1"),
		CloudTasksQueue:    getEnvDefault("CLOUD_TASKS_QUEUE", "webhook-processing"),

		// Server settings
		Port:     getEnvDefault("PORT", "8080"),
		GinMode:  getEnvDefault("GIN_MODE", "debug"),
		LogLevel: getEnvDefault("LOG_LEVEL", "info"),
	}

	// Parse duration values
	cfg.ServerReadTimeout = getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second)
	cfg.ServerWriteTimeout = getEnvDuration("SERVER_WRITE_TIMEOUT", 30*time.Second)
	cfg.ServerShutdownTimeout = getEnvDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second)
	cfg.WebhookProcessingTimeout = getEnvDuration("WEBHOOK_PROCESSING_TIMEOUT", 5*time.Minute)

	// Parse emoji configuration
	cfg.Emoji = EmojiConfig{
		Approved:         getEnvDefault("EMOJI_APPROVED", "white_check_mark"),
		ChangesRequested: getEnvDefault("EMOJI_CHANGES_REQUESTED", "arrows_counterclockwise"),
		Commented:        getEnvDefault("EMOJI_COMMENTED", "speech_balloon"),
		Merged:           getEnvDefault("EMOJI_MERGED", "tada"),
		Closed:           getEnvDefault("EMOJI_CLOSED", "x"),
		Dismissed:        getEnvDefault("EMOJI_DISMISSED", "wave"),
	}

	// Validate configuration
	cfg.validate()

	return cfg
}

// validate checks that all required configuration is present and valid.
// Panics if any validation fails.
func (c *Config) validate() {
	// Check required fields
	required := map[string]string{
		"FIRESTORE_PROJECT_ID":  c.FirestoreProjectID,
		"FIRESTORE_DATABASE_ID": c.FirestoreDatabaseID,
		"SLACK_BOT_TOKEN":       c.SlackBotToken,
		"GITHUB_WEBHOOK_SECRET": c.GitHubWebhookSecret,
		"SLACK_SIGNING_SECRET":  c.SlackSigningSecret,
		"API_ADMIN_KEY":         c.APIAdminKey,
		"GOOGLE_CLOUD_PROJECT":  c.GoogleCloudProject,
		"BASE_URL":              c.BaseURL,
	}

	for name, value := range required {
		if value == "" {
			panic(fmt.Sprintf("required environment variable %s is not set", name))
		}
	}

	// Validate GIN_MODE
	if c.GinMode != "debug" && c.GinMode != "release" && c.GinMode != "test" {
		panic(fmt.Sprintf("invalid GIN_MODE: %s (must be debug, release, or test)", c.GinMode))
	}

	// Validate log level
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		panic(fmt.Sprintf("invalid LOG_LEVEL: %s (must be debug, info, warn, or error)", c.LogLevel))
	}

	// Validate timeouts
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
