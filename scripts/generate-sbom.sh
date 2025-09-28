#!/bin/bash
set -e

echo "Generating SBOMs for release artifacts..."

# Check if syft is available
if ! command -v syft >/dev/null 2>&1; then
    echo "Error: syft not found. Please install syft first."
    exit 1
fi

# Set SYFT environment to avoid update checks
export SYFT_CHECK_FOR_APP_UPDATE=false

# Generate SBOM for the source directory (overall project SBOM)
echo "Generating project SBOM..."
syft scan . \
    --output spdx-json=mirrorctl_project.spdx.json \
    --output cyclonedx-json=mirrorctl_project.cyclonedx.json

# Generate SBOMs for each built archive in dist/ if they exist
if [ -d "dist" ] && [ "$(ls -A dist/*.tar.gz 2>/dev/null)" ]; then
    echo "Generating SBOMs for release archives..."
    for archive in dist/*.tar.gz; do
        if [ -f "$archive" ]; then
            basename=$(basename "$archive" .tar.gz)
            echo "  Processing $basename..."
            syft scan "$archive" \
                --output spdx-json="dist/${basename}.spdx.json" \
                --output cyclonedx-json="dist/${basename}.cyclonedx.json"
        fi
    done
else
    echo "No release archives found in dist/, skipping archive SBOM generation."
fi

echo "SBOM generation complete!"
echo "Generated files:"
ls -la *.spdx.json *.cyclonedx.json 2>/dev/null || true
ls -la dist/*.spdx.json dist/*.cyclonedx.json 2>/dev/null || true