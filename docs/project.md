# GitHub-Slack Notifier

## Overview

A GitHub webhook service that monitors repositories for pull request events and sends notifications to Slack channels with real-time status updates.

## Features

### Core Functionality

- **PR Creation Notifications**: Monitor GitHub repositories for new pull requests (excluding draft PRs) and send notifications to configured Slack channels
- **Review Status Updates**: Update Slack messages with appropriate emojis when reviews are submitted on pull requests
- **PR Closure Updates**: Add closure status emojis when pull requests are merged or closed

### Notification Flow

1. GitHub webhook triggers on PR events (opened, review_submitted, closed)
2. Service filters out draft PRs for creation events
3. Slack message sent with PR link and metadata
4. Message updated with status emojis based on subsequent events

## Technical Architecture

### Tech Stack

- **Language**: Go
- **Cloud Platform**: Google Cloud Platform (GCP)
- **Runtime**: Cloud Run (serverless containers)
- **Database**: Something simple (nosql?)

### Infrastructure

- **Deployment**: Cloud Run service for auto-scaling webhook processing
- **Database**: Prepared for future state management and analytics
- **Networking**: HTTPS endpoints for GitHub webhook delivery
- **Configuration**: Environment-based configuration for GitHub tokens and Slack webhooks

### Key Components

- **Webhook Handler**: Processes GitHub webhook events
- **Slack Client**: Manages message posting and updates
- **Event Processor**: Business logic for determining notification actions
- **Database Layer**: Future state persistence and message tracking

## Configuration Requirements

- GitHub webhook URL and secret
- Slack webhook URL or bot token
- User-to-channel mapping (i.e. the user's default review channel, typically based on
  team)
  - If the mapping doesn't exist, then do nothing
  - We'll need to allow connecting a Slack user identity to a GitHub user identity.
- Emoji configuration for different PR states
- Multiple repository support

## Future Enhancements

- Ability to specify which channel your review goes to, by annotating the PR description
- Ability for the author to dismiss the Slack message, by adding an emoji reaction
- Thread-based conversations (maybe?)

