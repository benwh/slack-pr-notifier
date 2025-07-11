# GitHub-Slack Notifier

A Go application that sends Slack notifications for GitHub pull request events.

## Features

- 🔗 **PR Notifications**: Sends Slack messages when PRs are opened
- 📝 **Review Updates**: Adds emoji reactions for PR reviews (approved ✅, changes requested 🔄, comments 💬)
- 🎉 **Closure Updates**: Adds emoji reactions when PRs are merged or closed
- ⚙️ **Slack Configuration**: Use slash commands to configure your settings

## Quick Start

1. **Deploy to GCP**:
   ```bash
   ./deploy.sh
   ```

2. **Set environment variables** in Cloud Run console or via gcloud CLI

3. **Configure GitHub webhook** with the deployed URL

4. **Configure Slack app** with slash commands

## Slash Commands

- `/notify-channel #channel-name` - Set your default notification channel
- `/notify-link github-username` - Link your GitHub account
- `/notify-status` - View your current configuration

## Environment Variables

See `.env.example` for all required and optional environment variables.

## Architecture

- **Language**: Go 1.21
- **Database**: Cloud Firestore
- **Deployment**: Cloud Run
- **Authentication**: GitHub webhook secrets, Slack signing secrets

## Development

```bash
# Install dependencies
go mod download

# Run locally
go run main.go

# Build
go build -o github-slack-notifier
```
