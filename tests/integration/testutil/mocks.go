package testutil

import (
	"context"
	"sync"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github.com/slack-go/slack"
)

// This matches the interface used by GitHubHandler.
type CloudTasksServiceInterface interface {
	EnqueueJob(ctx context.Context, job *models.Job) error
}

// MockCloudTasksService is an in-memory implementation of CloudTasksServiceInterface for testing.
type MockCloudTasksService struct {
	mu         sync.Mutex
	queuedJobs []*models.Job
}

// Compile-time check to ensure MockCloudTasksService implements CloudTasksServiceInterface.
var _ CloudTasksServiceInterface = (*MockCloudTasksService)(nil)

// NewMockCloudTasksService creates a new mock Cloud Tasks service.
func NewMockCloudTasksService() *MockCloudTasksService {
	return &MockCloudTasksService{
		queuedJobs: make([]*models.Job, 0),
	}
}

// EnqueueJob adds a job to the in-memory queue instead of sending to Cloud Tasks.
func (m *MockCloudTasksService) EnqueueJob(ctx context.Context, job *models.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Make a copy of the job to avoid race conditions
	jobCopy := &models.Job{
		ID:      job.ID,
		Type:    job.Type,
		TraceID: job.TraceID,
		Payload: make([]byte, len(job.Payload)),
	}
	copy(jobCopy.Payload, job.Payload)

	m.queuedJobs = append(m.queuedJobs, jobCopy)
	return nil
}

// GetQueuedJobs returns all jobs that have been queued.
func (m *MockCloudTasksService) GetQueuedJobs() []*models.Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return a copy of the slice to avoid race conditions
	jobs := make([]*models.Job, len(m.queuedJobs))
	copy(jobs, m.queuedJobs)
	return jobs
}

// ClearQueuedJobs removes all queued jobs.
func (m *MockCloudTasksService) ClearQueuedJobs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queuedJobs = m.queuedJobs[:0]
}

// MockGitHubAuthService is a mock implementation of GitHubAuthService for testing.
type MockGitHubAuthService struct {
	users map[string]*models.User // Maps GitHub username to User
}

// NewMockGitHubAuthService creates a new mock GitHub auth service.
func NewMockGitHubAuthService() *MockGitHubAuthService {
	return &MockGitHubAuthService{
		users: make(map[string]*models.User),
	}
}

// AddMockUser adds a mock user for testing GitHub operations.
func (m *MockGitHubAuthService) AddMockUser(githubUsername string, user *models.User) {
	m.users[githubUsername] = user
}

// GetUserForRepo returns a mock user if one has been configured.
func (m *MockGitHubAuthService) GetUserForRepo(ctx context.Context, repoOwner string) (*models.User, error) {
	if user, exists := m.users[repoOwner]; exists {
		return user, nil
	}
	return nil, nil
}

// SlackCall represents a call made to the MockSlackService for testing assertions.
type SlackCall struct {
	Method    string
	TeamID    string
	Channel   string
	UserID    string
	Text      string
	Timestamp string
	Emoji     string
	Args      map[string]interface{}
}

// MockSlackService is a mock implementation of SlackService for testing.
type MockSlackService struct {
	mu    sync.Mutex
	calls []SlackCall
}

// NewMockSlackService creates a new mock Slack service.
func NewMockSlackService() *MockSlackService {
	return &MockSlackService{
		calls: make([]SlackCall, 0),
	}
}

// GetCalls returns all calls made to the mock service.
func (m *MockSlackService) GetCalls() []SlackCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	calls := make([]SlackCall, len(m.calls))
	copy(calls, m.calls)
	return calls
}

// GetCallsByMethod returns calls filtered by method name.
func (m *MockSlackService) GetCallsByMethod(method string) []SlackCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	var filtered []SlackCall
	for _, call := range m.calls {
		if call.Method == method {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

// ClearCalls removes all recorded calls.
func (m *MockSlackService) ClearCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = m.calls[:0]
}

// recordCall adds a call to the recorded calls list.
func (m *MockSlackService) recordCall(call SlackCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

// PostPRMessage mocks posting a PR message and returns a mock timestamp.
func (m *MockSlackService) PostPRMessage(
	ctx context.Context, teamID, channel, repoName, prTitle, prAuthor, prDescription, prURL string, prSize int,
	authorSlackUserID string,
) (string, error) {
	m.recordCall(SlackCall{
		Method:  "PostPRMessage",
		TeamID:  teamID,
		Channel: channel,
		Args: map[string]interface{}{
			"repoName":          repoName,
			"prTitle":           prTitle,
			"prAuthor":          prAuthor,
			"prDescription":     prDescription,
			"prURL":             prURL,
			"prSize":            prSize,
			"authorSlackUserID": authorSlackUserID,
		},
	})

	// Return a mock timestamp
	return "1234567890.123456", nil
}

// SendEphemeralMessage mocks sending an ephemeral message.
func (m *MockSlackService) SendEphemeralMessage(ctx context.Context, teamID, channel, userID, text string) error {
	m.recordCall(SlackCall{
		Method:  "SendEphemeralMessage",
		TeamID:  teamID,
		Channel: channel,
		UserID:  userID,
		Text:    text,
	})
	return nil
}

// AddReaction mocks adding a reaction.
func (m *MockSlackService) AddReaction(ctx context.Context, teamID, channel, timestamp, emoji string) error {
	m.recordCall(SlackCall{
		Method:    "AddReaction",
		TeamID:    teamID,
		Channel:   channel,
		Timestamp: timestamp,
		Emoji:     emoji,
	})
	return nil
}

// ValidateChannel mocks channel validation.
func (m *MockSlackService) ValidateChannel(ctx context.Context, teamID, channel string) error {
	m.recordCall(SlackCall{
		Method:  "ValidateChannel",
		TeamID:  teamID,
		Channel: channel,
	})
	return nil
}

// GetEmojiForReviewState mocks getting emoji for review state.
func (m *MockSlackService) GetEmojiForReviewState(state string) string {
	switch state {
	case "approved":
		return "white_check_mark"
	case "changes_requested":
		return "arrows_counterclockwise"
	case "commented":
		return "speech_balloon"
	default:
		return ""
	}
}

// GetEmojiForPRState mocks getting emoji for PR state.
func (m *MockSlackService) GetEmojiForPRState(state string, merged bool) string {
	if merged {
		return "merged"
	}
	return "closed"
}

// AddReactionToMultipleMessages mocks adding reactions to multiple messages.
func (m *MockSlackService) AddReactionToMultipleMessages(ctx context.Context, messages []services.MessageRef, emoji string) error {
	for _, msg := range messages {
		m.recordCall(SlackCall{
			Method:    "AddReactionToMultipleMessages",
			Channel:   msg.Channel,
			Timestamp: msg.Timestamp,
			Emoji:     emoji,
		})
	}
	return nil
}

// RemoveReaction mocks removing a reaction.
func (m *MockSlackService) RemoveReaction(ctx context.Context, teamID, channel, timestamp, emoji string) error {
	m.recordCall(SlackCall{
		Method:    "RemoveReaction",
		TeamID:    teamID,
		Channel:   channel,
		Timestamp: timestamp,
		Emoji:     emoji,
	})
	return nil
}

// RemoveReactionFromMultipleMessages mocks removing reactions from multiple messages.
func (m *MockSlackService) RemoveReactionFromMultipleMessages(
	ctx context.Context, messages []services.MessageRef, emoji string,
) error {
	for _, msg := range messages {
		m.recordCall(SlackCall{
			Method:    "RemoveReactionFromMultipleMessages",
			Channel:   msg.Channel,
			Timestamp: msg.Timestamp,
			Emoji:     emoji,
		})
	}
	return nil
}

// SyncAllReviewReactions mocks syncing all review reactions.
func (m *MockSlackService) SyncAllReviewReactions(
	ctx context.Context, messages []services.MessageRef, currentReviewState string,
) error {
	m.recordCall(SlackCall{
		Method: "SyncAllReviewReactions",
		Args: map[string]interface{}{
			"messageCount":       len(messages),
			"currentReviewState": currentReviewState,
		},
	})
	return nil
}

// ResolveChannelID mocks resolving channel ID.
func (m *MockSlackService) ResolveChannelID(ctx context.Context, channel string) (string, error) {
	m.recordCall(SlackCall{
		Method:  "ResolveChannelID",
		Channel: channel,
	})

	// Return the channel as-is for testing, or prepend 'C' if it doesn't start with it
	if channel[0] != 'C' {
		return "C" + channel, nil
	}
	return channel, nil
}

// ExtractChannelFromDescription mocks extracting channel from description.
func (m *MockSlackService) ExtractChannelFromDescription(description string) string {
	// Simple mock implementation - just return empty string
	return ""
}

// PublishHomeView mocks publishing home view.
func (m *MockSlackService) PublishHomeView(ctx context.Context, teamID, userID string, view slack.HomeTabViewRequest) error {
	m.recordCall(SlackCall{
		Method: "PublishHomeView",
		TeamID: teamID,
		UserID: userID,
	})
	return nil
}

// OpenView mocks opening a view.
func (m *MockSlackService) OpenView(
	ctx context.Context, teamID, triggerID string, view slack.ModalViewRequest,
) (*slack.ViewResponse, error) {
	m.recordCall(SlackCall{
		Method: "OpenView",
		TeamID: teamID,
		Args: map[string]interface{}{
			"triggerID": triggerID,
		},
	})
	return &slack.ViewResponse{}, nil
}

// BuildHomeView mocks building home view.
func (m *MockSlackService) BuildHomeView(user *models.User) slack.HomeTabViewRequest {
	return slack.HomeTabViewRequest{}
}

// BuildOAuthModal mocks building OAuth modal.
func (m *MockSlackService) BuildOAuthModal(oauthURL string) slack.ModalViewRequest {
	return slack.ModalViewRequest{}
}

// BuildChannelSelectorModal mocks building channel selector modal.
func (m *MockSlackService) BuildChannelSelectorModal() slack.ModalViewRequest {
	return slack.ModalViewRequest{}
}

// GetChannelName mocks getting channel name.
func (m *MockSlackService) GetChannelName(ctx context.Context, teamID, channelID string) (string, error) {
	m.recordCall(SlackCall{
		Method:  "GetChannelName",
		TeamID:  teamID,
		Channel: channelID,
	})
	return "test-channel", nil
}
