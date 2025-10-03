/*
Package mirrorctl is a tool for mirroring Debian/Ubuntu APT repositories.

mirrorctl provides efficient, secure mirroring of APT repositories with features including:
  - Incremental updates with by-hash support
  - PGP signature verification
  - TLS certificate validation
  - Snapshot management
  - Concurrent downloads with connection pooling
  - Atomic updates with file locking

The main packages are:

	github.com/mirrorctl/mirrorctl/internal/apt     - APT repository format parsing and validation
	github.com/mirrorctl/mirrorctl/internal/mirror  - Core mirroring logic and storage abstraction
	github.com/mirrorctl/mirrorctl/cmd/mirrorctl    - Command-line interface
*/
package mirrorctl
