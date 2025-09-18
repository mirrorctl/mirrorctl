package mirror

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"golang.org/x/sync/errgroup"
)

const (
	lockFilename = ".lock"
)

// validateLockFilePath validates that a lock file path is safe for use.
// It prevents directory traversal attacks by ensuring the path is within the config directory.
func validateLockFilePath(lockFile, baseDir string) error {
	cleanLock := filepath.Clean(lockFile)
	cleanBase := filepath.Clean(baseDir)

	// Check for directory traversal attempts
	if strings.Contains(lockFile, "..") {
		return errors.New("unsafe lock file path (contains directory traversal): " + lockFile)
	}

	// Ensure lock file is within the base directory
	if !strings.HasPrefix(cleanLock, cleanBase) {
		return errors.New("lock file path outside of base directory: " + lockFile)
	}

	return nil
}

func updateMirrors(ctx context.Context, config *Config, mirrors []string, noPGPCheck, quiet, dryRun bool) ([]*Mirror, error) {
	timestamp := time.Now()

	var mirrorList []*Mirror
	for _, mirrorID := range mirrors {
		mirror, err := NewMirror(timestamp, mirrorID, config, noPGPCheck, quiet, dryRun)
		if err != nil {
			return nil, err
		}
		mirrorList = append(mirrorList, mirror)
	}

	if dryRun {
		slog.Info("dry-run mode: calculating disk usage without downloading")
	} else {
		slog.Info("update starts")
	}

	// run goroutines in an environment.
	group, ctx := errgroup.WithContext(ctx)

	for _, mirror := range mirrorList {
		mirror := mirror // capture loop variable
		group.Go(func() error {
			return mirror.Update(ctx)
		})
	}
	err := group.Wait()
	if err != nil {
		return nil, err
	}

	// Print summary in dry-run mode
	if dryRun {
		printDryRunSummary(mirrorList)
	} else {
		slog.Info("update ends")
	}
	return mirrorList, nil
}

// printDryRunSummary prints a summary of disk usage for all mirrors
func printDryRunSummary(mirrors []*Mirror) {
	fmt.Println()
	fmt.Println("=== Disk Usage Summary (Dry Run) ===")
	fmt.Println()

	// Sort mirrors alphabetically by ID for consistent output
	sort.Slice(mirrors, func(i, j int) bool {
		return mirrors[i].id < mirrors[j].id
	})

	var totalUsage UsageStats
	for _, mirror := range mirrors {
		stats := mirror.UsageStats()
		totalUsage.ReleaseFiles += stats.ReleaseFiles
		totalUsage.IndexFiles += stats.IndexFiles
		totalUsage.PackageFiles += stats.PackageFiles
		totalUsage.Total += stats.Total
		totalUsage.FileCount += stats.FileCount

		mirror.PrintUsageStats()
	}

	fmt.Printf("Total across all repositories:\n")
	fmt.Printf("  Release files:  %s\n", formatBytes(totalUsage.ReleaseFiles))
	fmt.Printf("  Index files:    %s\n", formatBytes(totalUsage.IndexFiles))
	fmt.Printf("  Package files:  %s\n", formatBytes(totalUsage.PackageFiles))
	fmt.Printf("  Total size:     %s (%d files)\n", formatBytes(totalUsage.Total), totalUsage.FileCount)
	fmt.Printf("\nNote: In dry-run mode, index files are downloaded to calculate package sizes,\n")
	fmt.Printf("but actual package files are not downloaded.\n")
	fmt.Println()
}

// gc removes old mirror files, if any.
func gc(ctx context.Context, config *Config) error {
	using := map[string]bool{
		lockFilename: true,
		".":          true,
		"..":         true,
	}

	dirEntries, err := os.ReadDir(config.Dir)
	if err != nil {
		return err
	}

	// search symlinks and its pointing directories
	for _, dirEntry := range dirEntries {
		info, err := dirEntry.Info()
		if err != nil {
			return errors.Wrap(err, "gc")
		}
		if (info.Mode() & os.ModeSymlink) == 0 {
			continue
		}
		filePath, err := filepath.EvalSymlinks(filepath.Join(config.Dir, dirEntry.Name()))
		if err != nil {
			return errors.Wrap(err, "gc")
		}

		// Validate that the resolved symlink stays within safe boundaries
		var snapshotDir string
		if config.Snapshot != nil {
			snapshotDir = config.Snapshot.Path
		}
		if err := validateSymlinkPath(filePath, config.Dir, snapshotDir); err != nil {
			return errors.Wrap(err, "gc: unsafe symlink "+dirEntry.Name())
		}

		using[dirEntry.Name()] = true
		using[filepath.Base(filepath.Dir(filePath))] = true
	}

	// remove unused dentries.
	for _, dirEntry := range dirEntries {
		if using[dirEntry.Name()] {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		filePath := filepath.Join(config.Dir, dirEntry.Name())
		slog.Info("removing old mirror", "path", filePath)
		err := os.RemoveAll(filePath)
		if err != nil {
			return errors.Wrap(err, "gc")
		}
	}

	return nil
}

// handleSnapshotting creates and stages snapshots for mirrors with publish_to_staging = true
func handleSnapshotting(config *Config, mirrors []*Mirror, force bool) error {
	snapshotManager := NewSnapshotManager(config.Snapshot, config.Dir)

	for _, mirror := range mirrors {
		mirrorConfig := config.Mirrors[mirror.id]

		// Check if this mirror should be staged
		if !mirrorConfig.PublishToStaging {
			continue
		}

		slog.Info("creating snapshot for staging", "repo", mirror.id)

		// Create snapshot with auto-generated name
		snapshotName, err := snapshotManager.CreateSnapshot(mirror.id, "", force, mirrorConfig.Snapshot)
		if err != nil {
			slog.Error("failed to create snapshot", "repo", mirror.id, "error", err)
			continue
		}

		// Publish to staging
		err = snapshotManager.PublishSnapshotToStaging(mirror.id, snapshotName)
		if err != nil {
			slog.Error("failed to stage snapshot", "repo", mirror.id, "snapshot", snapshotName, "error", err)
			continue
		}

		slog.Info("snapshot staged successfully", "repo", mirror.id, "snapshot", snapshotName)
	}

	return nil
}

// Run starts mirroring.
//
// The first thing to do is to acquire flock on the lock file.
//
// mirrors is a list of mirror IDs defined in the configuration file
// (or keys in c.Mirrors).  If mirrors is an empty list, all mirrors
// will be updated.
func Run(config *Config, mirrors []string, noPGPCheck, quiet, dryRun, force bool) error {
	lockFile := filepath.Join(config.Dir, lockFilename)

	// Validate lock file path for security
	if err := validateLockFilePath(lockFile, config.Dir); err != nil {
		return errors.Wrap(err, "Run")
	}

	file, err := os.Open(lockFile) // #nosec G304 - lockFile path is validated by validateLockFilePath
	switch {
	case os.IsNotExist(err):
		file2, err := os.OpenFile(lockFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644) // #nosec G304,G302 - lockFile path validated, 0644 standard for lock files
		if err != nil {
			return err
		}
		file = file2
	case err != nil:
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("failed to close lock file", "error", err)
		}
	}()

	fileLock := Flock{file}
	err = fileLock.Lock()
	if err != nil {
		return err
	}
	defer func() {
		if err := fileLock.Unlock(); err != nil {
			slog.Warn("failed to unlock file", "error", err)
		}
	}()

	// Clean up the lock file when the process completes
	defer func() {
		if err := os.Remove(lockFile); err != nil {
			slog.Warn("failed to remove lock file", "error", err, "path", lockFile)
		}
	}()

	if len(mirrors) == 0 {
		for mirrorID := range config.Mirrors {
			mirrors = append(mirrors, mirrorID)
		}
	}

	group, ctx := errgroup.WithContext(context.Background())
	group.Go(func() error {
		updatedMirrors, err := updateMirrors(ctx, config, mirrors, noPGPCheck, quiet, dryRun)
		if err != nil {
			if gcErr := gc(ctx, config); gcErr != nil {
				err = errors.Wrap(err, gcErr.Error())
			}
			return err
		}

		// Handle snapshotting for mirrors with publish_to_staging = true
		if !dryRun && config.Snapshot != nil {
			err = handleSnapshotting(config, updatedMirrors, force)
			if err != nil {
				slog.Warn("snapshot creation failed", "error", err)
				// Don't fail the entire sync for snapshot errors
			}
		}

		return gc(ctx, config)
	})
	return group.Wait()
}
