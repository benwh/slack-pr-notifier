# Channel Tracking Configuration Design

## Overview

This design document outlines a solution to decouple PR notification posting from automatic manual PR link tracking. Currently, when the bot is present in a channel (to post PR notifications), it automatically tracks all manually posted GitHub PR links. This feature will allow workspace users to configure whether manual PR tracking is enabled or disabled on a per-channel basis.

## Goals

1. **Maintain backwards compatibility**: By default, channels should continue tracking manual PR links (current behavior)
2. **Provide granular control**: Allow per-channel configuration of manual PR tracking
3. **Easy management**: Provide a simple UI in the Slack App Home for managing channel configurations
4. **Global accessibility**: Any user in the Slack workspace should be able to view and manage these settings

## Data Model

### New Firestore Collection: `channel_configs`

```go
type ChannelConfig struct {
    ID                   string    `firestore:"id"`                     // Document ID: {slack_team_id}#{channel_id}
    SlackTeamID          string    `firestore:"slack_team_id"`          // Slack workspace ID
    SlackChannelID       string    `firestore:"slack_channel_id"`       // Slack channel ID
    SlackChannelName     string    `firestore:"slack_channel_name"`     // Cached channel name for display
    ManualTrackingEnabled bool     `firestore:"manual_tracking_enabled"` // Whether to track manual PR links
    ConfiguredBy         string    `firestore:"configured_by"`          // Slack user ID who last updated
    CreatedAt            time.Time `firestore:"created_at"`
    UpdatedAt            time.Time `firestore:"updated_at"`
}
```

**Key Design Decisions:**

1. **Document ID Format**: Use `{slack_team_id}#{channel_id}` to ensure workspace isolation and efficient queries
2. **Default Behavior**: If no config exists for a channel, manual tracking is **enabled** (maintains current behavior)
3. **Channel Name Caching**: Store channel name to avoid API calls when displaying configuration list

## Implementation Changes

### 1. Manual PR Detection Logic

Update `handleMessageEvent` in `internal/handlers/slack.go`:

```go
func (sh *SlackHandler) handleMessageEvent(ctx context.Context, event *slackevents.MessageEvent) error {
    // ... existing validation ...

    // Check if manual tracking is enabled for this channel
    channelConfig, err := sh.fs.GetChannelConfig(ctx, sh.config.SlackTeamID, event.Channel)
    if err != nil {
        log.Error(ctx, "Failed to get channel config", "error", err)
        // Continue with default behavior on error
    }
    
    // Default to enabled if no config exists
    trackingEnabled := true
    if channelConfig != nil {
        trackingEnabled = channelConfig.ManualTrackingEnabled
    }
    
    if !trackingEnabled {
        log.Info(ctx, "Manual PR tracking disabled for channel", 
            "channel", event.Channel,
            "message_ts", event.TimeStamp)
        return nil
    }

    // ... continue with existing PR link extraction logic ...
}
```

### 2. Firestore Service Methods

Add to `internal/services/firestore.go`:

```go
// GetChannelConfig retrieves channel configuration
func (fs *FirestoreService) GetChannelConfig(ctx context.Context, slackTeamID, channelID string) (*models.ChannelConfig, error) {
    docID := slackTeamID + "#" + channelID
    doc, err := fs.client.Collection("channel_configs").Doc(docID).Get(ctx)
    if err != nil {
        if status.Code(err) == codes.NotFound {
            return nil, nil // No config means use defaults
        }
        return nil, fmt.Errorf("failed to get channel config: %w", err)
    }
    
    var config models.ChannelConfig
    err = doc.DataTo(&config)
    if err != nil {
        return nil, fmt.Errorf("failed to unmarshal channel config: %w", err)
    }
    
    return &config, nil
}

// SaveChannelConfig creates or updates channel configuration
func (fs *FirestoreService) SaveChannelConfig(ctx context.Context, config *models.ChannelConfig) error {
    config.UpdatedAt = time.Now()
    if config.CreatedAt.IsZero() {
        config.CreatedAt = time.Now()
    }
    
    docID := config.SlackTeamID + "#" + config.SlackChannelID
    _, err := fs.client.Collection("channel_configs").Doc(docID).Set(ctx, config)
    if err != nil {
        return fmt.Errorf("failed to save channel config: %w", err)
    }
    
    return nil
}

// ListChannelConfigs retrieves all channel configurations for a workspace
func (fs *FirestoreService) ListChannelConfigs(ctx context.Context, slackTeamID string) ([]*models.ChannelConfig, error) {
    iter := fs.client.Collection("channel_configs").
        Where("slack_team_id", "==", slackTeamID).
        OrderBy("slack_channel_name", firestore.Asc).
        Documents(ctx)
    defer iter.Stop()
    
    var configs []*models.ChannelConfig
    for {
        doc, err := iter.Next()
        if err != nil {
            if errors.Is(err, iterator.Done) {
                break
            }
            return nil, fmt.Errorf("failed to list channel configs: %w", err)
        }
        
        var config models.ChannelConfig
        err = doc.DataTo(&config)
        if err != nil {
            return nil, fmt.Errorf("failed to unmarshal channel config: %w", err)
        }
        
        configs = append(configs, &config)
    }
    
    return configs, nil
}
```

### 3. App Home UI Updates

#### New Section: Channel Tracking Settings

Add to the App Home view (after the default PR channel section):

```text
┌─────────────────────────────────────────┐
│ Manual PR Tracking Settings             │
├─────────────────────────────────────────┤
│ Configure which channels automatically  │
│ track GitHub PR links                   │
│                                         │
│ [Manage Channel Tracking]               │
└─────────────────────────────────────────┘
```

#### Channel Management Modal

When "Manage Channel Tracking" is clicked, open a modal with:

1. **List View** (Initial modal):

   ```text
   ┌─────────────────────────────────────────┐
   │ Channel Tracking Configuration          │
   ├─────────────────────────────────────────┤
   │ Select a channel to configure:          │
   │                                         │
   │ [Channel Selector Dropdown]             │
   │                                         │
   │ Currently Configured Channels:          │
   │                                         │
   │ #general         ✅ Tracking Enabled    │
   │ #random          ❌ Tracking Disabled   │
   │ #engineering     ✅ Tracking Enabled    │
   │                                         │
   │ Note: Channels not listed use the      │
   │ default setting (tracking enabled)      │
   │                                         │
   │                    [Cancel]    [Next]   │
   └─────────────────────────────────────────┘
   ```

2. **Configuration View** (After channel selection):

   ```text
   ┌─────────────────────────────────────────┐
   │ Configure: #engineering                 │
   ├─────────────────────────────────────────┤
   │ Manual PR Link Tracking:                │
   │                                         │
   │ ○ Enabled (Default)                     │
   │   The bot will track GitHub PR links    │
   │   posted by users in this channel       │
   │                                         │
   │ ○ Disabled                              │
   │   The bot will ignore GitHub PR links   │
   │   posted by users in this channel       │
   │                                         │
   │ Current Setting: Enabled                │
   │                                         │
   │              [Cancel]    [Save]         │
   └─────────────────────────────────────────┘
   ```

### 4. Interaction Handlers

Add new block actions and view submissions:

```go
// Block action for opening channel tracking manager
case "manage_channel_tracking":
    return sh.openChannelTrackingModal(ctx, payload)

// View submission for channel selection
case "channel_tracking_selector":
    return sh.handleChannelTrackingSelection(ctx, payload)

// View submission for saving channel config
case "save_channel_tracking":
    return sh.saveChannelTracking(ctx, payload)
```

## User Flow

1. User opens Slack App Home
2. User clicks "Manage Channel Tracking"
3. Modal opens showing:
   - Channel selector dropdown
   - List of currently configured channels with their status
4. User selects a channel and clicks "Next"
5. Configuration modal shows current setting for that channel
6. User selects enabled/disabled and clicks "Save"
7. System updates the configuration in Firestore
8. Modal closes and App Home refreshes to show success

## Permissions & Security

1. **Visibility**: Any workspace user can view and modify channel tracking settings
2. **Audit Trail**: Store `configured_by` to track who made changes
3. **Channel Validation**: Only allow configuration for channels the bot can access
4. **Rate Limiting**: Standard Slack interaction rate limits apply

## Migration & Rollout

1. **No Migration Required**: Default behavior (tracking enabled) is maintained for channels without configuration
2. **Feature Flag**: Consider adding an environment variable to enable/disable this feature during rollout
3. **Documentation**: Update user documentation to explain the new configuration option

## Future Enhancements

1. **Bulk Operations**: Allow enabling/disabling tracking for multiple channels at once
2. **Channel Patterns**: Support wildcard patterns (e.g., disable for all `#test-*` channels)
3. **Export/Import**: Allow exporting configuration for backup or migration
4. **Webhook for Changes**: Notify admins when tracking configuration changes
5. **Per-Repository Settings**: Allow different tracking rules for different repositories

## Technical Considerations

1. **Caching**: Consider caching channel configurations to reduce Firestore reads
2. **Performance**: The additional lookup in `handleMessageEvent` is minimal (one Firestore read)
3. **Consistency**: Channel renames should update the cached `slack_channel_name`
4. **Error Handling**: Default to tracking enabled if configuration lookup fails

## Alternative Approaches Considered

1. **User-Level Configuration**: Allow individual users to opt-out of tracking
   - Rejected: Too complex, doesn't solve the channel-wide concern

2. **Workspace-Wide Toggle**: Single on/off switch for all channels
   - Rejected: Not granular enough for diverse channel usage patterns

3. **Bot Commands**: Use slash commands to configure tracking
   - Rejected: Less discoverable than App Home UI

## Summary

This design provides a clean solution to decouple PR posting from automatic tracking while maintaining backwards compatibility. The App Home UI integration makes configuration discoverable and easy to manage, while the per-channel granularity provides the flexibility teams need.
