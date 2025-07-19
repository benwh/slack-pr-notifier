# GitHub-Slack Notifier Technical Specification

## Architecture Overview

The service uses a two-phase async processing model:
1. **Fast Path**: GitHub webhook ingress validates and queues to Cloud Tasks (< 100ms)
2. **Slow Path**: Worker processes business logic, updates Firestore, sends Slack notifications

## Authentication & Security

### GitHub Webhook Authentication

- HMAC-SHA256 signature validation using `X-Hub-Signature-256` header
- Utilizes official `github.com/google/go-github` library for validation
- Webhook secret stored in environment variables

### Slack Authentication

- Bot tokens (xoxb-) for message posting and slash commands
- Request signature validation using `X-Slack-Signature` header
- Required scopes: `channels:read`, `chat:write`, `commands`

## Database Schema (Firestore)

### Collections

#### `users`

```go
type User struct {
    ID             string    // GitHub username (document ID)
    SlackUserID    string    // Slack user identifier
    SlackTeamID    string    // Slack workspace ID
    DefaultChannel string    // Default notification channel
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

#### `messages`

```go
type Message struct {
    ID                   string    // Auto-generated UUID
    PRNumber            int       // GitHub PR number
    RepoFullName        string    // Format: "org/repo"
    SlackChannel        string    // Target Slack channel
    SlackMessageTS      string    // Slack message timestamp
    GitHubPRURL         string    // Full PR URL
    AuthorGitHubUsername string    // PR author's GitHub username
    CreatedAt           time.Time
    LastStatus          string    // Latest PR status
}
```

#### `repos`

```go
type Repo struct {
    ID             string    // Repository full name (document ID)
    DefaultChannel string    // Default notification channel
    WebhookSecret  string    // Encrypted webhook secret
    Enabled        bool      // Repository active status
    CreatedAt      time.Time
}
```

### Cloud Tasks Job Model

```go
type WebhookJob struct {
    ID          string          // Job UUID
    Event       string          // GitHub event type
    Payload     json.RawMessage // Raw webhook payload
    Repository  string          // Repository full name
    ReceivedAt  time.Time      
    RetryCount  int            // Retry attempt counter
    LastError   string         // Last processing error
    ProcessedAt *time.Time     // Completion timestamp
    Status      string         // pending/processing/completed/failed
}
```

## HTTP APIs

### Webhook Endpoints

#### `POST /webhooks/github`

Fast ingress endpoint for GitHub webhooks.

**Headers:**
- `X-Hub-Signature-256`: HMAC signature (required)
- `X-GitHub-Event`: Event type (required)

**Response:**
- `200 OK`: Event queued successfully
- `400 Bad Request`: Invalid payload or unsupported event
- `401 Unauthorized`: Invalid signature

#### `POST /process-webhook`

Internal worker endpoint called by Cloud Tasks.

**Headers:**
- Cloud Tasks authentication headers

**Request Body:**
```json
{
  "job_id": "uuid",
  "event": "pull_request",
  "payload": {}, // GitHub webhook payload
  "repository": "org/repo",
  "received_at": "2024-01-01T00:00:00Z"
}
```

#### `POST /webhooks/slack`

Handles Slack slash commands.

**Headers:**
- `X-Slack-Signature`: Request signature
- `X-Slack-Request-Timestamp`: Request timestamp

**Commands:**
- `/notify-channel #channel` - Set default notification channel
- `/notify-link github-username` - Link GitHub account
- `/notify-status` - View current configuration

### Admin APIs

#### `POST /api/repos`

Register a repository (requires admin API key).

**Headers:**
- `X-API-Key`: Admin API key

**Request:**
```json
{
  "repo_full_name": "org/repo",
  "default_channel": "#engineering",
  "webhook_secret": "secret"
}
```

### Health Check

#### `GET /health`

Returns service health status.

## Event Processing Logic

### GitHub Events

#### PR Opened
1. Validate webhook signature
2. Check if PR is draft → skip if true
3. Create WebhookJob and queue to Cloud Tasks
4. Worker processes:
   - Look up author in users collection
   - Determine target channel (user default → repo default)
   - Post Slack message
   - Store message info in Firestore

#### Review Submitted
1. Validate and queue to Cloud Tasks
2. Worker processes:
   - Find existing Slack message
   - Add reaction based on review state:
     - ✅ approved
     - 🔄 changes_requested
     - 💬 commented
   - Update message status

#### PR Closed
1. Validate and queue to Cloud Tasks
2. Worker processes:
   - Find existing Slack message
   - Add reaction:
     - 🎉 if merged
     - ❌ if closed without merge
   - Update final status

### Error Handling

- **Retryable errors**: Network issues, rate limits, timeouts
- **Non-retryable errors**: Invalid payloads, missing users, validation failures
- Cloud Tasks handles retry logic with exponential backoff
- "Already exists" errors (e.g., duplicate reactions) are gracefully ignored

## Slack Message Format

```
🐜 <{pr_url}|{pr_title} by {pr_author}>
```

Simple, concise format with:
- Ant emoji prefix (🐜)
- Hyperlinked PR title
- Author attribution

## Environment Variables

### Required

```bash
# Core Configuration
FIRESTORE_PROJECT_ID      # GCP project ID
FIRESTORE_DATABASE_ID     # Firestore database ID
SLACK_BOT_TOKEN          # Slack bot token (xoxb-)
GITHUB_WEBHOOK_SECRET    # GitHub webhook validation secret
SLACK_SIGNING_SECRET     # Slack request validation secret
API_ADMIN_KEY           # Admin API authentication key

# Async Processing
GOOGLE_CLOUD_PROJECT     # GCP project for Cloud Tasks
WEBHOOK_WORKER_URL       # Full URL to /process-webhook endpoint
```

### Optional

```bash
# Server Configuration
PORT                              # Server port (default: 8080)
SERVER_READ_TIMEOUT              # Request read timeout (default: 10s)
SERVER_WRITE_TIMEOUT             # Response write timeout (default: 10s)
SERVER_IDLE_TIMEOUT              # Keep-alive timeout (default: 120s)
SHUTDOWN_TIMEOUT                 # Graceful shutdown timeout (default: 10s)

# Cloud Tasks Configuration
CLOUD_TASKS_QUEUE                # Queue name (default: webhook-processing)
WEBHOOK_PROCESSING_TIMEOUT       # Job timeout (default: 5m)
GCP_REGION                       # Deployment region (default: europe-west1)
```

## Deployment Configuration

### Cloud Run Service

```yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: github-slack-notifier
spec:
  template:
    metadata:
      annotations:
        run.googleapis.com/cpu-throttling: "false"
    spec:
      containerConcurrency: 1000
      timeoutSeconds: 300
      containers:
      - image: {region}-docker.pkg.dev/{project}/docker/github-slack-notifier
        resources:
          limits:
            cpu: "2"
            memory: "2Gi"
        env:
        - name: WEBHOOK_WORKER_URL
          value: "https://{service-url}/process-webhook"
```

### Cloud Tasks Queue

```yaml
name: projects/{project}/locations/{region}/queues/webhook-processing
rateLimits:
  maxDispatchesPerSecond: 100
  maxConcurrentDispatches: 1000
retryConfig:
  maxAttempts: 10
  minBackoff: 1s
  maxBackoff: 600s
  maxDoublings: 5
```

### Firestore Indexes

```yaml
indexes:
- collectionGroup: messages
  fields:
  - fieldPath: RepoFullName
    order: ASCENDING
  - fieldPath: PRNumber
    order: ASCENDING

- collectionGroup: users
  fields:
  - fieldPath: SlackUserID
    order: ASCENDING
```

## Monitoring & Observability

- **Structured Logging**: All logs include trace IDs for request correlation
- **Cloud Run Metrics**: Automatic metrics for latency, errors, and scaling
- **Error Categories**: Errors tagged as retryable/non-retryable for monitoring
- **Health Endpoint**: `/health` for uptime monitoring

## Security Considerations

- All webhook signatures validated before processing
- Admin endpoints require API key authentication
- Webhook worker endpoint should be restricted to Cloud Tasks service account
- No secrets logged or exposed in error messages
- HTTPS enforced by Cloud Run

## Not Implemented (from original design)

The following features from the original specification are not currently implemented:

- PR description annotations for channel routing (`@slack-channel: #channel`)
- User dismissal of notifications via emoji reactions
- Thread-based conversations
- Custom emoji configuration via environment variables
- Showing recent PRs in `/notify-status` command
- Manual retry queue (handled by Cloud Tasks instead)