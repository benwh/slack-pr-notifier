package testutil

import (
	"encoding/json"
	"fmt"
)

// TestGitHubWebhooks contains fixture data for GitHub webhook events.
type TestGitHubWebhooks struct{}

// CreatePROpenedPayload creates a GitHub pull request opened webhook payload.
func (TestGitHubWebhooks) CreatePROpenedPayload(repoFullName string, prNumber int, prTitle, prBody, authorUsername string) []byte {
	payload := map[string]interface{}{
		"action": "opened",
		"pull_request": map[string]interface{}{
			"number": prNumber,
			"title":  prTitle,
			"body":   prBody,
			"user": map[string]interface{}{
				"login": authorUsername,
			},
			"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", repoFullName, prNumber),
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
			"name":      "test-repo",
			"owner": map[string]interface{}{
				"login": "test-owner",
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic("failed to marshal test payload: " + err.Error())
	}
	return data
}

// CreatePRReviewPayload creates a GitHub pull request review webhook payload.
func (TestGitHubWebhooks) CreatePRReviewPayload(repoFullName string, prNumber int, reviewState, reviewerUsername string) []byte {
	payload := map[string]interface{}{
		"action": "submitted",
		"review": map[string]interface{}{
			"state": reviewState, // "approved", "changes_requested", "commented"
			"user": map[string]interface{}{
				"login": reviewerUsername,
			},
		},
		"pull_request": map[string]interface{}{
			"number": prNumber,
			"title":  "Test PR",
			"user": map[string]interface{}{
				"login": "pr-author",
			},
		},
		"repository": map[string]interface{}{
			"full_name": repoFullName,
			"name":      "test-repo",
			"owner": map[string]interface{}{
				"login": "test-owner",
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic("failed to marshal test payload: " + err.Error())
	}
	return data
}

// TestConstants contains commonly used test constants.
type TestConstants struct {
	DefaultSlackTeamID    string
	DefaultSlackChannel   string
	DefaultSlackUserID    string
	DefaultSlackTimestamp string
	DefaultRepoFullName   string
	DefaultPRNumber       int
	DefaultGitHubUsername string
	DefaultGitHubUserID   int64
}

const (
	testPRNumber     = 123
	testGitHubUserID = 987654321
)

// NewTestConstants returns commonly used test constants.
func NewTestConstants() TestConstants {
	return TestConstants{
		DefaultSlackTeamID:    "T1234567890",
		DefaultSlackChannel:   "C1234567890",
		DefaultSlackUserID:    "U1234567890",
		DefaultSlackTimestamp: "1234567890.123456",
		DefaultRepoFullName:   "test-owner/test-repo",
		DefaultPRNumber:       testPRNumber,
		DefaultGitHubUsername: "test-user",
		DefaultGitHubUserID:   int64(testGitHubUserID),
	}
}
