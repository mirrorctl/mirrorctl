package apt

import (
	"strings"
	"testing"
)

func TestValidateRepositoryPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		// Valid paths
		{"valid relative path", "main/binary-amd64/Packages", false},
		{"valid nested path", "pool/main/a/apache2/apache2_2.4.41-4ubuntu3_amd64.deb", false},
		{"valid path with dots in filename", "main/i18n/Translation-en.bz2", false},
		{"valid by-hash path", "main/binary-amd64/by-hash/SHA256/abcdef123456", false},

		// Path traversal attempts
		{"parent directory reference", "../etc/passwd", true},
		{"nested parent directory", "main/../../../etc/passwd", true},
		{"parent in middle", "main/../binary-amd64/Packages", true},
		{"multiple parent references", "../../usr/bin/malicious", true},
		{"parent with slash", "../binary-amd64/Packages", true},

		// Absolute path attempts
		{"absolute unix path", "/etc/passwd", true},
		{"absolute path", "/usr/bin/malicious", true},
		{"windows absolute path", "C:\\Windows\\System32", true},
		{"root reference", "/", true},

		// Edge cases
		{"empty path", "", false}, // Empty path should be allowed (some checksums might be empty)
		{"just filename", "Packages", false},
		{"single dot", ".", false},                      // Current directory reference should be allowed after cleaning
		{"double slash", "main//binary-amd64", false},   // Should be cleaned and allowed
		{"trailing slash", "main/binary-amd64/", false}, // Should be cleaned and allowed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRepositoryPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRepositoryPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestParseChecksumSecurity(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid checksum line",
			line:    "d41d8cd98f00b204e9800998ecf8427e 0 main/binary-amd64/Packages",
			wantErr: false,
		},
		{
			name:    "path traversal in checksum",
			line:    "d41d8cd98f00b204e9800998ecf8427e 1024 ../../../etc/passwd",
			wantErr: true,
			errMsg:  "directory traversal",
		},
		{
			name:    "absolute path in checksum",
			line:    "d41d8cd98f00b204e9800998ecf8427e 1024 /etc/passwd",
			wantErr: true,
			errMsg:  "absolute path",
		},
		{
			name:    "nested path traversal",
			line:    "d41d8cd98f00b204e9800998ecf8427e 1024 main/../../bin/malicious",
			wantErr: true,
			errMsg:  "directory traversal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseChecksum(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseChecksum(%q) error = %v, wantErr %v", tt.line, err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("parseChecksum(%q) error = %v, wanted error containing %q", tt.line, err, tt.errMsg)
			}
		})
	}
}

func TestGetFilesFromPackagesSecurity(t *testing.T) {
	// Test malicious Packages file with path traversal
	maliciousPackages := `Package: malicious-package
Version: 1.0
Filename: ../../../etc/passwd
Size: 1024
MD5sum: d41d8cd98f00b204e9800998ecf8427e

`

	_, _, err := getFilesFromPackages("test", strings.NewReader(maliciousPackages))
	if err == nil {
		t.Error("Expected error for malicious Filename, got nil")
	}
	if !strings.Contains(err.Error(), "directory traversal") {
		t.Errorf("Expected 'directory traversal' error, got: %v", err)
	}
}

func TestGetFilesFromSourcesSecurity(t *testing.T) {
	// Test malicious Sources file with path traversal in Directory
	maliciousSources := `Package: test-package
Directory: ../../../etc
Files:
 d41d8cd98f00b204e9800998ecf8427e 1024 passwd

`

	_, _, err := getFilesFromSources("test", strings.NewReader(maliciousSources))
	if err == nil {
		t.Error("Expected error for malicious Directory, got nil")
	}
	if !strings.Contains(err.Error(), "directory traversal") {
		t.Errorf("Expected 'directory traversal' error, got: %v", err)
	}
}

func TestGetFilesFromReleaseSecurity(t *testing.T) {
	// Test malicious Release file with path traversal
	maliciousRelease := `Origin: Malicious
Label: Malicious
Suite: malicious
MD5Sum:
 d41d8cd98f00b204e9800998ecf8427e 1024 ../../../etc/passwd
 e3b0c44298fc1c149afbf4c8996fb924 512 ../../bin/malicious
SHA256:
 e3b0c44298fc1c149afbf4c8996fb924 1024 /absolute/path/attack
`

	_, _, err := getFilesFromRelease("dists/malicious/Release", strings.NewReader(maliciousRelease))
	if err == nil {
		t.Error("Expected error for malicious Release file, got nil")
	}
	if !strings.Contains(err.Error(), "directory traversal") && !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("Expected path traversal or absolute path error, got: %v", err)
	}
}
