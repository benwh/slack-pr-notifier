package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v73/github"
)

// GitHubService provides methods for interacting with the GitHub API.
type GitHubService struct {
	config           *config.Config
	firestoreService *FirestoreService
	privateKeyBytes  []byte
	clientCache      map[int64]*github.Client // Cache clients by installation ID
	transport        http.RoundTripper        // Custom transport for testing
}

// NewGitHubService creates a new GitHubService instance.
func NewGitHubService(cfg *config.Config, firestoreService *FirestoreService) (*GitHubService, error) {
	return NewGitHubServiceWithTransport(cfg, firestoreService, nil)
}

// NewGitHubServiceWithTransport creates a new GitHubService instance with a custom transport.
func NewGitHubServiceWithTransport(
	cfg *config.Config, firestoreService *FirestoreService, transport http.RoundTripper,
) (*GitHubService, error) {
	// Decode the base64 encoded private key
	privateKeyBytes, err := base64.StdEncoding.DecodeString(cfg.GitHubPrivateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode GitHub private key: %w", err)
	}

	// Use default transport if none provided
	if transport == nil {
		transport = http.DefaultTransport
	}

	return &GitHubService{
		config:           cfg,
		firestoreService: firestoreService,
		privateKeyBytes:  privateKeyBytes,
		clientCache:      make(map[int64]*github.Client),
		transport:        transport,
	}, nil
}

var (
	// ErrInvalidRepoFormat is returned when repository name format is invalid.
	ErrInvalidRepoFormat = errors.New("invalid repository name format")
	// ErrInstallationNotFound is returned when GitHub installation is not found.
	ErrInstallationNotFound = errors.New("GitHub installation not found for repository owner")
)

const (
	expectedRepoParts = 2
	maxReviewsPerPage = 100
)

// ClientForRepo returns a GitHub client configured for the given repository.
// It looks up the GitHub installation for the repository owner and creates a client.
func (s *GitHubService) ClientForRepo(ctx context.Context, repoFullName string) (*github.Client, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) != expectedRepoParts {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRepoFormat, repoFullName)
	}
	owner := parts[0]

	// Look up GitHub installation for this repository owner
	installation, err := s.firestoreService.GetGitHubInstallationByAccountLogin(ctx, owner)
	if err != nil {
		if errors.Is(err, ErrGitHubInstallationNotFound) {
			log.Error(ctx, "GitHub installation not found for repository owner",
				"owner", owner,
				"repo", repoFullName,
			)
			return nil, fmt.Errorf("%w for owner %s", ErrInstallationNotFound, owner)
		}
		return nil, fmt.Errorf("failed to lookup GitHub installation: %w", err)
	}

	// Check if we have a cached client for this installation
	if client, exists := s.clientCache[installation.ID]; exists {
		return client, nil
	}

	// Create new client for this installation
	client, err := s.createClientForInstallation(installation.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client for installation %d: %w", installation.ID, err)
	}

	// Cache the client
	s.clientCache[installation.ID] = client

	log.Debug(ctx, "Created GitHub client for repository",
		"repo", repoFullName,
		"owner", owner,
		"installation_id", installation.ID,
	)

	return client, nil
}

// createClientForInstallation creates a GitHub client for a specific installation.
func (s *GitHubService) createClientForInstallation(installationID int64) (*github.Client, error) {
	// Create the installation transport
	itr, err := ghinstallation.New(
		s.transport,
		s.config.GitHubAppID,
		installationID,
		s.privateKeyBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub App installation transport: %w", err)
	}

	// Create GitHub client with the installation transport
	client := github.NewClient(&http.Client{Transport: itr})
	return client, nil
}

// GetPullRequestWithReviews fetches a pull request and its review states.
func (s *GitHubService) GetPullRequestWithReviews(
	ctx context.Context, repoFullName string, prNumber int,
) (*github.PullRequest, string, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) != expectedRepoParts {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidRepoFormat, repoFullName)
	}
	owner, repo := parts[0], parts[1]

	// Get client for this repository
	client, err := s.ClientForRepo(ctx, repoFullName)
	if err != nil {
		return nil, "", err
	}

	// Fetch PR details
	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch PR: %w", err)
	}

	// If PR is closed or merged, no need to check reviews
	if pr.GetState() != "open" {
		return pr, "", nil
	}

	// Fetch PR reviews
	reviews, _, err := client.PullRequests.ListReviews(ctx, owner, repo, prNumber, &github.ListOptions{
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
