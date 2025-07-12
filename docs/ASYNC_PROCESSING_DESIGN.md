# Async Webhook Processing Design Specification

## Overview

This document outlines the design for implementing asynchronous webhook processing using Google Cloud Tasks to replace the current synchronous processing model. This addresses the critical reliability issue where GitHub's 10-second webhook timeout can cause dropped webhooks.

## Problem Statement

### Current Issues
- **Timeout Risk**: GitHub webhooks must be acknowledged within 10 seconds or they're marked as failed
- **Blocking Operations**: Current synchronous flow includes:
  - Firestore database queries (~100-500ms)
  - Slack API calls (~200-1000ms)
  - Multiple external API calls can compound to exceed 10s timeout
- **No Retry Logic**: Failed webhooks are lost permanently
- **Poor Observability**: No insight into processing status or failures

### Business Impact
- **Missed Notifications**: PR events may not reach Slack channels
- **Poor Developer Experience**: Inconsistent notification delivery
- **Operational Overhead**: Manual investigation required when webhooks fail

## Solution Design

### Architecture Overview

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  GitHub Webhook │───▶│  Cloud Run      │───▶│  Cloud Tasks    │───▶│  Cloud Run      │
│                 │    │  (Ingress)      │    │  Queue          │    │  (Worker)       │
└─────────────────┘    └─────────────────┘    └─────────────────┘    └─────────────────┘
                            │                                              │
                            │ ◀50ms response                              │
                            │                                              ▼
                            │                                       ┌─────────────────┐
                            │                                       │  Firestore      │
                            │                                       │  Slack API      │
                            │                                       │  Business Logic │
                            │                                       └─────────────────┘
```

### Components

#### 1. Webhook Ingress Handler (Fast Path)
**Responsibility**: Quickly validate and queue webhooks
**Response Time**: < 100ms
**Error Handling**: Return 4xx/5xx for invalid requests

#### 2. Cloud Tasks Queue
**Responsibility**: Reliable job queuing with retry logic
**Features**: 
- Automatic retry with exponential backoff
- Dead letter queue for failed jobs
- Rate limiting and throttling

#### 3. Webhook Worker Handler (Slow Path)
**Responsibility**: Process webhook business logic
**Response Time**: < 5 minutes (configurable)
**Error Handling**: Return 5xx for retryable failures, 2xx for success

## Implementation Specification

### 1. Data Models

```go
// models/webhook_job.go
type WebhookJob struct {
    ID            string    `json:"id"`
    EventType     string    `json:"event_type"`
    DeliveryID    string    `json:"delivery_id"`
    CorrelationID string    `json:"correlation_id"`
    Payload       []byte    `json:"payload"`
    ReceivedAt    time.Time `json:"received_at"`
    ProcessedAt   *time.Time `json:"processed_at,omitempty"`
    Status        string    `json:"status"` // queued, processing, completed, failed
    RetryCount    int       `json:"retry_count"`
    LastError     string    `json:"last_error,omitempty"`
}

// Validation for webhook job
func (wj *WebhookJob) Validate() error {
    if wj.ID == "" {
        return errors.New("job ID is required")
    }
    if wj.EventType == "" {
        return errors.New("event type is required")
    }
    if len(wj.Payload) == 0 {
        return errors.New("payload is required")
    }
    return nil
}
```

### 2. Cloud Tasks Service

```go
// services/cloud_tasks.go
type CloudTasksService struct {
    client     *cloudtasks.Client
    projectID  string
    location   string
    queueName  string
    workerURL  string
}

type CloudTasksConfig struct {
    ProjectID string `env:"GOOGLE_CLOUD_PROJECT,required"`
    Location  string `env:"CLOUD_TASKS_LOCATION" envDefault:"us-central1"`
    QueueName string `env:"CLOUD_TASKS_QUEUE" envDefault:"webhook-processing"`
    WorkerURL string `env:"WEBHOOK_WORKER_URL,required"`
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

func (cts *CloudTasksService) EnqueueWebhook(ctx context.Context, job *WebhookJob) error {
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
                    "Content-Type":       "application/json",
                    "X-Job-ID":          job.ID,
                    "X-Correlation-ID":  job.CorrelationID,
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
        "task_name", createdTask.Name,
        "event_type", job.EventType,
    )
    
    return nil
}
```

### 3. Updated Webhook Handler (Ingress)

```go
// handlers/github_async.go
type GitHubAsyncHandler struct {
    cloudTasksService *services.CloudTasksService
    validationService *services.ValidationService
    webhookSecret     string
}

func NewGitHubAsyncHandler(
    cloudTasksService *services.CloudTasksService,
    validationService *services.ValidationService,
    webhookSecret string,
) *GitHubAsyncHandler {
    return &GitHubAsyncHandler{
        cloudTasksService: cloudTasksService,
        validationService: validationService,
        webhookSecret:     webhookSecret,
    }
}

func (h *GitHubAsyncHandler) HandleWebhook(c *gin.Context) {
    startTime := time.Now()
    correlationID := c.GetString("correlation_id")
    
    logger := slog.With(
        "correlation_id", correlationID,
        "remote_addr", c.ClientIP(),
        "user_agent", c.Request.UserAgent(),
    )
    
    // 1. Fast validation (< 10ms)
    eventType := c.GetHeader("X-GitHub-Event")
    deliveryID := c.GetHeader("X-GitHub-Delivery")
    
    if eventType == "" || deliveryID == "" {
        logger.Error("Missing required headers")
        c.JSON(400, gin.H{"error": "missing required headers"})
        return
    }
    
    // 2. Signature validation (< 20ms)
    if !h.validateSignature(c) {
        logger.Error("Invalid webhook signature")
        c.JSON(401, gin.H{"error": "invalid signature"})
        return
    }
    
    // 3. Read payload (< 10ms)
    body, err := io.ReadAll(c.Request.Body)
    if err != nil {
        logger.Error("Failed to read request body", "error", err)
        c.JSON(400, gin.H{"error": "failed to read body"})
        return
    }
    
    // 4. Basic payload validation (< 20ms)
    if err := h.validationService.ValidateWebhookPayload(eventType, body); err != nil {
        logger.Error("Invalid webhook payload", "error", err, "event_type", eventType)
        c.JSON(400, gin.H{"error": "invalid payload"})
        return
    }
    
    // 5. Create job (< 5ms)
    job := &models.WebhookJob{
        ID:            uuid.New().String(),
        EventType:     eventType,
        DeliveryID:    deliveryID,
        CorrelationID: correlationID,
        Payload:       body,
        ReceivedAt:    time.Now(),
        Status:        "queued",
        RetryCount:    0,
    }
    
    // 6. Queue job (< 50ms)
    if err := h.cloudTasksService.EnqueueWebhook(c.Request.Context(), job); err != nil {
        logger.Error("Failed to enqueue webhook", "error", err)
        c.JSON(500, gin.H{"error": "failed to queue webhook"})
        return
    }
    
    // 7. Respond immediately (< 100ms total)
    processingTime := time.Since(startTime)
    logger.Info("Webhook queued successfully",
        "job_id", job.ID,
        "event_type", eventType,
        "processing_time_ms", processingTime.Milliseconds(),
    )
    
    c.JSON(200, gin.H{
        "status": "queued",
        "job_id": job.ID,
        "processing_time_ms": processingTime.Milliseconds(),
    })
}

func (h *GitHubAsyncHandler) validateSignature(c *gin.Context) bool {
    signature := c.GetHeader("X-Hub-Signature-256")
    if signature == "" {
        return false
    }
    
    body, err := io.ReadAll(c.Request.Body)
    if err != nil {
        return false
    }
    
    // Reset body for further reading
    c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
    
    expectedSignature := "sha256=" + computeHMAC256(body, h.webhookSecret)
    return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
```

### 4. Webhook Worker Handler

```go
// handlers/webhook_worker.go
type WebhookWorkerHandler struct {
    firestoreService *services.FirestoreService
    slackService     *services.SlackService
    maxProcessingTime time.Duration
}

func NewWebhookWorkerHandler(
    firestoreService *services.FirestoreService,
    slackService *services.SlackService,
) *WebhookWorkerHandler {
    return &WebhookWorkerHandler{
        firestoreService:  firestoreService,
        slackService:      slackService,
        maxProcessingTime: 5 * time.Minute,
    }
}

func (h *WebhookWorkerHandler) ProcessWebhook(c *gin.Context) {
    startTime := time.Now()
    
    // Parse job from Cloud Tasks
    var job models.WebhookJob
    if err := c.ShouldBindJSON(&job); err != nil {
        slog.Error("Invalid job payload", "error", err)
        c.JSON(400, gin.H{"error": "invalid job payload"})
        return
    }
    
    logger := slog.With(
        "job_id", job.ID,
        "event_type", job.EventType,
        "correlation_id", job.CorrelationID,
        "retry_count", job.RetryCount,
    )
    
    logger.Info("Processing webhook job")
    
    // Set processing timeout
    ctx, cancel := context.WithTimeout(c.Request.Context(), h.maxProcessingTime)
    defer cancel()
    
    // Process the webhook
    if err := h.processWebhookPayload(ctx, &job); err != nil {
        processingTime := time.Since(startTime)
        logger.Error("Failed to process webhook",
            "error", err,
            "processing_time_ms", processingTime.Milliseconds(),
        )
        
        // Return 5xx for retryable errors (Cloud Tasks will retry)
        // Return 4xx for non-retryable errors (Cloud Tasks will not retry)
        if isRetryableError(err) {
            c.JSON(500, gin.H{
                "error": "processing failed",
                "retryable": true,
                "processing_time_ms": processingTime.Milliseconds(),
            })
        } else {
            c.JSON(400, gin.H{
                "error": "processing failed",
                "retryable": false,
                "processing_time_ms": processingTime.Milliseconds(),
            })
        }
        return
    }
    
    processingTime := time.Since(startTime)
    logger.Info("Webhook processed successfully",
        "processing_time_ms", processingTime.Milliseconds(),
    )
    
    c.JSON(200, gin.H{
        "status": "processed",
        "processing_time_ms": processingTime.Milliseconds(),
    })
}

func (h *WebhookWorkerHandler) processWebhookPayload(ctx context.Context, job *models.WebhookJob) error {
    // Delegate to existing business logic
    switch job.EventType {
    case "pull_request":
        return h.processPullRequestEvent(ctx, job)
    case "pull_request_review":
        return h.processPullRequestReviewEvent(ctx, job)
    default:
        return fmt.Errorf("unsupported event type: %s", job.EventType)
    }
}

func isRetryableError(err error) bool {
    // Define which errors should be retried
    // Network errors, temporary service unavailability, etc.
    if errors.Is(err, context.DeadlineExceeded) {
        return true
    }
    
    // Check for specific Slack/Firestore errors that indicate temporary issues
    var slackErr *slack.RateLimitedError
    if errors.As(err, &slackErr) {
        return true
    }
    
    // Add more retryable error checks as needed
    return false
}
```

### 5. Configuration and Environment Variables

```go
// Add to .env.example
CLOUD_TASKS_LOCATION=us-central1
CLOUD_TASKS_QUEUE=webhook-processing
WEBHOOK_WORKER_URL=https://your-service-url.run.app/process-webhook
```

### 6. Router Updates

```go
// main.go - Add new routes
func main() {
    // ... existing setup ...
    
    // Webhook ingress (fast path)
    router.POST("/webhooks/github", app.githubAsyncHandler.HandleWebhook)
    
    // Worker endpoint (slow path)
    router.POST("/process-webhook", app.webhookWorkerHandler.ProcessWebhook)
    
    // ... existing routes ...
}
```

## Infrastructure Requirements

### 1. Cloud Tasks Queue Configuration

```yaml
# terraform/cloud-tasks.tf
resource "google_cloud_tasks_queue" "webhook_processing" {
  name         = "webhook-processing"
  location     = "us-central1"
  project      = var.project_id
  
  retry_config {
    max_attempts       = 5
    max_retry_duration = "300s"
    min_backoff        = "1s"
    max_backoff        = "30s"
    max_doublings      = 5
  }
  
  rate_limits {
    max_concurrent_dispatches = 10
    max_dispatches_per_second = 100
  }
  
  # Dead letter queue for failed jobs
  stackdriver_logging_config {
    sampling_ratio = 1.0
  }
}
```

### 2. IAM Permissions

```yaml
# Cloud Run service account needs:
roles/cloudtasks.enqueuer    # To create tasks
roles/cloudtasks.viewer      # To view queue status
roles/logging.logWriter      # For structured logging
roles/monitoring.metricWriter # For metrics
```

### 3. Cloud Run Configuration

```yaml
# deployment.yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: github-slack-notifier
spec:
  template:
    metadata:
      annotations:
        autoscaling.knative.dev/minScale: "1"
        autoscaling.knative.dev/maxScale: "10"
        run.googleapis.com/cpu: "1000m"
        run.googleapis.com/memory: "512Mi"
        run.googleapis.com/execution-environment: gen2
    spec:
      containers:
      - image: gcr.io/PROJECT/github-slack-notifier
        env:
        - name: CLOUD_TASKS_LOCATION
          value: "us-central1"
        - name: CLOUD_TASKS_QUEUE
          value: "webhook-processing"
        - name: WEBHOOK_WORKER_URL
          value: "https://github-slack-notifier-xxxx.a.run.app/process-webhook"
```

## Migration Strategy

### Phase 1: Feature Flag Implementation
- Add feature flag `ENABLE_ASYNC_PROCESSING=false`
- Implement both sync and async handlers
- Test async processing in development

### Phase 2: Gradual Rollout
- Enable async processing for specific event types
- Monitor performance and error rates
- Gradually increase traffic to async handler

### Phase 3: Full Migration
- Switch all webhook processing to async
- Remove synchronous processing code
- Update monitoring and alerting

## Monitoring and Observability

### 1. Key Metrics
- **Webhook Response Time**: Time from receipt to queue acknowledgment
- **Queue Depth**: Number of pending jobs
- **Processing Time**: Time to complete webhook processing
- **Error Rate**: Percentage of failed webhook processing
- **Retry Rate**: Number of jobs requiring retry

### 2. Alerting
- Queue depth > 100 jobs
- Error rate > 5%
- Average processing time > 30 seconds
- Webhook response time > 1 second

### 3. Logging
- Structured logs with correlation IDs
- Job lifecycle tracking (queued → processing → completed/failed)
- Performance metrics for each processing stage

## Testing Strategy

### 1. Unit Tests
- Test webhook validation logic
- Test job creation and queuing
- Test error handling scenarios

### 2. Integration Tests
- End-to-end webhook processing
- Cloud Tasks integration testing
- Retry logic verification

### 3. Load Testing
- Simulate high webhook volume
- Test queue performance under load
- Verify auto-scaling behavior

## Rollback Plan

### Emergency Rollback
1. Set feature flag `ENABLE_ASYNC_PROCESSING=false`
2. Deploy previous version if needed
3. Monitor for webhook processing recovery

### Gradual Rollback
1. Reduce traffic to async handler
2. Increase traffic to sync handler
3. Monitor performance metrics

## Success Criteria

### Performance Targets
- **Webhook Response Time**: < 100ms (95th percentile)
- **Processing Success Rate**: > 99%
- **Processing Time**: < 30 seconds (95th percentile)
- **Zero Missed Webhooks**: Due to timeout issues

### Operational Targets
- **Queue Depth**: < 50 jobs during normal operation
- **Error Rate**: < 1%
- **Retry Rate**: < 5%

## Future Enhancements

### 1. Advanced Retry Logic
- Exponential backoff with jitter
- Dead letter queue processing
- Manual retry mechanisms

### 2. Webhook Replay
- Store webhook payloads for replay
- Administrative interface for reprocessing
- Bulk replay capabilities

### 3. Advanced Monitoring
- Real-time dashboard for webhook processing
- Webhook processing analytics
- Performance trend analysis

---

## Implementation Timeline

| Phase | Duration | Tasks |
|-------|----------|-------|
| Phase 1 | 1 week | Implement Cloud Tasks service, basic async handler |
| Phase 2 | 1 week | Add worker handler, testing, monitoring |
| Phase 3 | 1 week | Feature flag, gradual rollout, documentation |
| Phase 4 | 1 week | Full migration, cleanup, optimization |

**Total Estimated Time: 4 weeks**

This design provides a robust, scalable solution for webhook processing that addresses the current reliability issues while providing a foundation for future enhancements.