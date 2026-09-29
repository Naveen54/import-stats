#!/usr/bin/env bash
# Cross-compile importstats and generate the npm packages (one per platform plus
# the main launcher) under npm/. Usage: scripts/build-npm.sh <version>
# Publishing is separate: platform packages first, then npm/importstats last.
set -euo pipefail

VERSION="${1:?usage: scripts/build-npm.sh <version>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# npm os-cpu  ->  GOOS/GOARCH
TARGETS=(
  "darwin-arm64 darwin arm64"
  "darwin-x64   darwin amd64"
  "linux-arm64  linux  arm64"
  "linux-x64    linux  amd64"
  "win32-arm64  windows arm64"
  "win32-x64    windows amd64"
)

rm -rf npm/platforms
for t in "${TARGETS[@]}"; do
  read -r NPM_TARGET GOOS GOARCH <<<"$t"
  OS="${NPM_TARGET%-*}"; CPU="${NPM_TARGET#*-}"
  EXE="importstats"; [ "$GOOS" = windows ] && EXE="importstats.exe"
  DIR="npm/platforms/$NPM_TARGET"
  mkdir -p "$DIR/bin"

  echo "==> $NPM_TARGET"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$DIR/bin/$EXE" ./cmd/importstats

  cat > "$DIR/package.json" <<JSON
{
  "name": "importstats-$NPM_TARGET",
  "version": "$VERSION",
  "description": "importstats binary for $OS-$CPU (installed automatically by the importstats package)",
  "license": "MIT",
  "os": ["$OS"],
  "cpu": ["$CPU"],
  "files": ["bin"],
  "repository": {
    "type": "git",
    "url": "git+https://github.com/Naveen54/import-stats.git"
  }
}
JSON
  cp LICENSE "$DIR/LICENSE"
  printf '# importstats-%s\n\nPlatform binary for [importstats](https://www.npmjs.com/package/importstats). Do not install directly.\n' "$NPM_TARGET" > "$DIR/README.md"
done

cp LICENSE npm/importstats/LICENSE

# Stamp the version into the main package and its optionalDependencies.
node -e '
const fs = require("fs"), p = "npm/importstats/package.json", v = process.argv[1];
const j = JSON.parse(fs.readFileSync(p, "utf8"));
j.version = v;
for (const k of Object.keys(j.optionalDependencies)) j.optionalDependencies[k] = v;
fs.writeFileSync(p, JSON.stringify(j, null, 2) + "\n");
' "$VERSION"

echo "done: npm/platforms/* and npm/importstats are ready to publish (version $VERSION)"
