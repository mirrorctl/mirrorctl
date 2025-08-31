#!/bin/sh -e

usage() {
    echo "Usage: build.sh [-v] [go-apt-mirror]"
    echo
    echo "Builds the go-apt-mirror binary for Debian repository mirroring."
    echo "If no target is specified, defaults to go-apt-mirror."
    echo "  -v    verbose output"
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
    if [ "$1" != "go-apt-mirror" ]; then
        if [ "$1" = "go-apt-cacher" ]; then
            echo "Error: go-apt-cacher has been removed from this application."
            echo "This application now focuses exclusively on repository mirroring."
            echo "Use 'go-apt-mirror' or run without arguments."
            echo
            exit 1
        fi
        usage
    fi
    TARGET="$1"
else
    TARGET="go-apt-mirror"
fi
XC_OS="${XC_OS:-$(go env GOOS)}"
XC_ARCH="${XC_ARCH:-$(go env GOARCH)}"

# build ------

echo "Building ${TARGET} ${VERSION} (${GIT_COMMIT}) built at ${BUILD_DATE}..."

# Create output directory
mkdir -p "pkg/${TARGET}_${XC_OS}_${XC_ARCH}"

# Build with version information injected via ldflags
GOOS="${XC_OS}" GOARCH="${XC_ARCH}" go build \
    ${VERBOSE} \
    -ldflags "-X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o "pkg/${TARGET}_${XC_OS}_${XC_ARCH}/${TARGET}" \
    ./cmd/${TARGET}
