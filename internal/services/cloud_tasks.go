package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	cloudtaskspb "cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"github-slack-notifier/internal/models"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type CloudTasksService struct {
	client    *cloudtasks.Client
	projectID string
	location  string
	queueName string
	config    *config.Config
}

type CloudTasksConfig struct {
	ProjectID  string
	Location   string
	QueueName  string
	Config     *config.Config
	HTTPClient *http.Client // Optional: custom HTTP client for testing
}

func NewCloudTasksService(config CloudTasksConfig) (*CloudTasksService, error) {
	ctx := context.Background()

	// Create client options
	var opts []option.ClientOption
	if config.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(config.HTTPClient))
	}

	client, err := cloudtasks.NewClient(ctx, opts...)
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
		config:    config.Config,
	}, nil
}

func (cts *CloudTasksService) Close() error {
	return cts.client.Close()
}

// EnqueueJob enqueues a job for processing.
func (cts *CloudTasksService) EnqueueJob(ctx context.Context, job *models.Job) error {
	if err := job.Validate(); err != nil {
		log.Error(ctx, "Invalid job for Cloud Tasks",
			"error", err,
			"job_id", job.ID,
			"job_type", job.Type,
			"operation", "validate_job",
		)
		return fmt.Errorf("invalid job: %w", err)
	}

	payload, err := json.Marshal(job)
	if err != nil {
		log.Error(ctx, "Failed to marshal job for Cloud Tasks",
			"error", err,
			"job_id", job.ID,
			"job_type", job.Type,
			"operation", "marshal_job",
		)
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	queuePath := fmt.Sprintf("projects/%s/locations/%s/queues/%s",
		cts.projectID, cts.location, cts.queueName)

	task := &cloudtaskspb.Task{
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        cts.config.JobProcessorURL(),
				Headers: map[string]string{
					"Content-Type":         "application/json",
					"X-Job-ID":             job.ID,
					"X-Trace-ID":           job.TraceID,
					"X-Cloud-Tasks-Secret": cts.config.CloudTasksSecret,
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
		log.Error(ctx, "Failed to create job processing task",
			"error", err,
			"job_id", job.ID,
			"job_type", job.Type,
			"queue_path", queuePath,
			"worker_url", cts.config.JobProcessorURL(),
			"operation", "create_job_task",
		)
		return fmt.Errorf("failed to create task: %w", err)
	}

	log.Info(ctx, "Job queued",
		"job_id", job.ID,
		"job_type", job.Type,
		"task_name", createdTask.GetName(),
	)

	return nil
}
