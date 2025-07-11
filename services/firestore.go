package services

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/models"
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
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (fs *FirestoreService) GetUserByGitHubID(ctx context.Context, githubUserID string) (*models.User, error) {
	doc, err := fs.client.Collection("users").Doc(githubUserID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	var user models.User
	err = doc.DataTo(&user)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (fs *FirestoreService) CreateOrUpdateUser(ctx context.Context, user *models.User) error {
	user.UpdatedAt = time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}

	_, err := fs.client.Collection("users").Doc(user.ID).Set(ctx, user, firestore.MergeAll)
	return err
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
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	var message models.Message
	err = doc.DataTo(&message)
	if err != nil {
		return nil, err
	}

	return &message, nil
}

func (fs *FirestoreService) CreateMessage(ctx context.Context, message *models.Message) error {
	message.CreatedAt = time.Now()
	docRef := fs.client.Collection("messages").NewDoc()
	message.ID = docRef.ID

	_, err := docRef.Set(ctx, message)
	return err
}

func (fs *FirestoreService) UpdateMessage(ctx context.Context, message *models.Message) error {
	_, err := fs.client.Collection("messages").Doc(message.ID).Set(ctx, message, firestore.MergeAll)
	return err
}

func (fs *FirestoreService) GetRepo(ctx context.Context, repoFullName string) (*models.Repo, error) {
	doc, err := fs.client.Collection("repos").Doc(repoFullName).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}

	var repo models.Repo
	err = doc.DataTo(&repo)
	if err != nil {
		return nil, err
	}

	return &repo, nil
}

func (fs *FirestoreService) CreateRepo(ctx context.Context, repo *models.Repo) error {
	repo.CreatedAt = time.Now()
	_, err := fs.client.Collection("repos").Doc(repo.ID).Set(ctx, repo)
	return err
}
