# Integration Tests

This directory contains end-to-end integration tests for the GitHub-Slack notifier application.

## Overview

The integration tests use:

- **Real Firestore emulator** for database operations
- **Real services** (SlackService, FirestoreService, GitHubAuthService)
- **Mock Cloud Tasks** to capture and process jobs in-memory
- **Test fixtures** for creating consistent test data

## Test Structure

### `testutil/` Package

- `app.go` - Test application setup with real and mock services
- `mocks.go` - Mock implementations for external services
- `fixtures.go` - Test data fixtures for Slack events and GitHub webhooks

### Test Files

- `slack_pr_link_test.go` - Tests for manual PR link detection and processing

## Running Tests

### Prerequisites

1. Install `gcloud` CLI (required for Firestore emulator)
2. Ensure Go 1.23+ is installed

### Run Tests

```bash
# Run all integration tests
go test ./tests/integration/... -v

# Run specific test
go test ./tests/integration -run TestSlackPRLinkDetection_EndToEnd -v

# Run with emulator already running (faster)
export FIRESTORE_EMULATOR_HOST=localhost:8080
gcloud emulators firestore start --host-port=localhost:8080 &
go test ./tests/integration/... -v
```

## Test Coverage

### Current Tests

- ✅ Manual PR link job processing creates TrackedMessage in Firestore
- ✅ Different PR numbers create separate tracked messages
- ✅ Invalid job payload error handling

### Planned Tests

- GitHub webhook job processing
- Multi-step workflows (webhook → job → notification → reaction sync)
- Cross-team message tracking
- Error scenarios and retry logic

## Test Architecture

The integration tests follow this pattern:

1. **Setup**: Start Firestore emulator, create test app with real services
2. **Action**: Process jobs directly through handlers (bypassing HTTP layer)
3. **Verify**: Query Firestore to assert expected state changes
4. **Cleanup**: Clear emulator data between tests

This approach provides confidence in the complete workflow while remaining fast and isolated.

