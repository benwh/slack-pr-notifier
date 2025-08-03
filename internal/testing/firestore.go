package testing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	ErrInvalidTCPAddress    = errors.New("failed to cast listener address to TCP address")
)

// Global emulator singleton.
var (
	globalEmulator     *FirestoreEmulator
	globalEmulatorOnce sync.Once
	errGlobalEmulator  error
)

// FirestoreEmulator manages a Firestore emulator instance for testing.
type FirestoreEmulator struct {
	Host      string
	ProjectID string
	Client    *firestore.Client
	cmd       *exec.Cmd
	cleanup   func()
}

// TestDatabase provides isolated database access for individual tests.
type TestDatabase struct {
	client    *firestore.Client
	testID    string
	namespace string
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

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, ErrInvalidTCPAddress
	}
	return tcpAddr.Port, nil
}

// generateUniqueProjectID generates a unique project ID for test isolation.
func generateUniqueProjectID(t *testing.T) string {
	t.Helper()

	// Use timestamp and random suffix for uniqueness (keep it short and valid)
	timestamp := time.Now().Unix()

	// Generate cryptographically secure random number
	randomBig, err := rand.Int(rand.Reader, big.NewInt(1000))
	if err != nil {
		// Fallback to timestamp-based randomness if crypto/rand fails
		randomBig = big.NewInt(timestamp % 1000)
	}
	randomSuffix := randomBig.Int64()

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
	// #nosec G204 -- Static arguments for test emulator command
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

// GetGlobalEmulator returns the global shared emulator instance.
func GetGlobalEmulator() (*FirestoreEmulator, error) {
	globalEmulatorOnce.Do(func() {
		globalEmulator, errGlobalEmulator = startGlobalEmulator()
	})
	return globalEmulator, errGlobalEmulator
}

// startGlobalEmulator starts the global emulator instance.
func startGlobalEmulator() (*FirestoreEmulator, error) {
	emulator := &FirestoreEmulator{
		ProjectID: "test-project-global",
	}

	// Check if emulator is already running (e.g., in CI or manually started)
	if existingHost := os.Getenv("FIRESTORE_EMULATOR_HOST"); existingHost != "" {
		emulator.Host = existingHost
	} else {
		// Use fixed port for stability
		emulator.Host = "localhost:8089"
		if err := emulator.startLocalEmulatorOnPort(8089); err != nil {
			return nil, err
		}
	}

	// Create Firestore client
	ctx := context.Background()
	client, err := emulator.createClient(ctx)
	if err != nil {
		if emulator.cmd != nil {
			_ = emulator.cmd.Process.Kill()
		}
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}
	emulator.Client = client

	// Set cleanup function
	emulator.cleanup = func() {
		_ = client.Close()
		if emulator.cmd != nil {
			_ = emulator.cmd.Process.Kill()
		}
	}

	return emulator, nil
}

// startLocalEmulatorOnPort starts emulator on a specific port.
func (e *FirestoreEmulator) startLocalEmulatorOnPort(port int) error {
	// Check if gcloud is available
	if _, err := exec.LookPath("gcloud"); err != nil {
		return fmt.Errorf("gcloud not found in PATH: %w", err)
	}

	// Start emulator on specific port
	e.Host = fmt.Sprintf("localhost:%d", port)
	// #nosec G204 -- Static arguments for test emulator command
	e.cmd = exec.Command("gcloud", "emulators", "firestore", "start", "--host-port", e.Host)

	// Start in background
	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start emulator: %w", err)
	}

	// Set environment variable
	_ = os.Setenv("FIRESTORE_EMULATOR_HOST", e.Host)

	// Wait for emulator to be ready
	if err := e.waitForEmulator(); err != nil {
		_ = e.cmd.Process.Kill()
		return fmt.Errorf("emulator failed to start: %w", err)
	}

	return nil
}

// NewTestDatabase creates an isolated database connection for a test.
func NewTestDatabase(t *testing.T) (*TestDatabase, error) {
	t.Helper()

	emulator, err := GetGlobalEmulator()
	if err != nil {
		return nil, err
	}

	// Use the shared emulator client instead of creating a new one
	// We'll rely on test cleanup rather than namespacing for isolation
	return &TestDatabase{
		client: emulator.Client,
		testID: fmt.Sprintf("test_%s_%d",
			strings.ReplaceAll(t.Name(), "/", "_"),
			time.Now().UnixNano()),
		namespace: "", // No namespace needed with shared client
	}, nil
}

// Collection returns a collection reference (no namespacing with shared client).
func (td *TestDatabase) Collection(name string) *firestore.CollectionRef {
	// Use regular collection names since we're using a shared client
	return td.client.Collection(name)
}

// Client returns the underlying Firestore client.
func (td *TestDatabase) Client() *firestore.Client {
	return td.client
}

// Cleanup deletes all data from the emulator database.
func (td *TestDatabase) Cleanup(ctx context.Context) error {
	// Get the global emulator and use its clear data method
	emulator, err := GetGlobalEmulator()
	if err != nil {
		return err
	}

	return emulator.ClearData(ctx)
}

// Close closes the test database client (no-op since we share the global client).
func (td *TestDatabase) Close() error {
	// Don't close the shared client, it will be closed when the global emulator stops
	return nil
}

// StopGlobalEmulator stops the global emulator instance.
func StopGlobalEmulator() error {
	if globalEmulator != nil && globalEmulator.cleanup != nil {
		globalEmulator.cleanup()
		globalEmulator = nil
		// Reset the sync.Once so it can be started again
		globalEmulatorOnce = sync.Once{}
		return nil
	}
	return nil
}
