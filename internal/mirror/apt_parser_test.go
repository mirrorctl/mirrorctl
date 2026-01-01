package mirror

import (
	"testing"

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
