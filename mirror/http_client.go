package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/apt"
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
	return &HTTPClient{
		client:    clonedTransport(),
		semaphore: make(chan struct{}, maxConns),
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
		ch <- r
		h.semaphore <- struct{}{}
	}()

	var retries uint
	targets := []string{p}
	if byhash && fi != nil {
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

	slog.Debug("downloaded", "repo", h.mirrorID, "path", p, "status_code", resp.StatusCode)

	r.status = resp.StatusCode
	if r.status >= 500 && retries < httpRetries {
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
	fi2, err := apt.CopyWithFileInfo(tempfile, resp.Body, p)
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
	err = os.Chmod(tempfile.Name(), 0644)
	if err != nil {
		r.err = errors.New("os.Chmod(tempfile.Name(), 0644) failed")
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

	slog.Info("stats", "repo", h.mirrorID, "total", len(fil), "reused", len(reused), "downloaded", len(downloaded))

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
	loggedAt := time.Now()

	for i, fi := range fil {
		// avoid assignment
		fi := fi
		now := time.Now()
		if now.Sub(loggedAt) > progressInterval {
			loggedAt = now
			slog.Info("download progress", "repo", h.mirrorID, "total", len(fil), "reused", len(reused), "downloads", i-len(reused))
		}

		// Try to reuse existing file if we have current storage
		if h.current != nil {
			localfi, fullpath := h.current.Lookup(fi, byhash)
			if localfi != nil {
				err := h.storeLink(localfi, fullpath, byhash)
				if err != nil {
					return nil, errors.Wrap(err, "storeLink")
				}
				reused = append(reused, localfi)
				slog.Debug("reuse item", "repo", h.mirrorID, "path", fi.Path())
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

	slog.Debug("downloaded", "repo", h.mirrorID, "path", r.path)
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

// storeLink stores a file in the storage system
func (h *HTTPClient) storeLink(fileInfo *apt.FileInfo, filePath string, byhash bool) error {
	if byhash {
		return h.storage.StoreLinkWithHash(fileInfo, filePath)
	}
	return h.storage.StoreLink(fileInfo, filePath)
}

// closeRespBody closes HTTP response body
func closeRespBody(resp *http.Response) {
	resp.Body.Close()
}

// closeAndRemoveFile closes and removes a temporary file
func closeAndRemoveFile(f *os.File) {
	f.Close()
	os.Remove(f.Name())
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