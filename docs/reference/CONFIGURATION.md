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

The application now uses **OAuth-based multi-workspace support**. The old `SLACK_BOT_TOKEN` approach is no longer supported.

See [SLACK_APP_SETUP.md](./SLACK_APP_SETUP.md) for detailed setup instructions.

**Quick Setup:**
1. Generate the manifest: `./scripts/generate-slack-manifest.sh`
2. Create app from CLI: `./scripts/apply-slack-manifest.sh --create`
3. Or create manually at https://api.slack.com/apps using "From an app manifest"
4. Configure OAuth settings and get credentials

**Required Environment Variables:**
- `SLACK_CLIENT_ID` - App Client ID for OAuth flow
- `SLACK_CLIENT_SECRET` - App Client Secret for OAuth flow  
- `SLACK_REDIRECT_URL` - OAuth callback URL (e.g., `https://your-domain.com/slack/oauth/callback`)
- `SLACK_SIGNING_SECRET` - Signing secret for webhook verification
- `SLACK_CONFIG_ACCESS_TOKEN` - App-Level Token for CLI management (optional, starts with `xoxe-1-`)

**Multi-Workspace Installation:**
- Each workspace installs the app via OAuth at: `https://your-domain.com/slack/install`
- Workspace-specific tokens are stored securely in Firestore
- No manual token configuration needed per workspace

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
- **Cloud Tasks Authentication**: Static secret protects job processing endpoints

### Cloud Tasks Static Secret Authentication

The `/jobs/process` endpoint is protected by a static secret to ensure only Google Cloud Tasks can execute jobs.

**Configuration:**
- `CLOUD_TASKS_SECRET` - Static secret added to Cloud Tasks HTTP headers (required)

**How it works:**
1. Cloud Tasks adds `X-Cloud-Tasks-Secret` header to all job processing requests
2. Job processing endpoint validates the secret matches the configured value
3. Requests without the correct secret are rejected with 401 Unauthorized

**Authentication Design Decision:**
We chose static secret authentication over Google Cloud OIDC tokens for practical Cloud Run considerations:

**Static Secret Advantages:**
- **Performance**: Ultra-fast validation (~1-2ms) vs OIDC (~100-500ms cold start penalty)
- **No cold start latency**: No external API calls to fetch Google's public keys
- **Simple debugging**: Clear success/failure without JWT complexity
- **Cloud Run optimized**: No network dependency on Google's cert endpoint
- **Fewer failure modes**: Only secret validation can fail

**OIDC Trade-offs Considered:**
- OIDC provides service account audit trails and automatic key rotation
- However, Cloud Run's frequent cold starts make certificate fetching expensive
- For internal endpoint protection, static secret provides sufficient security

**Setup:**
1. Generate a secure random 64+ character secret (e.g., `openssl rand -base64 64`)
2. Set `CLOUD_TASKS_SECRET` environment variable in your deployment
3. Keep the secret secure and rotate it periodically via deployment updates

**Security Notes:**
- Use a cryptographically secure random string (64+ characters recommended)
- Rotate the secret periodically by updating the environment variable
- Never log or expose the secret in application responses
- The secret provides adequate protection for internal service-to-service communication