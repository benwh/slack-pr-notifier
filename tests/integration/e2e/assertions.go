package e2e

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// SlackRequestCapture captures Slack API requests for assertions.
type SlackRequestCapture struct {
	mu       sync.Mutex
	requests []CapturedRequest
}

// CapturedRequest represents a captured HTTP request with parsed body.
type CapturedRequest struct {
	URL        string
	Method     string
	Headers    http.Header
	RawBody    []byte
	ParsedBody interface{}
}

// SlackPostMessageRequest represents a parsed chat.postMessage request.
type SlackPostMessageRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
	// Add other fields as needed
}

// SlackReactionRequest represents a parsed reactions.add/remove request.
type SlackReactionRequest struct {
	Channel   string `json:"channel"`
	Timestamp string `json:"timestamp"`
	Name      string `json:"name"` // emoji name
}

// SlackUpdateMessageRequest represents a parsed chat.update request.
type SlackUpdateMessageRequest struct {
	Channel   string `json:"channel"`
	Timestamp string `json:"ts"`
	Text      string `json:"text"`
	// Add other fields as needed
}

// NewSlackRequestCapture creates a new request capture instance.
func NewSlackRequestCapture() *SlackRequestCapture {
	return &SlackRequestCapture{
		requests: make([]CapturedRequest, 0),
	}
}

// CaptureRequest captures an HTTP request for later assertions.
func (c *SlackRequestCapture) CaptureRequest(req *http.Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Read the body
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	// Restore the body for the actual request
	req.Body = io.NopCloser(strings.NewReader(string(body)))

	captured := CapturedRequest{
		URL:     req.URL.String(),
		Method:  req.Method,
		Headers: req.Header.Clone(),
		RawBody: body,
	}

	// Parse based on URL/Content-Type
	if strings.Contains(req.URL.Path, "chat.postMessage") {
		var parsed SlackPostMessageRequest
		if err := parseFormURLEncoded(body, &parsed); err == nil {
			captured.ParsedBody = parsed
		}
	} else if strings.Contains(req.URL.Path, "chat.update") {
		var parsed SlackUpdateMessageRequest
		if err := parseFormURLEncoded(body, &parsed); err == nil {
			captured.ParsedBody = parsed
		}
	} else if strings.Contains(req.URL.Path, "reactions.add") || strings.Contains(req.URL.Path, "reactions.remove") {
		var parsed SlackReactionRequest
		if err := parseFormURLEncoded(body, &parsed); err == nil {
			captured.ParsedBody = parsed
		}
	}

	c.requests = append(c.requests, captured)
	return nil
}

// GetPostMessageRequests returns all captured chat.postMessage requests.
func (c *SlackRequestCapture) GetPostMessageRequests() []SlackPostMessageRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	var results []SlackPostMessageRequest
	for _, req := range c.requests {
		if parsed, ok := req.ParsedBody.(SlackPostMessageRequest); ok {
			results = append(results, parsed)
		}
	}
	return results
}

// GetUpdateMessageRequests returns all captured chat.update requests.
func (c *SlackRequestCapture) GetUpdateMessageRequests() []SlackUpdateMessageRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	var results []SlackUpdateMessageRequest
	for _, req := range c.requests {
		if parsed, ok := req.ParsedBody.(SlackUpdateMessageRequest); ok {
			results = append(results, parsed)
		}
	}
	return results
}

// GetReactionRequests returns all captured reaction requests.
func (c *SlackRequestCapture) GetReactionRequests() []SlackReactionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	var results []SlackReactionRequest
	for _, req := range c.requests {
		if parsed, ok := req.ParsedBody.(SlackReactionRequest); ok {
			results = append(results, parsed)
		}
	}
	return results
}

// GetAllRequests returns all captured requests.
func (c *SlackRequestCapture) GetAllRequests() []CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make([]CapturedRequest, len(c.requests))
	copy(result, c.requests)
	return result
}

// Clear removes all captured requests.
func (c *SlackRequestCapture) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = c.requests[:0]
}

// parseFormURLEncoded parses form-urlencoded data into a struct.
func parseFormURLEncoded(data []byte, v interface{}) error {
	// The Slack API uses form-urlencoded for POST requests
	values, err := url.ParseQuery(string(data))
	if err != nil {
		return err
	}

	// For simple parsing, we can use the values directly
	// This is a simplified version - enhance as needed
	switch target := v.(type) {
	case *SlackPostMessageRequest:
		target.Channel = values.Get("channel")
		target.Text = values.Get("text")
	case *SlackUpdateMessageRequest:
		target.Channel = values.Get("channel")
		target.Timestamp = values.Get("ts")
		target.Text = values.Get("text")
	case *SlackReactionRequest:
		target.Channel = values.Get("channel")
		target.Timestamp = values.Get("timestamp")
		target.Name = values.Get("name")
	}
	return nil
}

// Note: Most of these helper functions are kept for backward compatibility,
// but for new tests, prefer direct assertions like:
//   assert.Contains(t, message.Text, "🐱")
//   assert.Equal(t, "test-channel", message.Channel)

// ParseSlackMessageContent extracts components from a Slack message for detailed assertions.
type ParsedSlackMessage struct {
	Emoji      string
	URL        string
	Title      string
	Author     string
	HasMention bool
}

// ParseSlackMessage parses a Slack message to extract its components.
func ParseSlackMessage(text string) ParsedSlackMessage {
	result := ParsedSlackMessage{}

	result.Emoji = extractEmoji(text)
	result.URL, result.Title = extractURLAndTitle(text)
	result.Author, result.HasMention = extractAuthor(text)

	return result
}

// extractEmoji finds the first emoji in the text.
func extractEmoji(text string) string {
	emojiRunes := []rune(text)
	for i, r := range emojiRunes {
		// Simple emoji detection - can be enhanced
		if r >= 0x1F300 && r <= 0x1F9FF {
			if i+1 < len(emojiRunes) {
				return string(emojiRunes[i : i+2])
			}
			return string(r)
		}
	}
	return ""
}

// extractURLAndTitle extracts URL and title from markdown link format: <url|title>.
func extractURLAndTitle(text string) (string, string) {
	start := strings.Index(text, "<")
	if start == -1 {
		return "", ""
	}

	end := strings.Index(text[start:], ">")
	if end == -1 {
		return "", ""
	}

	link := text[start+1 : start+end]
	parts := strings.Split(link, "|")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// extractAuthor extracts the author from text (after "by ").
func extractAuthor(text string) (string, bool) {
	idx := strings.Index(text, " by ")
	if idx == -1 {
		return "", false
	}

	authorPart := text[idx+4:]
	if strings.HasPrefix(authorPart, "<@") {
		mentionEnd := strings.Index(authorPart, ">")
		if mentionEnd != -1 {
			return authorPart[:mentionEnd+1], true
		}
	}

	return strings.TrimSpace(authorPart), false
}
