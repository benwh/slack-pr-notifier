package handlers

import (
	"encoding/json"
	"testing"

	"github-slack-notifier/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteTrackedMessageJob_Validation tests the validation of DeleteTrackedMessageJob.
func TestDeleteTrackedMessageJob_Validation(t *testing.T) {
	t.Run("valid job", func(t *testing.T) {
		job := &models.DeleteTrackedMessageJob{
			ID:               "test-job-id",
			TrackedMessageID: "test-message-id",
			SlackChannel:     "C1234567890",
			SlackMessageTS:   "1234567890.123456",
			SlackTeamID:      "T1234567890",
			TraceID:          "test-trace-id",
		}

		err := job.Validate()
		assert.NoError(t, err)
	})

	t.Run("missing job ID", func(t *testing.T) {
		job := &models.DeleteTrackedMessageJob{
			TrackedMessageID: "test-message-id",
			SlackChannel:     "C1234567890",
			SlackMessageTS:   "1234567890.123456",
			SlackTeamID:      "T1234567890",
			TraceID:          "test-trace-id",
		}

		err := job.Validate()
		assert.EqualError(t, err, models.ErrJobIDRequired.Error())
	})

	t.Run("missing tracked message ID", func(t *testing.T) {
		job := &models.DeleteTrackedMessageJob{
			ID:             "test-job-id",
			SlackChannel:   "C1234567890",
			SlackMessageTS: "1234567890.123456",
			SlackTeamID:    "T1234567890",
			TraceID:        "test-trace-id",
		}

		err := job.Validate()
		assert.EqualError(t, err, models.ErrTrackedMessageIDRequired.Error())
	})

	t.Run("missing slack channel", func(t *testing.T) {
		job := &models.DeleteTrackedMessageJob{
			ID:               "test-job-id",
			TrackedMessageID: "test-message-id",
			SlackMessageTS:   "1234567890.123456",
			SlackTeamID:      "T1234567890",
			TraceID:          "test-trace-id",
		}

		err := job.Validate()
		assert.EqualError(t, err, models.ErrSlackChannelRequired.Error())
	})
}

// TestDeleteTrackedMessageJob_Serialization tests job serialization.
func TestDeleteTrackedMessageJob_Serialization(t *testing.T) {
	originalJob := &models.DeleteTrackedMessageJob{
		ID:               "test-job-id",
		TrackedMessageID: "test-message-id",
		SlackChannel:     "C1234567890",
		SlackMessageTS:   "1234567890.123456",
		SlackTeamID:      "T1234567890",
		TraceID:          "test-trace-id",
	}

	// Test marshaling
	data, err := json.Marshal(originalJob)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Test unmarshaling
	var deserializedJob models.DeleteTrackedMessageJob
	err = json.Unmarshal(data, &deserializedJob)
	require.NoError(t, err)
	assert.Equal(t, originalJob.ID, deserializedJob.ID)
	assert.Equal(t, originalJob.TrackedMessageID, deserializedJob.TrackedMessageID)
	assert.Equal(t, originalJob.SlackChannel, deserializedJob.SlackChannel)
	assert.Equal(t, originalJob.SlackMessageTS, deserializedJob.SlackMessageTS)
	assert.Equal(t, originalJob.SlackTeamID, deserializedJob.SlackTeamID)
	assert.Equal(t, originalJob.TraceID, deserializedJob.TraceID)
}

// TestTrackedMessage_DeletedByUserField tests the new DeletedByUser field.
func TestTrackedMessage_DeletedByUserField(t *testing.T) {
	message := &models.TrackedMessage{
		ID:            "test-id",
		PRNumber:      123,
		RepoFullName:  "test-owner/test-repo",
		SlackChannel:  "C1234567890",
		SlackTeamID:   "T1234567890",
		MessageSource: models.MessageSourceBot,
		DeletedByUser: false, // Default value
	}

	assert.False(t, message.DeletedByUser)

	// Test setting the field
	message.DeletedByUser = true
	assert.True(t, message.DeletedByUser)
}

// TestMessageSourceConstants tests the new message source constants.
func TestMessageSourceConstants(t *testing.T) {
	assert.Equal(t, "bot", models.MessageSourceBot)
	assert.Equal(t, "manual", models.MessageSourceManual)
}

// TestJobTypeConstants tests the new job type constant.
func TestJobTypeConstants(t *testing.T) {
	assert.Equal(t, "delete_tracked_message", models.JobTypeDeleteTrackedMessage)
}
