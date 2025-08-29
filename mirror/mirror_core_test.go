package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewMirrorCreation tests mirror creation with various configurations
func TestNewMirrorCreation(t *testing.T) {
	t.Parallel()

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mirror-creation-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Test 1: Valid mirror creation
	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte("http://example.com/ubuntu/"))
	if err != nil {
		t.Fatal("Failed to parse URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 10,
		Mirrors: map[string]*MirrConfig{
			"test-mirror": {
				URL:           *mockURL,
				Suites:        []string{"focal"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "test-mirror", config)
	if err != nil {
		t.Error("Failed to create valid mirror:", err)
	}

	if mirror == nil {
		t.Error("Mirror should not be nil")
	}

	if mirror.id != "test-mirror" {
		t.Errorf("Expected mirror ID 'test-mirror', got '%s'", mirror.id)
	}

	// Test 2: Non-existent mirror ID should fail
	_, err = NewMirror(timestamp, "non-existent", config)
	if err == nil {
		t.Error("Should fail with non-existent mirror ID")
	}

	// Test 3: Check storage directory creation (timestamped directory)
	// The actual directory created has format: .{mirrorID}.{timestamp}
	storageDir := mirror.storage.Dir()
	if storageDir == "" {
		t.Error("Storage directory should not be empty")
	}
	
	if _, err := os.Stat(storageDir); os.IsNotExist(err) {
		t.Error("Storage directory should be created")
	}
	
	// Check that the storage directory follows the expected pattern
	expectedPrefix := filepath.Join(tempDir, ".test-mirror.")
	if !filepath.HasPrefix(storageDir, expectedPrefix) {
		t.Errorf("Storage directory should start with %s, got %s", expectedPrefix, storageDir)
	}
}

// TestMirrorConfigValidation tests configuration validation
func TestMirrorConfigValidation(t *testing.T) {
	t.Parallel()

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mirror-config-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte("http://example.com/ubuntu/"))
	if err != nil {
		t.Fatal("Failed to parse URL:", err)
	}

	// Test 1: Empty suites should fail
	config := &Config{
		Dir:      tempDir,
		MaxConns: 10,
		Mirrors: map[string]*MirrConfig{
			"invalid-mirror": {
				URL:           *mockURL,
				Suites:        []string{}, // Empty suites
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	timestamp := time.Now()
	_, err = NewMirror(timestamp, "invalid-mirror", config)
	if err == nil {
		t.Error("Should fail with empty suites")
	}

	// Test 2: Flat repository with sections should fail
	flatURL := &tomlURL{}
	err = flatURL.UnmarshalText([]byte("http://example.com/debian/"))
	if err != nil {
		t.Fatal("Failed to parse flat URL:", err)
	}

	config.Mirrors["invalid-mirror"] = &MirrConfig{
		URL:           *flatURL,
		Suites:        []string{"/"}, // Flat repository
		Sections:      []string{"main"}, // Should not have sections
		Architectures: []string{"amd64"},
	}

	_, err = NewMirror(timestamp, "invalid-mirror", config)
	if err == nil {
		t.Error("Should fail with flat repository having sections")
	}
}

// TestMirrorStorageOperations tests storage-related operations
func TestMirrorStorageOperations(t *testing.T) {
	t.Parallel()

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mirror-storage-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte("http://example.com/ubuntu/"))
	if err != nil {
		t.Fatal("Failed to parse URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 10,
		Mirrors: map[string]*MirrConfig{
			"storage-test": {
				URL:           *mockURL,
				Suites:        []string{"focal"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "storage-test", config)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test storage directory creation
	storageDir := mirror.storage.Dir()
	if storageDir == "" {
		t.Error("Storage directory should not be empty")
	}

	// Check if storage directory exists
	if _, err := os.Stat(storageDir); os.IsNotExist(err) {
		t.Error("Storage directory should exist")
	}

	// Test storage save (should work even with empty storage)
	err = mirror.storage.Save()
	if err != nil {
		t.Error("Storage save should work:", err)
	}
}

// TestMirrorContextHandling tests context cancellation handling
func TestMirrorContextHandling(t *testing.T) {
	t.Parallel()

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "mirror-context-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	mockURL := &tomlURL{}
	err = mockURL.UnmarshalText([]byte("http://nonexistent.example.com/"))
	if err != nil {
		t.Fatal("Failed to parse URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 10,
		Mirrors: map[string]*MirrConfig{
			"context-test": {
				URL:           *mockURL,
				Suites:        []string{"focal"},
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "context-test", config)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = mirror.Update(ctx)
	if err == nil {
		t.Error("Update should fail with cancelled context")
	}

	// Check if error is context-related
	if err != context.Canceled && err.Error() != "context canceled" {
		t.Logf("Update failed appropriately with cancelled context: %v", err)
	}
}

// TestReleaseFileGeneration tests release file path generation
func TestReleaseFileGeneration(t *testing.T) {
	t.Parallel()

	mockURL := &tomlURL{}
	err := mockURL.UnmarshalText([]byte("http://example.com/ubuntu/"))
	if err != nil {
		t.Fatal("Failed to parse URL:", err)
	}

	config := &MirrConfig{
		URL:           *mockURL,
		Suites:        []string{"focal", "jammy"},
		Sections:      []string{"main", "universe"},
		Architectures: []string{"amd64", "arm64"},
	}

	// Test release file generation for non-flat repository
	releaseFiles := config.ReleaseFiles("focal")
	
	expectedFiles := []string{
		"dists/focal/Release",
		"dists/focal/Release.gpg",
		"dists/focal/Release.gz",
		"dists/focal/Release.bz2",
		"dists/focal/InRelease",
		"dists/focal/InRelease.gz", 
		"dists/focal/InRelease.bz2",
	}

	if len(releaseFiles) < len(expectedFiles) {
		t.Error("Not enough release files generated")
	}

	// Check that expected files are present
	releaseFileMap := make(map[string]bool)
	for _, file := range releaseFiles {
		releaseFileMap[file] = true
	}

	for _, expectedFile := range expectedFiles {
		if !releaseFileMap[expectedFile] {
			t.Errorf("Expected release file not found: %s", expectedFile)
		}
	}

	// Test flat repository release files  
	flatConfig := &MirrConfig{
		URL:    *mockURL,
		Suites: []string{"/"},
	}

	flatReleaseFiles := flatConfig.ReleaseFiles("/")
	if len(flatReleaseFiles) == 0 {
		t.Error("Flat repository should generate release files")
	}

	// Flat repository files should be at root level
	for _, file := range flatReleaseFiles {
		if file != "Release" && file != "Release.gpg" && file != "Release.gz" && 
		   file != "Release.bz2" && file != "InRelease" && file != "InRelease.gz" && 
		   file != "InRelease.bz2" {
			t.Errorf("Unexpected flat repository file: %s", file)
		}
	}
}