package mirror

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path"
	"strings"
)

const (
	defaultMaxConns = 10
)

// TLSConfig defines TLS/HTTPS security configuration
type TLSConfig struct {
	// MinVersion specifies the minimum TLS version to use (1.2, 1.3)
	MinVersion string `toml:"min_version"`

	// MaxVersion specifies the maximum TLS version to use (1.2, 1.3)
	MaxVersion string `toml:"max_version"`

	// InsecureSkipVerify controls whether to skip certificate verification
	// WARNING: Only use for testing - this is a security risk
	InsecureSkipVerify bool `toml:"insecure_skip_verify"`

	// CACertFile path to custom CA certificate file for verification
	CACertFile string `toml:"ca_cert_file"`

	// ClientCertFile path to client certificate file (for mutual TLS)
	ClientCertFile string `toml:"client_cert_file"`

	// ClientKeyFile path to client private key file (for mutual TLS)
	ClientKeyFile string `toml:"client_key_file"`

	// CipherSuites specifies allowed cipher suites (empty = Go defaults)
	CipherSuites []string `toml:"cipher_suites"`

	// ServerName for SNI (Server Name Indication) - overrides hostname
	ServerName string `toml:"server_name"`
}

// BuildTLSConfig creates a *tls.Config from the TLSConfig settings
func (t *TLSConfig) BuildTLSConfig() (*tls.Config, error) {
	config := &tls.Config{
		InsecureSkipVerify: t.InsecureSkipVerify,
		ServerName:         t.ServerName,
	}

	// Set TLS version constraints
	if t.MinVersion != "" {
		switch t.MinVersion {
		case "1.2":
			config.MinVersion = tls.VersionTLS12
		case "1.3":
			config.MinVersion = tls.VersionTLS13
		default:
			return nil, errors.New("invalid min_version: must be 1.2 or 1.3")
		}
	} else {
		// Default to TLS 1.2 minimum for security
		config.MinVersion = tls.VersionTLS12
	}

	if t.MaxVersion != "" {
		switch t.MaxVersion {
		case "1.2":
			config.MaxVersion = tls.VersionTLS12
		case "1.3":
			config.MaxVersion = tls.VersionTLS13
		default:
			return nil, errors.New("invalid max_version: must be 1.2 or 1.3")
		}
	}

	// Load custom CA certificates if specified
	if t.CACertFile != "" {
		caCert, err := os.ReadFile(t.CACertFile)
		if err != nil {
			return nil, errors.New("failed to read CA certificate file: " + err.Error())
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, errors.New("failed to parse CA certificate")
		}
		config.RootCAs = caCertPool
	}

	// Load client certificate and key for mutual TLS if specified
	if t.ClientCertFile != "" && t.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(t.ClientCertFile, t.ClientKeyFile)
		if err != nil {
			return nil, errors.New("failed to load client certificate: " + err.Error())
		}
		config.Certificates = []tls.Certificate{cert}
	} else if t.ClientCertFile != "" || t.ClientKeyFile != "" {
		return nil, errors.New("both client_cert_file and client_key_file must be specified for mutual TLS")
	}

	// Configure cipher suites if specified
	if len(t.CipherSuites) > 0 {
		var cipherSuites []uint16
		for _, suite := range t.CipherSuites {
			switch suite {
			case "TLS_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_AES_128_GCM_SHA256)
			case "TLS_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_AES_256_GCM_SHA384)
			case "TLS_CHACHA20_POLY1305_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_CHACHA20_POLY1305_SHA256)
			case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384)
			case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384)
			default:
				return nil, errors.New("unsupported cipher suite: " + suite)
			}
		}
		config.CipherSuites = cipherSuites
	}

	return config, nil
}

// Validate checks the TLS configuration for consistency and security
func (t *TLSConfig) Validate() error {
	// Warn about insecure settings
	if t.InsecureSkipVerify {
		slog.Warn("TLS certificate verification is DISABLED - this is insecure and should only be used for testing")
	}

	// Validate file paths exist if specified
	if t.CACertFile != "" {
		if _, err := os.Stat(t.CACertFile); err != nil {
			return errors.New("CA certificate file not found: " + t.CACertFile)
		}
	}

	if t.ClientCertFile != "" {
		if _, err := os.Stat(t.ClientCertFile); err != nil {
			return errors.New("client certificate file not found: " + t.ClientCertFile)
		}
	}

	if t.ClientKeyFile != "" {
		if _, err := os.Stat(t.ClientKeyFile); err != nil {
			return errors.New("client key file not found: " + t.ClientKeyFile)
		}
	}

	// Validate version constraints
	if t.MinVersion != "" && t.MaxVersion != "" {
		minVer := parseVersion(t.MinVersion)
		maxVer := parseVersion(t.MaxVersion)
		if minVer > maxVer {
			return errors.New("min_version cannot be greater than max_version")
		}
	}

	return nil
}

// parseVersion converts string version to numeric for comparison
func parseVersion(version string) int {
	switch version {
	case "1.2":
		return 12
	case "1.3":
		return 13
	default:
		return 0
	}
}

type tomlURL struct {
	*url.URL
}

func (u *tomlURL) UnmarshalText(text []byte) error {
	parsedURL, err := url.Parse(string(text))
	if err != nil {
		return err
	}
	switch parsedURL.Scheme {
	case "http":
	case "https":
	default:
		return errors.New("unsupported scheme: " + parsedURL.Scheme)
	}

	// for URL.ResolveReference
	if !strings.HasSuffix(parsedURL.Path, "/") {
		parsedURL.Path += "/"
		parsedURL.RawPath += "/"
	}

	u.URL = parsedURL
	return nil
}

// MirrorConfig is an auxiliary struct for Config.
type MirrorConfig struct {
	URL           tomlURL  `toml:"url"`
	Suites        []string `toml:"suites"`
	Sections      []string `toml:"sections"`
	Source        bool     `toml:"mirror_source"`
	Architectures []string `toml:"architectures"`

	PGPKeyPath string `toml:"pgp_key_path,omitempty"`
	NoPGPCheck bool   `toml:"no_pgp_check,omitempty"`

	// Package filtering configuration
	Filters *PackageFilters `toml:"filters,omitempty"`
}

// PackageFilters defines filtering rules for packages
type PackageFilters struct {
	KeepVersions    int      `toml:"keep_versions,omitempty"`
	ExcludePatterns []string `toml:"exclude_patterns,omitempty"`
}

// isFlat returns true if suite ends with "/" as described in
// https://wiki.debian.org/RepositoryFormat#Flat_Repository_Format
func isFlat(suite string) bool {
	return strings.HasSuffix(suite, "/")
}

// Check vaildates the configuration.
func (mc *MirrorConfig) Check() error {
	if mc.URL.URL == nil {
		return errors.New("url is not set")
	}
	if len(mc.Suites) == 0 {
		return errors.New("no suites")
	}

	flat := isFlat(mc.Suites[0])
	if flat {
		if len(mc.Sections) != 0 {
			return errors.New("flat repository cannot have sections")
		}
		if len(mc.Architectures) != 0 {
			return errors.New("flat repository cannot have architectures")
		}
	} else {
		if len(mc.Sections) == 0 {
			return errors.New("no sections")
		}
		if len(mc.Architectures) == 0 {
			return errors.New("no architectures")
		}
	}

	for _, suite := range mc.Suites[1:] {
		if flat != isFlat(suite) {
			return errors.New("mixed flat/non-flat in suites")
		}
	}

	// PGP configuration validation
	if !mc.NoPGPCheck && mc.PGPKeyPath != "" {
		if !path.IsAbs(mc.PGPKeyPath) {
			return errors.New("pgp_key_path must be an absolute path")
		}
		if _, err := os.Stat(mc.PGPKeyPath); os.IsNotExist(err) {
			return errors.New("pgp_key_path does not exist: " + mc.PGPKeyPath)
		} else if err != nil {
			return errors.New("cannot access pgp_key_path: " + err.Error())
		}

		// Check if file is readable
		file, err := os.Open(mc.PGPKeyPath)
		if err != nil {
			return errors.New("cannot read pgp_key_path: " + err.Error())
		}
		if err := file.Close(); err != nil {
			slog.Warn("failed to close PGP key file during validation", "path", mc.PGPKeyPath, "error", err)
		}
	}

	return nil
}

// ReleaseFiles generates a list relative paths to "Release",
// "Release.gpg", or "InRelease" files.
func (mc *MirrorConfig) ReleaseFiles(suite string) []string {
	var fileList []string

	relpath := suite
	if !isFlat(suite) {
		relpath = path.Join("dists", suite)
	}
	// <suite "/"> == <empty relative path ""> + <flat repository indicator "/">
	//             != <absolute path "/">
	if suite == "/" {
		relpath = ""
	}
	fileList = append(fileList, path.Clean(path.Join(relpath, "Release")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "Release.gpg")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "Release.gz")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "Release.bz2")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "InRelease")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "InRelease.gz")))
	fileList = append(fileList, path.Clean(path.Join(relpath, "InRelease.bz2")))

	return fileList
}

// Resolve returns *url.URL for a relative path.
func (mc *MirrorConfig) Resolve(path string) *url.URL {
	return mc.URL.ResolveReference(&url.URL{Path: path})
}

func rawName(filePath string) string {
	base := path.Base(filePath)
	ext := path.Ext(base)
	return base[0 : len(base)-len(ext)]
}

// MatchingIndex returns true if mc is configured for the given index.
func (mc *MirrorConfig) MatchingIndex(filePath string) bool {
	rawName := rawName(filePath)

	if rawName == "Index" {
		return true
	}

	// Only allow main Release files at suite level, not section/arch-specific ones
	if rawName == "Release" {
		// Check if this is a main release file (dists/suite/Release)
		// vs section-specific release file (section/binary-arch/Release)
		dir := path.Dir(filePath)
		return !strings.Contains(dir, "binary-") && !strings.Contains(dir, "source")
	}

	if isFlat(mc.Suites[0]) {
		// scan Packages and Sources
		switch rawName {
		case "Packages":
			return true
		case "Sources":
			return mc.Source
		}
		return false
	}

	pathNoExt := filePath[0 : len(filePath)-len(path.Ext(filePath))]
	var architectures []string
	architectures = append(architectures, "all")
	architectures = append(architectures, mc.Architectures...)
	for _, section := range mc.Sections {
		for _, arch := range architectures {
			t := path.Join(path.Clean(section), "binary-"+arch, "Packages")
			if strings.HasSuffix(pathNoExt, t) {
				return true
			}
		}
		if mc.Source {
			t := path.Join(path.Clean(section), "source", "Sources")
			if strings.HasSuffix(pathNoExt, t) {
				return true
			}
		}
	}

	return false
}

// LogConfig represents slog configuration options
type LogConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// Apply configures the global slog logger based on the configuration
func (lc *LogConfig) Apply() error {
	var level slog.Level
	switch strings.ToLower(lc.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return errors.New("invalid log level: " + lc.Level)
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(lc.Format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "plain", "", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		return errors.New("invalid log format: " + lc.Format)
	}

	slog.SetDefault(slog.New(handler))
	return nil
}

// Config is a struct to read TOML configurations.
//
// Use https://github.com/BurntSushi/toml as follows:
//
//	config := mirror.NewConfig()
//	md, err := toml.DecodeFile("/path/to/config.toml", config)
//	if err != nil {
//	    ...
//	}
type Config struct {
	Dir      string                   `toml:"dir"`
	MaxConns int                      `toml:"max_conns"`
	Log      LogConfig                `toml:"log"`
	TLS      TLSConfig                `toml:"tls"`
	Mirrors  map[string]*MirrorConfig `toml:"mirrors"`
}

// Check validates the configuration.
func (c *Config) Check() error {
	if c.Dir == "" {
		return errors.New("dir is not set")
	}

	// Validate TLS configuration
	if err := c.TLS.Validate(); err != nil {
		return errors.New("TLS configuration error: " + err.Error())
	}
	if !path.IsAbs(c.Dir) {
		return errors.New("dir must be an absolute path")
	}
	return nil
}

// NewConfig creates Config with default values.
func NewConfig() *Config {
	return &Config{
		MaxConns: defaultMaxConns,
	}
}
