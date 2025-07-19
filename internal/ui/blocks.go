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
	blocks := []slack.Block{
		// Header section
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, "Configuration options", false, false),
		),
		slack.NewDividerBlock(),
	}

	// GitHub connection status section
	blocks = append(blocks, b.buildGitHubConnectionSection(user)...)

	blocks = append(blocks, slack.NewDividerBlock())

	// Default channel configuration section
	blocks = append(blocks, b.buildChannelConfigSection(user)...)

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
					fmt.Sprintf("*GitHub connection*\n✅ Connected as @%s\nStatus: Verified via OAuth", user.GitHubUsername),
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
				"*GitHub connection*\n❌ Not connected\nConnect your GitHub account to have the bot post your PRs for review",
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
	if user != nil && user.DefaultChannel != "" {
		// Channel set
		return []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject(slack.MarkdownType,
					fmt.Sprintf("*Default PR channel*\nCurrent: <#%s>", user.DefaultChannel),
					false, false),
				nil,
				slack.NewAccessory(
					slack.NewButtonBlockElement(
						"select_channel",
						"change_channel",
						slack.NewTextBlockObject(slack.PlainTextType, "Change channel", false, false),
					),
				),
			),
		}
	}

	// No channel set
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType,
				"*Default PR channel*\n:warning: No channel configured!",
				false, false),
			nil,
			slack.NewAccessory(
				slack.NewButtonBlockElement(
					"select_channel",
					"select_channel",
					slack.NewTextBlockObject(slack.PlainTextType, "Select channel", false, false),
				).WithStyle(slack.StylePrimary),
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
				slack.NewTextBlockObject(slack.PlainTextType, "🔄 Refresh", false, false),
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
					slack.NewTextBlockObject(slack.PlainTextType, "Select a channel", false, false),
					&slack.SelectBlockElement{
						Type:        slack.OptTypeChannels,
						ActionID:    "channel_select",
						Placeholder: slack.NewTextBlockObject(slack.PlainTextType, "Choose a public channel", false, false),
					},
				),
			},
		},
	}
}
