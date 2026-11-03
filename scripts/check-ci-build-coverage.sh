#!/usr/bin/env bash
# Guard the pipeline invariant that a push/PR actually compiles the Wails
# Windows app, not just the Go packages and frontend assets.
set -euo pipefail
cd "$(dirname "$0")/.."

ci=.github/workflows/ci.yml
if ! grep -q 'runs-on: windows-latest' "$ci"; then
	echo "ci.yml 缺少 windows-latest job：桌面构建不再被 CI 覆盖" >&2
	exit 1
fi
if ! grep -q 'task windows:build' "$ci"; then
	echo "ci.yml 缺少 task windows:build：CI 没有真正构建 Windows exe" >&2
	exit 1
fi
if ! grep -q 'name: Test (windows)' "$ci"; then
	echo "ci.yml 的 Windows job 缺少 go test：平台相关用例会拖到打标签后才失败" >&2
	exit 1
fi

echo "CI Windows build coverage: OK"