# Slack App Setup Guide

This guide walks you through creating a Slack app using the provided manifest file for simplified setup.

## Quick Setup with Manifest

### Option 1: Command Line (Recommended)

**Prerequisites:**
```bash
# Install slack-manifest CLI globally
npm install -g slack-manifest
```

**For new apps:**
```bash
# Generate manifest and create new app
./scripts/generate-slack-manifest.sh
./scripts/apply-slack-manifest.sh --create
```

**For updating existing apps:**
```bash
# Set environment variables
export SLACK_CONFIG_ACCESS_TOKEN=xoxe-1-your-config-token
export SLACK_APP_ID=A1234567890

# Apply manifest changes
./scripts/apply-slack-manifest.sh
```

### Option 2: Web Interface

1. Go to [Slack App Management](https://api.slack.com/apps)
2. Click **"Create New App"**
3. Select **"From an app manifest"**
4. Choose your development workspace
5. Click **"Next"**
6. Copy and paste the contents of `slack-app-manifest.yaml` into the input field
7. Click **"Next"**
8. Review the configuration and click **"Create"**

### 2. Get Your Bot Token

1. In your app's settings, go to **"OAuth & Permissions"**
2. Click **"Install to Workspace"**
3. Authorize the app
4. Copy the **"Bot User OAuth Token"** (starts with `xoxb-`)
5. Add this to your `.env` file as `SLACK_BOT_TOKEN`

### 3. Configure Environment Variables

Add these to your `.env` file:

```bash
# Slack Configuration
SLACK_BOT_TOKEN=xoxb-your-bot-token-here

# Optional: Custom emoji for different PR states
EMOJI_APPROVED=white_check_mark
EMOJI_CHANGES_REQUESTED=arrows_counterclockwise
EMOJI_COMMENTED=speech_balloon
EMOJI_MERGED=tada
EMOJI_CLOSED=x
```

## Manual Setup (Alternative)

If you prefer to set up manually instead of using the manifest:

### Required Bot Token Scopes

- `chat:write` - Post messages in channels
- `chat:write.public` - Send messages to channels the bot isn't a member of
- `channels:read` - View basic channel information
- `groups:read` - View basic private channel information
- `reactions:write` - Add emoji reactions to messages

### App Settings

- **App Name**: "GitHub PR Notifier"
- **Bot Display Name**: "GitHub PR Bot"
- **Always Show My Bot as Online**: Enabled

## Testing Your Setup

1. Invite the bot to a test channel: `/invite @GitHub PR Bot`
2. Configure through the Slack App Home:
   - Open the app home tab
   - Connect your GitHub account via OAuth
   - Set your default notification channel

3. Trigger a test webhook from GitHub to verify notifications work
   - Repository configurations are created automatically when PRs are opened

## Production Deployment

For production, you'll need to:

1. **Update OAuth Redirect URLs** in your Slack app settings to match your production domain
2. **Enable Distribution** if you want to share the app with other workspaces
3. **Set up proper webhook URLs** in your GitHub repository settings

## Troubleshooting

### Common Issues

1. **Bot token not working**: Make sure you're using the "Bot User OAuth Token" (starts with `xoxb-`), not the "OAuth Access Token"
2. **Permission denied**: Ensure the bot has been invited to the channel you're trying to post to
3. **Channel not found**: Use the channel name without the `#` prefix (e.g., `general` not `#general`)

### Testing Permissions

You can test if your bot has the right permissions by running:

```bash
curl -X POST https://slack.com/api/auth.test \
  -H "Authorization: Bearer xoxb-your-bot-token-here"
```

## Next Steps

Once your Slack app is set up:

1. Configure GitHub webhooks to point to your application
2. Register repositories using the API endpoint
3. Test with actual GitHub events
4. Set up monitoring and alerting for production use

