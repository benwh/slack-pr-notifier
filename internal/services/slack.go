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

	_, timestamp, err := s.client.PostMessage(channel, slack.MsgOptionText(text, false))
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

func (s *SlackService) ExtractChannelFromDescription(description string) string {
	re := regexp.MustCompile(`@slack-channel:\s*(#\w+)`)
	matches := re.FindStringSubmatch(description)
	if len(matches) > 1 {
		return strings.TrimPrefix(matches[1], "#")
	}
	return ""
}
