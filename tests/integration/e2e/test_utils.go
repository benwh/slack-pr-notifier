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
		ID:           teamID,
		TeamName:     teamName,
		AccessToken:  accessToken,
		Scope:        "channels:read,chat:write,reactions:write,reactions:read,links:read,channels:history",
		InstalledBy:  installedBy,
		InstalledAt:  time.Now(),
		UpdatedAt:    time.Now(),
		AppID:        "A123456789",
		BotUserID:    "U987654321",
		EnterpriseID: "",
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
func setupTrackedMessage(t *testing.T, harness *TestHarness, prNumber int, channelID string) {
	t.Helper()
	ctx := context.Background()
	const repoFullName = "testorg/testrepo" // All tests use the same test repository
	const teamID = "T123456789"             // All tests use the same test workspace
	const messageTS = "1234567890.123456"   // All tests use the same test message timestamp
	err := harness.SetupTrackedMessage(ctx, repoFullName, prNumber, channelID, teamID, messageTS)
	require.NoError(t, err)
}

// setupGitHubInstallation creates a test GitHub installation for the test organization.
func setupGitHubInstallation(t *testing.T, harness *TestHarness) {
	t.Helper()
	ctx := context.Background()
	// Set up installation for testorg with installation ID 12345 (matches test config)
	err := harness.SetupGitHubInstallation(ctx, 12345, "testorg", "Organization")
	require.NoError(t, err)
}
