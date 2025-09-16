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

func TestShouldReplaceReviewState(t *testing.T) {
	tests := []struct {
		name          string
		existingState string
		newState      string
		shouldReplace bool
		description   string
	}{
		{
			name:          "Changes requested should replace approved",
			existingState: string(models.ReviewStateApproved),
			newState:      string(models.ReviewStateChangesRequested),
			shouldReplace: true,
			description:   "Changes requested has higher priority than approved",
		},
		{
			name:          "Changes requested should replace commented",
			existingState: string(models.ReviewStateCommented),
			newState:      string(models.ReviewStateChangesRequested),
			shouldReplace: true,
			description:   "Changes requested has higher priority than commented",
		},
		{
			name:          "Approved should replace commented",
			existingState: string(models.ReviewStateCommented),
			newState:      string(models.ReviewStateApproved),
			shouldReplace: true,
			description:   "Approved has higher priority than commented",
		},
		{
			name:          "Approved should NOT replace changes requested",
			existingState: string(models.ReviewStateChangesRequested),
			newState:      string(models.ReviewStateApproved),
			shouldReplace: false,
			description:   "Approved has lower priority than changes requested",
		},
		{
			name:          "Commented should NOT replace approved",
			existingState: string(models.ReviewStateApproved),
			newState:      string(models.ReviewStateCommented),
			shouldReplace: false,
			description:   "Commented has lower priority than approved",
		},
		{
			name:          "Commented should NOT replace changes requested",
			existingState: string(models.ReviewStateChangesRequested),
			newState:      string(models.ReviewStateCommented),
			shouldReplace: false,
			description:   "Commented has lower priority than changes requested",
		},
		{
			name:          "Same state should not replace",
			existingState: string(models.ReviewStateApproved),
			newState:      string(models.ReviewStateApproved),
			shouldReplace: false,
			description:   "Same priority states should not replace each other",
		},
		{
			name:          "Any state should replace dismissed",
			existingState: string(models.ReviewStateDismissed),
			newState:      string(models.ReviewStateCommented),
			shouldReplace: true,
			description:   "Any review state should have higher priority than dismissed",
		},
		{
			name:          "Dismissed should NOT replace any active state",
			existingState: string(models.ReviewStateCommented),
			newState:      string(models.ReviewStateDismissed),
			shouldReplace: false,
			description:   "Dismissed has lowest priority",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldReplaceReviewState(tt.existingState, tt.newState)
			assert.Equal(t, tt.shouldReplace, result, tt.description)
		})
	}
}

func TestDetermineOverallReviewState_MultipleStatesPerUser(t *testing.T) {
	tests := []struct {
		name             string
		userReviewStates map[int64]string
		prAuthorID       int64
		expectedState    string
		description      string
	}{
		{
			name: "User with approval should show approved (not comment)",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateApproved), // This would be the result after priority logic
			},
			prAuthorID:    200001, // Different user is PR author
			expectedState: string(models.ReviewStateApproved),
			description:   "When user has both approved and commented, approval should take precedence",
		},
		{
			name: "User with changes requested should show changes_requested",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateChangesRequested), // This would be the result after priority logic
			},
			prAuthorID:    200001,
			expectedState: string(models.ReviewStateChangesRequested),
			description:   "Changes requested should take precedence over other states",
		},
		{
			name: "Multiple users with mixed states - changes requested wins",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateApproved),         // User 1 approved
				200001: string(models.ReviewStateChangesRequested), // User 2 requested changes
				300001: string(models.ReviewStateCommented),        // User 3 commented
			},
			prAuthorID:    400001,
			expectedState: string(models.ReviewStateChangesRequested),
			description:   "When multiple users have different states, changes_requested has highest priority",
		},
		{
			name: "Multiple users with mixed states - approved wins over comment",
			userReviewStates: map[int64]string{
				100001: string(models.ReviewStateApproved),  // User 1 approved
				200001: string(models.ReviewStateCommented), // User 2 commented
			},
			prAuthorID:    300001,
			expectedState: string(models.ReviewStateApproved),
			description:   "When no changes requested, approved takes precedence over commented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineOverallReviewState(tt.userReviewStates, tt.prAuthorID)
			assert.Equal(t, tt.expectedState, result, tt.description)
		})
	}
}
