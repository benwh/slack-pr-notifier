# GitHub-Slack Notifier Technical Specification

## Authentication & Security

### GitHub Webhook Authentication
- Use webhook secret validation (HMAC-SHA256)
- Verify `X-Hub-Signature-256` header on all incoming requests
- Secret stored in environment variables

### Slack Authentication
- Use Slack Bot tokens (xoxb-) for message posting
- OAuth 2.0 flow for user identity linking
- Scopes required: `chat:write`, `chat:write.public`, `reactions:read`

### API Authentication
- Service-to-service: API keys for admin endpoints
- User endpoints: JWT tokens after Slack OAuth

## Database Schema (Firestore)

### Collections

#### `users`
```json
{
  "id": "github_user_id",
  "github_username": "string",
  "slack_user_id": "string",
  "slack_team_id": "string",
  "default_channel": "string",
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

#### `messages`
```json
{
  "id": "auto_generated",
  "pr_number": "integer",
  "repo_full_name": "string",
  "slack_channel": "string",
  "slack_message_ts": "string",
  "github_pr_url": "string",
  "author_github_id": "string",
  "created_at": "timestamp",
  "last_status": "string"
}
```

#### `repos`
```json
{
  "id": "repo_full_name",
  "default_channel": "string",
  "webhook_secret": "encrypted_string",
  "enabled": "boolean",
  "created_at": "timestamp"
}
```

## HTTP APIs

### Webhook Endpoints

#### `POST /webhooks/github`
Receives GitHub webhook events.

**Headers:**
- `X-Hub-Signature-256`: HMAC signature
- `X-GitHub-Event`: Event type

**Request Body:** GitHub webhook payload

**Response:**
- `200 OK`: Event processed
- `202 Accepted`: Event queued (for retries)
- `400 Bad Request`: Invalid payload
- `401 Unauthorized`: Invalid signature

### Configuration APIs

#### `POST /api/users/link`
Link GitHub and Slack accounts.

**Request:**
```json
{
  "slack_code": "oauth_code_from_slack",
  "github_username": "string"
}
```

**Response:**
```json
{
  "success": true,
  "user_id": "github_user_id"
}
```

#### `GET /api/users/me`
Get current user's configuration.

**Headers:**
- `Authorization: Bearer {jwt_token}`

**Response:**
```json
{
  "github_username": "string",
  "slack_username": "string",
  "default_channel": "string"
}
```

#### `PUT /api/users/me/channel`
Update default channel.

**Request:**
```json
{
  "default_channel": "string"
}
```

#### `POST /api/repos`
Register a repository (admin only).

**Headers:**
- `X-API-Key: {admin_api_key}`

**Request:**
```json
{
  "repo_full_name": "org/repo",
  "default_channel": "#engineering",
  "webhook_secret": "string"
}
```

## Event Processing Logic

### PR Opened Event
1. Check if PR is draft → ignore
2. Look up author in users collection
3. Determine target channel (PR annotation > user default > repo default)
4. Post Slack message with PR details
5. Store message info in database

### Review Submitted Event
1. Look up existing message in database
2. Add emoji based on review state:
   - ✅ for approved
   - 🔄 for changes requested
   - 💬 for commented
3. Update message status in database

### PR Closed Event
1. Look up existing message in database
2. Add emoji:
   - 🎉 if merged
   - ❌ if closed without merge
3. Update final status in database

## Slack Message Format

### Initial PR Message
```
🔗 *New PR in {repo_name}*
*Title:* {pr_title}
*Author:* {author_name}
*Description:* {first_100_chars}...
<{pr_url}|View Pull Request>
```

### PR Description Annotation
Users can add `@slack-channel: #channel-name` in PR description to override routing.

## Operational Details

### Retry Logic
- Exponential backoff for failed Slack API calls
- Max 3 retries with 1s, 2s, 4s delays
- Dead letter queue for persistent failures

### Rate Limiting
- Respect Slack rate limits (1 message per second per channel)
- Queue messages if rate limited
- GitHub webhooks have 30s timeout - respond quickly and process async

### Monitoring
- Cloud Run metrics for request latency
- Custom metrics for:
  - Webhook events received
  - Slack messages sent
  - User mappings created
  - Errors by type

### Emoji Configuration
Default emoji set (customizable via environment):
- `EMOJI_APPROVED`: ✅
- `EMOJI_CHANGES_REQUESTED`: 🔄
- `EMOJI_COMMENTED`: 💬
- `EMOJI_MERGED`: 🎉
- `EMOJI_CLOSED`: ❌
- `EMOJI_DISMISSED`: 👋 (user reaction to dismiss)

## Environment Variables
```
GITHUB_WEBHOOK_SECRET
SLACK_BOT_TOKEN
SLACK_CLIENT_ID
SLACK_CLIENT_SECRET
FIRESTORE_PROJECT_ID
API_ADMIN_KEY
JWT_SECRET
PORT (default: 8080)
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
      - image: gcr.io/{project}/github-slack-notifier
        resources:
          limits:
            cpu: "2"
            memory: "2Gi"
```

### Firestore Indexes
```yaml
indexes:
- collectionGroup: messages
  fields:
  - fieldPath: repo_full_name
    order: ASCENDING
  - fieldPath: pr_number
    order: ASCENDING
    
- collectionGroup: users
  fields:
  - fieldPath: slack_user_id
    order: ASCENDING
```