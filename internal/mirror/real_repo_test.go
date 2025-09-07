package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMirrorWithRealRepository tests mirror functionality against a small real repository
func TestMirrorWithRealRepository(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real repository test in short mode")
	}

	t.Parallel()

	// Use Microsoft's small Slurm repository - only contains ~10-20 packages
	repoURL := "https://packages.microsoft.com/repos/slurm-ubuntu-noble/"

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "real-mirror-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test configuration for the small Microsoft Slurm repo
	testURL := &tomlURL{}
	err = testURL.UnmarshalText([]byte(repoURL))
	if err != nil {
		t.Fatal("Failed to parse repository URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 5, // Limit connections to be respectful
		Mirrors: map[string]*MirrorConfig{
			"slurm-test": {
				URL:           *testURL,
				Suites:        []string{"stable"}, // Standard repository with stable suite
				Sections:      []string{"main"},   // Standard main section
				Architectures: []string{"amd64"},  // AMD64 architecture
			},
		},
	}

	// Create mirror instance
	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "slurm-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test: Real repository update with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Logf("Starting mirror update from %s", repoURL)
	err = mirror.Update(ctx)
	if err != nil {
		t.Errorf("Mirror update failed: %v", err)
		return
	}

	// Verify basic files were created
	storageDir := mirror.storage.Dir()

	// Log the storage directory structure for debugging
	t.Logf("Storage directory: %s", storageDir)

	// Check storage was populated (should have some files)
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		t.Error("Failed to read storage directory:", err)
	} else if len(entries) == 0 {
		t.Error("Storage directory is empty")
	} else {
		t.Logf("Storage directory contains %d entries", len(entries))

		// List first few entries for debugging
		for i, entry := range entries {
			if i < 5 { // Show first 5 entries
				t.Logf("  - %s", entry.Name())
			}
		}
	}

	// Check if Release file exists anywhere in storage
	var releaseFound bool
	filepath.WalkDir(storageDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "Release" {
			t.Logf("Found Release file at: %s", path)
			releaseFound = true
		}
		return nil
	})

	if !releaseFound {
		t.Log("No Release file found in storage directory")
	}

	// Verify storage metadata was saved
	err = mirror.storage.Save()
	if err != nil {
		t.Error("Failed to save storage metadata:", err)
	}

	t.Logf("Real repository mirror test completed successfully")
}

// TestMirrorRealRepoResume tests resuming an interrupted mirror operation
func TestMirrorRealRepoResume(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real repository resume test in short mode")
	}

	t.Parallel()

	repoURL := "https://packages.microsoft.com/repos/slurm-ubuntu-noble/"

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "resume-mirror-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	testURL := &tomlURL{}
	err = testURL.UnmarshalText([]byte(repoURL))
	if err != nil {
		t.Fatal("Failed to parse repository URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 2, // Very limited to make interruption more likely
		Mirrors: map[string]*MirrorConfig{
			"resume-test": {
				URL:           *testURL,
				Suites:        []string{"stable"}, // Standard repository
				Sections:      []string{"main"},
				Architectures: []string{"amd64"},
			},
		},
	}

	// First attempt: Start mirror but cancel quickly
	timestamp := time.Now()
	mirror1, err := NewMirror(timestamp, "resume-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create first mirror:", err)
	}

	// Very short timeout to simulate interruption
	ctx1, cancel1 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel1()

	t.Logf("Starting first mirror attempt (will be interrupted)")
	err = mirror1.Update(ctx1)
	if err == nil {
		t.Log("First attempt completed unexpectedly fast")
	} else {
		t.Logf("First attempt interrupted as expected: %v", err)
	}

	// Second attempt: Resume with same timestamp and longer timeout
	mirror2, err := NewMirror(timestamp, "resume-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create second mirror:", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel2()

	t.Logf("Starting second mirror attempt (resume)")
	err = mirror2.Update(ctx2)
	if err != nil {
		t.Errorf("Second mirror attempt failed: %v", err)
		return
	}

	t.Logf("Mirror resume test completed successfully")
}

// TestMirrorNetworkResilience tests mirror behavior with network issues
func TestMirrorNetworkResilience(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping network resilience test in short mode")
	}

	t.Parallel()

	// Use a non-existent subdomain to test network failure handling
	badRepoURL := "https://nonexistent.packages.microsoft.com/repos/slurm-ubuntu-noble/"

	// Create temporary directory for mirror
	tempDir, err := os.MkdirTemp("", "network-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	testURL := &tomlURL{}
	err = testURL.UnmarshalText([]byte(badRepoURL))
	if err != nil {
		t.Fatal("Failed to parse bad repository URL:", err)
	}

	config := &Config{
		Dir:      tempDir,
		MaxConns: 5,
		Mirrors: map[string]*MirrorConfig{
			"network-fail-test": {
				URL:           *testURL,
				Suites:        []string{"/"},
				Sections:      []string{},
				Architectures: []string{},
			},
		},
	}

	timestamp := time.Now()
	mirror, err := NewMirror(timestamp, "network-fail-test", config, false, false, false)
	if err != nil {
		t.Fatal("Failed to create mirror:", err)
	}

	// Test with reasonable timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Logf("Testing mirror with non-existent repository")
	err = mirror.Update(ctx)
	if err == nil {
		t.Error("Expected mirror update to fail with non-existent repository")
	} else {
		t.Logf("Mirror correctly failed with network error: %v", err)
	}
}
