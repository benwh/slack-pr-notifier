package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Configuration validation errors.
var (
	ErrRequiredEnvVar           = errors.New("required environment variable not set")
	ErrInvalidGinMode           = errors.New("invalid GIN_MODE")
	ErrInvalidLogLevel          = errors.New("invalid LOG_LEVEL")
	ErrInvalidReadTimeout       = errors.New("SERVER_READ_TIMEOUT must be positive")
	ErrInvalidWriteTimeout      = errors.New("SERVER_WRITE_TIMEOUT must be positive")
	ErrInvalidShutdownTimeout   = errors.New("SERVER_SHUTDOWN_TIMEOUT must be positive")
	ErrInvalidProcessingTimeout = errors.New("WEBHOOK_PROCESSING_TIMEOUT must be positive")
	ErrInvalidTimestampMaxAge   = errors.New("SLACK_TIMESTAMP_MAX_AGE must be positive")
	ErrInvalidBool              = errors.New("invalid boolean value")
	ErrInvalidDuration          = errors.New("invalid duration value")
)

// Config holds all application configuration.
type Config struct {
	// Core settings
	FirestoreProjectID    string
	FirestoreDatabaseID   string
	SlackBotToken         string
	GitHubWebhookSecret   string
	SlackSigningSecret    string
	APIAdminKey           string
	EnableAsyncProcessing bool

	// Cloud Tasks settings
	GoogleCloudProject string
	WebhookWorkerURL   string
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
	SlackTimestampMaxAge     time.Duration
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var err error
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
		WebhookWorkerURL:   getEnvRequired("WEBHOOK_WORKER_URL"),
		GCPRegion:          getEnvDefault("GCP_REGION", "europe-west1"),
		CloudTasksQueue:    getEnvDefault("CLOUD_TASKS_QUEUE", "webhook-processing"),

		// Server settings
		Port:     getEnvDefault("PORT", "8080"),
		GinMode:  getEnvDefault("GIN_MODE", "debug"),
		LogLevel: getEnvDefault("LOG_LEVEL", "info"),
	}

	// Parse boolean values
	cfg.EnableAsyncProcessing, err = getEnvBool("ENABLE_ASYNC_PROCESSING", true)
	if err != nil {
		return nil, err
	}

	// Parse duration values
	cfg.ServerReadTimeout, err = getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second)
	if err != nil {
		return nil, err
	}

	cfg.ServerWriteTimeout, err = getEnvDuration("SERVER_WRITE_TIMEOUT", 30*time.Second)
	if err != nil {
		return nil, err
	}

	cfg.ServerShutdownTimeout, err = getEnvDuration("SERVER_SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return nil, err
	}

	cfg.WebhookProcessingTimeout, err = getEnvDuration("WEBHOOK_PROCESSING_TIMEOUT", 5*time.Minute)
	if err != nil {
		return nil, err
	}

	cfg.SlackTimestampMaxAge, err = getEnvDuration("SLACK_TIMESTAMP_MAX_AGE", 5*time.Minute)
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// validate checks that all required configuration is present and valid.
func (c *Config) validate() error {
	// Check required fields
	required := map[string]string{
		"FIRESTORE_PROJECT_ID":  c.FirestoreProjectID,
		"FIRESTORE_DATABASE_ID": c.FirestoreDatabaseID,
		"SLACK_BOT_TOKEN":       c.SlackBotToken,
		"GITHUB_WEBHOOK_SECRET": c.GitHubWebhookSecret,
		"SLACK_SIGNING_SECRET":  c.SlackSigningSecret,
		"API_ADMIN_KEY":         c.APIAdminKey,
		"GOOGLE_CLOUD_PROJECT":  c.GoogleCloudProject,
		"WEBHOOK_WORKER_URL":    c.WebhookWorkerURL,
	}

	for name, value := range required {
		if value == "" {
			return fmt.Errorf("%w: %s", ErrRequiredEnvVar, name)
		}
	}

	// Validate GIN_MODE
	if c.GinMode != "debug" && c.GinMode != "release" && c.GinMode != "test" {
		return fmt.Errorf("%w: %s (must be debug, release, or test)", ErrInvalidGinMode, c.GinMode)
	}

	// Validate log level
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return fmt.Errorf("%w: %s (must be debug, info, warn, or error)", ErrInvalidLogLevel, c.LogLevel)
	}

	// Validate timeouts
	if c.ServerReadTimeout <= 0 {
		return ErrInvalidReadTimeout
	}
	if c.ServerWriteTimeout <= 0 {
		return ErrInvalidWriteTimeout
	}
	if c.ServerShutdownTimeout <= 0 {
		return ErrInvalidShutdownTimeout
	}
	if c.WebhookProcessingTimeout <= 0 {
		return ErrInvalidProcessingTimeout
	}
	if c.SlackTimestampMaxAge <= 0 {
		return ErrInvalidTimestampMaxAge
	}

	return nil
}

// getEnvRequired gets an environment variable or panics if not set.
func getEnvRequired(key string) string {
	value := os.Getenv(key)
	if value == "" {
		// Don't panic here, let validate() handle the error
		return ""
	}
	return value
}

// getEnvDefault gets an environment variable with a default value.
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool gets a boolean environment variable with a default value.
func getEnvBool(key string, defaultValue bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%w for %s: %s", ErrInvalidBool, key, value)
	}
	return b, nil
}

// getEnvDuration gets a duration environment variable with a default value.
func getEnvDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w for %s: %s", ErrInvalidDuration, key, value)
	}
	return d, nil
}
