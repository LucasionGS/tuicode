#!/bin/bash
#######################################
# tuicode AUR update by Lucasion      #
#######################################

# Publish the tuicode-bin PKGBUILD for the current version to the AUR, once its GitHub release exists.
set -e
cd "$(dirname "$0")"
VERSION="$(grep -m1 '^pkgver=' PKGBUILD | cut -d= -f2)"
if ! curl -fsIL "https://github.com/LucasionGS/tuicode/releases/download/v$VERSION/tuicode-$VERSION-1-x86_64.pkg.tar.zst" > /dev/null; then
  echo "The release v$VERSION is not on GitHub yet. Push to the release branch and wait for the Release workflow."
  exit 1
fi
echo "Updating checksums for tuicode-bin $VERSION..."
updpkgsums
if [ -d .aur ]; then
  rm -rf .aur
fi
git -c init.defaultBranch=master clone ssh://aur@aur.archlinux.org/tuicode-bin.git .aur
cp PKGBUILD .aur/PKGBUILD
makepkg --printsrcinfo > .aur/.SRCINFO
cd .aur
git add PKGBUILD .SRCINFO
if git diff --cached --quiet; then
  echo "The AUR package is already up to date."
  exit 0
fi
git commit -m "Update to $VERSION"
git push
echo "Done! Published tuicode-bin $VERSION to the AUR"
