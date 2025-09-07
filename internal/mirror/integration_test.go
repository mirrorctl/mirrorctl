package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockAPTRepository provides a test HTTP server that mimics an APT repository
type MockAPTRepository struct {
	server        *httptest.Server
	requestCount  int64
	failRequests  bool
	slowResponses bool
	mu            sync.RWMutex
	responses     map[string]string
}

func NewMockAPTRepository() *MockAPTRepository {
	mock := &MockAPTRepository{
		responses: make(map[string]string),
	}

	// Set up default APT repository files
	mock.setupDefaultResponses()

	mock.server = httptest.NewServer(http.HandlerFunc(mock.handleRequest))
	return mock
}

func (m *MockAPTRepository) Close() {
	m.server.Close()
}

func (m *MockAPTRepository) URL() string {
	return m.server.URL
}

func (m *MockAPTRepository) SetFailRequests(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failRequests = fail
}

func (m *MockAPTRepository) SetSlowResponses(slow bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.slowResponses = slow
}

func (m *MockAPTRepository) RequestCount() int64 {
	return atomic.LoadInt64(&m.requestCount)
}

func (m *MockAPTRepository) handleRequest(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&m.requestCount, 1)

	m.mu.RLock()
	fail := m.failRequests
	slow := m.slowResponses
	m.mu.RUnlock()

	if fail {
		http.Error(w, "Mock server error", http.StatusInternalServerError)
		return
	}

	if slow {
		time.Sleep(100 * time.Millisecond)
	}

	path := strings.TrimPrefix(r.URL.Path, "/")

	m.mu.RLock()
	content, exists := m.responses[path]
	m.mu.RUnlock()

	if !exists {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
}

func (m *MockAPTRepository) setupDefaultResponses() {
	// Minimal Release file content - just enough to parse
	releaseContent := `Origin: Test
Suite: test
Date: Wed, 15 Mar 2023 12:00:00 UTC
Architectures: amd64
Components: main
MD5Sum:
 d41d8cd98f00b204e9800998ecf8427e                0 main/binary-amd64/Packages
`

	// Empty Packages file (valid but minimal)
	packagesContent := ``

	m.responses["dists/test/Release"] = releaseContent
	m.responses["dists/test/main/binary-amd64/Packages"] = packagesContent
}

func (m *MockAPTRepository) AddFile(path, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[path] = content
}

// TestMirrorUpdateCycle tests the complete mirror update process
func TestMirrorUpdateCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Create mock APT repository
	mockRepo := NewMockAPTRepository()
	defer mockRepo.Close()

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "mirror-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte(mockRepo.URL()))
	if err != nil {
		t.Fatal("Failed to parse mock URL:", err)
	}

	config := &Config{
		Dir: tempDir,
		Mirrors: map[string]*MirrorConfig{
			"test-mirror": {
				URL:           *mockURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// Create mirror instance
	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "test-mirror", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test 1: Successful mirror update
	ctx := context.Background()
	err = mirror.Update(ctx)
	if err != nil {
		t.Error("Mirror update failed:", err)
	}

	// Verify files were created
	expectedFiles := []string{
		"dists/test/Release",
		"dists/test/main/binary-amd64/Packages",
	}

	for _, expectedFile := range expectedFiles {
		fullPath := filepath.Join(tempDir, "test-mirror", expectedFile)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("Expected file not created: %s", expectedFile)
		}
	}

	// Verify request count
	requestCount := mockRepo.RequestCount()
	if requestCount == 0 {
		t.Error("No requests made to mock repository")
	}

	t.Logf("Successful mirror update completed with %d requests", requestCount)
}

// TestMirrorNetworkErrors tests mirror behavior with network failures
func TestMirrorNetworkErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Create mock APT repository that fails requests
	mockRepo := NewMockAPTRepository()
	defer mockRepo.Close()
	mockRepo.SetFailRequests(true)

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "mirror-error-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte(mockRepo.URL()))
	if err != nil {
		t.Fatal("Failed to parse mock URL:", err)
	}

	config := &Config{
		Dir: tempDir,
		Mirrors: map[string]*MirrorConfig{
			"error-test": {
				URL:           *mockURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// Create mirror instance
	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "error-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test: Mirror update should handle network errors gracefully
	ctx := context.Background()
	err = mirror.Update(ctx)
	if err == nil {
		t.Error("Expected mirror update to fail with network errors")
	}

	t.Logf("Mirror correctly failed with network errors: %v", err)
}

// TestMirrorConcurrentUpdates tests concurrent mirror operations
func TestMirrorConcurrentUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Create mock APT repository with slow responses
	mockRepo := NewMockAPTRepository()
	defer mockRepo.Close()
	mockRepo.SetSlowResponses(true)

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "mirror-concurrent-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte(mockRepo.URL()))
	if err != nil {
		t.Fatal("Failed to parse mock URL:", err)
	}

	config := &Config{
		Dir: tempDir,
		Mirrors: map[string]*MirrorConfig{
			"concurrent-test": {
				URL:           *mockURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// Test: Run multiple concurrent updates
	const numConcurrent = 3
	var wg sync.WaitGroup
	errors := make(chan error, numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			timestamp := time.Now()
			mirror, err := NewMirror(timestamp, "concurrent-test", config, false, false, false)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: failed to create mirror: %v", id, err)
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err = mirror.Update(ctx)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: mirror update failed: %v", id, err)
				return
			}

			t.Logf("Goroutine %d completed successfully", id)
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errorCount int
	for err := range errors {
		errorCount++
		t.Error(err)
	}

	if errorCount == 0 {
		t.Logf("All %d concurrent updates completed successfully", numConcurrent)
	}

	// Verify total request count
	totalRequests := mockRepo.RequestCount()
	t.Logf("Total requests made: %d", totalRequests)
}

// TestMirrorContextCancellation tests proper handling of context cancellation
func TestMirrorContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Create mock APT repository with very slow responses
	mockRepo := NewMockAPTRepository()
	defer mockRepo.Close()
	mockRepo.SetSlowResponses(true)

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "mirror-cancel-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte(mockRepo.URL()))
	if err != nil {
		t.Fatal("Failed to parse mock URL:", err)
	}

	config := &Config{
		Dir: tempDir,
		Mirrors: map[string]*MirrorConfig{
			"cancel-test": {
				URL:           *mockURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// Create mirror instance
	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "cancel-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test: Cancel context during update
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = mirror.Update(ctx)
	if err == nil {
		t.Error("Expected mirror update to fail due to context cancellation")
	}

	if err == context.DeadlineExceeded || err == context.Canceled {
		t.Logf("Mirror correctly handled context cancellation: %v", err)
	} else {
		t.Errorf("Expected context cancellation error, got: %v", err)
	}
}

// TestMirrorPartialDownload tests handling of partial/interrupted downloads
func TestMirrorPartialDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Create a mock server that serves partial content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate connection drop by closing connection early
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)

		// Write partial content then close
		io.WriteString(w, "Partial content...")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Connection will be closed when handler returns
	}))
	defer server.Close()

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "mirror-partial-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte(server.URL))
	if err != nil {
		t.Fatal("Failed to parse server URL:", err)
	}

	config := &Config{
		Dir: tempDir,
		Mirrors: map[string]*MirrorConfig{
			"partial-test": {
				URL:           *mockURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// Create mirror instance
	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "partial-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test: Update should handle partial downloads
	ctx := context.Background()
	err = mirror.Update(ctx)

	// We expect this to fail due to incomplete/invalid APT repository data
	if err == nil {
		t.Error("Expected mirror update to fail with partial download")
	}

	t.Logf("Mirror correctly handled partial download: %v", err)
}
