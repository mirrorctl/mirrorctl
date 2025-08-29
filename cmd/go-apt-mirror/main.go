// Package main implements the go-apt-mirror command-line tool for mirroring APT repositories.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
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

By default, the application looks for a configuration file at /etc/apt/mirror.toml.

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

	rootCmd.Flags().BoolP("version", "v", false, "print version information and exit")

	rootCmd.PersistentFlags().BoolP("help", "h", false, "help for go-apt-mirror")
}

func runMirror(cmd *cobra.Command, args []string) {
	if versionFlag, _ := cmd.Flags().GetBool("version"); versionFlag {
		fmt.Printf("go-apt-mirror %s\n", version)
		fmt.Printf("commit: %s\n", commit)
		fmt.Printf("built: %s\n", buildDate)
		return
	}

	config := mirror.NewConfig()
	metadata, err := toml.DecodeFile(configPath, config)
	if err != nil {
		slog.Error("failed to decode config file", "error", err, "path", configPath)
		os.Exit(1)
	}
	if len(metadata.Undecoded()) > 0 {
		slog.Error("invalid config keys", "keys", fmt.Sprintf("%#v", metadata.Undecoded()))
		os.Exit(1)
	}

	err = config.Log.Apply()
	if err != nil {
		slog.Error("failed to apply log config", "error", err)
		os.Exit(1)
	}

	err = mirror.Run(config, args)
	if err != nil {
		slog.Error("mirror run failed", "error", err)
		os.Exit(1)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
