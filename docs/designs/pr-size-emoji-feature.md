# PR Size Animal Emoji Feature

## Overview

This feature adds animal emojis to Slack PR notifications based on the number of lines changed in the PR. The emoji provides a quick visual indicator of PR complexity/size.

## Animal Emoji Scale

Based on total lines changed (additions + deletions):

| Lines Changed | Animal | Emoji | Description |
|--------------|--------|-------|-------------|
| < 5 | Ant | 🐜 | Tiny change |
| 5-20 | Mouse | 🐭 | Very small change |
| 21-50 | Rabbit | 🐰 | Small change |
| 51-100 | Cat | 🐱 | Small-medium change |
| 101-250 | Dog | 🐕 | Medium change |
| 251-500 | Horse | 🐴 | Medium-large change |
| 501-1000 | Bear | 🐻 | Large change |
| 1001-1500 | Elephant | 🐘 | Very large change |
| 1501-2000 | Dinosaur | 🦕 | Huge change |
| > 2000 | Whale | 🐋 | Massive change |

## Implementation Details

### 1. GitHub API Integration

The GitHub API provides PR statistics in the pull request object:

- `additions`: Number of lines added
- `deletions`: Number of lines deleted
- Total lines changed = `additions + deletions`

### 2. Code Changes Required

#### handlers/github.go - Update GitHubWebhookPayload

Add fields to capture PR size data:

```go
type GitHubWebhookPayload struct {
    // existing fields...
    PullRequest struct {
        // existing fields...
        Additions int `json:"additions"`
        Deletions int `json:"deletions"`
    } `json:"pull_request"`
}
```

Note: GitHub includes these fields in the pull_request webhook payload automatically.

#### services/slack.go

Add method to determine emoji based on PR size:

```go
func (s *SlackService) getPRSizeEmoji(linesChanged int) string {
    switch {
    case linesChanged < 5:
        return "🐜" // ant
    case linesChanged <= 20:
        return "🐭" // mouse
    case linesChanged <= 50:
        return "🐰" // rabbit
    case linesChanged <= 100:
        return "🐱" // cat
    case linesChanged <= 250:
        return "🐕" // dog
    case linesChanged <= 500:
        return "🐴" // horse
    case linesChanged <= 1000:
        return "🐻" // bear
    case linesChanged <= 1500:
        return "🐘" // elephant
    case linesChanged <= 2000:
        return "🦕" // dinosaur
    default:
        return "🐋" // whale
    }
}
```

Update `PostPRMessage` method to accept and use PR size:

```go
func (s *SlackService) PostPRMessage(
    ctx context.Context, channel, repoName, prTitle, prAuthor, prDescription, prURL string, prSize int,
) (string, error) {
    emoji := s.getPRSizeEmoji(prSize)
    text := fmt.Sprintf("%s <%s|%s by %s>", emoji, prURL, prTitle, prAuthor)
    // rest of the method...
}
```

#### handlers/github.go

Update the call to PostPRMessage in handlePROpened method:

```go
// Calculate PR size
prSize := payload.PullRequest.Additions + payload.PullRequest.Deletions

timestamp, err := h.slackService.PostPRMessage(
    ctx,
    targetChannel,
    payload.Repository.Name,
    payload.PullRequest.Title,
    payload.PullRequest.User.Login,
    payload.PullRequest.Body,
    payload.PullRequest.HTMLURL,
    prSize, // New parameter
)
```

### 3. Message Format Examples

Current format:

```
benwh opened a PR: Fix authentication bug
https://github.com/org/repo/pull/123
```

New format option 1 (emoji prefix):

```
🐜 benwh opened a PR: Fix authentication bug
https://github.com/org/repo/pull/123
```

### 4. Edge Cases

- If GitHub API doesn't provide additions/deletions (rare), default to ant emoji
- For draft PRs that might grow, use current size (can update on subsequent events)
- Consider file count as secondary metric if needed in future

### 5. Benefits

- **Quick visual assessment**: Team members can instantly gauge PR complexity
- **Review prioritization**: Larger PRs (🐘🦕🐋) may need more review time
- **Fun element**: Makes PR notifications more engaging
- **No configuration needed**: Works automatically for all PRs

### 6. Implementation Steps Summary

1. Add `Additions` and `Deletions` fields to `GitHubWebhookPayload.PullRequest` struct
2. Create `getPRSizeEmoji` method in `SlackService`
3. Update `PostPRMessage` signature to accept `prSize` parameter
4. Calculate PR size in `handlePROpened` and pass to `PostPRMessage`
5. Update any tests that call `PostPRMessage` to include the new parameter

### 7. Future Enhancements

- Could add configuration to customize emoji ranges per repository
- Could consider files changed count as additional factor
- Could add tooltip/hover text explaining the size category
- Could make emoji ranges configurable via environment variables

