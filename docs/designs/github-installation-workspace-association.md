# GitHub App Installation Workspace Association - Technical Design

## Problem Statement

The current system has a critical multi-tenancy flaw: GitHub installations are stored globally without workspace association, allowing any Slack workspace to register repositories from any GitHub installation. This breaks tenant isolation and could allow unauthorized access to private repositories.

## Current Architecture Issues

1. **Global GitHub Installations**: `GitHubInstallation` model has no workspace association
2. **Incomplete OAuth Flow**: We redirect away from installation callbacks instead of processing the combined flow
3. **No Validation**: Repository registration doesn't verify workspace owns the installation
4. **Missing User Verification**: No verification that the installing user actually has access to the installation

## Root Cause Analysis

Our current implementation already has the right foundation but processes it incorrectly:

- ✅ Has "Request user authorization during installation" enabled (correct)
- ✅ Receives callbacks with `installation_id`, `setup_action=install`, `code`, and `state` (perfect!)
- ❌ But redirects users away instead of processing the combined OAuth + installation flow
- ❌ Never associates installations with workspaces or verifies user access

## Two Parallel OAuth Flows

Our solution maintains two distinct flows to handle different user intentions:

### Flow 1: User OAuth Only (Personal Account Linking)

For users who want to link their GitHub account without installing the app:

1. **User OAuth Initiation (from Slack)**
   - User clicks "Connect GitHub account" button in App Home
   - Generate OAuth state token (existing `OAuthState` model)
   - Store state with: `slack_workspace_id`, `slack_user_id`, `expires_at`
   - Redirect to: `https://github.com/login/oauth/authorize?client_id=CLIENT_ID&state=STATE_TOKEN`

2. **GitHub User Authorization Only**
   - User authorizes the app for their personal account
   - GitHub redirects back with: `?code=XXX&state=STATE_TOKEN` (no `installation_id`)

3. **User OAuth Callback Processing**
   - Validate state token exists and hasn't expired
   - Exchange OAuth `code` for user access token
   - Link user's GitHub account to Slack user
   - Clean up state token

### Flow 2: Combined OAuth + Installation (App Installation)

For users who want to install the app on repositories AND link their account:

1. **Installation Initiation (from Slack)**
   - User clicks "Install GitHub App" button in App Home
   - Generate OAuth state token (same `OAuthState` model)
   - Store state with: `slack_workspace_id`, `slack_user_id`, `expires_at`
   - Redirect to: `https://github.com/login/oauth/authorize?client_id=CLIENT_ID&state=STATE_TOKEN`

2. **GitHub Handles Both Installation AND User Auth**
   - User navigates through installation UI (selects repositories)
   - GitHub performs OAuth authorization automatically
   - GitHub redirects back with: `?code=XXX&installation_id=YYY&setup_action=install&state=STATE_TOKEN`

3. **Combined Callback Processing**
   - Validate state token exists and hasn't expired
   - Exchange OAuth `code` for user access token
   - **Verify user has access to installation** using GitHub API
   - Store installation with workspace association
   - Complete user GitHub account linking
   - Clean up state token

### Data Model Changes

```go
// Add workspace association to GitHubInstallation
type GitHubInstallation struct {
    // Existing fields...
    ID                   int64     `firestore:"id"`
    AccountLogin         string    `firestore:"account_login"`
    AccountType          string    `firestore:"account_type"`
    AccountID            int64     `firestore:"account_id"`
    RepositorySelection  string    `firestore:"repository_selection"`
    Repositories         []string  `firestore:"repositories,omitempty"`
    InstalledAt          time.Time `firestore:"installed_at"`
    UpdatedAt            time.Time `firestore:"updated_at"`
    
    // New fields for workspace association
    SlackWorkspaceID     string    `firestore:"slack_workspace_id,omitempty"`
    InstalledBySlackUser string    `firestore:"installed_by_slack_user,omitempty"`
    InstalledByGitHubUser int64    `firestore:"installed_by_github_user,omitempty"`
}

// Reuse existing OAuthState model - no new model needed!
// The existing OAuthState already has all fields we need:
// - SlackUserID, SlackTeamID for workspace association  
// - Expiration handling
// - State management
```

### API Changes

#### New Firestore Methods

```go
// Workspace-specific installation queries
func (fs *FirestoreService) HasGitHubInstallations(ctx context.Context, workspaceID string) (bool, error)
func (fs *FirestoreService) GetGitHubInstallationsByWorkspace(ctx context.Context, workspaceID string) ([]*models.GitHubInstallation, error)
func (fs *FirestoreService) GetGitHubInstallationByRepoOwner(ctx context.Context, repoOwner, workspaceID string) (*models.GitHubInstallation, error)
```

#### New Slack Handler Methods

```go
// Handle "Install GitHub App" button from App Home
func (sh *SlackHandler) handleInstallGitHubAppAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context)
```

#### Updated OAuth Handler

```go
// Enhanced callback processing to handle BOTH flows
func (h *OAuthHandler) validateGitHubCallbackParams(ctx context.Context, c *gin.Context) (string, string, bool)

// Process combined OAuth + installation flow
func (h *OAuthHandler) processGitHubAppInstallation(ctx context.Context, code, stateID, installationID string, state *models.OAuthState) error

// Process user OAuth only flow (existing, may need updates)
func (h *OAuthHandler) processUserOAuth(ctx context.Context, code, stateID string, state *models.OAuthState) error

// New method to verify user has access to installation
func (h *OAuthHandler) verifyUserInstallationAccess(ctx context.Context, userToken string, installationID int64) error
```

#### New GitHub Service Methods

```go
// Verify user can access installation
func (s *GitHubService) VerifyUserInstallationAccess(ctx context.Context, userToken string, installationID int64) error
```

### Validation Layer

#### Repository Registration Validation

```go
func (h *GitHubHandler) validateWorkspaceInstallationAccess(ctx context.Context, repoFullName, workspaceID string) (*models.GitHubInstallation, error) {
    // Extract repo owner from full name
    parts := strings.Split(repoFullName, "/")
    repoOwner := parts[0]
    
    // Check if workspace has installation for this repo owner
    installation, err := h.firestoreService.GetGitHubInstallationByRepoOwner(ctx, repoOwner, workspaceID)
    if err != nil {
        return nil, fmt.Errorf("workspace %s has no GitHub installation for %s", workspaceID, repoOwner)
    }
    
    // Validate repo is included in installation
    if installation.RepositorySelection == "selected" {
        if !contains(installation.Repositories, repoFullName) {
            return nil, fmt.Errorf("repository %s not included in installation", repoFullName)
        }
    }
    
    return installation, nil
}
```

### UI Changes

#### App Home Updates

```go
// Update button to initiate proper OAuth flow
func (b *HomeViewBuilder) buildGitHubInstallationWarning(workspaceID string) []slack.Block {
    return []slack.Block{
        slack.NewSectionBlock(
            slack.NewTextBlockObject(slack.MarkdownType,
                ":warning: *GitHub App Installation Required*\n"+
                    "PR Bot needs to be installed on your GitHub repositories to receive webhook events.\n\n"+
                    "This will redirect you to GitHub to select which repositories to install the app on.",
                false, false),
            nil,
            slack.NewAccessory(
                slack.NewButtonBlockElement(
                    "install_github_app",
                    "install_app",
                    slack.NewTextBlockObject(slack.PlainTextType, "Install GitHub App", false, false),
                ).WithStyle(slack.StylePrimary),
            ),
        ),
    }
}

// Update workspace-specific installation check
func (b *HomeViewBuilder) BuildHomeView(user *models.User, hasGitHubInstallations bool) slack.HomeTabViewRequest
```

### Security Considerations

1. **State Token Validation**: Each installation uses unique UUID state token (reusing existing OAuth state system)
2. **User Access Verification**: Use GitHub API to verify user actually has access to the installation
3. **Expiry Management**: State tokens expire after 15 minutes
4. **Workspace Isolation**: All installation queries are workspace-scoped
5. **Access Control**: Repository registration validates workspace ownership
6. **CSRF Protection**: State tokens prevent cross-site request forgery

### Key Security Flow

```go
func (h *OAuthHandler) verifyUserInstallationAccess(ctx context.Context, userToken string, installationID int64) error {
    // Call GET /user/installations/{installation_id} with user token
    // This verifies the user actually has legitimate access to this installation
    // Prevents spoofed installation_id parameters
    client := github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: userToken})))
    _, _, err := client.Apps.GetUserInstallation(ctx, installationID)
    return err
}
```

### Migration Strategy

Since we're pre-production:
1. **Clean Slate**: Wipe existing database (no migration needed)
2. **Fresh Installations**: All new installations will follow proper flow
3. **Testing**: Verify multi-workspace isolation works correctly

### Implementation Steps (Simplified)

1. **Phase 1: Model Updates**
   - Add workspace fields to `GitHubInstallation`
   - Add workspace-scoped Firestore methods
   - Update `HasGitHubInstallations` to be workspace-specific

2. **Phase 2: Fix OAuth Callback Flow**
   - Modify `validateGitHubCallbackParams` to detect and route both flows
   - Add `processGitHubAppInstallation` method for combined flow
   - Ensure `processUserOAuth` handles user-only flow correctly
   - Add user installation access verification
   - Store installation with workspace association

3. **Phase 3: Add Installation Initiation**
   - Add "Install GitHub App" button handler in SlackHandler
   - Generate OAuth state for installation flows (reuse existing system)
   - Redirect to GitHub OAuth authorize URL

4. **Phase 4: Add Validation**
   - Add workspace validation to repository operations
   - Update webhook processing to check installation ownership
   - Add validation to auto-registration flow

5. **Phase 5: UI Updates**
   - Update App Home installation warning with proper button
   - Make installation checking workspace-specific

6. **Phase 6: Testing**
   - Test user OAuth only flow (existing functionality)
   - Test combined OAuth + installation flow
   - Verify proper tenant isolation  
   - Test user access verification
   - Ensure both flows can coexist without interference

### Testing Scenarios

1. **User OAuth Only**: User connects GitHub account without installing app
2. **Combined Flow**: User installs app and links account in one flow
3. **Single Workspace**: User installs app from Slack, verifies proper association
4. **Multi-Workspace**: Two workspaces install for same GitHub org, verify isolation
5. **Concurrent Flows**: Multiple users using different flows simultaneously
6. **Unauthorized Access**: Verify workspace A cannot register repos from workspace B's installation
7. **State Expiry**: Verify expired state tokens are rejected for both flows
8. **Flow Interference**: Ensure OAuth-only users don't accidentally trigger installation
9. **Error Handling**: Test OAuth errors, GitHub API failures, etc.

### Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| State token collision | Installation mix-up | Use cryptographically secure UUIDs |
| OAuth flow failure | User stuck in broken flow | Proper error handling and user messaging |
| GitHub API changes | Installation flow breaks | Monitor GitHub changelog, add retries |
| Expired tokens | Failed installations | Clear error messages, retry mechanism |

### Success Metrics

1. **Security**: No cross-workspace repository access possible
2. **UX**: Installation flow completes successfully from Slack (single redirect)
3. **Reliability**: Combined OAuth + installation flow works without conflicts
4. **User Verification**: Only users with legitimate installation access can associate installations
5. **Observability**: Clear logging of installation association events

## Summary of Key Insights

1. **We already have the right GitHub App configuration** - "Request user authorization during installation" is perfect
2. **GitHub provides the combined flow** - No need for separate installation and OAuth steps
3. **Reuse existing OAuth state system** - No new state management models needed
4. **User verification is critical** - Must verify user actually has access to the installation
5. **Much simpler implementation** - Mainly fixing our callback processing, not building new systems