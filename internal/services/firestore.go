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
	ErrUserNotFound           = errors.New("user not found")
	ErrMessageNotFound        = errors.New("message not found")
	ErrTrackedMessageNotFound = errors.New("tracked message not found")
	ErrRepoNotFound           = errors.New("repository not found")
	ErrOAuthStateNotFound     = errors.New("OAuth state not found")
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

func (fs *FirestoreService) GetUserByGitHubUsername(ctx context.Context, githubUsername string) (*models.User, error) {
	iter := fs.client.Collection("users").Where("github_username", "==", githubUsername).Documents(ctx)
	doc, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, nil
		}
		log.Error(ctx, "Failed to get user by GitHub username",
			"error", err,
			"github_username", githubUsername,
			"operation", "query_user_by_github_username",
		)
		return nil, fmt.Errorf("failed to get user by github username %s: %w", githubUsername, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal user data by GitHub username",
			"error", err,
			"github_username", githubUsername,
			"operation", "unmarshal_user_data",
		)
		return nil, fmt.Errorf("failed to unmarshal user data for github username %s: %w", githubUsername, err)
	}

	return &user, nil
}

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
			"slack_user_id", user.SlackUserID,
			"github_username", user.GitHubUsername,
			"operation", "create_or_update_user",
		)
		return fmt.Errorf("failed to create or update user %s: %w", user.ID, err)
	}
	return nil
}

func (fs *FirestoreService) GetMessage(
	ctx context.Context,
	repoFullName string,
	prNumber int,
) (*models.Message, error) {
	iter := fs.client.Collection("messages").
		Where("repo_full_name", "==", repoFullName).
		Where("pr_number", "==", prNumber).
		Documents(ctx)

	doc, err := iter.Next()
	if err != nil {
		if status.Code(err) == codes.NotFound || err.Error() == "no more items in iterator" {
			return nil, nil
		}
		log.Error(ctx, "Failed to query message",
			"error", err,
			"repo", repoFullName,
			"pr_number", prNumber,
			"operation", "query_message",
		)
		return nil, fmt.Errorf("failed to query message for repo %s PR %d: %w", repoFullName, prNumber, err)
	}

	var message models.Message
	err = doc.DataTo(&message)
	if err != nil {
		log.Error(ctx, "Failed to unmarshal message data",
			"error", err,
			"repo", repoFullName,
			"pr_number", prNumber,
			"operation", "unmarshal_message_data",
		)
		return nil, fmt.Errorf("failed to unmarshal message data for repo %s PR %d: %w", repoFullName, prNumber, err)
	}

	return &message, nil
}

func (fs *FirestoreService) CreateMessage(ctx context.Context, message *models.Message) error {
	message.CreatedAt = time.Now()
	docRef := fs.client.Collection("messages").NewDoc()
	message.ID = docRef.ID

	_, err := docRef.Set(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to create message",
			"error", err,
			"repo", message.RepoFullName,
			"pr_number", message.PRNumber,
			"slack_channel", message.SlackChannel,
			"author", message.AuthorGitHubUsername,
			"operation", "create_message",
		)
		return fmt.Errorf("failed to create message for repo %s PR %d: %w", message.RepoFullName, message.PRNumber, err)
	}
	return nil
}

func (fs *FirestoreService) UpdateMessage(ctx context.Context, message *models.Message) error {
	_, err := fs.client.Collection("messages").Doc(message.ID).Set(ctx, message)
	if err != nil {
		log.Error(ctx, "Failed to update message",
			"error", err,
			"message_id", message.ID,
			"repo", message.RepoFullName,
			"pr_number", message.PRNumber,
			"last_status", message.LastStatus,
			"operation", "update_message",
		)
		return fmt.Errorf("failed to update message %s: %w", message.ID, err)
	}
	return nil
}

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

func (fs *FirestoreService) CreateRepo(ctx context.Context, repo *models.Repo) error {
	repo.CreatedAt = time.Now()
	docID := fs.encodeRepoDocID(repo.SlackTeamID, repo.ID)

	// Use a transaction to atomically create the repo and workspace mapping
	err := fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Create the repository document
		repoRef := fs.client.Collection("repos").Doc(docID)
		if err := tx.Set(repoRef, repo); err != nil {
			return fmt.Errorf("failed to create repo document: %w", err)
		}

		// Create the workspace mapping document
		mappingID := fs.encodeMappingDocID(repo.ID, repo.SlackTeamID)
		mapping := &models.RepoWorkspaceMapping{
			ID:           mappingID,
			RepoFullName: repo.ID,
			WorkspaceID:  repo.SlackTeamID,
			CreatedAt:    time.Now(),
		}

		mappingRef := fs.client.Collection("repo_workspace_mappings").Doc(mappingID)
		if err := tx.Set(mappingRef, mapping); err != nil {
			return fmt.Errorf("failed to create workspace mapping: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Error(ctx, "Failed to create repository with workspace mapping",
			"error", err,
			"repo", repo.ID,
			"slack_team_id", repo.SlackTeamID,
			"default_channel", repo.DefaultChannel,
			"operation", "create_repo_with_mapping",
		)
		return fmt.Errorf("failed to create repo %s for team %s: %w", repo.ID, repo.SlackTeamID, err)
	}

	log.Info(ctx, "Repository created with workspace mapping",
		"repo", repo.ID,
		"slack_team_id", repo.SlackTeamID,
	)

	return nil
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
		Where("pr_number", "==", prNumber).
		Where("slack_team_id", "==", slackTeamID)

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
			"slack_user_id", user.SlackUserID,
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

// encodeMappingDocID creates a document ID for repo-workspace mappings.
// Format: {encoded_repo_name}#{slack_team_id}.
func (fs *FirestoreService) encodeMappingDocID(repoFullName, slackTeamID string) string {
	return fs.encodeRepoName(repoFullName) + "#" + slackTeamID
}

// GetReposForAllWorkspaces retrieves all repository configurations for a given repository across all workspaces.
// Uses workspace mappings to efficiently find all relevant workspaces.
func (fs *FirestoreService) GetReposForAllWorkspaces(ctx context.Context, repoFullName string) ([]*models.Repo, error) {
	// Query all mapping documents that start with the repo name
	mappings, err := fs.getWorkspaceMappingsForRepo(ctx, repoFullName)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace mappings for %s: %w", repoFullName, err)
	}

	if len(mappings) == 0 {
		log.Debug(ctx, "No workspace mappings found for repository", "repo", repoFullName)
		return []*models.Repo{}, nil
	}

	// Get the actual repository configurations for each workspace
	var repos []*models.Repo
	for _, mapping := range mappings {
		repo, err := fs.GetRepo(ctx, repoFullName, mapping.WorkspaceID)
		if err != nil {
			log.Error(ctx, "Failed to get repository configuration for workspace",
				"error", err,
				"repo", repoFullName,
				"slack_team_id", mapping.WorkspaceID,
			)
			continue // Continue with other workspaces
		}

		if repo != nil {
			repos = append(repos, repo)
		} else {
			log.Warn(ctx, "Workspace mapping exists but repository configuration not found",
				"repo", repoFullName,
				"slack_team_id", mapping.WorkspaceID,
			)
		}
	}

	return repos, nil
}

// getWorkspaceMappingsForRepo queries all workspace mappings for a specific repository.
func (fs *FirestoreService) getWorkspaceMappingsForRepo(ctx context.Context, repoFullName string) ([]*models.RepoWorkspaceMapping, error) {
	// Query documents where repo_full_name equals the given repository
	iter := fs.client.Collection("repo_workspace_mappings").
		Where("repo_full_name", "==", repoFullName).
		Documents(ctx)
	defer iter.Stop()

	var mappings []*models.RepoWorkspaceMapping
	for {
		doc, err := iter.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			return nil, fmt.Errorf("failed to query workspace mappings: %w", err)
		}

		var mapping models.RepoWorkspaceMapping
		if err := doc.DataTo(&mapping); err != nil {
			log.Error(ctx, "Failed to unmarshal workspace mapping",
				"error", err,
				"doc_id", doc.Ref.ID,
			)
			continue // Skip malformed documents
		}

		mappings = append(mappings, &mapping)
	}

	return mappings, nil
}

// DeleteRepo removes a repository configuration and updates the workspace mapping.
func (fs *FirestoreService) DeleteRepo(ctx context.Context, repoFullName, slackTeamID string) error {
	docID := fs.encodeRepoDocID(slackTeamID, repoFullName)
	mappingID := fs.encodeMappingDocID(repoFullName, slackTeamID)

	// Use a transaction to atomically delete the repo and workspace mapping
	err := fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Delete the repository document
		repoRef := fs.client.Collection("repos").Doc(docID)
		if err := tx.Delete(repoRef); err != nil {
			return fmt.Errorf("failed to delete repo document: %w", err)
		}

		// Delete the workspace mapping document
		mappingRef := fs.client.Collection("repo_workspace_mappings").Doc(mappingID)
		if err := tx.Delete(mappingRef); err != nil {
			return fmt.Errorf("failed to delete workspace mapping: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Error(ctx, "Failed to delete repository with workspace mapping",
			"error", err,
			"repo", repoFullName,
			"slack_team_id", slackTeamID,
			"operation", "delete_repo_with_mapping",
		)
		return fmt.Errorf("failed to delete repo %s for team %s: %w", repoFullName, slackTeamID, err)
	}

	log.Info(ctx, "Repository deleted with workspace mapping update",
		"repo", repoFullName,
		"slack_team_id", slackTeamID,
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
