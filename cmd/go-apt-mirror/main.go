// Package main implements the go-apt-mirror command-line tool for mirroring APT repositories.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/cockroachdb/errors"
	"github.com/gomirror/go-apt-mirror/internal/mirror"
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

  # Suppress all output except for errors
  go-apt-mirror sync --quiet

  # Dry run - calculate disk usage without downloading
  go-apt-mirror sync --dry-run

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

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the configuration file",
	Long:  `Validate the configuration file and report any issues.`,
	Run:   runValidate,
}

func init() {
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(validateCmd)

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "configuration file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "override log level (debug, info, warn, error)")

	rootCmd.Flags().BoolP("version", "v", false, "print version information and exit")

	rootCmd.PersistentFlags().BoolP("help", "h", false, "help for go-apt-mirror")
	rootCmd.PersistentFlags().Bool("no-pgp-check", false, "disable PGP signature verification")
	rootCmd.PersistentFlags().Bool("verbose-errors", false, "show detailed error information including stack traces")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress all output except for errors")
	rootCmd.PersistentFlags().Bool("dry-run", false, "calculate disk usage without downloading files")
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

// analyzeUndecoded examines undecoded TOML keys and provides helpful suggestions
func analyzeUndecoded(undecoded []toml.Key) (suggestions []string, unknown []string) {
	// Group keys by their root section for mirror typos
	mirrorGroups := make(map[string]int)

	for _, key := range undecoded {
		keyStr := key.String()

		// Check for common "mirror" vs "mirrors" typo
		if strings.HasPrefix(keyStr, "mirror.") && !strings.HasPrefix(keyStr, "mirrors.") {
			// Extract the root section (e.g., "mirror.amlfs-noble" from "mirror.amlfs-noble.url")
			parts := strings.Split(keyStr, ".")
			if len(parts) >= 2 {
				rootSection := parts[0] + "." + parts[1] // "mirror.amlfs-noble"
				mirrorGroups[rootSection]++
			}
		} else {
			// Keep track of keys we couldn't provide suggestions for
			unknown = append(unknown, keyStr)
		}
	}

	// Generate grouped suggestions
	for rootSection, count := range mirrorGroups {
		correctedSection := strings.Replace(rootSection, "mirror.", "mirrors.", 1)
		if count == 1 {
			suggestions = append(suggestions, fmt.Sprintf("Section '%s' should be '%s'", rootSection, correctedSection))
		} else {
			suggestions = append(suggestions, fmt.Sprintf("Section '%s' should be '%s' (affects %d subsections)", rootSection, correctedSection, count))
		}
	}

	return suggestions, unknown
}

// formatUndecodedError builds a user-friendly error message for undecoded TOML keys
func formatUndecodedError(undecoded []toml.Key) string {
	suggestions, unknown := analyzeUndecoded(undecoded)

	var errorMsg strings.Builder
	if len(suggestions) > 0 {
		errorMsg.WriteString("configuration contains sections that don't match expected structure:\n")
		for _, suggestion := range suggestions {
			errorMsg.WriteString("  • " + suggestion + "\n")
		}
		errorMsg.WriteString("\nNote: Configuration section names are case-sensitive and must match exactly.")
	}

	if len(unknown) > 0 {
		if errorMsg.Len() > 0 {
			errorMsg.WriteString("\n\nAdditionally, found unknown sections: ")
		} else {
			errorMsg.WriteString("configuration contains unknown sections: ")
		}
		errorMsg.WriteString(fmt.Sprintf("%v", unknown))
		errorMsg.WriteString("\nThese sections don't match any expected configuration structure.")
	}

	return errorMsg.String()
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
	meta, err := toml.DecodeFile(configPath, config)
	if err != nil {
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

	// Check for undecoded keys which might indicate parsing stopped early
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		errorMsg := formatUndecodedError(undecoded)
		slog.Error("configuration validation failed", "error", errorMsg, "path", configPath)
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if err := mirror.Run(config, args, noPGPCheck, quiet, dryRun); err != nil {
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

func runValidate(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")

	config := mirror.NewConfig()
	meta, err := toml.DecodeFile(configPath, config)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("configuration file not found", "path", configPath)
			os.Exit(1)
		}
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to decode config file", "error", errorMsg, "path", configPath)
		os.Exit(1)
	}

	// Check for undecoded keys which might indicate parsing stopped early
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		errorMsg := formatUndecodedError(undecoded)
		slog.Error("configuration validation failed", "error", errorMsg, "path", configPath)
		os.Exit(1)
	}

	var validationErrors []error

	if err := config.Log.Apply(); err != nil {
		validationErrors = append(validationErrors, errors.Wrap(err, "log config"))
	}

	if err := config.Check(); err != nil {
		validationErrors = append(validationErrors, errors.Wrap(err, "global config"))
	}

	for mirrorID, mirrorConfig := range config.Mirrors {
		if !mirror.IsValidID(mirrorID) {
			validationErrors = append(validationErrors, errors.New("invalid mirror ID: "+mirrorID))
		}
		if err := mirrorConfig.Check(); err != nil {
			validationErrors = append(validationErrors, errors.Wrap(err, "mirror \""+mirrorID+"\""))
		}
	}

	if len(validationErrors) > 0 {
		slog.Error("the toml configuration file is not valid")
		for _, err := range validationErrors {
			slog.Error(err.Error())
		}
		os.Exit(1)
	}

	slog.Info("the toml configuration file passes validation checks")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
