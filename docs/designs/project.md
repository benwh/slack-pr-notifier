# GitHub-Slack Notifier

## Overview

A high-performance GitHub webhook service that monitors repositories for pull request events and sends notifications to Slack channels with real-time status updates. Built with an async-first architecture using Google Cloud Tasks for reliability and scalability.

## Features

### Core Functionality

- **PR Creation Notifications**: Monitor GitHub repositories for new pull requests (excluding draft PRs) and send notifications to configured Slack channels
- **Review Status Updates**: Update Slack messages with appropriate emojis when reviews are submitted on pull requests
- **PR Closure Updates**: Add closure status emojis when pull requests are merged or closed
- **User Configuration**: Slack slash commands for users to configure their notification preferences

### Async Processing Architecture

1. **Fast Ingress (< 100ms)**: GitHub webhooks are quickly validated and queued to Cloud Tasks
2. **Reliable Processing**: Cloud Tasks handles retries, backoff, and ensures no events are lost
3. **Decoupled Architecture**: Webhook ingress separated from business logic processing

### Notification Flow

1. GitHub webhook triggers on PR events (opened, review_submitted, closed)
2. Webhook handler validates signature and queues job to Cloud Tasks
3. Worker processes job asynchronously, updating Firestore and sending Slack notifications
4. Slack messages updated with status emojis based on PR lifecycle events

## Technical Architecture

### Tech Stack

- **Language**: Go
- **Cloud Platform**: Google Cloud Platform (GCP)
- **Runtime**: Cloud Run (serverless containers)
- **Queue**: Google Cloud Tasks (async job processing)
- **Database**: Cloud Firestore (NoSQL document store)
- **Logging**: Structured logging with trace IDs

### Infrastructure

- **Deployment**: Cloud Run service with auto-scaling
- **Async Processing**: Cloud Tasks for reliable webhook processing with automatic retries
- **Database**: Firestore for user preferences, message tracking, and repository configuration
- **Security**: HMAC signature validation for both GitHub and Slack webhooks

### Key Components

- **Webhook Ingress Handler**: Fast validation and queuing of GitHub events
- **Webhook Worker**: Async processor for business logic and Slack integration
- **Slack Client**: Manages message posting with reaction updates
- **Firestore Service**: Handles all database operations
- **Cloud Tasks Service**: Queue management for async processing

## Configuration Requirements

### GitHub Configuration
- Webhook URL pointing to `/webhooks/github`
- Webhook secret for HMAC validation
- Events: pull_request (opened, closed, reopened) and pull_request_review (submitted)

### Slack App Configuration
- Bot token with required OAuth scopes
- Signing secret for request validation
- Slash commands configured to `/webhooks/slack`

### User Configuration
- Users link their GitHub username to Slack identity
- Set default notification channel per user
- Repository-specific default channels supported

## Current Limitations

- No custom channel routing via PR description annotations
- No user dismissal of notifications via emoji reactions
- Single emoji set (not configurable per deployment)

## Deployment

The service runs on Google Cloud Run with:
- Automatic HTTPS endpoints
- Built-in load balancing and auto-scaling
- Integration with Cloud Tasks for async processing
- Firestore for persistent storage
- Regional deployment support

See [technical specification](technical-spec.md) for detailed implementation details.