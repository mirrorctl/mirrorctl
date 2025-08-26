package mirror

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cybozu-go/aptutil/apt"
	"log/slog"
	"golang.org/x/sync/errgroup"
	"github.com/pkg/errors"
)

const (
	timestampFormat  = "20060102_150405"
	progressInterval = 5 * time.Minute
	httpRetries      = 5
)

var (
	validID = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

// Mirror implements mirroring logics.
type Mirror struct {
	id      string
	dir     string
	mc      *MirrConfig
	storage *Storage
	current *Storage

	semaphore chan struct{}
	client    *http.Client
}

// NewMirror constructs a Mirror for given mirror id.
func NewMirror(timestamp time.Time, mirrorID string, config *Config) (*Mirror, error) {
	directory := filepath.Clean(config.Dir)
	mirrorConfig, ok := config.Mirrors[mirrorID]
	if !ok {
		return nil, errors.New("no such mirror: " + mirrorID)
	}

	// sanity checks
	if !validID.MatchString(mirrorID) {
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
	err = os.Mkdir(storageDirectory, 0755)
	if err != nil {
		return nil, errors.Wrap(err, mirrorID)
	}
	storage, err := NewStorage(storageDirectory, mirrorID)
	if err != nil {
		return nil, errors.Wrap(err, mirrorID)
	}

	semaphore := make(chan struct{}, config.MaxConns)
	for i := 0; i < config.MaxConns; i++ {
		semaphore <- struct{}{}
	}

	transport := clonedTransport(http.DefaultTransport)
	if transport == nil {
		transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		}
	}
	transport.MaxIdleConnsPerHost = config.MaxConns

	mirror := &Mirror{
		id:        mirrorID,
		dir:       directory,
		mc:        mirrorConfig,
		storage:   storage,
		current:   currentStorage,
		semaphore: semaphore,
		client: &http.Client{
			Transport: transport,
		},
	}
	return mirror, nil
}

func clonedTransport(rt http.RoundTripper) *http.Transport {
	t, ok := rt.(*http.Transport)
	if !ok {
		return nil
	}
	return t.Clone()
}

func (m *Mirror) storeLink(fileInfo *apt.FileInfo, filePath string, byhash bool) error {
	if byhash {
		return m.storage.StoreLinkWithHash(fileInfo, filePath)
	}
	return m.storage.StoreLink(fileInfo, filePath)
}

func (m *Mirror) extractItems(indices []*apt.FileInfo, indexMap map[string][]*apt.FileInfo, itemMap map[string]*apt.FileInfo, byhash bool) error {
	for _, index := range indices {
		p := index.Path()
		if !m.mc.MatchingIndex(p) || !apt.IsSupported(p) {
			continue
		}
		hashPath := p
		if byhash {
			hashPath = index.SHA256Path()
		}
		f, err := m.storage.Open(hashPath)
		if err != nil {
			return err
		}

		fil, _, err := apt.ExtractFileInfo(p, f)
		f.Close()
		if err != nil {
			return err
		}

		for _, fi := range fil {
			fipath := fi.Path()
			if _, ok := indexMap[fipath]; ok {
				// already included in Release/InRelease
				continue
			}
			itemMap[fipath] = fi
		}
	}
	return nil
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

// Update updates mirrored files.
func (m *Mirror) Update(ctx context.Context) error {
	itemMap := make(map[string]*apt.FileInfo)

	for _, suite := range m.mc.Suites {
		err := m.updateSuite(ctx, suite, itemMap)
		if err != nil {
			return err
		}
	}

	// download all files matching the configuration.
	slog.Info("download items", "repo", m.id, "items", len(itemMap))
	_, err := m.downloadItems(ctx, itemMap)
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	// all files are downloaded (or reused)
	slog.Info("saving meta data", "repo", m.id)
	err = m.storage.Save()
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
func (m *Mirror) updateSuite(ctx context.Context, suite string, itemMap map[string]*apt.FileInfo) error {
	slog.Info("download Release/InRelease", "repo", m.id, "suite", suite)
	indexMap, byhash, err := m.downloadRelease(ctx, suite)
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	if byhash {
		slog.Info("detected by-hash support", "repo", m.id, "suite", suite)
	}

	if len(indexMap) == 0 {
		return errors.New(m.id + ": found no Release/InRelease")
	}

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
	indices, err := m.downloadIndices(ctx, indexMap, byhash)
	if err != nil {
		return errors.Wrap(err, m.id)
	}

	// extract file information from indices
	err = m.extractItems(indices, indexMap, itemMap, byhash)
	if err != nil {
		return errors.Wrap(err, m.id)
	}
	return nil
}

type dlResult struct {
	status   int
	path     string
	fi       *apt.FileInfo
	tempfile *os.File
	err      error
}

func closeRespBody(r *http.Response) {
	io.Copy(ioutil.Discard, r.Body)
	r.Body.Close()
}

func closeAndRemoveFile(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}

// download is a goroutine to download an item.
func (m *Mirror) download(ctx context.Context,
	p string, fi *apt.FileInfo, byhash bool, ch chan<- *dlResult) {

	var tempfile *os.File
	r := &dlResult{
		path: p,
	}

	defer func() {
		r.tempfile = tempfile
		ch <- r
		m.semaphore <- struct{}{}
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
		slog.Warn("retrying download", "repo", m.id, "path", p)
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
		URL:        m.mc.Resolve(targets[0]),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header,
	}
	resp, err := m.client.Do(req.WithContext(ctx))
	if err != nil {
		if retries < httpRetries {
			retries++
			goto RETRY
		}
		r.err = err
		return
	}
	defer closeRespBody(resp)

	slog.Debug("downloaded", "repo", m.id, "path", p, "status_code", resp.StatusCode)

	r.status = resp.StatusCode
	if r.status >= 500 && retries < httpRetries {
		retries++
		goto RETRY
	}

	if r.status != 200 {
		return
	}

	tempfile, err = m.storage.TempFile()
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
			slog.Warn("try by-hash retrieval", "repo", m.id, "path", p, "target", targets[0])
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

func addFileInfoToList(fi *apt.FileInfo, m map[string][]*apt.FileInfo, byhash bool) error {
	p := fi.Path()
	fil, ok := m[p]
	if !ok {
		m[p] = []*apt.FileInfo{fi}
		return nil
	}

	for _, existing := range fil {
		if existing.Same(fi) {
			return nil
		}
	}

	// fi differs from all FileInfo in fil
	if !byhash {
		return errors.New("inconsistent checksum for " + p)
	}
	m[p] = append(fil, fi)
	return nil
}

func (m *Mirror) handleReleaseResults(results <-chan *dlResult, byhash *bool) ([]*apt.FileInfo, error) {
	r := <-results
	if r.tempfile != nil {
		defer closeAndRemoveFile(r.tempfile)
	}

	if r.err != nil {
		return nil, errors.Wrap(r.err, "download")
	}

	if 400 <= r.status && r.status < 500 {
		// return no error to continue
		return nil, nil
	}

	if r.status != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", r.status, r.path)
	}

	// 200 OK
	err := m.storage.StoreLink(r.fi, r.tempfile.Name())
	if err != nil {
		return nil, errors.Wrap(err, "storage.Store")
	}
	fil, d, err := apt.ExtractFileInfo(r.path, r.tempfile)
	if err != nil {
		return nil, errors.Wrap(err, "ExtractFileInfo: "+r.path)
	}

	if *byhash && path.Base(r.path) != "Release.gpg" {
		*byhash = apt.SupportByHash(d)
	}

	return fil, nil
}

func (m *Mirror) downloadRelease(ctx context.Context, suite string) (map[string][]*apt.FileInfo, bool, error) {
	releases := m.mc.ReleaseFiles(suite)
	results := make(chan *dlResult, len(releases))

	for _, p := range releases {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-m.semaphore:
		}

		go m.download(ctx, p, nil, false, results)
	}

	byhash := true
	filMap := make(map[string][]*apt.FileInfo)
	for i := 0; i < len(releases); i++ {
		fil, err := m.handleReleaseResults(results, &byhash)
		if err != nil {
			return nil, byhash, err
		}
		for _, fi := range fil {
			err = addFileInfoToList(fi, filMap, byhash)
			if err != nil {
				return nil, byhash, err
			}
		}
	}

	return filMap, byhash, nil
}

func (m *Mirror) downloadIndices(ctx context.Context,
	filMap map[string][]*apt.FileInfo, byhash bool) ([]*apt.FileInfo, error) {
	var fil []*apt.FileInfo
	for _, fil2 := range filMap {
		fil = append(fil, fil2...)
	}

	slog.Info("download other indices", "repo", m.id, "indices", len(fil))

	return m.downloadFiles(ctx, fil, true, byhash)
}

func (m *Mirror) downloadItems(ctx context.Context,
	fiMap map[string]*apt.FileInfo) ([]*apt.FileInfo, error) {
	fil := make([]*apt.FileInfo, 0, len(fiMap))
	for _, fi := range fiMap {
		fil = append(fil, fi)
	}
	return m.downloadFiles(ctx, fil, false, false)
}

func (m *Mirror) downloadFiles(ctx context.Context,
	fil []*apt.FileInfo, allowMissing, byhash bool) ([]*apt.FileInfo, error) {

	results := make(chan *dlResult, len(fil))
	var reused, downloaded []*apt.FileInfo

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		reused, err = m.reuseOrDownload(ctx, fil, byhash, results)
		return err
	})
	g.Go(func() error {
		var err error
		downloaded, err = m.recvResult(allowMissing, byhash, results)
		return err
	})
	err := g.Wait()
	if err != nil {
		return nil, err
	}

	slog.Info("stats", "repo", m.id, "total", len(fil), "reused", len(reused), "downloaded", len(downloaded))

	// reused has enough capacity.  See reuseOrDownload.
	return append(reused, downloaded...), nil
}

func (m *Mirror) reuseOrDownload(ctx context.Context, fil []*apt.FileInfo,
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
			slog.Info("download progress", "repo", m.id, "total", len(fil), "reused", len(reused), "downloads", i-len(reused))
		}

		if m.current != nil {
			localfi, fullpath := m.current.Lookup(fi, byhash)
			if localfi != nil {
				err := m.storeLink(localfi, fullpath, byhash)
				if err != nil {
					return nil, errors.Wrap(err, "storeLink")
				}
				reused = append(reused, localfi)
				slog.Debug("reuse item", "repo", m.id, "path", fi.Path())
				continue
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.semaphore:
		}

		g.Go(func() error {
			m.download(ctx, fi.Path(), fi, byhash, results)
			return nil
		})
	}
	return reused, nil
}

func (m *Mirror) handleResult(r *dlResult, allowMissing, byhash bool) (*apt.FileInfo, error) {
	if r.tempfile != nil {
		defer closeAndRemoveFile(r.tempfile)
	}

	if r.err != nil {
		return nil, errors.Wrap(r.err, "download")
	}

	if allowMissing && r.status == http.StatusNotFound {
		slog.Warn("missing file", "repo", m.id, "path", r.path)
		// return no error to continue
		return nil, nil
	}

	if r.status != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", r.status, r.path)
	}

	err := m.storeLink(r.fi, r.tempfile.Name(), byhash)
	if err != nil {
		return nil, errors.Wrap(err, "store")
	}

	return r.fi, nil
}

func (m *Mirror) recvResult(allowMissing, byhash bool, results <-chan *dlResult) ([]*apt.FileInfo, error) {
	var dlfil []*apt.FileInfo

	for r := range results {
		fi, err := m.handleResult(r, allowMissing, byhash)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			dlfil = append(dlfil, fi)
		}
	}

	return dlfil, nil
}
