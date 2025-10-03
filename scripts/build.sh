#!/bin/sh -e

usage() {
    echo "Usage: build.sh -version VERSION [-v] [mirrorctl]"
    echo
    echo "Builds the mirrorctl binary for Debian repository mirroring."
    echo "  -version VER    specify version (required)"
    echo "  -v              verbose output"
    echo
    echo "Note: For release builds with cross-compilation, use GoReleaser:"
    echo "  goreleaser build --snapshot --clean"
    echo "  goreleaser release --snapshot --clean"
    echo
    exit 2
}

VERBOSE=""
VERSION=""

while [ $# -gt 0 ]; do
    case "$1" in
        -v)
            VERBOSE="-v"
            shift
            ;;
        -version)
            if [ -z "$2" ]; then
                echo "Error: -version requires an argument"
                exit 1
            fi
            VERSION="$2"
            shift 2
            ;;
        -*)
            echo "Error: Unknown option $1"
            usage
            ;;
        *)
            break
            ;;
    esac
done

BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT=$(git rev-parse --short HEAD)
TARGET="mirrorctl"

# sanity check -------

if [ -z "$VERSION" ]; then
    echo "Error: -version is required"
    usage
fi

if [ $# -gt 1 ]; then
    usage
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
