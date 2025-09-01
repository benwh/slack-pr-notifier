package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUser_GetImpersonationEnabled(t *testing.T) {
	tests := []struct {
		name     string
		user     *User
		expected bool
	}{
		{
			name: "nil pointer defaults to true",
			user: &User{
				ImpersonationEnabled: nil,
			},
			expected: true,
		},
		{
			name: "explicit true value",
			user: &User{
				ImpersonationEnabled: &[]bool{true}[0],
			},
			expected: true,
		},
		{
			name: "explicit false value",
			user: &User{
				ImpersonationEnabled: &[]bool{false}[0],
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.user.GetImpersonationEnabled()
			if got != tt.expected {
				t.Errorf("GetImpersonationEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPRSizeConfiguration_GetCustomPRSizeEmoji(t *testing.T) {
	tests := []struct {
		name         string
		config       *PRSizeConfiguration
		linesChanged int
		expected     string
	}{
		{
			name:         "nil config returns empty string",
			config:       nil,
			linesChanged: 50,
			expected:     "",
		},
		{
			name: "disabled config returns empty string",
			config: &PRSizeConfiguration{
				Enabled: false,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 10, Emoji: ":small:"},
					{MaxLines: 100, Emoji: ":large:"},
				},
			},
			linesChanged: 50,
			expected:     "",
		},
		{
			name: "empty thresholds returns empty string",
			config: &PRSizeConfiguration{
				Enabled:    true,
				Thresholds: []PRSizeThreshold{},
			},
			linesChanged: 50,
			expected:     "",
		},
		{
			name: "single threshold - lines within threshold",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 100, Emoji: ":whale:"},
				},
			},
			linesChanged: 50,
			expected:     ":whale:",
		},
		{
			name: "single threshold - lines exceed threshold uses last emoji",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 100, Emoji: ":whale:"},
				},
			},
			linesChanged: 500,
			expected:     ":whale:",
		},
		{
			name: "multiple thresholds - first threshold match",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 20, Emoji: "🐭"},
					{MaxLines: 100, Emoji: "🐱"},
					{MaxLines: 1000, Emoji: "🐋"},
				},
			},
			linesChanged: 3,
			expected:     "🐜",
		},
		{
			name: "multiple thresholds - middle threshold match",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 20, Emoji: "🐭"},
					{MaxLines: 100, Emoji: "🐱"},
					{MaxLines: 1000, Emoji: "🐋"},
				},
			},
			linesChanged: 50,
			expected:     "🐱",
		},
		{
			name: "multiple thresholds - exact threshold match",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 20, Emoji: "🐭"},
					{MaxLines: 100, Emoji: "🐱"},
					{MaxLines: 1000, Emoji: "🐋"},
				},
			},
			linesChanged: 20,
			expected:     "🐭",
		},
		{
			name: "multiple thresholds - exceeds all thresholds uses last emoji",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 20, Emoji: "🐭"},
					{MaxLines: 100, Emoji: "🐱"},
					{MaxLines: 1000, Emoji: "🐋"},
				},
			},
			linesChanged: 5000,
			expected:     "🐋",
		},
		{
			name: "slack emoji format with custom thresholds",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 10, Emoji: ":custom_tiny:"},
					{MaxLines: 50, Emoji: ":custom_small:"},
					{MaxLines: 200, Emoji: ":custom_medium:"},
					{MaxLines: 9999, Emoji: ":custom_huge:"},
				},
			},
			linesChanged: 150,
			expected:     ":custom_medium:",
		},
		{
			name: "zero lines changed",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 100, Emoji: "🐋"},
				},
			},
			linesChanged: 0,
			expected:     "🐜",
		},
		{
			name: "negative lines changed (edge case)",
			config: &PRSizeConfiguration{
				Enabled: true,
				Thresholds: []PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 100, Emoji: "🐋"},
				},
			},
			linesChanged: -10,
			expected:     "🐜",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetCustomPRSizeEmoji(tt.linesChanged)
			assert.Equal(t, tt.expected, result)
		})
	}
}
