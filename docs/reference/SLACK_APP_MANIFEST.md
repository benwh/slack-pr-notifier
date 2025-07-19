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
| `channels:read` | Validate channel access for `/notify-channel` command |
| `chat:write` | Send PR notifications and add emoji reactions |
| `commands` | Handle slash commands |
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
| Slash Commands | `/webhooks/slack/slash-command` | Handle `/notify-*` commands |
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
2. **Slash Commands**: Test with `/notify-status` in any channel where the bot is invited

## Usage

1. **Invite the bot to channels:**
   ```
   /invite @GitHub PR Bot
   ```

2. **Configure notifications:**
   ```
   /notify-link your-github-username
   /notify-channel #engineering
   ```

3. **Test manual PR link detection:**
   ```
   Check out this PR: https://github.com/owner/repo/pull/123
   ```
   The bot will automatically add status reactions!

## Troubleshooting

### "URL verification failed"
- Ensure your service is deployed and accessible
- Check that the `/webhooks/slack/events` endpoint returns the challenge

### "Command not found"
- Verify slash command URLs point to `/webhooks/slack/slash-command`
- Check that your service is running and accessible

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

4. **Slash Commands:**
   - Create three commands: `/notify-channel`, `/notify-link`, `/notify-status`
   - Request URL for all: `https://your-service-url/webhooks/slack/slash-command`