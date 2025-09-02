package services

import (
	"testing"

	"github-slack-notifier/internal/models"

	"github.com/stretchr/testify/assert"
)

func TestDetermineOverallReviewState_PRAuthorCommentFiltering(t *testing.T) {
	tests := []struct {
		name             string
		userReviewStates map[int64]string
		prAuthorID       int64
		expectedState    string
		description      string
	}{
		{
			name: "PR author comments only - should return empty",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateCommented), // PR author
			},
			prAuthorID:    100001,
			expectedState: "",
			description:   "When only PR author comments, no reaction should be shown",
		},
		{
			name: "Other user comments only - should return commented",
			userReviewStates: map[int64]string{
				200001: string(models.ReviewStateCommented), // Other user
			},
			prAuthorID:    100001,
			expectedState: string(models.ReviewStateCommented),
			description:   "When other users comment, commented reaction should be shown",
		},
		{
			name: "Both PR author and other user comment - should return commented",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateCommented), // PR author
				200001: string(models.ReviewStateCommented), // Other user
			},
			prAuthorID:    100001,
			expectedState: string(models.ReviewStateCommented),
			description:   "When both PR author and others comment, commented reaction should be shown",
		},
		{
			name: "PR author approves - should return approved",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateApproved), // PR author
			},
			prAuthorID:    100001,
			expectedState: string(models.ReviewStateApproved),
			description:   "PR author's approval should still show approved reaction",
		},
		{
			name: "PR author requests changes - should return changes_requested",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateChangesRequested), // PR author
			},
			prAuthorID:    100001,
			expectedState: string(models.ReviewStateChangesRequested),
			description:   "PR author's changes request should still show changes_requested reaction",
		},
		{
			name: "Mixed reviews with PR author comment - priority should work correctly",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateCommented), // PR author (should be filtered)
				200001: string(models.ReviewStateApproved),  // Other user
				300001: string(models.ReviewStateCommented), // Another user
			},
			prAuthorID:    100001,
			expectedState: string(models.ReviewStateApproved),
			description:   "Priority should work: approved > commented, with PR author comments filtered",
		},
		{
			name:             "No reviews",
			userReviewStates: map[int64]string{},
			prAuthorID:       100001,
			expectedState:    "",
			description:      "No reviews should return empty state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineOverallReviewState(tt.userReviewStates, tt.prAuthorID)
			assert.Equal(t, tt.expectedState, result, tt.description)
		})
	}
}
