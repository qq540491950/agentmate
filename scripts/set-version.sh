#!/usr/bin/env bash
# set-version.sh <version> — 让 git tag 成为应用版本的唯一事实来源，
# 然后重新生成所有派生构建资产。
#
# 版本通过两条互不依赖的路径到达最终产品：
#
#   1. 二进制：链接时通过 `-X main.Version=<version>` 注入（见
#      build/{windows,linux,darwin}/Taskfile.yml 中的 BUILD_FLAGS）。
#   2. 安装器/打包元数据：build/config.yml 的 `info.version` 供
#      `wails3 update build-assets` 使用，后者生成：
#        build/windows/info.json      -> exe VERSIONINFO（wails3 generate syso）
#        build/windows/nsis/wails_tools.nsh -> NSIS VIProductVersion + 卸载键
#        build/darwin/Info.plist      -> CFBundleShortVersionString
#        build/linux/nfpm/nfpm.yaml   -> deb/rpm 版本
#
# 手工编辑这四个文件（或忘记编辑）正是本仓库曾在 0.4.0 应用里发布 0.1.0
# 元数据的原因。手工编辑也没有意义：`wails3 update build-assets` 会覆盖它们。
# 因此本脚本只写 build/config.yml，其余交给生成器。
set -euo pipefail
cd "$(dirname "$0")/.."
# 与 scripts/verify.sh 相同的 PATH 处理：`go install` 安装的 CLI 位于
# $GOPATH/bin，裸 bash shell 的 PATH 可能不包含它。
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

# main.Version 必须是 var，-X 才能生效；const 会静默忽略该标志。
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
