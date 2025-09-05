package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/internal/apt"
	"golang.org/x/sync/errgroup"
	"log/slog"
)

// HTTPClient handles HTTP downloading with retries and by-hash fallback
type HTTPClient struct {
	client    *http.Client
	semaphore chan struct{}
	mirrorID  string
	storage   *Storage
	current   *Storage // For file reuse logic
}

// NewHTTPClient creates a new HTTP client for downloads
func NewHTTPClient(maxConns int, mirrorID string, storage *Storage, current *Storage) *HTTPClient {
	semaphore := make(chan struct{}, maxConns)

	// Pre-fill the semaphore with tokens
	for i := 0; i < maxConns; i++ {
		semaphore <- struct{}{}
	}

	return &HTTPClient{
		client:    clonedTransport(),
		semaphore: semaphore,
		mirrorID:  mirrorID,
		storage:   storage,
		current:   current,
	}
}

// dlResult represents the result of a download operation
type dlResult struct {
	path     string
	status   int
	fi       *apt.FileInfo
	tempfile *os.File
	err      error
}

// download is a goroutine to download an item.
func (h *HTTPClient) download(ctx context.Context, mirrorConfig *MirrConfig,
	p string, fi *apt.FileInfo, byhash bool, ch chan<- *dlResult) {
	var tempfile *os.File
	r := &dlResult{
		path: p,
	}

	defer func() {
		r.tempfile = tempfile
		// Return semaphore token first, then send result
		h.semaphore <- struct{}{}
		// Safely send to channel with recovery from panic
		func() {
			defer func() {
				if recover() != nil {
					// Channel was closed, clean up tempfile if needed
					if tempfile != nil {
						closeAndRemoveFile(tempfile)
					}
				}
			}()
			ch <- r
		}()
	}()

	var retries uint
	targets := []string{p}
	if byhash && fi != nil {
		targets = append(targets, fi.SHA512Path())
		targets = append(targets, fi.SHA256Path())
		targets = append(targets, fi.SHA1Path())
		targets = append(targets, fi.MD5SumPath())
	}

RETRY:
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

	if retries > 0 {
		slog.Warn("retrying download", "repo", h.mirrorID, "path", p)
		time.Sleep(time.Duration(1<<(retries-1)) * time.Second)
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
		if retries < httpRetries {
			retries++
			goto RETRY
		}
		r.err = err
		return
	}
	defer closeRespBody(resp)

	r.status = resp.StatusCode
	if r.status >= 500 && retries < httpRetries {
		slog.Debug("server error, retrying", "repo", h.mirrorID, "path", p, "status", r.status, "attempt", retries+1)
		retries++
		goto RETRY
	}

	if r.status != 200 {
		return
	}

	tempfile, err = h.storage.TempFile()
	if err != nil {
		r.err = err
		return
	}
	// Use response body directly
	var reader io.Reader = resp.Body

	fi2, err := apt.CopyWithFileInfo(tempfile, reader, p)
	if err != nil {
		if retries < httpRetries {
			retries++
			goto RETRY
		}
		r.err = err
		return
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
		if len(targets) > 1 {
			targets = targets[1:]
			slog.Warn("try by-hash retrieval", "repo", h.mirrorID, "path", p, "target", targets[0])
			goto RETRY
		}
		r.err = errors.New("invalid checksum for " + p)
		return
	}

	_, err = tempfile.Seek(0, io.SeekStart)
	if err != nil {
		r.err = errors.New("tempfile.Seek failed")
		return
	}

	r.fi = fi2
}

// downloadFiles downloads a list of files concurrently
func (h *HTTPClient) downloadFiles(ctx context.Context, mirrorConfig *MirrConfig,
	fil []*apt.FileInfo, allowMissing, byhash bool) ([]*apt.FileInfo, error) {
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

	// Log download stats
	slog.Info("stats", "repo", h.mirrorID, "total", len(fil), "reused", len(reused), "downloaded", len(downloaded))
	slog.Debug("download complete", "repo", h.mirrorID, "reused_files", len(reused), "new_downloads", len(downloaded))

	// reused has enough capacity.  See reuseOrDownload.
	return append(reused, downloaded...), nil
}

// reuseOrDownload checks for existing files and downloads missing ones
func (h *HTTPClient) reuseOrDownload(ctx context.Context, mirrorConfig *MirrConfig, fil []*apt.FileInfo,
	byhash bool, results chan<- *dlResult) ([]*apt.FileInfo, error) {
	// environment to manage downloading goroutines.
	g, ctx := errgroup.WithContext(ctx)

	// on return, wait for all DL goroutines then signal recvResult
	// by closing results channel.
	defer func() {
		g.Wait()
		close(results)
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
					return nil, errors.Wrap(err, "storeLink")
				}
				reused = append(reused, localfi)
				continue
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.semaphore:
		}

		g.Go(func() error {
			h.download(ctx, mirrorConfig, fi.Path(), fi, byhash, results)
			return nil
		})
	}
	return reused, nil
}

// handleResult processes a download result
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

// recvResult receives and processes download results
func (h *HTTPClient) recvResult(allowMissing, byhash bool, results <-chan *dlResult) ([]*apt.FileInfo, error) {
	var fil []*apt.FileInfo
	for r := range results {
		fi, err := h.handleResult(r, allowMissing, byhash)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			fil = append(fil, fi)
		}
	}
	return fil, nil
}

// countReusableFiles counts how many files can be reused vs need downloading
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

// storeLink stores a file in the storage system
func (h *HTTPClient) storeLink(fileInfo *apt.FileInfo, filePath string, byhash bool) error {
	if byhash {
		return h.storage.StoreLinkWithHash(fileInfo, filePath)
	}
	return h.storage.StoreLink(fileInfo, filePath)
}

// closeRespBody closes HTTP response body
func closeRespBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Warn("failed to close response body", "error", err)
	}
}

// closeAndRemoveFile closes and removes a temporary file
func closeAndRemoveFile(f *os.File) {
	filename := f.Name()
	if err := f.Close(); err != nil {
		slog.Warn("failed to close temp file", "file", filename, "error", err)
	}
	if err := os.Remove(filename); err != nil {
		slog.Warn("failed to remove temp file", "file", filename, "error", err)
	}
}

// clonedTransport creates a new HTTP client with optimized transport settings
func clonedTransport() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.IdleConnTimeout = 90 * time.Second

	return &http.Client{
		Transport: tr,
		Timeout:   0, // no timeout; timeout is controlled by context
	}
}
