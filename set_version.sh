#!/bin/bash
#######################################
# tuicode version bump by Lucasion    #
#######################################

# Update the version in every place it is stored.
VERSION="$1"
if [[ -z "$VERSION" ]]; then
  echo "Set the version of tuicode in all files."
  echo "Usage: ./set_version.sh <VERSION>"
  exit 2
fi
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "Invalid version: $VERSION. Must match the regex: ^[0-9]+\.[0-9]+\.[0-9]+(-prerelease)?$"
  exit 3
fi
sed -i "s/^const Version = \".*\"/const Version = \"$VERSION\"/" internal/ui/app.go
sed -i "s/^version: \".*\"/version: \"$VERSION\"/" ipm-package.yaml
sed -i "s/\"version\": \"[^\"]*\"}/\"version\": \"$VERSION\"}/" internal/lsp/client.go
# a new version starts over at the first package release
sed -i -e "s/^pkgver=.*/pkgver=$VERSION/" -e "s/^pkgrel=.*/pkgrel=1/" PKGBUILD packaging/pacman/PKGBUILD
echo "Done! Version set to $VERSION"
