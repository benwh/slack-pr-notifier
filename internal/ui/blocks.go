// Package ui contains Slack Block Kit UI components and builders.
package ui

import (
	"fmt"

	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/utils"

	"github.com/slack-go/slack"
)

// HomeViewBuilder builds the App Home view blocks.
type HomeViewBuilder struct{}

// NewHomeViewBuilder creates a new home view builder.
func NewHomeViewBuilder() *HomeViewBuilder {
	return &HomeViewBuilder{}
}

// BuildHomeView constructs the home tab view based on user data.
func (b *HomeViewBuilder) BuildHomeView(
	user *models.User, hasGitHubInstallations bool, installations []*models.GitHubInstallation,
) slack.HomeTabViewRequest {
	blocks := []slack.Block{}

	// Introduction section
	blocks = append(blocks, b.buildIntroductionSection(user)...)

	// How it works section (only shown after GitHub connection)
	if user != nil && user.GitHubUsername != "" && user.Verified {
		blocks = append(blocks, b.buildHowItWorksSection()...)
	}

	// GitHub App installation warning (only shown if no installations exist)
	if !hasGitHubInstallations {
		blocks = append(blocks, b.buildGitHubInstallationWarning()...)
	}

	// My Options section
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "🔧 App setup", false, false),
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

	blocks = append(blocks, slack.NewDividerBlock())

	// PR size emoji configuration section
	blocks = append(blocks, b.buildPRSizeConfigSection(user)...)

	// Global Options section
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "⚙️ Advanced options", false, false),
		),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType, "_Configure *workspace-wide* settings_", false, false),
		),
		slack.NewDividerBlock(),
	)

	// Channel tracking settings section
	blocks = append(blocks, b.buildChannelTrackingSection()...)

	blocks = append(blocks, slack.NewDividerBlock())

	// GitHub installations management section
	blocks = append(blocks, b.buildGitHubInstallationsSection(installations)...)

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
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Setup*", false, false),
			nil, nil,
		),
	}

	if user != nil && user.GitHubUsername != "" && user.Verified {
		// Connected state
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("Connect your GitHub account\n_✅ Connected as @%s_", user.GitHubUsername),
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
		))
	} else {
		// Disconnected state
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"Connect your GitHub account\n_❌ Not connected - Link your GitHub account so PR Bot can identify your PRs_",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"connect_github",
					"connect",
					slack.NewTextBlockObject(slack.PlainTextType, "Connect GitHub account", false, false),
				).WithStyle(slack.StylePrimary),
			),
		))
	}

	return blocks
}

// buildChannelConfigSection builds the default channel configuration section.
func (b *HomeViewBuilder) buildChannelConfigSection(user *models.User) []slack.Block {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Options*", false, false),
			nil, nil,
		),
	}

	// Determine notification status based on both GitHub connection and notification preference
	var notificationStatus string
	var toggleText string
	var toggleStyle slack.Style

	// Check if GitHub is connected
	githubConnected := user != nil && user.GitHubUsername != "" && user.Verified

	if !githubConnected {
		// GitHub not connected - show pending state
		notificationStatus = "⏳ Pending - Connect GitHub first"
		toggleText = "Enable auto-posting"
		toggleStyle = slack.StylePrimary
	} else if user != nil && !user.NotificationsEnabled {
		// GitHub connected but notifications disabled
		notificationStatus = "❌ Disabled"
		toggleText = "Enable auto-posting"
		toggleStyle = slack.StylePrimary
	} else {
		// GitHub connected and notifications enabled
		notificationStatus = "✅ Enabled"
		toggleText = "Disable auto-posting"
		toggleStyle = slack.StyleDanger
	}

	// Build the section block with or without the button based on GitHub connection
	sectionText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("Enable PR posting\n_%s - When enabled, your PRs will be automatically posted_", notificationStatus),
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

	// User tagging toggle - only show if GitHub is connected
	if githubConnected {
		blocks = append(blocks, b.buildUserTaggingSection(user)...)
	}

	// Impersonation toggle - only show if GitHub is connected
	if githubConnected {
		blocks = append(blocks, b.buildImpersonationSection(user)...)
	}

	// Channel selection - always show but with different states
	var channelSectionText string
	var channelAccessory *slack.Accessory

	if !githubConnected {
		// GitHub not connected - show pending state
		channelSectionText = "Set your default channel\n_⏳ Pending - Connect GitHub first_"
	} else if user != nil && !user.NotificationsEnabled {
		// GitHub connected but notifications disabled
		channelSectionText = "Set your default channel\n_⏳ Pending - Enable notifications first_"
	} else if user != nil && user.DefaultChannel != "" {
		// Everything enabled and channel set
		channelSectionText = fmt.Sprintf("Set your default channel\n_✅ Current: <#%s> - This is where your PRs will be posted, "+
			"unless specified otherwise in the PR description_", user.DefaultChannel)
		channelAccessory = slack.NewAccessory(
			slack.NewButtonBlockElement(
				"select_channel",
				"change_channel",
				slack.NewTextBlockObject(slack.PlainTextType, "Change channel", false, false),
			),
		)
	} else {
		// Everything enabled but no channel set
		channelSectionText = "Set your default channel\n_:warning: No channel selected - Choose where your PRs should be posted_"
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

// buildUserTaggingSection builds the user tagging toggle section.
func (b *HomeViewBuilder) buildUserTaggingSection(user *models.User) []slack.Block {
	var taggingStatus string
	var taggingToggleText string
	var taggingToggleStyle slack.Style
	var taggingAccessory *slack.Accessory

	if user != nil && !user.NotificationsEnabled {
		// Notifications disabled - show pending state
		taggingStatus = "⏳ Pending - Enable notifications first"
	} else {
		// Determine tagging status - disabled by default and null treated as disabled
		taggingEnabled := user != nil && user.TaggingEnabled

		if taggingEnabled {
			taggingStatus = "✅ Enabled"
			taggingToggleText = "Disable mentions"
			taggingToggleStyle = slack.StyleDanger
		} else {
			taggingStatus = "🔔 Disabled"
			taggingToggleText = "Enable mentions"
			taggingToggleStyle = slack.StylePrimary
		}

		// Only show button if notifications are enabled
		if user != nil && user.NotificationsEnabled {
			taggingAccessory = slack.NewAccessory(
				slack.NewButtonBlockElement(
					"toggle_user_tagging",
					"toggle_tagging",
					slack.NewTextBlockObject(slack.PlainTextType, taggingToggleText, false, false),
				).WithStyle(taggingToggleStyle),
			)
		}
	}

	taggingSectionText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("Control user mentions\n_%s - When enabled, you will be mentioned (@username) in your PR messages, "+
			"to get *thread reply notifications*_", taggingStatus),
		false, false)

	return []slack.Block{
		slack.NewSectionBlock(taggingSectionText, nil, taggingAccessory),
	}
}

// buildImpersonationSection builds the impersonation toggle section.
func (b *HomeViewBuilder) buildImpersonationSection(user *models.User) []slack.Block {
	var impersonationStatus string
	var impersonationToggleText string
	var impersonationToggleStyle slack.Style
	var impersonationAccessory *slack.Accessory

	if user != nil && !user.NotificationsEnabled {
		// Notifications disabled - show pending state
		impersonationStatus = "⏳ Pending - Enable notifications first"
	} else {
		// Determine impersonation status - default to enabled for backward compatibility
		impersonationEnabled := user == nil || user.GetImpersonationEnabled()

		if impersonationEnabled {
			impersonationStatus = "✅ Enabled - Your PRs appear to come from you"
			impersonationToggleText = "Disable impersonation"
			impersonationToggleStyle = slack.StyleDanger
		} else {
			impersonationStatus = "❌ Disabled - Your PRs come from the bot"
			impersonationToggleText = "Enable impersonation"
			impersonationToggleStyle = slack.StylePrimary
		}

		// Only show toggle if notifications are enabled
		if user != nil && user.NotificationsEnabled {
			impersonationAccessory = slack.NewAccessory(
				slack.NewButtonBlockElement(
					"toggle_impersonation",
					"toggle_impersonation",
					slack.NewTextBlockObject(slack.PlainTextType, impersonationToggleText, false, false),
				).WithStyle(impersonationToggleStyle),
			)
		}
	}

	impersonationSectionText := slack.NewTextBlockObject(slack.MarkdownType,
		fmt.Sprintf("Control message appearance\n_%s - When enabled, PR notifications "+
			"appear to be posted by you instead of the bot_", impersonationStatus),
		false, false)

	return []slack.Block{
		slack.NewSectionBlock(impersonationSectionText, nil, impersonationAccessory),
	}
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

// BuildGitHubInstallationModal builds the GitHub App installation modal.
func (b *HomeViewBuilder) BuildGitHubInstallationModal(oauthURL string) slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:  slack.VTModal,
		Title: slack.NewTextBlockObject(slack.PlainTextType, "Install GitHub app", false, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						"🚀 *Ready to install PR Bot on GitHub!*\n\n"+
							fmt.Sprintf("<%s|:point_right: Install GitHub app>\n\n", oauthURL)+
							"During installation, you can:\n"+
							"• Select specific repositories or all repositories\n"+
							"• Choose which organization to install on\n"+
							"• Link your GitHub account automatically\n\n"+
							"*After installation:*\n"+
							"• Return to Slack - your App Home will automatically refresh\n"+
							"• You can close this modal and return to the installations list\n\n"+
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
func (b *HomeViewBuilder) buildIntroductionSection(user *models.User) []slack.Block {
	// Show different intro based on GitHub connection status
	githubConnected := user != nil && user.GitHubUsername != "" && user.Verified

	if githubConnected {
		// Terse version for connected users
		return []slack.Block{
			slack.NewHeaderBlock(
				slack.NewTextBlockObject(slack.PlainTextType, "PR Bot Settings 🤖", false, false),
			),
			slack.NewDividerBlock(),
		}
	}

	// Full intro for new users
	return []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "Welcome to PR Bot! 🤖", false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*PR Bot integrates between GitHub and Slack, with two main features:*\n\n"+
					"• *PR mirroring*: Automatically posts your PRs to Slack when opened.\n"+
					"• *PR status reactions*: Adds emoji reactions on Slack messages to show review/merge status.",
				false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
	}
}

// buildHowItWorksSection builds the "How it works" documentation section.
func (b *HomeViewBuilder) buildHowItWorksSection() []slack.Block {
	return []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "📖 Usage", false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*PR description hints:*\n"+
					"• Add or edit `!review ...` into your *GitHub PR description*, with various modifers, to customise behaviour:\n"+
					"• `!review #review-channel`: *override the Slack channel* the PR is posted into\n"+
					"• `!review @github_user`: *tag a user* (or multiple separated by spaces) for a review (only works if they've linked their account via PR Bot!)\n"+
					"• `!review skip`: to prevent the PR from being posted.\n"+
					"• `!review :custom_emoji:`: to override the emoji on the posted message.\n"+
					"• `<!-- !review @some_user #some_channel -->`: use a markdown comment to hide the hint\n\n"+

					"*Message management:*\n"+
					"• PRs opened as *draft* will be automatically skipped, and only posted when marked as ready for review.\n"+
					"• Add a :wastebasket: reaction to a bot-posted message, to *delete the message* (only the linked author can do this though!)\n"+
					"• PR review status reactions are automatic.\n"+
					"• If a PR hasn't been automaticaly posted, then you can post it yourself, and still receive review status reactions.",
				false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
	}
}

// buildGitHubInstallationWarning builds the GitHub App installation warning section.
func (b *HomeViewBuilder) buildGitHubInstallationWarning() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				":warning: *GitHub app installation required*\n"+
					"PR Bot needs to be installed on your GitHub repositories to receive webhook events.\n\n"+
					"Without this installation, the bot cannot detect new PRs, reviews, or status changes.",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"install_github_app",
					"install_app",
					slack.NewTextBlockObject(slack.PlainTextType, "Install GitHub App", false, false),
				).WithStyle(slack.StylePrimary),
			),
		),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType,
				"_This installation is separate from connecting your personal GitHub account. You need both for full functionality._",
				false, false),
		),
		slack.NewDividerBlock(),
	}
}

// buildGitHubInstallationsSection builds the GitHub installations management section.
func (b *HomeViewBuilder) buildGitHubInstallationsSection(installations []*models.GitHubInstallation) []slack.Block {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*GitHub app installations*\nManage GitHub installations and add new ones",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"manage_github_installations",
					"manage_installations",
					slack.NewTextBlockObject(slack.PlainTextType, "Manage installations", false, false),
				),
			),
		),
	}

	if len(installations) == 0 {
		blocks = append(blocks, slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType,
				"_No GitHub installations found. Install the GitHub App on your repositories to enable PR mirroring._",
				false, false),
		))
	} else {
		// Show summary of installations
		blocks = append(blocks, slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("_Currently installed on %d organization(s)/account(s)_", len(installations)),
				false, false),
		))
	}

	return blocks
}

// BuildGitHubInstallationsModal builds the GitHub installations management modal.
func (b *HomeViewBuilder) BuildGitHubInstallationsModal(
	installations []*models.GitHubInstallation, baseURL, appSlug string,
) slack.ModalViewRequest {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*Current GitHub app installations*",
				false, false),
			nil, nil,
		),
	}

	if len(installations) == 0 {
		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					"_No GitHub installations found._",
					false, false),
				nil, nil,
			),
		)
	} else {
		for _, installation := range installations {
			// Build the management URL for this installation
			var managementURL string
			if installation.AccountType == "Organization" {
				managementURL = fmt.Sprintf("https://github.com/organizations/%s/settings/installations/%d",
					installation.AccountLogin, installation.ID)
			} else {
				managementURL = fmt.Sprintf("https://github.com/settings/installations/%d", installation.ID)
			}

			// Build repository info
			repoInfo := "All repositories"
			if installation.RepositorySelection == "selected" && len(installation.Repositories) > 0 {
				repoInfo = fmt.Sprintf("%d selected repositories", len(installation.Repositories))
			} else if installation.RepositorySelection == "selected" {
				repoInfo = "Selected repositories (none configured)"
			}

			blocks = append(blocks,
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						fmt.Sprintf("*%s* (%s)\n%s • Installed %s\n<%s|:point_right: Manage on GitHub>",
							installation.AccountLogin,
							installation.AccountType,
							repoInfo,
							installation.InstalledAt.Format("Jan 2, 2006"),
							managementURL),
						false, false),
					nil, nil,
				),
			)
		}
	}

	// Add divider and new installation section
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*Add new installation*\nInstall the GitHub app on additional organizations or repositories",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"add_github_installation",
					"add_installation",
					slack.NewTextBlockObject(slack.PlainTextType, "Add new installation", false, false),
				).WithStyle(slack.StylePrimary),
			),
		),
	)

	// Add refresh instructions at the bottom
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType,
				"_After completing a GitHub installation, close this modal to see updated installations on your App Home._",
				false, false),
		),
	)

	return slack.ModalViewRequest{
		Type:       slack.VTModal,
		Title:      slack.NewTextBlockObject(slack.PlainTextType, "GitHub installations", false, false),
		CallbackID: "github_installations_modal",
		Blocks:     slack.Blocks{BlockSet: blocks},
	}
}

// buildPRSizeConfigSection builds the PR size emoji configuration section.
func (b *HomeViewBuilder) buildPRSizeConfigSection(user *models.User) []slack.Block {
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Emoji settings*", false, false),
			nil, nil,
		),
	}

	// Determine configuration status
	var configStatus string
	var buttonText string
	var buttonStyle slack.Style

	if user != nil && user.PRSizeConfig != nil && user.PRSizeConfig.Enabled {
		configStatus = fmt.Sprintf("✅ Custom emojis enabled (%d thresholds)", len(user.PRSizeConfig.Thresholds))
		buttonText = "Configure PR emojis"
		buttonStyle = slack.StyleDefault
	} else {
		configStatus = ":no_good: Using default animal emojis"
		buttonText = "Configure PR emojis"
		buttonStyle = slack.StylePrimary
	}

	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				fmt.Sprintf("Configure PR size emojis based on line count\n_%s_", configStatus),
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"configure_pr_size_emojis",
					"configure_emojis",
					slack.NewTextBlockObject(slack.PlainTextType, buttonText, false, false),
				).WithStyle(buttonStyle),
			),
		),
	)

	return blocks
}

// BuildPRSizeConfigModal builds the PR size emoji configuration modal.
func (b *HomeViewBuilder) BuildPRSizeConfigModal(user *models.User) slack.ModalViewRequest {
	// Prepare current configuration as text for the input
	var currentConfig string
	if user != nil && user.PRSizeConfig != nil && user.PRSizeConfig.Enabled && len(user.PRSizeConfig.Thresholds) > 0 {
		// Use user's custom configuration
		currentConfig = utils.FormatPRSizeThresholds(user.PRSizeConfig.Thresholds)
	} else {
		// Show default config as an example
		defaultThresholds := utils.GetDefaultPRSizeThresholds()
		currentConfig = utils.FormatPRSizeThresholds(defaultThresholds)
	}

	return slack.ModalViewRequest{
		Type:       slack.VTModal,
		Title:      slack.NewTextBlockObject(slack.PlainTextType, "Configure PR Emojis", false, false),
		CallbackID: "pr_size_config",
		Submit:     slack.NewTextBlockObject(slack.PlainTextType, "Save", false, false),
		Close:      slack.NewTextBlockObject(slack.PlainTextType, "Cancel", false, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						"*Customize PR size emojis and thresholds*\n\n"+
							"Configure which emoji appears based on number of lines changed in a PR. "+
							"Each line must contain an emoji and a *maximum* line count.\n\n"+
							"*Format:* `:emoji_name: max_lines`\n"+
							"*Examples:*\n"+
							"• `:ant: 5` — PRs with ≤5 lines get 🐜\n"+
							"• `🐭 20` — PRs with ≤20 lines get 🐭\n"+
							"• `:custom_small: 50` — Use workspace custom emojis\n\n"+
							"*Common emoji names:*\n"+
							"`:ant:` `:mouse2:` `:rabbit2:` `:badger:` `:dog2:` `:racing_horse:` `:gorilla:` `:elephant:` `:t-rex:` `:whale2:`\n\n"+
							"*Tips:*\n"+
							"• Numbers must be in ascending order\n"+
							"• The last line catches all larger PRs, regardless of size\n"+
							"• Copy/paste Unicode emojis or use `:name:` format",
						false, false),
					nil, nil,
				),
				&slack.InputBlock{
					Type:     slack.MBTInput,
					BlockID:  "pr_size_config_input",
					Label:    slack.NewTextBlockObject(slack.PlainTextType, "Emoji configuration", false, false),
					Hint:     slack.NewTextBlockObject(slack.PlainTextType, "One emoji and threshold per line", false, false),
					Optional: true,
					Element: &slack.PlainTextInputBlockElement{
						Type:         slack.METPlainTextInput,
						ActionID:     "pr_size_config_text",
						Placeholder:  slack.NewTextBlockObject(slack.PlainTextType, "Enter emoji configurations...", false, false),
						Multiline:    true,
						InitialValue: currentConfig,
					},
				},
				slack.NewSectionBlock(
					slack.NewTextBlockObject(slack.MarkdownType,
						"*Reset to defaults*\nTo go back to the default animal emojis: "+
							"*delete all text in the box*, and then save.",
						false, false),
					nil, nil,
				),
			},
		},
	}
}
