package mirror

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestConfig(t *testing.T) {
	t.Parallel()

	c := NewConfig()
	configPath := filepath.Join("..", "..", "examples", "configs", "mirror-secure.toml")
	md, err := toml.DecodeFile(configPath, c)
	if err != nil {
		t.Fatal(err)
	}

	if len(md.Undecoded()) > 0 {
		t.Errorf("undecoded keys: %#v", md.Undecoded())
	}

	if c.Dir != "examples/mirror-data/" {
		t.Errorf(`c.Dir = %q, want "examples/mirror-data/"`, c.Dir)
	}
	if c.MaxConns != 10 {
		t.Errorf(`c.MaxConns = %d, want 10`, c.MaxConns)
	}

	if c.Log.Level != "info" {
		t.Errorf(`c.Log.Level = %q, want "info"`, c.Log.Level)
	}

	expectedMirrors := 4 // amlfs-noble, openenclave, rear, slurm-ubuntu-noble
	if len(c.Mirrors) != expectedMirrors {
		t.Fatalf(`len(c.Mirrors) = %d, want %d`, len(c.Mirrors), expectedMirrors)
	}

	// Test amlfs-noble mirror
	if amlfs, ok := c.Mirrors["amlfs-noble"]; !ok {
		t.Error(`amlfs-noble mirror not found`)
	} else {
		if amlfs.URL.String() != "https://packages.microsoft.com/repos/amlfs-noble/" {
			t.Errorf(`amlfs-noble.URL = %q, want "https://packages.microsoft.com/repos/amlfs-noble/"`, amlfs.URL.String())
		}
		if !amlfs.PublishToStaging {
			t.Error(`amlfs-noble.PublishToStaging should be true`)
		}
		if !reflect.DeepEqual(amlfs.Architectures, []string{"amd64"}) {
			t.Errorf(`amlfs-noble.Architectures = %v, want ["amd64"]`, amlfs.Architectures)
		}
		if !reflect.DeepEqual(amlfs.Suites, []string{"noble"}) {
			t.Errorf(`amlfs-noble.Suites = %v, want ["noble"]`, amlfs.Suites)
		}
		if !reflect.DeepEqual(amlfs.Sections, []string{"main"}) {
			t.Errorf(`amlfs-noble.Sections = %v, want ["main"]`, amlfs.Sections)
		}
	}

	// Test slurm-ubuntu-noble mirror
	if slurm, ok := c.Mirrors["slurm-ubuntu-noble"]; !ok {
		t.Error(`slurm-ubuntu-noble mirror not found`)
	} else {
		if slurm.URL.String() != "https://packages.microsoft.com/repos/slurm-ubuntu-noble/" {
			t.Errorf(`slurm-ubuntu-noble.URL = %q, want "https://packages.microsoft.com/repos/slurm-ubuntu-noble/"`, slurm.URL.String())
		}
		if slurm.PublishToStaging {
			t.Error(`slurm-ubuntu-noble.PublishToStaging should be false`)
		}
	}
}

func TestMirrorConfig(t *testing.T) {
	t.Parallel()

	var c Config
	configPath := filepath.Join("..", "..", "examples", "configs", "mirror-secure.toml")
	_, err := toml.DecodeFile(configPath, &c)
	if err != nil {
		t.Fatal(err)
	}

	// Test amlfs-noble mirror configuration
	mc, ok := c.Mirrors["amlfs-noble"]
	if !ok {
		t.Fatal(`c.Mirrors["amlfs-noble"] not found`)
	}

	correct := "https://packages.microsoft.com/repos/amlfs-noble/dists/noble/Release"
	if mc.Resolve("dists/noble/Release").String() != correct {
		t.Errorf(`mc.Resolve("dists/noble/Release") = %q, want %q`, mc.Resolve("dists/noble/Release").String(), correct)
	}

	if err := mc.Check(); err != nil {
		t.Error(err)
	}

	// Test release files for noble suite
	m := make(map[string]struct{})
	for _, p := range mc.ReleaseFiles("noble") {
		m[p] = struct{}{}
	}
	if _, ok := m["dists/noble/Release"]; !ok {
		t.Error(`dists/noble/Release should be in release files`)
	}

	// Test index matching
	if !mc.MatchingIndex("dists/noble/main/binary-amd64/Packages.gz") {
		t.Error(`should match noble main binary-amd64 Packages.gz`)
	}
	if !mc.MatchingIndex("dists/noble/Release") {
		t.Error(`should match noble Release file`)
	}
	if mc.MatchingIndex("some-random-file.txt") {
		t.Error(`should not match random file`)
	}

	// Test slurm-ubuntu-noble mirror
	mc, ok = c.Mirrors["slurm-ubuntu-noble"]
	if !ok {
		t.Fatal(`c.Mirrors["slurm-ubuntu-noble"] not found`)
	}
	if err := mc.Check(); err != nil {
		t.Error(err)
	}
	if !mc.MatchingIndex("dists/stable/main/binary-amd64/Packages") {
		t.Error(`should match stable main binary-amd64 Packages`)
	}

	// Test openenclave mirror
	mc, ok = c.Mirrors["openenclave"]
	if !ok {
		t.Fatal(`c.Mirrors["openenclave"] not found`)
	}
	if err := mc.Check(); err != nil {
		t.Error(err)
	}

	// Test that bionic suite works
	if !mc.MatchingIndex("dists/bionic/main/binary-amd64/Packages") {
		t.Error(`should match bionic main binary-amd64 Packages`)
	}
}

func TestConfig_Check(t *testing.T) {
	t.Parallel()

	// Valid config
	c1 := NewConfig()
	c1.Dir = "/tmp"
	if err := c1.Check(); err != nil {
		t.Errorf("expected no error, but got: %v", err)
	}

	// Missing Dir
	c2 := NewConfig()
	if err := c2.Check(); err == nil {
		t.Error("expected an error for missing dir, but got none")
	}

	// Relative Dir
	c3 := NewConfig()
	c3.Dir = "tmp"
	if err := c3.Check(); err == nil {
		t.Error("expected an error for relative dir, but got none")
	}

	// Zero MaxConns
	c4 := NewConfig()
	c4.Dir = "/tmp"
	c4.MaxConns = 0
	if err := c4.Check(); err == nil {
		t.Error("expected an error for zero max_conns, but got none")
	}

	// Negative MaxConns
	c5 := NewConfig()
	c5.Dir = "/tmp"
	c5.MaxConns = -1
	if err := c5.Check(); err == nil {
		t.Error("expected an error for negative max_conns, but got none")
	}

	// Invalid mirror IDs
	invalidMirrorIDs := []struct {
		id     string
		reason string
	}{
		{"../etc", "path traversal"},
		{"/etc/passwd", "absolute path"},
		{"MyMirror", "uppercase letters"},
		{"mirror.prod", "dots"},
		{"foo/bar", "forward slash"},
		{"test\\path", "backslash"},
		{"mirror name", "space"},
		{"", "empty string"},
	}

	for _, tc := range invalidMirrorIDs {
		c := NewConfig()
		c.Dir = "/tmp"
		c.Mirrors = map[string]*MirrorConfig{
			tc.id: {},
		}

		if err := c.Check(); err == nil {
			t.Errorf("expected error for mirror ID %q (%s), but got none", tc.id, tc.reason)
		}
	}

	// Valid mirror ID
	c6 := NewConfig()
	c6.Dir = "/tmp"
	c6.Mirrors = map[string]*MirrorConfig{
		"valid-mirror_123": {},
	}
	if err := c6.Check(); err != nil {
		t.Errorf("expected no error for valid mirror ID, but got: %v", err)
	}
}
