#!/bin/sh -e

usage() {
    echo "Usage: build.sh [go-apt-mirror]"
    echo
    echo "Builds the go-apt-mirror binary for Debian repository mirroring."
    echo "If no target is specified, defaults to go-apt-mirror."
    echo
    exit 2
}


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

echo "Building..."

${GOPATH}/bin/gox \
    -os="${XC_OS}" \
    -arch="${XC_ARCH}" \
    -output "pkg/${TARGET}_{{.OS}}_{{.Arch}}/${TARGET}" \
    ./cmd/${TARGET}
