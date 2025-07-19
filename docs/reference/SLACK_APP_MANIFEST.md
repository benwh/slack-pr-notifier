# Slack App Manifest Setup

This document explains how to set up your Slack app using the provided manifest template.

## Quick Setup

1. **Generate the manifest with your service URL:**
   ```bash
   ./scripts/generate-manifest.sh https://your-service-url.run.app
   ```

2. **Copy the generated manifest:**
   ```bash
   cat slack-app-manifest.yaml
   ```

3. **Create your Slack app:**
   - Go to [https://api.slack.com/apps](https://api.slack.com/apps)
   - Click **"Create New App"** → **"From an app manifest"**
   - Select your workspace
   - Paste the manifest content and click **"Next"**
   - Review and click **"Create"**

4. **Install the app:**
   - Click **"Install to Workspace"**
   - Authorize the permissions
   - Copy the **Bot User OAuth Token** (starts with `xoxb-`)

## Required OAuth Scopes

The manifest configures these OAuth scopes automatically:

| Scope | Purpose |
|-------|---------|
| `channels:read` | View basic information about public channels |
| `channels:join` | Automatically join public channels when selected |
| `groups:read` | View basic information about private channels (for validation) |
| `chat:write` | Send PR notifications and add emoji reactions |
| `links:read` | Read GitHub links in messages for manual PR detection |
| `channels:history` | Required by message.channels event subscription |

## Event Subscriptions

The app subscribes to these events for manual PR link detection:

| Event | Purpose |
|-------|---------|
| `message.channels` | Detect GitHub PR links in public channels |

## Endpoints Configured

| Type | Endpoint | Purpose |
|------|----------|---------|
| Interactive Components | `/webhooks/slack/interactions` | Handle App Home interactions |
| Event Subscriptions | `/webhooks/slack/events` | Process message events for PR links |

## Environment Variables

After creating the app, set these environment variables in your deployment:

```bash
SLACK_BOT_TOKEN=xoxb-your-bot-token-here
SLACK_SIGNING_SECRET=your-signing-secret-here
```

## Verification

After deployment, Slack will verify your endpoints:

1. **Event Subscriptions**: Slack sends a challenge request to verify the endpoint
2. **App Home**: Open the GitHub PR Bot app from your Slack sidebar to access the configuration interface

## Usage

1. **Configure notifications:**
   - Open the GitHub PR Bot app from your Slack sidebar
   - Click "Connect GitHub Account" to link your GitHub account via OAuth
   - Click "Set Default Channel" to choose where you receive notifications
   - Note: The bot will automatically join public channels when you select them!

2. **Test manual PR link detection:**
   ```
   Check out this PR: https://github.com/owner/repo/pull/123
   ```
   The bot will automatically add status reactions!

3. **For private channels only:**
   If you need to use a private channel (not recommended), you must first invite the bot:
   ```
   /invite @GitHub PR Bot
   ```
   However, the app will reject private channels for security reasons.

## Troubleshooting

### "URL verification failed"
- Ensure your service is deployed and accessible
- Check that the `/webhooks/slack/events` endpoint returns the challenge

### "App Home not loading"
- Verify the App Home tab is enabled in your Slack app settings
- Check that the `/webhooks/slack/interactions` endpoint is accessible

### "Missing scopes"
- Use the manifest to ensure all required OAuth scopes are granted
- Reinstall the app if you added new scopes

## Manual Configuration (Alternative)

If you prefer to configure manually instead of using the manifest:

1. **Basic Information:**
   - App Name: `GitHub PR Notifier`
   - Description: `Automatically notify Slack channels about GitHub pull request events and track manual PR links`

2. **OAuth & Permissions:**
   - Add all scopes listed above under "Required OAuth Scopes"

3. **Event Subscriptions:**
   - Request URL: `https://your-service-url/webhooks/slack/events`
   - Subscribe to bot events: `message.channels`

4. **App Home:**
   - Enable the Home Tab in App Home settings
   - Disable the Messages Tab
   - Set Interactivity Request URL: `https://your-service-url/webhooks/slack/interactions`