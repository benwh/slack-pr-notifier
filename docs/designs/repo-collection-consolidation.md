# Repository Collection Consolidation Design

## Overview

This design document outlines the consolidation of the current dual-collection architecture (`repos` and `repo_workspace_mappings`) into a single, simplified `repos` collection.

## Current Architecture Problems

The current system uses two collections:

1. **`repos` collection**: Stores workspace-specific repository configurations
   - Document ID: `{workspace_id}#{encoded_repo_name}`
   - Contains: `id` (repo name), `slack_team_id`, `enabled`, `created_at`

2. **`repo_workspace_mappings` collection**: Acts as a reverse index for cross-workspace queries
   - Document ID: `{encoded_repo_name}#{workspace_id}`
   - Contains: `repo_full_name`, `workspace_id`, `created_at`

This dual-collection approach has several issues:

- **Complexity**: Requires transactions to keep both collections in sync
- **Redundancy**: Same relationship stored twice with different keys
- **Maintenance**: More code to maintain and test
- **Performance**: Two writes for every repo registration/deletion

## Proposed Architecture

Consolidate into a single `repos` collection with the following structure:

```go
type Repo struct {
    ID             string    `firestore:"id"`              // {workspace_id}#{repo_full_name} (for backward compatibility)
    RepoFullName   string    `firestore:"repo_full_name"`  // e.g., "owner/repo" (denormalized for queries)
    WorkspaceID    string    `firestore:"workspace_id"`    // Slack team ID (denormalized for queries)
    Enabled        bool      `firestore:"enabled"`
    CreatedAt      time.Time `firestore:"created_at"`
}
```

### Key Changes

1. **Add fields to `Repo` model**:
   - `RepoFullName`: Denormalized repository name for querying
   - `WorkspaceID`: Denormalized Slack team ID for filtering

2. **Keep document ID format**: `{workspace_id}#{encoded_repo_name}`
   - Maintains backward compatibility
   - Enables direct lookups for `GetRepo(workspace, repo)`

3. **Create composite index**: `(repo_full_name, workspace_id)`
   - Enables efficient `GetReposForAllWorkspaces(repo)` queries

## Implementation Plan

### 1. Model Updates

Update `internal/models/models.go`:

```go
type Repo struct {
    ID             string    `firestore:"id"`
    RepoFullName   string    `firestore:"repo_full_name"`  // NEW
    WorkspaceID    string    `firestore:"workspace_id"`    // NEW (rename from SlackTeamID)
    Enabled        bool      `firestore:"enabled"`
    CreatedAt      time.Time `firestore:"created_at"`
}

// Remove RepoWorkspaceMapping entirely
```

### 2. Service Layer Changes

Update `internal/services/firestore.go`:

#### CreateRepo

```go
func (fs *FirestoreService) CreateRepo(ctx context.Context, repo *models.Repo) error {
    repo.CreatedAt = time.Now()
    repo.RepoFullName = repo.ID  // Ensure denormalized field is set
    repo.WorkspaceID = repo.SlackTeamID  // Ensure denormalized field is set
    
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
```

#### GetReposForAllWorkspaces

```go
func (fs *FirestoreService) GetReposForAllWorkspaces(ctx context.Context, repoFullName string) ([]*models.Repo, error) {
    // Direct query on repos collection instead of mapping lookup
    iter := fs.client.Collection("repos").
        Where("repo_full_name", "==", repoFullName).
        Where("enabled", "==", true).  // Optional: only get enabled repos
        Documents(ctx)
    defer iter.Stop()
    
    var repos []*models.Repo
    for {
        doc, err := iter.Next()
        if err != nil {
            if err == iterator.Done {
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
```

#### DeleteRepo

```go
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
```

#### Remove These Functions

- `getWorkspaceMappingsForRepo()` - No longer needed
- `encodeMappingDocID()` - No longer needed

### 3. Firestore Index Configuration

Create a new composite index in `firestore.indexes.json`:

```json
{
  "indexes": [
    {
      "collectionGroup": "repos",
      "queryScope": "COLLECTION",
      "fields": [
        {
          "fieldPath": "repo_full_name",
          "order": "ASCENDING"
        },
        {
          "fieldPath": "workspace_id",
          "order": "ASCENDING"
        }
      ]
    }
  ]
}
```

### 4. Test Updates

Update test helpers to use the new structure:

- Remove any references to `repo_workspace_mappings`
- Update repo creation to include new fields
- Simplify test setup

### 5. Data Cleanup

Since the app isn't in production:

1. Delete all existing data from both collections
2. Drop the `repo_workspace_mappings` collection entirely
3. Deploy new code
4. Re-register repositories as needed

## Benefits

1. **Simplicity**: Single collection, no transactions needed
2. **Performance**: Single write per operation instead of two
3. **Maintainability**: Less code, fewer edge cases
4. **Query efficiency**: Direct queries without intermediate mapping lookups
5. **Cost**: Fewer Firestore operations = lower costs

## Query Performance Analysis

### Before (Two Collections)

- `GetRepo(workspace, repo)`: 1 read (direct lookup)
- `GetReposForAllWorkspaces(repo)`: N+1 reads (1 query + N lookups)
- `CreateRepo`: 2 writes (transaction)
- `DeleteRepo`: 2 deletes (transaction)

### After (Single Collection)

- `GetRepo(workspace, repo)`: 1 read (direct lookup) - **Same**
- `GetReposForAllWorkspaces(repo)`: 1 query returning N docs - **Better**
- `CreateRepo`: 1 write - **Better**
- `DeleteRepo`: 1 delete - **Better**

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Index not created properly | Queries fail | Test queries thoroughly before deploying |
| Field denormalization bugs | Data inconsistency | Ensure fields are set in CreateRepo |
| Missed code references | Runtime errors | Comprehensive grep for all references |

## Rollback Plan

Not needed since the app isn't in production. If issues arise:

1. Revert code changes
2. Clear Firestore data
3. Redeploy previous version

## Success Criteria

1. All existing functionality works unchanged
2. No references to `repo_workspace_mappings` remain in code
3. All tests pass
4. Firestore shows single `repos` collection with new structure
5. Cross-workspace queries perform efficiently

## Future Considerations

This simplified structure makes future enhancements easier:

- Adding per-workspace repo settings (just add fields)
- Implementing repo-level permissions (add permission fields)
- Supporting multiple channels per repo/workspace (change string to array)

