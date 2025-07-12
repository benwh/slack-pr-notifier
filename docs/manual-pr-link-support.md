# Manual PR Link Support Design

## Overview

This document outlines the design for supporting GitHub PR links that are manually posted in Slack channels. The goal is to:

1. Add review status emojis to manually posted PR links
2. Avoid duplicating notifications when webhooks arrive later
3. Maintain consistency with the existing notification system

## Current State

### Existing Flow

1. GitHub webhook → Cloud Tasks → Slack notification with PR link
2. Review webhooks → Add emoji reactions to existing Slack message
3. Messages tracked in Firestore by `repo_full_name` + `pr_number`

### Limitations

- Only tracks messages sent by the bot
- Cannot react to manually posted PR links
- No deduplication if both manual link and webhook notification exist

## Proposed Solution

### Architecture Overview

We'll use Slack's Events API to monitor messages in channels where the bot is present, detect GitHub PR URLs, and react to all instances of a PR link within a channel.

### Components

#### 1. Slack API Endpoints Reorganization

**Endpoint Changes**:
- Move existing slash commands: `/webhooks/slack` → `/webhooks/slack/slash-command`
- Add new events endpoint: `/webhooks/slack/events`

Both endpoints will be handled by the existing `SlackHandler` to share dependencies.

**Event Types to Subscribe**:
- `message` - New messages posted to channels
- `message.changed` - Message edits (in case PR links are added later)

**Permissions Required**:
- `links:read` - Read information about links shared in channels
- `reactions:write` - Add reactions to messages (already have via `chat:write`)

#### 2. PR Link Detection Utility

**New Utility Function**: `internal/utils/github.go` (or directly in the handler)

```go
// ExtractPRLinks parses GitHub PR URLs from message text
func ExtractPRLinks(text string) []PRLink {
    pattern := regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
    matches := pattern.FindAllStringSubmatch(text, -1)
    
    var links []PRLink
    for _, match := range matches {
        prNumber, _ := strconv.Atoi(match[3])
        links = append(links, PRLink{
            URL:          match[0],
            Owner:        match[1],
            Repo:         match[2],
            PRNumber:     prNumber,
            FullRepoName: match[1] + "/" + match[2],
        })
    }
    return links
}

type PRLink struct {
    URL          string
    Owner        string
    Repo         string
    PRNumber     int
    FullRepoName string // "owner/repo"
}
```

This is a simple utility function that doesn't need to be a service since it:
- Has no state to manage
- Requires no dependencies
- Is purely functional (input → output)
- Can be easily tested in isolation

#### 3. Enhanced Message Tracking

Since we'll react to ALL occurrences of a PR in a channel, we need to track multiple messages:

**New Model**: `TrackedMessage` (separate from existing `Message` model)

```go
type TrackedMessage struct {
    ID             string    // Auto-generated document ID
    PRNumber       int       // GitHub PR number
    RepoFullName   string    // e.g., "owner/repo"
    SlackChannel   string    // Slack channel ID
    SlackMessageTS string    // Slack message timestamp
    MessageSource  string    // "bot" or "manual"
    CreatedAt      time.Time // When we started tracking this message
}
```

This allows multiple `TrackedMessage` records per PR+channel combination.

**Note**: The existing `Message` model will continue to be used for the primary bot notification (one per PR per channel), while `TrackedMessage` entries will reference all occurrences of that PR in the channel for reaction synchronization.

#### 4. Reaction Strategy

**Key Principle**: React to ALL occurrences of a PR in a channel

**Scenarios**:

1. **Manual link posted first, webhook arrives later**:
   - Event API detects manual link → Create `TrackedMessage` record
   - Add reactions based on current PR status
   - Webhook arrives → Check for existing bot message in channel
   - If no bot message exists → Post new notification and track it
   - Update reactions on ALL tracked messages for this PR

2. **Webhook notification first, manual link posted later**:
   - Webhook posts notification → Create `TrackedMessage` record
   - Manual link detected → Create another `TrackedMessage` record
   - Add same reactions to the manual link

3. **Multiple links in same channel**:
   - Track ALL occurrences (both bot and manual)
   - Synchronize reactions across all tracked messages for the same PR

**Deduplication**: The bot will post at most ONE notification per PR per channel, but will react to ALL occurrences.

### Implementation Flow

#### Phase 1: Event Processing

1. **Updated Slack Handler**

   ```go
   // internal/handlers/slack.go
   func (h *SlackHandler) HandleSlashCommand(ctx context.Context, cmd slack.SlashCommand)
   func (h *SlackHandler) HandleEvent(ctx context.Context, event slack.Event)
   ```

2. **Message Processing Pipeline**:
   - Validate event signature
   - Extract message text and channel
   - Detect GitHub PR URLs
   - Queue processing to Cloud Tasks (async)

#### Phase 2: Async PR Link Processing

1. **New Cloud Task Type**: `process-manual-link`

2. **Processing Steps**:

   ```
   For each detected PR URL:
   1. Parse owner, repo, PR number
   2. Create TrackedMessage record for this occurrence
   3. Fetch current PR status from GitHub API (if needed)
   4. Get all TrackedMessages for this PR in this channel
   5. Synchronize reactions across all tracked messages
   ```

#### Phase 3: Webhook Updates

Update `WebhookWorkerHandler` to handle multiple tracked messages:

```go
// Check if we already posted a bot message for this PR in this channel
botMessage, err := h.firestoreService.GetBotMessage(ctx, repoFullName, prNumber, channelID)
if err == nil && botMessage != nil {
    // Bot already posted, just update reactions on all tracked messages
    h.updateAllTrackedMessages(ctx, repoFullName, prNumber, channelID, newStatus)
    return
}

// Post new bot message if needed
msgTS := h.slackService.PostPRMessage(...)
h.firestoreService.CreateTrackedMessage(ctx, TrackedMessage{
    MessageSource: "bot",
    SlackMessageTS: msgTS,
    // ... other fields
})

// Update reactions on ALL tracked messages (including any manual ones)
h.updateAllTrackedMessages(ctx, repoFullName, prNumber, channelID, newStatus)
```

### Configuration

This feature is enabled by default and works in all channels where the bot has been added. No additional configuration is required.

The bot will:
- Monitor all messages in channels where it's present
- React to all GitHub PR links (both manually posted and bot-posted)
- Post at most one notification per PR per channel
- Keep reactions synchronized across all occurrences of the same PR

### Security Considerations

1. **Event Validation**: Verify Slack signing secret for all event webhooks
2. **Channel Access**: Only process messages in channels where bot is invited
3. **Rate Limiting**: Implement rate limiting for event processing
4. **GitHub API**: Cache PR status to minimize API calls

### Performance Optimizations

1. **Batch Processing**: Group multiple PR links from same message
2. **Caching**: Cache GitHub PR metadata for 5 minutes
3. **Selective Processing**: Only process messages containing "github.com"
4. **Channel Filtering**: Skip DMs and private channels

### Migration & Rollout

#### Phase 1: Read-Only Mode

- Deploy event listener
- Log detected PR links without taking action
- Monitor for false positives

#### Phase 2: Reaction-Only Mode

- Add reactions to manual PR links
- No deduplication yet
- Gather metrics on overlap

#### Phase 3: Full Deduplication

- Enable webhook deduplication
- Monitor for missed notifications

### Monitoring & Metrics

Track:

- Manual PR links detected per hour
- Deduplication rate (prevented duplicate posts)
- Reaction sync operations
- Event processing latency

### Future Enhancements

1. **Unfurling Override**: Use Slack's `chat.unfurl` API to customize PR link previews
2. **Retroactive Processing**: Scan recent channel history for untracked PR links
3. **Smart Notifications**: Notify PR author when their manually-shared PR gets reviewed
4. **Cross-Channel Awareness**: Track same PR across multiple channels

## Implementation Checklist

- [ ] Refactor routing: `/webhooks/slack` → `/webhooks/slack/slash-command`
- [ ] Add `/webhooks/slack/events` endpoint in SlackHandler
- [ ] Implement PR link detection utility function
- [ ] Create TrackedMessage model and Firestore collection
- [ ] Create manual link processing worker
- [ ] Update webhook worker to handle multiple tracked messages
- [ ] Implement reaction synchronization logic
- [ ] Write comprehensive tests
- [ ] Update Slack app manifest with new scopes
- [ ] Document the feature for users
- [ ] Deploy in phases with monitoring

## Router Configuration Updates

```go
// cmd/github-slack-notifier/main.go
// Before:
router.POST("/webhooks/slack", slackHandler.Handle)

// After:
router.POST("/webhooks/slack/slash-command", slackHandler.HandleSlashCommand)
router.POST("/webhooks/slack/events", slackHandler.HandleEvent)

// Optional: Add redirect for backward compatibility during transition
router.POST("/webhooks/slack", func(c *gin.Context) {
    c.Redirect(http.StatusPermanentRedirect, "/webhooks/slack/slash-command")
})
```

**Important**: After deploying, update the Slack app's slash command URLs to point to `/webhooks/slack/slash-command`.

## Slack App Manifest Updates

Add to OAuth scopes:

```yaml
oauth_config:
  scopes:
    bot:
      - links:read     # Read URLs in messages
      - channels:history # Read message history (for retroactive processing)
```

Add to event subscriptions:

```yaml
event_subscriptions:
  request_url: https://your-service-url/webhooks/slack/events
  bot_events:
    - message.channels
    - message.groups    # For private channels
    - link_shared      # Alternative to message events
```

## Example User Experience

### Before

```
User: Check out this PR: https://github.com/acme/app/pull/123
[No reaction from bot]

Later: Bot posts duplicate: "🐜 [acme/app#123: Fix critical bug]"
```

### After

```
User A: Check out this PR: https://github.com/acme/app/pull/123
Bot adds: ✅ (if already approved)

User B: I'm also looking at https://github.com/acme/app/pull/123
Bot adds: ✅ (same reaction)

Later: Webhook arrives for a new review
- Bot posts ONE notification: "🐜 [acme/app#123: Fix critical bug]"
- Bot adds 🔄 reaction to ALL three messages (User A's, User B's, and its own)

All PR links in the channel stay synchronized with the latest status!
```

This design provides a seamless experience where all PR links get consistent status tracking, regardless of how they're shared.

