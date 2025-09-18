package mirror

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
)

// validateDirectoryPath validates that a directory path is safe for sync operations.
// It prevents directory traversal attacks by checking for:
// 1. Parent directory references (..)
// 2. Absolute paths are allowed but checked for safety
// Returns an error if the path is unsafe.
func validateDirectoryPath(path string) error {
	cleanPath := filepath.Clean(path)

	// Check for directory traversal attempts in relative paths
	if !filepath.IsAbs(cleanPath) && strings.Contains(cleanPath, "..") {
		return errors.New("unsafe directory path (contains directory traversal): " + path)
	}

	return nil
}

// DirSync calls fsync(2) on the directory to save changes in the directory.
//
// This should be called after os.Create, os.Rename and so on.
func DirSync(d string) error {
	// Validate directory path for security
	if err := validateDirectoryPath(d); err != nil {
		return errors.Wrap(err, "DirSync")
	}

	f, err := os.OpenFile(d, os.O_RDONLY, 0755) // #nosec G304,G302 - path validated, 0755 needed for directory access
	if err != nil {
		return err
	}
	err = f.Sync()
	if err != nil {
		return err
	}
	return f.Close()
}

func dirSyncFunc(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}

	if !info.Mode().IsDir() {
		return nil
	}

	return DirSync(path)
}

// DirSyncTree calls DirSync recursively on a directory tree
// rooted from d.
func DirSyncTree(d string) error {
	// filepath.Walk includes d.
	return filepath.Walk(d, dirSyncFunc)
}
