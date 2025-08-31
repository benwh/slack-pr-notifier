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
	"github-slack-notifier/internal/models"

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
	// ErrNoWorkspaceConfigurations is returned when no workspace configurations are found for a repository.
	ErrNoWorkspaceConfigurations = errors.New("no workspace configurations found for repository")
)

const (
	expectedRepoParts = 2
	maxReviewsPerPage = 100
)

// ClientForRepoWithWorkspace returns a GitHub client configured for the given repository with workspace validation.
// It ensures that only repositories from installations owned by the specified workspace can be accessed.
func (s *GitHubService) ClientForRepoWithWorkspace(ctx context.Context, repoFullName, workspaceID string) (*github.Client, error) {
	installation, err := s.ValidateWorkspaceInstallationAccess(ctx, repoFullName, workspaceID)
	if err != nil {
		return nil, err
	}

	return s.createAndCacheClient(ctx, installation, repoFullName)
}

// ValidateWorkspaceInstallationAccess validates that a workspace has access to a repository via GitHub installation.
func (s *GitHubService) ValidateWorkspaceInstallationAccess(
	ctx context.Context, repoFullName, workspaceID string,
) (*models.GitHubInstallation, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) != expectedRepoParts {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRepoFormat, repoFullName)
	}
	owner := parts[0]

	// Check if workspace has installation for this repository owner
	installation, err := s.firestoreService.GetGitHubInstallationByRepoOwner(ctx, owner, workspaceID)
	if err != nil {
		if errors.Is(err, ErrGitHubInstallationNotFound) {
			log.Warn(ctx, "Workspace does not have GitHub installation for repository owner",
				"workspace_id", workspaceID,
				"repo_owner", owner,
				"repo", repoFullName,
			)
			return nil, fmt.Errorf("%w for %s: workspace %s", models.ErrWorkspaceNoInstallation, owner, workspaceID)
		}
		return nil, fmt.Errorf("failed to validate workspace installation access: %w", err)
	}

	// Note: We don't validate the repository list for "selected" repositories because:
	// 1. GitHub only sends webhooks for repositories that are actually included in the installation
	// 2. The installation.created webhook doesn't reliably include the repository list
	// 3. The key security check is the workspace-to-installation mapping above
	// 4. If we receive a webhook, it's proof the repository is authorized

	log.Debug(ctx, "Workspace installation access validated",
		"workspace_id", workspaceID,
		"installation_id", installation.ID,
		"repo_owner", owner,
		"repo", repoFullName,
		"repository_selection", installation.RepositorySelection,
	)

	return installation, nil
}

// createAndCacheClient creates and caches a GitHub client for an installation.
func (s *GitHubService) createAndCacheClient(
	ctx context.Context, installation *models.GitHubInstallation, repoFullName string,
) (*github.Client, error) {
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

	// Get any workspace that has this repository configured
	repos, err := s.firestoreService.GetReposForAllWorkspaces(ctx, repoFullName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get repository configurations: %w", err)
	}
	if len(repos) == 0 {
		return nil, "", fmt.Errorf("%w: %s", ErrNoWorkspaceConfigurations, repoFullName)
	}

	// Use the first workspace's installation (any valid one will work for reading PR data)
	client, err := s.ClientForRepoWithWorkspace(ctx, repoFullName, repos[0].WorkspaceID)
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
		// TODO: Should probably bubble-up an error, so that we retry the job in case of a
		// transient failure? Need to check error handling in caller!
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
		// TODO: enum
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
// Priority order: changes_requested > approved > commented.
func determineOverallReviewState(userReviewStates map[string]string) string {
	if len(userReviewStates) == 0 {
		return ""
	}

	// Priority: changes_requested > approved > commented
	hasChangesRequested := false
	hasApproved := false
	hasCommented := false

	// TODO: Should define these states as an enum, then use them in GetEmojiForReviewState
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
