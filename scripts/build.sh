#!/bin/sh -e

usage() {
    echo "Usage: build.sh [-v] [mirrorctl]"
    echo
    echo "Builds the mirrorctl binary for Debian repository mirroring."
    echo "If no target is specified, defaults to mirrorctl."
    echo "  -v    verbose output"
    echo
    echo "Note: For release builds with cross-compilation, use GoReleaser:"
    echo "  goreleaser build --snapshot --clean"
    echo "  goreleaser release --snapshot --clean"
    echo
    exit 2
}

VERBOSE=""
if [ "$1" = "-v" ]; then
    VERBOSE="-v"
    shift
fi

VERSION="v1.4.9"
GIT_COMMIT=$(git rev-parse --short HEAD)
BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')

# sanity check -------

if [ $# -gt 1 ]; then
    usage
fi

if [ $# -eq 1 ]; then
    if [ "$1" != "mirrorctl" ]; then
        if [ "$1" = "go-apt-cacher" ]; then
            echo "Error: go-apt-cacher has been removed from this application."
            echo "This application now focuses exclusively on repository mirroring."
            echo "Use 'mirrorctl' or run without arguments."
            echo
            exit 1
        fi
        usage
    fi
    TARGET="$1"
else
    TARGET="mirrorctl"
fi
XC_OS="${XC_OS:-$(go env GOOS)}"
XC_ARCH="${XC_ARCH:-$(go env GOARCH)}"

# build ------

echo "Building ${TARGET} ${VERSION} (${GIT_COMMIT}) built at ${BUILD_DATE}..."

# Build with version information injected via ldflags
# Note: These ldflags match what GoReleaser uses for consistency
GOOS="${XC_OS}" GOARCH="${XC_ARCH}" go build \
    ${VERBOSE} \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o "bin/${TARGET}" \
    ./cmd/${TARGET}

# Generate SBOM if syft is available
if command -v syft >/dev/null 2>&1; then
    echo "Generating SBOM for ${TARGET}..."
    syft scan "bin/${TARGET}" --output spdx-json="bin/${TARGET}.spdx.json" --output cyclonedx-json="bin/${TARGET}.cyclonedx.json"
    echo "SBOM files generated: bin/${TARGET}.spdx.json, bin/${TARGET}.cyclonedx.json"
else
    echo "Warning: syft not found. Install syft to generate SBOM files:"
    echo "  curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b /usr/local/bin"
fi
