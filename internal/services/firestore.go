package services

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for not found cases.
var (
	ErrUserNotFound               = errors.New("user not found")
	ErrTrackedMessageNotFound     = errors.New("tracked message not found")
	ErrRepoNotFound               = errors.New("repository not found")
	ErrRepoAlreadyExists          = errors.New("repository already exists")
	ErrOAuthStateNotFound         = errors.New("OAuth state not found")
	ErrGitHubInstallationNotFound = errors.New("GitHub installation not found")
	ErrInvalidMessageID           = errors.New("message ID is required for update")
)

// FirestoreService provides database operations for Firestore.
type FirestoreService struct {
	client *firestore.Client
}

// NewFirestoreService creates a new FirestoreService with the provided client.
func NewFirestoreService(client *firestore.Client) *FirestoreService {
	return &FirestoreService{client: client}
}

// GetUserBySlackID retrieves a user by their Slack user ID.
func (fs *FirestoreService) GetUserBySlackID(ctx context.Context, slackUserID string) (*models.User, error) {
	iter := fs.client.Collection("users").Where("slack_user_id", "==", slackUserID).Documents(ctx)
	doc, err := iter.Next()
	if err != nil {
		if status.Code(err) == codes.NotFound || err.Error() == "no more items in iterator" {
			return nil, nil
		}
		log.Error(ctx, "Failed to query user by Slack ID",
			"error", err,
			"slack_user_id", slackUserID,
			"operation", "query_user_by_slack_id",
		)
		return nil, fmt.Errorf("failed to query user by slack ID %s: %w", slackUserID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data by Slack ID",
			"error", err,
			"slack_user_id", slackUserID,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for slack ID %s: %w", slackUserID, err)
	}

	return &user, nil
}

// GetUserByGitHubID retrieves a user by their GitHub document ID.
func (fs *FirestoreService) GetUserByGitHubID(ctx context.Context, githubUserID string) (*models.User, error) {
	doc, err := fs.client.Collection("users").Doc(githubUserID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		log.Error(ctx, "Failed to get user by GitHub ID",
			"error", err,
			"github_user_id", githubUserID,
			"operation", "get_user_by_github_id",
		)
		return nil, fmt.Errorf("failed to get user by github ID %s: %w", githubUserID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data by GitHub ID",
			"error", err,
			"github_user_id", githubUserID,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for github ID %s: %w", githubUserID, err)
	}

	return &user, nil
}

// GetUserByGitHubUsernameAndWorkspace retrieves a user by their GitHub username and Slack workspace ID.
func (fs *FirestoreService) GetUserByGitHubUsernameAndWorkspace(
	ctx context.Context, githubUsername, workspaceID string,
) (*models.User, error) {
	iter := fs.client.Collection("users").
		Where("github_username", "==", githubUsername).
		Where("slack_team_id", "==", workspaceID).
		Documents(ctx)
	doc, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, nil
		}
		log.Error(ctx, "Failed to get user by GitHub username and workspace",
			"error", err,
			"github_username", githubUsername,
			"workspace_id", workspaceID,
			"operation", "query_user_by_github_username_and_workspace",
		)
		return nil, fmt.Errorf("failed to get user by github username %s and workspace %s: %w", githubUsername, workspaceID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data by GitHub username and workspace",
			"error", err,
			"github_username", githubUsername,
			"workspace_id", workspaceID,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for github username %s and workspace %s: %w", githubUsername, workspaceID, err)
	}

	return &user, nil
}

// GetUserByGitHubUserID retrieves a user by their GitHub numeric user ID.
func (fs *FirestoreService) GetUserByGitHubUserID(ctx context.Context, githubUserID int64) (*models.User, error) {
	iter := fs.client.Collection("users").Where("github_user_id", "==", githubUserID).Documents(ctx)
	doc, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, nil
		}
		log.Error(ctx, "Failed to get user by GitHub user ID",
			"error", err,
			"github_user_id", githubUserID,
			"operation", "query_user_by_github_user_id",
		)
		return nil, fmt.Errorf("failed to get user by github user ID %d: %w", githubUserID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data by GitHub user ID",
			"error", err,
			"github_user_id", githubUserID,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for github user ID %d: %w", githubUserID, err)
	}

	return &user, nil
}

// CreateOrUpdateUser creates a new user or updates an existing user, setting timestamps appropriately.
func (fs *FirestoreService) CreateOrUpdateUser(ctx context.Context, user *models.User) error {
	user.UpdatedAt = time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}

	_, err := fs.client.Collection("users").Doc(user.ID).Set(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to create or update user",
			"error", err,
			"user_id", user.ID,
			"github_username", user.GitHubUsername,
			"operation", "create_or_update_user",
		)
		return fmt.Errorf("failed to create or update user %s: %w", user.ID, err)
	}
	return nil
}

// GetRepo retrieves a repository configuration for a specific workspace.
func (fs *FirestoreService) GetRepo(ctx context.Context, repoFullName, slackTeamID string) (*models.Repo, error) {
	docID := fs.encodeRepoDocID(slackTeamID, repoFullName)
	doc, err := fs.client.Collection("repos").Doc(docID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		log.Error(ctx, "Failed to get repository",
			"error", err,
			"repo", repoFullName,
			"slack_team_id", slackTeamID,
			"operation", "get_repo",
		)
		return nil, fmt.Errorf("failed to get repo %s for team %s: %w", repoFullName, slackTeamID, err)
	}

	var repo models.Repo
	err = doc.DataTo(&repo)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal repository data",
			"error", err,
			"repo", repoFullName,
			"slack_team_id", slackTeamID,
			"operation", "unmarshal_repo_data",
		)
		return nil, fmt.Errorf("failed to unmarshal repo data for %s team %s: %w", repoFullName, slackTeamID, err)
	}

	return &repo, nil
}

// CreateRepo creates a new repository configuration, setting creation timestamp and denormalized fields.
func (fs *FirestoreService) CreateRepo(ctx context.Context, repo *models.Repo) error {
	repo.CreatedAt = time.Now()
	repo.RepoFullName = repo.ID // Ensure denormalized field is set
	// WorkspaceID should already be set by caller

	docID := fs.encodeRepoDocID(repo.WorkspaceID, repo.RepoFullName)
	_, err := fs.client.Collection("repos").Doc(docID).Set(ctx, repo)

	if err != nil {
		return fmt.Errorf("failed to create repo %s for team %s: %w",
			repo.RepoFullName, repo.WorkspaceID, err)
	}

	log.Info(ctx, "Repository created",
		"repo", repo.RepoFullName,
		"workspace_id", repo.WorkspaceID,
	)
	return nil
}

// CreateRepoIfNotExists atomically creates a repository only if it doesn't already exist.
// This prevents race conditions during concurrent auto-registration attempts.
func (fs *FirestoreService) CreateRepoIfNotExists(ctx context.Context, repo *models.Repo) error {
	repo.CreatedAt = time.Now()
	// RepoFullName and WorkspaceID should already be set by caller

	docID := fs.encodeRepoDocID(repo.WorkspaceID, repo.RepoFullName)
	docRef := fs.client.Collection("repos").Doc(docID)

	return fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Check if repository already exists
		doc, err := tx.Get(docRef)
		if err != nil && status.Code(err) != codes.NotFound {
			log.Error(ctx, "Failed to check existing repository in transaction",
				"error", err,
				"repo", repo.RepoFullName,
				"workspace_id", repo.WorkspaceID,
			)
			return fmt.Errorf("failed to check existing repo %s for team %s: %w",
				repo.RepoFullName, repo.WorkspaceID, err)
		}

		// If repository already exists, return error
		if doc.Exists() {
			log.Info(ctx, "Repository already exists, skipping creation",
				"repo", repo.RepoFullName,
				"workspace_id", repo.WorkspaceID,
			)
			return fmt.Errorf("%w for %s in workspace %s", ErrRepoAlreadyExists, repo.RepoFullName, repo.WorkspaceID)
		}

		// Repository doesn't exist, create it atomically
		err = tx.Set(docRef, repo)
		if err != nil {
			log.Error(ctx, "Failed to create repository in transaction",
				"error", err,
				"repo", repo.RepoFullName,
				"workspace_id", repo.WorkspaceID,
			)
			return fmt.Errorf("failed to create repo %s for team %s: %w",
				repo.RepoFullName, repo.WorkspaceID, err)
		}

		log.Info(ctx, "Repository created atomically",
			"repo", repo.RepoFullName,
			"workspace_id", repo.WorkspaceID,
		)
		return nil
	})
}

// TrackedMessage operations for the new manual PR link tracking system.

// GetTrackedMessages retrieves all tracked messages for a specific PR in a channel.
// If messageSource is provided, filters by message source ("bot" or "manual").
func (fs *FirestoreService) GetTrackedMessages(
	ctx context.Context,
	repoFullName string,
	prNumber int,
	slackChannel string,
	slackTeamID string,
	messageSource string,
) ([]*models.TrackedMessage, error) {
	query := fs.client.Collection("trackedmessages").
		Where("repo_full_name", "==", repoFullName).
		Where("pr_number", "==", prNumber)

	if slackTeamID != "" {
		query = query.Where("slack_team_id", "==", slackTeamID)
	}

	if slackChannel != "" {
		query = query.Where("slack_channel", "==", slackChannel)
	}

	if messageSource != "" {
		query = query.Where("message_source", "==", messageSource)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	var messages []*models.TrackedMessage
	for {
		doc, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			log.Error(ctx, "Failed to query tracked messages",
				"error", err,
				"repo", repoFullName,
				"pr_number", prNumber,
				"slack_channel", slackChannel,
				"slack_team_id", slackTeamID,
				"message_source", messageSource,
				"operation", "query_tracked_messages",
			)
			return nil, fmt.Errorf("failed to query tracked messages for repo %s PR %d team %s: %w", repoFullName, prNumber, slackTeamID, err)
		}

		var message models.TrackedMessage
		err = doc.DataTo(&message)
		if err != nil {
			log.Error(ctx, "Failed to unmarshal tracked message data",
				"error", err,
				"doc_id", doc.Ref.ID,
				"operation", "unmarshal_tracked_message_data",
			)
			continue
		}

		messages = append(messages, &message)
	}

	return messages, nil
}

// CreateTrackedMessage creates a new tracked message record.
func (fs *FirestoreService) CreateTrackedMessage(ctx context.Context, message *models.TrackedMessage) error {
	message.CreatedAt = time.Now()
	docRef := fs.client.Collection("trackedmessages").NewDoc()
	message.ID = docRef.ID

	_, err := docRef.Set(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to create tracked message",
			"error", err,
			"repo", message.RepoFullName,
			"pr_number", message.PRNumber,
			"slack_channel", message.SlackChannel,
			"message_source", message.MessageSource,
			"operation", "create_tracked_message",
		)
		return fmt.Errorf("failed to create tracked message for repo %s PR %d: %w",
			message.RepoFullName, message.PRNumber, err)
	}
	return nil
}

// UpdateTrackedMessage updates an existing tracked message in Firestore.
func (fs *FirestoreService) UpdateTrackedMessage(ctx context.Context, message *models.TrackedMessage) error {
	if message.ID == "" {
		return ErrInvalidMessageID
	}

	docRef := fs.client.Collection("trackedmessages").Doc(message.ID)
	// Update only the CC-related fields instead of overwriting the entire document
	updates := []firestore.Update{
		{Path: "user_to_cc", Value: message.UserToCC},
		{Path: "has_review_directive", Value: message.HasReviewDirective},
	}
	_, err := docRef.Update(ctx, updates)
	if err != nil {
		log.Error(ctx, "Failed to update tracked message",
			"error", err,
			"message_id", message.ID,
			"repo", message.RepoFullName,
			"pr_number", message.PRNumber,
			"operation", "update_tracked_message",
		)
		return fmt.Errorf("failed to update tracked message %s: %w", message.ID, err)
	}

	log.Debug(ctx, "Successfully updated tracked message",
		"message_id", message.ID,
		"repo", message.RepoFullName,
		"pr_number", message.PRNumber,
		"user_to_cc", message.UserToCC,
		"has_review_directive", message.HasReviewDirective,
	)

	return nil
}

// DeleteTrackedMessages deletes multiple tracked messages by their IDs.
func (fs *FirestoreService) DeleteTrackedMessages(ctx context.Context, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	err := fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		for _, messageID := range messageIDs {
			docRef := fs.client.Collection("trackedmessages").Doc(messageID)
			err := tx.Delete(docRef)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Error(ctx, "Failed to delete tracked messages",
			"error", err,
			"message_count", len(messageIDs),
			"operation", "delete_tracked_messages",
		)
		return fmt.Errorf("failed to delete %d tracked messages: %w", len(messageIDs), err)
	}

	log.Info(ctx, "Successfully deleted tracked messages",
		"message_count", len(messageIDs),
	)
	return nil
}

// GetUser retrieves a user by their document ID (Slack user ID).
func (fs *FirestoreService) GetUser(ctx context.Context, userID string) (*models.User, error) {
	doc, err := fs.client.Collection("users").Doc(userID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrUserNotFound
		}
		log.Error(ctx, "Failed to get user",
			"error", err,
			"user_id", userID,
			"operation", "get_user",
		)
		return nil, fmt.Errorf("failed to get user %s: %w", userID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data",
			"error", err,
			"user_id", userID,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for %s: %w", userID, err)
	}

	return &user, nil
}

// SaveUser saves or updates a user document.
func (fs *FirestoreService) SaveUser(ctx context.Context, user *models.User) error {
	user.UpdatedAt = time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}

	_, err := fs.client.Collection("users").Doc(user.ID).Set(ctx, user)
	if err != nil {
		log.Error(ctx, "Failed to save user",
			"error", err,
			"user_id", user.ID,
			"github_username", user.GitHubUsername,
			"verified", user.Verified,
			"operation", "save_user",
		)
		return fmt.Errorf("failed to save user %s: %w", user.ID, err)
	}
	return nil
}

// OAuth state operations.

// CreateOAuthState stores a new OAuth state for CSRF protection.
func (fs *FirestoreService) CreateOAuthState(ctx context.Context, state *models.OAuthState) error {
	_, err := fs.client.Collection("oauth_states").Doc(state.ID).Set(ctx, state)
	if err != nil {
		log.Error(ctx, "Failed to create OAuth state",
			"error", err,
			"state_id", state.ID,
			"slack_user_id", state.SlackUserID,
			"operation", "create_oauth_state",
		)
		return fmt.Errorf("failed to create OAuth state %s: %w", state.ID, err)
	}
	return nil
}

// GetOAuthState retrieves an OAuth state by ID.
func (fs *FirestoreService) GetOAuthState(ctx context.Context, stateID string) (*models.OAuthState, error) {
	doc, err := fs.client.Collection("oauth_states").Doc(stateID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrOAuthStateNotFound
		}
		log.Error(ctx, "Failed to get OAuth state",
			"error", err,
			"state_id", stateID,
			"operation", "get_oauth_state",
		)
		return nil, fmt.Errorf("failed to get OAuth state %s: %w", stateID, err)
	}

	var state models.OAuthState
	err = doc.DataTo(&state)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal OAuth state data",
			"error", err,
			"state_id", stateID,
			"operation", "unmarshal_oauth_state_data",
		)
		return nil, fmt.Errorf("failed to unmarshal OAuth state data for %s: %w", stateID, err)
	}

	return &state, nil
}

// DeleteOAuthState deletes an OAuth state by ID.
func (fs *FirestoreService) DeleteOAuthState(ctx context.Context, stateID string) error {
	_, err := fs.client.Collection("oauth_states").Doc(stateID).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// Already deleted, consider this success
			return nil
		}
		log.Error(ctx, "Failed to delete OAuth state",
			"error", err,
			"state_id", stateID,
			"operation", "delete_oauth_state",
		)
		return fmt.Errorf("failed to delete OAuth state %s: %w", stateID, err)
	}
	return nil
}

// encodeRepoName encodes a repository full name to be safe for use as a Firestore document ID.
// Forward slashes are not allowed in document IDs, so we URL encode the name.
func (fs *FirestoreService) encodeRepoName(repoFullName string) string {
	return url.QueryEscape(repoFullName)
}

// encodeRepoDocID creates a workspace-scoped document ID for repositories.
// Format: {slack_team_id}#{encoded_repo_name}.
func (fs *FirestoreService) encodeRepoDocID(slackTeamID, repoFullName string) string {
	return slackTeamID + "#" + fs.encodeRepoName(repoFullName)
}

// GetReposForAllWorkspaces retrieves all repository configurations for a given repository across all workspaces.
func (fs *FirestoreService) GetReposForAllWorkspaces(ctx context.Context, repoFullName string) ([]*models.Repo, error) {
	// Direct query on repos collection instead of mapping lookup
	iter := fs.client.Collection("repos").
		Where("repo_full_name", "==", repoFullName).
		Where("enabled", "==", true). // Optional: only get enabled repos
		Documents(ctx)
	defer iter.Stop()

	var repos []*models.Repo
	for {
		doc, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			return nil, fmt.Errorf("failed to query repos: %w", err)
		}

		var repo models.Repo
		if err := doc.DataTo(&repo); err != nil {
			log.Error(ctx, "Failed to unmarshal repository",
				"error", err,
				"doc_id", doc.Ref.ID,
			)
			continue
		}
		repos = append(repos, &repo)
	}

	return repos, nil
}

// DeleteRepo removes a repository configuration.
func (fs *FirestoreService) DeleteRepo(ctx context.Context, repoFullName, workspaceID string) error {
	docID := fs.encodeRepoDocID(workspaceID, repoFullName)
	_, err := fs.client.Collection("repos").Doc(docID).Delete(ctx)

	if err != nil {
		return fmt.Errorf("failed to delete repo %s for team %s: %w",
			repoFullName, workspaceID, err)
	}

	log.Info(ctx, "Repository deleted",
		"repo", repoFullName,
		"workspace_id", workspaceID,
	)
	return nil
}

// GetChannelConfig retrieves channel configuration.
func (fs *FirestoreService) GetChannelConfig(ctx context.Context, slackTeamID, channelID string) (*models.ChannelConfig, error) {
	docID := slackTeamID + "#" + channelID
	doc, err := fs.client.Collection("channel_configs").Doc(docID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil // No config means use defaults
		}
		return nil, fmt.Errorf("failed to get channel config: %w", err)
	}

	var config models.ChannelConfig
	err = doc.DataTo(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal channel config: %w", err)
	}

	return &config, nil
}

// SaveChannelConfig creates or updates channel configuration.
func (fs *FirestoreService) SaveChannelConfig(ctx context.Context, config *models.ChannelConfig) error {
	config.UpdatedAt = time.Now()
	if config.CreatedAt.IsZero() {
		config.CreatedAt = time.Now()
	}

	docID := config.SlackTeamID + "#" + config.SlackChannelID
	_, err := fs.client.Collection("channel_configs").Doc(docID).Set(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to save channel config: %w", err)
	}

	return nil
}

// ListChannelConfigs retrieves all channel configurations for a workspace.
func (fs *FirestoreService) ListChannelConfigs(ctx context.Context, slackTeamID string) ([]*models.ChannelConfig, error) {
	iter := fs.client.Collection("channel_configs").
		Where("slack_team_id", "==", slackTeamID).
		Documents(ctx)
	defer iter.Stop()

	var configs []*models.ChannelConfig
	for {
		doc, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			return nil, fmt.Errorf("failed to list channel configs: %w", err)
		}

		var config models.ChannelConfig
		err = doc.DataTo(&config)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal channel config: %w", err)
		}

		configs = append(configs, &config)
	}

	// Sort by channel name in memory to avoid Firestore index requirement
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].SlackChannelName < configs[j].SlackChannelName
	})

	return configs, nil
}

// CreateGitHubInstallation creates a new GitHub installation record.
func (fs *FirestoreService) CreateGitHubInstallation(ctx context.Context, installation *models.GitHubInstallation) error {
	if err := installation.Validate(); err != nil {
		return fmt.Errorf("invalid GitHub installation: %w", err)
	}

	// Use installation ID as document ID
	docID := fmt.Sprintf("%d", installation.ID)

	_, err := fs.client.Collection("github_installations").Doc(docID).Set(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to create GitHub installation",
			"error", err,
			"installation_id", installation.ID,
			"account_login", installation.AccountLogin,
		)
		return fmt.Errorf("failed to create GitHub installation: %w", err)
	}

	log.Info(ctx, "GitHub installation created successfully",
		"installation_id", installation.ID,
		"account_login", installation.AccountLogin,
		"account_type", installation.AccountType,
		"repository_selection", installation.RepositorySelection,
	)

	return nil
}

// GetGitHubInstallationByID retrieves a GitHub installation by installation ID.
func (fs *FirestoreService) GetGitHubInstallationByID(ctx context.Context, installationID int64) (*models.GitHubInstallation, error) {
	docID := fmt.Sprintf("%d", installationID)
	doc, err := fs.client.Collection("github_installations").Doc(docID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrGitHubInstallationNotFound
		}
		log.Error(ctx, "Failed to get GitHub installation by ID",
			"error", err,
			"installation_id", installationID,
		)
		return nil, fmt.Errorf("failed to get GitHub installation: %w", err)
	}

	var installation models.GitHubInstallation
	if err := doc.DataTo(&installation); err != nil {
		log.Error(ctx, "Failed to unmarshal GitHub installation",
			"error", err,
			"installation_id", installationID,
		)
		return nil, fmt.Errorf("failed to unmarshal GitHub installation: %w", err)
	}

	return &installation, nil
}

// GetGitHubInstallationByAccountLogin retrieves a GitHub installation by account login (org/username).
func (fs *FirestoreService) GetGitHubInstallationByAccountLogin(
	ctx context.Context, accountLogin string,
) (*models.GitHubInstallation, error) {
	iter := fs.client.Collection("github_installations").Where("account_login", "==", accountLogin).Documents(ctx)
	defer iter.Stop()

	doc, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, ErrGitHubInstallationNotFound
		}
		log.Error(ctx, "Failed to query GitHub installation by account login",
			"error", err,
			"account_login", accountLogin,
		)
		return nil, fmt.Errorf("failed to query GitHub installation: %w", err)
	}

	var installation models.GitHubInstallation
	if err := doc.DataTo(&installation); err != nil {
		log.Error(ctx, "Failed to unmarshal GitHub installation",
			"error", err,
			"account_login", accountLogin,
		)
		return nil, fmt.Errorf("failed to unmarshal GitHub installation: %w", err)
	}

	return &installation, nil
}

// UpdateGitHubInstallation updates an existing GitHub installation.
func (fs *FirestoreService) UpdateGitHubInstallation(ctx context.Context, installation *models.GitHubInstallation) error {
	if err := installation.Validate(); err != nil {
		return fmt.Errorf("invalid GitHub installation: %w", err)
	}

	installation.UpdatedAt = time.Now()
	docID := fmt.Sprintf("%d", installation.ID)

	_, err := fs.client.Collection("github_installations").Doc(docID).Set(ctx, installation)
	if err != nil {
		log.Error(ctx, "Failed to update GitHub installation",
			"error", err,
			"installation_id", installation.ID,
			"account_login", installation.AccountLogin,
		)
		return fmt.Errorf("failed to update GitHub installation: %w", err)
	}

	log.Info(ctx, "GitHub installation updated successfully",
		"installation_id", installation.ID,
		"account_login", installation.AccountLogin,
	)

	return nil
}

// DeleteGitHubInstallation deletes a GitHub installation record.
func (fs *FirestoreService) DeleteGitHubInstallation(ctx context.Context, installationID int64) error {
	docID := fmt.Sprintf("%d", installationID)

	_, err := fs.client.Collection("github_installations").Doc(docID).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrGitHubInstallationNotFound
		}
		log.Error(ctx, "Failed to delete GitHub installation",
			"error", err,
			"installation_id", installationID,
		)
		return fmt.Errorf("failed to delete GitHub installation: %w", err)
	}

	log.Info(ctx, "GitHub installation deleted successfully",
		"installation_id", installationID,
	)

	return nil
}

// HasGitHubInstallations checks if any GitHub installations exist for a specific workspace.
func (fs *FirestoreService) HasGitHubInstallations(ctx context.Context, workspaceID string) (bool, error) {
	iter := fs.client.Collection("github_installations").
		Where("slack_workspace_id", "==", workspaceID).
		Limit(1).Documents(ctx)
	defer iter.Stop()

	doc, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		// No installations found for this workspace
		return false, nil
	}
	if err != nil {
		log.Error(ctx, "Failed to check for GitHub installations",
			"error", err,
			"workspace_id", workspaceID,
		)
		return false, fmt.Errorf("failed to check for GitHub installations: %w", err)
	}

	// At least one installation exists for this workspace
	return doc.Exists(), nil
}

// GetGitHubInstallationsByWorkspace retrieves all GitHub installations for a specific workspace.
func (fs *FirestoreService) GetGitHubInstallationsByWorkspace(
	ctx context.Context, workspaceID string,
) ([]*models.GitHubInstallation, error) {
	iter := fs.client.Collection("github_installations").
		Where("slack_workspace_id", "==", workspaceID).
		Documents(ctx)
	defer iter.Stop()

	var installations []*models.GitHubInstallation
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			log.Error(ctx, "Error iterating GitHub installations",
				"error", err,
				"workspace_id", workspaceID,
			)
			return nil, fmt.Errorf("failed to retrieve GitHub installations: %w", err)
		}

		var installation models.GitHubInstallation
		if err := doc.DataTo(&installation); err != nil {
			log.Error(ctx, "Failed to unmarshal GitHub installation",
				"error", err,
				"doc_id", doc.Ref.ID,
				"workspace_id", workspaceID,
			)
			return nil, fmt.Errorf("failed to unmarshal GitHub installation: %w", err)
		}

		installations = append(installations, &installation)
	}

	return installations, nil
}

// GetGitHubInstallationByRepoOwner finds a GitHub installation for a specific repository owner within a workspace.
func (fs *FirestoreService) GetGitHubInstallationByRepoOwner(
	ctx context.Context, repoOwner, workspaceID string,
) (*models.GitHubInstallation, error) {
	iter := fs.client.Collection("github_installations").
		Where("account_login", "==", repoOwner).
		Where("slack_workspace_id", "==", workspaceID).
		Documents(ctx)
	defer iter.Stop()

	doc, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		log.Warn(ctx, "GitHub installation not found for repository owner",
			"repo_owner", repoOwner,
			"workspace_id", workspaceID,
		)
		return nil, ErrGitHubInstallationNotFound
	}
	if err != nil {
		log.Error(ctx, "Failed to query GitHub installation",
			"error", err,
			"repo_owner", repoOwner,
			"workspace_id", workspaceID,
		)
		return nil, fmt.Errorf("failed to query GitHub installation: %w", err)
	}

	var installation models.GitHubInstallation
	if err := doc.DataTo(&installation); err != nil {
		log.Error(ctx, "Failed to unmarshal GitHub installation",
			"error", err,
			"doc_id", doc.Ref.ID,
			"repo_owner", repoOwner,
			"workspace_id", workspaceID,
		)
		return nil, fmt.Errorf("failed to unmarshal GitHub installation: %w", err)
	}

	return &installation, nil
}
