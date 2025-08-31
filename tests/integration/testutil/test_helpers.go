package testutil

import (
	"context"
	"testing"

	"github-slack-notifier/internal/models"
	"github.com/stretchr/testify/require"
)

// SetupTestUserAndRepo creates test user and repository for tests.
func SetupTestUserAndRepo(t *testing.T, app *TestApp, ctx context.Context, constants *TestConstants) {
	t.Helper()
	// Create test user
	user := &models.User{
		ID:                   constants.DefaultSlackUserID,
		SlackTeamID:          constants.DefaultSlackTeamID,
		GitHubUsername:       constants.DefaultGitHubUsername,
		GitHubUserID:         constants.DefaultGitHubUserID,
		DefaultChannel:       constants.DefaultSlackChannel,
		Verified:             true,
		NotificationsEnabled: true,
		ImpersonationEnabled: &[]bool{true}[0],
	}
	require.NoError(t, app.FirestoreService.SaveUser(ctx, user))

	// Create test repository configuration
	repo := &models.Repo{
		ID:           constants.DefaultRepoFullName,
		RepoFullName: constants.DefaultRepoFullName,
		WorkspaceID:  constants.DefaultSlackTeamID,
		Enabled:      true,
	}
	require.NoError(t, app.FirestoreService.CreateRepo(ctx, repo))
}
