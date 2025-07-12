package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	cloudtaskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
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
		return fmt.Errorf("invalid job: %w", err)
	}

	payload, err := json.Marshal(job)
	if err != nil {
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
		return fmt.Errorf("failed to create task: %w", err)
	}

	slog.Info("Webhook job queued",
		"job_id", job.ID,
		"task_name", createdTask.GetName(),
		"event_type", job.EventType,
	)

	return nil
}
