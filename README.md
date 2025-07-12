# GitHub-Slack Notifier

A Go application that sends Slack notifications for GitHub pull request events.

## Features

- 🔗 **PR Notifications**: Sends Slack messages when PRs are opened
- 📝 **Review Updates**: Adds emoji reactions for PR reviews (approved ✅, changes requested 🔄, comments 💬)
- 🎉 **Closure Updates**: Adds emoji reactions when PRs are merged or closed
- ⚙️ **Slack Configuration**: Use slash commands to configure your settings

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

## Configuration

### Environment Variables

Create `.env` file based on `.env.example`

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
   - See [detailed setup guide](docs/SLACK_APP_SETUP.md) for complete instructions

2. **Get Bot Token**:
   - Install app to workspace
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
   - Add scopes: `chat:write`, `chat:write.public`, `reactions:write`, `channels:read`, `groups:read`
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
- `/notify-link octocat` - Link GitHub username to Slack account
- `/notify-status` - View current configuration

### Channel Override

Users can override the notification channel by adding this to their PR description:

```
@slack-channel: #specific-channel
```

### Notification Flow

1. **PR Opened**: Posts message to determined channel (annotation > user default > repo default)
2. **Reviews**: Adds emoji reactions (✅ approved, 🔄 changes requested, 💬 comments)
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

### Logs

```bash
# View Cloud Run logs
gcloud logs tail --filter="resource.type=cloud_run_revision" --project=your-project-id

# Local logs
go run main.go
```
