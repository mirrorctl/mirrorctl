package mirror

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/gopenpgp/v3/crypto"
	"github.com/cheggaaa/pb/v3"
	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/internal/apt"
	"github.com/knqyf263/go-deb-version"
	"log/slog"
)

// APTParser handles parsing APT repository metadata
type APTParser struct {
	storage  *Storage
	config   *MirrConfig
	mirrorID string
	pgp      *crypto.PGPHandle
}

// NewAPTParser creates a new APT parser
func NewAPTParser(storage *Storage, config *MirrConfig, mirrorID string) *APTParser {
	return &APTParser{
		storage:  storage,
		config:   config,
		mirrorID: mirrorID,
		pgp:      crypto.PGP(),
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
		if closeErr := f.Close(); closeErr != nil {
			slog.Warn("failed to close file", "path", hashPath, "error", closeErr)
		}
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
func (p *APTParser) handleReleaseResults(results <-chan *dlResult, byhash *bool) ([]*apt.FileInfo, map[string]*dlResult, error) {
	downloaded := make(map[string]*dlResult)
	var allFileInfos []*apt.FileInfo
	var processedOne bool
	var downloadErrors []error

	for result := range results {
		if result.err != nil {
			downloadErrors = append(downloadErrors, result.err)
			slog.Warn("failed to download release file", "repo", p.mirrorID, "path", result.path, "error", result.err)
			if result.tempfile != nil {
				closeAndRemoveFile(result.tempfile)
			}
			continue
		}

		if result.status != 200 {
			err := errors.Newf("unexpected status code %d for %s", result.status, result.path)
			downloadErrors = append(downloadErrors, err)
			if result.tempfile != nil {
				closeAndRemoveFile(result.tempfile)
			}
			continue
		}

		// Store the result for PGP validation (don't clean up immediately)
		downloaded[path.Base(result.path)] = result
		slog.Debug("successfully downloaded release file", "repo", p.mirrorID, "file", path.Base(result.path), "status", result.status)

		// Only process the first successful Release or InRelease file for metadata extraction
		// Skip signature files (.gpg) as they don't contain metadata
		isMetadataFile := (path.Base(result.path) == "Release" || path.Base(result.path) == "InRelease" ||
			strings.HasSuffix(result.path, "Release.gz") || strings.HasSuffix(result.path, "Release.bz2") ||
			strings.HasSuffix(result.path, "InRelease.gz") || strings.HasSuffix(result.path, "InRelease.bz2"))

		if !processedOne && isMetadataFile {
			processedOne = true

			// Use result.fi if available, otherwise create one from path
			var releaseFile *apt.FileInfo
			if result.fi != nil {
				releaseFile = result.fi
			}

			resultPath := result.path
			hashPath := resultPath
			if *byhash && releaseFile != nil {
				hashPath = releaseFile.SHA256Path()
			}

			err := p.storage.StoreLink(releaseFile, result.tempfile.Name())
			if err != nil {
				return nil, nil, errors.Wrap(err, "storeLink")
			}

			f, err := p.storage.Open(hashPath)
			if err != nil {
				return nil, nil, err
			}

			fil, _, err := apt.ExtractFileInfo(resultPath, f)
			if closeErr := f.Close(); closeErr != nil {
				slog.Warn("failed to close file", "path", hashPath, "error", closeErr)
			}
			if err != nil {
				return nil, nil, err
			}

			// Check if the repository supports by-hash by looking for by-hash entries
			for _, fi := range fil {
				if strings.Contains(fi.Path(), "by-hash/") {
					*byhash = true
					break
				}
			}

			allFileInfos = append(allFileInfos, fil...)
		}
	}

	// Categorize errors by type
	var notFoundErrors, actualErrors []error
	for _, err := range downloadErrors {
		if strings.Contains(err.Error(), "unexpected status code 404") {
			notFoundErrors = append(notFoundErrors, err)
		} else {
			actualErrors = append(actualErrors, err)
		}
	}

	slog.Debug("download results summary", "repo", p.mirrorID,
		"successful_downloads", len(downloaded),
		"not_found_variants", len(notFoundErrors),
		"actual_errors", len(actualErrors))

	if len(notFoundErrors) > 0 {
		slog.Debug("some release file variants not available (expected)", "repo", p.mirrorID, "count", len(notFoundErrors))
	}

	if len(downloaded) == 0 {
		if len(actualErrors) > 0 {
			slog.Error("all release file downloads failed with errors", "repo", p.mirrorID, "errors", actualErrors)
			return nil, nil, errors.Wrap(errors.Join(actualErrors...), "failed to download Release/InRelease")
		} else if len(notFoundErrors) > 0 {
			slog.Error("no release files found - all variants returned 404", "repo", p.mirrorID, "tried", len(notFoundErrors))
			return nil, nil, errors.Wrap(errors.Join(notFoundErrors...), "no Release/InRelease files available")
		} else {
			slog.Error("no release files downloaded and no errors reported", "repo", p.mirrorID)
			return nil, nil, errors.New("failed to download Release/InRelease")
		}
	}

	if len(actualErrors) > 0 {
		slog.Warn("some release file downloads failed", "repo", p.mirrorID, "errors", actualErrors)
	}

	return allFileInfos, downloaded, nil
}

// downloadRelease downloads Release/InRelease files and extracts index information
func (p *APTParser) downloadRelease(ctx context.Context, httpClient *HTTPClient, suite string, m *Mirror) (map[string][]*apt.FileInfo, bool, error) {
	releaseFiles := p.config.ReleaseFiles(suite)
	results := make(chan *dlResult, len(releaseFiles))
	byhash := false

	slog.Debug("attempting to download release files", "repo", p.mirrorID, "suite", suite, "files", releaseFiles)

	// Launch download goroutines
	for _, path := range releaseFiles {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-httpClient.semaphore:
		}
		go httpClient.download(ctx, p.config, path, nil, false, results, nil)
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

	// Process all download results
	allFileInfos, downloaded, err := p.handleReleaseResults(results, &byhash)
	if err != nil {
		return nil, false, err
	}

	// Ensure temp files are cleaned up
	defer func() {
		for _, r := range downloaded {
			if r.tempfile != nil {
				closeAndRemoveFile(r.tempfile)
			}
		}
	}()

	// Perform PGP validation
	if err := p.verifyPGPSignature(m, suite, downloaded); err != nil {
		return nil, false, err
	}

	indexMap := make(map[string][]*apt.FileInfo)
	for _, fi := range allFileInfos {
		err := addFileInfoToList(fi, indexMap, byhash)
		if err != nil {
			return nil, false, err
		}
	}

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

	return httpClient.downloadFiles(ctx, p.config, indices, false, byhash, nil)
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

	// Apply package filtering if configured
	filteredItemMap := p.applyPackageFilters(itemMap)

	var items []*apt.FileInfo
	for _, fi := range filteredItemMap {
		items = append(items, fi)
	}

	// Check if we need to download files and show progress bar accordingly
	reusableCount, needDownloadCount := httpClient.countReusableFiles(items, byhash)

	if needDownloadCount == 0 {
		// All files will be reused - no progress bar needed
		slog.Info("all files up to date", "repo", p.mirrorID, "total", len(items), "reused", reusableCount)
		return httpClient.downloadFiles(ctx, p.config, items, true, byhash, nil)
	}

	// Some files need downloading - show progress bar
	var totalSize uint64
	for _, fi := range items {
		totalSize += fi.Size()
	}

	// Create progress bar with visual bar display
	bar := pb.New64(int64(totalSize))
	bar.Set(pb.Bytes, true)
	bar.SetTemplateString(`[{{string . "repo"}}] {{counters . }} {{bar . }} {{percent . }} {{speed . }}`)
	bar.Set("repo", p.mirrorID)
	bar.SetWriter(os.Stderr)
	bar.SetRefreshRate(time.Millisecond * 500) // Update every 500ms to show intermediate progress
	bar.Start()

	defer bar.Finish()

	return httpClient.downloadFiles(ctx, p.config, items, true, byhash, bar)
}

func (p *APTParser) verifyPGPSignature(m *Mirror, suite string, downloaded map[string]*dlResult) error {
	// PGP validation logic
	performCheck := !m.noPGPCheck && !m.mc.NoPGPCheck
	if !performCheck {
		return nil
	}

	if m.mc.PGPKeyPath == "" {
		return errors.Newf("PGP verification is required for repo '%s', but 'pgp_key_path' is not set", m.id)
	}

	keyringFile, err := os.Open(m.mc.PGPKeyPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open PGP key file: %s", m.mc.PGPKeyPath)
	}
	defer keyringFile.Close()

	keyringBytes, err := io.ReadAll(keyringFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read PGP keyring from: %s", m.mc.PGPKeyPath)
	}

	// Parse the keyring
	publicKey, err := crypto.NewKeyFromArmored(string(keyringBytes))
	if err != nil {
		return errors.Wrapf(err, "failed to parse PGP keyring from: %s", m.mc.PGPKeyPath)
	}

	// Strategy 1: Verify InRelease file
	if inReleaseResult, ok := downloaded["InRelease"]; ok {
		slog.Info("verifying InRelease signature", "repo", m.id, "suite", suite)
		_, err := inReleaseResult.tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "failed to seek InRelease tempfile")
		}

		// Try to decode as a clear-signed message first
		inReleaseBytes, err := io.ReadAll(inReleaseResult.tempfile)
		if err != nil {
			return errors.Wrap(err, "failed to read InRelease tempfile")
		}

		verifier, err := p.pgp.Verify().VerificationKey(publicKey).New()
		if err != nil {
			return errors.Wrap(err, "failed to create verifier")
		}

		verifyResult, err := verifier.VerifyCleartext(inReleaseBytes)
		if err != nil {
			return errors.Wrapf(err, "PGP signature verification failed for InRelease file in repo '%s'", m.id)
		}

		if sigErr := verifyResult.SignatureError(); sigErr != nil {
			return errors.Wrapf(sigErr, "PGP signature verification failed for InRelease file in repo '%s'", m.id)
		}

		slog.Info("PGP signature for clear-signed InRelease is valid", "repo", m.id, "suite", suite, "key_id", publicKey.GetHexKeyID())
		return nil
	}

	// Strategy 2: Verify Release + Release.gpg
	releaseResult, releaseOK := downloaded["Release"]
	releaseGPGResult, releaseGPGOK := downloaded["Release.gpg"]
	if releaseOK && releaseGPGOK {
		slog.Info("verifying Release signature", "repo", m.id, "suite", suite)
		_, err := releaseResult.tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "failed to seek Release tempfile")
		}
		_, err = releaseGPGResult.tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "failed to seek Release.gpg tempfile")
		}

		releaseBytes, err := io.ReadAll(releaseResult.tempfile)
		if err != nil {
			return errors.Wrap(err, "failed to read Release tempfile")
		}

		sigBytes, err := io.ReadAll(releaseGPGResult.tempfile)
		if err != nil {
			return errors.Wrap(err, "failed to read Release.gpg tempfile")
		}

		verifier, err := p.pgp.Verify().VerificationKey(publicKey).New()
		if err != nil {
			return errors.Wrap(err, "failed to create verifier")
		}

		verifyResult, err := verifier.VerifyDetached(releaseBytes, sigBytes, crypto.Armor)
		if err != nil {
			return errors.Wrapf(err, "PGP signature verification failed for Release file in repo '%s'", m.id)
		}

		if sigErr := verifyResult.SignatureError(); sigErr != nil {
			return errors.Wrapf(sigErr, "PGP signature verification failed for Release file in repo '%s'", m.id)
		}

		slog.Info("PGP signature for Release is valid", "repo", m.id, "suite", suite, "key_id", publicKey.GetHexKeyID())
		return nil
	}

	return errors.Newf("PGP verification failed for repo '%s': no valid signed file found (checked InRelease, Release+Release.gpg)", m.id)
}

// packageNameVersion holds parsed package name and version from filename
type packageNameVersion struct {
	name    string
	version string
}

// parsePackageNameVersion extracts package name and version from a .deb filename
func parsePackageNameVersion(filePath string) packageNameVersion {
	filename := path.Base(filePath)

	// Check if it's a .deb file
	if !strings.HasSuffix(filename, ".deb") {
		return packageNameVersion{}
	}

	// Remove .deb extension
	nameVersionArch := strings.TrimSuffix(filename, ".deb")

	// Split by underscores: name_version_architecture
	parts := strings.Split(nameVersionArch, "_")
	if len(parts) < 3 {
		return packageNameVersion{}
	}

	name := parts[0]
	// Version is everything between name and architecture
	version := strings.Join(parts[1:len(parts)-1], "_")

	return packageNameVersion{
		name:    name,
		version: version,
	}
}

// applyPackageFilters filters packages based on configured rules
func (p *APTParser) applyPackageFilters(itemMap map[string]*apt.FileInfo) map[string]*apt.FileInfo {
	if p.config.Filters == nil {
		slog.Debug("no package filters configured", "repo", p.mirrorID)
		return itemMap // No filtering configured
	}

	slog.Debug("applying package filters", "repo", p.mirrorID,
		"keep_versions", p.config.Filters.KeepVersions,
		"exclude_patterns", len(p.config.Filters.ExcludePatterns),
		"total_items", len(itemMap))

	// Group packages by name from filename
	packages := make(map[string][]*apt.FileInfo)
	skippedFiles := 0

	for filePath, fileInfo := range itemMap {
		// Parse package name and version from filename
		nameVersion := parsePackageNameVersion(filePath)
		if nameVersion.name == "" {
			skippedFiles++
			continue // Not a package file
		}

		// Check exclude patterns
		if p.shouldExcludePackageByName(nameVersion.name, nameVersion.version) {
			slog.Debug("excluding package by pattern", "repo", p.mirrorID,
				"package", nameVersion.name, "version", nameVersion.version)
			continue
		}

		packages[nameVersion.name] = append(packages[nameVersion.name], fileInfo)
	}

	slog.Debug("package grouping results", "repo", p.mirrorID,
		"total_files", len(itemMap), "skipped_files", skippedFiles,
		"unique_packages", len(packages))

	// Apply version filtering
	filteredMap := make(map[string]*apt.FileInfo)
	totalPackages := 0
	keptPackages := 0

	for packageName, versions := range packages {
		totalPackages += len(versions)

		// Sort versions in descending order (newest first)
		sort.Slice(versions, func(i, j int) bool {
			nv1 := parsePackageNameVersion(versions[i].Path())
			nv2 := parsePackageNameVersion(versions[j].Path())

			v1, err1 := version.NewVersion(nv1.version)
			v2, err2 := version.NewVersion(nv2.version)

			if err1 != nil || err2 != nil {
				// Fallback to string comparison if version parsing fails
				return nv1.version > nv2.version
			}

			return v1.GreaterThan(v2)
		})

		// Keep only the specified number of versions
		keepCount := len(versions)
		if p.config.Filters.KeepVersions > 0 && p.config.Filters.KeepVersions < len(versions) {
			keepCount = p.config.Filters.KeepVersions
		}

		for i := 0; i < keepCount; i++ {
			pkg := versions[i]
			filteredMap[pkg.Path()] = pkg
			keptPackages++
		}

		if len(versions) > keepCount {
			slog.Debug("filtered package versions", "repo", p.mirrorID,
				"package", packageName, "total_versions", len(versions),
				"kept_versions", keepCount)
		}
	}

	slog.Info("package filtering complete", "repo", p.mirrorID,
		"total_packages", totalPackages, "kept_packages", keptPackages,
		"filtered_out", totalPackages-keptPackages)

	return filteredMap
}

// shouldExcludePackageByName checks if a package should be excluded based on patterns
func (p *APTParser) shouldExcludePackageByName(name, version string) bool {
	if p.config.Filters.ExcludePatterns == nil {
		return false
	}

	fullName := name + "_" + version
	for _, pattern := range p.config.Filters.ExcludePatterns {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, version); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, fullName); matched {
			return true
		}
	}

	return false
}

// formatBytes formats a byte count as a human-readable string
func formatBytes(bytes uint64) string {
	if bytes == 0 {
		return "0 B"
	}

	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(bytes)
	unitIndex := 0

	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}

	if unitIndex == 0 {
		return fmt.Sprintf("%.0f %s", size, units[unitIndex])
	}
	return fmt.Sprintf("%.2f %s", size, units[unitIndex])
}
