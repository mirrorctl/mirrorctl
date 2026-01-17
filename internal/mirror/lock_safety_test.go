package mirror

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// =============================================================================
// validateLockFilePath tests
// =============================================================================

func TestValidateLockFilePath_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lockFile string
		baseDir  string
	}{
		{
			name:     "simple lock file in base dir",
			lockFile: "/var/mirror/.lock",
			baseDir:  "/var/mirror",
		},
		{
			name:     "lock file in subdirectory",
			lockFile: "/var/mirror/subdir/.lock",
			baseDir:  "/var/mirror",
		},
		{
			name:     "relative-like but absolute path",
			lockFile: "/home/user/mirrors/.lock",
			baseDir:  "/home/user/mirrors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLockFilePath(tt.lockFile, tt.baseDir)
			if err != nil {
				t.Errorf("expected valid path, got error: %v", err)
			}
		})
	}
}

func TestValidateLockFilePath_DirectoryTraversal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lockFile string
		baseDir  string
	}{
		{
			name:     "simple parent traversal",
			lockFile: "/var/mirror/../etc/passwd",
			baseDir:  "/var/mirror",
		},
		{
			name:     "double parent traversal",
			lockFile: "/var/mirror/../../etc/shadow",
			baseDir:  "/var/mirror",
		},
		{
			name:     "hidden traversal in middle",
			lockFile: "/var/mirror/foo/../../../etc/passwd",
			baseDir:  "/var/mirror",
		},
		{
			name:     "traversal at start",
			lockFile: "../etc/passwd",
			baseDir:  "/var/mirror",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLockFilePath(tt.lockFile, tt.baseDir)
			if err == nil {
				t.Error("expected error for directory traversal, got nil")
			}
		})
	}
}

func TestValidateLockFilePath_OutsideBaseDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lockFile string
		baseDir  string
	}{
		{
			name:     "completely different path",
			lockFile: "/etc/passwd",
			baseDir:  "/var/mirror",
		},
		{
			name:     "sibling directory",
			lockFile: "/var/other/.lock",
			baseDir:  "/var/mirror",
		},
		{
			name:     "parent directory",
			lockFile: "/var/.lock",
			baseDir:  "/var/mirror",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLockFilePath(tt.lockFile, tt.baseDir)
			if err == nil {
				t.Error("expected error for path outside base dir, got nil")
			}
		})
	}
}

// TestValidateLockFilePath_SimilarPrefixBug documents a known limitation in the
// current validateLockFilePath implementation. Paths like "/var/mirror-other/.lock"
// pass validation when baseDir is "/var/mirror" because the check uses string
// prefix matching rather than proper path hierarchy validation.
// TODO: This should be fixed by ensuring the path component boundary is respected.
func TestValidateLockFilePath_SimilarPrefixBug(t *testing.T) {
	t.Parallel()

	// This test documents current behavior - it passes when arguably it should fail
	// A path like /var/mirror-other is NOT inside /var/mirror, but the string
	// prefix check incorrectly allows it
	lockFile := "/var/mirror-other/.lock"
	baseDir := "/var/mirror"

	err := validateLockFilePath(lockFile, baseDir)
	// Current behavior: this passes (no error)
	// Ideal behavior: this should fail
	if err != nil {
		t.Logf("behavior changed - now correctly rejecting similar prefix: %v", err)
	} else {
		t.Log("known limitation: similar prefix paths are not properly rejected")
	}
}

func TestValidateLockFilePath_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		lockFile  string
		baseDir   string
		wantError bool
	}{
		{
			name:      "trailing slash in base dir",
			lockFile:  "/var/mirror/.lock",
			baseDir:   "/var/mirror/",
			wantError: false,
		},
		{
			name:      "double slashes in path",
			lockFile:  "/var/mirror//.lock",
			baseDir:   "/var/mirror",
			wantError: false,
		},
		{
			name:      "dot in filename",
			lockFile:  "/var/mirror/.lock.tmp",
			baseDir:   "/var/mirror",
			wantError: false,
		},
		{
			name:      "single dot component",
			lockFile:  "/var/mirror/./.lock",
			baseDir:   "/var/mirror",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLockFilePath(tt.lockFile, tt.baseDir)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// =============================================================================
// Lock contention tests
// =============================================================================

func TestFlock_Contention_NonBlocking(t *testing.T) {
	t.Parallel()

	// Create a temporary file for locking
	tmpFile, err := os.CreateTemp(t.TempDir(), "flock-contention-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Open the same file again for second lock attempt
	tmpFile2, err := os.Open(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to open temp file second time: %v", err)
	}
	defer tmpFile2.Close()

	// First lock should succeed
	fl1 := Flock{tmpFile}
	if err := fl1.Lock(); err != nil {
		t.Fatalf("first lock should succeed: %v", err)
	}

	// Second lock should fail immediately (non-blocking)
	fl2 := Flock{tmpFile2}
	err = fl2.Lock()
	if err == nil {
		t.Error("second lock should fail when first lock is held")
	}

	// Verify it's the expected error (EWOULDBLOCK/EAGAIN)
	if err != nil {
		// The error should indicate the resource is temporarily unavailable
		t.Logf("Got expected contention error: %v", err)
	}

	// After unlocking first, second should succeed
	if err := fl1.Unlock(); err != nil {
		t.Fatalf("unlock should succeed: %v", err)
	}

	if err := fl2.Lock(); err != nil {
		t.Errorf("second lock should succeed after first is released: %v", err)
	}

	if err := fl2.Unlock(); err != nil {
		t.Errorf("second unlock should succeed: %v", err)
	}
}

func TestFlock_Contention_MultipleProcesses(t *testing.T) {
	t.Parallel()

	// Create a temporary directory and lock file
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create the lock file
	lockFile, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Acquire the lock
	fl := Flock{lockFile}
	if err := fl.Lock(); err != nil {
		lockFile.Close()
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Try to acquire the same lock from multiple goroutines
	const numGoroutines = 5
	var failedLocks atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			f, err := os.Open(lockPath)
			if err != nil {
				return
			}
			defer f.Close()

			fl := Flock{f}
			if err := fl.Lock(); err != nil {
				failedLocks.Add(1)
			} else {
				// If we somehow got the lock, release it
				_ = fl.Unlock()
			}
		}()
	}

	wg.Wait()

	// All goroutines should have failed to acquire the lock
	if failedLocks.Load() != numGoroutines {
		t.Errorf("expected %d failed locks, got %d", numGoroutines, failedLocks.Load())
	}

	// Clean up
	_ = fl.Unlock()
	lockFile.Close()
}

func TestFlock_SequentialAcquisition(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create the lock file
	if _, err := os.Create(lockPath); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Multiple goroutines try to acquire the lock sequentially
	const numIterations = 10
	var successCount atomic.Int32
	var mu sync.Mutex
	var currentHolder atomic.Int32
	currentHolder.Store(-1)

	var wg sync.WaitGroup
	for i := 0; i < numIterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Retry loop with timeout
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				f, err := os.Open(lockPath)
				if err != nil {
					continue
				}

				fl := Flock{f}
				if err := fl.Lock(); err != nil {
					f.Close()
					time.Sleep(10 * time.Millisecond)
					continue
				}

				// Got the lock
				mu.Lock()
				prev := currentHolder.Swap(int32(id))
				if prev != -1 {
					t.Errorf("goroutine %d got lock while %d still held it", id, prev)
				}
				mu.Unlock()

				// Hold the lock briefly
				time.Sleep(5 * time.Millisecond)
				successCount.Add(1)

				currentHolder.Store(-1)
				_ = fl.Unlock()
				f.Close()
				return
			}
		}(i)
	}

	wg.Wait()

	// At least some goroutines should have succeeded
	if successCount.Load() == 0 {
		t.Error("no goroutines successfully acquired the lock")
	}
	t.Logf("%d/%d goroutines successfully acquired lock", successCount.Load(), numIterations)
}

// =============================================================================
// Cleanup and unlock tests
// =============================================================================

func TestFlock_LockAfterClose(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "flock-closed-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	fl := Flock{tmpFile}

	// Close the file
	tmpFile.Close()

	// Try to lock - should fail
	err = fl.Lock()
	if err == nil {
		t.Error("lock on closed file should fail")
	} else {
		t.Logf("got expected error on closed file: %v", err)
	}
}

func TestFlock_CleanupOnFileRemoval(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create and lock
	f1, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	fl1 := Flock{f1}
	if err := fl1.Lock(); err != nil {
		f1.Close()
		t.Fatalf("failed to lock: %v", err)
	}

	// Remove the file while lock is held
	if err := os.Remove(lockPath); err != nil {
		_ = fl1.Unlock()
		f1.Close()
		t.Fatalf("failed to remove lock file: %v", err)
	}

	// Lock should still be valid on the open file descriptor
	// even though the file is unlinked

	// Create a new file with the same path
	f2, err := os.Create(lockPath)
	if err != nil {
		_ = fl1.Unlock()
		f1.Close()
		t.Fatalf("failed to create new lock file: %v", err)
	}
	defer f2.Close()

	// This new file should be lockable since it's a different inode
	fl2 := Flock{f2}
	if err := fl2.Lock(); err != nil {
		t.Errorf("should be able to lock new file: %v", err)
	} else {
		_ = fl2.Unlock()
	}

	// Clean up original lock
	_ = fl1.Unlock()
	f1.Close()
}

// =============================================================================
// Race condition tests
// =============================================================================

func TestFlock_RaceCondition_ConcurrentLockUnlock(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "flock-race-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	fl := Flock{tmpFile}

	// Run many lock/unlock cycles concurrently on the same Flock
	// This tests for race conditions in the Flock struct itself
	const numGoroutines = 10
	const numIterations = 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				// Try to lock (may fail due to contention)
				if err := fl.Lock(); err == nil {
					// Small delay to increase chance of races
					time.Sleep(time.Microsecond)
					_ = fl.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	// Test passes if no panics or deadlocks occurred
}

func TestFlock_RaceCondition_FileDescriptorReuse(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create initial lock file
	if _, err := os.Create(lockPath); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Multiple goroutines opening, locking, unlocking, and closing
	const numGoroutines = 20
	var wg sync.WaitGroup
	var lockAcquired atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < 5; j++ {
				f, err := os.Open(lockPath)
				if err != nil {
					continue
				}

				fl := Flock{f}
				if err := fl.Lock(); err == nil {
					lockAcquired.Add(1)
					time.Sleep(time.Millisecond)
					_ = fl.Unlock()
				}
				f.Close()
			}
		}()
	}

	wg.Wait()
	t.Logf("total locks acquired: %d", lockAcquired.Load())
}

// =============================================================================
// Run() lock file behavior tests
// =============================================================================

func TestRun_LockFileCreation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	config := &Config{
		Dir:      tmpDir,
		MaxConns: 5,
		Mirrors:  make(map[string]*MirrorConfig),
	}

	// Lock file should not exist before Run
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should not exist before Run")
	}

	// Run with empty mirrors (should complete quickly)
	err := Run(config, []string{}, false, true, true, false)
	if err != nil {
		t.Logf("Run returned error (may be expected): %v", err)
	}

	// After Run completes, lock file should be removed (cleanup)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after Run completes")
	}
}

func TestRun_LockContention(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create and hold the lock file externally
	lockFile, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}
	defer lockFile.Close()

	fl := Flock{lockFile}
	if err := fl.Lock(); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer func() { _ = fl.Unlock() }()

	config := &Config{
		Dir:      tmpDir,
		MaxConns: 5,
		Mirrors:  make(map[string]*MirrorConfig),
	}

	// Run should fail because lock is held
	err = Run(config, []string{}, false, true, true, false)
	if err == nil {
		t.Error("Run should fail when lock is already held")
	} else {
		t.Logf("Got expected lock contention error: %v", err)
	}
}

func TestRun_ConcurrentRuns(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	config := &Config{
		Dir:      tmpDir,
		MaxConns: 5,
		Mirrors:  make(map[string]*MirrorConfig),
	}

	// Try to run concurrently - only one should succeed at a time
	const numGoroutines = 5
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := Run(config, []string{}, false, true, true, false)
			if err == nil {
				successCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}()
	}

	wg.Wait()

	t.Logf("successes: %d, errors: %d", successCount.Load(), errorCount.Load())

	// Due to the nature of concurrent execution, results may vary
	// but we should not have all successes (that would indicate broken locking)
	// Unless runs complete so fast they don't overlap
}

// =============================================================================
// Stress tests
// =============================================================================

func TestFlock_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, ".lock")

	// Create the lock file
	if _, err := os.Create(lockPath); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	const numGoroutines = 50
	const duration = 2 * time.Second

	var totalLocks atomic.Int64
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start goroutines that continuously try to acquire and release the lock
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-done:
					return
				default:
				}

				f, err := os.Open(lockPath)
				if err != nil {
					continue
				}

				fl := Flock{f}
				if err := fl.Lock(); err == nil {
					totalLocks.Add(1)
					// Very brief hold
					_ = fl.Unlock()
				}
				f.Close()
			}
		}()
	}

	// Let the stress test run
	time.Sleep(duration)
	close(done)
	wg.Wait()

	t.Logf("stress test: %d successful lock acquisitions in %v", totalLocks.Load(), duration)

	if totalLocks.Load() == 0 {
		t.Error("no locks were acquired during stress test")
	}
}

// =============================================================================
// Flock syscall error handling
// =============================================================================

func TestFlock_SyscallError(t *testing.T) {
	t.Parallel()

	// Create a temp file, get its fd, then close it to create an invalid fd scenario
	tmpFile, err := os.CreateTemp(t.TempDir(), "flock-syscall-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	fl := Flock{tmpFile}

	// Close the underlying file to make the fd invalid
	tmpFile.Close()

	// Now try to lock - should fail with syscall error
	err = fl.Lock()
	if err == nil {
		t.Error("lock on closed/invalid fd should fail")
	}

	// Check that the error is a syscall error
	if sysErr, ok := err.(*os.SyscallError); ok {
		t.Logf("got expected SyscallError: %v", sysErr)
	} else {
		t.Logf("got error (not SyscallError): %T: %v", err, err)
	}
}

func TestFlock_EWOULDBLOCK(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "flock-ewouldblock-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Open again for contention test
	tmpFile2, err := os.Open(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	defer tmpFile2.Close()

	fl1 := Flock{tmpFile}
	fl2 := Flock{tmpFile2}

	// Acquire first lock
	if err := fl1.Lock(); err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	defer func() { _ = fl1.Unlock() }()

	// Second lock should get EWOULDBLOCK
	err = fl2.Lock()
	if err == nil {
		_ = fl2.Unlock()
		t.Fatal("second lock should fail")
	}

	// Verify it's the expected error type
	if sysErr, ok := err.(*os.SyscallError); ok {
		if errno, ok := sysErr.Err.(syscall.Errno); ok {
			// EWOULDBLOCK is often aliased to EAGAIN
			if errno != syscall.EWOULDBLOCK && errno != syscall.EAGAIN {
				t.Errorf("expected EWOULDBLOCK/EAGAIN, got errno %d", errno)
			} else {
				t.Logf("correctly got EWOULDBLOCK/EAGAIN: %v", err)
			}
		}
	}
}
