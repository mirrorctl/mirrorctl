package mirror

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cockroachdb/errors"

	"github.com/mirrorctl/mirrorctl/internal/apt"
)

const (
	infoJSON = "info.json"
)

// validatePath validates that a path is safe for use within the storage directory.
// It prevents directory traversal attacks by checking for:
// 1. Parent directory references (..)
// 2. Absolute paths
// Returns an error if the path is unsafe.
func validatePath(path string) error {
	cleanPath := filepath.Clean(path)

	// Check for directory traversal attempts
	if strings.Contains(cleanPath, "..") {
		return errors.New("unsafe path (contains directory traversal): " + path)
	}

	// Check for absolute paths
	if filepath.IsAbs(cleanPath) {
		return errors.New("unsafe path (absolute path not allowed): " + path)
	}

	return nil
}

// Storage manages a directory tree that mirrors a Debian repository.
//
// Storage also keeps checksum information for stored files.
type Storage struct {
	dir    string
	prefix string

	mu   sync.RWMutex
	info map[string]*apt.FileInfo
}

// NewStorage constructs Storage.
//
// dir must be an absolute path to an existing directory.
// prefix should be a directory name.
func NewStorage(dir, prefix string) (*Storage, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("none absolute: " + dir)
	}

	dir = filepath.Clean(dir)
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsDir() {
		return nil, errors.New("not a directory: " + dir)
	}

	return &Storage{
		dir:    dir,
		prefix: prefix,
		info:   make(map[string]*apt.FileInfo),
	}, nil
}

// Dir returns the directory of the Storage.
func (s *Storage) Dir() string {
	return s.dir
}

// Load loads existing directory contents.
func (s *Storage) Load() error {
	infoPath := filepath.Join(s.dir, infoJSON)

	f, err := os.Open(infoPath) // #nosec G304 - infoPath is constructed from validated config.Dir and constant infoJSON
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			// Don't use slog here as it may not be initialized yet
			_ = err // Intentionally ignoring error
		}
	}()

	jd := json.NewDecoder(f)
	err = jd.Decode(&s.info)
	if err != nil {
		return errors.Wrap(err, "Storage.Load: "+infoPath)
	}
	return nil
}

// TempFile creates a new temporary file
// in the directory specified in Storage,
// opens the file for reading and writing,
// and returns the resulting *os.File.
func (s *Storage) TempFile() (*os.File, error) {
	return os.CreateTemp(s.dir, "_tmp")
}

// Save saves storage contents persistently.
func (s *Storage) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	infoPath := filepath.Join(s.dir, infoJSON)
	f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644) // #nosec G304 - infoPath is constructed from validated config.Dir and constant infoJSON
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			// Don't use slog here as it may not be initialized yet
			_ = err // Intentionally ignoring error
		}
	}()

	enc := json.NewEncoder(f)
	err = enc.Encode(s.info)
	if err != nil {
		return err
	}

	_ = f.Sync()
	err = DirSyncTree(s.dir)
	if err != nil {
		return errors.Wrap(err, "DirSyncTree(s.dir)")
	}

	return nil
}

// StoreLink stores a hard link to a file into this storage.
func (s *Storage) StoreLink(fi *apt.FileInfo, fullpath string) error {
	p := fi.Path()

	// Validate path for security
	if err := validatePath(p); err != nil {
		return errors.Wrap(err, "StoreLink")
	}

	fp := filepath.Join(s.dir, s.prefix, filepath.Clean(p))
	d := filepath.Dir(fp)

	err := os.MkdirAll(d, 0750)
	if err != nil {
		return err
	}

	err = os.Link(fullpath, fp)
	if err != nil && os.IsExist(err) {
		// File already exists - this is expected during resume operations
		// Remove the existing file and try again
		if removeErr := os.Remove(fp); removeErr != nil {
			return errors.Wrap(removeErr, "failed to remove existing file for resume")
		}
		err = os.Link(fullpath, fp)
	}
	if err != nil {
		return err
	}

	// Only add to s.info after successful file creation
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.info[p]
	if ok {
		// Path already stored in this session - this is a duplicate
		return errors.New("already stored: " + p)
	}
	s.info[p] = fi
	return nil
}

// StoreLinkWithHash stores a hard link to a file into this storage
// with additional hard links for by-hash retrieval.
func (s *Storage) StoreLinkWithHash(fi *apt.FileInfo, fullpath string) error {
	p := fi.Path()
	md5p := fi.MD5SumPath()
	sha1p := fi.SHA1Path()
	sha256p := fi.SHA256Path()
	sha512p := fi.SHA512Path()

	// Validate all paths for security
	paths := []string{p, md5p, sha1p, sha256p, sha512p}
	for _, path := range paths {
		if path != "" { // Skip empty paths (when checksums aren't available)
			if err := validatePath(path); err != nil {
				return errors.Wrap(err, "StoreLinkWithHash")
			}
		}
	}

	fpl := []string{
		filepath.Join(s.dir, s.prefix, filepath.Clean(p)),
	}

	// Only add hash paths that exist (non-empty)
	if md5p != "" {
		fpl = append(fpl, filepath.Join(s.dir, s.prefix, filepath.Clean(md5p)))
	}
	if sha1p != "" {
		fpl = append(fpl, filepath.Join(s.dir, s.prefix, filepath.Clean(sha1p)))
	}
	if sha256p != "" {
		fpl = append(fpl, filepath.Join(s.dir, s.prefix, filepath.Clean(sha256p)))
	}
	if sha512p != "" {
		fpl = append(fpl, filepath.Join(s.dir, s.prefix, filepath.Clean(sha512p)))
	}

	s.mu.Lock()
	_, ok := s.info[p]
	if ok {
		// ignore the canonical path because another file was already stored.
		fpl = fpl[1:]
	} else {
		s.info[p] = fi
	}

	// This may overwrite existing entries in s.info if another item
	// accidentally has the same checksums.  In such cases, Storage.Lookup
	// for the previous item will return nil and go-apt-mirror would
	// fail to reuse the item.
	//
	// Although we may fix the problem in Storage.Lookup, at this point
	// we leave it as it is not too bad.
	if md5p != "" {
		s.info[md5p] = fi
	}
	if sha1p != "" {
		s.info[sha1p] = fi
	}
	if sha256p != "" {
		s.info[sha256p] = fi
	}
	if sha512p != "" {
		s.info[sha512p] = fi
	}
	s.mu.Unlock()

	for _, fp := range fpl {
		d := filepath.Dir(fp)
		err := os.MkdirAll(d, 0750)
		if err != nil {
			return errors.Wrap(err, "StoreLinkWithHash: "+fp)
		}
		err = os.Link(fullpath, fp)
		if err != nil && !os.IsExist(err) {
			return errors.Wrap(err, "StoreLinkWithHash: "+fp)
		}
	}
	return nil
}

// Lookup looks up a file in this storage.
//
// If a file matching fi exists, its info and full path is returned.
// Otherwise, nil and empty string is returned.
func (s *Storage) Lookup(fi *apt.FileInfo, byhash bool) (*apt.FileInfo, string) {
	f := func(p string) (*apt.FileInfo, string) {
		// Validate path for security
		if err := validatePath(p); err != nil {
			// Log the error but don't fail - just return not found
			// This prevents attacks while maintaining functionality
			return nil, ""
		}

		s.mu.RLock()
		defer s.mu.RUnlock()

		fi2, ok := s.info[p]
		if !ok || !fi.Same(fi2) {
			return nil, ""
		}
		return fi2, filepath.Join(s.dir, s.prefix, filepath.Clean(p))
	}

	if byhash {
		// Try SHA512 first (strongest hash), then fall back to others
		if sha512path := fi.SHA512Path(); sha512path != "" {
			fi2, fullpath := f(sha512path)
			if fi2 != nil {
				return fi2, fullpath
			}
		}

		if sha256path := fi.SHA256Path(); sha256path != "" {
			fi2, fullpath := f(sha256path)
			if fi2 != nil {
				return fi2, fullpath
			}
		}
	}

	return f(fi.Path())
}

// Open opens the named file and returns it.
func (s *Storage) Open(p string) (*os.File, error) {
	// Validate path for security
	if err := validatePath(p); err != nil {
		return nil, errors.Wrap(err, "Open")
	}

	return os.Open(filepath.Join(s.dir, s.prefix, filepath.Clean(p)))
}
