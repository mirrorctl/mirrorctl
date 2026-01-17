package mirror

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/gopenpgp/v3/crypto"

	"github.com/mirrorctl/mirrorctl/internal/apt"
)

func TestParsePackageNameVersion(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		wantName string
		wantVer  string
	}{
		{
			name:     "standard package",
			filePath: "pool/main/r/rear/rear_2.7-0_amd64.deb",
			wantName: "rear",
			wantVer:  "2.7-0",
		},
		{
			name:     "simple package",
			filePath: "vim_8.2.0_amd64.deb",
			wantName: "vim",
			wantVer:  "8.2.0",
		},
		{
			name:     "package with epoch",
			filePath: "git_1%3a2.25.1-1ubuntu3_amd64.deb",
			wantName: "git",
			wantVer:  "1%3a2.25.1-1ubuntu3",
		},
		{
			name:     "package with complex version",
			filePath: "libssl1.1_1.1.1f-1ubuntu2.16_amd64.deb",
			wantName: "libssl1.1",
			wantVer:  "1.1.1f-1ubuntu2.16",
		},
		{
			name:     "package name with hyphens",
			filePath: "libgdk-pixbuf_2.40.0+dfsg-3_amd64.deb",
			wantName: "libgdk-pixbuf",
			wantVer:  "2.40.0+dfsg-3",
		},
		{
			name:     "invalid file extension",
			filePath: "package.tar.gz",
			wantName: "",
			wantVer:  "",
		},
		{
			name:     "not enough parts",
			filePath: "package.deb",
			wantName: "",
			wantVer:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePackageNameVersion(tt.filePath)
			if got.name != tt.wantName {
				t.Errorf("parsePackageNameVersion(%q).name = %q, want %q", tt.filePath, got.name, tt.wantName)
			}
			if got.version != tt.wantVer {
				t.Errorf("parsePackageNameVersion(%q).version = %q, want %q", tt.filePath, got.version, tt.wantVer)
			}
		})
	}
}

func TestShouldExcludePackageByName(t *testing.T) {
	config := &MirrorConfig{
		Filters: &PackageFilters{
			ExcludePatterns: []string{"vim*", "emacs", "*debug*", "*-dev"},
		},
	}
	ap := &APTParser{
		config:   config,
		mirrorID: "test",
	}

	tests := []struct {
		name    string
		pkgName string
		version string
		want    bool
	}{
		{
			name:    "exact match exclusion",
			pkgName: "emacs",
			version: "27.1",
			want:    true,
		},
		{
			name:    "pattern match exclusion (prefix)",
			pkgName: "vim-tiny",
			version: "8.2",
			want:    true,
		},
		{
			name:    "pattern match exclusion (wildcard)",
			pkgName: "libc6-dbg", // matches *debug*? No.
			version: "2.31",
			want:    false,
		},
		{
			name:    "pattern match exclusion (wildcard 2)",
			pkgName: "mypackage-debug-symbols",
			version: "1.0",
			want:    true,
		},
		{
			name:    "no match (allowed)",
			pkgName: "nano",
			version: "4.8",
			want:    false,
		},
		{
			name:    "version match",
			pkgName: "mypackage",
			version: "1.0-debug",
			want:    true,
		},
		{
			name:    "pattern match exclusion (suffix)",
			pkgName: "libfoo-dev",
			version: "1.0",
			want:    true,
		},
		{
			name:    "no match for similar suffix",
			pkgName: "libfoo-devel",
			version: "1.0",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ap.shouldExcludePackageByName(tt.pkgName, tt.version)
			if got != tt.want {
				t.Errorf("shouldExcludePackageByName(%q, %q) = %v, want %v", tt.pkgName, tt.version, got, tt.want)
			}
		})
	}
}

func TestApplyPackageFilters(t *testing.T) {
	// Setup test data
	itemMap := map[string]*apt.FileInfo{
		"pool/vim_8.0_amd64.deb": apt.MakeFileInfoNoChecksum("pool/vim_8.0_amd64.deb", 100),
		"pool/vim_8.1_amd64.deb": apt.MakeFileInfoNoChecksum("pool/vim_8.1_amd64.deb", 100),
		"pool/vim_8.2_amd64.deb": apt.MakeFileInfoNoChecksum("pool/vim_8.2_amd64.deb", 100), // Newest

		"pool/nano_4.0_amd64.deb": apt.MakeFileInfoNoChecksum("pool/nano_4.0_amd64.deb", 100),

		"pool/exclude-me_1.0_amd64.deb": apt.MakeFileInfoNoChecksum("pool/exclude-me_1.0_amd64.deb", 100),

		"pool/not-a-package.txt": apt.MakeFileInfoNoChecksum("pool/not-a-package.txt", 10),
	}

	tests := []struct {
		name         string
		keepVersions int
		exclude      []string
		wantCount    int
		wantPresent  []string
		wantMissing  []string
	}{
		{
			name:         "no filters",
			keepVersions: 0,
			exclude:      nil,
			wantCount:    5, // vim (3), nano (1), exclude-me (1). not-a-package.txt is skipped.
			wantPresent:  []string{"pool/vim_8.2_amd64.deb", "pool/exclude-me_1.0_amd64.deb"},
		},
		{
			name:         "keep last 2 versions",
			keepVersions: 2,
			exclude:      nil,
			wantCount:    4, // vim (3->2), nano (1->1), exclude-me (1->1) = 4
			wantPresent:  []string{"pool/vim_8.2_amd64.deb", "pool/vim_8.1_amd64.deb"},
			wantMissing:  []string{"pool/vim_8.0_amd64.deb"},
		},
		{
			name:         "exclude pattern",
			keepVersions: 0,
			exclude:      []string{"exclude-*"},
			wantCount:    4, // vim (3), nano (1). exclude-me removed.
			wantMissing:  []string{"pool/exclude-me_1.0_amd64.deb"},
		},
		{
			name:         "combine keep versions and exclude",
			keepVersions: 1,
			exclude:      []string{"nano"},
			wantCount:    2, // vim (3->1), nano (excluded), exclude-me (1->1). Total 2.
			wantPresent:  []string{"pool/vim_8.2_amd64.deb", "pool/exclude-me_1.0_amd64.deb"},
			wantMissing:  []string{"pool/vim_8.1_amd64.deb", "pool/vim_8.0_amd64.deb", "pool/nano_4.0_amd64.deb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &MirrorConfig{
				Filters: &PackageFilters{
					KeepVersions:    tt.keepVersions,
					ExcludePatterns: tt.exclude,
				},
			}
			ap := &APTParser{
				config:   config,
				mirrorID: "test",
			}

			gotMap := ap.applyPackageFilters(itemMap)

			// Check count
			if len(gotMap) != tt.wantCount {
				t.Errorf("got %d items, want %d", len(gotMap), tt.wantCount)
			}

			// Check presence
			for _, p := range tt.wantPresent {
				if _, ok := gotMap[p]; !ok {
					t.Errorf("expected %s to be present", p)
				}
			}

			// Check absence
			for _, p := range tt.wantMissing {
				if _, ok := gotMap[p]; ok {
					t.Errorf("expected %s to be missing", p)
				}
			}
		})
	}
}

func TestVerifyPGPSignature_Disabled(t *testing.T) {
	// Test that it returns nil (no error) when disabled
	config := &MirrorConfig{
		NoPGPCheck: true,
	}
	// Need to set noPGPCheck on Mirror struct which is passed in
	// But verifyPGPSignature takes *Mirror

	// We need to construct a Mirror struct.
	// Since Mirror struct has private fields and we are in the same package, we can set them.

	m := &Mirror{
		id: "test-mirror",
		mc: config,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
	}

	err := ap.verifyPGPSignature(m, "stable", nil)
	if err != nil {
		t.Errorf("expected no error when PGP check is disabled, got %v", err)
	}
}

func TestIsIndexFile(t *testing.T) {
	ap := &APTParser{}

	tests := []struct {
		path string
		want bool
	}{
		{"dists/stable/Packages", true},
		{"dists/stable/Packages.gz", true},
		{"dists/stable/Packages.xz", true},
		{"dists/stable/Sources", true},
		{"dists/stable/Sources.gz", true},
		{"dists/stable/Contents-amd64", false},    // Current implementation does not recognize Contents-arch as index
		{"dists/stable/Contents-amd64.gz", false}, // Current implementation does not recognize Contents-arch as index
		{"dists/stable/Release", false},           // Release is not an "index" in this context (it's metadata)
		{"pool/main/p/package.deb", false},
		{"Index", true},
	}

	for _, tt := range tests {
		got := ap.isIndexFile(tt.path)
		if got != tt.want {
			t.Errorf("isIndexFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// Helper function to create a temp file from testdata
func createTempFileFromTestdata(t *testing.T, testdataPath string) *os.File {
	t.Helper()
	content, err := os.ReadFile(testdataPath)
	if err != nil {
		t.Fatalf("failed to read testdata file %s: %v", testdataPath, err)
	}
	tmpFile, err := os.CreateTemp(t.TempDir(), "pgptest-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to write to temp file: %v", err)
	}
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to seek temp file: %v", err)
	}
	return tmpFile
}

func TestVerifyPGPSignature_InRelease_Valid(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	publicKeyPath := filepath.Join(testdataDir, "public-key.asc")

	// Verify test fixtures exist
	if _, err := os.Stat(publicKeyPath); err != nil {
		t.Fatalf("test fixture missing: %s", publicKeyPath)
	}

	config := &MirrorConfig{
		PGPKeyPath: publicKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	inReleaseFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "InRelease"))
	defer func() {
		inReleaseFile.Close()
		os.Remove(inReleaseFile.Name())
	}()

	downloaded := map[string]*dlResult{
		"InRelease": {
			tempfile: inReleaseFile,
		},
	}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err != nil {
		t.Errorf("expected successful verification of valid InRelease, got error: %v", err)
	}
}

func TestVerifyPGPSignature_Release_Valid(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	publicKeyPath := filepath.Join(testdataDir, "public-key.asc")

	config := &MirrorConfig{
		PGPKeyPath: publicKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	releaseFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "Release"))
	releaseGPGFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "Release.gpg"))
	defer func() {
		releaseFile.Close()
		releaseGPGFile.Close()
		os.Remove(releaseFile.Name())
		os.Remove(releaseGPGFile.Name())
	}()

	downloaded := map[string]*dlResult{
		"Release": {
			tempfile: releaseFile,
		},
		"Release.gpg": {
			tempfile: releaseGPGFile,
		},
	}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err != nil {
		t.Errorf("expected successful verification of valid Release+Release.gpg, got error: %v", err)
	}
}

func TestVerifyPGPSignature_InvalidSignature(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	publicKeyPath := filepath.Join(testdataDir, "public-key.asc")

	config := &MirrorConfig{
		PGPKeyPath: publicKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	// Use the corrupted InRelease file
	inReleaseFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "InRelease.invalid"))
	defer func() {
		inReleaseFile.Close()
		os.Remove(inReleaseFile.Name())
	}()

	downloaded := map[string]*dlResult{
		"InRelease": {
			tempfile: inReleaseFile,
		},
	}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error for invalid signature, got nil")
	}
}

func TestVerifyPGPSignature_WrongKey(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	wrongKeyPath := filepath.Join(testdataDir, "wrong-public-key.asc")

	// Verify test fixtures exist
	if _, err := os.Stat(wrongKeyPath); err != nil {
		t.Fatalf("test fixture missing: %s", wrongKeyPath)
	}

	config := &MirrorConfig{
		PGPKeyPath: wrongKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	// Try to verify with the correct signature but wrong key
	inReleaseFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "InRelease"))
	defer func() {
		inReleaseFile.Close()
		os.Remove(inReleaseFile.Name())
	}()

	downloaded := map[string]*dlResult{
		"InRelease": {
			tempfile: inReleaseFile,
		},
	}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error for wrong key, got nil")
	}
}

func TestVerifyPGPSignature_TamperedContent(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	publicKeyPath := filepath.Join(testdataDir, "public-key.asc")

	config := &MirrorConfig{
		PGPKeyPath: publicKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	// Use tampered Release with the original (valid) signature
	tamperedReleaseFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "Release.tampered"))
	releaseGPGFile := createTempFileFromTestdata(t, filepath.Join(testdataDir, "Release.gpg"))
	defer func() {
		tamperedReleaseFile.Close()
		releaseGPGFile.Close()
		os.Remove(tamperedReleaseFile.Name())
		os.Remove(releaseGPGFile.Name())
	}()

	downloaded := map[string]*dlResult{
		"Release": {
			tempfile: tamperedReleaseFile,
		},
		"Release.gpg": {
			tempfile: releaseGPGFile,
		},
	}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error for tampered content, got nil")
	}
}

func TestVerifyPGPSignature_MissingKeyFile(t *testing.T) {
	config := &MirrorConfig{
		PGPKeyPath: "/nonexistent/path/to/key.asc",
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	downloaded := map[string]*dlResult{}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}

func TestVerifyPGPSignature_NoSignedFile(t *testing.T) {
	testdataDir := filepath.Join("testdata", "pgp")
	publicKeyPath := filepath.Join(testdataDir, "public-key.asc")

	config := &MirrorConfig{
		PGPKeyPath: publicKeyPath,
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	// Empty downloaded map - no InRelease or Release files
	downloaded := map[string]*dlResult{}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error when no signed files are available, got nil")
	}
	if err != nil && err.Error() != "PGP verification failed for repo 'test-mirror': no valid signed file found (checked InRelease, Release+Release.gpg)" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestVerifyPGPSignature_MissingPGPKeyPath(t *testing.T) {
	config := &MirrorConfig{
		PGPKeyPath: "",
		NoPGPCheck: false,
	}
	m := &Mirror{
		id:         "test-mirror",
		mc:         config,
		noPGPCheck: false,
	}

	ap := &APTParser{
		config:   config,
		mirrorID: "test-mirror",
		pgp:      crypto.PGP(),
	}

	downloaded := map[string]*dlResult{}

	err := ap.verifyPGPSignature(m, "stable", downloaded)
	if err == nil {
		t.Error("expected error for missing PGPKeyPath, got nil")
	}
	if err != nil && err.Error() != "PGP verification is required for repo 'test-mirror', but 'pgp_key_path' is not set" {
		t.Errorf("unexpected error message: %v", err)
	}
}
