package apt

import (
	"strings"
	"testing"
)

func TestSHA512Support(t *testing.T) {
	// Test SHA512 parsing from Release file
	releaseContent := `Origin: Ubuntu
Label: Ubuntu
Suite: focal
Version: 20.04
SHA512:
 1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef          1024 main/binary-amd64/Packages
 fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321           512 main/binary-i386/Packages
`

	files, _, err := getFilesFromRelease("dists/focal/Release", strings.NewReader(releaseContent))
	if err != nil {
		t.Fatalf("Failed to parse release with SHA512: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}

	// Create map for easier checking since file order is not guaranteed
	fileMap := make(map[string]*FileInfo)
	for _, fi := range files {
		fileMap[fi.Path()] = fi
	}

	// Check amd64 file
	fi1, exists := fileMap["dists/focal/main/binary-amd64/Packages"]
	if !exists {
		t.Error("Expected amd64 file to be present")
	} else {
		if fi1.Size() != 1024 {
			t.Errorf("Expected size 1024, got %d", fi1.Size())
		}
		if len(fi1.sha512sum) == 0 {
			t.Error("Expected SHA512 checksum to be present")
		}

		// Test SHA512Path method
		expectedSHA512Path := "dists/focal/main/binary-amd64/by-hash/SHA512/1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
		if fi1.SHA512Path() != expectedSHA512Path {
			t.Errorf("Expected SHA512Path '%s', got '%s'", expectedSHA512Path, fi1.SHA512Path())
		}
	}

	// Check i386 file
	fi2, exists := fileMap["dists/focal/main/binary-i386/Packages"]
	if !exists {
		t.Error("Expected i386 file to be present")
	} else {
		if fi2.Size() != 512 {
			t.Errorf("Expected size 512, got %d", fi2.Size())
		}
		if len(fi2.sha512sum) == 0 {
			t.Error("Expected SHA512 checksum to be present")
		}
	}
}

func TestSHA512InPackages(t *testing.T) {
	// Test SHA512 parsing from Packages file
	packagesContent := `Package: test-package
Version: 1.0
Filename: pool/main/t/test-package/test-package_1.0_amd64.deb
Size: 2048
MD5sum: 098f6bcd4621d373cade4e832627b4f6
SHA1: a94a8fe5ccb19ba61c4c0873d391e987982fbbd3
SHA256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
SHA512: ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff

`

	files, _, err := getFilesFromPackages("dists/focal/main/binary-amd64/Packages", strings.NewReader(packagesContent))
	if err != nil {
		t.Fatalf("Failed to parse packages with SHA512: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(files))
	}

	fi := files[0]
	if fi.Path() != "pool/main/t/test-package/test-package_1.0_amd64.deb" {
		t.Errorf("Expected path 'pool/main/t/test-package/test-package_1.0_amd64.deb', got '%s'", fi.Path())
	}
	if fi.Size() != 2048 {
		t.Errorf("Expected size 2048, got %d", fi.Size())
	}
	if len(fi.sha512sum) == 0 {
		t.Error("Expected SHA512 checksum to be present")
	}

	// Test SHA512Path method
	expectedSHA512Path := "pool/main/t/test-package/by-hash/SHA512/ee26b0dd4af7e749aa1a8ee3c10ae9923f618980772e473f8819a5d4940e0db27ac185f8a0e1d5f84f88bc887fd67b143732c304cc5fa9ad8e6f57f50028a8ff"
	if fi.SHA512Path() != expectedSHA512Path {
		t.Errorf("Expected SHA512Path '%s', got '%s'", expectedSHA512Path, fi.SHA512Path())
	}
}

func TestSHA512JSON(t *testing.T) {
	// Test JSON marshaling/unmarshaling with SHA512
	fi := &FileInfo{
		path: "test/file.deb",
		size: 1024,
		sha512sum: []byte{0xee, 0x26, 0xb0, 0xdd, 0x4a, 0xf7, 0xe7, 0x49},
	}

	data, err := fi.MarshalJSON()
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	var fi2 FileInfo
	err = fi2.UnmarshalJSON(data)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if !fi.Same(&fi2) {
		t.Error("Unmarshaled FileInfo should be the same as original")
	}
}