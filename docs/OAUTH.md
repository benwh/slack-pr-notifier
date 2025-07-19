# GitHub OAuth Authentication

This document covers the GitHub OAuth implementation for secure user authentication.

## Overview

The system uses GitHub OAuth to securely link Slack users to their GitHub accounts. This replaced the previous trust-based system where users could claim any GitHub username.

## OAuth Flow

1. User runs `/notify-link` in Slack
2. Bot generates a secure OAuth URL with CSRF protection
3. User clicks link and authorizes via GitHub
4. GitHub redirects back with user information
5. System verifies and creates verified user account link

## Security Features

- **CSRF Protection**: Cryptographically secure state parameters
- **Time-Limited**: 15-minute expiration on OAuth requests
- **Verified Ownership**: Users can only link accounts they control
- **No Token Storage**: OAuth used only for identity verification, not API access

## User Experience

### Linking Account
```
User: /notify-link
Bot: 🔗 Link Your GitHub Account

Click this link to securely connect your GitHub account:
[Connect GitHub Account](https://example.com/auth/github/link?state=abc123)

This link expires in 15 minutes for security.
```

### Status Check
```
User: /notify-status
Bot: 📊 Your Configuration:
• GitHub: octocat (✅ Verified)
• Default Channel: <#general>
```

### Unlinking Account
```
User: /notify-unlink
Bot: ✅ Your GitHub account has been disconnected. You can use /notify-link to connect a different account.
```

## Technical Implementation

### OAuth State Management
- State stored in `oauth_states` Firestore collection
- 15-minute TTL for automatic cleanup
- Cryptographically secure random generation
- One-time use (deleted after successful validation)

### User Data Storage
- `GitHubUserID`: Numeric GitHub user ID (more stable than username)
- `GitHubUsername`: Current GitHub username
- `Verified`: Boolean indicating OAuth verification status
- Legacy users show as "⚠️ Unverified (legacy)"

### Endpoints
- `GET /auth/github/link?state={id}` - Initiate OAuth flow
- `GET /auth/github/callback` - Handle OAuth callback

## Architecture Decision: GitHub App vs OAuth App

We chose to extend our existing **GitHub App** with OAuth capabilities rather than creating a separate OAuth App.

### Pros of GitHub App Approach
- Single app to manage (webhooks + user auth)
- Consistent with existing webhook infrastructure  
- GitHub Apps support both webhook events and user authorization
- Simpler deployment (fewer secrets to manage)

### Cons Considered
- Slightly more complex configuration (need to enable OAuth on existing app)
- Mixing webhook and auth concerns in one app

### Alternative Considered
Separate OAuth App would have provided cleaner separation of concerns but added operational complexity.

## Migration from Legacy System

Existing users with manually-entered GitHub usernames:
- Continue to work but show as "unverified (legacy)"
- Encouraged to re-link via OAuth for verification
- No forced migration - graceful coexistence

## Troubleshooting

### Common Issues

**"Invalid or expired state"**
- OAuth link has expired (>15 minutes)
- User needs to run `/notify-link` again

**"Authorization failed"**
- User denied authorization on GitHub
- User needs to accept permissions

**"GitHub API request failed"**
- Network issues or GitHub API unavailable
- Retry the OAuth flow

### Debugging

OAuth state and user creation is logged with structured logging for debugging purposes.