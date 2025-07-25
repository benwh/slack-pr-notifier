// Package ui contains Slack Block Kit UI components and builders.
package ui

import (
	"fmt"

	"github-slack-notifier/internal/models"

	"github.com/slack-go/slack"
)

// HomeViewBuilder builds the App Home view blocks.
type HomeViewBuilder struct{}

// NewHomeViewBuilder creates a new home view builder.
func NewHomeViewBuilder() *HomeViewBuilder {
	return &HomeViewBuilder{}
}

// BuildHomeView constructs the home tab view based on user data.
func (b *HomeViewBuilder) BuildHomeView(user *models.User) slack.HomeTabViewRequest {
	blocks := []slack.Block{}

	// Introduction section
	blocks = append(blocks, b.buildIntroductionSection()...)

	// My Options section
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "🔧 Setup your account", false, false),
		),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType, "_Configure your personal settings to start receiving PR notifications_", false, false),
		),
		slack.NewDividerBlock(),
	)

	// GitHub connection status section
	blocks = append(blocks, b.buildGitHubConnectionSection(user)...)

	blocks = append(blocks, slack.NewDividerBlock())

	// Default channel configuration section
	blocks = append(blocks, b.buildChannelConfigSection(user)...)

	// Global Options section
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "⚙️ Advanced options", false, false),
		),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType, "_Configure workspace-wide settings_", false, false),
		),
		slack.NewDividerBlock(),
	)

	// Channel tracking settings section
	blocks = append(blocks, b.buildChannelTrackingSection()...)

	blocks = append(blocks, slack.NewDividerBlock())

	// Quick actions section
	blocks = append(blocks, b.buildQuickActionsSection()...)

	return slack.HomeTabViewRequest{
		Type:   slack.VTHomeTab,
		Blocks: slack.Blocks{BlockSet: blocks},
	}
}

// buildGitHubConnectionSection builds the GitHub connection status section.
func (b *HomeViewBuilder) buildGitHubConnectionSection(user *models.User) []slack.Block {
	if user != nil && user.GitHubUsername != "" && user.Verified {
		// Connected state
		return []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("*Step 1: Connect your GitHub account*\n✅ Connected as @%s (Verified via OAuth)", user.GitHubUsername),
					false, false),
				nil,
				slack.NewAccessory(
					slack.NewButtonBlockElement(
						"disconnect_github",
						"disconnect",
						slack.NewTextBlockObject(slack.PlainTextType, "Disconnect", false, false),
					).WithStyle(slack.StyleDanger).WithConfirm(
						slack.NewConfirmationBlockObject(
							slack.NewTextBlockObject(slack.PlainTextType, "Disconnect GitHub?", false, false),
							slack.NewTextBlockObject(slack.MarkdownType, "Are you sure you want to disconnect your GitHub account?", false, false),
							slack.NewTextBlockObject(slack.PlainTextType, "Yes, disconnect", false, false),
							slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false),
						),
					),
				),
			),
		}
	}

	// Disconnected state
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*Step 1: Connect your GitHub account*\n❌ Not connected - Link your GitHub account so PR Bot can identify your PRs",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"connect_github",
					"connect",
					slack.NewTextBlockObject(slack.PlainTextType, "Connect GitHub account", false, false),
				).WithStyle(slack.StylePrimary),
			),
		),
	}
}

// buildChannelConfigSection builds the default channel configuration section.
func (b *HomeViewBuilder) buildChannelConfigSection(user *models.User) []slack.Block {
	blocks := []slack.Block{}

	// Determine notification status based on both GitHub connection and notification preference
	var notificationStatus string
	var toggleText string
	var toggleStyle slack.Style

	// Check if GitHub is connected
	githubConnected := user != nil && user.GitHubUsername != "" && user.Verified

	if !githubConnected {
		// GitHub not connected - show pending state
		notificationStatus = "⏳ Pending - Connect GitHub first"
		toggleText = "Enable notifications"
		toggleStyle = slack.StylePrimary
	} else if user != nil && !user.NotificationsEnabled {
		// GitHub connected but notifications disabled
		notificationStatus = "❌ Disabled"
		toggleText = "Enable notifications"
		toggleStyle = slack.StylePrimary
	} else {
		// GitHub connected and notifications enabled
		notificationStatus = "✅ Enabled"
		toggleText = "Disable notifications"
		toggleStyle = slack.StyleDanger
	}

	// Build the section block with or without the button based on GitHub connection
	sectionText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("*Step 2: Enable PR mirroring*\n%s - When enabled, your PRs will be automatically posted", notificationStatus),
		false, false)

	var accessory *slack.Accessory
	if githubConnected {
		// Only show the button if GitHub is connected
		accessory = slack.NewAccessory(
			slack.NewButtonBlockElement(
				"toggle_notifications",
				"toggle",
				slack.NewTextBlockObject(slack.PlainTextType, toggleText, false, false),
			).WithStyle(toggleStyle),
		)
	}

	blocks = append(blocks, slack.NewSectionBlock(sectionText, nil, accessory))

	// Channel selection - always show but with different states
	var channelSectionText string
	var channelAccessory *slack.Accessory

	if !githubConnected {
		// GitHub not connected - show pending state
		channelSectionText = "*Step 3: Set your default channel*\n⏳ Pending - Connect GitHub first"
	} else if user != nil && !user.NotificationsEnabled {
		// GitHub connected but notifications disabled
		channelSectionText = "*Step 3: Set your default channel*\n⏳ Pending - Enable notifications first"
	} else if user != nil && user.DefaultChannel != "" {
		// Everything enabled and channel set
		channelSectionText = fmt.Sprintf("*Step 3: Set your default channel*\n✅ Current: <#%s> - This is where your PRs will be posted, "+
			"unless specified otherwise in the PR description", user.DefaultChannel)
		channelAccessory = slack.NewAccessory(
			slack.NewButtonBlockElement(
				"select_channel",
				"change_channel",
				slack.NewTextBlockObject(slack.PlainTextType, "Change channel", false, false),
			),
		)
	} else {
		// Everything enabled but no channel set
		channelSectionText = "*Step 3: Set your default channel*\n:warning: No channel selected - Choose where your PRs should be posted"
		channelAccessory = slack.NewAccessory(
			slack.NewButtonBlockElement(
				"select_channel",
				"select_channel",
				slack.NewTextBlockObject(slack.PlainTextType, "Select channel", false, false),
			).WithStyle(slack.StylePrimary),
		)
	}

	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, channelSectionText, false, false),
		nil,
		channelAccessory,
	))

	return blocks
}

// buildChannelTrackingSection builds the channel tracking settings section.
func (b *HomeViewBuilder) buildChannelTrackingSection() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*PR link detection settings*\nConfigure which channels automatically track and react to GitHub PR links _*not*_ managed by the bot",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"manage_channel_tracking",
					"manage_tracking",
					slack.NewTextBlockObject(slack.PlainTextType, "Manage reaction syncing", false, false),
				),
			),
		),
	}
}

// buildQuickActionsSection builds the quick actions section.
func (b *HomeViewBuilder) buildQuickActionsSection() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Quick actions*", false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			"quick_actions",
			slack.NewButtonBlockElement(
				"refresh_view",
				"refresh",
				slack.NewTextBlockObject(slack.PlainTextType, "🔄 Refresh page", false, false),
			),
		),
	}
}

// BuildOAuthModal builds the OAuth connection modal.
func (b *HomeViewBuilder) BuildOAuthModal(oauthURL string) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: slack.NewTextBlockObject(slack.PlainTextType, "Connect GitHub account", false, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						"*Authorise via GitHub to link Slack and GitHub identities*\n\n"+
							fmt.Sprintf("<%s|:point_right: Initiate OAuth flow>\n\n", oauthURL)+
							"_This link expires in 15 minutes._",
						false, false),
					nil, nil,
				),
			},
		},
	}
}

// BuildChannelSelectorModal builds the channel selector modal.
func (b *HomeViewBuilder) BuildChannelSelectorModal() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:       slack.VTModal,
		Title:      slack.NewTextBlockObject(slack.PlainTextType, "Select channel", false, false),
		CallbackID: "channel_selector",
		Submit:     slack.NewTextBlockObject(slack.PlainTextType, "Save", false, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType, "Select default channel for PRs to be posted to:\n\n"+
						":information_source: The bot will automatically join public channels when selected.\n"+
						":warning: Private channels are not supported for security reasons.",
						false, false),
					nil, nil,
				),
				slack.NewInputBlock(
					"channel_input",
					slack.NewTextBlockObject(slack.PlainTextType, "Channel", false, false),
					nil, // No hint text
					slack.NewOptionsSelectBlockElement(
						slack.OptTypeChannels,
						slack.NewTextBlockObject(slack.PlainTextType, "Choose a public channel", false, false),
						"channel_select",
					),
				),
			},
		},
	}
}

// BuildChannelTrackingModal builds the channel tracking configuration modal.
func (b *HomeViewBuilder) BuildChannelTrackingModal(configs []*models.ChannelConfig) slack.ModalViewRequest {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"Select a channel to configure:",
				false, false),
			nil, nil,
		),
		slack.NewInputBlock(
			"channel_tracking_input",
			slack.NewTextBlockObject(slack.PlainTextType, "Channel", false, false),
			nil, // No hint text
			slack.NewOptionsSelectBlockElement(
				slack.OptTypeChannels,
				slack.NewTextBlockObject(slack.PlainTextType, "Choose a channel", false, false),
				"tracking_channel_select",
			),
		),
	}

	// Add currently configured channels section if any exist
	if len(configs) > 0 {
		blocks = append(blocks,
			slack.NewDividerBlock(),
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType, "*Currently Configured Channels:*", false, false),
				nil, nil,
			),
		)

		for _, config := range configs {
			status := "✅ Tracking Enabled"
			if !config.ManualTrackingEnabled {
				status = "❌ Tracking Disabled"
			}
			blocks = append(blocks, slack.NewContextBlock(
				"",
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("<#%s> %s", config.SlackChannelID, status),
					false, false),
			))
		}

		blocks = append(blocks, slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType,
				"_Note: Channels not listed use the default setting (tracking enabled)_",
				false, false),
		))
	}

	return slack.ModalViewRequest{
		Type:       slack.VTModal,
		Title:      slack.NewTextBlockObject(slack.PlainTextType, "Channel Tracking", false, false),
		Close:      slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false),
		Submit:     slack.NewTextBlockObject(slack.PlainTextType, "Next", false, false),
		CallbackID: "channel_tracking_selector",
		Blocks:     slack.Blocks{BlockSet: blocks},
	}
}

// BuildChannelTrackingConfigModal builds the modal for configuring a specific channel's tracking settings.
func (b *HomeViewBuilder) BuildChannelTrackingConfigModal(channelID, channelName string, currentlyEnabled bool) slack.ModalViewRequest {
	currentSettingText := "Enabled"
	if !currentlyEnabled {
		currentSettingText = "Disabled"
	}

	// Truncate channel name if needed to fit in title (max 24 chars)
	const maxChannelNameLength = 15
	const truncatedLength = 12
	displayName := channelName
	if len(displayName) > maxChannelNameLength {
		displayName = displayName[:truncatedLength] + "..."
	}

	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		Title:           slack.NewTextBlockObject(slack.PlainTextType, fmt.Sprintf("#%s", displayName), false, false),
		CallbackID:      "save_channel_tracking",
		Submit:          slack.NewTextBlockObject(slack.PlainTextType, "Save", false, false),
		PrivateMetadata: channelID, // Store channel ID in private metadata
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						"*Manual PR Link Tracking:*",
						false, false),
					nil, nil,
				),
				slack.NewInputBlock(
					"tracking_enabled_input",
					slack.NewTextBlockObject(slack.PlainTextType, "Setting", false, false),
					slack.NewTextBlockObject(slack.PlainTextType, "Choose setting", false, false),
					slack.NewRadioButtonsBlockElement(
						"tracking_enabled_radio",
						slack.NewOptionBlockObject(
							"true",
							slack.NewTextBlockObject(slack.PlainTextType, "Enabled (Default)", false, false),
							slack.NewTextBlockObject(slack.PlainTextType, "The bot will track GitHub PR links posted by users in this channel", false, false),
						),
						slack.NewOptionBlockObject(
							"false",
							slack.NewTextBlockObject(slack.PlainTextType, "Disabled", false, false),
							slack.NewTextBlockObject(slack.PlainTextType, "The bot will ignore GitHub PR links posted by users in this channel", false, false),
						),
					),
				),
				slack.NewContextBlock(
					"",
					slack.NewTextBlockObject(slack.MarkdownType,
						fmt.Sprintf("_Current Setting: %s_", currentSettingText),
						false, false),
				),
			},
		},
	}
}

// buildIntroductionSection builds the introduction section explaining what PR Bot does.
func (b *HomeViewBuilder) buildIntroductionSection() []slack.Block {
	return []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "Welcome to PR Bot! 🤖", false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*PR Bot provides seamless integration between GitHub and Slack with two key features:*\n\n"+
					"• *PR mirroring*: Automatically posts your PRs to Slack when opened\n"+
					"• *PR status reactions*: Adds emoji reactions to show review status (includes manually-posted links)",
				false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
	}
}
