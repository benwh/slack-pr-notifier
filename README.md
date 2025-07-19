# GitHub-Slack Notifier

A Go application that sends Slack notifications for GitHub pull request events with high-reliability async processing.

## Features

- 🔗 **PR Notifications**: Sends Slack messages when PRs are opened
- 📝 **Review Updates**: Automatically syncs emoji reactions for PR reviews (approved ✅, changes requested 🔄, comments 💬, dismissed 👋)
- 🎉 **Closure Updates**: Adds emoji reactions when PRs are merged or closed
- 🔐 **Secure OAuth Authentication**: Users link GitHub accounts via OAuth (no more username trust)
- ⚙️ **Slack Configuration**: Use slash commands to configure your settings
- 🚀 **Async Processing**: Uses Google Cloud Tasks for reliable webhook processing with automatic retries
- 📊 **Observability**: Structured logging with trace IDs for full request tracking

## Quick Start

### Prerequisites

- [Go 1.23+](https://golang.org/dl/)
- [Docker](https://docs.docker.com/get-docker/)
- [gcloud CLI](https://cloud.google.com/sdk/docs/install)
- [ngrok](https://ngrok.com/) (for local development)

### Initial Setup

1. **Clone and setup infrastructure**:

   ```bash
   cd github-slack-notifier
   ./scripts/setup-infrastructure.sh
   ```

2. **Configure environment**:

   ```bash
   cp .env.example .env
   # Now edit .env with your configuration
   ```

### Local Development

```bash
# Run with ngrok tunnel
./scripts/dev.sh
```

### Deploy to Production

```bash
./scripts/deploy.sh
```

## Architecture

### Async Webhook Processing

The application uses **async processing by default** for high reliability:

- **Fast Response**: GitHub webhooks are acknowledged within ~100ms
- **Reliable Processing**: Uses Google Cloud Tasks for guaranteed processing with automatic retries
- **Error Handling**: Distinguishes between temporary and permanent errors for smart retry logic
- **Observability**: Full trace ID tracking from webhook receipt to completion

```
GitHub Webhook → Fast Ingress → Cloud Tasks Queue → Worker Processing → Slack/Firestore
     (100ms)        (immediate)      (reliable)        (retryable)
```

### Processing Architecture

The application uses **async processing** via Google Cloud Tasks for reliable webhook processing:

- Uses Cloud Tasks for reliable processing
- Handles GitHub's 10-second timeout requirement
- Automatic retries for transient failures

## Configuration

See the [Configuration Guide](docs/reference/CONFIGURATION.md) for detailed setup instructions.

**Quick Start:**
```bash
cp .env.example .env
# Edit .env with your configuration
```

### GitHub App Setup

1. **Create GitHub App**:
   - Go to <https://github.com/settings/apps/new>
   - Name (make it unique): Slack PR notifier
   - Homepage URL: <https://example.com>
   - Webhook URL: Retrieve from dev.sh output
   - Secret: Use `pwgen -s 32 1`
   - Enable permissions: Pull requests (Read Only)
   - Subscribe to events: Pull requests, Pull request reviews

2. **Install GitHub App**:
   - Install the app on your repositories

### Slack App Setup

**📋 Easy Setup with Manifest (Recommended)**

1. **Create App from Manifest**:
   - Go to [Slack API](https://api.slack.com/apps)
   - Click "Create New App" → "From an app manifest"
   - Choose your workspace and paste contents of `slack-app-manifest.yaml`
   - See [detailed setup guide](docs/reference/SLACK_APP_SETUP.md) for complete instructions

2. **Configure OAuth Scopes**:
   - Go to OAuth & Permissions in the app sidebar
   - Ensure these scopes are added:
     - `channels:read` - Validate channel access
     - `chat:write` - Send PR notifications
     - `commands` - Handle slash commands
     - `reactions:write` - Add emoji reactions to PRs

3. **Get Bot Token**:
   - Install app to workspace (or reinstall if adding new scopes)
   - Copy Bot User OAuth Token from OAuth & Permissions

**🔧 Manual Setup (Alternative)**

<details>
<summary>Click to expand manual setup instructions</summary>

1. **Create Slack App**:
   - Go to [Slack API](https://api.slack.com/apps)
   - Click "Create New App" → "From scratch"
   - Choose your workspace

2. **Configure Bot Token**:
   - Go to OAuth & Permissions in the app sidebar
   - Add scopes: `chat:write`, `reactions:write`, `channels:read`, `commands`
   - Install app to workspace
   - Copy Bot User OAuth Token

3. **Add Slash Commands**:
   - Go to Slash Commands
   - Add each command with request URL `https://your-domain/webhooks/slack`, and tick
   'Escape ... sent to your app':
     - `/notify-channel` - Set default notification channel
     - `/notify-link` - Link GitHub account
     - `/notify-status` - View current settings

4. **Configure Signing Secret**:
   - Go to Basic Information
   - Copy Signing Secret

</details>

### Post-Deployment Configuration

1. **Set environment variables in Cloud Run**:

   ```bash
   gcloud run services update github-slack-notifier \
     --region=europe-west1 \
     --project=your-project-id \
     --set-env-vars="SLACK_BOT_TOKEN=xoxb-...,GITHUB_WEBHOOK_SECRET=...,SLACK_SIGNING_SECRET=...,API_ADMIN_KEY=..."
   ```

2. **Register repositories** (optional - repos can use default channels):

   ```bash
   curl -X POST https://your-domain/api/repos \
     -H "X-API-Key: your-admin-api-key" \
     -H "Content-Type: application/json" \
     -d '{
       "repo_full_name": "owner/repo",
       "default_channel": "engineering",
       "webhook_secret": "your-webhook-secret"
     }'
   ```

## Usage

### User Commands

Users can configure their preferences in Slack:

- `/notify-channel #engineering` - Set default channel for PR notifications
- `/notify-link` - Get secure OAuth link to connect your GitHub account
- `/notify-unlink` - Disconnect your GitHub account
- `/notify-status` - View current configuration and verification status

### Channel Override

Users can override the notification channel by adding this to their PR description:

```
@slack-channel: #specific-channel
```

### Notification Flow

1. **PR Opened**: Posts message to determined channel (annotation > user default > repo default)
2. **Reviews**: Syncs emoji reactions across all tracked messages (✅ approved, 🔄 changes requested, 💬 comments, 👋 dismissed)
3. **PR Closed**: Adds final emoji (🎉 merged, ❌ closed)

## Development

### Scripts

- `./scripts/dev.sh` - Start local development with ngrok
- `./scripts/lint.sh` - Run all linters
- `./scripts/deploy.sh` - Deploy to Cloud Run
- `./scripts/setup-infrastructure.sh` - Setup GCP infrastructure

### Linting

```bash
# Run all linters
./scripts/lint.sh

# Individual tools
golangci-lint run
hadolint Dockerfile
shellcheck scripts/*.sh
```

### Testing

```bash
# Run tests
go test ./...

# Test with coverage
go test -cover ./...
```

### Building

```bash
# Build locally
go build -o github-slack-notifier

# Build Docker image
docker build -t github-slack-notifier .
```

## Project Structure

```
github-slack-notifier/
├── cmd/github-slack-notifier/    # Application entry point
│   └── main.go                   # HTTP server setup, dependency injection
├── internal/                     # Private application code
│   ├── handlers/                 # HTTP request handlers
│   │   ├── github.go            # GitHub webhook handler
│   │   ├── slack.go             # Slack command handler
│   │   └── webhook_worker.go    # Async webhook processor
│   ├── services/                 # Business logic and external integrations
│   │   ├── firestore.go         # Database operations
│   │   ├── slack.go             # Slack API operations
│   │   └── cloud_tasks.go       # Task queue operations
│   ├── models/                   # Data structures
│   │   └── models.go            # User, Message, Repo, WebhookJob
│   ├── middleware/               # HTTP middleware
│   │   └── logging.go           # Request logging with trace IDs
│   ├── config/                   # Configuration
│   │   └── config.go            # Environment variable parsing
│   └── log/                      # Logging utilities
│       ├── context.go           # Context-aware logging
│       └── logger.go            # Logger initialization
├── scripts/                      # Development and deployment scripts
├── docs/                         # Documentation
└── CLAUDE.md                     # AI assistant guidelines
```

See [CLAUDE.md](CLAUDE.md#architecture-guidelines) for development guidelines.

## Documentation

- 📋 [Configuration Guide](docs/reference/CONFIGURATION.md) - Environment setup and app configuration
- 🔐 [OAuth Authentication](docs/reference/OAUTH.md) - GitHub OAuth implementation details
- 📡 [API Reference](docs/reference/API.md) - HTTP endpoints and Slack commands
- 🔧 [Slack App Setup](docs/reference/SLACK_APP_SETUP.md) - Detailed Slack app configuration
- 👨‍💻 [Development Guide](CLAUDE.md) - Architecture and coding guidelines

## Architecture

- **Language**: Go 1.23
- **Database**: Cloud Firestore
- **Deployment**: Cloud Run
- **Registry**: Artifact Registry
- **Authentication**: GitHub webhook secrets, Slack signing secrets

## Troubleshooting

### Common Issues

1. **Firestore permission denied**:
   - Ensure Cloud Run service account has Firestore permissions
   - Check `FIRESTORE_PROJECT_ID` and `FIRESTORE_DATABASE_ID` environment variables

2. **Slack commands not working**:
   - Verify webhook URL is correct
   - Check Slack signing secret
   - Ensure bot has required scopes

3. **GitHub webhooks failing**:
   - Verify webhook secret matches
   - Check webhook URL is accessible
   - Ensure app has correct permissions

4. **Emoji reactions not working**:
   - Check if you get `missing_scope` error in logs
   - Ensure `reactions:write` scope is added to your Slack app
   - Reinstall the app to workspace after adding the scope

5. **Async processing issues**:
   - Check Cloud Tasks queue status: `gcloud tasks queues describe webhook-processing --location=your-region`
   - Verify `WEBHOOK_WORKER_URL` points to your deployed service
   - Check worker endpoint logs for processing errors
   - Monitor queue depth for backlog issues

6. **"already_reacted" errors (now handled gracefully)**:
   - These are no longer errors - reactions that already exist are silently ignored
   - If you see retries for this, check your application version

### Monitoring Async Processing

```bash
# Check queue status
gcloud tasks queues describe webhook-processing --location=us-central1 --project=your-project

# View failed tasks
gcloud tasks list --queue=webhook-processing --location=us-central1 --project=your-project --filter="state:FAILED"

# Monitor processing times
gcloud logs read "resource.type=cloud_run_revision AND jsonPayload.msg=\"Webhook processed successfully\"" --project=your-project --format="value(jsonPayload.processing_time_ms)"
```

### Logs

```bash
# View Cloud Run logs
gcloud logs tail --filter="resource.type=cloud_run_revision" --project=your-project-id

# Local logs
go run main.go
```
