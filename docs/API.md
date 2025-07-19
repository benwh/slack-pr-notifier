# API Reference

This document covers all HTTP endpoints and Slack commands provided by the GitHub-Slack Notifier.

## HTTP Endpoints

### Webhook Endpoints

| Method | Path | Description | Authentication |
|--------|------|-------------|----------------|
| `POST` | `/webhooks/github` | GitHub webhook fast ingress (queues to Cloud Tasks) | Webhook signature |
| `POST` | `/process-webhook` | Internal webhook worker (called by Cloud Tasks) | Internal only |
| `POST` | `/process-manual-link` | Internal manual PR link worker (called by Cloud Tasks) | Internal only |
| `POST` | `/webhooks/slack/slash-command` | Slack slash command processor | Slack signature |
| `POST` | `/webhooks/slack/events` | Slack Events API processor (detects manual PR links) | Slack signature |

### OAuth Endpoints

| Method | Path | Description | Parameters |
|--------|------|-------------|------------|
| `GET` | `/auth/github/link` | Initiate GitHub OAuth flow (redirects to GitHub) | `state` (required) |
| `GET` | `/auth/github/callback` | Handle GitHub OAuth callback | `code`, `state` |

### Admin Endpoints

| Method | Path | Description | Authentication |
|--------|------|-------------|----------------|
| `POST` | `/api/repos` | Repository registration | `X-API-Key` header |
| `GET` | `/health` | Health check | None |

**⚠️ Security Note**: The `/process-webhook` and `/process-manual-link` endpoints should not be exposed publicly - they're designed to be called only by Google Cloud Tasks.

## Slack Commands

### `/notify-channel <channel>`

Set your default notification channel for PRs.

**Usage:**
```
/notify-channel #engineering
/notify-channel engineering
/notify-channel C1234567890
```

**Response:**
```
✅ Default notification channel set to #engineering
```

### `/notify-link`

Get a secure OAuth link to connect your GitHub account.

**Usage:**
```
/notify-link
```

**Response:**
```
🔗 Link Your GitHub Account

Click this link to securely connect your GitHub account:
[Connect GitHub Account](https://example.com/auth/github/link?state=abc123)

This link expires in 15 minutes for security.
```

**Legacy Usage:**
If a username is provided, explains the new OAuth flow:
```
/notify-link octocat
```

**Response:**
```
🔗 New OAuth Flow Available!

We've upgraded to secure GitHub OAuth authentication. The /notify-link command no longer requires a username.

Simply run /notify-link to get your personalized OAuth link!
```

### `/notify-unlink`

Disconnect your GitHub account from Slack.

**Usage:**
```
/notify-unlink
```

**Response:**
```
✅ Your GitHub account has been disconnected. You can use /notify-link to connect a different account.
```

### `/notify-status`

View your current configuration and verification status.

**Usage:**
```
/notify-status
```

**Response:**
```
📊 Your Configuration:
• GitHub: octocat (✅ Verified)
• Default Channel: #engineering
```

**Verification States:**
- `✅ Verified` - Account linked via OAuth
- `⚠️ Unverified (legacy)` - Account linked via old manual system
- `Not linked` - No GitHub account connected

## Webhook Payloads

### GitHub Webhooks

The system processes these GitHub webhook events:

- `pull_request` - PR opened/closed/merged
- `pull_request_review` - PR reviews submitted/dismissed

Events are queued via Cloud Tasks for reliable processing.

### Slack Events

The system processes these Slack events:

- `message.channels` - Detects manual PR links in public channels

## Error Responses

All endpoints return JSON error responses:

```json
{
  "error": "Error Type",
  "message": "Human-readable error description"
}
```

Common HTTP status codes:
- `400` - Bad Request (invalid payload, missing parameters)
- `401` - Unauthorized (invalid API key, webhook signature)
- `404` - Not Found
- `500` - Internal Server Error

## Rate Limiting

No explicit rate limiting is implemented, but the system is designed to handle:
- GitHub webhook bursts via async processing
- Slack command rate limits (handled by Slack)
- Cloud Tasks processing limits

## Authentication

### GitHub Webhooks
- HMAC-SHA256 signature validation
- Secret configured per repository

### Slack Requests  
- Request signature validation
- Timestamp validation (max 5 minutes age)

### Admin Endpoints
- Simple API key authentication
- Header: `X-API-Key: your-admin-key`

### OAuth Endpoints
- CSRF protection via state parameters
- No additional authentication (public endpoints)