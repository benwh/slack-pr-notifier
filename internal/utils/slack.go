package utils

import (
	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/models"
)

// GetPRSizeEmoji returns an animal emoji based on the number of lines changed in a PR.
// The emoji serves as a visual indicator of PR size, with larger animals representing
// bigger changes. The thresholds are: ant (<5), mouse (≤20), rabbit (≤50), cat (≤100),
// dog (≤250), horse (≤500), bear (≤1000), elephant (≤1500), dinosaur (≤2000), whale (>2000).
//
//nolint:mnd
func GetPRSizeEmoji(linesChanged int) string {
	switch {
	case linesChanged < 5:
		return "🐜" // ant
	case linesChanged <= 20:
		return "🐭" // mouse
	case linesChanged <= 50:
		return "🐰" // rabbit
	case linesChanged <= 100:
		return "🐱" // cat
	case linesChanged <= 250:
		return "🐕" // dog
	case linesChanged <= 500:
		return "🐴" // horse
	case linesChanged <= 1000:
		return "🐻" // bear
	case linesChanged <= 1500:
		return "🐘" // elephant
	case linesChanged <= 2000:
		return "🦕" // dinosaur
	default:
		return "🐋" // whale
	}
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
