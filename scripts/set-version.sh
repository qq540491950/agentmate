#!/usr/bin/env bash
# set-version.sh <version> — make the git tag the single source of truth for
# the app version, then regenerate every derived build asset.
#
# The version reaches the shipped product through two disjoint paths:
#
#   1. The binary: `-X main.Version=<version>` at link time (see the
#      BUILD_FLAGS in build/{windows,linux,darwin}/Taskfile.yml).
#   2. The installer/packager metadata: build/config.yml `info.version`
#      feeds `wails3 update build-assets`, which renders
#        build/windows/info.json      -> exe VERSIONINFO (wails3 generate syso)
#        build/windows/nsis/wails_tools.nsh -> NSIS VIProductVersion + uninstall key
#        build/darwin/Info.plist      -> CFBundleShortVersionString
#        build/linux/nfpm/nfpm.yaml   -> deb/rpm version
#
# Hand-editing those four files (or forgetting to) is exactly how this repo
# ended up shipping 0.1.0 metadata inside a 0.4.0 app. Editing them is also
# pointless: `wails3 update build-assets` overwrites them. So this script only
# writes build/config.yml and lets the generator do the rest.
set -euo pipefail
cd "$(dirname "$0")/.."
# Same PATH accommodation as scripts/verify.sh: `go install`ed CLIs land in
# $GOPATH/bin, which a bare bash shell may not have on PATH.
export PATH="$PATH:$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
	echo "usage: $0 <version>   (e.g. 0.5.0 or v0.5.0)" >&2
	exit 2
fi
VERSION="${VERSION#v}"
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
	echo "set-version: '$VERSION' is not a semver version (got from the tag)" >&2
	exit 2
fi

# main.Version must be a var for -X to land; a const silently ignores the flag.
if grep -qE '^const Version = ' main.go; then
	echo "set-version: main.go still declares 'const Version'; it must be 'var Version' for -ldflags -X to work" >&2
	exit 1
fi

CONFIG=build/config.yml
tmp="$(mktemp)"
awk -v v="$VERSION" '
	BEGIN { done = 0 }
	{ if (!done && $0 ~ /^  version: /) { printf "  version: \"%s\"\n", v; done = 1; next } print }
' "$CONFIG" >"$tmp"
mv "$tmp" "$CONFIG"

echo "==> build/config.yml info.version = $VERSION"
(cd build && wails3 update build-assets -name "agentmate" -binaryname "agentmate" -config config.yml -dir .)

echo "==> generated assets now report:"
grep -Ho '"ProductVersion": "[^"]*"' build/windows/info.json || true
grep -Ho 'INFO_PRODUCTVERSION "[^"]*"' build/windows/nsis/wails_tools.nsh || true
grep -Ho '<string>[0-9].*</string>' build/darwin/Info.plist | head -2 || true
grep -Ho '^version: "[^"]*"' build/linux/nfpm/nfpm.yaml || true
echo "set-version: ok ($VERSION)"
