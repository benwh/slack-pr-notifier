package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"github-slack-notifier/internal/services"
	"github-slack-notifier/internal/utils"
)

// ProcessReactionSyncJob processes a reaction sync job from the job system.
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

	// Handle closed PR state
	if pr.GetState() == "closed" {
		return h.syncClosedPRReactions(ctx, pr, messagesByTeam, trackedMessages)
	}

	// Handle open PR review state
	return h.syncOpenPRReactions(ctx, currentReviewState, messagesByTeam, trackedMessages)
}

// groupMessagesByTeam groups tracked messages by team ID.
// TODO: could be replaced by `lo.GroupBy` or similar
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

// syncClosedPRReactions syncs reactions for closed PRs.
func (h *GitHubHandler) syncClosedPRReactions(
	ctx context.Context, pr interface{ GetMerged() bool },
	messagesByTeam map[string][]services.MessageRef, trackedMessages []*models.TrackedMessage,
) error {
	emoji := utils.GetEmojiForPRState(PRActionClosed, pr.GetMerged(), h.emojiConfig)
	if emoji == "" {
		return nil
	}

	// Add reactions for each team
	for teamID, teamMessageRefs := range messagesByTeam {
		err := h.slackService.AddReactionToMultipleMessages(ctx, teamID, teamMessageRefs, emoji)
		if err != nil {
			log.Error(ctx, "Failed to add closed PR reactions for team",
				"error", err,
				"team_id", teamID,
				"emoji", emoji,
				"merged", pr.GetMerged(),
			)
		}
	}

	log.Info(ctx, "PR is closed, synced closed state reactions",
		"merged", pr.GetMerged(),
		"message_count", len(trackedMessages))

	return nil
}

// syncOpenPRReactions syncs reactions for open PRs.
func (h *GitHubHandler) syncOpenPRReactions(
	ctx context.Context, currentReviewState string,
	messagesByTeam map[string][]services.MessageRef, trackedMessages []*models.TrackedMessage,
) error {
	// Sync reactions for each team separately
	for teamID, teamMessageRefs := range messagesByTeam {
		err := h.slackService.SyncAllReviewReactions(ctx, teamID, teamMessageRefs, currentReviewState)
		if err != nil {
			log.Error(ctx, "Failed to sync review reactions for team",
				"error", err,
				"team_id", teamID,
				"review_state", currentReviewState,
				"message_count", len(teamMessageRefs),
			)
			// Continue with other teams even if one fails
		}
	}

	log.Info(ctx, "Reaction sync completed",
		"review_state", currentReviewState,
		"total_messages", len(trackedMessages),
		"workspace_count", len(messagesByTeam))

	return nil
}
