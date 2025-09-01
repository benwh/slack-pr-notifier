package handlers

import (
	"testing"

	"github-slack-notifier/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackHandler_parsePRSizeConfig(t *testing.T) {
	// Create a minimal SlackHandler instance for testing
	handler := &SlackHandler{}

	tests := []struct {
		name           string
		configText     string
		expectedValid  bool
		expectedConfig *models.PRSizeConfiguration
		expectedErrors map[string]string
	}{
		{
			name:          "empty config text disables custom config",
			configText:    "",
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled:    false,
				Thresholds: nil,
			},
			expectedErrors: nil,
		},
		{
			name:          "whitespace only config disables custom config",
			configText:    "   \n\t  \n  ",
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled:    false,
				Thresholds: nil,
			},
			expectedErrors: nil,
		},
		{
			name:          "valid single threshold with slack emoji",
			configText:    ":whale: 100",
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 100, Emoji: ":whale:"},
				},
			},
			expectedErrors: nil,
		},
		{
			name:          "valid single threshold with unicode emoji",
			configText:    "🐋 100",
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 100, Emoji: "🐋"},
				},
			},
			expectedErrors: nil,
		},
		{
			name: "valid multiple thresholds in ascending order",
			configText: `:ant: 5
:mouse: 20
:rabbit: 50
:whale: 1000`,
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 5, Emoji: ":ant:"},
					{MaxLines: 20, Emoji: ":mouse:"},
					{MaxLines: 50, Emoji: ":rabbit:"},
					{MaxLines: 1000, Emoji: ":whale:"},
				},
			},
			expectedErrors: nil,
		},
		{
			name: "valid config with extra whitespace",
			configText: `  :ant: 5  
  
  :whale: 100  `,
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 5, Emoji: ":ant:"},
					{MaxLines: 100, Emoji: ":whale:"},
				},
			},
			expectedErrors: nil,
		},
		{
			name:           "invalid format - missing threshold",
			configText:     ":ant:",
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 1: Format must be 'emoji max_lines' (e.g., ':ant: 5')",
			},
		},
		{
			name:           "invalid format - non-numeric threshold",
			configText:     ":ant: abc",
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 1: Max lines must be a positive number",
			},
		},
		{
			name:           "invalid format - negative threshold",
			configText:     ":ant: -5",
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 1: Max lines must be a positive number",
			},
		},
		{
			name:           "invalid format - zero threshold",
			configText:     ":ant: 0",
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 1: Max lines must be a positive number",
			},
		},
		{
			name: "invalid - thresholds not in ascending order",
			configText: `:ant: 50
:whale: 10`,
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 2: Max lines (10) must be greater than previous (50)",
			},
		},
		{
			name: "invalid - duplicate thresholds",
			configText: `:ant: 10
:whale: 10`,
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 2: Max lines (10) must be greater than previous (10)",
			},
		},
		{
			name:           "invalid emoji format - not slack or unicode",
			configText:     "invalid_emoji 100",
			expectedValid:  false,
			expectedConfig: nil,
			expectedErrors: map[string]string{
				"pr_size_config_input": "Line 1: Invalid emoji format. Use ':emoji_name:' or Unicode emoji",
			},
		},
		{
			name: "valid config with unicode and slack emojis mixed",
			configText: `🐜 5
:mouse: 20
🐋 100`,
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 5, Emoji: "🐜"},
					{MaxLines: 20, Emoji: ":mouse:"},
					{MaxLines: 100, Emoji: "🐋"},
				},
			},
			expectedErrors: nil,
		},
		{
			name:          "very large threshold number",
			configText:    `:whale: 999999`,
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled: true,
				Thresholds: []models.PRSizeThreshold{
					{MaxLines: 999999, Emoji: ":whale:"},
				},
			},
			expectedErrors: nil,
		},
		{
			name: "empty lines only results in disabled config",
			configText: `


`,
			expectedValid: true,
			expectedConfig: &models.PRSizeConfiguration{
				Enabled:    false,
				Thresholds: nil,
			},
			expectedErrors: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, errors := handler.parsePRSizeConfig(tt.configText)

			if tt.expectedValid {
				require.Nil(t, errors, "Expected no validation errors")
				require.NotNil(t, config, "Expected valid config")
				assert.Equal(t, tt.expectedConfig.Enabled, config.Enabled)

				if tt.expectedConfig.Thresholds == nil {
					assert.Nil(t, config.Thresholds)
				} else {
					require.NotNil(t, config.Thresholds)
					assert.Len(t, config.Thresholds, len(tt.expectedConfig.Thresholds))

					for i, expectedThreshold := range tt.expectedConfig.Thresholds {
						if i < len(config.Thresholds) {
							assert.Equal(t, expectedThreshold.MaxLines, config.Thresholds[i].MaxLines)
							assert.Equal(t, expectedThreshold.Emoji, config.Thresholds[i].Emoji)
						}
					}
				}
			} else {
				require.NotNil(t, errors, "Expected validation errors")
				assert.Nil(t, config, "Expected no config when validation fails")

				// Check that expected error messages are present
				for key, expectedMessage := range tt.expectedErrors {
					actualMessage, exists := errors[key]
					assert.True(t, exists, "Expected error key %s to exist", key)
					assert.Equal(t, expectedMessage, actualMessage)
				}
			}
		})
	}
}
