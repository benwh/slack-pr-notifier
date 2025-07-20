# TODO List

This document tracks future improvements and technical debt identified during development.

## 🔧 Technical Improvements

### Error Handling

- [ ] **Improve error handling patterns**
  - Replace `log.Fatal` calls with graceful shutdown
  - Add proper error wrapping with context
  - Implement retry logic for transient failures
  - Add structured logging with levels

### Database & Storage

- [ ] **Firestore optimizations**
  - Implement proper indexes for complex queries
  - Add database connection pooling/management
  - Consider batch operations for bulk updates
  - Add database migration system for schema changes

### Security Enhancements

- [ ] **Authentication & Authorization**
  - Implement proper GitHub App authentication (vs webhook secrets)

## 🚀 Feature Enhancements

### Core Features

- [ ] **Enhanced PR notifications**
  - Customizable message templates
  - Notification for PR comments (not just reviews)

### User Experience

- [ ] **Slack integration improvements**
  - Add interactive buttons for quick actions
  - Implement slash command autocomplete
  - Add help command with usage examples

### Multi-tenancy

- [ ] **Organization support**
  - Support multiple GitHub organizations
  - Per-organization configuration
  - Workspace-level settings

## 📦 Deployment & Operations

### Infrastructure

- [ ] **Production readiness**
  - Implement graceful shutdown handling

## 🐛 Known Issues & Technical Debt

### Current Limitations

- [ ] **Message tracking limitations**
  - No cleanup of old message records
