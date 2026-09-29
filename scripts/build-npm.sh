#!/usr/bin/env bash
# Cross-compile importstats and generate the npm packages under npm/: one
# @mnkdev/importstats-<os>-<cpu> package per platform (npm/platforms/*) plus the
# importstats launcher (npm/importstats). Usage: scripts/build-npm.sh <version>
# Publishing is separate: platform packages first, then npm/importstats last.
set -euo pipefail

VERSION="${1:?usage: scripts/build-npm.sh <version>}"
SCOPE="@mnkdev"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# npm os-cpu (process.platform-process.arch)  ->  GOOS GOARCH
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
  "name": "$SCOPE/importstats-$NPM_TARGET",
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
  printf '# %s/importstats-%s\n\nPlatform binary for [importstats](https://www.npmjs.com/package/importstats). Do not install directly.\n' "$SCOPE" "$NPM_TARGET" > "$DIR/README.md"
done

cp LICENSE npm/importstats/LICENSE

# Stamp the version and the platform optionalDependencies into the main package.
node -e '
const fs = require("fs"), p = "npm/importstats/package.json";
const [v, scope, ...targets] = process.argv.slice(1);
const j = JSON.parse(fs.readFileSync(p, "utf8"));
j.version = v;
j.optionalDependencies = {};
for (const t of targets) j.optionalDependencies[scope + "/importstats-" + t] = v;
fs.writeFileSync(p, JSON.stringify(j, null, 2) + "\n");
' "$VERSION" "$SCOPE" $(for t in "${TARGETS[@]}"; do echo "${t%% *}"; done)

echo "done: npm/platforms/* and npm/importstats are ready to publish (version $VERSION)"
