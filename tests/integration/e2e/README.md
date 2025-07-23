# End-to-End Integration Tests

This directory contains comprehensive end-to-end integration tests that provide black-box testing of the entire GitHub-Slack notifier application.

## Overview

These tests start the real application and test complete workflows without mocking internal components. They provide confidence that the entire system works correctly together, including HTTP routing, authentication, async processing, and external integrations.

## Architecture

### Test Harness (`harness.go`)

The test harness starts the real application with dependency injection for testing:

- **Real HTTP Server**: Starts the application on a random port with all routes configured
- **Firestore Emulator**: Uses a real Firestore emulator for database operations
- **Dependency Injection**: Replaces Cloud Tasks with a fake implementation for controlled testing
- **HTTP Mocking**: Uses `httpmock` to mock external API calls to Slack and GitHub
- **Cleanup**: Automatically cleans up resources after tests complete

### Fake Cloud Tasks (`fake_cloud_tasks.go`)

Replaces the real Cloud Tasks service with a test implementation that:

- **Immediate Execution**: Executes jobs immediately or with configurable delays
- **HTTP Callbacks**: Calls back to the application via HTTP (like real Cloud Tasks)
- **Job Tracking**: Tracks executed jobs for test assertions
- **Async Support**: Can execute jobs synchronously or asynchronously
- **Authentication**: Uses the same authentication as real Cloud Tasks

## Test Files

### GitHub Webhook Tests (`github_webhook_test.go`)

Tests the complete GitHub webhook processing workflow:

- **PR Opened**: Full workflow from webhook receipt to Slack notification
- **PR Reviews**: Review processing with emoji reaction synchronization
- **Security**: Webhook signature validation and rejection of invalid signatures
- **Concurrency**: Multiple webhooks processed simultaneously
- **Repository Registration**: Automatic repository registration for verified users

### Slack Events Tests (`slack_events_test.go`)

Tests Slack event processing including:

- **Manual PR Links**: Detection and processing of GitHub PR links in Slack messages
- **URL Verification**: Slack's URL verification challenge handling
- **Bot Filtering**: Ignoring messages from bots to prevent loops
- **Multiple Links**: Handling of messages with multiple PR links (ignored by design)
- **Security**: Slack signature validation and rejection of invalid requests

### Slack Interactions Tests (`slack_interactions_test.go`)

Tests Slack App Home and interactive components:

- **Channel Selection**: User configuration via App Home channel selector
- **View Submissions**: Processing of Slack modal view submissions
- **Security**: Signature validation for interactive components

## Running Tests

```bash
# Run all e2e tests
go test ./tests/integration/e2e/... -v

# Run specific test file
go test ./tests/integration/e2e/github_webhook_test.go -v

# Run with race detection
go test ./tests/integration/e2e/... -v -race

# Run tests with timeout
go test ./tests/integration/e2e/... -v -timeout=30s
```

## Test Patterns

### Basic Test Setup

```go
func TestExample(t *testing.T) {
    // Setup test harness (starts real app)
    harness := NewTestHarness(t)
    defer harness.Cleanup()

    // Setup external API mocks
    harness.SetupMockResponses()

    // Context for database operations
    ctx := context.Background()

    t.Run("Test case", func(t *testing.T) {
        // Clear data between test cases
        require.NoError(t, harness.ClearFirestore(ctx))
        harness.FakeCloudTasks().ClearExecutedJobs()

        // Setup test data
        harness.SetupUser(ctx, "github-user", "slack-user", "channel")
        harness.SetupRepo(ctx, "org/repo", "channel", "team")

        // Send HTTP request to application
        resp := sendRequest(t, harness, payload)
        assert.Equal(t, http.StatusOK, resp.StatusCode)

        // Verify async job execution
        jobs := harness.FakeCloudTasks().GetExecutedJobs()
        require.Len(t, jobs, 1)
    })
}
```

### Security Testing

```go
// Test signature validation
func TestInvalidSignature(t *testing.T) {
    harness := NewTestHarness(t)
    defer harness.Cleanup()

    // Send request with invalid signature
    req := buildRequestWithSignature(t, payload, "invalid-signature")
    resp, err := client.Do(req)
    require.NoError(t, err)
    defer func() { _ = resp.Body.Close() }()

    // Should be rejected
    assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
```

### Concurrent Processing

```go
// Test concurrent webhook handling
func TestConcurrentProcessing(t *testing.T) {
    harness := NewTestHarness(t)
    defer harness.Cleanup()

    // Configure async processing
    harness.FakeCloudTasks().SetAsync(true, 10*time.Millisecond)

    // Send multiple requests concurrently
    numRequests := 5
    done := make(chan bool, numRequests)

    for i := range numRequests {
        go func(id int) {
            payload := buildPayload(id)
            resp := sendRequest(t, harness, payload)
            assert.Equal(t, http.StatusOK, resp.StatusCode)
            done <- true
        }(i)
    }

    // Wait for all requests
    for range numRequests {
        <-done
    }

    // Wait for all jobs to execute
    err := harness.FakeCloudTasks().WaitForJobs(numRequests, 5*time.Second)
    require.NoError(t, err)
}
```

## Key Benefits

1. **Real Application Testing**: Tests the actual HTTP server, routing, middleware, and business logic
2. **End-to-End Workflows**: Verifies complete user workflows from HTTP request to final side effects
3. **Async Processing**: Tests the actual async job processing architecture with Cloud Tasks
4. **Security Validation**: Ensures authentication and signature validation work correctly
5. **Concurrency Testing**: Validates the application handles concurrent requests properly
6. **External Integration**: Mocks external APIs while testing integration points
7. **Database Operations**: Uses real database operations against Firestore emulator
8. **Error Scenarios**: Tests error handling and edge cases in realistic conditions

## Debugging Tests

### View Application Logs

The test harness runs the application with structured logging. Set the log level to see detailed output:

```go
cfg := &config.Config{
    LogLevel: "info", // Change from "error" to see more logs
    // ... other config
}
```

### Inspect Mock API Calls

```go
// Check which external APIs were called
info := httpmock.GetCallCountInfo()
slackCalls := info["POST https://slack.com/api/chat.postMessage"]
assert.Positive(t, slackCalls, "Expected Slack API to be called")
```

### Debug Job Execution

```go
// Inspect executed jobs
jobs := harness.FakeCloudTasks().GetExecutedJobs()
for i, job := range jobs {
    t.Logf("Job %d: Type=%s, ID=%s", i, job.Type, job.ID)
}
```

### Check Database State

```go
// Query Firestore directly
users := harness.firestoreEmulator.Client.Collection("users")
doc, err := users.Doc("test-user").Get(ctx)
require.NoError(t, err)
t.Logf("User data: %+v", doc.Data())
```

## Maintenance

### Adding New Tests

1. **Follow existing patterns** in the test files
2. **Use descriptive test names** that explain the scenario being tested
3. **Clear data between test cases** to avoid interference
4. **Test both success and error scenarios**
5. **Include security validation** where appropriate

### Updating Mock Responses

When external APIs change, update the mock responses in `harness.SetupMockResponses()`:

```go
// Add new mock response
httpmock.RegisterResponder("POST", "https://api.example.com/new-endpoint",
    httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
        "status": "success",
    }))
```

### Performance Considerations

- Tests start the full application, so they're slower than unit tests
- Use `t.Parallel()` cautiously as tests share the Firestore emulator
- Clean up resources properly to avoid resource leaks
- Consider timeout values for async operations