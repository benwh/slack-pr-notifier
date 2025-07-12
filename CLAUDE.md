# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based GitHub-Slack notifier that sends Slack notifications for GitHub pull request events. It uses **async processing** via Google Cloud Tasks for high reliability, processing PR opens, reviews, and closures via webhooks, storing state in Cloud Firestore and sending notifications to Slack channels.

## Development Commands

### Core Development

```bash
# Start local development with ngrok tunnel
./scripts/dev.sh

# Build the application
go build -o github-slack-notifier ./cmd/github-slack-notifier

# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...
```

### Linting and Code Quality

```bash
# Run all linters (preferred)
./scripts/lint.sh

# Individual linting tools
golangci-lint run ./...
go fmt ./...
go vet ./...
staticcheck ./...
```

### Deployment

```bash
# Deploy to Cloud Run
./scripts/deploy.sh

# Setup GCP infrastructure
./scripts/setup-infrastructure.sh
```

## Architecture

### Core Components

- **cmd/github-slack-notifier/main.go**: Application entry point with HTTP server setup, graceful shutdown, and dependency injection
- **internal/handlers/**: HTTP handlers for GitHub webhooks (`github.go`), webhook workers (`webhook_worker.go`), and Slack webhooks (`slack.go`)
- **internal/services/**: Business logic layer with `FirestoreService`, `SlackService`, and `CloudTasksService`
- **internal/models/**: Data structures for `User`, `Message`, `Repo`, and `WebhookJob` entities
- **internal/middleware/**: HTTP middleware including structured logging with trace IDs
- **internal/log/**: Custom logging utilities with context support

### Architecture Guidelines

#### What is a Service? (`internal/services/`)

A **Service** encapsulates business logic and external integrations. Services should be created when:

- **Managing state or connections**: Database connections, API clients, connection pools
- **Complex business logic**: Operations that coordinate multiple steps or handle complex workflows
- **External system integration**: Slack API, Firestore, Cloud Tasks, GitHub API
- **Dependency injection needed**: When the component needs configuration or other services injected
- **Shared functionality**: Logic used by multiple handlers or other services

**Examples of good services:**

- `SlackService`: Manages Slack API client and all Slack operations
- `FirestoreService`: Handles database connections and CRUD operations
- `CloudTasksService`: Manages task queue client and job creation

**What should NOT be a service:**

- Simple utility functions (e.g., string parsing, regex matching)
- Pure functions without state or dependencies
- Single-use helper functions

#### What is a Handler? (`internal/handlers/`)

A **Handler** is responsible for HTTP request/response processing. Handlers should:

- **Parse and validate** incoming HTTP requests
- **Call services** to perform business logic
- **Format responses** and handle HTTP status codes
- **Handle errors** and return appropriate error responses
- **Add observability** (logging, metrics, tracing)

**Handler responsibilities:**

- Request parsing and validation
- Authentication/authorization checks
- Calling appropriate service methods
- Response formatting
- Error handling and status codes

**Handlers should NOT:**

- Contain business logic (use services)
- Make direct database or API calls (use services)
- Share state between requests

#### What is a Model? (`internal/models/`)

A **Model** represents data structures used throughout the application. Models should:

- **Define data structures** with appropriate types and tags
- **Document fields** with clear comments
- **Use appropriate tags** for JSON, Firestore, validation
- **Be simple DTOs** (Data Transfer Objects) without methods containing business logic

**Examples of good models:**

```go
type User struct {
    GitHubUsername string `firestore:"github_username" json:"github_username"`
    SlackUserID    string `firestore:"slack_user_id" json:"slack_user_id"`
    DefaultChannel string `firestore:"default_channel,omitempty" json:"default_channel,omitempty"`
}
```

**Models should NOT:**

- Contain business logic methods
- Make database or API calls
- Have complex initialization logic

#### When to Create Utils (`internal/lib/something`)

Utility functions are appropriate for:

- Pure functions without state
- Simple transformations or parsing
- Shared helper functions
- Functions that don't fit the service pattern

**Examples:**

- `ExtractPRLinks(text string) []PRLink` - Simple regex parsing
- `ValidateWebhookSignature(payload, secret, signature) bool`
- `FormatSlackMessage(pr PullRequest) string`

### Processing Architecture

**Fast Path (< 100ms):**

1. **GitHub Webhook** → `handlers/github.go` → validates signature & payload → creates `WebhookJob` → queues to Cloud Tasks → returns 200

**Slow Path (reliable, retryable):**
2. **Cloud Tasks** → `handlers/webhook_worker.go` → processes business logic → updates Firestore → sends Slack notifications

### Data Flow

1. **GitHub Webhook** → Fast ingress handler → Cloud Tasks queue → Worker handler → Slack/Firestore
2. **Slack Commands** → `handlers/slack.go` → validates signing secret → processes user configuration
3. **Services Layer** → `services/firestore.go` for persistence, `services/slack.go` for messaging, `services/cloud_tasks.go` for queuing
4. **Models** → Firestore documents and Cloud Tasks job payloads with struct tags

### Key Dependencies

- **Web Framework**: `github.com/gin-gonic/gin` for HTTP routing
- **Database**: `cloud.google.com/go/firestore` for Cloud Firestore integration
- **Queue**: `cloud.google.com/go/cloudtasks` for Google Cloud Tasks integration
- **Slack API**: `github.com/slack-go/slack` for Slack Bot API
- **Logging**: Built-in `log/slog` for structured logging with trace IDs
- **IDs**: `github.com/google/uuid` for generating job and trace IDs

## Environment Configuration

Required environment variables (configured in `.env` file):

**Core Configuration:**

- `FIRESTORE_PROJECT_ID`: GCP project ID
- `FIRESTORE_DATABASE_ID`: Firestore database ID
- `SLACK_BOT_TOKEN`: Slack bot token (xoxb-)
- `GITHUB_WEBHOOK_SECRET`: GitHub webhook secret for signature validation
- `SLACK_SIGNING_SECRET`: Slack signing secret for request validation
- `API_ADMIN_KEY`: Admin API key for repository registration

**Async Processing:**

- `GOOGLE_CLOUD_PROJECT`: GCP project ID for Cloud Tasks
- `CLOUD_TASKS_QUEUE`: Queue name (defaults to `webhook-processing`)
- `WEBHOOK_WORKER_URL`: Full URL to the `/process-webhook` endpoint of your deployed service

### Slack App Configuration

The Slack app requires the following OAuth scopes:

- `channels:read`: Validate channel access for `/notify-channel` command
- `chat:write`: Send PR notifications to channels
- `commands`: Handle slash commands

## Database Schema

### Firestore Collections

- **users**: User configuration with GitHub username, Slack user ID, and default channel
- **messages**: PR notification tracking with Slack message timestamps and GitHub PR URLs
- **repos**: Repository configuration with default channels and webhook secrets

## Testing Strategy

Use Go's built-in testing framework:

```bash
# Run all tests
go test ./...

# Test specific package
go test ./internal/handlers

# Test with verbose output
go test -v ./...
```

## Common Development Patterns

### Error Handling

- Use structured logging with `slog` for error reporting
- Return sentinel errors from services (e.g., `ErrUserNotFound`)
- Validate webhook signatures before processing

### Security

- Always validate GitHub webhook signatures using HMAC-SHA256
- Validate Slack request signatures using signing secret
- Use admin API key for repository registration endpoint

### Local Development

- Use `./scripts/dev.sh` which sets up ngrok tunneling automatically
- Environment variables loaded from `.env` file
- Hot reload requires manual restart

## Deployment Architecture

- **Platform**: Google Cloud Run (serverless containers)
- **Database**: Cloud Firestore (NoSQL document database)
- **Queue**: Google Cloud Tasks (managed queue service)
- **Registry**: Google Artifact Registry
- **Region**: Configurable via `GCP_REGION` (defaults to europe-west1)

### Slack App Setup

1. Create a new Slack app at <https://api.slack.com/apps>
2. Configure OAuth scopes under "OAuth & Permissions":
   - `channels:read`
   - `chat:write`
   - `commands`
3. Enable slash commands under "Slash Commands":
   - `/notify-channel`
   - `/notify-link`
   - `/notify-status`
4. Set request URL to your deployed Cloud Run service: `https://your-service-url/webhooks/slack`
5. Install the app to your workspace to generate the bot token

## Webhook Endpoints

- `POST /webhooks/github`: GitHub webhook fast ingress (queues to Cloud Tasks)
- `POST /process-webhook`: Internal webhook worker endpoint (called by Cloud Tasks)
- `POST /webhooks/slack`: Slack slash command processor
- `POST /api/repos`: Repository registration (admin only)
- `GET /health`: Health check endpoint

**Note:** The `/process-webhook` endpoint should not be exposed publicly - it's designed to be called only by Google Cloud Tasks with proper authentication.

