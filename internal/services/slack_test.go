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
				Skip: true,
			},
		},
		{
			name:        "Skip directive - no",
			description: "!review: no",
			expected: &PRDirectives{
				Skip: true,
			},
		},
		{
			name:        "Skip directive - case insensitive",
			description: "!REVIEW: SKIP",
			expected: &PRDirectives{
				Skip: true,
			},
		},
		{
			name:        "Channel directive",
			description: "!review: #dev-team",
			expected: &PRDirectives{
				Channel: "dev-team",
			},
		},
		{
			name:        "User CC directive",
			description: "!review: @john.doe",
			expected: &PRDirectives{
				UserToCC: "john.doe",
			},
		},
		{
			name:        "Combined directives",
			description: "!review: #dev-team @jane.smith",
			expected: &PRDirectives{
				Channel:  "dev-team",
				UserToCC: "jane.smith",
			},
		},
		{
			name:        "All directives including skip (skip takes precedence)",
			description: "!review: skip #dev-team @someone",
			expected: &PRDirectives{
				Skip:     true,
				Channel:  "dev-team",
				UserToCC: "someone",
			},
		},
		{
			name:        "Reviews plural form",
			description: "!reviews: #engineering @lead",
			expected: &PRDirectives{
				Channel:  "engineering",
				UserToCC: "lead",
			},
		},
		{
			name:        "Multiple spaces and mixed order",
			description: "!review:   @user1   #channel1    skip  ",
			expected: &PRDirectives{
				Skip:     true,
				Channel:  "channel1",
				UserToCC: "user1",
			},
		},
		{
			name:        "Invalid channel name (contains special chars)",
			description: "!review: #dev-team! @user",
			expected: &PRDirectives{
				UserToCC: "user",
			},
		},
		{
			name:        "Invalid user name (contains special chars)",
			description: "!review: #dev-team @user@domain",
			expected: &PRDirectives{
				Channel: "dev-team",
			},
		},
		{
			name:        "Valid channel with hyphens and underscores",
			description: "!review: #dev-team_backend-v2",
			expected: &PRDirectives{
				Channel: "dev-team_backend-v2",
			},
		},
		{
			name:        "Valid user with dots and hyphens",
			description: "!review: @john.doe-smith",
			expected: &PRDirectives{
				UserToCC: "john.doe-smith",
			},
		},
		{
			name: "Multiline description with directive",
			description: `This is a PR description.

!review: #dev-team @reviewer

More details about the PR.`,
			expected: &PRDirectives{
				Channel:  "dev-team",
				UserToCC: "reviewer",
			},
		},
		{
			name:        "Directive in middle of line",
			description: "Some text !review: #channel @user and more text",
			expected: &PRDirectives{
				Channel:  "channel",
				UserToCC: "user",
			},
		},
		{
			name:        "Multiple directives - last one wins",
			description: "!review: #first @user1\n!review: #second @user2",
			expected: &PRDirectives{
				Channel:  "second",
				UserToCC: "user2",
			},
		},
		{
			name:        "Empty directive content",
			description: "!review:",
			expected:    &PRDirectives{},
		},
		{
			name:        "Whitespace only directive content",
			description: "!review:   \t  ",
			expected:    &PRDirectives{},
		},
		{
			name:        "Case insensitive directive name",
			description: "!Review: #test @user",
			expected: &PRDirectives{
				Channel:  "test",
				UserToCC: "user",
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
				Channel:  "dev-team",
				UserToCC: "user",
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
				Skip:     true,
				UserToCC: "user",
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
				Channel: "backend-team",
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
