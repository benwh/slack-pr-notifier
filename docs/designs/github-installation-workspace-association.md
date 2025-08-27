# GitHub App Installation Workspace Association - Technical Design

## Problem Statement

The current system has a critical multi-tenancy flaw: GitHub installations are stored globally without workspace association, allowing any Slack workspace to register repositories from any GitHub installation. This breaks tenant isolation and could allow unauthorized access to private repositories.

## Current Architecture Issues

1. **Global GitHub Installations**: `GitHubInstallation` model has no workspace association
2. **Missing OAuth Flow**: We redirect away from installation callbacks instead of processing them
3. **No Validation**: Repository registration doesn't verify workspace owns the installation
4. **Concurrency Risk**: Simple state token checking could mix up concurrent installations

## Root Cause Analysis

When GitHub sends `installation.created` webhooks, there's no way to determine which Slack workspace initiated the installation. Our current implementation:

- Has "Request user authorization during installation" enabled
- Receives callbacks with `installation_id`, `setup_action=install`, `code`, and `state`
- But ignores the OAuth flow and just redirects users away
- Never associates installations with workspaces

## Proposed Solution

### GitHub App OAuth Flow (Correct Implementation)

With "Request user authorization during installation" enabled, the proper flow is:

1. **Installation Initiation (from Slack)**
   - User clicks "Install GitHub App" button in App Home
   - Generate unique state token (UUID)
   - Store state with: `slack_workspace_id`, `slack_user_id`, `expires_at`
   - Redirect to: `https://github.com/login/oauth/authorize?client_id=CLIENT_ID&state=STATE_TOKEN`

2. **GitHub Installation Process**
   - User selects repositories and installs app
   - GitHub performs OAuth authorization
   - GitHub redirects back with: `?code=XXX&installation_id=YYY&setup_action=install&state=STATE_TOKEN`

3. **Installation Callback Processing**
   - Validate state token exists and hasn't expired
   - Exchange OAuth `code` for user access token
   - Fetch installation details from GitHub API
   - Store installation with workspace association
   - Complete user OAuth flow
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

// Add installation state management (similar to existing OAuthState)
type InstallationState struct {
    ID                string    `firestore:"id"`             // Random UUID
    SlackWorkspaceID  string    `firestore:"slack_workspace_id"`
    SlackUserID       string    `firestore:"slack_user_id"`
    CreatedAt         time.Time `firestore:"created_at"`
    ExpiresAt         time.Time `firestore:"expires_at"`     // 15 minutes expiry
}
```

### API Changes

#### New Firestore Methods

```go
// Installation state management
func (fs *FirestoreService) CreateInstallationState(ctx context.Context, state *models.InstallationState) error
func (fs *FirestoreService) GetInstallationState(ctx context.Context, stateID string) (*models.InstallationState, error)
func (fs *FirestoreService) DeleteInstallationState(ctx context.Context, stateID string) error

// Workspace-specific installation queries
func (fs *FirestoreService) HasGitHubInstallations(ctx context.Context, workspaceID string) (bool, error)
func (fs *FirestoreService) GetGitHubInstallationsByWorkspace(ctx context.Context, workspaceID string) ([]*models.GitHubInstallation, error)
func (fs *FirestoreService) GetGitHubInstallationByRepoOwner(ctx context.Context, repoOwner, workspaceID string) (*models.GitHubInstallation, error)

// Update existing methods to include workspace validation
func (fs *FirestoreService) CreateGitHubInstallation(ctx context.Context, installation *models.GitHubInstallation) error
```

#### New Slack Handler Methods

```go
// Handle "Install GitHub App" button from App Home
func (sh *SlackHandler) handleInstallGitHubAppAction(ctx context.Context, userID, teamID, triggerID string, c *gin.Context)

// Process installation state and redirect to GitHub
func (sh *SlackHandler) initiateGitHubAppInstallation(ctx context.Context, userID, teamID string) (string, error)
```

#### Updated OAuth Handler

```go
// Enhanced callback processing
func (h *OAuthHandler) processGitHubAppInstallation(ctx context.Context, code, stateID, installationID string) error
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

1. **State Token Validation**: Each installation uses unique UUID state token
2. **Expiry Management**: State tokens expire after 15 minutes
3. **Workspace Isolation**: All installation queries are workspace-scoped
4. **Access Control**: Repository registration validates workspace ownership
5. **CSRF Protection**: State tokens prevent cross-site request forgery

### Migration Strategy

Since we're pre-production:
1. **Clean Slate**: Wipe existing database (no migration needed)
2. **Fresh Installations**: All new installations will follow proper flow
3. **Testing**: Verify multi-workspace isolation works correctly

### Implementation Steps

1. **Phase 1: Models and Database**
   - Add workspace fields to `GitHubInstallation`
   - Create `InstallationState` model
   - Add new Firestore methods
   - Update existing methods for workspace scoping

2. **Phase 2: OAuth Flow**
   - Add installation initiation handler in SlackHandler
   - Fix OAuth callback to process installation flows
   - Add state management for installations
   - Update GitHub service client creation

3. **Phase 3: Validation**
   - Add workspace validation to repository operations
   - Update webhook processing to check installation ownership
   - Add validation to auto-registration flow

4. **Phase 4: UI**
   - Update App Home installation warning
   - Add proper "Install GitHub App" button handler
   - Make installation checking workspace-specific

5. **Phase 5: Testing**
   - Test multi-workspace scenarios
   - Verify proper tenant isolation
   - Test concurrent installation flows

### Testing Scenarios

1. **Single Workspace**: User installs app from Slack, verifies proper association
2. **Multi-Workspace**: Two workspaces install for same GitHub org, verify isolation
3. **Concurrent Installations**: Multiple users install simultaneously, verify no cross-contamination
4. **Unauthorized Access**: Verify workspace A cannot register repos from workspace B's installation
5. **State Expiry**: Verify expired state tokens are rejected
6. **Error Handling**: Test OAuth errors, GitHub API failures, etc.

### Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| State token collision | Installation mix-up | Use cryptographically secure UUIDs |
| OAuth flow failure | User stuck in broken flow | Proper error handling and user messaging |
| GitHub API changes | Installation flow breaks | Monitor GitHub changelog, add retries |
| Expired tokens | Failed installations | Clear error messages, retry mechanism |

### Success Metrics

1. **Security**: No cross-workspace repository access possible
2. **UX**: Installation flow completes successfully from Slack
3. **Reliability**: Concurrent installations work without conflicts
4. **Observability**: Clear logging of installation association events