# Multi-Tenancy Architecture Design

## Overview

The GitHub-Slack notifier now supports installation across multiple Slack workspaces with proper data isolation and efficient cross-workspace operations.

## Problem & Solution

**Problem**: Original single-workspace design prevented multi-organization adoption.

**Solution**: Workspace-scoped data model with optimized cross-workspace queries using denormalized indexes.

## Key Implementation Details

### Data Model Changes

**All models include `SlackTeamID` for workspace scoping**:
- `User`, `Repo`, `TrackedMessage` models enhanced
- Repository-workspace index for O(1) cross-workspace lookups:

```go
type RepoWorkspaceMapping struct {
    ID           string    // Format: {repo_full_name}#{workspace_id}
    RepoFullName string    // e.g., "owner/repo"
    WorkspaceID  string    // Slack team ID
    CreatedAt    time.Time
}
```

### Document Scoping

**Collections**:
- `repos/{slack_team_id}#{encoded_repo_name}` - Workspace-scoped repo configs
- `repo_workspace_mappings/{repo_name}#{workspace_id}` - Cross-workspace index

### Performance Optimization

**Repository Lookup**: O(1) using denormalized index instead of O(n) workspace scans.

**Atomic Operations**: Firestore transactions maintain index consistency during repo create/delete.

### Cross-Workspace Processing

**GitHub webhook** → **lookup all workspace configs** → **process each workspace** → **continue-on-error pattern**

### Single Slack Client

Uses one Slack client across all workspaces instead of per-workspace clients. Simplifies lifecycle management and reduces overhead.

## Automatic Repository Registration Enhancement

### Problem
With `/api/repos` endpoint removed, no mechanism exists for repository registration. GitHub webhooks for unknown repositories result in "No workspace mappings found" → no notifications sent.

### Solution
**Auto-register repositories when PR webhooks arrive for unknown repos.**

**Trigger conditions**:
- GitHub webhook for unknown repository
- PR author is **verified user** (completed OAuth)  
- User has **default channel** configured

**Implementation** in `handlePROpened()`:
```go
if len(repos) == 0 {
    if user != nil && user.Verified && user.DefaultChannel != "" {
        // Auto-create repo using user's workspace + default channel
        repo := &models.Repo{
            ID:             payload.Repository.FullName,
            SlackTeamID:    user.SlackTeamID, 
            DefaultChannel: user.DefaultChannel,
            Enabled:        true,
        }
        err = h.firestoreService.CreateRepo(ctx, repo)
        // Continue processing with newly created repo
    } else {
        // Log skip reason and exit
    }
}
```

**Benefits**:
- Zero-friction user experience
- Respects user preferences (default channel)
- Security: only verified users trigger auto-registration
- Full audit trail via structured logging

**Requirements**: User must complete OAuth verification + configure default channel.