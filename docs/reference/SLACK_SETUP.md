# Slack App Setup Guide

This guide walks you through creating a Slack app with OAuth support for multi-workspace installations.

## Quick Setup with Manifest (Recommended)

### Option 1: Command Line

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

1. Generate the manifest: `./scripts/generate-slack-manifest.sh`
2. Go to [Slack App Management](https://api.slack.com/apps)
3. Click **"Create New App"** → **"From an app manifest"**
4. Choose your development workspace
5. Copy and paste the contents of `slack-app-manifest.yaml`
6. Click **"Next"** → Review → **"Create"**

## OAuth Configuration

### Required Bot Token Scopes

The manifest configures these OAuth scopes automatically:

| Scope | Purpose |
|-------|---------|
| `channels:read` | View basic information about public channels |
| `chat:write` | Send PR notifications and add emoji reactions |
| `links:read` | Read GitHub links in messages for manual PR detection |
| `channels:history` | Required by message.channels event subscription |

### Event Subscriptions

The app subscribes to these events for manual PR link detection:

| Event | Purpose |
|-------|---------|
| `message.channels` | Detect GitHub PR links in public channels |
| `app_home_opened` | For App Home interface |

### Endpoints Configured

| Type | Endpoint | Purpose |
|------|----------|---------|
| Interactive Components | `/webhooks/slack/interactions` | Handle App Home interactions |
| Event Subscriptions | `/webhooks/slack/events` | Process message events for PR links |

## Get OAuth Credentials

From your app's **"Basic Information"** page:

1. Copy the **Client ID**
2. Copy the **Client Secret** (you may need to generate one)
3. Copy the **Signing Secret**

Add these to your environment:

```bash
# Slack OAuth Configuration (required)
SLACK_CLIENT_ID=1234567890.1234567890
SLACK_CLIENT_SECRET=your-client-secret-here
SLACK_SIGNING_SECRET=your-signing-secret-here
```

## Installation Flow

### Multi-Workspace Support

This app supports installation to multiple Slack workspaces:

1. **Installation URL**: `https://your-domain.com/auth/slack/install`
2. **Users visit the URL** → Redirected to Slack OAuth
3. **Slack prompts for permissions** → User authorizes
4. **App stores workspace token** → Installation complete

### Workspace Management

- Each workspace gets its own OAuth token stored in Firestore
- Tokens are cached for performance
- Workspaces can be uninstalled and reinstalled independently

## Testing Your Setup

### 1. Test OAuth Installation

1. Visit your installation URL: `https://your-domain.com/auth/slack/install`
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
   - Add production URL: `https://your-domain.com/auth/slack/callback`

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
curl https://your-domain.com/auth/slack/install

# Should redirect to Slack OAuth with proper client_id and scopes
```

### Debug Workspace Installations

Check which workspaces are installed by looking for "Workspace saved successfully" messages in your application logs during installation.

## Manual Configuration (Alternative)

If you prefer to configure manually instead of using the manifest:

1. **Basic Information:**
   - App Name: `GitHub PR Notifier`
   - Description: `Automatically notify Slack channels about GitHub pull request events and track manual PR links`

2. **OAuth & Permissions:**
   - Add all scopes listed above under "Required Bot Token Scopes"

3. **Event Subscriptions:**
   - Request URL: `https://your-service-url/webhooks/slack/events`
   - Subscribe to bot events: `message.channels`, `app_home_opened`

4. **App Home:**
   - Enable the Home Tab in App Home settings
   - Disable the Messages Tab
   - Set Interactivity Request URL: `https://your-service-url/webhooks/slack/interactions`