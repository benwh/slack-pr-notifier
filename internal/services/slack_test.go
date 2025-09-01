package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlackService_ParsePRDirectives(t *testing.T) {
	tests := []struct {
		name        string
		description string
		expected    *PRDirectives
	}{
		{
			name:        "Empty description",
			description: "",
			expected:    &PRDirectives{},
		},
		{
			name:        "No directives",
			description: "This is a regular PR description without directives.",
			expected:    &PRDirectives{},
		},
		{
			name:        "Skip directive - skip",
			description: "!review: skip",
			expected: &PRDirectives{
				Skip:               true,
				HasReviewDirective: true,
			},
		},
		{
			name:        "Skip directive - no",
			description: "!review: no",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip: true,
			},
		},
		{
			name:        "Skip directive - case insensitive",
			description: "!REVIEW: SKIP",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip: true,
			},
		},
		{
			name:        "Channel directive",
			description: "!review: #dev-team",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel: "dev-team",
			},
		},
		{
			name:        "User CC directive",
			description: "!review: @john.doe",
			expected: &PRDirectives{HasReviewDirective: true,
				UserToCC: "john.doe",
			},
		},
		{
			name:        "Combined directives",
			description: "!review: #dev-team @jane.smith",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "dev-team",
				UserToCC: "jane.smith",
			},
		},
		{
			name:        "All directives including skip (skip takes precedence)",
			description: "!review: skip #dev-team @someone",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip:     true,
				Channel:  "dev-team",
				UserToCC: "someone",
			},
		},
		{
			name:        "Reviews plural form",
			description: "!reviews: #engineering @lead",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "engineering",
				UserToCC: "lead",
			},
		},
		{
			name:        "Multiple spaces and mixed order",
			description: "!review:   @user1   #channel1    skip  ",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip:     true,
				Channel:  "channel1",
				UserToCC: "user1",
			},
		},
		{
			name:        "Invalid channel name (contains special chars)",
			description: "!review: #dev-team! @user",
			expected: &PRDirectives{HasReviewDirective: true,
				UserToCC: "user",
			},
		},
		{
			name:        "Invalid user name (contains special chars)",
			description: "!review: #dev-team @user@domain",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel: "dev-team",
			},
		},
		{
			name:        "Valid channel with hyphens and underscores",
			description: "!review: #dev-team_backend-v2",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel: "dev-team_backend-v2",
			},
		},
		{
			name:        "Valid user with dots and hyphens",
			description: "!review: @john.doe-smith",
			expected: &PRDirectives{HasReviewDirective: true,
				UserToCC: "john.doe-smith",
			},
		},
		{
			name: "Multiline description with directive",
			description: `This is a PR description.

!review: #dev-team @reviewer

More details about the PR.`,
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "dev-team",
				UserToCC: "reviewer",
			},
		},
		{
			name:        "Directive in middle of line",
			description: "Some text !review: #channel @user and more text",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "channel",
				UserToCC: "user",
			},
		},
		{
			name:        "Multiple directives - last one wins",
			description: "!review: #first @user1\n!review: #second @user2",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "second",
				UserToCC: "user2",
			},
		},
		{
			name:        "Empty directive content",
			description: "!review:",
			expected: &PRDirectives{
				HasReviewDirective: true,
			},
		},
		{
			name:        "Whitespace only directive content",
			description: "!review:   \t  ",
			expected: &PRDirectives{
				HasReviewDirective: true,
			},
		},
		{
			name:        "Case insensitive directive name",
			description: "!Review: #test @user",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel:  "test",
				UserToCC: "user",
			},
		},
		{
			name:        "Review-skip directive (shorthand for review: skip)",
			description: "!review-skip",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip: true,
			},
		},
		{
			name:        "Review-skip directive case insensitive",
			description: "!REVIEW-SKIP",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip: true,
			},
		},
		{
			name:        "Review-skip directive with other text",
			description: "Please remove this PR from Slack.\n\n!review-skip\n\nThanks!",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip: true,
			},
		},
		{
			name:        "Review-skip with later directive (skip persists unless overridden)",
			description: "!review-skip\n!review: #dev-team @user",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip:     true, // Skip persists since second directive doesn't mention skip
				Channel:  "dev-team",
				UserToCC: "user",
			},
		},
		{
			name:        "Review-skip with later channel directive (accumulative)",
			description: "!review: skip\n!review: #dev-team",
			expected: &PRDirectives{HasReviewDirective: true,
				Skip:    true, // Skip persists from first directive
				Channel: "dev-team",
			},
		},
		{
			name:        "Custom emoji directive",
			description: "!review: :rocket:",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":rocket:",
			},
		},
		{
			name:        "Custom emoji with other directives",
			description: "!review: :sparkles: #dev-team @reviewer",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":sparkles:",
				Channel:     "dev-team",
				UserToCC:    "reviewer",
			},
		},
		{
			name:        "Custom emoji in different order",
			description: "!review: #dev-team :fire: @user",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":fire:",
				Channel:     "dev-team",
				UserToCC:    "user",
			},
		},
		{
			name:        "Empty emoji (invalid)",
			description: "!review: :: #dev-team",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel: "dev-team",
			},
		},
		{
			name:        "Emoji without colons (invalid)",
			description: "!review: rocket #dev-team",
			expected: &PRDirectives{HasReviewDirective: true,
				Channel: "dev-team",
			},
		},
		{
			name:        "Multiple emojis - last one wins",
			description: "!review: :boom: #dev-team\n!review: :tada: @user",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":tada:",
				Channel:     "dev-team",
				UserToCC:    "user",
			},
		},
		{
			name:        "Emoji with skip directive",
			description: "!review: :fire: skip #dev-team",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":fire:",
				Skip:        true,
				Channel:     "dev-team",
			},
		},
		{
			name:        "Complex emoji name with underscores",
			description: "!review: :white_check_mark: #approvals",
			expected: &PRDirectives{HasReviewDirective: true,
				CustomEmoji: ":white_check_mark:",
				Channel:     "approvals",
			},
		},
		{
			name:        "Bare review directive (no colon)",
			description: "!review",
			expected: &PRDirectives{
				HasReviewDirective: true,
			},
		},
		{
			name:        "Bare review directive with text after",
			description: "!review and some other text",
			expected: &PRDirectives{
				HasReviewDirective: true,
			},
		},
		{
			name:        "Bare directive with channel",
			description: "!review #dev-team",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "dev-team",
			},
		},
		{
			name:        "Bare directive with user CC",
			description: "!review @john.doe",
			expected: &PRDirectives{
				HasReviewDirective: true,
				UserToCC:           "john.doe",
			},
		},
		{
			name:        "Bare directive with skip",
			description: "!review skip",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Skip:               true,
			},
		},
		{
			name:        "Bare directive with custom emoji",
			description: "!review :rocket:",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        ":rocket:",
			},
		},
		{
			name:        "Bare directive with channel and user",
			description: "!review #backend-team @reviewer",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "backend-team",
				UserToCC:           "reviewer",
			},
		},
		{
			name:        "Bare directive with all components",
			description: "!review :fire: #dev-team @lead skip",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        ":fire:",
				Channel:            "dev-team",
				UserToCC:           "lead",
				Skip:               true,
			},
		},
		{
			name:        "Bare plural form with channel",
			description: "!reviews #engineering",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "engineering",
			},
		},
		{
			name:        "Mixed formats - bare and colon",
			description: "!review: #first\n!review #second @user",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "second",
				UserToCC:           "user",
			},
		},
		{
			name:        "Bare directive case insensitive",
			description: "!REVIEW #DEV-TEAM @USER",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "DEV-TEAM",
				UserToCC:           "USER",
			},
		},
		// Unicode emoji test cases
		{
			name:        "Unicode fire emoji",
			description: "!review: 🔥",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "🔥",
			},
		},
		{
			name:        "Unicode rocket emoji with channel",
			description: "!review: 🚀 #dev-team",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "🚀",
				Channel:            "dev-team",
			},
		},
		{
			name:        "Unicode sparkles emoji with all components",
			description: "!review: ✨ #dev-team @reviewer skip",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "✨",
				Channel:            "dev-team",
				UserToCC:           "reviewer",
				Skip:               true,
			},
		},
		{
			name:        "Mixed emoji formats - Unicode wins (last directive)",
			description: "!review: :fire: #dev-team\n!review: 🚀 @user",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "🚀",
				Channel:            "dev-team",
				UserToCC:           "user",
			},
		},
		{
			name:        "Mixed emoji formats - colon emoji wins (last directive)",
			description: "!review: 🔥 #dev-team\n!review: :tada: @user",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        ":tada:",
				Channel:            "dev-team",
				UserToCC:           "user",
			},
		},
		{
			name:        "Unicode emoji in bare directive",
			description: "!review 🎯 #backend @lead",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "🎯",
				Channel:            "backend",
				UserToCC:           "lead",
			},
		},
		{
			name:        "Plain text should be ignored (not treated as emoji)",
			description: "!review: fire #dev-team",
			expected: &PRDirectives{
				HasReviewDirective: true,
				Channel:            "dev-team",
			},
		},
		{
			name:        "Multiple Unicode emojis in different order",
			description: "!review: #dev-team 🔥 @user ⚡",
			expected: &PRDirectives{
				HasReviewDirective: true,
				CustomEmoji:        "⚡", // Last emoji wins
				Channel:            "dev-team",
				UserToCC:           "user",
			},
		},
	}

	// Create a minimal SlackService just for testing the parsing function
	service := &SlackService{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.ParsePRDirectives(tt.description)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSlackService_ExtractChannelAndDirectives(t *testing.T) {
	tests := []struct {
		name               string
		description        string
		expectedChannel    string
		expectedDirectives *PRDirectives
	}{
		{
			name:            "Directive with channel and user",
			description:     "!review: #dev-team @user",
			expectedChannel: "dev-team",
			expectedDirectives: &PRDirectives{
				Channel:            "dev-team",
				UserToCC:           "user",
				HasReviewDirective: true,
			},
		},
		{
			name:               "No directive present",
			description:        "Regular description with no directives",
			expectedChannel:    "",
			expectedDirectives: &PRDirectives{},
		},
		{
			name:            "Directive without channel",
			description:     "!review: @user skip",
			expectedChannel: "",
			expectedDirectives: &PRDirectives{
				Skip:               true,
				UserToCC:           "user",
				HasReviewDirective: true,
			},
		},
		{
			name:               "Empty description",
			description:        "",
			expectedChannel:    "",
			expectedDirectives: &PRDirectives{},
		},
		{
			name:            "Channel only directive",
			description:     "!review: #backend-team",
			expectedChannel: "backend-team",
			expectedDirectives: &PRDirectives{
				Channel:            "backend-team",
				HasReviewDirective: true,
			},
		},
	}

	// Create a minimal SlackService just for testing
	service := &SlackService{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, directives := service.ExtractChannelAndDirectives(tt.description)
			assert.Equal(t, tt.expectedChannel, channel)
			assert.Equal(t, tt.expectedDirectives, directives)
		})
	}
}
