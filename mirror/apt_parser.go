package mirror

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/clearsign"

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

	if len(downloaded) == 0 {
		if len(downloadErrors) > 0 {
			return nil, nil, errors.Wrap(errors.Join(downloadErrors...), "failed to download Release/InRelease")
		}
		return nil, nil, errors.New("failed to download Release/InRelease")
	}

	return allFileInfos, downloaded, nil
}

// downloadRelease downloads Release/InRelease files and extracts index information
func (p *APTParser) downloadRelease(ctx context.Context, httpClient *HTTPClient, suite string, m *Mirror) (map[string][]*apt.FileInfo, bool, error) {
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

	keyring, err := openpgp.ReadArmoredKeyRing(keyringFile)
	if err != nil {
		return errors.Wrapf(err, "failed to read PGP keyring from: %s", m.mc.PGPKeyPath)
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
		block, _ := clearsign.Decode(inReleaseBytes)
		if block != nil {
			// It's a clear-signed message
			_, err = openpgp.CheckDetachedSignature(keyring, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body)
			if err != nil {
				// Check if this is a BSN Pgp compatibility issue with golang.org/x/crypto/openpgp
				if strings.Contains(err.Error(), "tag byte does not have MSB set") ||
					strings.Contains(err.Error(), "invalid data") {
					slog.Warn("Go openpgp library cannot parse BSN Pgp signature, attempting external gpg verification",
						"repo", m.id, "suite", suite, "error", err)
					
					// Fallback to external GPG verification for BSN Pgp signatures
					if fallbackErr := p.verifyWithExternalGPG(m, suite, inReleaseBytes); fallbackErr != nil {
						return errors.Wrapf(fallbackErr, "PGP signature verification failed for InRelease file in repo '%s': both Go openpgp and external gpg verification failed", m.id)
					}
					slog.Info("PGP signature for InRelease verified using external gpg", "repo", m.id, "suite", suite)
					return nil
				}
				return errors.Wrapf(err, "PGP signature verification failed for clear-signed InRelease file in repo '%s'", m.id)
			}
			slog.Info("PGP signature for clear-signed InRelease is valid", "repo", m.id, "suite", suite)
			return nil
		}

		// If not clear-signed, try to read as a regular PGP message
		_, err = inReleaseResult.tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "failed to seek InRelease tempfile")
		}
		_, err = openpgp.ReadMessage(inReleaseResult.tempfile, keyring, nil, nil)
		if err != nil {
			return errors.Wrapf(err, "PGP signature verification failed for InRelease file in repo '%s'", m.id)
		}
		slog.Info("PGP signature for InRelease is valid", "repo", m.id, "suite", suite)
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

		_, err = openpgp.CheckDetachedSignature(keyring, releaseResult.tempfile, releaseGPGResult.tempfile)
		if err != nil {
			// Check if this is a BSN Pgp compatibility issue with golang.org/x/crypto/openpgp
			if strings.Contains(err.Error(), "tag byte does not have MSB set") ||
				strings.Contains(err.Error(), "invalid data") {
				slog.Warn("Go openpgp library cannot parse BSN Pgp signature, attempting external gpg verification",
					"repo", m.id, "suite", suite, "error", err)
				
				// Fallback to external GPG verification for BSN Pgp signatures
				if fallbackErr := p.verifyDetachedWithExternalGPG(m, suite, releaseResult.tempfile.Name(), releaseGPGResult.tempfile.Name()); fallbackErr != nil {
					return errors.Wrapf(fallbackErr, "PGP signature verification failed for Release file in repo '%s': both Go openpgp and external gpg verification failed", m.id)
				}
				slog.Info("PGP signature for Release verified using external gpg", "repo", m.id, "suite", suite)
				return nil
			}
			return errors.Wrapf(err, "PGP signature verification failed for Release file in repo '%s'", m.id)
		}
		slog.Info("PGP signature for Release is valid", "repo", m.id, "suite", suite)
		return nil
	}

	return errors.Newf("PGP verification failed for repo '%s': no valid signed file found (checked InRelease, Release+Release.gpg)", m.id)
}

// verifyWithExternalGPG attempts to verify a PGP signature using external gpg command
// This is a fallback for BSN Pgp signatures that the Go openpgp library cannot parse
func (p *APTParser) verifyWithExternalGPG(m *Mirror, suite string, signedData []byte) error {
	// Check if gpg is available
	if _, err := exec.LookPath("gpg"); err != nil {
		return errors.Wrap(err, "gpg command not found, cannot perform external verification")
	}

	// Create a temporary file for the signed data
	tempFile, err := os.CreateTemp("", "inrelease-*.tmp")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file for gpg verification")
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	// Write the signed data to the temporary file
	if _, err := tempFile.Write(signedData); err != nil {
		return errors.Wrap(err, "failed to write data to temporary file")
	}
	if err := tempFile.Close(); err != nil {
		return errors.Wrap(err, "failed to close temporary file")
	}

	// Import the keyring to a temporary GPG home directory
	tempGPGHome, err := os.MkdirTemp("", "gpg-home-*")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary GPG home directory")
	}
	defer os.RemoveAll(tempGPGHome)

	// Import the keyring
	importCmd := exec.Command("gpg", "--homedir", tempGPGHome, "--import", m.mc.PGPKeyPath)
	importCmd.Stderr = nil // Suppress import output
	if err := importCmd.Run(); err != nil {
		return errors.Wrapf(err, "failed to import PGP key for external verification: %s", m.mc.PGPKeyPath)
	}

	// Verify the signature
	verifyCmd := exec.Command("gpg", "--homedir", tempGPGHome, "--verify", tempFile.Name())
	output, err := verifyCmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "external gpg verification failed: %s", string(output))
	}

	// Check if the output indicates a good signature
	outputStr := string(output)
	if !strings.Contains(outputStr, "Good signature") {
		return errors.Newf("external gpg verification did not report a good signature: %s", outputStr)
	}

	return nil
}

// verifyDetachedWithExternalGPG attempts to verify a detached PGP signature using external gpg command
// This is a fallback for BSN Pgp signatures that the Go openpgp library cannot parse
func (p *APTParser) verifyDetachedWithExternalGPG(m *Mirror, suite string, dataFile, sigFile string) error {
	// Check if gpg is available
	if _, err := exec.LookPath("gpg"); err != nil {
		return errors.Wrap(err, "gpg command not found, cannot perform external verification")
	}

	// Import the keyring to a temporary GPG home directory
	tempGPGHome, err := os.MkdirTemp("", "gpg-home-*")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary GPG home directory")
	}
	defer os.RemoveAll(tempGPGHome)

	// Import the keyring
	importCmd := exec.Command("gpg", "--homedir", tempGPGHome, "--import", m.mc.PGPKeyPath)
	importCmd.Stderr = nil // Suppress import output
	if err := importCmd.Run(); err != nil {
		return errors.Wrapf(err, "failed to import PGP key for external verification: %s", m.mc.PGPKeyPath)
	}

	// Verify the detached signature
	verifyCmd := exec.Command("gpg", "--homedir", tempGPGHome, "--verify", sigFile, dataFile)
	output, err := verifyCmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "external gpg verification failed: %s", string(output))
	}

	// Check if the output indicates a good signature
	outputStr := string(output)
	if !strings.Contains(outputStr, "Good signature") {
		return errors.Newf("external gpg verification did not report a good signature: %s", outputStr)
	}

	return nil
}