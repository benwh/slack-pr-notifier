package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/go-github/v74/github"

	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github-slack-notifier/internal/utils"
)

// ProcessReactionSyncJob processes a reaction sync job from the job system.
// Fetches PR details from GitHub, gets tracked messages, and syncs emoji reactions based on current state.
func (h *GitHubHandler) ProcessReactionSyncJob(ctx context.Context, job *models.Job) error {
	var reactionSyncJob models.ReactionSyncJob
	if err := json.Unmarshal(job.Payload, &reactionSyncJob); err != nil {
		return fmt.Errorf("failed to unmarshal reaction sync job: %w", err)
	}

	// Validate the reaction sync job
	if err := reactionSyncJob.Validate(); err != nil {
		return fmt.Errorf("invalid reaction sync job: %w", err)
	}

	ctx = log.WithFields(ctx, log.LogFields{
		"repo":                 reactionSyncJob.RepoFullName,
		"pr_number":            reactionSyncJob.PRNumber,
		"reaction_sync_job_id": reactionSyncJob.ID,
	})

	log.Debug(ctx, "Processing reaction sync job")

	// Fetch PR details and current review state from GitHub
	pr, currentReviewState, err := h.githubService.GetPullRequestWithReviews(
		ctx, reactionSyncJob.RepoFullName, reactionSyncJob.PRNumber,
	)
	if err != nil {
		log.Error(ctx, "Failed to fetch PR details from GitHub", "error", err)
		return fmt.Errorf("failed to fetch PR details: %w", err)
	}

	// Get all tracked messages for this PR across all workspaces
	trackedMessages, err := h.getAllTrackedMessagesForPR(ctx, reactionSyncJob.RepoFullName, reactionSyncJob.PRNumber)
	if err != nil {
		log.Error(ctx, "Failed to get tracked messages for reaction sync", "error", err)
		return err
	}

	if len(trackedMessages) == 0 {
		log.Warn(ctx, "No tracked messages found for reaction sync")
		return nil
	}

	// Convert tracked messages to message refs and group by team
	messagesByTeam := h.groupMessagesByTeam(trackedMessages)

	// Sync reactions based on current PR state
	return h.syncReactions(ctx, pr, currentReviewState, messagesByTeam, trackedMessages)
}

// groupMessagesByTeam groups tracked messages by Slack team ID for team-scoped API calls.
// Converts tracked messages to MessageRef format and organizes by team.
func (h *GitHubHandler) groupMessagesByTeam(trackedMessages []*models.TrackedMessage) map[string][]services.MessageRef {
	messageRefs := make([]services.MessageRef, len(trackedMessages))
	for i, msg := range trackedMessages {
		messageRefs[i] = services.MessageRef{
			Channel:   msg.SlackChannel,
			Timestamp: msg.SlackMessageTS,
		}
	}

	messagesByTeam := make(map[string][]services.MessageRef)
	for i, msg := range trackedMessages {
		messagesByTeam[msg.SlackTeamID] = append(messagesByTeam[msg.SlackTeamID], messageRefs[i])
	}

	return messagesByTeam
}

// syncReactions syncs emoji reactions for pull requests based on current state.
// For open PRs: removes PR state reactions, then syncs review reactions.
// For closed PRs: syncs review reactions, then adds closed/merged emoji.
func (h *GitHubHandler) syncReactions(
	ctx context.Context, pr *github.PullRequest, currentReviewState string,
	messagesByTeam map[string][]services.MessageRef, trackedMessages []*models.TrackedMessage,
) error {
	isClosed := pr.GetState() == "closed"

	for teamID, teamMessageRefs := range messagesByTeam {
		if isClosed {
			// For closed PRs: sync review reactions, then add closed/merged emoji
			err := h.slackService.SyncReviewReactions(ctx, teamID, teamMessageRefs, currentReviewState)
			if err != nil {
				log.Error(ctx, "Failed to sync review reactions for closed PR",
					"error", err,
					"team_id", teamID,
					"review_state", currentReviewState,
				)
			}

			// Add the appropriate closed/merged emoji
			emoji := utils.GetEmojiForPRState(PRActionClosed, pr.GetMerged(), h.emojiConfig)
			if emoji != "" {
				err = h.slackService.AddReactionToMultipleMessages(ctx, teamID, teamMessageRefs, emoji)
				if err != nil {
					log.Error(ctx, "Failed to add PR state reaction",
						"error", err,
						"team_id", teamID,
						"emoji", emoji,
						"merged", pr.GetMerged(),
					)
				}
			}
		} else {
			// For open PRs: remove any PR state reactions, then sync review reactions
			err := h.slackService.RemovePRStateReactions(ctx, teamID, teamMessageRefs)
			if err != nil {
				log.Error(ctx, "Failed to remove PR state reactions",
					"error", err,
					"team_id", teamID,
				)
			}

			err = h.slackService.SyncReviewReactions(ctx, teamID, teamMessageRefs, currentReviewState)
			if err != nil {
				log.Error(ctx, "Failed to sync review reactions for open PR",
					"error", err,
					"team_id", teamID,
					"review_state", currentReviewState,
				)
			}
		}
	}

	// Log final state
	if isClosed {
		log.Info(ctx, "PR is closed, synced reactions",
			"merged", pr.GetMerged(),
			"review_state", currentReviewState,
			"message_count", len(trackedMessages))
	} else {
		log.Info(ctx, "Reaction sync completed for open PR",
			"review_state", currentReviewState,
			"total_messages", len(trackedMessages),
			"workspace_count", len(messagesByTeam))
	}

	return nil
}
