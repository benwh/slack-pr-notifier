package models

import (
	"testing"
)

func TestTrackedMessage_MigrateUserToCC(t *testing.T) {
	tests := []struct {
		name          string
		before        *TrackedMessage
		expectedAfter *TrackedMessage
		description   string
	}{
		{
			name: "migrate single user from old field",
			before: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "john.doe",
				UsersToCC: nil,
			},
			expectedAfter: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: []string{"john.doe"},
			},
			description: "Should migrate single user from UserToCC to UsersToCC array",
		},
		{
			name: "no migration needed - new field already populated",
			before: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "old.user",
				UsersToCC: []string{"new.user1", "new.user2"},
			},
			expectedAfter: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: []string{"new.user1", "new.user2"},
			},
			description: "Should not migrate when UsersToCC already has data",
		},
		{
			name: "no migration needed - no old field",
			before: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: []string{"user1", "user2"},
			},
			expectedAfter: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: []string{"user1", "user2"},
			},
			description: "Should not change anything when no old field data",
		},
		{
			name: "no migration needed - both fields empty",
			before: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: nil,
			},
			expectedAfter: &TrackedMessage{
				ID:        "test-id",
				UserToCC:  "",
				UsersToCC: nil,
			},
			description: "Should not change anything when both fields are empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			tt.before.MigrateUserToCC()

			// Assert
			if tt.before.UserToCC != tt.expectedAfter.UserToCC {
				t.Errorf("UserToCC = %v, want %v", tt.before.UserToCC, tt.expectedAfter.UserToCC)
			}

			if len(tt.before.UsersToCC) != len(tt.expectedAfter.UsersToCC) {
				t.Errorf("UsersToCC length = %d, want %d", len(tt.before.UsersToCC), len(tt.expectedAfter.UsersToCC))
				return
			}

			for i, user := range tt.before.UsersToCC {
				if user != tt.expectedAfter.UsersToCC[i] {
					t.Errorf("UsersToCC[%d] = %v, want %v", i, user, tt.expectedAfter.UsersToCC[i])
				}
			}
		})
	}
}
