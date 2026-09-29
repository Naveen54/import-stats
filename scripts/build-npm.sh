#!/usr/bin/env bash
# Cross-compile importstats for every supported platform into the single npm
# package at npm/importstats (bin/<os>-<cpu>/importstats[.exe]) and stamp its
# version. Usage: scripts/build-npm.sh <version>
# Publishing is separate: cd npm/importstats && npm publish --access public
set -euo pipefail

VERSION="${1:?usage: scripts/build-npm.sh <version>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PKG=npm/importstats

# npm os-cpu (process.platform-process.arch)  ->  GOOS GOARCH
TARGETS=(
  "darwin-arm64 darwin arm64"
  "darwin-x64   darwin amd64"
  "linux-arm64  linux  arm64"
  "linux-x64    linux  amd64"
  "win32-arm64  windows arm64"
  "win32-x64    windows amd64"
)

rm -rf "$PKG"/bin/*/
for t in "${TARGETS[@]}"; do
  read -r NPM_TARGET GOOS GOARCH <<<"$t"
  EXE="importstats"; [ "$GOOS" = windows ] && EXE="importstats.exe"
  mkdir -p "$PKG/bin/$NPM_TARGET"

  echo "==> $NPM_TARGET"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$PKG/bin/$NPM_TARGET/$EXE" ./cmd/importstats
done

cp LICENSE "$PKG/LICENSE"

node -e '
const fs = require("fs"), p = process.argv[1], v = process.argv[2];
const j = JSON.parse(fs.readFileSync(p, "utf8"));
j.version = v;
fs.writeFileSync(p, JSON.stringify(j, null, 2) + "\n");
' "$PKG/package.json" "$VERSION"

echo "done: $PKG is ready to publish (version $VERSION)"
