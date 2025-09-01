package utils

import (
	"testing"

	"github-slack-notifier/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestGetPRSizeEmojiWithConfig(t *testing.T) {
	tests := []struct {
		name         string
		linesChanged int
		user         *models.User
		expected     string
	}{
		{
			name:         "nil user falls back to default",
			linesChanged: 50,
			user:         nil,
			expected:     ":raccoon:", // Default for 50 lines
		},
		{
			name:         "user with nil config falls back to default",
			linesChanged: 150,
			user: &models.User{
				PRSizeConfig: nil,
			},
			expected: ":llama:", // Default for 150 lines
		},
		{
			name:         "user with disabled config falls back to default",
			linesChanged: 10,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: false,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 5, Emoji: ":custom:"},
					},
				},
			},
			expected: ":mouse2:", // Default for 10 lines
		},
		{
			name:         "user with custom config - small PR",
			linesChanged: 3,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: true,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 10, Emoji: ":tiny:"},
						{MaxLines: 100, Emoji: ":big:"},
					},
				},
			},
			expected: ":tiny:",
		},
		{
			name:         "user with custom config - large PR uses last threshold",
			linesChanged: 500,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: true,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 10, Emoji: ":tiny:"},
						{MaxLines: 100, Emoji: ":big:"},
					},
				},
			},
			expected: ":big:",
		},
		{
			name:         "user with unicode emojis in custom config",
			linesChanged: 25,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: true,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 20, Emoji: "🚀"},
						{MaxLines: 100, Emoji: "🎯"},
						{MaxLines: 500, Emoji: "🔥"},
					},
				},
			},
			expected: "🎯",
		},
		{
			name:         "fallback to default when custom config returns empty",
			linesChanged: 100,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled:    true,
					Thresholds: []models.PRSizeThreshold{},
				},
			},
			expected: ":dog2:", // Default for 100 lines
		},
		{
			name:         "very small PR with custom config",
			linesChanged: 1,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: true,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 5, Emoji: ":micro:"},
						{MaxLines: 50, Emoji: ":small:"},
						{MaxLines: 9999, Emoji: ":huge:"},
					},
				},
			},
			expected: ":micro:",
		},
		{
			name:         "very large PR exceeds all custom thresholds",
			linesChanged: 10000,
			user: &models.User{
				PRSizeConfig: &models.PRSizeConfiguration{
					Enabled: true,
					Thresholds: []models.PRSizeThreshold{
						{MaxLines: 10, Emoji: ":small:"},
						{MaxLines: 100, Emoji: ":medium:"},
						{MaxLines: 1000, Emoji: ":large:"},
					},
				},
			},
			expected: ":large:", // Should use last threshold
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetPRSizeEmojiWithConfig(tt.linesChanged, tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetPRSizeEmoji(t *testing.T) {
	tests := []struct {
		name         string
		linesChanged int
		expected     string
	}{
		{
			name:         "zero lines changed",
			linesChanged: 0,
			expected:     ":ant:",
		},
		{
			name:         "very small PR (ant)",
			linesChanged: 1,
			expected:     ":ant:",
		},
		{
			name:         "boundary case - ant threshold",
			linesChanged: 2,
			expected:     ":ant:",
		},
		{
			name:         "small PR (mouse)",
			linesChanged: 5,
			expected:     ":mouse2:",
		},
		{
			name:         "boundary case - mouse threshold",
			linesChanged: 10,
			expected:     ":mouse2:",
		},
		{
			name:         "medium-small PR (rabbit)",
			linesChanged: 15,
			expected:     ":rabbit2:",
		},
		{
			name:         "boundary case - rabbit threshold",
			linesChanged: 25,
			expected:     ":rabbit2:",
		},
		{
			name:         "medium PR (raccoon)",
			linesChanged: 35,
			expected:     ":raccoon:",
		},
		{
			name:         "boundary case - raccoon threshold",
			linesChanged: 50,
			expected:     ":raccoon:",
		},
		{
			name:         "large PR (dog)",
			linesChanged: 75,
			expected:     ":dog2:",
		},
		{
			name:         "boundary case - dog threshold",
			linesChanged: 100,
			expected:     ":dog2:",
		},
		{
			name:         "very large PR (llama)",
			linesChanged: 200,
			expected:     ":llama:",
		},
		{
			name:         "boundary case - llama threshold",
			linesChanged: 250,
			expected:     ":llama:",
		},
		{
			name:         "huge PR (pig)",
			linesChanged: 400,
			expected:     ":pig2:",
		},
		{
			name:         "boundary case - pig threshold",
			linesChanged: 500,
			expected:     ":pig2:",
		},
		{
			name:         "massive PR (gorilla)",
			linesChanged: 800,
			expected:     ":gorilla:",
		},
		{
			name:         "boundary case - gorilla threshold",
			linesChanged: 1000,
			expected:     ":gorilla:",
		},
		{
			name:         "enormous PR (elephant)",
			linesChanged: 1200,
			expected:     ":elephant:",
		},
		{
			name:         "boundary case - elephant threshold",
			linesChanged: 1500,
			expected:     ":elephant:",
		},
		{
			name:         "gigantic PR (t-rex)",
			linesChanged: 1800,
			expected:     ":t-rex:",
		},
		{
			name:         "boundary case - t-rex threshold",
			linesChanged: 2000,
			expected:     ":t-rex:",
		},
		{
			name:         "whale PR (largest)",
			linesChanged: 5000,
			expected:     ":whale2:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetPRSizeEmoji(tt.linesChanged)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDefaultPRSizeThresholds(t *testing.T) {
	thresholds := GetDefaultPRSizeThresholds()

	// Should have 11 thresholds matching the new logic
	assert.Len(t, thresholds, 11)

	// Check that all thresholds are in ascending order
	for i := 1; i < len(thresholds); i++ {
		assert.Greater(t, thresholds[i].MaxLines, thresholds[i-1].MaxLines,
			"Thresholds should be in ascending order")
	}

	// Check specific values match new implementation
	expected := []struct {
		emoji    string
		maxLines int
	}{
		{":ant:", 2},         // ant
		{":mouse2:", 10},     // mouse
		{":rabbit2:", 25},    // rabbit
		{":raccoon:", 50},    // raccoon
		{":dog2:", 100},      // dog
		{":llama:", 250},     // llama
		{":pig2:", 500},      // pig
		{":gorilla:", 1000},  // gorilla
		{":elephant:", 1500}, // elephant
		{":t-rex:", 2000},    // t-rex
		{":whale2:", 9999},   // whale
	}

	for i, exp := range expected {
		assert.Equal(t, exp.emoji, thresholds[i].Emoji, "Emoji at index %d should match", i)
		assert.Equal(t, exp.maxLines, thresholds[i].MaxLines, "MaxLines at index %d should match", i)
	}
}

func TestFormatPRSizeThresholds(t *testing.T) {
	tests := []struct {
		name       string
		thresholds []models.PRSizeThreshold
		expected   string
	}{
		{
			name:       "empty thresholds",
			thresholds: []models.PRSizeThreshold{},
			expected:   "",
		},
		{
			name: "single threshold",
			thresholds: []models.PRSizeThreshold{
				{MaxLines: 100, Emoji: "🐋"},
			},
			expected: "🐋 100",
		},
		{
			name: "multiple thresholds",
			thresholds: []models.PRSizeThreshold{
				{MaxLines: 5, Emoji: "🐜"},
				{MaxLines: 50, Emoji: "🐰"},
				{MaxLines: 1000, Emoji: "🐋"},
			},
			expected: "🐜 5\n🐰 50\n🐋 1000",
		},
		{
			name: "slack emoji format",
			thresholds: []models.PRSizeThreshold{
				{MaxLines: 10, Emoji: ":ant:"},
				{MaxLines: 100, Emoji: ":whale:"},
			},
			expected: ":ant: 10\n:whale: 100",
		},
		{
			name: "mixed emoji formats",
			thresholds: []models.PRSizeThreshold{
				{MaxLines: 5, Emoji: "🐜"},
				{MaxLines: 20, Emoji: ":mouse:"},
				{MaxLines: 100, Emoji: "🐋"},
			},
			expected: "🐜 5\n:mouse: 20\n🐋 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatPRSizeThresholds(tt.thresholds)
			assert.Equal(t, tt.expected, result)
		})
	}
}
