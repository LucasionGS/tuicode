#!/bin/bash
#######################################
# tuicode build script by Lucasion    #
#######################################

# Build release binaries for linux x64 and arm64 into dist/<platform>/bin.
set -e
cd "$(dirname "$0")"
for ARCH in amd64 arm64; do
  PLATFORM="linux_x64"
  if [[ "$ARCH" == "arm64" ]]; then
    PLATFORM="linux_arm64"
  fi
  echo "Building tuicode for $PLATFORM..."
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w" -o "dist/$PLATFORM/bin/tuicode" ./cmd/tuicode
done
echo "Done! Binaries are in dist/"
