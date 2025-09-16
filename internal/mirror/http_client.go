package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/mirrorctl/mirrorctl/internal/apt"
	"golang.org/x/sync/errgroup"
	"log/slog"
)

// HTTPClient handles HTTP downloading with retries and by-hash fallback.
type HTTPClient struct {
	client    *http.Client
	semaphore chan struct{}
	mirrorID  string
	storage   *Storage
	current   *Storage // For file reuse logic
}

// NewHTTPClient creates a new HTTP client for downloads.
func NewHTTPClient(maxConns int, mirrorID string, storage *Storage, current *Storage, tlsConfig *TLSConfig) *HTTPClient {
	semaphore := make(chan struct{}, maxConns)

	// Pre-fill the semaphore with tokens
	for i := 0; i < maxConns; i++ {
		semaphore <- struct{}{}
	}

	return &HTTPClient{
		client:    clonedTransport(tlsConfig),
		semaphore: semaphore,
		mirrorID:  mirrorID,
		storage:   storage,
		current:   current,
	}
}

// dlResult represents the result of a download operation.
type dlResult struct {
	path     string
	status   int
	fi       *apt.FileInfo
	tempfile *os.File
	err      error
}

// download is a goroutine to download an item.
func (h *HTTPClient) download(ctx context.Context, mirrorConfig *MirrorConfig,
	p string, fi *apt.FileInfo, byhash bool, ch chan<- *dlResult) {
	var tempfile *os.File
	r := &dlResult{
		path: p,
	}

	defer func() {
		r.tempfile = tempfile
		// Return semaphore token first, then send result
		h.semaphore <- struct{}{}
		ch <- r
	}()

	targets := []string{p}
	if byhash && fi != nil {
		targets = append(targets, fi.SHA512Path())
		targets = append(targets, fi.SHA256Path())
		targets = append(targets, fi.SHA1Path())
		targets = append(targets, fi.MD5SumPath())
	}

	const maxDownloadAttempts = 15 // Safety limit for all retries and fallbacks
	var lastErr error

	for attempt := 0; attempt < maxDownloadAttempts; attempt++ {
		if tempfile != nil {
			closeAndRemoveFile(tempfile)
			tempfile = nil
		}

		// allow interrupts
		select {
		case <-ctx.Done():
			r.err = ctx.Err()
			return
		default:
		}

		if attempt > 0 {
			slog.Warn("retrying download", "repo", h.mirrorID, "path", p, "attempt", attempt+1, "max_attempts", maxDownloadAttempts)
			// Simple backoff, consider more advanced strategies if needed
			time.Sleep(1 * time.Second)
		}

		// imitation apt-get command
		// NOTE: apt-get sets If-Modified-Since and makes a request to the server,
		// but the current aptutil cannot handle this because it cold-starts every time.
		header := http.Header{}
		header.Add("Cache-Control", "max-age=0")
		header.Add("User-Agent", "Debian APT-HTTP/1.3 (aptutil)")

		req := &http.Request{
			Method:     "GET",
			URL:        mirrorConfig.Resolve(targets[0]),
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     header,
		}
		resp, err := h.client.Do(req.WithContext(ctx))
		if err != nil {
			lastErr = err
			continue
		}

		r.status = resp.StatusCode
		if r.status >= 500 {
			lastErr = fmt.Errorf("server error %d", r.status)
			closeRespBody(resp)
			continue
		}

		if r.status != 200 {
			// For non-500 errors (like 404), don't retry, just return the result.
			// The caller will handle it.
			closeRespBody(resp)
			return
		}

		tempfile, err = h.storage.TempFile()
		if err != nil {
			r.err = err
			closeRespBody(resp)
			return
		}
		// Use response body directly
		var reader io.Reader = resp.Body

		fi2, err := apt.CopyWithFileInfo(tempfile, reader, p)
		closeRespBody(resp) // Close body after reading
		if err != nil {
			lastErr = err
			continue
		}
		err = tempfile.Sync()
		if err != nil {
			r.err = errors.New("tempfile.Sync failed")
			return
		}
		err = os.Chmod(tempfile.Name(), 0600)
		if err != nil {
			r.err = errors.New("os.Chmod(tempfile.Name(), 0600) failed")
			return
		}

		if fi != nil && !fi.Same(fi2) {
			lastErr = errors.New("invalid checksum for " + p)
			if len(targets) > 1 {
				// Move to next target for by-hash fallback
				targets = targets[1:]
				slog.Warn("try by-hash retrieval", "repo", h.mirrorID, "path", p, "target", targets[0])
				continue
			}
			// No more by-hash targets, return the checksum error
			r.err = lastErr
			return
		}

		_, err = tempfile.Seek(0, io.SeekStart)
		if err != nil {
			r.err = errors.New("tempfile.Seek failed")
			return
		}

		r.fi = fi2
		r.err = nil // Explicitly set error to nil on success
		return      // success
	}

	// If the loop completes, all attempts have failed.
	r.err = fmt.Errorf("download failed for %s after %d attempts: %w", p, maxDownloadAttempts, lastErr)
}

// downloadFiles downloads a list of files concurrently.
func (h *HTTPClient) downloadFiles(ctx context.Context, mirrorConfig *MirrorConfig,
	fil []*apt.FileInfo, allowMissing, byhash bool) ([]*apt.FileInfo, error) {
	return h.downloadFilesWithContext(ctx, mirrorConfig, fil, allowMissing, byhash, "files")
}

// downloadIndicesFiles downloads index files with clear logging context.
func (h *HTTPClient) downloadIndicesFiles(ctx context.Context, mirrorConfig *MirrorConfig,
	fil []*apt.FileInfo, allowMissing, byhash bool) ([]*apt.FileInfo, error) {
	return h.downloadFilesWithContext(ctx, mirrorConfig, fil, allowMissing, byhash, "indices")
}

// downloadPackageFiles downloads package files with clear logging context.
func (h *HTTPClient) downloadPackageFiles(ctx context.Context, mirrorConfig *MirrorConfig,
	fil []*apt.FileInfo, allowMissing, byhash bool) ([]*apt.FileInfo, error) {
	return h.downloadFilesWithContext(ctx, mirrorConfig, fil, allowMissing, byhash, "packages")
}

// downloadFilesWithContext downloads a list of files concurrently with a context description for logging.
func (h *HTTPClient) downloadFilesWithContext(ctx context.Context, mirrorConfig *MirrorConfig,
	fil []*apt.FileInfo, allowMissing, byhash bool, fileType string) ([]*apt.FileInfo, error) {
	results := make(chan *dlResult, len(fil))
	var reused, downloaded []*apt.FileInfo

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		reused, err = h.reuseOrDownload(ctx, mirrorConfig, fil, byhash, results)
		return err
	})
	g.Go(func() error {
		var err error
		downloaded, err = h.recvResult(allowMissing, byhash, results)
		return err
	})
	err := g.Wait()
	if err != nil {
		return nil, err
	}

	// Log download stats with file type context
	slog.Info("download stats", "repo", h.mirrorID, "type", fileType, "total", len(fil), "reused", len(reused), "downloaded", len(downloaded))
	slog.Debug("download complete", "repo", h.mirrorID, "type", fileType, "reused_files", len(reused), "new_downloads", len(downloaded))

	// reused has enough capacity.  See reuseOrDownload.
	return append(reused, downloaded...), nil
}

// reuseOrDownload checks for existing files and downloads missing ones.
func (h *HTTPClient) reuseOrDownload(ctx context.Context, mirrorConfig *MirrorConfig, fil []*apt.FileInfo,
	byhash bool, results chan<- *dlResult) ([]*apt.FileInfo, error) {
	// by closing results channel.
	defer close(results)

	// This errgroup is just for managing the download workers
	workerGroup, workerCtx := errgroup.WithContext(ctx)

	// It is essential to wait for all workers to finish before reuseOrDownload returns.
	// This ensures the results channel is not closed prematurely.
	defer func() {
		_ = workerGroup.Wait() // Wait for all download goroutines to complete
	}()

	reused := make([]*apt.FileInfo, 0, len(fil))

	for _, fi := range fil {
		// avoid assignment
		fi := fi

		// Try to reuse existing file if we have current storage
		if h.current != nil {
			localfi, fullpath := h.current.Lookup(fi, byhash)
			if localfi != nil {
				slog.Debug("reusing existing file", "repo", h.mirrorID, "path", fi.Path())
				err := h.storeLink(localfi, fullpath, byhash)
				if err != nil {
					// This is a critical error, but we must not block the pipeline.
					// We can't return directly. We'll let the main errgroup handle it.
					return nil, errors.Wrap(err, "storeLink")
				}
				reused = append(reused, localfi)
				continue
			}
		}

		select {
		case <-ctx.Done(): // Check the main context before starting a new worker
			return nil, ctx.Err()
		case <-h.semaphore:
		}

		workerGroup.Go(func() error {
			// Pass the worker context to the download function
			h.download(workerCtx, mirrorConfig, fi.Path(), fi, byhash, results)
			return nil
		})
	}
	return reused, nil
}

// handleResult processes a download result.
func (h *HTTPClient) handleResult(r *dlResult, allowMissing, byhash bool) (*apt.FileInfo, error) {
	if r.tempfile != nil {
		defer closeAndRemoveFile(r.tempfile)
	}

	if r.err != nil {
		return nil, errors.Wrap(r.err, "download")
	}

	if allowMissing && r.status == http.StatusNotFound {
		slog.Warn("missing file", "repo", h.mirrorID, "path", r.path)
		// return no error to continue
		return nil, nil
	}

	if r.status != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", r.status, r.path)
	}

	err := h.storeLink(r.fi, r.tempfile.Name(), byhash)
	if err != nil {
		return nil, errors.Wrap(err, "store")
	}

	slog.Debug("file downloaded successfully", "repo", h.mirrorID, "path", r.path, "size", r.fi.Size())
	return r.fi, nil
}

// recvResult receives and processes download results.
func (h *HTTPClient) recvResult(allowMissing, byhash bool, results <-chan *dlResult) ([]*apt.FileInfo, error) {
	var fil []*apt.FileInfo
	var firstErr error

	for r := range results {
		fi, err := h.handleResult(r, allowMissing, byhash)
		if err != nil {
			// Don't return immediately. Store the first error and continue draining the channel
			// to prevent deadlocking the sender goroutines.
			if firstErr == nil {
				firstErr = err
			}
		}
		if fi != nil {
			fil = append(fil, fi)
		}
	}

	// Return the collected files and the first error that occurred.
	return fil, firstErr
}

// countReusableFiles counts how many files can be reused vs need downloading.
func (h *HTTPClient) countReusableFiles(fil []*apt.FileInfo, byhash bool) (reusableCount, needDownloadCount int) {
	if h.current == nil {
		return 0, len(fil)
	}

	for _, fi := range fil {
		localfi, _ := h.current.Lookup(fi, byhash)
		if localfi != nil {
			reusableCount++
		} else {
			needDownloadCount++
		}
	}
	return reusableCount, needDownloadCount
}

// storeLink stores a file in the storage system.
func (h *HTTPClient) storeLink(fileInfo *apt.FileInfo, filePath string, byhash bool) error {
	if byhash {
		return h.storage.StoreLinkWithHash(fileInfo, filePath)
	}
	return h.storage.StoreLink(fileInfo, filePath)
}

// closeRespBody closes HTTP response body.
func closeRespBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Warn("failed to close response body", "error", err)
	}
}

// closeAndRemoveFile closes and removes a temporary file.
func closeAndRemoveFile(f *os.File) {
	filename := f.Name()
	if err := f.Close(); err != nil {
		slog.Warn("failed to close temp file", "file", filename, "error", err)
	}
	if err := os.Remove(filename); err != nil {
		slog.Warn("failed to remove temp file", "file", filename, "error", err)
	}
}

// clonedTransport creates a new HTTP client with optimized transport settings and TLS configuration.
func clonedTransport(tlsConfig *TLSConfig) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.IdleConnTimeout = 90 * time.Second

	// Apply TLS configuration if provided
	if tlsConfig != nil {
		customTLSConfig, err := tlsConfig.BuildTLSConfig()
		if err != nil {
			// Log error but continue with default transport
			// In production, you might want to fail here instead
			slog.Error("Failed to build TLS config, using defaults", "error", err)
		} else {
			tr.TLSClientConfig = customTLSConfig
		}
	}

	return &http.Client{
		Transport: tr,
		Timeout:   0, // no timeout; timeout is controlled by context
	}
}
