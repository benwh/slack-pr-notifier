# App Home Migration Design

## Overview

This document outlines the migration from slash commands to Slack's App Home feature for the GitHub PR Notifier. App Home provides a richer, more intuitive user interface compared to text-based slash commands, offering a persistent dashboard where users can manage their notification settings.

## Current State Analysis

### Existing Slash Commands

The application currently supports four slash commands:

1. **`/notify-channel`** - Set default channel for notifications
   - Usage: `/notify-channel #channel-name`
   - Validates channel existence and bot access
   - Stores channel ID in Firestore

2. **`/notify-link`** - Link GitHub account via OAuth
   - Usage: `/notify-link` (no parameters)
   - Generates OAuth state with 15-minute expiration
   - Returns clickable OAuth link

3. **`/notify-unlink`** - Disconnect GitHub account
   - Usage: `/notify-unlink` (no parameters)
   - Removes GitHub connection while preserving other settings

4. **`/notify-status`** - View current configuration
   - Usage: `/notify-status` (no parameters)
   - Shows GitHub connection status and default channel

### Current Architecture

- **Handler**: `SlackHandler` in `internal/handlers/slack.go`
- **Routes**:
  - `/webhooks/slack/slash-command` - All slash commands
  - `/webhooks/slack/events` - Message events for PR link detection
- **Signature Verification**: HMAC-based verification for all Slack requests
- **Response Format**: Plain text responses with emoji indicators

## Proposed App Home Design

### User Interface

The App Home will display a personalized dashboard with the following sections:

#### 1. Header Section

```
🔔 GitHub PR Notifier
Your personal notification settings dashboard
```

#### 2. GitHub Connection Status

- **Connected State**:

  ```
  ✅ GitHub Connected
  Username: @username
  Status: Verified via OAuth
  [Disconnect Account] button
  ```

- **Disconnected State**:

  ```
  ❌ GitHub Not Connected
  Connect your GitHub account to receive personalized PR notifications
  [Connect GitHub Account] button
  ```

#### 3. Default Channel Configuration

- **Channel Set**:

  ```
  📢 Default Notification Channel
  Current: #engineering
  [Change Channel] button
  ```

- **No Channel Set**:

  ```
  📢 Default Notification Channel
  No channel configured
  [Select Channel] button
  ```

#### 4. Quick Actions

- Refresh button to update view with latest data
- Help/documentation link

### Block Kit Structure

```json
{
  "type": "home",
  "blocks": [
    {
      "type": "header",
      "text": {
        "type": "plain_text",
        "text": "🔔 GitHub PR Notifier"
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "Your personal notification settings dashboard"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*GitHub Connection*\n✅ Connected as @username"
      },
      "accessory": {
        "type": "button",
        "text": {
          "type": "plain_text",
          "text": "Disconnect"
        },
        "action_id": "disconnect_github",
        "style": "danger",
        "confirm": {
          "title": {
            "type": "plain_text",
            "text": "Disconnect GitHub?"
          },
          "text": {
            "type": "mrkdwn",
            "text": "Are you sure you want to disconnect your GitHub account?"
          },
          "confirm": {
            "type": "plain_text",
            "text": "Yes, disconnect"
          },
          "deny": {
            "type": "plain_text",
            "text": "Cancel"
          }
        }
      }
    }
  ]
}
```

## Technical Implementation

### New Components

#### 1. App Home Handler Methods

Add to `SlackHandler`:

```go
// HandleAppHomeOpened processes app_home_opened events
func (sh *SlackHandler) HandleAppHomeOpened(c *gin.Context) {
    // Parse event
    // Verify signature
    // Extract user ID
    // Fetch user data
    // Build and publish home view
}

// HandleInteraction processes interactive component actions
func (sh *SlackHandler) HandleInteraction(c *gin.Context) {
    // Parse interaction payload
    // Verify signature
    // Route based on action_id:
    //   - connect_github: Generate OAuth link
    //   - disconnect_github: Remove connection
    //   - select_channel: Open channel selector
    //   - refresh_view: Update home view
}
```

#### 2. View Building Service Methods

Add to `SlackService`:

```go
// PublishHomeView publishes the home tab view for a user
func (s *SlackService) PublishHomeView(ctx context.Context, userID string, view slack.HomeTabViewRequest) error {
    _, err := s.client.PublishViewContext(ctx, userID, view, "")
    return err
}

// BuildHomeView constructs the home tab view based on user data
func (s *SlackService) BuildHomeView(user *models.User) slack.HomeTabViewRequest {
    blocks := []slack.Block{
        // Header blocks
        // GitHub status section
        // Channel configuration section
        // Action buttons
    }

    return slack.HomeTabViewRequest{
        Type:   slack.VTHomeTab,
        Blocks: slack.Blocks{BlockSet: blocks},
    }
}
```

#### 3. Interactive Components

**Action IDs**:

- `connect_github` - Initiates OAuth flow
- `disconnect_github` - Removes GitHub connection
- `select_channel` - Opens channel selector modal
- `refresh_view` - Updates App Home with latest data

**Modal for Channel Selection**:

```go
type ChannelSelectorModal struct {
    Type   string
    Title  slack.TextBlockObject
    Blocks []slack.Block
    Submit slack.TextBlockObject
}
```

### Routes and Endpoints

Add new route to `main.go`:

```go
router.POST("/webhooks/slack/interactions", app.slackHandler.HandleInteraction)
```

Update event handler to process `app_home_opened` events:

```go
// In HandleEvent method
case slackevents.AppHomeOpened:
    sh.handleAppHomeOpened(ctx, ev)
```

### Slack App Manifest Updates

```yaml
features:
  app_home:
    home_tab_enabled: true
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false

settings:
  event_subscriptions:
    bot_events:
      - app_home_opened  # Add this event
      - message.channels
  interactivity:
    is_enabled: true  # Enable interactivity
    request_url: "{{BASE_URL}}/webhooks/slack/interactions"
```

### OAuth Flow Integration

When user clicks "Connect GitHub Account":

1. Generate OAuth state with return context
2. Store state with additional metadata:

   ```go
   type OAuthState struct {
       ID            string
       SlackUserID   string
       SlackTeamID   string
       SlackChannelID string
       ReturnToHome  bool  // New field
       ExpiresAt     time.Time
   }
   ```

3. After successful OAuth:
   - If `ReturnToHome` is true, publish updated home view
   - Show success message in App Home

## Migration Strategy

### Phase 1: Parallel Operation

1. Deploy App Home alongside existing slash commands

### Phase 2: Feature Parity

1. Ensure App Home has all slash command functionality
2. Add advanced features only available in App Home:
   - Channel preview/validation
   - OAuth status details
   - Recent notification history (future)

### Phase 3: Removal

1. Remove slash command handlers
2. Update documentation
3. Clean up manifest

## Error Handling

### User-Facing Errors

Display errors directly in App Home view:

```json
{
  "type": "section",
  "text": {
    "type": "mrkdwn",
    "text": "⚠️ *Error:* Unable to load your settings. Please try refreshing."
  }
}
```

### OAuth Errors

Handle OAuth failures gracefully:

- Expired state: Show "Link expired, please try again"
- Network errors: Show "Connection failed, please retry"
- Invalid state: Show "Invalid request, please start over"

### Channel Validation Errors

- Channel not found: "Channel not found. Please select a public channel."
- No access: "Bot doesn't have access to this channel. Please invite the bot first."

## Testing Strategy

### Integration Tests

1. Test full App Home flow:
   - Open App Home → View published
   - Click connect → OAuth flow → View updated
   - Select channel → Modal opened → Channel saved
   - Disconnect → Confirmation → Connection removed

2. Test error scenarios:
   - Network failures
   - Invalid user states
   - Concurrent updates

### Manual Testing

1. Test on multiple Slack clients (desktop, mobile, web)
2. Test with various permission levels
3. Test state consistency across sessions

## Documentation Updates

1. Update README with App Home instructions
2. Create user guide with screenshots
3. Update API documentation
4. Add troubleshooting guide for common issues

## Conclusion

Migrating to App Home will provide a significantly improved user experience while maintaining all existing functionality. The phased approach ensures a smooth transition with minimal disruption to users. The rich UI capabilities of App Home will also enable future enhancements that aren't possible with slash commands.
