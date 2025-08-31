# Configuration Guide

This document covers GitHub App setup and deployment configuration for the GitHub-Slack Notifier.

## Environment Variables

All configuration is managed through environment variables. See [`.env.example`](../.env.example) for all available configuration options and their descriptions.

## GitHub App Setup

### Creating a New GitHub App

You'll need to create a GitHub App to handle both webhooks and user authentication. Here's how to set it up from scratch:

1. **Navigate to GitHub App Creation**
   - Click your profile picture in the upper-right corner of GitHub
   - Click **Settings** (for personal account) or **Your organizations** → organization **Settings**
   - In the left sidebar, click **Developer settings**
   - Click **GitHub Apps**
   - Click **New GitHub App**

2. **Basic Information**
   - **GitHub App name**: `PR Bot (your-organization)`
   - **Homepage URL**: `https://your-service-url.run.app`
   - **Webhook URL**: `https://your-service-url.run.app/webhooks/github`
   - **Webhook secret**: Generate a secure random string and save it as `GITHUB_WEBHOOK_SECRET`

3. **Repository Permissions**
   - **Pull requests**: Read (required to fetch PR details and review states)
   - **Metadata**: Read (required to access basic repository information)

4. **Subscribe to Events**
   - ✅ `pull_request` (PR opened, closed, merged)
   - ✅ `pull_request_review` (reviews submitted, dismissed)
   - ✅ `installation` (for automatic installation management)

5. **User Authorization (OAuth)**
   - ✅ Enable "Request user authorization (OAuth) during installation"
   - **User authorization callback URL**: `https://your-service-url.run.app/auth/github/callback`

6. **Create the App**
   - Under "Where can this GitHub App be installed?", choose:
     - **Only on this account** (for personal/single organization use)
     - **Any account** (if you plan to distribute the app)
   - Click "Create GitHub App"
   - Note your **App ID** and **Client ID**
   - Generate and download a **Private Key**
   - Generate a **Client Secret**

### Why GitHub App Over OAuth App?

**GitHub Apps are the officially recommended approach** for most integrations. We use a GitHub App instead of an OAuth App because:

**Security & Permissions:**

- **Fine-grained permissions**: Only request access to what's needed (Pull requests: Read, Metadata: Read)
- **Short-lived tokens**: Installation tokens expire after 1 hour (vs OAuth's long-lived tokens)
- **Repository-specific access**: Can be installed on specific repositories rather than all user repositories

**Operational Benefits:**

- **Single app to manage**: Handles both webhooks and user authentication
- **Centralized webhooks**: Built-in webhook management vs configuring individually per repository
- **Better rate limits**: Scale with repository count and organization size
- **Independent operation**: Can function without user interaction after installation

**GitHub's Recommendation:**
Per GitHub's official documentation: "GitHub Apps are preferred over OAuth apps" due to enhanced security and more precise access controls.

**When to use OAuth Apps instead:**

- Enterprise-level resource access (GitHub Apps can't access enterprise objects yet)
- Simple user authentication where broad repository access is acceptable

### Configuring Your Environment

After creating your GitHub App, you'll need to configure several environment variables:

**From your GitHub App settings page, gather:**

- **App ID**: Displayed at the top of your app's settings page
- **Client ID**: Found in the "OAuth 2.0" section
- **Client Secret**: Generated in the "OAuth 2.0" section
- **Private Key**: Downloaded `.pem` file from "Private keys" section

**Configure your `.env` file:**

```bash
# GitHub App Configuration
GITHUB_APP_ID=12345                    # Your App ID
GITHUB_CLIENT_ID=Iv1.abc123def456       # OAuth Client ID
GITHUB_CLIENT_SECRET=your_client_secret  # OAuth Client Secret
GITHUB_WEBHOOK_SECRET=your_webhook_secret # Webhook secret you generated

# Private key in base64 format
# Encode your private key file:
# On macOS/Linux: base64 -i private-key.pem
# On Windows: certutil -encode private-key.pem encoded.txt
GITHUB_PRIVATE_KEY_BASE64=LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQo...
```

### How GitHub App Authentication Works

The application uses the GitHub App for two purposes:

**1. Webhook Processing**

- Receives `pull_request`, `pull_request_review`, and `installation` events
- Validates webhook signatures using the webhook secret
- Processes PR opens, reviews, and installations automatically

**2. API Authentication**

- Authenticates as the GitHub App installation to make API calls
- Fetches PR details when manual links are posted in Slack
- Gets current review states (approved, changes requested, commented)
- Accesses private repositories where your app is installed
- Provides reliable API access with 5,000 requests/hour rate limit

**Installation Management**

- The app automatically discovers installations via `installation.created` webhook events
- No manual installation ID configuration needed
- The `ghinstallation` library handles token generation and renewal automatically

**Security Notes:**

- The private key must be kept secure - never commit it to version control
- The app automatically handles token generation and renewal
- The app must be installed to repositories where you want PR notifications

### Installing Your GitHub App

After creating the GitHub App, you need to install it to your organization or repositories:

1. **Navigate to App Installation**
   - Go to your GitHub App settings page
   - Click "Install App" in the left sidebar
   - Choose your organization or personal account

2. **Select Repository Access**
   - **All repositories**: Install to all current and future repositories
   - **Selected repositories**: Choose specific repositories for PR notifications

3. **Complete Installation**
   - Click "Install" to complete the process
   - The app will automatically receive the `installation.created` webhook event
   - Check your application logs to verify the installation was processed

**Important Notes:**

- You can modify repository access anytime from the GitHub App installation settings
- Each installation generates a separate installation ID (managed automatically)
- The app must be installed to repositories before it can send PR notifications for them
- Private repositories require the app to be explicitly installed with access permissions

### Troubleshooting GitHub App Setup

**Common Issues:**

**1. "Webhook deliveries failing"**

- Verify your webhook URL is accessible: `https://your-service-url.run.app/webhooks/github`
- Check that `GITHUB_WEBHOOK_SECRET` matches what you configured in the GitHub App
- Ensure your service is deployed and running
- Test webhook delivery from your GitHub App settings page

**2. "Installation events not received"**

- Confirm you subscribed to the `installation` event in your GitHub App settings
- Check application logs for "installation.created" events
- Verify webhook signature validation is passing

**3. "API calls failing with authentication errors"**

- Double-check your `GITHUB_APP_ID` matches the App ID shown in GitHub
- Verify your private key is properly base64 encoded
- Ensure the GitHub App is installed to the repositories you're trying to access
- Check that your app has the required permissions (Pull requests: Read, Metadata: Read)

**4. "OAuth flow not working"**

- Verify `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET` are correct
- Check that the OAuth callback URL matches exactly: `https://your-service-url.run.app/auth/github/callback`
- Ensure "Request user authorization (OAuth) during installation" is enabled

**Verification Steps:**

1. **Test Webhook Delivery**:
   - Go to your GitHub App settings → Advanced → Recent Deliveries
   - Create a test PR in a repository where the app is installed
   - Verify webhook events are being delivered with 200 responses

2. **Check Installation Discovery**:
   - Look for log messages like "Installation saved successfully" in your application logs
   - Verify installations are being stored in your Firestore database

3. **Test OAuth Flow**:
   - Visit `https://your-service-url.run.app/auth/github/link?state=test-state`
   - Should redirect to GitHub authorization page

## Slack App Configuration

See [SLACK_SETUP.md](./SLACK_SETUP.md) for complete Slack app setup instructions.

## Database Setup

The application uses Cloud Firestore with automatic collection creation. No manual schema setup is required.

## Deployment Configuration

### Google Cloud Run

Required GCP services:

- Cloud Firestore
- Cloud Tasks
- Artifact Registry
- Cloud Run

### Environment Variables in Production

Ensure all required environment variables from `.env.example` are set in your deployment environment. Never commit secrets to version control.

## Security Considerations

- **Webhook Signatures**: Always validate GitHub webhook signatures
- **OAuth State**: CSRF protection with 15-minute expiration
- **API Keys**: Use strong random strings for admin endpoints
- **Secrets**: Never log or expose secrets in responses
- **HTTPS**: Always use HTTPS in production for OAuth callbacks
- **Cloud Tasks Authentication**: Static secret protects job processing endpoints

### Cloud Tasks Static Secret Authentication

The `/jobs/process` endpoint is protected by a static secret to ensure only Google Cloud Tasks can execute jobs.

**Configuration:**

- `CLOUD_TASKS_SECRET` - Static secret added to Cloud Tasks HTTP headers (required)

**How it works:**

1. Cloud Tasks adds `X-Cloud-Tasks-Secret` header to all job processing requests
2. Job processing endpoint validates the secret matches the configured value
3. Requests without the correct secret are rejected with 401 Unauthorized

**Fan-out Architecture Benefits:**
The system uses a fan-out pattern where GitHub webhook events create individual workspace jobs, providing improved error isolation - if one workspace fails, others can succeed and retry independently.

**Authentication Design Decision:**
We chose static secret authentication over Google Cloud OIDC tokens for practical Cloud Run considerations:

**Static Secret Advantages:**

- **Performance**: Ultra-fast validation (~1-2ms) vs OIDC (~100-500ms cold start penalty)
- **No cold start latency**: No external API calls to fetch Google's public keys
- **Simple debugging**: Clear success/failure without JWT complexity
- **Cloud Run optimized**: No network dependency on Google's cert endpoint
- **Fewer failure modes**: Only secret validation can fail

**OIDC Trade-offs Considered:**

- OIDC provides service account audit trails and automatic key rotation
- However, Cloud Run's frequent cold starts make certificate fetching expensive
- For internal endpoint protection, static secret provides sufficient security

**Setup:**

1. Generate a secure random 64+ character secret (e.g., `openssl rand -base64 64`)
2. Set `CLOUD_TASKS_SECRET` environment variable in your deployment
3. Keep the secret secure and rotate it periodically via deployment updates

**Security Notes:**

- Use a cryptographically secure random string (64+ characters recommended)
- Rotate the secret periodically by updating the environment variable
- Never log or expose the secret in application responses
- The secret provides adequate protection for internal service-to-service communication
