package e2e

import (
	"context"
	"testing"
	"time"

	"github-slack-notifier/internal/models"

	"github.com/stretchr/testify/require"
)

// setupTestWorkspace creates a test workspace for OAuth functionality.
func setupTestWorkspace(t *testing.T, harness *TestHarness, installedBy string) {
	t.Helper()
	ctx := context.Background()
	const teamID = "T123456789"
	const teamName = "Test Workspace"
	const accessToken = "xoxb-test-token" // #nosec G101 -- Test token, not real credentials
	workspace := &models.SlackWorkspace{
		ID:          teamID,
		TeamName:    teamName,
		AccessToken: accessToken,
		Scope:       "channels:read,chat:write,links:read,channels:history",
		InstalledBy: installedBy,
		InstalledAt: time.Now(),
		UpdatedAt:   time.Now(),
	}
	err := harness.SlackWorkspaceService.SaveWorkspace(ctx, workspace)
	require.NoError(t, err)
}

// setupTestUser creates a test user in Firestore.
func setupTestUser(t *testing.T, harness *TestHarness, githubUsername, slackUserID, defaultChannel string) {
	t.Helper()
	ctx := context.Background()
	err := harness.SetupUser(ctx, githubUsername, slackUserID, defaultChannel)
	require.NoError(t, err)
}

// setupTestRepo creates a test repository in Firestore.
func setupTestRepo(t *testing.T, harness *TestHarness, channelID string) {
	t.Helper()
	ctx := context.Background()
	const repoFullName = "testorg/testrepo"
	const teamID = "T123456789"
	err := harness.SetupRepo(ctx, repoFullName, channelID, teamID)
	require.NoError(t, err)
}

// setupTrackedMessage creates a test tracked message in Firestore.
func setupTrackedMessage(t *testing.T, harness *TestHarness, repoFullName string, prNumber int, channelID, teamID, messageTS string) {
	t.Helper()
	ctx := context.Background()
	err := harness.SetupTrackedMessage(ctx, repoFullName, prNumber, channelID, teamID, messageTS)
	require.NoError(t, err)
}
