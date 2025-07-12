# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based GitHub-Slack notifier that sends Slack notifications for GitHub pull request events. It uses **async processing by default** via Google Cloud Tasks for high reliability, processing PR opens, reviews, and closures via webhooks, storing state in Cloud Firestore and sending notifications to Slack channels.

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
- **internal/handlers/**: HTTP handlers for async GitHub webhooks (`github_async.go`), webhook workers (`webhook_worker.go`), and Slack webhooks (`slack.go`)
- **internal/services/**: Business logic layer with `FirestoreService`, `SlackService`, `CloudTasksService`, and `ValidationService`
- **internal/models/**: Data structures for `User`, `Message`, `Repo`, and `WebhookJob` entities
- **internal/middleware/**: HTTP middleware including structured logging with trace IDs
- **internal/log/**: Custom logging utilities with context support

### Async Processing Architecture (Default)

**Fast Path (< 100ms):**
1. **GitHub Webhook** → `handlers/github_async.go` → validates signature & payload → creates `WebhookJob` → queues to Cloud Tasks → returns 200

**Slow Path (reliable, retryable):**
2. **Cloud Tasks** → `handlers/webhook_worker.go` → processes business logic → updates Firestore → sends Slack notifications

**Legacy Sync Path** (when `ENABLE_ASYNC_PROCESSING=false`):
1. **GitHub Webhook** → `handlers/github.go` → validates webhook signature → processes PR events directly

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

**Async Processing (Default Mode):**
- `ENABLE_ASYNC_PROCESSING`: Set to `true` (default) for async mode, `false` for legacy sync mode
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

1. Create a new Slack app at https://api.slack.com/apps
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

**Async Mode (Default):**
- `POST /webhooks/github`: GitHub webhook fast ingress (queues to Cloud Tasks)
- `POST /process-webhook`: Internal webhook worker endpoint (called by Cloud Tasks)
- `POST /webhooks/slack`: Slack slash command processor
- `POST /api/repos`: Repository registration (admin only)
- `GET /health`: Health check endpoint

**Legacy Sync Mode** (when `ENABLE_ASYNC_PROCESSING=false`):
- `POST /webhooks/github`: GitHub webhook direct processor (legacy sync mode)
- `POST /webhooks/slack`: Slack slash command processor
- `POST /api/repos`: Repository registration (admin only)
- `GET /health`: Health check endpoint

**Note:** The `/process-webhook` endpoint should not be exposed publicly - it's designed to be called only by Google Cloud Tasks with proper authentication.