package mirror

import (
	"testing"
)

func TestDownloadLoggingContext(t *testing.T) {
	// This test verifies that the HTTPClient has the expected download methods
	// for different logging contexts (indices vs packages vs custom)

	// Create test HTTP client
	httpClient := &HTTPClient{
		mirrorID: "test-repo",
	}

	// Simply verify the methods exist by attempting to take their addresses
	// If the methods don't exist, this won't compile
	t.Run("downloadFiles function exists", func(t *testing.T) {
		_ = httpClient.downloadFiles
		t.Log("downloadFiles method exists")
	})

	t.Run("downloadIndicesFiles function exists", func(t *testing.T) {
		_ = httpClient.downloadIndicesFiles
		t.Log("downloadIndicesFiles method exists")
	})

	t.Run("downloadPackageFiles function exists", func(t *testing.T) {
		_ = httpClient.downloadPackageFiles
		t.Log("downloadPackageFiles method exists")
	})

	t.Run("downloadFilesWithContext function exists", func(t *testing.T) {
		_ = httpClient.downloadFilesWithContext
		t.Log("downloadFilesWithContext method exists")
	})
}

// Example of what the improved log output will look like:
//
// BEFORE (ambiguous):
// time=2024-01-01T10:00:00Z level=INFO msg="stats" repo=ubuntu total=4 reused=0 downloaded=4
//
// AFTER (clear):
// time=2024-01-01T10:00:00Z level=INFO msg="download stats" repo=ubuntu type=indices total=4 reused=0 downloaded=4
// time=2024-01-01T10:00:00Z level=INFO msg="download stats" repo=ubuntu type=packages total=1250 reused=800 downloaded=450
//
// This clearly distinguishes between:
// - type=indices: Release, Packages, Sources files (metadata)
// - type=packages: .deb, .tar.gz, etc. (actual package files)
//
// Additional improvements in logging messages:
// - "downloading files" → "downloading package files"
// - "all files up to date" → "all package files up to date"
