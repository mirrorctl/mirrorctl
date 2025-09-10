# GoReleaser Setup Guide

This project uses GoReleaser for automated releases and cross-platform builds.

I can't really imagine someone wanting to create a Debian repository from a Windows or Mac desktop
device, but this project may support alternate storage back-ends in the future, so I'm building
binaries for Windows and Mac for now. That, and people may just want to kick the tires.

## Install GoReleaser

### Install via Go
```bash
go install github.com/goreleaser/goreleaser/v2@latest
```

**Install via manual download:**
```bash
# Download the latest release from https://github.com/goreleaser/goreleaser/releases
# Extract and place the binary in your PATH
```

### Install via package manager

**macOS:**
```bash
brew install goreleaser
```

## Usage

### Test Configuration
```bash
# Check if the configuration is valid
goreleaser check

# Test build without releasing (creates snapshot builds)
goreleaser build --snapshot --clean

# Test full release process without publishing
goreleaser release --snapshot --clean
```

### Local Development Builds
```bash
# Use the existing build script for single-platform builds
./scripts/build.sh

# Or use GoReleaser for cross-platform builds
goreleaser build --snapshot --clean
```

### Release Process

1. **Tag a release:**
   ```bash
   git tag -a v1.5.0 -m "Release v1.5.0"
   git push origin v1.5.0
   ```

2. **GitHub Actions will automatically:**
   - Run tests
   - Build binaries for multiple platforms
   - Create a GitHub release
   - Upload binaries and checksums
   - Generate changelog

### Manual Release (if needed)
```bash
# Ensure you have GITHUB_TOKEN set
export GITHUB_TOKEN="your_token_here"

# Create a release
goreleaser release --clean
```

## Configuration Files

- `.goreleaser.yml` - Main GoReleaser configuration
- `.github/workflows/release.yml` - GitHub Actions for releases
- `.github/workflows/ci.yml` - Continuous Integration workflow

## Build Outputs
goreleaser build --snapshot --clean
GoReleaser will create:
- Cross-platform binaries (Linux, macOS, Windows)
- Multiple architectures (amd64, arm64, arm)
- Compressed archives (tar.gz for Unix, zip for Windows)
- SHA256 checksums
- GitHub releases with changelog

## Features Enabled

✅ Cross-platform builds (Linux, macOS, Windows)  
✅ Multiple architectures (amd64, arm64, armv7)  
✅ Version injection (same as build.sh script)  
✅ Automatic changelog generation  
✅ GitHub release creation  
✅ Binary checksums  
✅ Archive creation  

## Optional Features (Commented Out)

The configuration includes commented sections for:
- Homebrew tap publishing
- Docker image building
- Slack/Discord notifications

Uncomment these sections if needed for your workflow.

## Testing Your Setup

1. Install GoReleaser
2. Run `goreleaser check` to validate config
3. Run `goreleaser build --snapshot --clean` to test builds
4. Check the `dist/` directory for generated binaries
5. Test a binary: `./dist/go-apt-mirror_linux_amd64_v1/go-apt-mirror version`

## Troubleshooting

**Configuration errors:**
- Run `goreleaser check` to validate
- Check `.goreleaser.yml` syntax

**Build failures:**
- Ensure Go modules are tidy: `go mod tidy`
- Check that tests pass: `go test ./...`

**Release failures:**
- Verify GITHUB_TOKEN has proper permissions
- Check that the tag follows semver (v1.2.3)
