# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based GitHub-Slack notifier that sends Slack notifications for GitHub pull request events. It processes PR opens, reviews, and closures via webhooks, storing state in Cloud Firestore and sending notifications to Slack channels.

## Development Commands

### Core Development
```bash
# Start local development with ngrok tunnel
./scripts/dev.sh

# Build the application
go build -o github-slack-notifier

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

- **main.go**: Application entry point with HTTP server setup, graceful shutdown, and dependency injection
- **handlers/**: HTTP handlers for GitHub webhooks (`github.go`) and Slack webhooks (`slack.go`)
- **services/**: Business logic layer with `FirestoreService` for database operations and `SlackService` for Slack API interactions
- **models/**: Data structures for `User`, `Message`, and `Repo` entities
- **middleware/**: HTTP middleware including structured logging

### Data Flow

1. **GitHub Webhook** → `handlers/github.go` → validates webhook signature → processes PR events
2. **Slack Commands** → `handlers/slack.go` → validates signing secret → processes user configuration
3. **Services Layer** → `services/firestore.go` for data persistence, `services/slack.go` for messaging
4. **Models** → Firestore documents with struct tags for serialization

### Key Dependencies

- **Web Framework**: `github.com/gin-gonic/gin` for HTTP routing
- **Database**: `cloud.google.com/go/firestore` for Cloud Firestore integration
- **Slack API**: `github.com/slack-go/slack` for Slack Bot API
- **Logging**: Built-in `log/slog` for structured logging

## Environment Configuration

Required environment variables (configured in `.env` file):
- `FIRESTORE_PROJECT_ID`: GCP project ID
- `FIRESTORE_DATABASE_ID`: Firestore database ID
- `SLACK_BOT_TOKEN`: Slack bot token (xoxb-)
- `GITHUB_WEBHOOK_SECRET`: GitHub webhook secret for signature validation
- `SLACK_SIGNING_SECRET`: Slack signing secret for request validation
- `API_ADMIN_KEY`: Admin API key for repository registration

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
go test ./handlers

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

- `POST /webhooks/github`: GitHub webhook processor
- `POST /webhooks/slack`: Slack slash command processor
- `POST /api/repos`: Repository registration (admin only)
- `GET /health`: Health check endpoint