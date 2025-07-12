package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	cloudtaskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CloudTasksService struct {
	client    *cloudtasks.Client
	projectID string
	location  string
	queueName string
	workerURL string
}

type CloudTasksConfig struct {
	ProjectID string
	Location  string
	QueueName string
	WorkerURL string
}

func NewCloudTasksService(config CloudTasksConfig) (*CloudTasksService, error) {
	ctx := context.Background()
	client, err := cloudtasks.NewClient(ctx)
	if err != nil {
		log.Error(ctx, "Failed to create Cloud Tasks client",
			"error", err,
			"project_id", config.ProjectID,
			"location", config.Location,
			"queue_name", config.QueueName,
			"operation", "create_cloud_tasks_client",
		)
		return nil, fmt.Errorf("failed to create Cloud Tasks client: %w", err)
	}

	return &CloudTasksService{
		client:    client,
		projectID: config.ProjectID,
		location:  config.Location,
		queueName: config.QueueName,
		workerURL: config.WorkerURL,
	}, nil
}

func (cts *CloudTasksService) Close() error {
	return cts.client.Close()
}

func (cts *CloudTasksService) EnqueueWebhook(ctx context.Context, job *models.WebhookJob) error {
	if err := job.Validate(); err != nil {
		log.Error(ctx, "Invalid webhook job for Cloud Tasks",
			"error", err,
			"job_id", job.ID,
			"event_type", job.EventType,
			"operation", "validate_webhook_job",
		)
		return fmt.Errorf("invalid job: %w", err)
	}

	payload, err := json.Marshal(job)
	if err != nil {
		log.Error(ctx, "Failed to marshal webhook job for Cloud Tasks",
			"error", err,
			"job_id", job.ID,
			"event_type", job.EventType,
			"operation", "marshal_webhook_job",
		)
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	queuePath := fmt.Sprintf("projects/%s/locations/%s/queues/%s",
		cts.projectID, cts.location, cts.queueName)

	task := &cloudtaskspb.Task{
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        cts.workerURL,
				Headers: map[string]string{
					"Content-Type": "application/json",
					"X-Job-ID":     job.ID,
					"X-Trace-ID":   job.TraceID,
				},
				Body: payload,
			},
		},
		ScheduleTime: timestamppb.Now(),
	}

	req := &cloudtaskspb.CreateTaskRequest{
		Parent: queuePath,
		Task:   task,
	}

	createdTask, err := cts.client.CreateTask(ctx, req)
	if err != nil {
		log.Error(ctx, "Failed to create Cloud Tasks task",
			"error", err,
			"job_id", job.ID,
			"event_type", job.EventType,
			"queue_path", queuePath,
			"worker_url", cts.workerURL,
			"operation", "create_cloud_tasks_task",
		)
		return fmt.Errorf("failed to create task: %w", err)
	}

	slog.Info("Webhook job queued",
		"job_id", job.ID,
		"task_name", createdTask.GetName(),
		"event_type", job.EventType,
	)

	return nil
}
