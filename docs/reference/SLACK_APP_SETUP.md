# Slack App Setup Guide

This guide walks you through creating a Slack app with OAuth support for multi-workspace installations.

> **Important**: This application now uses OAuth-based multi-workspace support. The old `SLACK_BOT_TOKEN` approach is no longer supported.

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

## OAuth Configuration

### 1. Enable OAuth & Permissions

In your Slack app settings:

1. Go to **"OAuth & Permissions"**
2. Add your redirect URLs:
   - Development: `http://localhost:8080/slack/oauth/callback`
   - Production: `https://your-domain.com/slack/oauth/callback`

### 2. Required Bot Token Scopes

Ensure these scopes are configured:

- `channels:read` - View basic channel information for channel validation
- `chat:write` - Send PR notifications to channels
- `links:read` - Detect manual PR links in messages
- `channels:history` - Required for the message.channels event subscription

### 3. Event Subscriptions

1. Go to **"Event Subscriptions"**
2. Enable events
3. Set Request URL: `https://your-domain.com/webhooks/slack/events`
4. Subscribe to **Bot Events**:
   - `message.channels` - For manual PR link detection
   - `app_home_opened` - For App Home interface

### 4. App Home & Interactivity

1. **App Home**:
   - Go to **"App Home"**
   - Enable the **"Home Tab"**

2. **Interactivity**:
   - Go to **"Interactivity & Shortcuts"**
   - Enable **"Interactivity"**
   - Set Request URL: `https://your-domain.com/webhooks/slack/interactions`

### 5. Get OAuth Credentials

From your app's **"Basic Information"** page:

1. Copy the **Client ID**
2. Copy the **Client Secret** (you may need to generate one)
3. Copy the **Signing Secret**

## Environment Configuration

Add these OAuth credentials to your `.env` file:

```bash
# Slack OAuth Configuration (REQUIRED)
SLACK_CLIENT_ID=1234567890.1234567890
SLACK_CLIENT_SECRET=your-client-secret-here
SLACK_REDIRECT_URL=https://your-domain.com/slack/oauth/callback
SLACK_SIGNING_SECRET=your-signing-secret-here

# Optional: Custom emoji for different PR states
EMOJI_APPROVED=white_check_mark
EMOJI_CHANGES_REQUESTED=arrows_counterclockwise
EMOJI_COMMENTED=speech_balloon
EMOJI_MERGED=tada
EMOJI_CLOSED=x
```

> **Note**: `SLACK_BOT_TOKEN` is no longer used. The app obtains workspace-specific tokens through OAuth installation.

## Installation Flow

### Multi-Workspace Support

This app supports installation to multiple Slack workspaces:

1. **Installation URL**: `https://your-domain.com/slack/install`
2. **Users visit the URL** → Redirected to Slack OAuth
3. **Slack prompts for permissions** → User authorizes
4. **App stores workspace token** → Installation complete

### Workspace Management

- Each workspace gets its own OAuth token stored in Firestore
- Tokens are cached for performance
- Workspaces can be uninstalled and reinstalled independently

## Testing Your Setup

### 1. Test OAuth Installation

1. Visit your installation URL: `https://your-domain.com/slack/install`
2. Complete the OAuth flow
3. Check your application logs for successful workspace installation

### 2. Test App Home

1. Open your Slack workspace
2. Find your app in the sidebar
3. Click on the **"Home"** tab
4. Verify the interface loads correctly

### 3. Test PR Link Detection

1. Invite the bot to a test channel: `/invite @your-bot-name`
2. Post a GitHub PR link in the channel
3. Verify the bot processes the manual link

### 4. Test GitHub Integration

1. Configure a repository webhook
2. Open a PR in that repository
3. Verify notifications appear in the configured Slack channel

## Production Deployment

### OAuth Redirect URLs

Update your Slack app settings with production URLs:

1. Go to **"OAuth & Permissions"**
2. Update **Redirect URLs**:
   - Remove development URLs (`localhost`)
   - Add production URL: `https://your-domain.com/slack/oauth/callback`

### Event Subscription URLs

Update event endpoints:

1. **Event Subscriptions**: `https://your-domain.com/webhooks/slack/events`
2. **Interactivity**: `https://your-domain.com/webhooks/slack/interactions`

### Distribution (Optional)

To distribute your app to other organizations:

1. Go to **"Manage Distribution"**
2. Complete the checklist
3. Submit for review (if public distribution desired)

## Troubleshooting

### Common Issues

1. **OAuth installation fails**:
   - Check `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET` are correct
   - Verify redirect URL matches exactly (including protocol)
   - Check application logs for specific error messages

2. **Events not received**:
   - Verify Event Subscription URL is accessible
   - Check `SLACK_SIGNING_SECRET` matches your app settings
   - Ensure bot is invited to channels where events should be received

3. **App Home not loading**:
   - Verify App Home is enabled in app settings
   - Check for errors in application logs
   - Ensure workspace is properly installed via OAuth

4. **Messages not posting**:
   - Verify workspace has completed OAuth installation
   - Check bot has necessary permissions in target channels
   - Ensure channels are public (private channels are not supported)

### Testing OAuth Configuration

Test your OAuth setup:

```bash
# Test installation endpoint
curl https://your-domain.com/slack/install

# Should redirect to Slack OAuth with proper client_id and scopes
```

### Debug Workspace Installations

Check which workspaces are installed:

```bash
# View application logs during installation
# Look for "Workspace saved successfully" messages
```

## Migration from Bot Token

If migrating from the old `SLACK_BOT_TOKEN` system:

1. **Remove old environment variables**:
   - Remove `SLACK_BOT_TOKEN` from your `.env`
   - The app will no longer accept this configuration

2. **Add OAuth credentials**:
   - Configure `SLACK_CLIENT_ID`, `SLACK_CLIENT_SECRET`, etc.
   - Update your Slack app with OAuth settings

3. **Reinstall to workspaces**:
   - Each workspace needs to complete the OAuth flow
   - Use the installation URL: `https://your-domain.com/slack/install`

4. **Update documentation**:
   - Share the new installation URL with users
   - Update any internal setup documentation

## Next Steps

Once your Slack app is set up with OAuth:

1. **Test the installation flow** with a development workspace
2. **Configure GitHub webhooks** to point to your application
3. **Test the complete flow** from GitHub PR to Slack notification
4. **Set up monitoring** for OAuth installations and errors
5. **Document the installation process** for your users
