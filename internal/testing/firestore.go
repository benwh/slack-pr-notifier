package testing

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultEmulatorHost = "localhost:5065"
	defaultProjectID    = "test-project"
	emulatorStartupTime = 5 * time.Second
	pollInterval        = 100 * time.Millisecond
	emulatorWaitTime    = 3 * time.Second
	clearDataTimeout    = 10 * time.Second
	httpRequestTimeout  = 1 * time.Second
)

// Static errors.
var (
	ErrEmulatorStartTimeout = errors.New("emulator did not start within timeout")
	ErrEmulatorClearFailed  = errors.New("failed to clear emulator data")
	ErrNoAvailablePort      = errors.New("no available port found for emulator")
)

// FirestoreEmulator manages a Firestore emulator instance for testing.
type FirestoreEmulator struct {
	Host      string
	ProjectID string
	Client    *firestore.Client
	cmd       *exec.Cmd
	cleanup   func()
}

// SetupFirestoreEmulator creates a new Firestore emulator instance for testing.
// It first checks if FIRESTORE_EMULATOR_HOST is already set (e.g., from CI environment).
// If not, it attempts to start a local emulator using gcloud.
func SetupFirestoreEmulator(t *testing.T) (*FirestoreEmulator, context.Context) {
	t.Helper()

	ctx := context.Background()
	emulator := &FirestoreEmulator{
		ProjectID: generateUniqueProjectID(t),
	}

	// Check if emulator is already running (e.g., in CI or manually started)
	if existingHost := os.Getenv("FIRESTORE_EMULATOR_HOST"); existingHost != "" {
		t.Logf("Using existing Firestore emulator at %s", existingHost)
		emulator.Host = existingHost
	} else {
		// Try to start local emulator
		if err := emulator.startLocalEmulator(t); err != nil {
			t.Fatalf("Failed to start Firestore emulator: %v", err)
		}
	}

	// Create Firestore client
	client, err := emulator.createClient(ctx)
	if err != nil {
		if emulator.cmd != nil {
			_ = emulator.cmd.Process.Kill()
		}
		t.Fatalf("Failed to create Firestore client: %v", err)
	}
	emulator.Client = client

	// Set cleanup function
	emulator.cleanup = func() {
		_ = client.Close()
		if emulator.cmd != nil {
			_ = emulator.cmd.Process.Kill()
		}
	}

	// Clear any existing data
	if err := emulator.ClearData(ctx); err != nil {
		t.Logf("Warning: Failed to clear emulator data: %v", err)
	}

	return emulator, ctx
}

// findAvailablePort finds an available port on localhost.
func findAvailablePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, fmt.Errorf("failed to resolve TCP address: %w", err)
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to listen on TCP address: %w", err)
	}
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

// generateUniqueProjectID generates a unique project ID for test isolation.
func generateUniqueProjectID(t *testing.T) string {
	t.Helper()

	// Use timestamp and random suffix for uniqueness (keep it short and valid)
	timestamp := time.Now().Unix()
	randomSuffix := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(1000)

	// Format as test-{timestamp}-{random} but keep it under 30 chars total
	projectID := fmt.Sprintf("test-%d-%d", timestamp, randomSuffix)

	// Ensure it's not too long (max 30 chars for GCP project IDs)
	if len(projectID) > 30 {
		// Truncate timestamp if needed
		truncatedTimestamp := timestamp % 1000000 // Last 6 digits
		projectID = fmt.Sprintf("test-%d-%d", truncatedTimestamp, randomSuffix)
	}

	return projectID
}

// startLocalEmulator attempts to start a local Firestore emulator using gcloud.
func (e *FirestoreEmulator) startLocalEmulator(t *testing.T) error {
	t.Helper()

	// Check if gcloud is available
	if _, err := exec.LookPath("gcloud"); err != nil {
		return fmt.Errorf("gcloud not found in PATH: %w", err)
	}

	// Find an available port
	port, err := findAvailablePort()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoAvailablePort, err)
	}

	// Start emulator on available port
	e.Host = fmt.Sprintf("localhost:%d", port)
	// #nosec G204 -- Static arguments for test emulator command
	e.cmd = exec.Command("gcloud", "emulators", "firestore", "start", "--host-port", e.Host)

	// Start in background
	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start emulator: %w", err)
	}

	// Set environment variable
	t.Setenv("FIRESTORE_EMULATOR_HOST", e.Host)
	t.Logf("Started Firestore emulator at %s", e.Host)

	// Wait for emulator to be ready
	if err := e.waitForEmulator(); err != nil {
		_ = e.cmd.Process.Kill()
		return fmt.Errorf("emulator failed to start: %w", err)
	}

	return nil
}

// waitForEmulator waits for the emulator to be ready to accept connections.
func (e *FirestoreEmulator) waitForEmulator() error {
	deadline := time.Now().Add(emulatorStartupTime)
	url := fmt.Sprintf("http://%s/", e.Host)

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), httpRequestTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			time.Sleep(pollInterval)
			continue
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				cancel()
				return nil
			}
		}
		cancel()
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("%w: %v", ErrEmulatorStartTimeout, emulatorStartupTime)
}

// createClient creates a Firestore client connected to the emulator.
func (e *FirestoreEmulator) createClient(ctx context.Context) (*firestore.Client, error) {
	// Create gRPC connection to emulator
	conn, err := grpc.Dial(e.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Create Firestore client
	client, err := firestore.NewClient(ctx, e.ProjectID, option.WithGRPCConn(conn))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}

	return client, nil
}

// ClearData clears all data from the Firestore emulator.
func (e *FirestoreEmulator) ClearData(ctx context.Context) error {
	url := fmt.Sprintf("http://%s/emulator/v1/projects/%s/databases/(default)/documents", e.Host, e.ProjectID)

	timeoutCtx, cancel := context.WithTimeout(ctx, clearDataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create clear data request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to clear emulator data: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Accept OK, Not Found, or Internal Server Error (project doesn't exist yet)
	// These are all valid states for a fresh emulator with a unique project ID
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusInternalServerError {
		return fmt.Errorf("%w: status %d", ErrEmulatorClearFailed, resp.StatusCode)
	}

	return nil
}

// Cleanup shuts down the emulator and cleans up resources.
func (e *FirestoreEmulator) Cleanup() {
	if e.cleanup != nil {
		e.cleanup()
	}
}

// TestMain helper for running tests with Firestore emulator.
// This can be used in a package's TestMain function to set up the emulator once for all tests.
func RunWithFirestoreEmulator(m *testing.M) int {
	// Check if emulator is already running
	if os.Getenv("FIRESTORE_EMULATOR_HOST") != "" {
		// Emulator already configured, just run tests
		return m.Run()
	}

	// Find an available port
	port, err := findAvailablePort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to find available port for Firestore emulator: %v\n", err)
		fmt.Fprintf(os.Stderr, "Tests requiring Firestore will be skipped\n")
		return m.Run()
	}

	emulatorHost := fmt.Sprintf("localhost:%d", port)

	// Try to start emulator
	cmd := exec.Command("gcloud", "emulators", "firestore", "start", "--host-port", emulatorHost)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to start Firestore emulator: %v\n", err)
		fmt.Fprintf(os.Stderr, "Tests requiring Firestore will be skipped\n")
		return m.Run()
	}

	// Set environment variable
	_ = os.Setenv("FIRESTORE_EMULATOR_HOST", emulatorHost)

	// Give emulator time to start
	time.Sleep(3 * time.Second)

	// Run tests
	code := m.Run()

	// Cleanup
	_ = cmd.Process.Kill()

	return code
}
