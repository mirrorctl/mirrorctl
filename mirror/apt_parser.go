package mirror

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/apt"
	"log/slog"
)

// APTParser handles parsing APT repository metadata
type APTParser struct {
	storage  *Storage
	config   *MirrConfig
	mirrorID string
}

// NewAPTParser creates a new APT parser
func NewAPTParser(storage *Storage, config *MirrConfig, mirrorID string) *APTParser {
	return &APTParser{
		storage:  storage,
		config:   config,
		mirrorID: mirrorID,
	}
}

// extractItems extracts file information from downloaded APT index files
func (p *APTParser) extractItems(indices []*apt.FileInfo, indexMap map[string][]*apt.FileInfo, itemMap map[string]*apt.FileInfo, byhash bool) error {
	for _, index := range indices {
		path := index.Path()
		if !p.config.MatchingIndex(path) || !apt.IsSupported(path) {
			continue
		}
		hashPath := path
		if byhash {
			hashPath = index.SHA256Path()
		}
		f, err := p.storage.Open(hashPath)
		if err != nil {
			return err
		}

		fil, _, err := apt.ExtractFileInfo(path, f)
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

// addFileInfoToList adds a FileInfo to a list, checking for duplicates
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

	if !byhash {
		return errors.New("file entry mismatch for " + p)
	}

	m[p] = append(fil, fi)
	return nil
}

// handleReleaseResults processes download results from Release/InRelease files
func (p *APTParser) handleReleaseResults(results <-chan *dlResult, byhash *bool) ([]*apt.FileInfo, error) {
	var releaseFile *apt.FileInfo

	for result := range results {
		if result.tempfile != nil {
			defer closeAndRemoveFile(result.tempfile)
		}

		if result.err != nil {
			slog.Debug("failed to download", "repo", p.mirrorID, "path", result.path, "error", result.err)
			continue
		}

		if result.status != 200 {
			slog.Debug("failed to download", "repo", p.mirrorID, "path", result.path, "status_code", result.status)
			continue
		}

		if releaseFile == nil {
			releaseFile = result.fi
		}

		path := releaseFile.Path()
		hashPath := path
		if *byhash {
			hashPath = releaseFile.SHA256Path()
		}

		err := p.storage.StoreLink(releaseFile, result.tempfile.Name())
		if err != nil {
			return nil, errors.Wrap(err, "storeLink")
		}

		f, err := p.storage.Open(hashPath)
		if err != nil {
			return nil, err
		}

		fil, _, err := apt.ExtractFileInfo(path, f)
		f.Close()
		if err != nil {
			return nil, err
		}

		// Check if the repository supports by-hash by looking for by-hash entries
		for _, fi := range fil {
			if strings.Contains(fi.Path(), "by-hash/") {
				*byhash = true
				break
			}
		}

		slog.Debug("downloaded", "repo", p.mirrorID, "path", result.path)
		return fil, nil
	}

	return nil, errors.New("failed to download Release/InRelease")
}

// downloadRelease downloads Release/InRelease files and extracts index information
func (p *APTParser) downloadRelease(ctx context.Context, httpClient *HTTPClient, suite string) (map[string][]*apt.FileInfo, bool, error) {
	releaseFiles := p.config.ReleaseFiles(suite)
	results := make(chan *dlResult, len(releaseFiles))
	byhash := false

	// Launch download goroutines
	for _, path := range releaseFiles {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-httpClient.semaphore:
		}

		go httpClient.download(ctx, p.config, path, nil, false, results)
	}

	// Close results channel after all goroutines complete
	go func() {
		// Wait for all download goroutines to complete
		for i := 0; i < len(releaseFiles); i++ {
			<-httpClient.semaphore
			httpClient.semaphore <- struct{}{}
		}
		close(results)
	}()

	fil, err := p.handleReleaseResults(results, &byhash)
	if err != nil {
		return nil, false, err
	}

	indexMap := make(map[string][]*apt.FileInfo)
	for _, fi := range fil {
		err := addFileInfoToList(fi, indexMap, byhash)
		if err != nil {
			return nil, false, err
		}
	}

	slog.Debug("release info", "repo", p.mirrorID, "suite", suite, "by_hash", byhash, "files", len(fil))
	return indexMap, byhash, nil
}

// downloadIndices downloads index files (Packages, Sources, etc.)
func (p *APTParser) downloadIndices(ctx context.Context, httpClient *HTTPClient,
	indexMap map[string][]*apt.FileInfo, byhash bool) ([]*apt.FileInfo, error) {

	var indices []*apt.FileInfo
	for _, fil := range indexMap {
		for _, fi := range fil {
			path := fi.Path()
			if p.config.MatchingIndex(path) && apt.IsSupported(path) {
				indices = append(indices, fi)
			}
		}
	}

	if len(indices) == 0 {
		return nil, nil
	}

	return httpClient.downloadFiles(ctx, p.config, indices, false, byhash)
}

// downloadItems downloads package files listed in the indices
func (p *APTParser) downloadItems(ctx context.Context, httpClient *HTTPClient,
	indices []*apt.FileInfo, byhash bool) ([]*apt.FileInfo, error) {

	indexMap := make(map[string][]*apt.FileInfo)
	itemMap := make(map[string]*apt.FileInfo)

	err := p.extractItems(indices, indexMap, itemMap, byhash)
	if err != nil {
		return nil, err
	}

	if len(itemMap) == 0 {
		return nil, nil
	}

	var items []*apt.FileInfo
	for _, fi := range itemMap {
		items = append(items, fi)
	}

	return httpClient.downloadFiles(ctx, p.config, items, true, byhash)
}