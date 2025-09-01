package mirror

import (
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

// MirrConfig is an auxiliary struct for Config.
type MirrConfig struct {
	URL           tomlURL  `toml:"url"`
	Suites        []string `toml:"suites"`
	Sections      []string `toml:"sections"`
	Source        bool     `toml:"mirror_source"`
	Architectures []string `toml:"architectures"`

	PGPKeyPath string `toml:"pgp_key_path,omitempty"`
	NoPGPCheck bool   `toml:"no_pgp_check,omitempty"`
}

// isFlat returns true if suite ends with "/" as described in
// https://wiki.debian.org/RepositoryFormat#Flat_Repository_Format
func isFlat(suite string) bool {
	return strings.HasSuffix(suite, "/")
}

// Check vaildates the configuration.
func (mirrorConfig *MirrConfig) Check() error {
	if len(mirrorConfig.Suites) == 0 {
		return errors.New("no suites")
	}

	flat := isFlat(mirrorConfig.Suites[0])
	if flat && len(mirrorConfig.Sections) != 0 {
		return errors.New("flat repository cannot have sections")
	}
	if flat && len(mirrorConfig.Architectures) != 0 {
		return errors.New("flat repository cannot have sections")
	}
	for _, suite := range mirrorConfig.Suites[1:] {
		if flat != isFlat(suite) {
			return errors.New("mixed flat/non-flat in suites")
		}
	}

	// PGP configuration validation
	if !mirrorConfig.NoPGPCheck && mirrorConfig.PGPKeyPath != "" {
		if _, err := os.Stat(mirrorConfig.PGPKeyPath); os.IsNotExist(err) {
			return errors.New("pgp_key_path does not exist: " + mirrorConfig.PGPKeyPath)
		} else if err != nil {
			return errors.New("cannot access pgp_key_path: " + err.Error())
		}

		// Check if file is readable
		file, err := os.Open(mirrorConfig.PGPKeyPath)
		if err != nil {
			return errors.New("cannot read pgp_key_path: " + err.Error())
		}
		if err := file.Close(); err != nil {
			slog.Warn("failed to close PGP key file during validation", "path", mirrorConfig.PGPKeyPath, "error", err)
		}
	}

	return nil
}

// ReleaseFiles generates a list relative paths to "Release",
// "Release.gpg", or "InRelease" files.
func (mirrorConfig *MirrConfig) ReleaseFiles(suite string) []string {
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
func (mirrorConfig *MirrConfig) Resolve(path string) *url.URL {
	return mirrorConfig.URL.ResolveReference(&url.URL{Path: path})
}

func rawName(filePath string) string {
	base := path.Base(filePath)
	ext := path.Ext(base)
	return base[0 : len(base)-len(ext)]
}

// MatchingIndex returns true if mc is configured for the given index.
func (mirrorConfig *MirrConfig) MatchingIndex(filePath string) bool {
	rawName := rawName(filePath)

	if rawName == "Index" || rawName == "Release" {
		return true
	}

	if isFlat(mirrorConfig.Suites[0]) {
		// scan Packages and Sources
		switch rawName {
		case "Packages":
			return true
		case "Sources":
			return mirrorConfig.Source
		}
		return false
	}

	pathNoExt := filePath[0 : len(filePath)-len(path.Ext(filePath))]
	var architectures []string
	architectures = append(architectures, "all")
	architectures = append(architectures, mirrorConfig.Architectures...)
	for _, section := range mirrorConfig.Sections {
		for _, arch := range architectures {
			t := path.Join(path.Clean(section), "binary-"+arch, "Packages")
			if strings.HasSuffix(pathNoExt, t) {
				return true
			}
		}
		if mirrorConfig.Source {
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
func (logConfig *LogConfig) Apply() error {
	var level slog.Level
	switch strings.ToLower(logConfig.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return errors.New("invalid log level: " + logConfig.Level)
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(logConfig.Format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "plain", "", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		return errors.New("invalid log format: " + logConfig.Format)
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
	Dir      string                 `toml:"dir"`
	MaxConns int                    `toml:"max_conns"`
	Log      LogConfig              `toml:"log"`
	Mirrors  map[string]*MirrConfig `toml:"mirror"`
}

// NewConfig creates Config with default values.
func NewConfig() *Config {
	return &Config{
		MaxConns: defaultMaxConns,
	}
}
