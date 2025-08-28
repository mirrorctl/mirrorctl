How to configure and run go-apt-mirror
======================================

Synopsis
--------

```
go-apt-mirror [flags] [mirror-ids...]
go-apt-mirror [command]
```

go-apt-mirror is a console application for creating and maintaining mirrors of Debian package repositories.
Run it in your shell, or use `sudo -u USER` to run it as USER.

If `mirror-ids` arguments are given, go-apt-mirror updates only the specified
Debian repository mirrors. With no arguments, it updates all mirrors
defined in the configuration file.

Examples
--------

```bash
# Mirror all configured repositories
go-apt-mirror

# Mirror only specific repositories
go-apt-mirror ubuntu security

# Use custom configuration file
go-apt-mirror --config /path/to/custom.toml

# Show version information
go-apt-mirror --version

# Get help
go-apt-mirror --help
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
| `--config`  | `-c`  | `/etc/apt/mirror.toml` | Configuration file path            |
| `--help`    | `-h`  |                        | Show help information              |
| `--version` | `-v`  |                        | Print version information and exit |

Configuration
-------------

go-apt-mirror reads configurations from a [TOML][] file.  
The default location is `/etc/apt/mirror.toml`.

A sample configuration file is available [here](mirror.toml).

### Configuration Options

```toml
# Directory to store mirrored files and control files
dir = "/var/spool/go-apt-mirror"

# Maximum concurrent connections per upstream server
max_conns = 10

# Logging configuration
[log]
level = "info"    # debug, info, warn, error
format = "plain"  # plain, text, json

# Mirror definitions
[mirror.ubuntu]
url = "http://archive.ubuntu.com/ubuntu"
suites = ["jammy", "jammy-updates", "jammy-security"]
sections = ["main", "restricted", "universe", "multiverse"]
architectures = ["amd64", "arm64"]
mirror_source = false
```

Proxy Support
-------------

go-apt-mirror uses HTTP proxy as specified in [`ProxyFromEnvironment`](https://golang.org/pkg/net/http/#ProxyFromEnvironment).
Set the `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables as needed.

Logging
-------

go-apt-mirror uses Go's standard `slog` library for structured logging. Log level and format can be configured via the configuration file.

Shell Completion
---------------

Generate shell completion scripts:

```bash
# Bash
go-apt-mirror completion bash > /etc/bash_completion.d/go-apt-mirror

# Zsh
go-apt-mirror completion zsh > /usr/local/share/zsh/site-functions/_go-apt-mirror

# Fish
go-apt-mirror completion fish > ~/.config/fish/completions/go-apt-mirror.fish

# PowerShell
go-apt-mirror completion powershell > go-apt-mirror.ps1
```

Build Information
----------------

The version information includes build details when built with proper build flags:

```bash
go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse HEAD) -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./cmd/go-apt-mirror
```
