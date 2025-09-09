package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotManager_Basic(t *testing.T) {
	// Create temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create config
	config := &SnapshotConfig{
		Path:              filepath.Join(tmpDir, "snapshots"),
		DefaultNameFormat: "2006-01-02T15-04-05Z",
		Prune: SnapshotPruneConfig{
			KeepLast:   3,
			KeepWithin: "7d",
		},
	}

	livePath := filepath.Join(tmpDir, "live")
	sm := NewSnapshotManager(config, livePath)

	// Test path generation
	expectedSnapshotPath := filepath.Join(tmpDir, "snapshots", "test-mirror", "snapshot-1")
	actualSnapshotPath := sm.GetSnapshotPath("test-mirror", "snapshot-1")
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path:              filepath.Join(tmpDir, "snapshots"),
		DefaultNameFormat: "test-snapshot",
	}
	sm := NewSnapshotManager(config, livePath)

	// Temporarily create live symlink for CreateSnapshot to work
	os.MkdirAll(livePath, 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create snapshot
	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Verify snapshot file exists
	snapshotFile := filepath.Join(sm.GetSnapshotPath("test-mirror", "snapshot-1"), "test.txt")
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path: filepath.Join(tmpDir, "snapshots"),
	}
	sm := NewSnapshotManager(config, livePath)

	// Create snapshots from mirror data (temporarily create symlink for CreateSnapshot)
	os.MkdirAll(filepath.Join(livePath), 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
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

	expectedTarget := sm.GetSnapshotPath("test-mirror", "snapshot-1")
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path:              filepath.Join(tmpDir, "snapshots"),
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
	os.MkdirAll(livePath, 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path: filepath.Join(tmpDir, "snapshots"),
		Prune: SnapshotPruneConfig{
			KeepLast:   2,
			KeepWithin: "1s", // Very short for testing
		},
	}
	sm := NewSnapshotManager(config, livePath)

	// Temporarily create live symlink for CreateSnapshot to work
	os.MkdirAll(livePath, 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create multiple snapshots
	for i := 1; i <= 5; i++ {
		snapshotName := fmt.Sprintf("snapshot-%d", i)
		_, err = sm.CreateSnapshot("test-mirror", snapshotName, false, nil)
		if err != nil {
			t.Fatalf("failed to create %s: %v", snapshotName, err)
		}
		time.Sleep(1100 * time.Millisecond) // Ensure different timestamps (filesystem resolution)
	}

	// Publish one snapshot
	err = sm.PublishSnapshot("test-mirror", "snapshot-3")
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path: filepath.Join(tmpDir, "snapshots"),
	}
	sm := NewSnapshotManager(config, livePath)

	// Create live symlink for CreateSnapshot to work
	os.MkdirAll(livePath, 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create snapshot
	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
		Path: filepath.Join(tmpDir, "snapshots"),
		Prune: SnapshotPruneConfig{
			KeepLast:   1,
			KeepWithin: "1s", // Very short for testing
		},
	}
	sm := NewSnapshotManager(config, livePath)

	// Create live symlink for CreateSnapshot to work
	os.MkdirAll(livePath, 0755)
	os.Symlink(mirrorDataPath, filepath.Join(livePath, "test-mirror"))

	// Create multiple snapshots
	_, err = sm.CreateSnapshot("test-mirror", "snapshot-1", false, nil)
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
	tmpDir, err := os.MkdirTemp("", "snapshot-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create snapshot manager
	config := &SnapshotConfig{
		Path: filepath.Join(tmpDir, "snapshots"),
	}
	sm := NewSnapshotManager(config, filepath.Join(tmpDir, "live"))

	// Try to promote when nothing is staged (should fail)
	_, err = sm.PromoteSnapshot("test-mirror")
	if err == nil {
		t.Error("should fail when nothing is staged")
	}

	// Test GetCurrentlyStaged when nothing is staged
	_, err = sm.GetCurrentlyStaged("test-mirror")
	if err == nil {
		t.Error("should fail when nothing is staged")
	}
}
