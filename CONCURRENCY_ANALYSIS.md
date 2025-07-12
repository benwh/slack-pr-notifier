# Concurrency Analysis: GitHub-Slack Notifier

## Overview
This service processes webhooks from GitHub and Slack concurrently. Multiple webhooks can arrive simultaneously for the same PR, different PRs, or from different sources entirely. This analysis identifies potential concurrency issues and their impact.

## Architecture Review

### Fast Path (Webhook Ingress)
- **Endpoint**: `POST /webhooks/github`
- **Processing**: Minimal - validates signature, creates WebhookJob, queues to Cloud Tasks
- **Concurrency**: High - multiple GitHub webhooks can arrive simultaneously

### Slow Path (Worker Processing)
- **Endpoint**: `POST /process-webhook`
- **Processing**: Complex - reads/writes Firestore, sends Slack messages
- **Concurrency**: Controlled by Cloud Tasks, but still concurrent

## Potential Concurrency Issues

### 1. Duplicate PR Notifications
**Scenario**: GitHub sometimes sends duplicate webhooks for the same event
- Multiple workers process the same PR event simultaneously
- Both read Firestore, see no existing message, both send to Slack
- Result: Duplicate Slack messages for the same PR

**Impact**: High - User experience degradation

### 2. Lost Updates in Firestore
**Scenario**: Two PR events for the same PR arrive close together
- Worker 1: Reads PR state, processes review event
- Worker 2: Reads PR state (before Worker 1 writes), processes comment event
- Worker 1: Writes updated state
- Worker 2: Writes updated state (overwriting Worker 1's changes)
- Result: Lost review information

**Impact**: Medium - Incorrect PR state tracking

### 3. Slack API Rate Limiting
**Scenario**: Many PRs updated simultaneously (e.g., mass CI run)
- Multiple workers send Slack messages concurrently
- Slack API has rate limits (1 message per second per channel)
- Result: 429 errors, failed notifications

**Impact**: High - Failed notifications

### 4. Message Ordering Issues
**Scenario**: PR opened, then immediately commented on
- Comment webhook processed before open webhook (due to queue delays)
- Slack shows comment notification before PR open notification
- Result: Confusing message order

**Impact**: Medium - User confusion

### 5. Thread Safety in Shared Resources
**Scenario**: Multiple goroutines accessing shared in-memory state
- If using any in-memory caches or shared state
- Result: Data races, crashes, incorrect behavior

**Impact**: Critical - Service instability

### 6. Concurrent User Configuration Updates
**Scenario**: User runs `/notify-link` while PR notification is being processed
- Worker reads user config
- User updates config via Slack command
- Worker sends notification to old channel
- Result: Notification sent to wrong channel

**Impact**: Low - Temporary inconsistency

### 7. Repository Registration Conflicts
**Scenario**: Multiple admins register the same repository simultaneously
- Both check if repo exists (it doesn't)
- Both create new repo documents
- Result: Duplicate repo entries or one fails

**Impact**: Low - Admin operation

### 8. Cloud Tasks Retry Storms
**Scenario**: Transient Firestore/Slack outage causes failures
- Cloud Tasks retries failed jobs
- When service recovers, flood of retries
- Result: Thundering herd, service overload

**Impact**: High - Service availability

### 9. Webhook Signature Validation Race
**Scenario**: Repository webhook secret updated during processing
- Webhook arrives with signature from old secret
- Secret updated in Firestore
- Validation might use new secret against old signature
- Result: Valid webhooks rejected

**Impact**: Low - Rare edge case

### 10. Memory Exhaustion from Concurrent Requests
**Scenario**: Large PR with many files/comments
- Multiple large PR webhooks processed simultaneously
- Each loads full PR data into memory
- Result: Out of memory errors

**Impact**: Medium - Service crashes

## Risk Matrix

| Issue | Likelihood | Impact | Risk Level |
|-------|------------|---------|------------|
| Duplicate PR Notifications | High | High | Critical |
| Lost Firestore Updates | Medium | Medium | High |
| Slack Rate Limiting | High | High | Critical |
| Message Ordering | Medium | Medium | Medium |
| Thread Safety | Low | Critical | Medium |
| Config Update Races | Low | Low | Low |
| Repo Registration Conflicts | Low | Low | Low |
| Retry Storms | Medium | High | High |
| Webhook Validation Race | Low | Low | Low |
| Memory Exhaustion | Medium | Medium | Medium |

## Recommendations

### Immediate Actions
1. Implement idempotency keys for PR notifications
2. Add Slack API rate limiting/queuing
3. Use Firestore transactions for critical updates
4. Add request deduplication at ingress

### Medium-term Improvements
1. Implement distributed locking for PR state updates
2. Add message ordering guarantees
3. Implement circuit breakers for external services
4. Add concurrent request limits

### Long-term Architecture
1. Consider event sourcing for PR state
2. Implement CQRS pattern for read/write separation
3. Add caching layer with proper invalidation
4. Consider using Pub/Sub for better ordering guarantees