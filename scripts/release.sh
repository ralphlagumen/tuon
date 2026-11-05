#!/bin/sh
# Build a release the reproducible way and print its checksums.
# Anyone can rebuild the same tag with the same Go version and compare.
#
#   ./scripts/release.sh v0.2.0      (the tag must exist and be signed)
set -eu
cd "$(dirname "$0")/.."
tag="${1:?usage: release.sh <tag>}"
git verify-tag "$tag" >/dev/null 2>&1 || { echo "tag $tag is missing or not signed"; exit 1; }
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "commit your changes first"; exit 1; }
[ "$(git rev-parse HEAD)" = "$(git rev-parse "$tag^{commit}")" ] || { echo "check out $tag first"; exit 1; }
out="dist/$tag"; rm -rf "$out"; mkdir -p "$out"
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}; ext=""; [ "$os" = windows ] && ext=.exe
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags "-s -w -buildid= -X main.version=$tag" -o "$out/tuon-$os-$arch$ext" ./cmd/tuon
done
( cd "$out" && shasum -a 256 tuon-* > SHA256SUMS && cat SHA256SUMS )
echo "Built with $(go version | cut -d' ' -f3). Publish SHA256SUMS with the release."
