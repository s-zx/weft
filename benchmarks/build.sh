#!/usr/bin/env bash
# Build crest-headless for use in Terminal-Bench Docker containers.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Building crest-headless (linux/amd64)..."
cd "$REPO_ROOT"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" \
    -o "$SCRIPT_DIR/crest-headless" \
    ./cmd/crest-headless

ls -lh "$SCRIPT_DIR/crest-headless"
echo "Done. Binary at: $SCRIPT_DIR/crest-headless"
