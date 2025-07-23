# PR Bot Technical Specification

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
    ID          string    // Repository full name (document ID)
    SlackTeamID string    // Slack workspace/team ID
    Enabled     bool      // Repository active status
    CreatedAt   time.Time
}
```

### Cloud Tasks Job Model

```go
// Job structure for all async processing
type Job struct {
    ID      string          // Job UUID
    Type    string          // Job type (github_webhook, manual_pr_link)
    TraceID string          // Request trace ID
    Payload json.RawMessage // Type-specific payload
}

// GitHub webhook job payload
type WebhookJob struct {
    ID         string          // Job UUID
    EventType  string          // GitHub event type
    DeliveryID string          // GitHub delivery ID
    TraceID    string          // Request trace ID
    Payload    []byte          // Raw webhook payload
    ReceivedAt time.Time
    Status     string          // queued/processing/completed/failed
    RetryCount int
}

// Manual PR link job payload
type ManualLinkJob struct {
    ID             string // Job UUID
    PRNumber       int    // PR number
    RepoFullName   string // Repository full name
    SlackChannel   string // Slack channel ID
    SlackMessageTS string // Slack message timestamp
    TraceID        string // Request trace ID
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

#### `POST /jobs/process`

Job processor endpoint called by Cloud Tasks for all async work.

**Headers:**

- Cloud Tasks authentication headers

**Request Body:**

```json
{
  "id": "uuid",
  "type": "github_webhook", // or "manual_pr_link"
  "trace_id": "trace-uuid",
  "payload": {} // Type-specific payload (WebhookJob or ManualLinkJob)
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

### Repository Configuration

Repository configurations are managed automatically through the Slack App Home interface:

- Users connect GitHub accounts via OAuth
- Default channels are set through the App Home
- Repository configurations are created automatically when PRs are opened

### Health Check

#### `GET /health`

Returns service health status.

## Event Processing Logic

### Job Processing

All async work is processed through a job system:

1. **Fast Path**: Ingress endpoints create Job objects and queue to Cloud Tasks
2. **Slow Path**: JobProcessor routes jobs to appropriate handlers based on job type
3. **Domain Handlers**: GitHubHandler and SlackHandler contain domain-specific business logic

### GitHub Events

#### PR Opened

1. Validate webhook signature
2. Check if PR is draft → skip if true
3. Create WebhookJob wrapped in Job and queue to Cloud Tasks
4. JobProcessor routes to GitHubHandler which processes:
   - Look up author in users collection
   - Determine target channel (annotated channel → user default)
   - Post Slack message
   - Store message info in Firestore

#### Review Submitted

1. Validate and queue to Cloud Tasks
2. JobProcessor routes to GitHubHandler which processes:
   - Find existing Slack messages across all channels
   - Sync reactions based on review state:
     - ✅ approved
     - 🔄 changes_requested
     - 💬 commented
   - Handle dismissed reviews by removing all review reactions

#### PR Closed

1. Validate and queue to Cloud Tasks
2. JobProcessor routes to GitHubHandler which processes:
   - Find existing Slack messages across all channels
   - Add reaction to all tracked messages:
     - 🎉 if merged
     - ❌ if closed without merge

### Error Handling

- **Retryable errors**: Network issues, rate limits, timeouts
- **Non-retryable errors**: Invalid payloads, missing users, validation failures, unsupported job types
- Cloud Tasks handles retry logic with exponential backoff
- JobProcessor provides error handling and retry logic
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
JOB_PROCESSOR_URL        # Full URL to /jobs/process endpoint
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
        - name: JOB_PROCESSOR_URL
          value: "https://{service-url}/jobs/process"
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
