package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SnapshotConfig defines global snapshot configuration
type SnapshotConfig struct {
	DefaultNameFormat string              `toml:"default_name_format"`
	Prune             SnapshotPruneConfig `toml:"prune"`
}

// SnapshotPruneConfig defines retention policies
type SnapshotPruneConfig struct {
	KeepLast   int    `toml:"keep_last"`
	KeepWithin string `toml:"keep_within"`
}

// MirrorSnapshotConfig defines per-mirror snapshot overrides
//
//revive:disable:exported
type MirrorSnapshotConfig struct {
	DefaultNameFormat string               `toml:"default_name_format,omitempty"`
	Prune             *SnapshotPruneConfig `toml:"prune,omitempty"`
}

// SnapshotManager handles snapshot operations
type SnapshotManager struct {
	config       *SnapshotConfig
	livePath     string // Base path where live mirrors are symlinked (e.g., /var/www/apt)
	snapshotPath string // Path where snapshots are stored (always .snapshots sibling to livePath)
}

// SnapshotInfo represents a snapshot
type SnapshotInfo struct {
	Name        string
	Mirror      string
	Path        string
	CreatedAt   time.Time
	IsPublished bool
	IsStaged    bool
	Size        int64
	FileCount   int
}

// Status returns a human-readable status string for the snapshot
func (s *SnapshotInfo) Status() string {
	var statusParts []string
	if s.IsPublished {
		statusParts = append(statusParts, "published")
	}
	if s.IsStaged {
		statusParts = append(statusParts, "staged")
	}

	if len(statusParts) == 0 {
		return ""
	}

	return "(" + strings.Join(statusParts, ", ") + ")"
}

// ValidatePathComponent ensures a string is safe to use as a path component.
// This prevents path traversal attacks by enforcing the same validation as mirror IDs.
// Valid components must match: ^[a-z0-9_-]+$
func ValidatePathComponent(component string) error {
	if component == "" {
		return fmt.Errorf("path component cannot be empty")
	}

	if !IsValidID(component) {
		return fmt.Errorf("path component must contain only lowercase letters, numbers, hyphens, and underscores (got: %q)", component)
	}

	return nil
}

// NewSnapshotManager creates a new snapshot manager.
// The snapshot path is always set to a .snapshots directory as a sibling
// to the mirror directory (e.g., if livePath is /var/www/mirrors, snapshots
// will be stored in /var/www/.snapshots). This ensures snapshots are on the
// same filesystem as mirrors (required for hard links) and keeps related data
// co-located for easier management.
func NewSnapshotManager(config *SnapshotConfig, livePath string) *SnapshotManager {
	// Always place snapshots as a sibling to the mirror directory
	// e.g., /var/www/mirrors -> /var/www/.snapshots
	// This is NOT configurable for security reasons
	snapshotPath := filepath.Join(filepath.Dir(livePath), ".snapshots")

	// Set defaults if not configured
	if config.DefaultNameFormat == "" {
		config.DefaultNameFormat = "2006-01-02T15-04-05Z"
	}
	if config.Prune.KeepLast == 0 {
		config.Prune.KeepLast = 5
	}
	if config.Prune.KeepWithin == "" {
		config.Prune.KeepWithin = "30d"
	}

	return &SnapshotManager{
		config:       config,
		livePath:     livePath,
		snapshotPath: snapshotPath,
	}
}

// GetSnapshotPath returns the path for a specific snapshot.
// Returns an error if the mirror or snapshot names contain invalid characters
// or if the resolved path would escape the snapshot directory.
func (sm *SnapshotManager) GetSnapshotPath(mirror, snapshot string) (string, error) {
	// Validate inputs
	if err := ValidatePathComponent(mirror); err != nil {
		return "", fmt.Errorf("invalid mirror ID: %w", err)
	}
	if err := ValidatePathComponent(snapshot); err != nil {
		return "", fmt.Errorf("invalid snapshot name: %w", err)
	}

	// Construct path
	path := filepath.Join(sm.snapshotPath, mirror, snapshot)

	// Verify containment (defense in depth)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	absBase, err := filepath.Abs(sm.snapshotPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base path: %w", err)
	}

	// Ensure resolved path is still within snapshot directory
	relPath, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("path traversal attempt detected")
	}

	return path, nil
}

// GetMirrorSnapshotsPath returns the path containing all snapshots for a mirror.
// Returns an error if the mirror name contains invalid characters.
func (sm *SnapshotManager) GetMirrorSnapshotsPath(mirror string) (string, error) {
	// Validate input
	if err := ValidatePathComponent(mirror); err != nil {
		return "", fmt.Errorf("invalid mirror ID: %w", err)
	}

	// Construct path
	path := filepath.Join(sm.snapshotPath, mirror)

	// Verify containment (defense in depth)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	absBase, err := filepath.Abs(sm.snapshotPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base path: %w", err)
	}

	// Ensure resolved path is still within snapshot directory
	relPath, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("path traversal attempt detected")
	}

	return path, nil
}

// GetLivePath returns the path for the live mirror symlink
func (sm *SnapshotManager) GetLivePath(mirror string) string {
	return filepath.Join(sm.livePath, mirror)
}

// GetStagingPath returns the path for the staging mirror symlink
func (sm *SnapshotManager) GetStagingPath(mirror string) string {
	return filepath.Join(sm.livePath, mirror+"-staging")
}

// GenerateSnapshotName generates a snapshot name using the configured format
func (sm *SnapshotManager) GenerateSnapshotName() string {
	return sm.GenerateSnapshotNameForMirror(nil)
}

// GenerateSnapshotNameForMirror generates a snapshot name using per-mirror format if available
func (sm *SnapshotManager) GenerateSnapshotNameForMirror(mirrorConfig *MirrorSnapshotConfig) string {
	format := sm.config.DefaultNameFormat

	// Override with mirror-specific format if provided
	if mirrorConfig != nil && mirrorConfig.DefaultNameFormat != "" {
		format = mirrorConfig.DefaultNameFormat
	}

	return time.Now().UTC().Format(format)
}

// GetCurrentlyPublished returns the name of the currently published snapshot for a mirror
func (sm *SnapshotManager) GetCurrentlyPublished(mirror string) (string, error) {
	livePath := sm.GetLivePath(mirror)

	// Read the symlink target
	target, err := os.Readlink(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no published snapshot for mirror %s", mirror)
		}
		return "", fmt.Errorf("failed to read live symlink for mirror %s: %w", mirror, err)
	}

	// Extract snapshot name from target path
	snapshotName := filepath.Base(target)
	return snapshotName, nil
}

// GetCurrentlyStaged returns the name of the currently staged snapshot for a mirror
func (sm *SnapshotManager) GetCurrentlyStaged(mirror string) (string, error) {
	stagingPath := sm.GetStagingPath(mirror)

	// Read the symlink target
	target, err := os.Readlink(stagingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no staged snapshot for mirror %s", mirror)
		}
		return "", fmt.Errorf("failed to read staging symlink for mirror %s: %w", mirror, err)
	}

	// Extract snapshot name from target path
	snapshotName := filepath.Base(target)
	return snapshotName, nil
}

// ListSnapshots returns all snapshots for a mirror
func (sm *SnapshotManager) ListSnapshots(mirror string) ([]*SnapshotInfo, error) {
	snapshotsPath, err := sm.GetMirrorSnapshotsPath(mirror)
	if err != nil {
		return nil, err
	}

	// Check if snapshots directory exists
	if _, err := os.Stat(snapshotsPath); os.IsNotExist(err) {
		return []*SnapshotInfo{}, nil
	}

	entries, err := os.ReadDir(snapshotsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots for mirror %s: %w", mirror, err)
	}

	var snapshots []*SnapshotInfo
	currentlyPublished, _ := sm.GetCurrentlyPublished(mirror)
	currentlyStaged, _ := sm.GetCurrentlyStaged(mirror)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		snapshotPath := filepath.Join(snapshotsPath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue // Skip entries we can't stat
		}

		// Calculate size and file count
		size, fileCount := sm.calculateSnapshotSize(snapshotPath)

		snapshot := &SnapshotInfo{
			Name:        entry.Name(),
			Mirror:      mirror,
			Path:        snapshotPath,
			CreatedAt:   info.ModTime(),
			IsPublished: entry.Name() == currentlyPublished,
			IsStaged:    entry.Name() == currentlyStaged,
			Size:        size,
			FileCount:   fileCount,
		}

		snapshots = append(snapshots, snapshot)
	}

	// Sort by creation time (newest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.After(snapshots[j].CreatedAt)
	})

	return snapshots, nil
}

// calculateSnapshotSize calculates the total size and file count of a snapshot
func (sm *SnapshotManager) calculateSnapshotSize(path string) (int64, int) {
	var totalSize int64
	var fileCount int

	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			totalSize += info.Size()
			fileCount++
		}
		return nil
	})

	return totalSize, fileCount
}

// CreateSnapshot creates a new snapshot by hard-linking files from the live mirror
// Returns the actual snapshot name that was used
func (sm *SnapshotManager) CreateSnapshot(mirror, snapshotName string, force bool, mirrorConfig *MirrorSnapshotConfig) (string, error) {
	if snapshotName == "" {
		snapshotName = sm.GenerateSnapshotNameForMirror(mirrorConfig)
	}

	livePath := sm.GetLivePath(mirror)
	snapshotPath, err := sm.GetSnapshotPath(mirror, snapshotName)
	if err != nil {
		return "", err
	}

	// Check if live mirror exists and resolve symlink if needed
	if _, err := os.Stat(livePath); os.IsNotExist(err) {
		return "", fmt.Errorf("live mirror %s does not exist", mirror)
	}

	// If livePath is a symlink, resolve it to the actual path
	resolvedLivePath, err := filepath.EvalSymlinks(livePath)
	if err != nil {
		// If it's not a symlink, use the original path
		resolvedLivePath = livePath
	}

	// Check if snapshot already exists
	if _, err := os.Stat(snapshotPath); err == nil {
		if !force {
			return "", fmt.Errorf("snapshot %s already exists for mirror %s (use --force to overwrite)", snapshotName, mirror)
		}
		// Remove existing snapshot
		if err := os.RemoveAll(snapshotPath); err != nil {
			return "", fmt.Errorf("failed to remove existing snapshot: %w", err)
		}
	}

	// Create snapshot directory
	// #nosec G301 - 0755 needed for web server directory access
	if err := os.MkdirAll(snapshotPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Create hard links from live mirror to snapshot
	err = sm.createHardLinks(resolvedLivePath, snapshotPath)
	if err != nil {
		// Clean up on failure
		os.RemoveAll(snapshotPath) // #nosec G104 - cleanup on failure, ignore errors
		return "", fmt.Errorf("failed to create hard links: %w", err)
	}

	return snapshotName, nil
}

// createHardLinks recursively creates hard links from src to dst
func (sm *SnapshotManager) createHardLinks(src, dst string) error {
	return filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from src
		relPath, err := filepath.Rel(src, srcPath)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Skip non-regular files (symlinks, devices, etc.)
		if !info.Mode().IsRegular() {
			return nil
		}

		// Create hard link
		if err := os.Link(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to create hard link %s -> %s: %w", srcPath, dstPath, err)
		}

		return nil
	})
}

// PublishSnapshot makes a snapshot the live version by updating the symlink
func (sm *SnapshotManager) PublishSnapshot(mirror, snapshotName string) error {
	snapshotPath, err := sm.GetSnapshotPath(mirror, snapshotName)
	if err != nil {
		return err
	}
	livePath := sm.GetLivePath(mirror)

	// Verify snapshot exists
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return fmt.Errorf("snapshot %s does not exist for mirror %s", snapshotName, mirror)
	}

	// Create live directory if it doesn't exist
	liveDir := filepath.Dir(livePath)
	// #nosec G301 - 0755 needed for web server directory access
	if err := os.MkdirAll(liveDir, 0755); err != nil {
		return fmt.Errorf("failed to create live directory: %w", err)
	}

	// Create temporary symlink name
	tempLink := livePath + ".tmp"

	// Remove temporary link if it exists
	os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors

	// Create new symlink
	if err := os.Symlink(snapshotPath, tempLink); err != nil {
		return fmt.Errorf("failed to create temporary symlink: %w", err)
	}

	// Remove existing live symlink before renaming (ignore errors)
	os.Remove(livePath) // #nosec G104 - cleanup operation, errors expected and ignored

	// Atomically replace the live symlink
	if err := os.Rename(tempLink, livePath); err != nil {
		os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors // #nosec G104 - cleanup on failure, ignore errors
		return fmt.Errorf("failed to update live symlink: %w", err)
	}

	return nil
}

// PublishSnapshotToStaging makes a snapshot the staged version by updating the staging symlink
func (sm *SnapshotManager) PublishSnapshotToStaging(mirror, snapshotName string) error {
	snapshotPath, err := sm.GetSnapshotPath(mirror, snapshotName)
	if err != nil {
		return err
	}
	stagingPath := sm.GetStagingPath(mirror)

	// Verify snapshot exists
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return fmt.Errorf("snapshot %s does not exist for mirror %s", snapshotName, mirror)
	}

	// Create staging directory if it doesn't exist
	stagingDir := filepath.Dir(stagingPath)
	// #nosec G301 - 0755 needed for web server directory access
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Create temporary symlink name
	tempLink := stagingPath + ".tmp"

	// Remove temporary link if it exists
	os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors

	// Create new symlink
	if err := os.Symlink(snapshotPath, tempLink); err != nil {
		return fmt.Errorf("failed to create temporary staging symlink: %w", err)
	}

	// Remove existing staging symlink before renaming (ignore errors)
	os.Remove(stagingPath) // #nosec G104 - cleanup operation, errors expected and ignored

	// Atomically replace the staging symlink
	if err := os.Rename(tempLink, stagingPath); err != nil {
		os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors // #nosec G104 - cleanup on failure, ignore errors
		return fmt.Errorf("failed to update staging symlink: %w", err)
	}

	return nil
}

// PromoteSnapshot promotes the currently staged snapshot to production
func (sm *SnapshotManager) PromoteSnapshot(mirror string) (string, error) {
	stagingPath := sm.GetStagingPath(mirror)
	livePath := sm.GetLivePath(mirror)

	// Verify staging symlink exists
	if _, err := os.Lstat(stagingPath); os.IsNotExist(err) {
		return "", fmt.Errorf("no snapshot is currently staged for mirror %s", mirror)
	}

	// Read the staging symlink target
	target, err := os.Readlink(stagingPath)
	if err != nil {
		return "", fmt.Errorf("failed to read staging symlink for mirror %s: %w", mirror, err)
	}

	// Extract snapshot name for return value
	snapshotName := filepath.Base(target)

	// Verify the target snapshot actually exists
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return "", fmt.Errorf("staged snapshot %s does not exist for mirror %s", snapshotName, mirror)
	}

	// Create live directory if it doesn't exist
	liveDir := filepath.Dir(livePath)
	// #nosec G301 - 0755 needed for web server directory access
	if err := os.MkdirAll(liveDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create live directory: %w", err)
	}

	// Create temporary symlink name
	tempLink := livePath + ".tmp"

	// Remove temporary link if it exists
	os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors

	// Create new symlink pointing to the same target as staging
	if err := os.Symlink(target, tempLink); err != nil {
		return "", fmt.Errorf("failed to create temporary production symlink: %w", err)
	}

	// Remove existing production symlink before renaming (ignore errors)
	os.Remove(livePath) // #nosec G104 - cleanup operation, errors expected and ignored

	// Atomically replace the production symlink
	if err := os.Rename(tempLink, livePath); err != nil {
		os.Remove(tempLink) // #nosec G104 - cleanup operation, ignore errors // #nosec G104 - cleanup on failure, ignore errors
		return "", fmt.Errorf("failed to update production symlink: %w", err)
	}

	return snapshotName, nil
}

// DeleteSnapshot removes a snapshot
func (sm *SnapshotManager) DeleteSnapshot(mirror, snapshotName string, force bool) error {
	snapshotPath, err := sm.GetSnapshotPath(mirror, snapshotName)
	if err != nil {
		return err
	}

	// Verify snapshot exists
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		return fmt.Errorf("snapshot %s does not exist for mirror %s", snapshotName, mirror)
	}

	// Check if snapshot is currently published
	currentlyPublished, err := sm.GetCurrentlyPublished(mirror)
	if err == nil && currentlyPublished == snapshotName {
		if !force {
			return fmt.Errorf("cannot delete snapshot %s as it is currently published for mirror %s (use --force to override)", snapshotName, mirror)
		}
		// With --force, remove the live symlink first
		os.Remove(sm.GetLivePath(mirror)) // #nosec G104 - force cleanup, ignore errors
	}

	// Check if snapshot is currently staged
	currentlyStaged, err := sm.GetCurrentlyStaged(mirror)
	if err == nil && currentlyStaged == snapshotName {
		if !force {
			return fmt.Errorf("cannot delete snapshot %s as it is currently staged for mirror %s (use --force to override)", snapshotName, mirror)
		}
		// With --force, remove the staging symlink first
		os.Remove(sm.GetStagingPath(mirror)) // #nosec G104 - force cleanup, ignore errors
	}

	// Remove the snapshot directory
	if err := os.RemoveAll(snapshotPath); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	return nil
}

// ParseDuration parses a duration string like "30d", "1w", "2h", "2h30m"
func (sm *SnapshotManager) ParseDuration(duration string) (time.Duration, error) {
	if duration == "" {
		return 0, nil
	}

	// First try parsing as standard Go duration (handles compound durations like "2h30m")
	if parsed, err := time.ParseDuration(duration); err == nil {
		return parsed, nil
	}

	// Handle custom simple cases like "30d", "1w"
	if len(duration) < 2 {
		return 0, fmt.Errorf("invalid duration format: %s", duration)
	}

	unit := duration[len(duration)-1:]
	valueStr := duration[:len(duration)-1]

	var value int
	if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
		return 0, fmt.Errorf("invalid duration value: %s", duration)
	}

	switch strings.ToLower(unit) {
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration format: %s", duration)
	}
}

// GetPruneConfig returns the effective prune configuration for a mirror
func (sm *SnapshotManager) GetPruneConfig(_ string, mirrorConfig *MirrorSnapshotConfig) SnapshotPruneConfig {
	config := sm.config.Prune

	// Override with mirror-specific settings if available
	if mirrorConfig != nil && mirrorConfig.Prune != nil {
		if mirrorConfig.Prune.KeepLast > 0 {
			config.KeepLast = mirrorConfig.Prune.KeepLast
		}
		if mirrorConfig.Prune.KeepWithin != "" {
			config.KeepWithin = mirrorConfig.Prune.KeepWithin
		}
	}

	return config
}

// PruneSnapshots removes old snapshots according to retention policy
func (sm *SnapshotManager) PruneSnapshots(mirror string, mirrorConfig *MirrorSnapshotConfig, dryRun bool, keepLast *int, keepWithin *string) ([]string, error) {
	config := sm.GetPruneConfig(mirror, mirrorConfig)

	// Override with CLI flags if provided
	if keepLast != nil {
		config.KeepLast = *keepLast
	}
	if keepWithin != nil {
		config.KeepWithin = *keepWithin
	}

	// Get all snapshots for the mirror
	snapshots, err := sm.ListSnapshots(mirror)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	// Remove currently published and staged snapshots from deletion list
	var candidates []*SnapshotInfo
	for _, snapshot := range snapshots {
		if !snapshot.IsPublished && !snapshot.IsStaged {
			candidates = append(candidates, snapshot)
		}
	}

	// Sort by creation time (oldest first for deletion)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})

	// Apply keep-last rule
	if config.KeepLast > 0 && len(candidates) > config.KeepLast {
		// Keep the newest config.KeepLast snapshots, delete the rest (oldest)
		// Since candidates is sorted oldest->newest, delete from beginning
		candidates = candidates[:len(candidates)-config.KeepLast]
	}
	// If we have <= KeepLast candidates, don't delete any based on this rule alone

	// Apply keep-within rule
	if config.KeepWithin != "" {
		duration, err := sm.ParseDuration(config.KeepWithin)
		if err != nil {
			return nil, fmt.Errorf("invalid keep_within duration: %w", err)
		}

		cutoff := time.Now().Add(-duration)
		var filtered []*SnapshotInfo
		for _, candidate := range candidates {
			if candidate.CreatedAt.Before(cutoff) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}

	// Collect names of snapshots to delete
	var toDelete []string
	for _, candidate := range candidates {
		toDelete = append(toDelete, candidate.Name)
	}

	// If dry run, just return the list
	if dryRun {
		return toDelete, nil
	}

	// Actually delete the snapshots
	for _, snapshotName := range toDelete {
		if err := sm.DeleteSnapshot(mirror, snapshotName, false); err != nil {
			return toDelete, fmt.Errorf("failed to delete snapshot %s: %w", snapshotName, err)
		}
	}

	return toDelete, nil
}
