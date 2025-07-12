# Specific Concurrency Issues in GitHub-Slack Notifier

## Critical Issues Found in Code

### 1. Duplicate PR Notifications (CRITICAL)
**Location**: `internal/handlers/webhook_worker.go:269-366` (handlePROpened)
```go
// No check if message already exists before creating
message := &models.Message{...}
err = h.firestoreService.CreateMessage(ctx, message)
```
**Problem**: No verification if a message already exists for this PR before sending to Slack and creating in Firestore.
**Scenario**: GitHub retries webhook → duplicate Slack notifications

### 2. No Deduplication at Ingress (HIGH)
**Location**: `internal/handlers/github.go:86-95`
```go
job := &models.WebhookJob{
    ID: uuid.New().String(), // Always creates new ID
    DeliveryID: deliveryID,  // GitHub's unique ID not used for dedup
    ...
}
```
**Problem**: GitHub Delivery ID is stored but not used to prevent duplicate processing
**Impact**: Same webhook processed multiple times

### 3. No Task Name Deduplication (HIGH)
**Location**: `internal/services/cloud_tasks.go:82-101`
```go
task := &cloudtaskspb.Task{
    // No custom Name field set - Cloud Tasks generates random name
    MessageType: &cloudtaskspb.Task_HttpRequest{...}
}
```
**Problem**: Could use delivery ID as task name for idempotency, but doesn't
**Impact**: Duplicate tasks in queue for same webhook

### 4. Race Condition in Message Updates (HIGH)
**Location**: `internal/handlers/webhook_worker.go:251-266` (processPullRequestReviewEvent)
```go
message, err := h.firestoreService.GetMessage(ctx, ...)  // Read
err = h.slackService.AddReaction(ctx, ...)               // External call
message.LastStatus = "review_" + payload.Review.State     // Modify
err = h.firestoreService.UpdateMessage(ctx, message)      // Write
```
**Problem**: Non-atomic read-modify-write pattern
**Scenario**: Two reviews arrive simultaneously → last writer wins

### 5. Full Document Overwrites (MEDIUM)
**Location**: `internal/services/firestore.go:202`
```go
_, err := fs.client.Collection("messages").Doc(message.ID).Set(ctx, message)
```
**Problem**: `Set()` overwrites entire document, not specific fields
**Impact**: Concurrent updates to different fields lost

### 6. No Slack Rate Limiting (HIGH)
**Location**: `internal/services/slack.go:33`
```go
_, timestamp, err := s.client.PostMessage(channel, slack.MsgOptionText(text, false))
```
**Problem**: Direct API calls without rate limiting
**Impact**: 429 errors during bursts, failed notifications

### 7. CreateRepo Without Existence Check (LOW)
**Location**: `internal/services/firestore.go:247`
```go
_, err := fs.client.Collection("repos").Doc(fs.encodeRepoName(repo.ID)).Set(ctx, repo)
```
**Problem**: Will overwrite existing repo if two admins register simultaneously
**Impact**: Configuration loss

### 8. No Circuit Breaker Pattern (MEDIUM)
**Locations**: All Slack API calls and Firestore operations
**Problem**: No protection against cascading failures
**Impact**: Retry storms can overwhelm recovering services

## Code-Level Fixes Needed

### Fix 1: Implement Idempotency for PR Opens
```go
// In handlePROpened, before sending to Slack:
existingMessage, err := h.firestoreService.GetMessage(ctx, 
    payload.Repository.FullName, payload.PullRequest.Number)
if existingMessage != nil {
    log.Info(ctx, "Message already exists for PR, skipping")
    return nil
}
```

### Fix 2: Use GitHub Delivery ID for Deduplication
```go
// In Cloud Tasks service:
task := &cloudtaskspb.Task{
    Name: fmt.Sprintf("%s/tasks/github-%s", queuePath, job.DeliveryID),
    ...
}
```

### Fix 3: Use Firestore Transactions
```go
// For message creation:
err := fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
    // Check if exists
    ref := fs.client.Collection("messages").
        Where("repo_full_name", "==", repoFullName).
        Where("pr_number", "==", prNumber)
    // If not exists, create
    // Return early if exists
})
```

### Fix 4: Implement Field-Level Updates
```go
// Instead of Set(), use Update() for specific fields:
_, err := fs.client.Collection("messages").Doc(message.ID).Update(ctx, []firestore.Update{
    {Path: "last_status", Value: newStatus},
    {Path: "updated_at", Value: time.Now()},
})
```

### Fix 5: Add Slack Rate Limiter
```go
// Implement a rate limiter:
type RateLimitedSlackService struct {
    client      *slack.Client
    rateLimiter *rate.Limiter // 1 req/sec per Slack limits
}
```

## Immediate Recommendations

1. **Add duplicate check in handlePROpened** - Prevent duplicate notifications
2. **Use Cloud Tasks task naming** - Leverage built-in deduplication
3. **Implement Firestore transactions** - For atomic operations
4. **Add Slack rate limiting** - Prevent 429 errors
5. **Use Update() instead of Set()** - Prevent data loss

## Testing Recommendations

1. **Concurrent webhook test**: Send same webhook 10 times simultaneously
2. **Race condition test**: Send PR open + review within 100ms
3. **Load test**: Send 100 PR events in 1 second
4. **Failure injection**: Simulate Slack API failures during retries