package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrWorkspaceNotInstalled  = errors.New("workspace not installed")
	ErrNoSlackClientAvailable = errors.New("no Slack client available")
)

// SlackWorkspaceService manages Slack workspace installations and tokens.
type SlackWorkspaceService struct {
	client     *firestore.Client
	tokenCache map[string]*models.SlackWorkspace // Cache workspace tokens by team ID
	cacheMutex sync.RWMutex                      // Protects token cache
}

// NewSlackWorkspaceService creates a new SlackWorkspaceService.
func NewSlackWorkspaceService(client *firestore.Client) *SlackWorkspaceService {
	return &SlackWorkspaceService{
		client:     client,
		tokenCache: make(map[string]*models.SlackWorkspace),
	}
}

// SaveWorkspace saves or updates a workspace installation.
func (sws *SlackWorkspaceService) SaveWorkspace(ctx context.Context, workspace *models.SlackWorkspace) error {
	if err := workspace.Validate(); err != nil {
		return fmt.Errorf("invalid workspace: %w", err)
	}

	workspace.UpdatedAt = time.Now()

	// Save to Firestore using team ID as document ID
	_, err := sws.client.Collection("slack_workspaces").Doc(workspace.ID).Set(ctx, workspace)
	if err != nil {
		log.Error(ctx, "Failed to save workspace",
			"error", err,
			"team_id", workspace.ID,
			"team_name", workspace.TeamName,
			"operation", "save_workspace",
		)
		return fmt.Errorf("failed to save workspace: %w", err)
	}

	// Update cache
	sws.cacheMutex.Lock()
	sws.tokenCache[workspace.ID] = workspace
	sws.cacheMutex.Unlock()

	log.Info(ctx, "Workspace saved successfully",
		"team_id", workspace.ID,
		"team_name", workspace.TeamName,
		"installed_by", workspace.InstalledBy,
	)

	return nil
}

// GetWorkspace retrieves a workspace by team ID.
func (sws *SlackWorkspaceService) GetWorkspace(ctx context.Context, teamID string) (*models.SlackWorkspace, error) {
	// Check cache first
	sws.cacheMutex.RLock()
	if workspace, exists := sws.tokenCache[teamID]; exists {
		sws.cacheMutex.RUnlock()
		return workspace, nil
	}
	sws.cacheMutex.RUnlock()

	// Fetch from Firestore
	doc, err := sws.client.Collection("slack_workspaces").Doc(teamID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrWorkspaceNotFound
		}
		log.Error(ctx, "Failed to get workspace",
			"error", err,
			"team_id", teamID,
			"operation", "get_workspace",
		)
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	var workspace models.SlackWorkspace
	if err := doc.DataTo(&workspace); err != nil {
		log.Error(ctx, "Failed to decode workspace",
			"error", err,
			"team_id", teamID,
			"operation", "decode_workspace",
		)
		return nil, fmt.Errorf("failed to decode workspace: %w", err)
	}

	// Update cache
	sws.cacheMutex.Lock()
	sws.tokenCache[teamID] = &workspace
	sws.cacheMutex.Unlock()

	return &workspace, nil
}

// GetWorkspaceToken retrieves the bot token for a specific workspace.
func (sws *SlackWorkspaceService) GetWorkspaceToken(ctx context.Context, teamID string) (string, error) {
	workspace, err := sws.GetWorkspace(ctx, teamID)
	if err != nil {
		return "", err
	}
	return workspace.AccessToken, nil
}

// DeleteWorkspace removes a workspace installation (for uninstalls).
func (sws *SlackWorkspaceService) DeleteWorkspace(ctx context.Context, teamID string) error {
	_, err := sws.client.Collection("slack_workspaces").Doc(teamID).Delete(ctx)
	if err != nil {
		log.Error(ctx, "Failed to delete workspace",
			"error", err,
			"team_id", teamID,
			"operation", "delete_workspace",
		)
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	// Remove from cache
	sws.cacheMutex.Lock()
	delete(sws.tokenCache, teamID)
	sws.cacheMutex.Unlock()

	log.Info(ctx, "Workspace deleted successfully",
		"team_id", teamID,
	)

	return nil
}

// ListWorkspaces returns all installed workspaces.
func (sws *SlackWorkspaceService) ListWorkspaces(ctx context.Context) ([]*models.SlackWorkspace, error) {
	iter := sws.client.Collection("slack_workspaces").Documents(ctx)
	defer iter.Stop()

	var workspaces []*models.SlackWorkspace
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			log.Error(ctx, "Failed to iterate workspaces",
				"error", err,
				"operation", "list_workspaces",
			)
			return nil, fmt.Errorf("failed to iterate workspaces: %w", err)
		}

		var workspace models.SlackWorkspace
		if err := doc.DataTo(&workspace); err != nil {
			log.Error(ctx, "Failed to decode workspace",
				"error", err,
				"doc_id", doc.Ref.ID,
				"operation", "decode_workspace_list",
			)
			continue
		}

		workspaces = append(workspaces, &workspace)
	}

	return workspaces, nil
}

// IsWorkspaceInstalled checks if a workspace is installed.
func (sws *SlackWorkspaceService) IsWorkspaceInstalled(ctx context.Context, teamID string) (bool, error) {
	_, err := sws.GetWorkspace(ctx, teamID)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
