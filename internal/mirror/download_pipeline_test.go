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
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirrorctl/mirrorctl/internal/apt"
)

// DownloadTestServer provides a mock HTTP server for testing download functionality
type DownloadTestServer struct {
	server       *httptest.Server
	responses    map[string]*testResponse
	requestCount int64
	failAfter    int64 // Fail requests after N successful ones
}

type testResponse struct {
	statusCode int
	content    []byte
	delay      time.Duration
	checksum   string // Expected SHA256 for content
}

func NewDownloadTestServer() *DownloadTestServer {
	mock := &DownloadTestServer{
		responses: make(map[string]*testResponse),
	}
	mock.server = httptest.NewServer(http.HandlerFunc(mock.handleRequest))
	return mock
}

func (m *DownloadTestServer) Close() {
	m.server.Close()
}

func (m *DownloadTestServer) URL() string {
	return m.server.URL
}

func (m *DownloadTestServer) SetFailAfter(count int64) {
	atomic.StoreInt64(&m.failAfter, count)
}

func (m *DownloadTestServer) RequestCount() int64 {
	return atomic.LoadInt64(&m.requestCount)
}

func (m *DownloadTestServer) AddResponse(path string, statusCode int, content []byte, delay time.Duration) {
	// Calculate checksum for the content
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(content)), path)
	checksum := ""
	if err == nil {
		checksum = fi.SHA256Path()
	}

	m.responses[path] = &testResponse{
		statusCode: statusCode,
		content:    content,
		delay:      delay,
		checksum:   checksum,
	}
}

func (m *DownloadTestServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	count := atomic.AddInt64(&m.requestCount, 1)

	// Check if we should fail this request
	failAfter := atomic.LoadInt64(&m.failAfter)
	if failAfter > 0 && count > failAfter {
		http.Error(w, "Mock server error", http.StatusInternalServerError)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")

	response, exists := m.responses[path]
	if !exists {
		http.NotFound(w, r)
		return
	}

	if response.delay > 0 {
		time.Sleep(response.delay)
	}

	w.WriteHeader(response.statusCode)
	if response.statusCode == http.StatusOK && len(response.content) > 0 {
		w.Write(response.content)
	}
}

// setupTestMirror creates a mirror instance for testing
func setupTestMirror(t *testing.T, serverURL string) (*Mirror, string) {
	tempDir, err := os.MkdirTemp("", "download-test-")
	if err != nil {
		t.Fatal(err)
	}

	testURL := &tomlURL{}
	err = testURL.UnmarshalText([]byte(serverURL))
	if err != nil {
		t.Fatal("Failed to parse test URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 5,
		Mirrors: map[string]*MirrorConfig{
			"test-mirror": {
				URL:           *testURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	mirror, err := NewMirror(time.Now(), "test-mirror", config, false, false, false)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatal("Failed to create mirror:", err)
	}

	return mirror, tempDir
}

// TestDownloadBasic tests basic file download functionality
func TestDownloadBasic(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	// Add test file
	testContent := []byte("test package content")
	server.AddResponse("test/file.txt", http.StatusOK, testContent, 0)

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	// Create expected FileInfo
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(testContent)), "test/file.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	// Test download
	ctx := context.Background()
	results := make(chan *dlResult, 1)

	// Start download (acquire semaphore token first)
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "test/file.txt", fi, false, results)
	}()

	// Get result
	result := <-results

	if result.err != nil {
		t.Errorf("Download failed: %v", result.err)
	}

	if result.status != http.StatusOK {
		t.Errorf("Expected status 200, got %d", result.status)
	}

	if result.fi == nil {
		t.Error("FileInfo should not be nil")
	}

	if result.tempfile == nil {
		t.Error("Temp file should not be nil")
	} else {
		defer os.Remove(result.tempfile.Name())
		result.tempfile.Close()
	}

	if server.RequestCount() != 1 {
		t.Errorf("Expected 1 request, got %d", server.RequestCount())
	}
}

// TestDownloadRetryLogic tests HTTP retry on failures
func TestDownloadRetryLogic(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	// Set server to always return 500 (server error)
	testContent := []byte("retry test content")
	server.AddResponse("test/retry.txt", http.StatusInternalServerError, nil, 0)

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	// Create expected FileInfo
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(testContent)), "test/retry.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	ctx := context.Background()
	results := make(chan *dlResult, 1)

	// Start download (this should retry multiple times and then fail)
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "test/retry.txt", fi, false, results)
	}()

	// Get result
	result := <-results

	if result.err != nil {
		t.Logf("Download failed as expected after retries: %v", result.err)
	}

	if result.status != http.StatusInternalServerError {
		t.Errorf("Expected 500 status, got %d", result.status)
	}

	if result.tempfile != nil {
		defer os.Remove(result.tempfile.Name())
		result.tempfile.Close()
	}

	// Should have made multiple requests due to retries (at least 2, up to httpRetries+1)
	if server.RequestCount() < 2 {
		t.Errorf("Expected multiple requests due to retries, got %d", server.RequestCount())
	}

	t.Logf("Made %d retry attempts as expected", server.RequestCount())
}

// TestDownloadContextCancellation tests proper context cancellation handling
func TestDownloadContextCancellation(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	// Add slow response
	testContent := []byte("slow response")
	server.AddResponse("test/slow.txt", http.StatusOK, testContent, 500*time.Millisecond)

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results := make(chan *dlResult, 1)

	// Start download
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "test/slow.txt", nil, false, results)
	}()

	// Get result
	result := <-results

	if result.err == nil {
		t.Error("Expected context cancellation error")
	}

	if result.err != context.DeadlineExceeded {
		t.Logf("Download correctly failed with context error: %v", result.err)
	}
}

// TestDownloadByHashFallback tests SHA256/SHA1/MD5 fallback mechanism
func TestDownloadByHashFallback(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	testContent := []byte("by-hash test content")

	// Create FileInfo to get hash paths
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(testContent)), "test/byhash.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	// Main path returns checksum mismatch (wrong content), which should trigger by-hash fallback
	wrongContent := []byte("wrong content for main path")
	server.AddResponse("test/byhash.txt", http.StatusOK, wrongContent, 0)

	// SHA256 path should return correct content
	sha256Path := strings.TrimPrefix(fi.SHA256Path(), "/")
	server.AddResponse(sha256Path, http.StatusOK, testContent, 0)

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	results := make(chan *dlResult, 1)

	// Start download with by-hash enabled
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "test/byhash.txt", fi, true, results)
	}()

	// Get result
	result := <-results

	if result.err != nil {
		t.Errorf("Download should succeed with by-hash fallback: %v", result.err)
	}

	if result.tempfile != nil {
		defer os.Remove(result.tempfile.Name())
		result.tempfile.Close()
	}

	// Should have made at least 2 requests (original + SHA256)
	if server.RequestCount() < 2 {
		t.Errorf("Expected multiple requests for by-hash fallback, got %d", server.RequestCount())
	}

	t.Logf("By-hash fallback made %d requests as expected", server.RequestCount())
}

// TestDownloadChecksumValidation tests checksum mismatch handling
func TestDownloadChecksumValidation(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	// Serve content that doesn't match expected checksum
	wrongContent := []byte("wrong content")
	server.AddResponse("test/checksum.txt", http.StatusOK, wrongContent, 0)

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	// Create FileInfo with different content (will have different checksum)
	expectedContent := []byte("expected content")
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(expectedContent)), "test/checksum.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	ctx := context.Background()
	results := make(chan *dlResult, 1)

	// Start download
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "test/checksum.txt", fi, false, results)
	}()

	// Get result
	result := <-results

	if result.err == nil {
		t.Error("Expected checksum validation error")
	}

	if result.err != nil && !strings.Contains(result.err.Error(), "invalid checksum") {
		t.Errorf("Expected checksum error, got: %v", result.err)
	}

	if result.tempfile != nil {
		defer os.Remove(result.tempfile.Name())
		result.tempfile.Close()
	}
}

// TestStoreLinkBasic tests basic file storage linking
func TestStoreLinkBasic(t *testing.T) {
	t.Parallel()

	tempDir, err := os.MkdirTemp("", "storelink-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create storage (not directly used but validates storage creation)
	_, err = NewStorage(tempDir, "test")
	if err != nil {
		t.Fatal("Failed to create storage:", err)
	}

	// Create mirror with storage
	testURL := &tomlURL{}
	testURL.UnmarshalText([]byte("http://example.com"))

	config := &Config{
		Dir:      tempDir,
		MaxConns: 5,
		Mirrors: map[string]*MirrorConfig{
			"test": {
				URL:           *testURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	mirror, err := NewMirror(time.Now(), "test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Create test file
	testContent := []byte("store link test")
	testFile, err := os.CreateTemp(tempDir, "testfile")
	if err != nil {
		t.Fatal("Failed to create test file:", err)
	}
	defer os.Remove(testFile.Name())

	testFile.Write(testContent)
	testFile.Close()

	// Create FileInfo
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(testContent)), "test/store.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	// Test storeLink without hash
	err = mirror.storage.StoreLink(fi, testFile.Name())
	if err != nil {
		t.Errorf("storeLink without hash failed: %v", err)
	}

	// Test storeLink with hash
	err = mirror.storage.StoreLinkWithHash(fi, testFile.Name())
	if err != nil {
		t.Errorf("storeLink with hash failed: %v", err)
	}
}

// TestAddFileInfoToList tests file info deduplication logic
func TestAddFileInfoToList(t *testing.T) {
	t.Parallel()

	// Create test FileInfo objects
	fi1, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader("content1"), "test/file1.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo 1:", err)
	}

	fi2, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader("content2"), "test/file2.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo 2:", err)
	}

	// Same path, same content (duplicate)
	fi3, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader("content1"), "test/file1.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo 3:", err)
	}

	// Same path, different content (should cause error)
	fi4, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader("different content"), "test/file1.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo 4:", err)
	}

	fileMap := make(map[string][]*apt.FileInfo)

	// Test adding first file
	err = addFileInfoToList(fi1, fileMap, false)
	if err != nil {
		t.Errorf("Adding first file failed: %v", err)
	}

	// Test adding second file (different path)
	err = addFileInfoToList(fi2, fileMap, false)
	if err != nil {
		t.Errorf("Adding second file failed: %v", err)
	}

	// Test adding duplicate (same path, same content) - should succeed
	err = addFileInfoToList(fi3, fileMap, false)
	if err != nil {
		t.Errorf("Adding duplicate file failed: %v", err)
	}

	// Test adding conflicting file (same path, different content) - should fail
	err = addFileInfoToList(fi4, fileMap, false)
	if err == nil {
		t.Error("Adding conflicting file should have failed")
	}

	// Verify map contents
	if len(fileMap) != 2 {
		t.Errorf("Expected 2 unique paths, got %d", len(fileMap))
	}

	if len(fileMap["test/file1.txt"]) != 1 {
		t.Errorf("Expected 1 file for path test/file1.txt, got %d", len(fileMap["test/file1.txt"]))
	}
}

// TestExtractItems tests extracting file info from downloaded indices
func TestExtractItems(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping extract items test in short mode")
	}

	t.Parallel()

	tempDir, err := os.MkdirTemp("", "extract-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test mirror
	testURL := &tomlURL{}
	testURL.UnmarshalText([]byte("http://example.com"))

	config := &Config{
		Dir:      tempDir,
		MaxConns: 5,
		Mirrors: map[string]*MirrorConfig{
			"test": {
				URL:           *testURL,
				Suites:        []string{"test"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	mirror, err := NewMirror(time.Now(), "test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Create a mock Packages file content
	packagesContent := `Package: test-package
Version: 1.0.0
Architecture: amd64
Filename: pool/main/t/test-package/test-package_1.0.0_amd64.deb
Size: 1024
MD5sum: d41d8cd98f00b204e9800998ecf8427e
SHA1: da39a3ee5e6b4b0d3255bfef95601890afd80709
SHA256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855

Package: another-package
Version: 2.0.0
Architecture: amd64
Filename: pool/main/a/another-package/another-package_2.0.0_amd64.deb
Size: 2048
MD5sum: 098f6bcd4621d373cade4e832627b4f6
SHA1: 356a192b7913b04c54574d18c28d46e6395428ab
SHA256: a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3

`

	// Create temporary packages file
	packagesFile, err := os.CreateTemp(tempDir, "Packages")
	if err != nil {
		t.Fatal("Failed to create packages file:", err)
	}
	defer os.Remove(packagesFile.Name())

	packagesFile.WriteString(packagesContent)
	packagesFile.Close()

	// Create FileInfo for the packages file
	packagesPath := "dists/test/main/binary-amd64/Packages"
	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(packagesContent), packagesPath)
	if err != nil {
		t.Fatal("Failed to create packages FileInfo:", err)
	}

	// Store the packages file in storage
	err = mirror.storage.StoreLink(fi, packagesFile.Name())
	if err != nil {
		t.Fatal("Failed to store packages file:", err)
	}

	// Test extractItems
	indices := []*apt.FileInfo{fi}
	indexMap := make(map[string][]*apt.FileInfo)
	itemMap := make(map[string]*apt.FileInfo)

	err = mirror.parser.extractItems(indices, indexMap, itemMap, false, "test/")
	if err != nil {
		t.Errorf("extractItems failed: %v", err)
	}

	// Verify extracted items (for flat repositories, paths should be prefixed with suite)
	expectedFiles := []string{
		"test/pool/main/t/test-package/test-package_1.0.0_amd64.deb",
		"test/pool/main/a/another-package/another-package_2.0.0_amd64.deb",
	}

	if len(itemMap) != len(expectedFiles) {
		t.Errorf("Expected %d extracted items, got %d", len(expectedFiles), len(itemMap))
	}

	for _, expectedFile := range expectedFiles {
		if _, exists := itemMap[expectedFile]; !exists {
			t.Errorf("Expected file not found in extracted items: %s", expectedFile)
		}
	}
}

// TestHandleResult tests download result processing
func TestHandleResult(t *testing.T) {
	t.Parallel()

	tempDir, err := os.MkdirTemp("", "handle-result-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	mirror, _ := setupTestMirror(t, "http://example.com")
	defer os.RemoveAll(filepath.Dir(mirror.storage.Dir()))

	// Test 1: Successful result
	testContent := []byte("successful content")
	tempFile, err := os.CreateTemp(tempDir, "success")
	if err != nil {
		t.Fatal("Failed to create temp file:", err)
	}
	defer os.Remove(tempFile.Name())

	tempFile.Write(testContent)
	tempFile.Close()

	fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(testContent)), "test/success.txt")
	if err != nil {
		t.Fatal("Failed to create FileInfo:", err)
	}

	result := &dlResult{
		path:     "test/success.txt",
		status:   http.StatusOK,
		fi:       fi,
		tempfile: tempFile,
		err:      nil,
	}

	processedFI, err := mirror.httpClient.handleResult(result, false, false)
	if err != nil {
		t.Errorf("handleResult should succeed: %v", err)
	}

	if processedFI == nil {
		t.Error("Processed FileInfo should not be nil")
	}

	// Test 2: Missing file with allowMissing=true
	result404 := &dlResult{
		path:   "test/missing.txt",
		status: http.StatusNotFound,
		err:    nil,
	}

	processedFI, err = mirror.httpClient.handleResult(result404, true, false)
	if err != nil {
		t.Errorf("handleResult should handle missing file gracefully: %v", err)
	}

	if processedFI != nil {
		t.Error("Processed FileInfo should be nil for missing file")
	}

	// Test 3: Missing file with allowMissing=false
	_, err = mirror.httpClient.handleResult(result404, false, false)
	if err == nil {
		t.Error("handleResult should fail for missing file when not allowed")
	}

	// Test 4: Server error
	result500 := &dlResult{
		path:   "test/error.txt",
		status: http.StatusInternalServerError,
		err:    nil,
	}

	_, err = mirror.httpClient.handleResult(result500, false, false)
	if err == nil {
		t.Error("handleResult should fail for server error")
	}

	// Test 5: Download error
	resultError := &dlResult{
		path: "test/error.txt",
		err:  fmt.Errorf("network error"),
	}

	_, err = mirror.httpClient.handleResult(resultError, false, false)
	if err == nil {
		t.Error("handleResult should fail for download error")
	}
}

// TestDownloadFilesIntegration tests complete download pipeline integration
func TestDownloadFilesIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping download files integration test in short mode")
	}

	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	// Add multiple test files
	testFiles := map[string][]byte{
		"file1.txt": []byte("content of file 1"),
		"file2.txt": []byte("content of file 2"),
		"file3.txt": []byte("content of file 3"),
	}

	var fileInfos []*apt.FileInfo
	for path, content := range testFiles {
		server.AddResponse(path, http.StatusOK, content, 10*time.Millisecond)

		fi, err := apt.CopyWithFileInfo(io.Discard, strings.NewReader(string(content)), path)
		if err != nil {
			t.Fatal("Failed to create FileInfo:", err)
		}
		fileInfos = append(fileInfos, fi)
	}

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	// Test downloadFiles
	ctx := context.Background()
	downloadedFiles, err := mirror.httpClient.downloadFiles(ctx, mirror.mc, fileInfos, false, false)
	if err != nil {
		t.Errorf("downloadFiles failed: %v", err)
	}

	if len(downloadedFiles) != len(testFiles) {
		t.Errorf("Expected %d downloaded files, got %d", len(testFiles), len(downloadedFiles))
	}

	// Verify all files were requested
	if server.RequestCount() != int64(len(testFiles)) {
		t.Errorf("Expected %d requests, got %d", len(testFiles), server.RequestCount())
	}
}

// TestRealRepositoryDownloadPipeline tests download pipeline with real Microsoft Slurm repository
func TestRealRepositoryDownloadPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real repository download pipeline test in short mode")
	}

	t.Parallel()

	// Use Microsoft's small Slurm repository
	repoURL := "https://packages.microsoft.com/repos/slurm-ubuntu-noble/"

	tempDir, err := os.MkdirTemp("", "real-download-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration
	testURL := &tomlURL{}
	err = testURL.UnmarshalText([]byte(repoURL))
	if err != nil {
		t.Fatal("Failed to parse repository URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 3, // Limited to be respectful
		Mirrors: map[string]*MirrorConfig{
			"slurm-pipeline-test": {
				URL:           *testURL,
				Suites:        []string{"stable"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "slurm-pipeline-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test downloading Release file
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	releaseFiles := mirror.mc.ReleaseFiles("stable")
	if len(releaseFiles) == 0 {
		t.Fatal("No release files configured")
	}

	// Test downloading the main Release file
	results := make(chan *dlResult, 1)
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, releaseFiles[0], nil, false, results)
	}()

	result := <-results
	if result.err != nil {
		t.Errorf("Failed to download Release file: %v", result.err)
		return
	}

	if result.status != http.StatusOK {
		t.Errorf("Expected status 200 for Release file, got %d", result.status)
	}

	if result.fi == nil {
		t.Error("FileInfo should not be nil for downloaded Release file")
	}

	if result.tempfile != nil {
		defer os.Remove(result.tempfile.Name())
		result.tempfile.Close()

		// Verify file content
		info, err := os.Stat(result.tempfile.Name())
		if err != nil {
			t.Errorf("Failed to stat downloaded file: %v", err)
		} else if info.Size() == 0 {
			t.Error("Downloaded Release file should not be empty")
		}
	}

	t.Logf("Successfully downloaded Release file from real repository: %s", releaseFiles[0])
}

// TestDownloadPipelineErrorScenarios tests various error scenarios
func TestDownloadPipelineErrorScenarios(t *testing.T) {
	t.Parallel()

	server := NewDownloadTestServer()
	defer server.Close()

	mirror, tempDir := setupTestMirror(t, server.URL())
	defer os.RemoveAll(tempDir)

	ctx := context.Background()

	// Test 1: Non-existent file
	server.AddResponse("nonexistent.txt", http.StatusNotFound, nil, 0)
	results := make(chan *dlResult, 1)
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "nonexistent.txt", nil, false, results)
	}()
	result := <-results

	if result.err != nil {
		t.Logf("Expected error for non-existent file: %v", result.err)
	}
	if result.status != http.StatusNotFound {
		t.Errorf("Expected 404 status, got %d", result.status)
	}

	// Test 2: Server error with max retries
	server.AddResponse("server-error.txt", http.StatusInternalServerError, nil, 0)
	results = make(chan *dlResult, 1)
	go func() {
		<-mirror.httpClient.semaphore
		mirror.httpClient.download(ctx, mirror.mc, "server-error.txt", nil, false, results)
	}()
	result = <-results

	if result.status != http.StatusInternalServerError {
		t.Errorf("Expected 500 status, got %d", result.status)
	}

	// Verify multiple retry attempts were made
	if server.RequestCount() < 2 {
		t.Errorf("Expected multiple retry attempts, got %d requests", server.RequestCount())
	}

	t.Logf("Download pipeline error scenarios completed with %d total requests", server.RequestCount())
}
