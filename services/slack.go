// Package services provides business logic services for Slack and Firestore integration.
package services

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/slack-go/slack"
)

type SlackService struct {
	client *slack.Client
}

func NewSlackService(client *slack.Client) *SlackService {
	return &SlackService{client: client}
}

func (s *SlackService) PostPRMessage(
	channel, repoName, prTitle, prAuthor, prDescription, prURL string,
) (string, error) {
	description := prDescription
	const maxDescriptionLength = 100
	if len(description) > maxDescriptionLength {
		description = description[:maxDescriptionLength] + "..."
	}

	text := fmt.Sprintf("🔗 *New PR in %s*\n*Title:* %s\n*Author:* %s\n*Description:* %s\n<%s|View Pull Request>",
		repoName, prTitle, prAuthor, description, prURL)

	_, timestamp, err := s.client.PostMessage(channel, slack.MsgOptionText(text, false))
	if err != nil {
		return "", err
	}

	return timestamp, nil
}

func (s *SlackService) AddReaction(channel, timestamp, emoji string) error {
	msgRef := slack.NewRefToMessage(channel, timestamp)
	return s.client.AddReaction(emoji, msgRef)
}

func (s *SlackService) ValidateChannel(channel string) error {
	_, err := s.client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channel,
	})
	return err
}

func (s *SlackService) GetEmojiForReviewState(state string) string {
	switch state {
	case "approved":
		return getEnvOrDefault("EMOJI_APPROVED", "white_check_mark")
	case "changes_requested":
		return getEnvOrDefault("EMOJI_CHANGES_REQUESTED", "arrows_counterclockwise")
	case "commented":
		return getEnvOrDefault("EMOJI_COMMENTED", "speech_balloon")
	default:
		return ""
	}
}

func (s *SlackService) GetEmojiForPRState(state string, merged bool) string {
	if merged {
		return getEnvOrDefault("EMOJI_MERGED", "tada")
	}
	return getEnvOrDefault("EMOJI_CLOSED", "x")
}

func (s *SlackService) ExtractChannelFromDescription(description string) string {
	re := regexp.MustCompile(`@slack-channel:\s*(#\w+)`)
	matches := re.FindStringSubmatch(description)
	if len(matches) > 1 {
		return strings.TrimPrefix(matches[1], "#")
	}
	return ""
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
