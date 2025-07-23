# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Documentation Structure

The documentation is organized into three main categories:

### Reference Documentation (`docs/reference/`)

Stable, production-ready documentation for end users and developers:

- **[docs/reference/CONFIGURATION.md](docs/reference/CONFIGURATION.md)** - Environment variables, GitHub/Slack app setup, deployment config
- **[docs/reference/OAUTH.md](docs/reference/OAUTH.md)** - GitHub OAuth authentication implementation and architecture decisions
- **[docs/reference/API.md](docs/reference/API.md)** - HTTP endpoints, Slack App Home interactions, authentication methods
- **[docs/reference/SLACK_APP_SETUP.md](docs/reference/SLACK_APP_SETUP.md)** - Detailed Slack app configuration instructions
- **[docs/reference/SLACK_APP_MANIFEST.md](docs/reference/SLACK_APP_MANIFEST.md)** - Slack app manifest for easy setup

### Design Documentation (`docs/designs/`)

Technical design documents and architectural specifications:

- **[docs/designs/project.md](docs/designs/project.md)** - High-level project overview and features
- **[docs/designs/technical-spec.md](docs/designs/technical-spec.md)** - Detailed technical specifications
- **[docs/designs/manual-pr-link-support.md](docs/designs/manual-pr-link-support.md)** - Feature design for manual PR link detection

### Planning Documentation (`docs/planning/`)

Work-in-progress planning and improvement tracking:

- **[docs/planning/TODO.md](docs/planning/TODO.md)** - Development tasks and technical debt
- **[docs/planning/FUTURE_IMPROVEMENTS.md](docs/planning/FUTURE_IMPROVEMENTS.md)** - 2024-25 best practices and future enhancements

### Other Documentation

- **[README.md](README.md)** - Project overview, quick start, usage examples
- **This file (CLAUDE.md)** - Development patterns, code architecture, and AI coding assistance

When users ask about configuration, setup, or usage, refer them to the **reference** documentation. For architectural questions, use **design** documentation. For future improvements, refer to **planning** documentation.

## Project Overview

This is **PR Bot**, a Go-based application that provides PR mirroring and status reactions between GitHub and Slack. It uses **async processing** via Google Cloud Tasks for high reliability, processing PR opens, reviews, and closures via webhooks, storing state in Cloud Firestore and sending notifications to Slack channels.

## Development Commands

### Core Development

```bash
# Start local development with ngrok tunnel
./scripts/dev.sh

# Build the application
go build -o github-slack-notifier ./cmd/github-slack-notifier

# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...
```

### Linting and Code Quality

```bash
# Run all linters (preferred)
./scripts/lint.sh

# Individual linting tools
golangci-lint run ./...
go fmt ./...
go vet ./...
staticcheck ./...
```

#### Linting Best Practices

**CRITICAL**: Always write lint-free code from the start. Follow these patterns to avoid common linter errors:

**Function Signatures & Line Length:**

- Keep lines under 120 characters
- For long function signatures, use multi-line format:

```go
func NewSlackHandler(
 fs *services.FirestoreService, slack *services.SlackService,
 cloudTasks *services.CloudTasksService, githubAuth *services.GitHubAuthService,
 cfg *config.Config,
) *SlackHandler {
```

**Error Handling:**

- Use static errors instead of dynamic `fmt.Errorf()` calls:

```go
// Good - static errors
var (
 ErrStateRequired = fmt.Errorf("state parameter is required")
 ErrInvalidState  = fmt.Errorf("invalid or expired state")
)

// For contextual errors, wrap static errors:
return fmt.Errorf("%w: status %d", ErrTokenExchangeFailed, statusCode)

// Bad - dynamic errors (triggers err113)
return fmt.Errorf("state parameter is required")
```

**Constants vs Magic Numbers:**

- Always define constants for any numeric values:

```go
const (
 stateIDLength     = 16
 oauthStateTimeout = 15 * time.Minute
 httpClientTimeout = 30 * time.Second
)
```

**HTTP Methods:**

- Use standard library constants:

```go
// Good
http.NewRequestWithContext(ctx, http.MethodPost, url, body)

// Bad
http.NewRequestWithContext(ctx, "POST", url, body)
```

**Resource Cleanup:**

- Always handle `Close()` return values:

```go
// Good
defer func() { _ = resp.Body.Close() }()

// Bad (triggers errcheck)
defer resp.Body.Close()
```

**Comments:**

- End all comments with periods:

```go
// CreateOAuthState creates a new OAuth state for CSRF protection.
func CreateOAuthState(...) { // Good

// CreateOAuthState creates a new OAuth state for CSRF protection
func CreateOAuthState(...) { // Bad (triggers godot)
```

**Security (gosec):**

- Use `#nosec` comments for false positives:

```go
// #nosec G101 -- Public GitHub OAuth endpoint, not credentials
githubTokenURL = "https://github.com/login/oauth/access_token"
```

**Test Function Signatures:**

- Ensure test calls match current function signatures
- When adding parameters to constructors, update ALL test files
- Example: `NewSlackHandler(fs, slack, cloudTasks, githubAuth, cfg)` needs 5 parameters, not 4

**Formatting:**

- Run `gofmt -s -w` on files after making changes
- Use `gofmt -s` (simplify) flag to clean up formatting automatically

### Deployment

```bash
# Deploy to Cloud Run
./scripts/deploy.sh

# Setup GCP infrastructure
./scripts/setup-infrastructure.sh
```

### Slack App Management

```bash
# Install slack-manifest CLI globally
npm install -g slack-manifest

# Generate manifest from current config
./scripts/generate-slack-manifest.sh

# Create new Slack app from manifest
./scripts/apply-slack-manifest.sh --create

# Update existing Slack app manifest
export SLACK_CONFIG_ACCESS_TOKEN=xoxe-1-your-token
export SLACK_APP_ID=A1234567890
./scripts/apply-slack-manifest.sh
```

## Architecture

### Core Components

- **cmd/github-slack-notifier/main.go**: Application entry point with HTTP server setup, graceful shutdown, and dependency injection
- **internal/handlers/**: HTTP handlers for GitHub webhooks (`github.go`), job processing (`job_processor.go`), and Slack webhooks (`slack.go`)
- **internal/services/**: Business logic layer with `FirestoreService`, `SlackService`, and `CloudTasksService`
- **internal/models/**: Data structures for `User`, `Message`, `Repo`, `Job`, `WebhookJob`, and `ManualLinkJob` entities
- **internal/middleware/**: HTTP middleware including structured logging with trace IDs
- **internal/log/**: Custom logging utilities with context support

### Architecture Guidelines

#### What is a Service? (`internal/services/`)

A **Service** encapsulates business logic and external integrations. Services should be created when:

- **Managing state or connections**: Database connections, API clients, connection pools
- **Complex business logic**: Operations that coordinate multiple steps or handle complex workflows
- **External system integration**: Slack API, Firestore, Cloud Tasks, GitHub API
- **Dependency injection needed**: When the component needs configuration or other services injected
- **Shared functionality**: Logic used by multiple handlers or other services

**Examples of good services:**

- `SlackService`: Manages Slack API client and all Slack operations
- `FirestoreService`: Handles database connections and CRUD operations
- `CloudTasksService`: Manages task queue client and job creation

**What should NOT be a service:**

- Simple utility functions (e.g., string parsing, regex matching)
- Pure functions without state or dependencies
- Single-use helper functions

#### What is a Handler? (`internal/handlers/`)

A **Handler** is responsible for HTTP request/response processing. Handlers should:

- **Parse and validate** incoming HTTP requests
- **Call services** to perform business logic
- **Format responses** and handle HTTP status codes
- **Handle errors** and return appropriate error responses
- **Add observability** (logging, metrics, tracing)

**Handler responsibilities:**

- Request parsing and validation
- Authentication/authorization checks
- Calling appropriate service methods
- Response formatting
- Error handling and status codes

**Handlers should NOT:**

- Contain business logic (use services)
- Make direct database or API calls (use services)
- Share state between requests

#### What is a Model? (`internal/models/`)

A **Model** represents data structures used throughout the application. Models should:

- **Define data structures** with appropriate types and tags
- **Document fields** with clear comments
- **Use appropriate tags** for JSON, Firestore, validation
- **Be simple DTOs** (Data Transfer Objects) without methods containing business logic

**Examples of good models:**

```go
type User struct {
    GitHubUsername string `firestore:"github_username" json:"github_username"`
    SlackUserID    string `firestore:"slack_user_id" json:"slack_user_id"`
    DefaultChannel string `firestore:"default_channel,omitempty" json:"default_channel,omitempty"`
}
```

**Models should NOT:**

- Contain business logic methods
- Make database or API calls
- Have complex initialization logic

#### When to Create Utils (`internal/lib/something`)

Utility functions are appropriate for:

- Pure functions without state
- Simple transformations or parsing
- Shared helper functions
- Functions that don't fit the service pattern

**Examples:**

- `ExtractPRLinks(text string) []PRLink` - Simple regex parsing
- `ValidateWebhookSignature(payload, secret, signature) bool`
- `FormatSlackMessage(pr PullRequest) string`

### Processing Architecture

**Fast Path (< 100ms):**

1. **GitHub Webhook** → `handlers/github.go` → validates signature & payload → creates `WebhookJob` wrapped in `Job` → queues to Cloud Tasks → returns 200
2. **Slack Events** → `handlers/slack.go` → detects manual PR links → creates `ManualLinkJob` wrapped in `Job` → queues to Cloud Tasks

**Slow Path (reliable, retryable):**
3. **Cloud Tasks** → `handlers/job_processor.go` → routes `Job` by type → calls appropriate domain handler → processes business logic → updates Firestore → sends Slack notifications → syncs review reactions

**Job Processing:**

- **JobProcessor** provides single entrypoint for all async work with retry/timeout/logging logic
- **Domain Handlers** (GitHubHandler, SlackHandler) contain domain-specific business logic
- **Job Types**: `github_webhook` (routed to GitHubHandler) and `manual_pr_link` (routed to SlackHandler)

### Data Flow

1. **GitHub Webhook** → Fast ingress handler → Cloud Tasks queue → Job processor → Domain handler → Slack/Firestore
2. **Slack Events** → `handlers/slack.go` → detects PR links → Cloud Tasks queue → Job processor → Domain handler → Firestore
3. **Slack Interactions** → `handlers/slack.go` → validates signing secret → processes user configuration
4. **Services Layer** → `services/firestore.go` for persistence, `services/slack.go` for messaging, `services/cloud_tasks.go` for queuing
5. **Models** → Firestore documents and Cloud Tasks job payloads with struct tags

### Review Reaction Management

The system automatically manages Slack emoji reactions on PR notification messages based on GitHub review events:

**Supported Review Actions:**

- **Submitted Reviews**: Adds appropriate emoji based on review state (approved ✅, changes requested 🔄, commented 💬)
- **Dismissed Reviews**: Removes all review-related emoji reactions from tracked messages

**Reaction Sync Approach:**

- Uses a comprehensive sync strategy that removes all existing review reactions before adding current ones
- Ensures message reactions always match the actual PR review state
- Handles multiple tracked messages across different channels for the same PR
- Gracefully handles cases where reactions don't exist or API calls fail

**Review States:**

- `approved` → ✅ (`white_check_mark`)
- `changes_requested` → 🔄 (`arrows_counterclockwise`)
- `commented` → 💬 (`speech_balloon`)
- `dismissed` → Removes all review reactions

### Key Dependencies

- **Web Framework**: `github.com/gin-gonic/gin` for HTTP routing
- **Database**: `cloud.google.com/go/firestore` for Cloud Firestore integration
- **Queue**: `cloud.google.com/go/cloudtasks` for Google Cloud Tasks integration
- **Slack API**: `github.com/slack-go/slack` for Slack Bot API
- **Logging**: Built-in `log/slog` for structured logging with trace IDs
- **IDs**: `github.com/google/uuid` for generating job and trace IDs

## OAuth Architecture Decision

**Decision**: We chose to extend our existing **GitHub App** with OAuth capabilities rather than creating a separate OAuth App.

**Rationale:**

- Single app to manage (webhooks + user auth)
- Consistent with existing webhook infrastructure
- GitHub Apps support both webhook events and user authorization
- Simpler deployment (fewer secrets to manage)

**Tradeoffs considered:**

- Slightly more complex configuration (need to enable OAuth on existing app)
- Mixing webhook and auth concerns in one app
- Alternative: Separate OAuth App would provide cleaner separation of concerns but add operational complexity

**Implementation**: Enable "Request user authorization (OAuth) during installation" in GitHub App settings and configure callback URL.

## Testing Strategy

The project uses a comprehensive testing approach with multiple test types:

### Unit Tests

Use Go's built-in testing framework for individual components:

```bash
# Run all tests
go test ./...

# Test specific package
go test ./internal/handlers

# Test with verbose output
go test -v ./...
```

### End-to-End Integration Tests

The project includes comprehensive e2e integration tests that provide black-box testing of the entire application:

```bash
# Run e2e integration tests
go test ./tests/integration/e2e/... -v

# Run with race detection
go test ./tests/integration/e2e/... -v -race
```

**E2E Test Architecture (`tests/integration/e2e/`):**

- **Test Harness (`harness.go`)**: Starts the real application with dependency injection for testing
- **Fake Cloud Tasks (`fake_cloud_tasks.go`)**: Replaces Cloud Tasks with HTTP callback-based execution
- **HTTP Mocking**: Uses `httpmock` for external API calls (Slack, GitHub)
- **Firestore Emulator**: Real database operations against an emulated Firestore instance

## Common Development Patterns

### Error Handling

- Use structured logging with `slog` for error reporting
- Return sentinel errors from services (e.g., `ErrUserNotFound`)
- Validate webhook signatures before processing

### Security

- Always validate GitHub webhook signatures using HMAC-SHA256
- Validate Slack request signatures using signing secret
- Use admin API key for repository registration endpoint

### Local Development

- Use `./scripts/dev.sh` which sets up ngrok tunneling automatically
- Environment variables loaded from `.env` file
- Hot reload requires manual restart

## Deployment Architecture

- **Platform**: Google Cloud Run (serverless containers)
- **Database**: Cloud Firestore (NoSQL document database)
- **Queue**: Google Cloud Tasks (managed queue service)
- **Registry**: Google Artifact Registry
- **Region**: Configurable via `GCP_REGION` (defaults to europe-west1)

### Slack App Setup

**Recommended: Use App Manifest (Easier)**

1. Generate the manifest for your service:

   ```bash
   ./scripts/generate-slack-manifest.sh
   ```

2. Create a new Slack app at <https://api.slack.com/apps>
3. Choose "From an app manifest" and paste the generated `slack-app-manifest.yaml` content
4. Install the app to your workspace to generate the bot token

**Alternative: Manual Configuration**

1. Create a new Slack app at <https://api.slack.com/apps>
2. Configure OAuth scopes under "OAuth & Permissions":
   - `channels:read` - Validate channel access for App Home channel selection
   - `chat:write` - Send PR notifications and add emoji reactions
   - `links:read` - Read GitHub links in messages for manual PR detection
   - `channels:history` - Required by message.channels event subscription
3. Enable event subscriptions under "Event Subscriptions":
   - Request URL: `https://your-service-url/webhooks/slack/events`
   - Subscribe to bot events: `message.channels`
4. Enable App Home and Interactive Components:
   - Go to App Home → Enable the Home Tab
   - Go to Interactivity & Shortcuts → Enable Interactivity
   - Set Request URL: `https://your-service-url/webhooks/slack/interactions`
5. Install the app to your workspace to generate the bot token

See `docs/reference/SLACK_APP_MANIFEST.md` for detailed setup instructions.

## Development Tips

- Whenever trying to add newlines to ends of files, just use `gofmt -w $file` instead
