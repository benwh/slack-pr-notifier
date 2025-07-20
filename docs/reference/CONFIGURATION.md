# Configuration Guide

This document covers all configuration aspects of the GitHub-Slack Notifier.

## Environment Variables

All configuration is managed through environment variables. For local development, copy `.env.example` to `.env` and update the values.

**Reference**: See [`.env.example`](../.env.example) for all available configuration options and their descriptions.

## GitHub App Setup

### Option 1: Extend Existing GitHub App (Recommended)

If you already have a GitHub App for webhooks:

1. Go to your GitHub App settings
2. Enable "Request user authorization (OAuth) during installation" 
3. Set "User authorization callback URL" to: `https://your-service-url.run.app/auth/github/callback`
4. Note your Client ID and generate a new Client Secret
5. Update your `.env` file with these credentials

**Why this approach?** We chose to extend our existing GitHub App rather than create a separate OAuth App for simpler deployment (fewer secrets to manage) and consistency with existing webhook infrastructure.

### Option 2: Create New OAuth App

1. Go to [GitHub OAuth Apps](https://github.com/settings/applications/new)
2. Set "Authorization callback URL" to: `https://your-service-url.run.app/auth/github/callback`
3. Note your Client ID and Client Secret
4. Update your `.env` file

## Slack App Configuration

The Slack app requires specific OAuth scopes. See [SLACK_APP_SETUP.md](./SLACK_APP_SETUP.md) for detailed setup instructions.

**Quick Setup:**
1. Generate the manifest: `./scripts/generate-slack-manifest.sh`
2. Create app from CLI: `./scripts/apply-slack-manifest.sh --create`
3. Or create manually at https://api.slack.com/apps using "From an app manifest"
4. Install the app to generate the bot token

**Required Environment Variables:**
- `SLACK_BOT_TOKEN` - Bot User OAuth Token (starts with `xoxb-`)
- `SLACK_CONFIG_ACCESS_TOKEN` - App-Level Token for CLI management (starts with `xoxe-1-`, optional)

## Database Setup

The application uses Cloud Firestore with automatic collection creation. No manual schema setup is required.

## Deployment Configuration

### Google Cloud Run

Required GCP services:
- Cloud Firestore
- Cloud Tasks  
- Artifact Registry
- Cloud Run

### Environment Variables in Production

Ensure all required environment variables from `.env.example` are set in your deployment environment. Never commit secrets to version control.

## Security Considerations

- **Webhook Signatures**: Always validate GitHub webhook signatures
- **OAuth State**: CSRF protection with 15-minute expiration
- **API Keys**: Use strong random strings for admin endpoints
- **Secrets**: Never log or expose secrets in responses
- **HTTPS**: Always use HTTPS in production for OAuth callbacks
- **Cloud Tasks Authentication**: OIDC tokens protect job processing endpoints

### Cloud Tasks OIDC Authentication

The `/jobs/process` endpoint is protected by OIDC token verification to ensure only Google Cloud Tasks can execute jobs.

**Configuration:**
- `CLOUD_TASKS_SERVICE_ACCOUNT_EMAIL` - Service account email for OIDC token generation (optional for development)

**How it works:**
1. Cloud Tasks generates OIDC tokens signed by the configured service account
2. Job processing endpoint verifies tokens against Google's public keys
3. Tokens are validated for audience, issuer, and service account email
4. If no service account is configured, authentication is bypassed (development mode)

**Setup:**
1. Create or use existing service account with Cloud Tasks permissions
2. Set `CLOUD_TASKS_SERVICE_ACCOUNT_EMAIL` environment variable
3. Ensure the service account has permissions to execute Cloud Tasks