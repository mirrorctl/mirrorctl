package mirror

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/mirrorctl/mirrorctl/internal/apt"
)

const (
	timestampFormat  = "20060102_150405.000000"
	progressInterval = 5 * time.Minute
	httpRetries      = 5
)

var (
	validID = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

// UsageStats tracks disk usage statistics for a mirror.
type UsageStats struct {
	mu           sync.Mutex
	ReleaseFiles uint64 // Size of Release/InRelease files
	IndexFiles   uint64 // Size of Packages/Sources files
	PackageFiles uint64 // Size of .deb/.tar.gz files
	Total        uint64 // Total size
	FileCount    int    // Total number of files
}

// AddReleaseFile adds statistics for a release file in a thread-safe manner.
func (us *UsageStats) AddReleaseFile(size uint64) {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.ReleaseFiles += size
	us.Total += size
	us.FileCount++
}

// AddIndexFile adds statistics for an index file in a thread-safe manner.
func (us *UsageStats) AddIndexFile(size uint64) {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.IndexFiles += size
	us.Total += size
	us.FileCount++
}

// AddPackageFile adds statistics for a package file in a thread-safe manner.
func (us *UsageStats) AddPackageFile(size uint64) {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.PackageFiles += size
	us.Total += size
	us.FileCount++
}

// GetStats returns a copy of the current statistics in a thread-safe manner.
func (us *UsageStats) GetStats() UsageStats {
	us.mu.Lock()
	defer us.mu.Unlock()
	return UsageStats{
		ReleaseFiles: us.ReleaseFiles,
		IndexFiles:   us.IndexFiles,
		PackageFiles: us.PackageFiles,
		Total:        us.Total,
		FileCount:    us.FileCount,
	}
}

// IsValidID checks if the given ID is valid.
func IsValidID(id string) bool {
	return validID.MatchString(id)
}

// validateSymlinkPath validates that a resolved symlink path stays within allowed directories.
// This prevents symlink attacks that could access files outside the intended directory structure.
// It allows paths within baseDir or snapshotDir (if provided).
func validateSymlinkPath(resolvedPath, baseDir string, allowedDirs ...string) error {
	// Clean the resolved path
	cleanResolved := filepath.Clean(resolvedPath)

	// Check if the resolved path is within the base directory
	cleanBase := filepath.Clean(baseDir)
	rel, err := filepath.Rel(cleanBase, cleanResolved)
	if err == nil && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, ".."+string(filepath.Separator)) {
		return nil // Path is within base directory
	}

	// Check against additional allowed directories (e.g., snapshot directory)
	for _, allowedDir := range allowedDirs {
		if allowedDir == "" {
			continue
		}
		cleanAllowed := filepath.Clean(allowedDir)
		rel, err := filepath.Rel(cleanAllowed, cleanResolved)
		if err == nil && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, ".."+string(filepath.Separator)) {
			return nil // Path is within allowed directory
		}
	}

	return errors.New("unsafe symlink: resolved path outside allowed directories")
}

// Mirror implements mirroring logics.
type Mirror struct {
	id         string
	dir        string
	mc         *MirrorConfig
	storage    *Storage
	current    *Storage
	httpClient *HTTPClient
	parser     *APTParser
	noPGPCheck bool
	quiet      bool
	dryRun     bool
	usageStats *UsageStats
}

// NewMirror constructs a Mirror for given mirror id.
func NewMirror(timestamp time.Time, mirrorID string, config *Config, noPGPCheck, quiet, dryRun bool) (*Mirror, error) {
	directory := filepath.Clean(config.Dir)

	mirrorConfig, ok := config.Mirrors[mirrorID]
	if !ok {
		return nil, errors.New("no such mirror: " + mirrorID)
	}

	// sanity checks
	if !IsValidID(mirrorID) {
		return nil, errors.New("invalid id: " + mirrorID)
	}
	if err := mirrorConfig.Check(); err != nil {
		return nil, errors.Wrap(err, mirrorID)
	}

	var currentStorage *Storage
	currentDir, err := filepath.EvalSymlinks(filepath.Join(directory, mirrorID))
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, errors.Wrap(err, mirrorID)
	default:
		// Validate that the resolved symlink stays within safe boundaries
		var snapshotDir string
		if config.Snapshot != nil {
			snapshotDir = config.Snapshot.Path
		}
		if err := validateSymlinkPath(currentDir, directory, snapshotDir); err != nil {
			return nil, errors.Wrap(err, "NewMirror: "+mirrorID)
		}

		currentStorage, err = NewStorage(filepath.Dir(currentDir), mirrorID)
		if err != nil {
			return nil, errors.Wrap(err, mirrorID)
		}
		err = currentStorage.Load()
		if err != nil {
			return nil, errors.Wrap(err, mirrorID)
		}
	}

	storageDirectory := filepath.Join(directory, "."+mirrorID+"."+timestamp.Format(timestampFormat))
	err = os.Mkdir(storageDirectory, 0750)
	if err != nil && !os.IsExist(err) {
		return nil, errors.Wrap(err, mirrorID)
	}
	storage, err := NewStorage(storageDirectory, mirrorID)
	if err != nil {
		return nil, errors.Wrap(err, mirrorID)
	}

	// Create components
	httpClient := NewHTTPClient(config.MaxConns, mirrorID, storage, currentStorage, &config.TLS)
	parser := NewAPTParser(storage, mirrorConfig, mirrorID)

	mirror := &Mirror{
		id:         mirrorID,
		dir:        directory,
		mc:         mirrorConfig,
		storage:    storage,
		current:    currentStorage,
		httpClient: httpClient,
		parser:     parser,
		noPGPCheck: noPGPCheck,
		quiet:      quiet,
		dryRun:     dryRun,
		usageStats: &UsageStats{},
	}
	return mirror, nil
}

func (m *Mirror) replaceLink() error {
	tname := filepath.Join(m.dir, m.id+".tmp")
	os.Remove(tname)
	err := os.Symlink(filepath.Join(m.storage.Dir(), m.id), tname)
	if err != nil {
		return err
	}

	// symlink exists only in dentry
	err = DirSync(m.dir)
	if err != nil {
		return err
	}

	err = os.Rename(tname, filepath.Join(m.dir, m.id))
	if err != nil {
		return err
	}

	return DirSync(m.dir)
}

// UsageStats returns the usage statistics for this mirror.
func (m *Mirror) UsageStats() UsageStats {
	return m.usageStats.GetStats()
}

// PrintUsageStats prints usage statistics for this mirror.
func (m *Mirror) PrintUsageStats() {
	stats := m.usageStats.GetStats()
	fmt.Printf("Repository: %s\n", m.id)
	fmt.Printf("  Release files:  %s\n", formatBytes(stats.ReleaseFiles))
	fmt.Printf("  Index files:    %s\n", formatBytes(stats.IndexFiles))
	fmt.Printf("  Package files:  %s\n", formatBytes(stats.PackageFiles))
	fmt.Printf("  Total size:     %s (%d files)\n", formatBytes(stats.Total), stats.FileCount)
	fmt.Println()
}

// Update updates mirrored files.
func (m *Mirror) Update(ctx context.Context) error {
	itemMap := make(map[string]*apt.FileInfo)

	for _, suite := range m.mc.Suites {
		err := m.updateSuite(ctx, suite, itemMap, m.quiet)
		if err != nil {
			return err
		}
	}

	// All files are downloaded via updateSuite -> parser.downloadItems

	if m.dryRun {
		// In dry-run mode, skip storage operations
		return nil
	}

	// all files are downloaded (or reused)
	err := m.storage.Save()
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	// replace the symlink atomically
	err = m.replaceLink()
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	slog.Info("update succeeded", "repo", m.id)
	return nil
}

// updateSuite partially updates mirror for a suite.
func (m *Mirror) updateSuite(ctx context.Context, suite string, itemMap map[string]*apt.FileInfo, quiet bool) error {
	slog.Info("downloading Release/InRelease files", "repo", m.id, "suite", suite)
	slog.Debug("processing suite", "repo", m.id, "suite", suite, "sections", m.mc.Sections, "architectures", m.mc.Architectures)
	indexMap, byhash, err := m.parser.downloadRelease(ctx, m.httpClient, suite, m)
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	if len(indexMap) == 0 {
		return errors.New(m.id + ": found no Release/InRelease")
	}

	slog.Debug("release files parsed", "repo", m.id, "suite", suite, "by_hash", byhash, "index_files", len(indexMap))

	// WORKAROUND: some (zabbix) repositories returns wrong contents
	// for non-existent files such as Sources (looks like the body of
	// Sources.gz is returned).
	if !m.mc.Source {
		tmpMap := make(map[string][]*apt.FileInfo)
		for p, fil := range indexMap {
			base := path.Base(p)
			base = base[0 : len(base)-len(path.Ext(base))]
			if base == "Sources" {
				continue
			}
			tmpMap[p] = fil
		}
		indexMap = tmpMap
	}

	// download (or reuse) all indices
	slog.Info("downloading package/source index files)", "repo", m.id, "suite", suite, "total", len(indexMap))
	indices, err := m.parser.downloadIndices(ctx, m.httpClient, indexMap, byhash, m)
	if err != nil {
		return errors.Wrap(err, m.id)
	}
	slog.Debug("index files processed", "repo", m.id, "suite", suite, "downloaded", len(indices))

	// extract file information from indices and download items
	slog.Info("processing package files", "repo", m.id, "suite", suite)
	items, err := m.parser.downloadItems(ctx, m.httpClient, indices, byhash, quiet, m, suite)
	if err != nil {
		return errors.Wrap(err, m.id)
	}
	slog.Debug("package files processed", "repo", m.id, "suite", suite, "total", len(items))

	// Add items to the item map
	for _, item := range items {
		itemMap[item.Path()] = item
	}
	return nil
}
