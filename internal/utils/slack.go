package utils

import "github-slack-notifier/internal/config"

// GetPRSizeEmoji returns an animal emoji based on the number of lines changed in a PR.
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

// GetEmojiForReviewState returns the appropriate emoji for a given review state.
func GetEmojiForReviewState(state string, emojiConfig config.EmojiConfig) string {
	switch state {
	case "approved":
		return emojiConfig.Approved
	case "changes_requested":
		return emojiConfig.ChangesRequested
	case "commented":
		return emojiConfig.Commented
	default:
		return ""
	}
}

// GetEmojiForPRState returns the appropriate emoji for a closed or merged PR.
func GetEmojiForPRState(state string, merged bool, emojiConfig config.EmojiConfig) string {
	if merged {
		return emojiConfig.Merged
	}
	return emojiConfig.Closed
}
