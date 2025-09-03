// Package main implements the go-apt-mirror command-line tool for mirroring APT repositories.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/cockroachdb/errors"
	"github.com/cybozu-go/aptutil/internal/mirror"
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
	Use:   "go-apt-mirror",
	Short: "Mirror Debian package repositories",
	Long: `go-apt-mirror is a tool for creating and maintaining mirrors of Debian package repositories.

Find more information at: https://tbd.websitename.xyz`,
}

var syncCmd = &cobra.Command{
	Use:   "sync [mirror-ids...]",
	Short: "Synchronize one or more APT repositories",
	Long: `Synchronizes one or more APT repositories based on the provided configuration.

Usage:
  # Synchronize all repositories in your configuration file
  go-apt-mirror sync

  # Synchronize only specific repositories
  go-apt-mirror sync ubuntu security

  # Use a custom configuration file
  go-apt-mirror sync --config /path/to/custom-location.toml

  # Override the log level
  go-apt-mirror sync --log-level debug

  # Show detailed error information
  go-apt-mirror sync --verbose-errors

If no mirror IDs are specified, all repositories in the configuration file will be
synchronized.`,
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
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "configuration file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "override log level (debug, info, warn, error)")

	rootCmd.Flags().BoolP("version", "v", false, "print version information and exit")

	rootCmd.PersistentFlags().BoolP("help", "h", false, "help for go-apt-mirror")
	rootCmd.PersistentFlags().Bool("no-pgp-check", false, "disable PGP signature verification")
	rootCmd.PersistentFlags().Bool("verbose-errors", false, "show detailed error information including stack traces")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress all output except for errors")
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
	if _, err := toml.DecodeFile(configPath, config); err != nil {
		if os.IsNotExist(err) {
			slog.Error("configuration file not found", "path", configPath)
			slog.Info("Please create a configuration file at the default location or specify one with the --config flag.")
			os.Exit(1)
		}
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to decode config file", "error", errorMsg, "path", configPath)
		if !verboseErrors {
			slog.Info("run with --verbose-errors for detailed stack traces")
		}
		os.Exit(1)
	}

	// Apply log configuration immediately after config loading
	if err := config.Log.Apply(); err != nil {
		slog.Error("failed to apply log config", "error", err)
		os.Exit(1)
	}

	// Override log level if specified on command line
	if logLevel != "" {
		config.Log.Level = logLevel
		if err := config.Log.Apply(); err != nil {
			slog.Error("failed to apply command-line log level", "level", logLevel, "error", err)
			os.Exit(1)
		}
		slog.Debug("log level successfully overridden from command line", "level", logLevel)
	}

	quiet, _ := cmd.Flags().GetBool("quiet")
	if quiet {
		config.Log.Level = "error"
		if err := config.Log.Apply(); err != nil {
			slog.Error("failed to apply quiet log level", "error", err)
			os.Exit(1)
		}
	}

	noPGPCheck, _ := cmd.Flags().GetBool("no-pgp-check")

	if err := mirror.Run(config, args, noPGPCheck, quiet); err != nil {
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
