package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"

	"github.com/google/go-github/v73/github"
)

// GitHubService provides methods for interacting with the GitHub API.
type GitHubService struct {
	client *github.Client
}

// NewGitHubService creates a new GitHubService instance.
func NewGitHubService(cfg *config.Config) *GitHubService {
	// Create an authenticated client with the GitHub App token
	transport := &github.BasicAuthTransport{
		Username: "x-access-token",
		Password: cfg.GitHubAppToken,
	}
	httpClient := transport.Client()

	return &GitHubService{
		client: github.NewClient(httpClient),
	}
}

var (
	// ErrInvalidRepoFormat is returned when repository name format is invalid.
	ErrInvalidRepoFormat = errors.New("invalid repository name format")
)

const (
	expectedRepoParts = 2
	maxReviewsPerPage = 100
)

// GetPullRequestWithReviews fetches a pull request and its review states.
func (s *GitHubService) GetPullRequestWithReviews(
	ctx context.Context, repoFullName string, prNumber int,
) (*github.PullRequest, string, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) != expectedRepoParts {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidRepoFormat, repoFullName)
	}
	owner, repo := parts[0], parts[1]

	// Fetch PR details
	pr, _, err := s.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch PR: %w", err)
	}

	// If PR is closed or merged, no need to check reviews
	if pr.GetState() != "open" {
		return pr, "", nil
	}

	// Fetch PR reviews
	reviews, _, err := s.client.PullRequests.ListReviews(ctx, owner, repo, prNumber, &github.ListOptions{
		PerPage: maxReviewsPerPage,
	})
	if err != nil {
		log.Error(ctx, "Failed to fetch PR reviews", "error", err)
		// Continue without review state - PR details are still useful
		return pr, "", nil
	}

	// Determine the latest review state per user
	userReviewStates := make(map[string]string)
	for _, review := range reviews {
		if review.User == nil || review.State == nil {
			continue
		}
		userID := review.User.GetLogin()
		state := review.GetState()

		// Only track meaningful review states
		if state == "APPROVED" || state == "CHANGES_REQUESTED" || state == "COMMENTED" {
			// Keep the latest review state for each user
			userReviewStates[userID] = strings.ToLower(state)
		}
	}

	// Determine overall review state based on all reviews
	currentReviewState := determineOverallReviewState(userReviewStates)

	log.Debug(ctx, "Fetched PR with reviews",
		"repo", repoFullName,
		"pr_number", prNumber,
		"pr_state", pr.GetState(),
		"review_state", currentReviewState,
		"review_count", len(reviews),
	)

	return pr, currentReviewState, nil
}

// determineOverallReviewState determines the overall review state from individual user reviews.
func determineOverallReviewState(userReviewStates map[string]string) string {
	if len(userReviewStates) == 0 {
		return ""
	}

	// Priority: changes_requested > approved > commented
	hasChangesRequested := false
	hasApproved := false
	hasCommented := false

	for _, state := range userReviewStates {
		switch state {
		case "changes_requested":
			hasChangesRequested = true
		case "approved":
			hasApproved = true
		case "commented":
			hasCommented = true
		}
	}

	// Return the highest priority state
	if hasChangesRequested {
		return "changes_requested"
	}
	if hasApproved {
		return "approved"
	}
	if hasCommented {
		return "commented"
	}

	return ""
}
