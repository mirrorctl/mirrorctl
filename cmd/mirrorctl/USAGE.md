How to configure and run mirrorctl
======================================

Synopsis
--------

```
mirrorctl [flags] [mirror-ids...]
mirrorctl [command]
```

mirrorctl is a console application for creating and maintaining mirrors of Debian package repositories.
Run it in your shell, or use `sudo -u USER` to run it as USER.

If `mirror-ids` arguments are given, mirrorctl updates only the specified
Debian repository mirrors. With no arguments, it updates all mirrors
defined in the configuration file.

Examples
--------

```bash
# Mirror all configured repositories
mirrorctl

# Mirror only specific repositories
mirrorctl ubuntu security

# Use custom configuration file
mirrorctl --config /path/to/custom.toml

# Show version information
mirrorctl --version

# Get help
mirrorctl --help
```

Available Commands
------------------

| Command      | Description                           |
| ------------ | ------------------------------------- |
| `help`       | Help about any command                |
| `version`    | Print version information             |
| `completion` | Generate shell autocompletion scripts |

Flags
-----

| Flag        | Short | Default                | Description                        |
| ----------- | ----- | ---------------------- | ---------------------------------- |
| `--config`  | `-c`  | `/etc/mirrorctl/mirror.toml` | Configuration file path            |
| `--help`    | `-h`  |                        | Show help information              |
| `--version` | `-v`  |                        | Print version information and exit |

Configuration
-------------

mirrorctl reads configurations from a [TOML][] file.  
The default location is `/etc/mirrorctl/mirror.toml`.

A sample configuration file is available [here](mirror.toml).

### Configuration Options

```toml
# Directory to store mirrored files and control files
dir = "/var/spool/mirrorctl"

# Maximum concurrent connections per upstream server
max_conns = 10

# Logging configuration
[log]
level = "info"    # debug, info, warn, error
format = "plain"  # plain, text, json

# Mirror definitions
[mirror.ubuntu]
url = "http://archive.ubuntu.com/ubuntu"
suites = ["noble", "noble-updates", "noble-security"]
sections = ["main", "restricted", "universe", "multiverse"]
architectures = ["amd64", "arm64"]
mirror_source = false
```

Proxy Support
-------------

mirrorctl uses HTTP proxy as specified in [`ProxyFromEnvironment`](https://golang.org/pkg/net/http/#ProxyFromEnvironment).
Set the `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables as needed.

Logging
-------

mirrorctl uses Go's standard `slog` library for structured logging. Log level and format can be configured via the configuration file.

Shell Completion
---------------

Generate shell completion scripts:

```bash
# Bash
mirrorctl completion bash > /etc/bash_completion.d/mirrorctl

# Zsh
mirrorctl completion zsh > /usr/local/share/zsh/site-functions/_mirrorctl

# Fish
mirrorctl completion fish > ~/.config/fish/completions/mirrorctl.fish

# PowerShell
mirrorctl completion powershell > mirrorctl.ps1
```

Build Information
----------------

The version information includes build details when built with proper build flags:

```bash
go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse HEAD) -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./cmd/mirrorctl
```
