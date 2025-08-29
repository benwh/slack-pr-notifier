// Package services provides business logic services for Slack and Firestore integration.
package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/ui"
	"github-slack-notifier/internal/utils"

	"github.com/slack-go/slack"
)

// ErrReactionNotFound indicates a reaction doesn't exist (expected behavior).
var ErrReactionNotFound = errors.New("reaction not found")

// ErrChannelNotFound indicates a channel could not be found by name.
var ErrChannelNotFound = errors.New("channel not found")

// ErrPrivateChannelNotSupported indicates that private channels are not supported.
var ErrPrivateChannelNotSupported = errors.New("private_channel_not_supported")

// ErrCannotJoinChannel indicates the bot cannot join the specified channel.
var ErrCannotJoinChannel = errors.New("cannot_join_channel")

var (
	directiveRegex          = regexp.MustCompile(`(?i)!reviews?:\s*(.+)`)
	skipDirectiveRegex      = regexp.MustCompile(`(?i)!review-skip`)
	channelValidationRegex  = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	usernameValidationRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

const minMatchesRequired = 2

type SlackService struct {
	workspaceService *SlackWorkspaceService // Service to get workspace-specific tokens
	emojiConfig      config.EmojiConfig
	uiBuilder        *ui.HomeViewBuilder
	config           *config.Config
	httpClient       *http.Client
}

func NewSlackService(
	workspaceService *SlackWorkspaceService,
	emojiConfig config.EmojiConfig,
	config *config.Config,
	httpClient *http.Client,
) *SlackService {
	return &SlackService{
		workspaceService: workspaceService,
		emojiConfig:      emojiConfig,
		uiBuilder:        ui.NewHomeViewBuilder(),
		config:           config,
		httpClient:       httpClient,
	}
}

// getSlackClient returns the appropriate Slack client for the given team ID.
func (s *SlackService) getSlackClient(ctx context.Context, teamID string) (*slack.Client, error) {
	// Get workspace-specific token
	token, err := s.workspaceService.GetWorkspaceToken(ctx, teamID)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotInstalled, teamID)
		}
		return nil, fmt.Errorf("failed to get workspace token: %w", err)
	}
	return slack.New(token, slack.OptionHTTPClient(s.httpClient)), nil
}

func (s *SlackService) PostPRMessage(
	ctx context.Context, teamID, channel, repoName, prTitle, prAuthor, prDescription, prURL string, prSize int,
	authorSlackUserID, userToCC, customEmoji string,
) (string, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return "", err
	}

	// Use custom emoji if provided, otherwise fall back to size-based emoji
	emoji := customEmoji
	if emoji == "" {
		emoji = utils.GetPRSizeEmoji(prSize)
	} else if !strings.HasPrefix(emoji, ":") {
		// Format custom emoji for Slack (add colons if not present)
		emoji = ":" + emoji + ":"
	}

	// Format author with Slack user mention if available
	authorDisplay := prAuthor
	if authorSlackUserID != "" {
		authorDisplay = fmt.Sprintf("<@%s>", authorSlackUserID)
	}

	text := fmt.Sprintf("%s <%s|%s> by %s", emoji, prURL, prTitle, authorDisplay)

	// Add user CC if specified
	if userToCC != "" {
		text += fmt.Sprintf(" (cc: @%s)", userToCC)
	}

	_, timestamp, err := client.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	if err != nil {
		log.Error(ctx, "Failed to post PR message to Slack",
			"error", err,
			"channel", channel,
			"team_id", teamID,
			"repo_name", repoName,
			"pr_title", prTitle,
			"pr_author", prAuthor,
			"pr_url", prURL,
			"operation", "post_pr_message",
		)
		return "", fmt.Errorf("failed to post PR message to channel %s for team %s repo %s: %w", channel, teamID, repoName, err)
	}

	return timestamp, nil
}

// SendEphemeralMessage sends an ephemeral message visible only to a specific user.
func (s *SlackService) SendEphemeralMessage(ctx context.Context, teamID, channel, userID, text string) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	_, err = client.PostEphemeral(channel, userID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	if err != nil {
		log.Error(ctx, "Failed to send ephemeral message to Slack",
			"error", err,
			"channel", channel,
			"team_id", teamID,
			"user_id", userID,
			"operation", "send_ephemeral_message",
		)
		return fmt.Errorf("failed to send ephemeral message to user %s in channel %s for team %s: %w", userID, channel, teamID, err)
	}

	return nil
}

func (s *SlackService) AddReaction(ctx context.Context, teamID, channel, timestamp, emoji string) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	msgRef := slack.NewRefToMessage(channel, timestamp)
	err = client.AddReaction(emoji, msgRef)
	if err != nil {
		// Handle "already_reacted" as success - this is the most common case for retries
		errMsg := err.Error()
		if strings.Contains(errMsg, "already_reacted") {
			// This is not an error - the reaction already exists, which is fine
			log.Info(ctx, "Reaction already exists on Slack message",
				"channel", channel,
				"team_id", teamID,
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
					"team_id", teamID,
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
			"team_id", teamID,
			"message_timestamp", timestamp,
			"emoji", emoji,
			"operation", "add_reaction",
		)
		return fmt.Errorf("failed to add reaction %s to message %s in channel %s for team %s: %w", emoji, timestamp, channel, teamID, err)
	}
	return nil
}

func (s *SlackService) ValidateChannel(ctx context.Context, teamID, channel string) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	// Resolve channel name to channel ID if needed
	channelID, err := s.resolveChannelID(ctx, teamID, client, channel)
	if err != nil {
		log.Error(ctx, "Failed to resolve Slack channel",
			"error", err,
			"channel", channel,
			"team_id", teamID,
			"operation", "resolve_channel",
		)
		return fmt.Errorf("failed to resolve channel %s for team %s: %w", channel, teamID, err)
	}

	// Check if channel exists and get info including membership status
	channelInfo, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channelID,
	})
	if err != nil {
		log.Error(ctx, "Failed to get channel info",
			"error", err,
			"channel", channel,
			"team_id", teamID,
			"channel_id", channelID,
			"operation", "validate_channel",
		)
		return fmt.Errorf("failed to get channel info for %s in team %s: %w", channel, teamID, err)
	}

	// Explicitly reject private channels for security and privacy reasons
	if channelInfo.IsPrivate {
		log.Warn(ctx, "Private channel selected, rejecting",
			"channel", channel,
			"channel_id", channelID,
		)
		return ErrPrivateChannelNotSupported
	}

	// If bot is not a member of the public channel, join it
	if !channelInfo.IsMember {
		log.Info(ctx, "Bot not in channel, attempting to join",
			"channel", channel,
			"channel_id", channelID,
			"channel_name", channelInfo.Name,
		)

		// Join the public channel
		_, _, _, err := client.JoinConversation(channelID)
		if err != nil {
			log.Error(ctx, "Failed to join channel",
				"error", err,
				"channel", channel,
				"channel_id", channelID,
				"channel_name", channelInfo.Name,
				"operation", "join_channel",
			)
			// If we can't join, it might be because:
			// - The channel is archived
			// - We don't have permission (shouldn't happen with channels:join scope)
			// - Some other restriction
			return ErrCannotJoinChannel
		}

		log.Info(ctx, "Successfully joined channel",
			"channel", channel,
			"channel_id", channelID,
			"channel_name", channelInfo.Name,
		)
	}

	return nil
}

// AddReactionToMultipleMessages adds the same reaction to multiple Slack messages.
func (s *SlackService) AddReactionToMultipleMessages(ctx context.Context, teamID string, messages []MessageRef, emoji string) error {
	if emoji == "" {
		return nil
	}

	var lastError error
	successCount := 0

	for _, msg := range messages {
		err := s.AddReaction(ctx, teamID, msg.Channel, msg.Timestamp, emoji)
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
func (s *SlackService) RemoveReaction(ctx context.Context, teamID, channel, timestamp, emoji string) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	err = client.RemoveReaction(emoji, slack.ItemRef{
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
	ctx context.Context, teamID string, messages []MessageRef, emoji string,
) error {
	if emoji == "" {
		return nil
	}

	var lastNonExpectedError error
	successCount := 0
	noReactionCount := 0

	for _, msg := range messages {
		err := s.RemoveReaction(ctx, teamID, msg.Channel, msg.Timestamp, emoji)
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
	ctx context.Context, teamID string, messages []MessageRef, currentReviewState string,
) error {
	if len(messages) == 0 {
		return nil
	}

	// Define all possible review emojis that might need to be removed
	allReviewEmojis := []string{
		s.emojiConfig.Approved,
		s.emojiConfig.ChangesRequested,
		s.emojiConfig.Commented,
	}

	// Remove all existing review reactions
	for _, emoji := range allReviewEmojis {
		if emoji != "" {
			err := s.RemoveReactionFromMultipleMessages(ctx, teamID, messages, emoji)
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
	currentEmoji := utils.GetEmojiForReviewState(currentReviewState, s.emojiConfig)
	if currentEmoji != "" {
		err := s.AddReactionToMultipleMessages(ctx, teamID, messages, currentEmoji)
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

// DeleteMessage deletes a Slack message.
func (s *SlackService) DeleteMessage(ctx context.Context, teamID, channel, timestamp string) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	_, _, err = client.DeleteMessage(channel, timestamp)
	if err != nil {
		log.Error(ctx, "Failed to delete Slack message",
			"error", err,
			"channel", channel,
			"team_id", teamID,
			"message_timestamp", timestamp,
			"operation", "delete_message",
		)
		return fmt.Errorf("failed to delete message %s in channel %s for team %s: %w", timestamp, channel, teamID, err)
	}

	log.Info(ctx, "Successfully deleted Slack message",
		"channel", channel,
		"team_id", teamID,
		"message_timestamp", timestamp,
	)
	return nil
}

// DeleteMultipleMessages deletes multiple Slack messages.
func (s *SlackService) DeleteMultipleMessages(ctx context.Context, teamID string, messages []MessageRef) error {
	if len(messages) == 0 {
		return nil
	}

	var lastError error
	successCount := 0

	for _, msg := range messages {
		err := s.DeleteMessage(ctx, teamID, msg.Channel, msg.Timestamp)
		if err != nil {
			log.Error(ctx, "Failed to delete tracked message",
				"error", err,
				"channel", msg.Channel,
				"message_ts", msg.Timestamp,
			)
			lastError = err
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		log.Info(ctx, "Messages deleted successfully",
			"success_count", successCount,
			"total_count", len(messages),
		)
	}

	// Return error only if all messages failed to delete
	if successCount == 0 && lastError != nil {
		return lastError
	}

	return nil
}

// MessageRef represents a reference to a Slack message for reaction operations.
type MessageRef struct {
	Channel   string
	Timestamp string
}

// ResolveChannelID converts a channel name to channel ID if needed.
// If the input is already a channel ID (starts with 'C'), returns it as-is.
func (s *SlackService) ResolveChannelID(ctx context.Context, teamID, channel string) (string, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return "", err
	}
	return s.resolveChannelID(ctx, teamID, client, channel)
}

// resolveChannelID converts a channel name to channel ID if needed.
// If the input is already a channel ID (starts with 'C'), returns it as-is.
func (s *SlackService) resolveChannelID(ctx context.Context, _ string, client *slack.Client, channel string) (string, error) {
	// If already a channel ID (starts with 'C'), return as-is
	if strings.HasPrefix(channel, "C") {
		return channel, nil
	}

	// Look up channel by name using GetConversationsContext
	const maxConversationsPerPage = 1000 // Slack's max limit per page
	params := &slack.GetConversationsParameters{
		ExcludeArchived: true,
		Limit:           maxConversationsPerPage,
		Types:           []string{"public_channel", "private_channel"},
	}

	for {
		channels, nextCursor, err := client.GetConversationsContext(ctx, params)
		if err != nil {
			return "", fmt.Errorf("failed to list conversations: %w", err)
		}

		for _, ch := range channels {
			if ch.Name == channel {
				return ch.ID, nil
			}
		}

		if nextCursor == "" {
			break
		}
		params.Cursor = nextCursor
	}

	return "", fmt.Errorf("%w: %s", ErrChannelNotFound, channel)
}

// PRDirectives represents the parsed directives from a PR description.
type PRDirectives struct {
	Skip        bool
	Channel     string
	UserToCC    string
	CustomEmoji string
}

// !review[s]: [skip|no] [#channel_name] [@user_to_cc].
func (s *SlackService) ParsePRDirectives(description string) *PRDirectives {
	directives := &PRDirectives{}

	// Replace !review-skip with !review: skip to normalize all skip directives
	normalizedDescription := skipDirectiveRegex.ReplaceAllString(description, "!review: skip")

	// Find all matches - last directive wins
	allMatches := directiveRegex.FindAllStringSubmatch(normalizedDescription, -1)
	if len(allMatches) == 0 {
		return directives
	}

	// Process all directive matches, last one wins for each component
	for _, matches := range allMatches {
		if len(matches) < minMatchesRequired {
			continue
		}
		s.processDirectiveMatch(matches[1], directives)
	}

	return directives
}

// processDirectiveMatch processes a single directive match and updates the directives.
func (s *SlackService) processDirectiveMatch(content string, directives *PRDirectives) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}

	// Split content by whitespace and parse each component
	parts := strings.Fields(content)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s.processDirectivePart(part, directives)
	}
}

// processDirectivePart processes a single part of a directive.
func (s *SlackService) processDirectivePart(part string, directives *PRDirectives) {
	// Check for skip directive
	if strings.EqualFold(part, "skip") || strings.EqualFold(part, "no") {
		directives.Skip = true
		return
	}

	// Check for emoji directive (format :emoji_name:)
	if strings.HasPrefix(part, ":") && strings.HasSuffix(part, ":") && len(part) > 2 {
		emojiName := strings.Trim(part, ":")
		if emojiName != "" {
			directives.CustomEmoji = emojiName
		}
		return
	}

	// Check for channel directive (starts with #)
	if strings.HasPrefix(part, "#") {
		s.processChannelDirective(part, directives)
		return
	}

	// Check for user CC directive (starts with @)
	if strings.HasPrefix(part, "@") {
		s.processUserDirective(part, directives)
	}
}

// processChannelDirective processes a channel directive part.
func (s *SlackService) processChannelDirective(part string, directives *PRDirectives) {
	// Validate channel name format: alphanumeric, hyphens, underscores
	channelName := strings.TrimPrefix(part, "#")
	if channelValidationRegex.MatchString(channelName) {
		directives.Channel = channelName
	}
}

// processUserDirective processes a user CC directive part.
func (s *SlackService) processUserDirective(part string, directives *PRDirectives) {
	// Validate username format: alphanumeric, dots, hyphens, underscores
	username := strings.TrimPrefix(part, "@")
	if usernameValidationRegex.MatchString(username) {
		directives.UserToCC = username
	}
}

// ExtractChannelAndDirectives parses PR directives and returns the channel and directive information.
func (s *SlackService) ExtractChannelAndDirectives(description string) (string, *PRDirectives) {
	directives := s.ParsePRDirectives(description)
	return directives.Channel, directives
}

// GetUserInfo retrieves Slack user information including display name.
func (s *SlackService) GetUserInfo(ctx context.Context, teamID, userID string) (*slack.User, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return nil, err
	}

	user, err := client.GetUserInfoContext(ctx, userID)
	if err != nil {
		log.Error(ctx, "Failed to get Slack user info",
			"error", err,
			"team_id", teamID,
			"user_id", userID,
			"operation", "get_user_info",
		)
		return nil, fmt.Errorf("failed to get user info for %s: %w", userID, err)
	}

	return user, nil
}

// PublishHomeView publishes the home tab view for a user.
func (s *SlackService) PublishHomeView(ctx context.Context, teamID, userID string, view slack.HomeTabViewRequest) error {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return err
	}

	_, err = client.PublishViewContext(ctx, userID, view, "")
	if err != nil {
		log.Error(ctx, "Failed to publish home view",
			"error", err,
			"user_id", userID,
			"team_id", teamID,
			"operation", "publish_home_view",
		)
		return fmt.Errorf("failed to publish home view for user %s in team %s: %w", userID, teamID, err)
	}
	return nil
}

// OpenView opens a modal or app home view.
func (s *SlackService) OpenView(ctx context.Context, teamID, triggerID string, view slack.ModalViewRequest) (*slack.ViewResponse, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return nil, err
	}

	response, err := client.OpenViewContext(ctx, triggerID, view)
	if err != nil {
		// Check if it's a Slack API error with more details
		var slackErr slack.SlackErrorResponse
		if errors.As(err, &slackErr) {
			log.Error(ctx, "Failed to open view - Slack API error",
				"error", err,
				"trigger_id", triggerID,
				"slack_error", slackErr.Err,
				"response_metadata", slackErr.ResponseMetadata,
				"operation", "open_view",
			)
		} else {
			log.Error(ctx, "Failed to open view",
				"error", err,
				"trigger_id", triggerID,
				"operation", "open_view",
			)
		}
		return nil, fmt.Errorf("failed to open view with trigger %s: %w", triggerID, err)
	}
	return response, nil
}

// BuildHomeView constructs the home tab view based on user data.
func (s *SlackService) BuildHomeView(
	user *models.User, hasGitHubInstallations bool, installations []*models.GitHubInstallation,
) slack.HomeTabViewRequest {
	return s.uiBuilder.BuildHomeView(user, hasGitHubInstallations, installations)
}

// BuildOAuthModal builds the OAuth connection modal.
func (s *SlackService) BuildOAuthModal(oauthURL string) slack.ModalViewRequest {
	return s.uiBuilder.BuildOAuthModal(oauthURL)
}

// BuildGitHubInstallationModal builds the GitHub App installation modal.
func (s *SlackService) BuildGitHubInstallationModal(oauthURL string) slack.ModalViewRequest {
	return s.uiBuilder.BuildGitHubInstallationModal(oauthURL)
}

// BuildGitHubInstallationsModal builds the GitHub installations management modal.
func (s *SlackService) BuildGitHubInstallationsModal(
	installations []*models.GitHubInstallation, baseURL, appSlug string,
) slack.ModalViewRequest {
	return s.uiBuilder.BuildGitHubInstallationsModal(installations, baseURL, appSlug)
}

// BuildChannelSelectorModal builds the channel selector modal.
func (s *SlackService) BuildChannelSelectorModal() slack.ModalViewRequest {
	return s.uiBuilder.BuildChannelSelectorModal()
}

// BuildChannelTrackingModal builds the channel tracking configuration modal.
func (s *SlackService) BuildChannelTrackingModal(configs []*models.ChannelConfig) slack.ModalViewRequest {
	return s.uiBuilder.BuildChannelTrackingModal(configs)
}

// BuildChannelTrackingConfigModal builds the modal for configuring a specific channel's tracking settings.
func (s *SlackService) BuildChannelTrackingConfigModal(channelID, channelName string, currentlyEnabled bool) slack.ModalViewRequest {
	return s.uiBuilder.BuildChannelTrackingConfigModal(channelID, channelName, currentlyEnabled)
}

// UpdateView updates an existing modal view.
func (s *SlackService) UpdateView(ctx context.Context, teamID, viewID string, view slack.ModalViewRequest) (*slack.ViewResponse, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return nil, err
	}

	response, err := client.UpdateViewContext(ctx, view, "", "", viewID)
	if err != nil {
		log.Error(ctx, "Failed to update view",
			"error", err,
			"view_id", viewID,
			"operation", "update_view",
		)
		return nil, fmt.Errorf("failed to update view %s: %w", viewID, err)
	}
	return response, nil
}

// GetChannelName retrieves the channel name for a given channel ID.
func (s *SlackService) GetChannelName(ctx context.Context, teamID, channelID string) (string, error) {
	client, err := s.getSlackClient(ctx, teamID)
	if err != nil {
		return "", err
	}

	channel, err := client.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channelID,
	})
	if err != nil {
		log.Error(ctx, "Failed to get channel info for name",
			"error", err,
			"channel_id", channelID,
			"team_id", teamID,
			"operation", "get_channel_name",
		)
		return "", fmt.Errorf("failed to get channel info for %s in team %s: %w", channelID, teamID, err)
	}

	return channel.Name, nil
}
