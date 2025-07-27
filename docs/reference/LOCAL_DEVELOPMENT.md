# Local Development Environment Setup

This guide walks you through setting up a complete local development environment for the GitHub-Slack Notifier.

## Prerequisites

Install these tools before beginning:

- **[Go 1.23+](https://golang.org/dl/)** - Application runtime
- **[Docker](https://docs.docker.com/get-docker/)** - For Firestore emulator
- **[gcloud CLI](https://cloud.google.com/sdk/docs/install)** - Google Cloud tools
- **[ngrok](https://ngrok.com/)** - Public tunneling for webhooks
- **[Node.js & npm](https://nodejs.org/)** - For Slack manifest management (optional)

## Quick Start

```bash
# Clone and setup
git clone <your-repo-url>
cd github-slack-notifier

# Copy environment template
cp .env.example .env

# Edit .env with your configuration (see sections below)
vim .env

# Start development with automatic tunneling
./scripts/dev.sh
```

The `dev.sh` script will:

- Start the Firestore emulator
- Start your application
- Create an ngrok tunnel
- Display all the URLs you need

## Environment Configuration

### 1. Core Settings

Edit `.env` and configure these required settings:

```bash
# Core Configuration
FIRESTORE_PROJECT_ID=your-local-project-id
FIRESTORE_DATABASE_ID=github-slack-notifier
GITHUB_WEBHOOK_SECRET=your-github-webhook-secret
SLACK_SIGNING_SECRET=your-slack-signing-secret
API_ADMIN_KEY=some-random-long-string
```

### 2. Slack OAuth Setup

**Create a Slack App** (if you haven't already):

1. Go to [Slack App Management](https://api.slack.com/apps)
2. Click **"Create New App"** → **"From scratch"**
3. Name your app and select a development workspace

**Configure OAuth settings:**

1. Go to **"OAuth & Permissions"**
2. Add redirect URL: `http://localhost:8080/auth/slack/callback`
3. Add bot scopes:
   - `channels:read`
   - `chat:write`  
   - `links:read`
   - `channels:history`

**Get credentials** from **"Basic Information"**:

```bash
# Add to .env
SLACK_CLIENT_ID=1234567890.1234567890
SLACK_CLIENT_SECRET=your-slack-client-secret
```

### 3. GitHub App Setup

**Create a GitHub App**:

1. Go to [GitHub Apps](https://github.com/settings/apps/new)
2. Fill required fields:
   - **App name**: "PR Bot Dev" (make it unique)
   - **Homepage URL**: `http://localhost:8080`
   - **Webhook URL**: `https://your-ngrok-domain.ngrok.io/webhooks/github`
   - **Webhook secret**: Generate with `openssl rand -hex 32`

3. **Permissions**:
   - Repository permissions → Pull requests: **Read**
   - Repository permissions → Metadata: **Read**

4. **Events**:
   - Pull requests
   - Pull request reviews

5. **Install** the app on your test repositories

**Configure OAuth** (same app):

1. Enable **"Request user authorization (OAuth) during installation"**
2. Set **"User authorization callback URL"**: `http://localhost:8080/auth/github/callback`

**Get credentials**:

```bash
# Add to .env
GITHUB_CLIENT_ID=your_github_app_client_id
GITHUB_CLIENT_SECRET=your_github_app_client_secret
```

### 4. Cloud Tasks (Simulated)

For local development, Cloud Tasks is replaced with direct HTTP calls:

```bash
# Local development settings
GOOGLE_CLOUD_PROJECT=local-dev-project
BASE_URL=http://localhost:8080
CLOUD_TASKS_SECRET=local-development-secret
```

## Development Workflow

### Starting the Application

```bash
# Method 1: Full development setup (recommended)
./scripts/dev.sh

# Method 2: Manual steps
# Terminal 1: Start Firestore emulator
gcloud emulators firestore start --host-port=localhost:8080

# Terminal 2: Start application  
export FIRESTORE_EMULATOR_HOST=localhost:8080
go run ./cmd/github-slack-notifier

# Terminal 3: Start ngrok
ngrok http 8080
```

### Testing the Setup

#### 1. Test Slack OAuth Installation

1. **Visit installation URL**: `http://localhost:8080/auth/slack/install`
2. **Complete OAuth flow** in your development workspace
3. **Check logs** for "Workspace saved successfully"

#### 2. Test GitHub Integration

1. **Update GitHub App webhook URL** with your ngrok URL
2. **Open a PR** in a test repository
3. **Check application logs** for webhook processing
4. **Verify Slack notification** appears in your configured channel

#### 3. Test App Home

1. **Open Slack** → Find your app in the sidebar
2. **Click "Home" tab** → Verify the interface loads
3. **Connect GitHub account** via OAuth
4. **Configure notification settings**

### Common Development Tasks

#### View Application Logs

```bash
# Follow logs in real-time
tail -f tmp/app.log

# Search for specific events
grep "OAuth" tmp/app.log
grep "Webhook" tmp/app.log
grep "ERROR" tmp/app.log
```

#### Reset Local Database

```bash
# Clear all Firestore data
rm -rf ~/.config/gcloud/emulators/firestore/

# Or use the admin endpoint
curl -X DELETE http://localhost:8080/admin/clear-data \
  -H "X-Admin-Key: your-api-admin-key"
```

#### Test Specific Workflows

```bash
# Test GitHub webhook (simulate PR opened)
curl -X POST http://localhost:8080/webhooks/github \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-Hub-Signature-256: sha256=$(echo -n '{"action":"opened"}' | openssl dgst -sha256 -hmac 'your-webhook-secret')" \
  -d '{"action":"opened","pull_request":{"number":123,"title":"Test PR"}}'

# Test Slack OAuth installation
curl http://localhost:8080/auth/slack/install
# Should redirect to Slack OAuth
```

#### Run Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run integration tests
go test ./tests/integration/e2e/... -v

# Run specific test
go test -run TestSlackOAuthInstallationFlow ./tests/integration/e2e/...
```

#### Code Quality

```bash
# Run linters (recommended before commits)
./scripts/lint.sh

# Run specific linters
golangci-lint run ./...
go fmt ./...
go vet ./...
```

## Development Environment Variables

### Full .env Example for Development

```bash
# vim: ft=sh

# Core Configuration
FIRESTORE_PROJECT_ID=local-dev-project
FIRESTORE_DATABASE_ID=github-slack-notifier
GITHUB_WEBHOOK_SECRET=your-github-webhook-secret-from-app-setup
SLACK_SIGNING_SECRET=your-slack-signing-secret-from-app-setup
API_ADMIN_KEY=local-dev-admin-key

# Slack OAuth Configuration
SLACK_CLIENT_ID=1234567890.1234567890
SLACK_CLIENT_SECRET=your-slack-client-secret

# GitHub OAuth Configuration
GITHUB_CLIENT_ID=your_github_app_client_id
GITHUB_CLIENT_SECRET=your_github_app_client_secret

# Cloud Tasks Configuration (local simulation)
GOOGLE_CLOUD_PROJECT=local-dev-project
BASE_URL=http://localhost:8080
GCP_REGION=us-central1
CLOUD_TASKS_QUEUE=local-queue
CLOUD_TASKS_SECRET=local-development-secret
CLOUD_TASKS_MAX_ATTEMPTS=3

# Server Configuration
PORT=8080
GIN_MODE=debug
LOG_LEVEL=debug

# Timeouts
SERVER_READ_TIMEOUT=30s
SERVER_WRITE_TIMEOUT=30s
SERVER_SHUTDOWN_TIMEOUT=30s
WEBHOOK_PROCESSING_TIMEOUT=1m

# Development settings
NGROK_DOMAIN=your-subdomain.ngrok.io

# Emoji customization (optional)
EMOJI_APPROVED=white_check_mark
EMOJI_CHANGES_REQUESTED=arrows_counterclockwise
EMOJI_COMMENTED=speech_balloon
EMOJI_MERGED=tada
EMOJI_CLOSED=x
```

## Troubleshooting

### Common Issues

#### 1. Firestore Connection Issues

```bash
# Check if emulator is running
curl http://localhost:8080/

# Restart emulator
pkill -f firestore
gcloud emulators firestore start --host-port=localhost:8080
```

#### 2. ngrok Connection Issues

```bash
# Check ngrok status
curl http://127.0.0.1:4040/api/tunnels

# Restart ngrok
pkill ngrok
ngrok http 8080
```

#### 3. Slack OAuth Issues

- **Redirect URL mismatch**: Ensure Slack app redirect URL exactly matches your `.env` setting
- **Invalid client credentials**: Double-check `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET`
- **Scope issues**: Verify all required scopes are added to your Slack app

#### 4. GitHub Webhook Issues

- **Webhook not received**: Check ngrok URL is publicly accessible
- **Signature validation fails**: Verify `GITHUB_WEBHOOK_SECRET` matches your GitHub App
- **Events not processed**: Check webhook is configured for "Pull requests" and "Pull request reviews"

#### 5. OAuth Flow Issues

**GitHub OAuth fails**:

```bash
# Check GitHub App settings
curl -H "Authorization: token your-github-token" \
  https://api.github.com/user

# Verify redirect URL matches exactly
```

**Slack OAuth fails**:

```bash
# Test installation endpoint
curl -I http://localhost:8080/auth/slack/install

# Should return 302 redirect to slack.com/oauth/v2/authorize
```

### Development Tips

#### Hot Reload

The application doesn't have built-in hot reload. Use a tool like `air`:

```bash
# Install air
go install github.com/cosmtrek/air@latest

# Create .air.toml config
echo 'root = "."
tmp_dir = "tmp"
[build]
  cmd = "go build -o ./tmp/main ./cmd/github-slack-notifier"
  bin = "tmp/main"' > .air.toml

# Start with hot reload
air
```

#### Database Inspection

Use the Firestore emulator UI:

```bash
# While emulator is running, visit:
http://localhost:4000
```

#### Debugging Webhooks

Create test webhook payloads:

```bash
# Save test PR payload
cat > test-pr.json << 'EOF'
{
  "action": "opened",
  "pull_request": {
    "number": 123,
    "title": "Test PR",
    "body": "Test description",
    "html_url": "https://github.com/test/repo/pull/123",
    "user": {"login": "testuser"}
  },
  "repository": {
    "full_name": "test/repo"
  }
}
EOF

# Send test webhook
curl -X POST http://localhost:8080/webhooks/github \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-Hub-Signature-256: sha256=$(cat test-pr.json | openssl dgst -sha256 -hmac 'your-webhook-secret' | cut -d' ' -f2)" \
  -d @test-pr.json
```

## Production-like Local Testing

To test production-like behavior locally:

### 1. Use Production Build

```bash
# Build production binary
go build -o github-slack-notifier ./cmd/github-slack-notifier

# Run with production settings
GIN_MODE=release LOG_LEVEL=info ./github-slack-notifier
```

### 2. Test with Real Cloud Services

```bash
# Use real Firestore (not emulator)
unset FIRESTORE_EMULATOR_HOST
FIRESTORE_PROJECT_ID=your-real-gcp-project

# Use real Cloud Tasks
# Note: This requires GCP authentication
gcloud auth application-default login
```

### 3. Test Error Scenarios

```bash
# Test invalid webhook signatures
curl -X POST http://localhost:8080/webhooks/github \
  -H "X-Hub-Signature-256: sha256=invalid" \
  -d '{"test": true}'

# Test malformed payloads
curl -X POST http://localhost:8080/webhooks/github \
  -H "Content-Type: application/json" \
  -d 'invalid-json'
```

## Next Steps

Once your local environment is working:

1. **Set up CI/CD** for automatic testing and deployment
2. **Configure monitoring** and alerting for production
3. **Set up production environments** with proper secrets management
4. **Document deployment procedures** for your team

## Getting Help

- **Check application logs**: `tail -f tmp/app.log`
- **Review test files**: Examples in `tests/integration/e2e/`
- **Run tests**: `go test ./...` for comprehensive testing
- **Use debug mode**: Set `LOG_LEVEL=debug` for detailed logging
