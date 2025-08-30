// Package main implements the go-apt-mirror command-line tool for mirroring APT repositories.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/mirror"
	"github.com/spf13/cobra"
)

const (
	defaultConfigPath = "/etc/apt/mirror.toml"
)

var (
	// Build information - can be set via build flags or by the build.sh script
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"

	// Command-line flags
	configPath string
	logLevel   string
)

var rootCmd = &cobra.Command{
	Use:   "go-apt-mirror [mirror-ids...]",
	Short: "Mirror Debian package repositories",
	Long: `go-apt-mirror is a tool for creating and maintaining mirrors of Debian package repositories.

go-apt-mirror securely mirrors Debian package repositories.

Find more information at: https://tbd.websitename.xyz

Usage:
  # Syncronize all repositories in your configuration file
  go-apt-mirror

  # Mirror only specific repositories
  go-apt-mirror ubuntu security

  # Use custom configuration file
  go-apt-mirror --config /path/to/custom-location.toml

  # Override log level (debug, info, warn, error)
  go-apt-mirror --log-level debug

  # Show detailed error information
  go-apt-mirror --verbose-errors

By default, the application looks for a configuration file at /etc/apt/mirror.toml.
The log level can be overridden from the command line, taking precedence over the configuration file setting.

See the website for further examples and documentation.`,
	Run: runMirror,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  "Print version information including build details",
	Run: func(_ *cobra.Command, args []string) {
		fmt.Printf("go-apt-mirror %s\n", version)
		fmt.Printf("commit: %s\n", commit)
		fmt.Printf("built: %s\n", buildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "configuration file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "override log level (debug, info, warn, error)")

	rootCmd.Flags().BoolP("version", "v", false, "print version information and exit")

	rootCmd.PersistentFlags().BoolP("help", "h", false, "help for go-apt-mirror")
	rootCmd.PersistentFlags().Bool("no-pgp-check", false, "disable PGP signature verification")
	rootCmd.PersistentFlags().Bool("verbose-errors", false, "show detailed error information including stack traces")
}

// formatError returns a human-friendly error message, optionally with stack trace
func formatError(err error, verbose bool) string {
	if verbose {
		return fmt.Sprintf("%+v", err) // Full details with stack trace
	}

	// For human-friendly output, try to extract the root message
	flattened := errors.FlattenDetails(err)
	if flattened != "" {
		return flattened
	}

	// Fallback to simple error message
	return err.Error()
}

func runMirror(cmd *cobra.Command, args []string) {
	if versionFlag, _ := cmd.Flags().GetBool("version"); versionFlag {
		fmt.Printf("go-apt-mirror %s\n", version)
		fmt.Printf("commit: %s\n", commit)
		fmt.Printf("built: %s\n", buildDate)
		return
	}

	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")

	config := mirror.NewConfig()
	metadata, err := toml.DecodeFile(configPath, config)
	if err != nil {
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to decode config file", "error", errorMsg, "path", configPath)
		if !verboseErrors {
			slog.Info("run with --verbose-errors for detailed stack traces")
		}
		os.Exit(1)
	}
	if len(metadata.Undecoded()) > 0 {
		slog.Error("invalid config keys", "keys", fmt.Sprintf("%#v", metadata.Undecoded()))
		os.Exit(1)
	}

	// Apply log configuration immediately after config loading
	err = config.Log.Apply()
	if err != nil {
		slog.Error("failed to apply log config", "error", err)
		os.Exit(1)
	}

	// Override log level if specified on command line
	if logLevel != "" {
		config.Log.Level = logLevel
		err = config.Log.Apply()
		if err != nil {
			slog.Error("failed to apply command-line log level", "level", logLevel, "error", err)
			os.Exit(1)
		}
		slog.Debug("log level successfully overridden from command line", "level", logLevel)
	}

	noPGPCheck, _ := cmd.Flags().GetBool("no-pgp-check")

	err = mirror.Run(config, args, noPGPCheck)
	if err != nil {
		errorMsg := formatError(err, verboseErrors)
		if verboseErrors {
			slog.Error("mirror run failed", "error", errorMsg)
		} else {
			slog.Error("mirror run failed", "error", errorMsg)
			slog.Info("run with --verbose-errors for detailed stack traces")
		}
		os.Exit(1)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
