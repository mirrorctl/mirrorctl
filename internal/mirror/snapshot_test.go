package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotManager_Basic(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create config
	config := &SnapshotConfig{
		DefaultNameFormat: "2006-01-02T15-04-05Z",
		Prune: SnapshotPruneConfig{
			KeepLast:   3,
			KeepWithin: "7d",
		},
	}

	livePath := filepath.Join(tmpDir, "live")
	sm := NewSnapshotManager(config, livePath)

	// Test path generation
	expectedSnapshotPath := filepath.Join(tmpDir, ".snapshots", "test-mirror", "snapshot-1")
	actualSnapshotPath, err := sm.GetSnapshotPath("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("GetSnapshotPath failed: %v", err)
	}
	if actualSnapshotPath != expectedSnapshotPath {
		t.Errorf("expected snapshot path %s, got %s", expectedSnapshotPath, actualSnapshotPath)
	}

	expectedLivePath := filepath.Join(tmpDir, "live", "test-mirror")
	actualLivePath := sm.GetLivePath("test-mirror")
	if actualLivePath != expectedLivePath {
		t.Errorf("expected live path %s, got %s", expectedLivePath, actualLivePath)
	}

	// Test snapshot name generation
	name := sm.GenerateSnapshotName()
	if len(name) == 0 {
		t.Error("generated snapshot name should not be empty")
	}

	// Test with custom format
	customConfig := &SnapshotConfig{
		DefaultNameFormat: "2006-01-02",
	}
	customSm := NewSnapshotManager(customConfig, livePath)
	customName := customSm.GenerateSnapshotName()
	if len(customName) != 10 { // YYYY-MM-DD
		t.Errorf("expected custom name format length 10, got %d (%s)", len(customName), customName)
	}
}

func TestSnapshotManager_CreateAndList(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure (separate from live path)
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager
	config := &SnapshotConfig{
		DefaultNameFormat: "test-snapshot",
	}
	sm := NewSnapshotManager(config, livePath)

	// Temporarily create live symlink for CreateSnapshot to work
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create snapshot
	_, err := sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Verify snapshot file exists
	snapshotPath, err := sm.GetSnapshotPath("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("GetSnapshotPath failed: %v", err)
	}
	snapshotFile := filepath.Join(snapshotPath, "test.txt")
	if _, err := os.Stat(snapshotFile); os.IsNotExist(err) {
		t.Error("snapshot file should exist")
	}

	// Verify it's a hard link
	sourceInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat source file: %v", err)
	}

	snapshotInfo, err := os.Stat(snapshotFile)
	if err != nil {
		t.Fatalf("failed to stat snapshot file: %v", err)
	}

	if !os.SameFile(sourceInfo, snapshotInfo) {
		t.Error("snapshot file should be a hard link to source file")
	}

	// Test listing snapshots
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snapshots))
	}

	if snapshots[0].Name != "snapshot-1" {
		t.Errorf("expected snapshot name 'snapshot-1', got '%s'", snapshots[0].Name)
	}

	if snapshots[0].Mirror != "test-mirror" {
		t.Errorf("expected mirror 'test-mirror', got '%s'", snapshots[0].Mirror)
	}

	// Test creating duplicate snapshot (should fail)
	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err == nil {
		t.Error("creating duplicate snapshot should fail without force flag")
	}

	// Test creating duplicate snapshot with force (should succeed)
	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", true, nil)
	if err != nil {
		t.Errorf("creating duplicate snapshot with force should succeed: %v", err)
	}
}

func TestSnapshotManager_PublishAndDelete(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure (separate from live path)
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager
	config := &SnapshotConfig{}
	sm := NewSnapshotManager(config, livePath)

	// Create snapshots from mirror data (temporarily create symlink for CreateSnapshot)
	_ = os.MkdirAll(filepath.Join(livePath), 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	_, err := sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot-1: %v", err)
	}

	_, err = sm.CreateSnapshot("test-mirror", "snapshot-2", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot-2: %v", err)
	}

	// Remove the temporary symlink so there's no published snapshot initially
	os.Remove(filepath.Join(livePath, "test-mirror"))

	// Initially no published snapshot
	_, err = sm.GetCurrentlyPublished("test-mirror")
	if err == nil {
		t.Error("should not have published snapshot initially")
	}

	// Publish snapshot-1
	err = sm.PublishSnapshot("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("failed to publish snapshot-1: %v", err)
	}

	// Verify it's published
	published, err := sm.GetCurrentlyPublished("test-mirror")
	if err != nil {
		t.Fatalf("failed to get currently published: %v", err)
	}

	if published != "snapshot-1" {
		t.Errorf("expected published snapshot 'snapshot-1', got '%s'", published)
	}

	// Verify live symlink points to correct location
	liveMirrorPath := sm.GetLivePath("test-mirror")
	target, err := os.Readlink(liveMirrorPath)
	if err != nil {
		t.Fatalf("failed to read live symlink: %v", err)
	}

	expectedTarget, err := sm.GetSnapshotPath("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("GetSnapshotPath failed: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("expected symlink target %s, got %s", expectedTarget, target)
	}

	// Try to delete published snapshot (should fail)
	err = sm.DeleteSnapshot("test-mirror", "snapshot-1", false)
	if err == nil {
		t.Error("should not be able to delete published snapshot")
	}

	// Delete unpublished snapshot (should succeed)
	err = sm.DeleteSnapshot("test-mirror", "snapshot-2", false)
	if err != nil {
		t.Errorf("failed to delete unpublished snapshot: %v", err)
	}

	// Verify snapshot was deleted
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot after deletion, got %d", len(snapshots))
	}

	if snapshots[0].Name != "snapshot-1" {
		t.Errorf("remaining snapshot should be 'snapshot-1', got '%s'", snapshots[0].Name)
	}

	if !snapshots[0].IsPublished {
		t.Error("remaining snapshot should be marked as published")
	}
}

func TestSnapshotManager_PerMirrorNaming(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager with default format
	config := &SnapshotConfig{
		DefaultNameFormat: "2006-01-02T15-04-05Z",
	}
	sm := NewSnapshotManager(config, livePath)

	// Test default naming (no name provided, no mirror config)
	defaultName := sm.GenerateSnapshotNameForMirror(nil)
	if len(defaultName) != len("2006-01-02T15-04-05Z") {
		t.Errorf("expected default name format length %d, got %d (%s)",
			len("2006-01-02T15-04-05Z"), len(defaultName), defaultName)
	}

	// Test per-mirror naming override
	mirrorConfig := &MirrorSnapshotConfig{
		DefaultNameFormat: "2006-01-02",
	}
	customName := sm.GenerateSnapshotNameForMirror(mirrorConfig)
	if len(customName) != 10 { // YYYY-MM-DD
		t.Errorf("expected custom name format length 10, got %d (%s)", len(customName), customName)
	}

	// Test creating snapshot with per-mirror naming
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create snapshot without specifying name - should use mirror-specific format
	actualName, err := sm.CreateSnapshot("test-mirror", "", false, mirrorConfig)
	if err != nil {
		t.Fatalf("failed to create snapshot with mirror config: %v", err)
	}

	if len(actualName) != 10 { // Should use YYYY-MM-DD format
		t.Errorf("expected snapshot name with custom format length 10, got %d (%s)", len(actualName), actualName)
	}

	// Verify snapshot was created
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snapshots))
	}

	if snapshots[0].Name != actualName {
		t.Errorf("expected snapshot name '%s', got '%s'", actualName, snapshots[0].Name)
	}
}

func TestSnapshotManager_ParseDuration(t *testing.T) {
	sm := NewSnapshotManager(&SnapshotConfig{}, "")

	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"60m", 60 * time.Minute, false},
		{"2h30m", 2*time.Hour + 30*time.Minute, false},
		{"invalid", 0, true},
		{"", 0, false},
	}

	for _, test := range tests {
		result, err := sm.ParseDuration(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("expected error for input %s, got nil", test.input)
			}
			continue
		}

		if err != nil {
			t.Errorf("unexpected error for input %s: %v", test.input, err)
			continue
		}

		if result != test.expected {
			t.Errorf("input %s: expected %v, got %v", test.input, test.expected, result)
		}
	}
}

func TestSnapshotManager_Prune(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure (separate from live path)
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager with strict retention policy
	config := &SnapshotConfig{
		Prune: SnapshotPruneConfig{
			KeepLast:   2,
			KeepWithin: "1s", // Very short for testing
		},
	}
	sm := NewSnapshotManager(config, livePath)

	// Temporarily create live symlink for CreateSnapshot to work
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create multiple snapshots
	for i := 1; i <= 5; i++ {
		snapshotName := fmt.Sprintf("snapshot-%d", i)
		_, err := sm.CreateSnapshot("test-mirror", snapshotName, false, nil)
		if err != nil {
			t.Fatalf("failed to create %s: %v", snapshotName, err)
		}
		time.Sleep(1100 * time.Millisecond) // Ensure different timestamps (filesystem resolution)
	}

	// Publish one snapshot
	err := sm.PublishSnapshot("test-mirror", "snapshot-3")
	if err != nil {
		t.Fatalf("failed to publish snapshot: %v", err)
	}

	// Verify we have 5 snapshots
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 5 {
		t.Errorf("expected 5 snapshots before pruning, got %d", len(snapshots))
	}

	// Wait to ensure snapshots are older than 1s
	time.Sleep(1200 * time.Millisecond)

	// Test dry-run prune
	toDelete, err := sm.PruneSnapshots("test-mirror", nil, true, nil, nil)
	if err != nil {
		t.Fatalf("failed to dry-run prune: %v", err)
	}

	if len(toDelete) == 0 {
		t.Error("should have snapshots to delete in dry-run")
	}

	// Verify snapshots still exist after dry-run
	snapshots, err = sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots after dry-run: %v", err)
	}
	if len(snapshots) != 5 {
		t.Errorf("expected 5 snapshots after dry-run, got %d", len(snapshots))
	}

	// Actually prune
	deleted, err := sm.PruneSnapshots("test-mirror", nil, false, nil, nil)
	if err != nil {
		t.Fatalf("failed to prune: %v", err)
	}

	if len(deleted) == 0 {
		t.Error("should have deleted some snapshots")
	}

	// Verify retention policy was applied
	snapshots, err = sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots after prune: %v", err)
	}

	// Should keep: published snapshot + 2 most recent
	if len(snapshots) > 3 {
		t.Errorf("expected at most 3 snapshots after prune, got %d", len(snapshots))
	}

	// Verify published snapshot is still there
	hasPublished := false
	for _, snapshot := range snapshots {
		if snapshot.IsPublished {
			hasPublished = true
			break
		}
	}
	if !hasPublished {
		t.Error("published snapshot should not be pruned")
	}
}

func TestSnapshotManager_StagingWorkflow(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager
	config := &SnapshotConfig{}
	sm := NewSnapshotManager(config, livePath)

	// Create live symlink for CreateSnapshot to work
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create snapshot
	_, err := sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Test publishing to staging
	err = sm.PublishSnapshotToStaging("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("failed to publish to staging: %v", err)
	}

	// Verify staging symlink exists and points to correct location
	stagingPath := sm.GetStagingPath("test-mirror")
	if _, err := os.Lstat(stagingPath); err != nil {
		t.Fatalf("staging symlink should exist: %v", err)
	}

	// Verify we can get the currently staged snapshot
	stagedSnapshot, err := sm.GetCurrentlyStaged("test-mirror")
	if err != nil {
		t.Fatalf("failed to get currently staged: %v", err)
	}

	if stagedSnapshot != "snapshot-1" {
		t.Errorf("expected staged snapshot 'snapshot-1', got '%s'", stagedSnapshot)
	}

	// Test that snapshot list shows staging status
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snapshots))
	}

	if !snapshots[0].IsStaged {
		t.Error("snapshot should be marked as staged")
	}

	if snapshots[0].IsPublished {
		t.Error("snapshot should not be marked as published yet")
	}

	// Test promotion
	promotedSnapshot, err := sm.PromoteSnapshot("test-mirror")
	if err != nil {
		t.Fatalf("failed to promote snapshot: %v", err)
	}

	if promotedSnapshot != "snapshot-1" {
		t.Errorf("expected promoted snapshot 'snapshot-1', got '%s'", promotedSnapshot)
	}

	// Verify production symlink now exists and points to correct location
	livePath = sm.GetLivePath("test-mirror")
	if _, err := os.Lstat(livePath); err != nil {
		t.Fatalf("production symlink should exist after promotion: %v", err)
	}

	// Verify we can get the currently published snapshot
	publishedSnapshot, err := sm.GetCurrentlyPublished("test-mirror")
	if err != nil {
		t.Fatalf("failed to get currently published: %v", err)
	}

	if publishedSnapshot != "snapshot-1" {
		t.Errorf("expected published snapshot 'snapshot-1', got '%s'", publishedSnapshot)
	}

	// Test that snapshot list shows both staging and published status
	snapshots, err = sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots after promotion: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot after promotion, got %d", len(snapshots))
	}

	if !snapshots[0].IsStaged {
		t.Error("snapshot should still be marked as staged")
	}

	if !snapshots[0].IsPublished {
		t.Error("snapshot should now be marked as published")
	}
}

func TestSnapshotManager_StagingProtection(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager
	config := &SnapshotConfig{
		Prune: SnapshotPruneConfig{
			KeepLast:   1,
			KeepWithin: "1s", // Very short for testing
		},
	}
	sm := NewSnapshotManager(config, livePath)

	// Create live symlink for CreateSnapshot to work
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create multiple snapshots
	_, err := sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot-1: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	_, err = sm.CreateSnapshot("test-mirror", "snapshot-2", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot-2: %v", err)
	}

	// Stage one snapshot
	err = sm.PublishSnapshotToStaging("test-mirror", "snapshot-1")
	if err != nil {
		t.Fatalf("failed to publish to staging: %v", err)
	}

	// Try to delete staged snapshot (should fail)
	err = sm.DeleteSnapshot("test-mirror", "snapshot-1", false)
	if err == nil {
		t.Error("should not be able to delete staged snapshot")
	}

	// Wait for snapshots to be older than keep-within duration
	time.Sleep(1200 * time.Millisecond)

	// Test pruning - staged snapshot should be protected
	deleted, err := sm.PruneSnapshots("test-mirror", nil, false, nil, nil)
	if err != nil {
		t.Fatalf("failed to prune: %v", err)
	}

	// Should only delete snapshot-2, keeping snapshot-1 because it's staged
	if len(deleted) != 1 || deleted[0] != "snapshot-2" {
		t.Errorf("expected to delete only snapshot-2, but deleted: %v", deleted)
	}

	// Verify staged snapshot still exists
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Errorf("expected 1 snapshot after pruning, got %d", len(snapshots))
	}

	if snapshots[0].Name != "snapshot-1" {
		t.Errorf("expected remaining snapshot to be 'snapshot-1', got '%s'", snapshots[0].Name)
	}

	if !snapshots[0].IsStaged {
		t.Error("remaining snapshot should still be staged")
	}
}

func TestSnapshotManager_PromoteErrors(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create snapshot manager
	config := &SnapshotConfig{}
	sm := NewSnapshotManager(config, filepath.Join(tmpDir, "live"))

	// Try to promote when nothing is staged (should fail)
	_, err := sm.PromoteSnapshot("test-mirror")
	if err == nil {
		t.Error("should fail when nothing is staged")
	}

	// Test GetCurrentlyStaged when nothing is staged
	_, err = sm.GetCurrentlyStaged("test-mirror")
	if err == nil {
		t.Error("should fail when nothing is staged")
	}
}

func TestSnapshotInfo_Status(t *testing.T) {
	tests := []struct {
		name        string
		isPublished bool
		isStaged    bool
		expected    string
	}{
		{
			name:        "neither published nor staged",
			isPublished: false,
			isStaged:    false,
			expected:    "",
		},
		{
			name:        "published only",
			isPublished: true,
			isStaged:    false,
			expected:    "(published)",
		},
		{
			name:        "staged only",
			isPublished: false,
			isStaged:    true,
			expected:    "(staged)",
		},
		{
			name:        "both published and staged",
			isPublished: true,
			isStaged:    true,
			expected:    "(published, staged)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := &SnapshotInfo{
				Name:        "test-snapshot",
				Mirror:      "test-mirror",
				Path:        "/test/path",
				CreatedAt:   time.Now(),
				IsPublished: tt.isPublished,
				IsStaged:    tt.isStaged,
				Size:        1024,
				FileCount:   5,
			}

			result := snapshot.Status()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestSnapshotManager_PathTraversalProtection(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create snapshot manager
	config := &SnapshotConfig{}
	sm := NewSnapshotManager(config, filepath.Join(tmpDir, "live"))

	// Test cases for path traversal attempts
	testCases := []struct {
		name        string
		mirror      string
		snapshot    string
		shouldFail  bool
		description string
	}{
		{
			name:        "valid names",
			mirror:      "ubuntu",
			snapshot:    "2024-01-15",
			shouldFail:  false,
			description: "normal valid names should work",
		},
		{
			name:        "path traversal in mirror",
			mirror:      "../../etc",
			snapshot:    "passwd",
			shouldFail:  true,
			description: "should reject .. in mirror name",
		},
		{
			name:        "path traversal in snapshot",
			mirror:      "ubuntu",
			snapshot:    "../../../etc/passwd",
			shouldFail:  true,
			description: "should reject .. in snapshot name",
		},
		{
			name:        "absolute path in mirror",
			mirror:      "/etc/passwd",
			snapshot:    "test",
			shouldFail:  true,
			description: "should reject absolute paths in mirror",
		},
		{
			name:        "absolute path in snapshot",
			mirror:      "ubuntu",
			snapshot:    "/etc/passwd",
			shouldFail:  true,
			description: "should reject absolute paths in snapshot",
		},
		{
			name:        "forward slash in mirror",
			mirror:      "ubuntu/malicious",
			snapshot:    "test",
			shouldFail:  true,
			description: "should reject forward slash in mirror",
		},
		{
			name:        "forward slash in snapshot",
			mirror:      "ubuntu",
			snapshot:    "test/malicious",
			shouldFail:  true,
			description: "should reject forward slash in snapshot",
		},
		{
			name:        "dot in mirror",
			mirror:      ".",
			snapshot:    "test",
			shouldFail:  true,
			description: "should reject dot as mirror name",
		},
		{
			name:        "empty mirror",
			mirror:      "",
			snapshot:    "test",
			shouldFail:  true,
			description: "should reject empty mirror name",
		},
		{
			name:        "empty snapshot",
			mirror:      "ubuntu",
			snapshot:    "",
			shouldFail:  true,
			description: "should reject empty snapshot name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sm.GetSnapshotPath(tc.mirror, tc.snapshot)

			if tc.shouldFail {
				if err == nil {
					t.Errorf("%s: expected error but got none", tc.description)
				}
			} else {
				if err != nil {
					t.Errorf("%s: expected success but got error: %v", tc.description, err)
				}
			}
		})
	}

	// Also test GetMirrorSnapshotsPath
	for _, tc := range testCases {
		if tc.mirror == "" {
			continue // Skip empty mirror test for this function
		}
		t.Run(tc.name+"_mirror_path", func(t *testing.T) {
			_, err := sm.GetMirrorSnapshotsPath(tc.mirror)

			// Should fail if mirror name is invalid
			mirrorInvalid := tc.mirror == "../../etc" ||
				tc.mirror == "/etc/passwd" ||
				tc.mirror == "ubuntu/malicious" ||
				tc.mirror == "." ||
				tc.mirror == ""

			if mirrorInvalid {
				if err == nil {
					t.Errorf("%s: expected error for GetMirrorSnapshotsPath but got none", tc.description)
				}
			} else {
				if err != nil {
					t.Errorf("%s: expected success for GetMirrorSnapshotsPath but got error: %v", tc.description, err)
				}
			}
		})
	}
}

func TestSnapshotManager_DeleteWithForce(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()

	// Create test mirror data structure
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}

	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create snapshot manager
	config := &SnapshotConfig{}
	sm := NewSnapshotManager(config, livePath)

	// Create live symlink and snapshots
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	_, err := sm.CreateSnapshot("test-mirror", "snapshot-published", false, nil)
	if err != nil {
		t.Fatalf("failed to create published snapshot: %v", err)
	}

	_, err = sm.CreateSnapshot("test-mirror", "snapshot-staged", false, nil)
	if err != nil {
		t.Fatalf("failed to create staged snapshot: %v", err)
	}

	_, err = sm.CreateSnapshot("test-mirror", "snapshot-both", false, nil)
	if err != nil {
		t.Fatalf("failed to create both snapshot: %v", err)
	}

	// Remove the temporary symlink
	os.Remove(filepath.Join(livePath, "test-mirror"))

	// Test 1: Published snapshot - should fail without force, succeed with force
	err = sm.PublishSnapshot("test-mirror", "snapshot-published")
	if err != nil {
		t.Fatalf("failed to publish snapshot: %v", err)
	}

	// Should fail without force
	err = sm.DeleteSnapshot("test-mirror", "snapshot-published", false)
	if err == nil {
		t.Error("deleting published snapshot should fail without --force")
	}
	if !strings.Contains(err.Error(), "use --force to override") {
		t.Errorf("error message should suggest --force, got: %v", err)
	}

	// Should succeed with force and remove live symlink
	err = sm.DeleteSnapshot("test-mirror", "snapshot-published", true)
	if err != nil {
		t.Errorf("deleting published snapshot should succeed with --force: %v", err)
	}

	// Verify live symlink was removed
	liveMirrorPath := sm.GetLivePath("test-mirror")
	if _, err := os.Lstat(liveMirrorPath); !os.IsNotExist(err) {
		t.Error("live symlink should be removed when force-deleting published snapshot")
	}

	// Test 2: Staged snapshot - should fail without force, succeed with force
	err = sm.PublishSnapshotToStaging("test-mirror", "snapshot-staged")
	if err != nil {
		t.Fatalf("failed to stage snapshot: %v", err)
	}

	// Should fail without force
	err = sm.DeleteSnapshot("test-mirror", "snapshot-staged", false)
	if err == nil {
		t.Error("deleting staged snapshot should fail without --force")
	}
	if !strings.Contains(err.Error(), "use --force to override") {
		t.Errorf("error message should suggest --force, got: %v", err)
	}

	// Should succeed with force and remove staging symlink
	err = sm.DeleteSnapshot("test-mirror", "snapshot-staged", true)
	if err != nil {
		t.Errorf("deleting staged snapshot should succeed with --force: %v", err)
	}

	// Verify staging symlink was removed
	stagingPath := sm.GetStagingPath("test-mirror")
	if _, err := os.Lstat(stagingPath); !os.IsNotExist(err) {
		t.Error("staging symlink should be removed when force-deleting staged snapshot")
	}

	// Test 3: Both published and staged - should remove both symlinks
	err = sm.PublishSnapshot("test-mirror", "snapshot-both")
	if err != nil {
		t.Fatalf("failed to publish snapshot: %v", err)
	}

	err = sm.PublishSnapshotToStaging("test-mirror", "snapshot-both")
	if err != nil {
		t.Fatalf("failed to stage snapshot: %v", err)
	}

	// Should fail without force
	err = sm.DeleteSnapshot("test-mirror", "snapshot-both", false)
	if err == nil {
		t.Error("deleting published+staged snapshot should fail without --force")
	}

	// Should succeed with force and remove both symlinks
	err = sm.DeleteSnapshot("test-mirror", "snapshot-both", true)
	if err != nil {
		t.Errorf("deleting published+staged snapshot should succeed with --force: %v", err)
	}

	// Verify both symlinks were removed
	if _, err := os.Lstat(liveMirrorPath); !os.IsNotExist(err) {
		t.Error("live symlink should be removed when force-deleting published+staged snapshot")
	}
	if _, err := os.Lstat(stagingPath); !os.IsNotExist(err) {
		t.Error("staging symlink should be removed when force-deleting published+staged snapshot")
	}

	// Verify all snapshots were actually deleted
	snapshots, err := sm.ListSnapshots("test-mirror")
	if err != nil {
		t.Fatalf("failed to list snapshots: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected no snapshots after force deletion, got %d", len(snapshots))
	}
}

func TestSnapshotManager_AutoGeneratedNameValidation(t *testing.T) {
	// This test verifies that auto-generated snapshot names with various formats
	// pass validation correctly. Snapshot names support uppercase letters to
	// allow standard date formats like RFC3339.

	tmpDir := t.TempDir()
	livePath := filepath.Join(tmpDir, "live")
	mirrorDataPath := filepath.Join(tmpDir, "mirror-data", "test-mirror")
	testFile := filepath.Join(mirrorDataPath, "test.txt")

	// Create test mirror data
	if err := os.MkdirAll(mirrorDataPath, 0755); err != nil {
		t.Fatalf("failed to create mirror data dir: %v", err)
	}
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create live symlink
	_ = os.MkdirAll(livePath, 0755)
	_ = os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	testCases := []struct {
		name          string
		format        string
		shouldSucceed bool
		description   string
	}{
		{
			name:          "default RFC3339 format with uppercase T and Z",
			format:        "2006-01-02T15-04-05Z",
			shouldSucceed: true,
			description:   "Default RFC3339-style format with uppercase should pass validation",
		},
		{
			name:          "lowercase format",
			format:        "2006-01-02t15-04-05z",
			shouldSucceed: true,
			description:   "Lowercase version should also pass validation",
		},
		{
			name:          "simple date format",
			format:        "2006-01-02",
			shouldSucceed: true,
			description:   "Simple date format should pass validation",
		},
		{
			name:          "unix timestamp format",
			format:        "20060102-150405",
			shouldSucceed: true,
			description:   "Unix-style timestamp should pass validation",
		},
		{
			name:          "format with underscores",
			format:        "2006_01_02_15_04_05",
			shouldSucceed: true,
			description:   "Format with underscores should pass validation",
		},
		{
			name:          "mixed case format",
			format:        "2006Jan02-Mon",
			shouldSucceed: true,
			description:   "Mixed case month/day names should pass validation",
		},
		{
			name:          "format with colons should fail",
			format:        "2006-01-02T15:04:05Z",
			shouldSucceed: false,
			description:   "Format with colons should fail (colons not allowed in filenames)",
		},
		{
			name:          "format with slashes should fail",
			format:        "2006/01/02",
			shouldSucceed: false,
			description:   "Format with slashes should fail (path separator)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &SnapshotConfig{
				DefaultNameFormat: tc.format,
			}
			sm := NewSnapshotManager(config, livePath)

			// Call CreateSnapshot with empty string to trigger auto-generation
			snapshotName, err := sm.CreateSnapshot("test-mirror", "", false, nil)

			if tc.shouldSucceed {
				if err != nil {
					t.Errorf("%s: expected success but got error: %v", tc.description, err)
				}
				if snapshotName == "" {
					t.Errorf("%s: expected non-empty snapshot name", tc.description)
				}
				// Verify the generated name passes validation
				if err := ValidateSnapshotName(snapshotName); err != nil {
					t.Errorf("%s: generated name %q should pass validation but got: %v", tc.description, snapshotName, err)
				}
			} else {
				if err == nil {
					t.Errorf("%s: expected error but got success with snapshot name: %q", tc.description, snapshotName)
				}
				if !strings.Contains(err.Error(), "invalid snapshot name") {
					t.Errorf("%s: expected 'invalid snapshot name' error but got: %v", tc.description, err)
				}
			}

			// Clean up created snapshots
			if snapshotName != "" {
				sm.DeleteSnapshot("test-mirror", snapshotName, true)
			}
		})
	}
}

func TestSnapshotManager_GenerateSnapshotNameValidation(t *testing.T) {
	// Test that generated snapshot names always pass validation
	tmpDir := t.TempDir()
	livePath := filepath.Join(tmpDir, "live")

	testCases := []struct {
		name        string
		format      string
		shouldPass  bool
		description string
	}{
		{
			name:        "default empty config",
			format:      "",
			shouldPass:  true,
			description: "Default format from empty config should generate valid names",
		},
		{
			name:        "uppercase letters in format",
			format:      "2006-01-02T15-04-05Z",
			shouldPass:  true,
			description: "Format with uppercase letters generates valid names (uppercase now allowed)",
		},
		{
			name:        "all lowercase format",
			format:      "2006-01-02t15-04-05z",
			shouldPass:  true,
			description: "All lowercase format generates valid names",
		},
		{
			name:        "mixed case format",
			format:      "Mon-Jan-02-2006",
			shouldPass:  true,
			description: "Mixed case with month/day names generates valid names",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &SnapshotConfig{
				DefaultNameFormat: tc.format,
			}
			sm := NewSnapshotManager(config, livePath)

			generatedName := sm.GenerateSnapshotName()
			err := ValidateSnapshotName(generatedName)

			if tc.shouldPass {
				if err != nil {
					t.Errorf("%s: generated name %q should be valid but got error: %v", tc.description, generatedName, err)
				}
			} else {
				if err == nil {
					t.Errorf("%s: generated name %q should be invalid but passed validation", tc.description, generatedName)
				}
			}
		})
	}
}

func TestValidateSnapshotName_AllowsUppercase(t *testing.T) {
	// Test that snapshot names allow uppercase while mirror IDs do not
	testCases := []struct {
		name               string
		input              string
		snapshotShouldPass bool
		mirrorIDShouldPass bool
		description        string
	}{
		{
			name:               "lowercase only",
			input:              "test-snapshot-123",
			snapshotShouldPass: true,
			mirrorIDShouldPass: true,
			description:        "Lowercase should pass both validations",
		},
		{
			name:               "uppercase letters",
			input:              "Test-Snapshot-123",
			snapshotShouldPass: true,
			mirrorIDShouldPass: false,
			description:        "Uppercase should pass snapshot validation but fail mirror ID validation",
		},
		{
			name:               "RFC3339-style timestamp",
			input:              "2025-12-29T19-54-21Z",
			snapshotShouldPass: true,
			mirrorIDShouldPass: false,
			description:        "RFC3339 timestamp should pass snapshot validation but fail mirror ID validation",
		},
		{
			name:               "all uppercase",
			input:              "PRODUCTION",
			snapshotShouldPass: true,
			mirrorIDShouldPass: false,
			description:        "All uppercase should pass snapshot validation but fail mirror ID validation",
		},
		{
			name:               "mixed case with numbers",
			input:              "Snapshot-2025-v1",
			snapshotShouldPass: true,
			mirrorIDShouldPass: false,
			description:        "Mixed case should pass snapshot validation but fail mirror ID validation",
		},
		{
			name:               "with slashes should fail both",
			input:              "test/snapshot",
			snapshotShouldPass: false,
			mirrorIDShouldPass: false,
			description:        "Slashes should fail both validations",
		},
		{
			name:               "with dots should fail both",
			input:              "../snapshot",
			snapshotShouldPass: false,
			mirrorIDShouldPass: false,
			description:        "Path traversal attempts should fail both validations",
		},
		{
			name:               "with colons should fail both",
			input:              "2025-12-29T19:54:21Z",
			snapshotShouldPass: false,
			mirrorIDShouldPass: false,
			description:        "Colons should fail both validations",
		},
		{
			name:               "empty string should fail both",
			input:              "",
			snapshotShouldPass: false,
			mirrorIDShouldPass: false,
			description:        "Empty string should fail both validations",
		},
		{
			name:               "with spaces should fail both",
			input:              "test snapshot",
			snapshotShouldPass: false,
			mirrorIDShouldPass: false,
			description:        "Spaces should fail both validations",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test snapshot validation
			snapshotErr := ValidateSnapshotName(tc.input)
			if tc.snapshotShouldPass && snapshotErr != nil {
				t.Errorf("%s: ValidateSnapshotName(%q) should pass but got error: %v", tc.description, tc.input, snapshotErr)
			}
			if !tc.snapshotShouldPass && snapshotErr == nil {
				t.Errorf("%s: ValidateSnapshotName(%q) should fail but passed", tc.description, tc.input)
			}

			// Test mirror ID validation (using ValidatePathComponent which enforces lowercase)
			mirrorErr := ValidatePathComponent(tc.input)
			if tc.mirrorIDShouldPass && mirrorErr != nil {
				t.Errorf("%s: ValidatePathComponent(%q) should pass but got error: %v", tc.description, tc.input, mirrorErr)
			}
			if !tc.mirrorIDShouldPass && mirrorErr == nil {
				t.Errorf("%s: ValidatePathComponent(%q) should fail but passed", tc.description, tc.input)
			}
		})
	}
}
