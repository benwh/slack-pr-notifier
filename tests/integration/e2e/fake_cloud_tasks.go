package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github-slack-notifier/internal/models"
)

// FakeCloudTasksService implements CloudTasksServiceInterface for testing.
// It immediately executes tasks by making HTTP requests back to the application.
type FakeCloudTasksService struct {
	baseURL        string
	secret         string
	client         *http.Client
	executedJobs   []*models.Job
	mu             sync.Mutex
	executeAsync   bool
	executionDelay time.Duration
}

// NewFakeCloudTasksService creates a new fake Cloud Tasks service.
func NewFakeCloudTasksService(baseURL, secret string) *FakeCloudTasksService {
	return &FakeCloudTasksService{
		baseURL:        baseURL,
		secret:         secret,
		client:         &http.Client{Timeout: 30 * time.Second},
		executedJobs:   make([]*models.Job, 0),
		executeAsync:   false, // By default, execute synchronously for predictable tests
		executionDelay: 0,
	}
}

// SetAsync configures whether jobs should be executed asynchronously.
func (f *FakeCloudTasksService) SetAsync(async bool, delay time.Duration) {
	f.executeAsync = async
	f.executionDelay = delay
}

// EnqueueJob implements CloudTasksServiceInterface.
// Instead of queueing to real Cloud Tasks, it immediately calls the job processor endpoint.
func (f *FakeCloudTasksService) EnqueueJob(ctx context.Context, job *models.Job) error {
	// Validate job
	if err := job.Validate(); err != nil {
		return fmt.Errorf("invalid job: %w", err)
	}

	// Record that we executed this job
	f.mu.Lock()
	f.executedJobs = append(f.executedJobs, job)
	f.mu.Unlock()

	// Execute the job by calling back to the application
	if f.executeAsync {
		go func() {
			_ = f.executeJob(job)
		}()
	} else {
		return f.executeJob(job)
	}

	return nil
}

// executeJob makes an HTTP request to the job processor endpoint.
func (f *FakeCloudTasksService) executeJob(job *models.Job) error {
	// Simulate any configured delay
	if f.executionDelay > 0 {
		time.Sleep(f.executionDelay)
	}

	// Marshal job to JSON
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	// Create request to job processor endpoint
	url := f.baseURL + "/jobs/process"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to match what Cloud Tasks would send
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Job-Id", job.ID)
	req.Header.Set("X-Trace-Id", job.TraceID)
	req.Header.Set("X-Cloud-Tasks-Secret", f.secret)

	// Execute the request
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute job: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		// Read response body for debugging
		body, _ := io.ReadAll(resp.Body)
		//nolint:err113 // Test code, dynamic error is fine
		return fmt.Errorf("job processor returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Close implements CloudTasksServiceInterface.
func (f *FakeCloudTasksService) Close() error {
	// Nothing to close in fake implementation
	return nil
}

// GetExecutedJobs returns all jobs that have been executed.
func (f *FakeCloudTasksService) GetExecutedJobs() []*models.Job {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Return a copy to avoid race conditions
	jobs := make([]*models.Job, len(f.executedJobs))
	copy(jobs, f.executedJobs)
	return jobs
}

// ClearExecutedJobs clears the list of executed jobs.
func (f *FakeCloudTasksService) ClearExecutedJobs() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executedJobs = make([]*models.Job, 0)
}

// WaitForJobs waits for a specific number of jobs to be executed.
func (f *FakeCloudTasksService) WaitForJobs(count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		f.mu.Lock()
		currentCount := len(f.executedJobs)
		f.mu.Unlock()

		if currentCount >= count {
			return nil
		}

		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for %d jobs, only %d executed", count, len(f.executedJobs)) //nolint:err113 // Test code
}
