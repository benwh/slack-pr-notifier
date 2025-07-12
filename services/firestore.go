package services

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/models"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel errors for not found cases.
var (
	ErrUserNotFound    = errors.New("user not found")
	ErrMessageNotFound = errors.New("message not found")
	ErrRepoNotFound    = errors.New("repository not found")
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
		return nil, fmt.Errorf("failed to query user by slack ID %s: %w", slackUserID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
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
		return nil, fmt.Errorf("failed to get user by github ID %s: %w", githubUserID, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
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
		return nil, fmt.Errorf("failed to get user by github username %s: %w", githubUsername, err)
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
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
		return nil, fmt.Errorf("failed to query message for repo %s PR %d: %w", repoFullName, prNumber, err)
	}

	var message models.Message
	err = doc.DataTo(&message)
	if err != nil {
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
		return fmt.Errorf("failed to create message for repo %s PR %d: %w", message.RepoFullName, message.PRNumber, err)
	}
	return nil
}

func (fs *FirestoreService) UpdateMessage(ctx context.Context, message *models.Message) error {
	_, err := fs.client.Collection("messages").Doc(message.ID).Set(ctx, message)
	if err != nil {
		return fmt.Errorf("failed to update message %s: %w", message.ID, err)
	}
	return nil
}

func (fs *FirestoreService) GetRepo(ctx context.Context, repoFullName string) (*models.Repo, error) {
	doc, err := fs.client.Collection("repos").Doc(fs.encodeRepoName(repoFullName)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get repo %s: %w", repoFullName, err)
	}

	var repo models.Repo
	err = doc.DataTo(&repo)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal repo data for %s: %w", repoFullName, err)
	}

	return &repo, nil
}

func (fs *FirestoreService) CreateRepo(ctx context.Context, repo *models.Repo) error {
	repo.CreatedAt = time.Now()
	_, err := fs.client.Collection("repos").Doc(fs.encodeRepoName(repo.ID)).Set(ctx, repo)
	if err != nil {
		return fmt.Errorf("failed to create repo %s: %w", repo.ID, err)
	}
	return nil
}

// encodeRepoName encodes a repository full name to be safe for use as a Firestore document ID.
// Forward slashes are not allowed in document IDs, so we URL encode the name.
func (fs *FirestoreService) encodeRepoName(repoFullName string) string {
	return url.QueryEscape(repoFullName)
}
