// Package main implements the mirrorctl command-line tool for mirroring APT repositories.
package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/mirrorctl/mirrorctl/internal/mirror"
)

const (
	defaultConfigPath = "/etc/mirrorctl/mirror.toml"
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
	Use:   "mirrorctl",
	Short: "Mirror Debian package repositories",
	Long: `mirrorctl is a tool for creating and maintaining mirrors of Debian package repositories.

Find more information at: https://github.com/mirrorctl/mirrorctl`,
}

var syncCmd = &cobra.Command{
	Use:   "sync [mirror-ids...]",
	Short: "Synchronize one or more APT repositories",
	Long: `Synchronizes one or more APT repositories based on the provided configuration.

Usage:
  # Synchronize all repositories in your configuration file
  mirrorctl sync

  # Synchronize only specific repositories
  mirrorctl sync ubuntu security

  # Use a custom configuration file
  mirrorctl sync --config /path/to/custom-location.toml

  # Override the log level
  mirrorctl sync --log-level debug

  # Show detailed error information
  mirrorctl sync --verbose-errors

  # Suppress all output except for errors
  mirrorctl sync --quiet

  # Dry run - calculate disk usage without downloading
  mirrorctl sync --dry-run

If no mirror IDs are specified, all repositories in the configuration file will be
synchronized.`,
	Run: runMirror,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  "Print version information including build details",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("mirrorctl %s\n", version)
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

var tlsCheckCmd = &cobra.Command{
	Use:   "tls-check [mirror-id]",
	Short: "Check TLS configuration and capabilities for a mirror",
	Long: `Performs a detailed TLS handshake and certificate check against the remote server for a configured mirror.

This command helps diagnose TLS connection issues by testing supported TLS versions,
negotiated cipher suites, and examining the certificate chain.

Examples:
  mirrorctl tls-check amlfs-noble
  mirrorctl tls-check openenclave`,
	Args: cobra.ExactArgs(1),
	Run:  runTLSCheck,
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage repository snapshots",
	Long:  `Manage repository snapshots for staging and production deployments.`,
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create [mirror-id] [snapshot-name]",
	Short: "Create a new snapshot of a mirror",
	Long: `Create a new snapshot of a mirror.

Examples:
  mirrorctl snapshot create ubuntu-main
  mirrorctl snapshot create ubuntu-main "before-upgrade"
  mirrorctl snapshot create ubuntu-main --stage`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSnapshotCreate,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list [mirror-id...]",
	Short: "List snapshots for mirrors",
	Long: `List snapshots for one or more mirrors.

Examples:
  mirrorctl snapshot list
  mirrorctl snapshot list ubuntu-main
  mirrorctl snapshot list --detailed`,
	Run: runSnapshotList,
}

var snapshotPublishCmd = &cobra.Command{
	Use:   "publish <mirror-id> <snapshot-name>",
	Short: "Publish a snapshot to production",
	Long: `Publish a snapshot to production.

Examples:
  mirrorctl snapshot publish ubuntu-main "2024-01-15T10-30-00Z"`,
	Args: cobra.ExactArgs(2),
	Run:  runSnapshotPublish,
}

var snapshotStageCmd = &cobra.Command{
	Use:   "stage <mirror-id> <snapshot-name>",
	Short: "Publish a snapshot to staging environment",
	Long: `Publish a snapshot to staging environment.

Examples:
  mirrorctl snapshot stage ubuntu-main "2024-01-15T10-30-00Z"`,
	Args: cobra.ExactArgs(2),
	Run:  runSnapshotStage,
}

var snapshotPromoteCmd = &cobra.Command{
	Use:   "promote <mirror-id>",
	Short: "Promote the currently staged snapshot to production",
	Long: `Promote the currently staged snapshot to production.

Examples:
  mirrorctl snapshot promote ubuntu-main`,
	Args: cobra.ExactArgs(1),
	Run:  runSnapshotPromote,
}

var snapshotDeleteCmd = &cobra.Command{
	Use:   "delete <mirror-id> <snapshot-name...>",
	Short: "Delete one or more snapshots",
	Long: `Delete one or more snapshots.

Examples:
  mirrorctl snapshot delete ubuntu-main "old-snapshot"
  mirrorctl snapshot delete ubuntu-main "snap1" "snap2"
  mirrorctl snapshot delete ubuntu-main "current" --force`,
	Args: cobra.MinimumNArgs(2),
	Run:  runSnapshotDelete,
}

var snapshotPruneCmd = &cobra.Command{
	Use:   "prune [mirror-id...]",
	Short: "Remove old snapshots according to retention policy",
	Long: `Remove old snapshots according to retention policy.

Examples:
  mirrorctl snapshot prune
  mirrorctl snapshot prune ubuntu-main
  mirrorctl snapshot prune ubuntu-main --keep-last 10 --keep-within 60d
  mirrorctl snapshot prune ubuntu-main --dry-run`,
	Run: runSnapshotPrune,
}

func init() {
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(tlsCheckCmd)
	rootCmd.AddCommand(snapshotCmd)

	// Add snapshot subcommands
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)
	snapshotCmd.AddCommand(snapshotPublishCmd)
	snapshotCmd.AddCommand(snapshotStageCmd)
	snapshotCmd.AddCommand(snapshotPromoteCmd)
	snapshotCmd.AddCommand(snapshotDeleteCmd)
	snapshotCmd.AddCommand(snapshotPruneCmd)

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "configuration file path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "override log level (debug, info, warn, error)")

	rootCmd.Flags().BoolP("version", "v", false, "print version information and exit")

	rootCmd.PersistentFlags().BoolP("help", "h", false, "help for mirrorctl")
	rootCmd.PersistentFlags().Bool("no-pgp-check", false, "disable PGP signature verification")
	rootCmd.PersistentFlags().Bool("verbose-errors", false, "show detailed error information including stack traces")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress all output except for errors")
	rootCmd.PersistentFlags().Bool("dry-run", false, "calculate disk usage without downloading files")

	// Add the --force flag specifically to the sync command
	syncCmd.Flags().Bool("force", false, "overwrite snapshot if it already exists")

	// Add snapshot-specific flags
	snapshotCreateCmd.Flags().Bool("force", false, "overwrite existing snapshot with same name")
	snapshotCreateCmd.Flags().Bool("stage", false, "publish to staging after creation")
	snapshotListCmd.Flags().Bool("detailed", false, "show detailed snapshot information including size and status")
	snapshotDeleteCmd.Flags().Bool("force", false, "delete even if snapshot is currently published or staged")
	snapshotPruneCmd.Flags().Int("keep-last", 0, "number of recent snapshots to keep")
	snapshotPruneCmd.Flags().String("keep-within", "", "keep snapshots within duration (e.g., \"30d\", \"1w\")")
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
		fmt.Printf("mirrorctl %s\n", version)
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
	force, _ := cmd.Flags().GetBool("force")

	if err := mirror.Run(config, args, noPGPCheck, quiet, dryRun, force); err != nil {
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

func runValidate(cmd *cobra.Command, _ []string) {
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

func runTLSCheck(_ *cobra.Command, args []string) {
	mirrorID := args[0]

	// Load configuration file
	config := mirror.NewConfig()
	meta, err := toml.DecodeFile(configPath, config)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("configuration file not found", "path", configPath)
			os.Exit(1)
		}
		slog.Error("failed to decode config file", "error", err, "path", configPath)
		os.Exit(1)
	}

	// Check for undecoded keys
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		errorMsg := formatUndecodedError(undecoded)
		slog.Error("configuration validation failed", "error", errorMsg, "path", configPath)
		os.Exit(1)
	}

	// Find the target mirror
	mirrorConfig, ok := config.Mirrors[mirrorID]
	if !ok {
		fmt.Printf("Mirror '%s' not found in configuration.\n\n", mirrorID)
		fmt.Println("Available mirrors:")
		var mirrorIDs []string
		for id := range config.Mirrors {
			mirrorIDs = append(mirrorIDs, id)
		}
		sort.Strings(mirrorIDs)
		for _, id := range mirrorIDs {
			fmt.Printf("  - %s\n", id)
		}
		os.Exit(1)
	}

	// Extract host and port from URL
	host := mirrorConfig.URL.Hostname()
	port := mirrorConfig.URL.Port()
	if port == "" {
		if mirrorConfig.URL.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	fmt.Printf("Checking TLS status for mirror '%s' (%s:%s)...\n\n", mirrorID, host, port)

	// Perform TLS version checks
	checkTLSVersions(config, host, port)

	// Perform detailed certificate check
	checkCertificateDetails(config, host, port)

	fmt.Println("TLS check complete.")
}

func checkTLSVersions(config *mirror.Config, host, port string) {
	fmt.Println("[+] TLS Version Support:")

	tlsVersions := []struct {
		version uint16
		name    string
	}{
		{tls.VersionTLS10, "TLS 1.0"},
		{tls.VersionTLS11, "TLS 1.1"},
		{tls.VersionTLS12, "TLS 1.2"},
		{tls.VersionTLS13, "TLS 1.3"},
	}

	for _, tlsVer := range tlsVersions {
		// Build TLS config from user's global settings
		tlsConf, err := config.TLS.BuildTLSConfig()
		if err != nil {
			fmt.Printf("    %s: Error building TLS config (%v)\n", tlsVer.name, err)
			continue
		}

		// Override version settings to test specific version
		tlsConf.MinVersion = tlsVer.version
		tlsConf.MaxVersion = tlsVer.version

		// Attempt connection
		conn, err := tls.Dial("tcp", net.JoinHostPort(host, port), tlsConf)
		if err != nil {
			fmt.Printf("    %s: Not Supported (%v)\n", tlsVer.name, err)
		} else {
			fmt.Printf("    %s: Supported\n", tlsVer.name)
			conn.Close()
		}
	}
	fmt.Println()
}

func checkCertificateDetails(config *mirror.Config, host, port string) {
	fmt.Println("[+] Connection Details:")

	// Build TLS config from user's global settings without version overrides
	tlsConf, err := config.TLS.BuildTLSConfig()
	if err != nil {
		fmt.Printf("Error building TLS config: %v\n", err)
		return
	}

	// Perform connection with best negotiated settings
	conn, err := tls.Dial("tcp", net.JoinHostPort(host, port), tlsConf)
	if err != nil {
		fmt.Printf("Failed to establish connection: %v\n", err)
		return
	}
	defer conn.Close()

	connState := conn.ConnectionState()

	// Print negotiated details
	fmt.Printf("    Negotiated Version: %s\n", tlsVersionString(connState.Version))
	fmt.Printf("    Negotiated Cipher:  %s\n", tls.CipherSuiteName(connState.CipherSuite))
	fmt.Println()

	// Print certificate chain
	fmt.Println("[+] Server Certificate Chain:")
	for i, cert := range connState.PeerCertificates {
		fmt.Printf("    - Cert %d:\n", i)
		fmt.Printf("      Subject:  %s\n", cert.Subject.CommonName)
		fmt.Printf("      Issuer:   %s\n", cert.Issuer.CommonName)
		fmt.Printf("      Expires:  %s\n", cert.NotAfter.Format(time.RFC3339))
		if i < len(connState.PeerCertificates)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
}

func tlsVersionString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("Unknown (0x%04x)", version)
	}
}

// loadConfigForSnapshot is a helper function to load configuration for snapshot commands
func loadConfigForSnapshot(_ bool) (*mirror.Config, error) {
	config := mirror.NewConfig()
	meta, err := toml.DecodeFile(configPath, config)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("configuration file not found", "path", configPath)
			return nil, err
		}
		return nil, err
	}

	// Check for undecoded keys
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		errorMsg := formatUndecodedError(undecoded)
		slog.Error("configuration validation failed", "error", errorMsg, "path", configPath)
		return nil, fmt.Errorf("configuration validation failed")
	}

	// Apply log configuration
	if err := config.Log.Apply(); err != nil {
		return nil, err
	}

	// Override log level if specified on command line
	if logLevel != "" {
		config.Log.Level = logLevel
		if err := config.Log.Apply(); err != nil {
			return nil, err
		}
	}

	return config, nil
}

func runSnapshotCreate(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	mirrorID := args[0]
	var snapshotName string
	if len(args) > 1 {
		snapshotName = args[1]
	}

	mirrorConfig, ok := config.Mirrors[mirrorID]
	if !ok {
		slog.Error("mirror not found in configuration", "mirror", mirrorID)
		os.Exit(1)
	}

	force, _ := cmd.Flags().GetBool("force")
	stage, _ := cmd.Flags().GetBool("stage")

	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	if snapshotName == "" {
		snapshotName = sm.GenerateSnapshotNameForMirror(mirrorConfig.Snapshot)
	}

	createdSnapshot, err := sm.CreateSnapshot(mirrorID, snapshotName, force, mirrorConfig.Snapshot)
	if err != nil {
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to create snapshot", "error", errorMsg)
		os.Exit(1)
	}

	slog.Info("snapshot created successfully", "mirror", mirrorID, "snapshot", createdSnapshot)

	if stage {
		if err := sm.PublishSnapshotToStaging(mirrorID, createdSnapshot); err != nil {
			errorMsg := formatError(err, verboseErrors)
			slog.Error("failed to publish snapshot to staging", "error", errorMsg)
			os.Exit(1)
		}
		slog.Info("snapshot published to staging", "mirror", mirrorID, "snapshot", createdSnapshot)
	}
}

func runSnapshotList(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	detailed, _ := cmd.Flags().GetBool("detailed")
	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	// If no mirrors specified, list all configured mirrors
	mirrors := args
	if len(mirrors) == 0 {
		for mirrorID := range config.Mirrors {
			mirrors = append(mirrors, mirrorID)
		}
		sort.Strings(mirrors)
	}

	for _, mirrorID := range mirrors {
		if _, ok := config.Mirrors[mirrorID]; !ok {
			slog.Error("mirror not found in configuration", "mirror", mirrorID)
			continue
		}

		snapshots, err := sm.ListSnapshots(mirrorID)
		if err != nil {
			errorMsg := formatError(err, verboseErrors)
			slog.Error("failed to list snapshots", "mirror", mirrorID, "error", errorMsg)
			continue
		}

		fmt.Printf("Snapshots for mirror '%s':\n", mirrorID)
		if len(snapshots) == 0 {
			fmt.Println("  No snapshots found")
		} else {
			for _, snapshot := range snapshots {
				if detailed {
					fmt.Printf("  - %s (%s, size: %d bytes, files: %d)\n",
						snapshot.Name, snapshot.Status(), snapshot.Size, snapshot.FileCount)
				} else {
					fmt.Printf("  - %s (%s)\n", snapshot.Name, snapshot.Status())
				}
			}
		}
		fmt.Println()
	}
}

func runSnapshotPublish(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	mirrorID := args[0]
	snapshotName := args[1]

	if _, ok := config.Mirrors[mirrorID]; !ok {
		slog.Error("mirror not found in configuration", "mirror", mirrorID)
		os.Exit(1)
	}

	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	if err := sm.PublishSnapshot(mirrorID, snapshotName); err != nil {
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to publish snapshot", "error", errorMsg)
		os.Exit(1)
	}

	slog.Info("snapshot published to production", "mirror", mirrorID, "snapshot", snapshotName)
}

func runSnapshotStage(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	mirrorID := args[0]
	snapshotName := args[1]

	if _, ok := config.Mirrors[mirrorID]; !ok {
		slog.Error("mirror not found in configuration", "mirror", mirrorID)
		os.Exit(1)
	}

	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	if err := sm.PublishSnapshotToStaging(mirrorID, snapshotName); err != nil {
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to publish snapshot to staging", "error", errorMsg)
		os.Exit(1)
	}

	slog.Info("snapshot published to staging", "mirror", mirrorID, "snapshot", snapshotName)
}

func runSnapshotPromote(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	mirrorID := args[0]

	if _, ok := config.Mirrors[mirrorID]; !ok {
		slog.Error("mirror not found in configuration", "mirror", mirrorID)
		os.Exit(1)
	}

	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	promotedSnapshot, err := sm.PromoteSnapshot(mirrorID)
	if err != nil {
		errorMsg := formatError(err, verboseErrors)
		slog.Error("failed to promote snapshot", "error", errorMsg)
		os.Exit(1)
	}

	slog.Info("snapshot promoted to production", "mirror", mirrorID, "snapshot", promotedSnapshot)
}

func runSnapshotDelete(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	mirrorID := args[0]
	snapshotNames := args[1:]

	if _, ok := config.Mirrors[mirrorID]; !ok {
		slog.Error("mirror not found in configuration", "mirror", mirrorID)
		os.Exit(1)
	}

	force, _ := cmd.Flags().GetBool("force")
	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	for _, snapshotName := range snapshotNames {
		if err := sm.DeleteSnapshot(mirrorID, snapshotName, force); err != nil {
			errorMsg := formatError(err, verboseErrors)
			slog.Error("failed to delete snapshot", "mirror", mirrorID, "snapshot", snapshotName, "error", errorMsg)
			continue
		}
		slog.Info("snapshot deleted", "mirror", mirrorID, "snapshot", snapshotName)
	}
}

func runSnapshotPrune(cmd *cobra.Command, args []string) {
	verboseErrors, _ := cmd.Flags().GetBool("verbose-errors")
	config, err := loadConfigForSnapshot(verboseErrors)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if config.Snapshot == nil {
		slog.Error("snapshot configuration is required for snapshot commands")
		os.Exit(1)
	}

	keepLast, _ := cmd.Flags().GetInt("keep-last")
	keepWithin, _ := cmd.Flags().GetString("keep-within")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	sm := mirror.NewSnapshotManager(config.Snapshot, config.Dir)

	// If no mirrors specified, prune all configured mirrors
	mirrors := args
	if len(mirrors) == 0 {
		for mirrorID := range config.Mirrors {
			mirrors = append(mirrors, mirrorID)
		}
		sort.Strings(mirrors)
	}

	for _, mirrorID := range mirrors {
		mirrorConfig, ok := config.Mirrors[mirrorID]
		if !ok {
			slog.Error("mirror not found in configuration", "mirror", mirrorID)
			continue
		}

		var keepLastPtr *int
		var keepWithinPtr *string
		if keepLast > 0 {
			keepLastPtr = &keepLast
		}
		if keepWithin != "" {
			keepWithinPtr = &keepWithin
		}

		deletedSnapshots, err := sm.PruneSnapshots(mirrorID, mirrorConfig.Snapshot, dryRun, keepLastPtr, keepWithinPtr)
		if err != nil {
			errorMsg := formatError(err, verboseErrors)
			slog.Error("failed to prune snapshots", "mirror", mirrorID, "error", errorMsg)
			continue
		}

		if dryRun {
			if len(deletedSnapshots) > 0 {
				fmt.Printf("Would delete %d snapshots for mirror '%s':\n", len(deletedSnapshots), mirrorID)
				for _, snapshot := range deletedSnapshots {
					fmt.Printf("  - %s\n", snapshot)
				}
			} else {
				fmt.Printf("No snapshots would be deleted for mirror '%s'\n", mirrorID)
			}
		} else {
			if len(deletedSnapshots) > 0 {
				slog.Info("pruned snapshots", "mirror", mirrorID, "count", len(deletedSnapshots))
			} else {
				slog.Info("no snapshots pruned", "mirror", mirrorID)
			}
		}
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
