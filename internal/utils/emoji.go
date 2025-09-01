package utils

import (
	"strconv"
	"strings"

	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/models"
)

// GetPRSizeEmojiWithConfig returns a PR size emoji using user's custom config or defaults.
// If user has custom configuration enabled, uses their thresholds and emojis.
// Otherwise falls back to the default animal emoji logic.
func GetPRSizeEmojiWithConfig(linesChanged int, user *models.User) string {
	if user != nil && user.PRSizeConfig != nil {
		if emoji := user.PRSizeConfig.GetCustomPRSizeEmoji(linesChanged); emoji != "" {
			return emoji
		}
	}
	return GetPRSizeEmoji(linesChanged)
}

// GetDefaultPRSizeThresholds returns the default PR size thresholds used by GetPRSizeEmoji.
// This provides a single source of truth for the default configuration.
// Uses non-face emojis progressing from smallest to largest animals.
func GetDefaultPRSizeThresholds() []models.PRSizeThreshold {
	return []models.PRSizeThreshold{
		{MaxLines: 2, Emoji: ":ant:"},         // ant (< 5 originally)
		{MaxLines: 10, Emoji: ":mouse2:"},     // mouse
		{MaxLines: 25, Emoji: ":rabbit2:"},    // rabbit
		{MaxLines: 50, Emoji: ":raccoon:"},    // raccoon
		{MaxLines: 100, Emoji: ":dog2:"},      // dog
		{MaxLines: 250, Emoji: ":llama:"},     // llama
		{MaxLines: 500, Emoji: ":pig2:"},      // pig
		{MaxLines: 1000, Emoji: ":gorilla:"},  // gorilla
		{MaxLines: 1500, Emoji: ":elephant:"}, // elephant
		{MaxLines: 2000, Emoji: ":t-rex:"},    // t-rex
		{MaxLines: 9999, Emoji: ":whale2:"},   // whale (catches all larger PRs)
	}
}

// FormatPRSizeThresholds formats PR size thresholds as text for display in modals.
// Each threshold is formatted as "emoji max_lines" on a separate line.
func FormatPRSizeThresholds(thresholds []models.PRSizeThreshold) string {
	var lines []string
	for _, threshold := range thresholds {
		lines = append(lines, threshold.Emoji+" "+strconv.Itoa(threshold.MaxLines))
	}
	return strings.Join(lines, "\n")
}

// GetPRSizeEmoji returns an animal emoji based on the number of lines changed in a PR.
// The emoji serves as a visual indicator of PR size, with larger animals representing
// bigger changes. Uses the default thresholds from GetDefaultPRSizeThresholds().
func GetPRSizeEmoji(linesChanged int) string {
	thresholds := GetDefaultPRSizeThresholds()

	// Find the first threshold where linesChanged <= MaxLines
	for _, threshold := range thresholds {
		if linesChanged <= threshold.MaxLines {
			return threshold.Emoji
		}
	}

	// If no threshold matched, use the last (largest) emoji
	if len(thresholds) > 0 {
		return thresholds[len(thresholds)-1].Emoji
	}

	return "🐋" // fallback
}

// GetEmojiForReviewState returns the appropriate emoji for a GitHub PR review state.
// It maps review states to configured emojis: approved uses emojiConfig.Approved,
// changes_requested uses emojiConfig.ChangesRequested, commented uses emojiConfig.Commented.
// Returns an empty string for unknown or invalid review states.
func GetEmojiForReviewState(state models.ReviewState, emojiConfig config.EmojiConfig) string {
	switch state {
	case models.ReviewStateApproved:
		return emojiConfig.Approved
	case models.ReviewStateChangesRequested:
		return emojiConfig.ChangesRequested
	case models.ReviewStateCommented:
		return emojiConfig.Commented
	case models.ReviewStateDismissed:
		// Dismissed reviews don't have a specific emoji
		return ""
	default:
		return ""
	}
}

// GetEmojiForPRState returns the appropriate emoji for a closed or merged pull request.
// If the PR was merged (merged=true), it returns emojiConfig.Merged.
// If the PR was closed without merging (merged=false), it returns emojiConfig.Closed.
// The state parameter is currently unused but kept for potential future functionality.
func GetEmojiForPRState(state string, merged bool, emojiConfig config.EmojiConfig) string {
	if merged {
		return emojiConfig.Merged
	}
	return emojiConfig.Closed
}
