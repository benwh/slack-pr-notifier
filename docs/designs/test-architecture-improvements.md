# E2E Test Architecture Improvements

## Overview

This document outlines potential improvements to the end-to-end testing architecture for the GitHub-Slack Notifier service. The current approach uses a custom test harness with request capture mechanisms, which adds complexity. This document presents more idiomatic Go testing approaches.

## Current State

The current e2e tests use:
- Custom `TestHarness` with `SlackRequestCapture`
- `httpmock` library for mocking external HTTP calls
- Complex assertion helpers that duplicate business logic
- Request capture and replay mechanisms

### Issues with Current Approach

1. **Non-idiomatic**: Custom request capture isn't a standard Go testing pattern
2. **Complexity**: Requires understanding custom test infrastructure
3. **Maintenance burden**: Custom test harness needs its own maintenance
4. **Duplication**: Some test helpers reimplement business logic (e.g., emoji selection)

## Proposed Options

### Option 1: Standard httptest.Server Approach

Use Go's built-in `httptest.Server` to mock external services.

#### Implementation

```go
func TestGitHubWebhook(t *testing.T) {
    // Create a test Slack server
    var capturedRequest *http.Request
    var capturedBody []byte
    
    slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        capturedRequest = r
        capturedBody, _ = io.ReadAll(r.Body)
        
        // Return expected Slack response
        w.WriteHeader(200)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "ok": true,
            "ts": "1234567890.123456",
        })
    }))
    defer slackServer.Close()
    
    // Configure app to use slackServer.URL
    cfg := &config.Config{
        SlackAPIURL: slackServer.URL,
        // ... other config
    }
    
    // Start app and test
    app := StartApp(cfg)
    
    // Send webhook
    resp, _ := http.Post(app.URL+"/webhooks/github", "application/json", webhookPayload)
    
    // Assert on captured request
    assert.Contains(t, string(capturedBody), "channel=test-channel")
    assert.Contains(t, string(capturedBody), "🐱") // cat emoji for 80 lines
}
```

#### Pros
- Uses only Go standard library
- No external dependencies
- Very clear what's being tested
- True black-box testing

#### Cons
- Requires making external API URLs configurable
- Each test needs server setup
- More verbose than current approach

### Option 2: Interface-Based Testing

Define interfaces for external services and use test implementations.

#### Implementation

```go
// In application code
type SlackClient interface {
    PostMessage(channel, text string) (string, error)
    AddReaction(channel, timestamp, emoji string) error
}

// In tests
type testSlackClient struct {
    // Configure behavior
    postMessageFunc func(channel, text string) (string, error)
    
    // Record calls
    messages  []slackMessage
    reactions []slackReaction
}

func (t *testSlackClient) PostMessage(channel, text string) (string, error) {
    t.messages = append(t.messages, slackMessage{channel, text})
    
    if t.postMessageFunc != nil {
        return t.postMessageFunc(channel, text)
    }
    return "123.456", nil
}

func TestPRWebhook(t *testing.T) {
    tests := []struct {
        name          string
        slackBehavior func() *testSlackClient
        webhook       webhookPayload
        wantErr       bool
        assertions    func(t *testing.T, slack *testSlackClient)
    }{
        {
            name: "PR opened sends notification",
            slackBehavior: func() *testSlackClient {
                return &testSlackClient{} // Default behavior
            },
            webhook: buildPRPayload(50, 30), // 80 lines total
            assertions: func(t *testing.T, slack *testSlackClient) {
                require.Len(t, slack.messages, 1)
                assert.Contains(t, slack.messages[0].text, "🐱")
            },
        },
        {
            name: "Slack API error retries",
            slackBehavior: func() *testSlackClient {
                callCount := 0
                return &testSlackClient{
                    postMessageFunc: func(channel, text string) (string, error) {
                        callCount++
                        if callCount < 3 {
                            return "", errors.New("rate_limited")
                        }
                        return "123.456", nil
                    },
                }
            },
            webhook: buildPRPayload(10, 5),
            assertions: func(t *testing.T, slack *testSlackClient) {
                assert.Len(t, slack.messages, 3) // Retried 3 times
            },
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            slack := tt.slackBehavior()
            app := NewApp(slack)
            
            err := app.HandleWebhook(tt.webhook)
            
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
            
            if tt.assertions != nil {
                tt.assertions(t, slack)
            }
        })
    }
}
```

#### Pros
- Most idiomatic Go approach
- Fast test execution (no HTTP)
- Easy to test edge cases and error scenarios
- Follows "accept interfaces, return structs" principle
- Clear separation of concerns
- Highly maintainable

#### Cons
- Requires refactoring to use interfaces
- Not pure black-box (tests know about interfaces)
- Initial refactoring effort

### Option 3: Minimal Test Server with Table-Driven Tests

Combine httptest with table-driven tests for a middle-ground approach.

#### Implementation

```go
type TestServer struct {
    *httptest.Server
    requests []CapturedRequest
    mu       sync.Mutex
    
    // Configure responses
    responses map[string]func(w http.ResponseWriter, r *http.Request)
}

func NewTestServer() *TestServer {
    ts := &TestServer{
        responses: make(map[string]func(w http.ResponseWriter, r *http.Request)),
    }
    
    ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        
        ts.mu.Lock()
        ts.requests = append(ts.requests, CapturedRequest{
            URL:    r.URL.String(),
            Method: r.Method,
            Body:   string(body),
        })
        ts.mu.Unlock()
        
        // Route to configured response
        if handler, ok := ts.responses[r.URL.Path]; ok {
            handler(w, r)
        } else {
            w.WriteHeader(404)
        }
    }))
    
    return ts
}

func TestWebhookScenarios(t *testing.T) {
    tests := []struct {
        name           string
        setupServer    func(*TestServer)
        webhook        webhookPayload
        wantSlackMsg   string
        wantEmoji      string
    }{
        {
            name: "small PR",
            setupServer: func(ts *TestServer) {
                ts.responses["/api/chat.postMessage"] = func(w http.ResponseWriter, r *http.Request) {
                    fmt.Fprintf(w, `{"ok": true, "ts": "123.456"}`)
                }
            },
            webhook:      buildPRPayload(2, 1),   // 3 lines
            wantEmoji:    "🐜",
        },
        {
            name: "large PR",
            setupServer: func(ts *TestServer) {
                ts.responses["/api/chat.postMessage"] = func(w http.ResponseWriter, r *http.Request) {
                    fmt.Fprintf(w, `{"ok": true, "ts": "123.456"}`)
                }
            },
            webhook:      buildPRPayload(500, 200), // 700 lines
            wantEmoji:    "🐻",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            server := NewTestServer()
            defer server.Close()
            
            tt.setupServer(server)
            
            app := NewApp(Config{SlackAPIURL: server.URL})
            app.HandleWebhook(tt.webhook)
            
            // Find Slack message in captured requests
            var slackMsg string
            for _, req := range server.requests {
                if strings.Contains(req.URL, "chat.postMessage") {
                    slackMsg = req.Body
                    break
                }
            }
            
            assert.Contains(t, slackMsg, tt.wantEmoji)
        })
    }
}
```

#### Pros
- Good balance between simplicity and functionality
- Table-driven tests are idiomatic Go
- Still black-box testing
- Reusable test server

#### Cons
- Still requires URL configuration
- Some custom test infrastructure

### Option 4: Hybrid Approach - Interfaces for Core, httptest for Integration

Use interfaces for unit tests and httptest for true e2e tests.

#### Implementation

```go
// Unit tests use interfaces
func TestPRProcessor_CalculateEmoji(t *testing.T) {
    processor := NewPRProcessor(&mockSlackClient{})
    
    tests := []struct {
        additions int
        deletions int
        want      string
    }{
        {2, 1, "🐜"},
        {50, 30, "🐱"},
        {500, 200, "🐻"},
    }
    
    for _, tt := range tests {
        emoji := processor.calculateEmoji(tt.additions, tt.deletions)
        assert.Equal(t, tt.want, emoji)
    }
}

// E2E tests use httptest
func TestEndToEnd_PRNotification(t *testing.T) {
    // Start real services with test servers
    slack := httptest.NewServer(slackHandler)
    github := httptest.NewServer(githubHandler)
    
    app := StartApp(Config{
        SlackURL:  slack.URL,
        GitHubURL: github.URL,
    })
    
    // Send real webhook
    resp, _ := http.Post(app.URL+"/webhook", "application/json", realWebhookPayload)
    
    // Assert on actual behavior
    assert.Equal(t, 200, resp.StatusCode)
}
```

#### Pros
- Best of both worlds
- Fast unit tests, thorough integration tests
- Clear test boundaries
- Scales well with complexity

#### Cons
- Two different testing approaches
- Requires good test organization

## Recommendation

For this project, I recommend **Option 2 (Interface-Based Testing)** for the following reasons:

1. **Most idiomatic**: This is how most successful Go projects handle testing
2. **Fast feedback**: Tests run quickly without HTTP overhead
3. **Easy scenarios**: Simple to test different error conditions and edge cases
4. **Maintainable**: Clear, explicit test code without magic
5. **Refactoring benefit**: Introducing interfaces improves overall code design

### Migration Path

If we choose Option 2, here's a suggested migration approach:

1. **Phase 1**: Define interfaces for external services (Slack, GitHub)
2. **Phase 2**: Refactor services to accept interfaces
3. **Phase 3**: Write new tests using test implementations
4. **Phase 4**: Gradually migrate existing tests
5. **Phase 5**: Remove old test harness

### Alternative for Minimal Change

If refactoring to interfaces is too much work initially, **Option 3 (Minimal Test Server)** provides a good compromise:
- Simpler than current approach
- No production code changes needed
- Still enables black-box testing
- Can migrate to interfaces later

## Decision Criteria

When choosing an approach, consider:

1. **Current codebase structure**: How much refactoring is acceptable?
2. **Team familiarity**: What patterns is the team comfortable with?
3. **Test execution time**: How important is test speed?
4. **Test scenarios**: How many edge cases need testing?
5. **Maintenance burden**: Who will maintain the tests?

## Next Steps

1. Review this document with the team
2. Decide on preferred approach
3. Create proof-of-concept for chosen option
4. Plan migration if needed
5. Update testing guidelines

## References

- [Go Wiki: Table Driven Tests](https://go.dev/wiki/TableDrivenTests)
- [Go Blog: Using Subtests and Sub-benchmarks](https://go.dev/blog/subtests)
- [httptest package documentation](https://pkg.go.dev/net/http/httptest)
- [Effective Go: Interfaces](https://go.dev/doc/effective_go#interfaces)