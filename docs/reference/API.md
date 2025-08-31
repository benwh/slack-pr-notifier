# API Reference

This document covers all HTTP endpoints provided by PR Bot.

## HTTP Endpoints

### Webhook Endpoints

| Method | Path | Description | Authentication |
|--------|------|-------------|----------------|
| `POST` | `/webhooks/github` | GitHub webhook fast ingress (queues to Cloud Tasks) | Webhook signature |
| `POST` | `/jobs/process` | Job processor (called by Cloud Tasks for all async work) | Internal only |
| `POST` | `/webhooks/slack/interactions` | Slack interactive components processor (App Home) | Slack signature |
| `POST` | `/webhooks/slack/events` | Slack Events API processor (detects manual PR links) | Slack signature |

### OAuth Endpoints

| Method | Path | Description | Parameters |
|--------|------|-------------|------------|
| `GET` | `/auth/github/link` | Initiate GitHub OAuth flow (redirects to GitHub) | `state` (required) |
| `GET` | `/auth/github/callback` | Handle GitHub OAuth callback | `code`, `state` |

### Admin Endpoints

| Method | Path | Description | Authentication |
|--------|------|-------------|----------------|
| `GET` | `/health` | Health check | None |

**⚠️ Security Note**: The `/jobs/process` endpoint should not be exposed publicly - it's designed to be called only by Google Cloud Tasks for processing all queued jobs.

## Slack App Home

User configuration is handled through the Slack App Home interface instead of slash commands.

### App Home Features

**GitHub Account Management:**

- Connect GitHub account via OAuth
- Disconnect GitHub account
- View verification status

**Channel Configuration:**

- Set default notification channel
- View current channel setting

**Status Display:**

- Current GitHub account (if connected)
- Default notification channel (if set)
- Account verification status

### Interactive Components

The App Home uses Slack's Block Kit interactive components:

- **Button Actions**: Connect/Disconnect GitHub, Set Channel, Refresh View
- **Modal Dialogs**: OAuth link display, Channel selection
- **Channel Selectors**: Choose default notification channel

All interactions are processed via the `/webhooks/slack/interactions` endpoint.

## Webhook Payloads

### GitHub Webhooks

The system processes these GitHub webhook events:

- `pull_request` - PR opened/closed/merged
- `pull_request_review` - PR reviews submitted/dismissed

Events are queued via Cloud Tasks for reliable processing with fan-out to individual workspaces.

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

- Request signature validation for events and interactions
- Timestamp validation (max 5 minutes age)

### OAuth Endpoints

- CSRF protection via state parameters
- No additional authentication (public endpoints)
