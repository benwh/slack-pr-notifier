// Package services provides business logic services for Slack and Firestore integration.
package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github.com/slack-go/slack"
)

// ErrReactionNotFound indicates a reaction doesn't exist (expected behavior).
var ErrReactionNotFound = errors.New("reaction not found")

type SlackService struct {
	client      *slack.Client
	emojiConfig config.EmojiConfig
}

func NewSlackService(client *slack.Client, emojiConfig config.EmojiConfig) *SlackService {
	return &SlackService{
		client:      client,
		emojiConfig: emojiConfig,
	}
}

func (s *SlackService) PostPRMessage(
	ctx context.Context, channel, repoName, prTitle, prAuthor, prDescription, prURL string,
) (string, error) {
	text := fmt.Sprintf("🐜 <%s|%s by %s>", prURL, prTitle, prAuthor)

	_, timestamp, err := s.client.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack",
			"error", err,
			"channel", channel,
			"repo_name", repoName,
			"pr_title", prTitle,
			"pr_author", prAuthor,
			"pr_url", prURL,
			"operation", "post_pr_message",
		)
		return "", fmt.Errorf("failed to post PR message to channel %s for repo %s: %w", channel, repoName, err)
	}

	return timestamp, nil
}

func (s *SlackService) AddReaction(ctx context.Context, channel, timestamp, emoji string) error {
	msgRef := slack.NewRefToMessage(channel, timestamp)
	err := s.client.AddReaction(emoji, msgRef)
	if err != nil {
		// Handle "already_reacted" as success - this is the most common case for retries
		errMsg := err.Error()
		if strings.Contains(errMsg, "already_reacted") {
			// This is not an error - the reaction already exists, which is fine
			log.Info(ctx, "Reaction already exists on Slack message",
				"channel", channel,
				"message_timestamp", timestamp,
				"emoji", emoji,
			)
			return nil
		}
		// Check for SlackErrorResponse type
		var slackErr *slack.SlackErrorResponse
		if errors.As(err, &slackErr) {
			if slackErr.Err == "already_reacted" {
				log.Info(ctx, "Reaction already exists on Slack message",
					"channel", channel,
					"message_timestamp", timestamp,
					"emoji", emoji,
				)
				return nil
			}
		}

		// Additional check: just to be thorough with any error containing the string
		// This should catch it regardless of the exact error type

		log.Error(ctx, "Failed to add reaction to Slack message",
			"error", err,
			"channel", channel,
			"message_timestamp", timestamp,
			"emoji", emoji,
			"operation", "add_reaction",
		)
		return fmt.Errorf("failed to add reaction %s to message %s in channel %s: %w", emoji, timestamp, channel, err)
	}
	return nil
}

func (s *SlackService) ValidateChannel(ctx context.Context, channel string) error {
	_, err := s.client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channel,
	})
	if err != nil {
		log.Error(ctx, "Failed to validate Slack channel",
			"error", err,
			"channel", channel,
			"operation", "validate_channel",
		)
		return fmt.Errorf("failed to validate channel %s: %w", channel, err)
	}
	return nil
}

func (s *SlackService) GetEmojiForReviewState(state string) string {
	switch state {
	case "approved":
		return s.emojiConfig.Approved
	case "changes_requested":
		return s.emojiConfig.ChangesRequested
	case "commented":
		return s.emojiConfig.Commented
	case "dismissed":
		return s.emojiConfig.Dismissed
	default:
		return ""
	}
}

func (s *SlackService) GetEmojiForPRState(state string, merged bool) string {
	if merged {
		return s.emojiConfig.Merged
	}
	return s.emojiConfig.Closed
}

// AddReactionToMultipleMessages adds the same reaction to multiple Slack messages.
func (s *SlackService) AddReactionToMultipleMessages(ctx context.Context, messages []MessageRef, emoji string) error {
	if emoji == "" {
		return nil
	}

	var lastError error
	successCount := 0

	for _, msg := range messages {
		err := s.AddReaction(ctx, msg.Channel, msg.Timestamp, emoji)
		if err != nil {
			log.Error(ctx, "Failed to add reaction to tracked message",
				"error", err,
				"channel", msg.Channel,
				"message_ts", msg.Timestamp,
				"emoji", emoji,
			)
			lastError = err
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		log.Info(ctx, "Reactions synchronized across tracked messages",
			"emoji", emoji,
			"success_count", successCount,
			"total_count", len(messages),
		)
	}

	// Return error only if all messages failed
	if successCount == 0 && lastError != nil {
		return lastError
	}

	return nil
}

// RemoveReaction removes a reaction from a Slack message.
func (s *SlackService) RemoveReaction(ctx context.Context, channel, timestamp, emoji string) error {
	err := s.client.RemoveReaction(emoji, slack.ItemRef{
		Channel:   channel,
		Timestamp: timestamp,
	})
	if err != nil {
		// Check for expected error conditions that shouldn't be logged
		switch err.Error() {
		case "no_reaction":
			// Reaction doesn't exist or user didn't originally add it - this is expected
			return ErrReactionNotFound
		case "message_not_found":
			// Message was deleted - also treat as expected behavior
			return ErrReactionNotFound
		case "channel_not_found":
			// Channel was deleted or archived - permanent error, but expected
			return ErrReactionNotFound
		}

		log.Error(ctx, "Failed to remove reaction from Slack message",
			"error", err,
			"channel", channel,
			"message_timestamp", timestamp,
			"emoji", emoji,
			"operation", "remove_reaction",
		)
		return fmt.Errorf("failed to remove reaction %s from message %s in channel %s: %w", emoji, timestamp, channel, err)
	}
	return nil
}

// RemoveReactionFromMultipleMessages removes the same reaction from multiple Slack messages.
func (s *SlackService) RemoveReactionFromMultipleMessages(
	ctx context.Context, messages []MessageRef, emoji string,
) error {
	if emoji == "" {
		return nil
	}

	var lastNonExpectedError error
	successCount := 0
	noReactionCount := 0

	for _, msg := range messages {
		err := s.RemoveReaction(ctx, msg.Channel, msg.Timestamp, emoji)
		if err != nil {
			// Check if this is our expected "reaction not found" error
			if errors.Is(err, ErrReactionNotFound) {
				// This is expected - reaction doesn't exist, which is fine
				noReactionCount++
			} else {
				// This is a genuine error worth logging
				log.Error(ctx, "Failed to remove reaction from tracked message",
					"error", err,
					"channel", msg.Channel,
					"message_ts", msg.Timestamp,
					"emoji", emoji,
				)
				lastNonExpectedError = err
			}
		} else {
			successCount++
		}
	}

	// Only log if we actually removed some reactions
	if successCount > 0 {
		log.Info(ctx, "Reactions removed from tracked messages",
			"emoji", emoji,
			"success_count", successCount,
			"total_count", len(messages),
		)
	}

	// Return error only if we had genuine failures (not just "no_reaction")
	if successCount == 0 && noReactionCount == 0 && lastNonExpectedError != nil {
		return lastNonExpectedError
	}

	return nil
}

// SyncAllReviewReactions removes all review-related reactions and adds only current ones.
func (s *SlackService) SyncAllReviewReactions(
	ctx context.Context, messages []MessageRef, currentReviewState string,
) error {
	if len(messages) == 0 {
		return nil
	}

	// Define all possible review emojis that might need to be removed
	allReviewEmojis := []string{
		s.emojiConfig.Approved,
		s.emojiConfig.ChangesRequested,
		s.emojiConfig.Commented,
		s.emojiConfig.Dismissed,
	}

	// Remove all existing review reactions
	for _, emoji := range allReviewEmojis {
		if emoji != "" {
			err := s.RemoveReactionFromMultipleMessages(ctx, messages, emoji)
			if err != nil {
				// Only log if it's a genuine error (RemoveReactionFromMultipleMessages
				// already filters out expected "no_reaction" errors)
				log.Warn(ctx, "Failed to remove some review reactions during sync",
					"error", err,
					"emoji", emoji,
				)
			}
		}
	}

	// Add the current review state reaction if applicable
	currentEmoji := s.GetEmojiForReviewState(currentReviewState)
	if currentEmoji != "" {
		err := s.AddReactionToMultipleMessages(ctx, messages, currentEmoji)
		if err != nil {
			log.Error(ctx, "Failed to add current review reaction during sync",
				"error", err,
				"emoji", currentEmoji,
				"review_state", currentReviewState,
			)
			return err
		}
	}

	log.Info(ctx, "Review reactions synchronized",
		"review_state", currentReviewState,
		"emoji", currentEmoji,
		"message_count", len(messages),
	)

	return nil
}

// MessageRef represents a reference to a Slack message for reaction operations.
type MessageRef struct {
	Channel   string
	Timestamp string
}

func (s *SlackService) ExtractChannelFromDescription(description string) string {
	re := regexp.MustCompile(`@slack-channel:\s*(#\w+)`)
	matches := re.FindStringSubmatch(description)
	if len(matches) > 1 {
		return strings.TrimPrefix(matches[1], "#")
	}
	return ""
}
