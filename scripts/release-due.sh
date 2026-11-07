#!/usr/bin/env bash
# Report whether the batch release window is ready (manual diagnostics):
#   due            — interval elapsed and current main has new commits
#   not-due        — too soon since the last release
#   no-new-commits — the latest release already contains current main
#   not-synced     — local main differs from origin/main; do not release
#
# 发布列车的决策（pending / P0 / 未发布 tag 重试）由 scripts/release-train.sh
# 负责；本脚本只用于人工排查窗口是否到期。
# The interval defaults to 30 minutes; override with RELEASE_INTERVAL_MINUTES.
set -euo pipefail
cd "$(dirname "$0")/.."

interval="${RELEASE_INTERVAL_MINUTES:-30}"
if ! printf '%s' "$interval" | grep -Eq '^[0-9]+$'; then
	echo "release-due: invalid RELEASE_INTERVAL_MINUTES=$interval" >&2
	exit 2
fi

if ! git fetch origin main --quiet; then
	echo "not-synced"
	exit 0
fi
git fetch --tags --quiet origin 2>/dev/null || true

ahead=$(git rev-list --count origin/main..HEAD)
behind=$(git rev-list --count HEAD..origin/main)
if [ "$ahead" -ne 0 ] || [ "$behind" -ne 0 ]; then
	echo "not-synced"
	exit 0
fi

if ! latest="$(gh release list --repo qq540491950/agentmate --limit 1 \
	--json tagName,publishedAt \
	--jq 'if length == 0 then "" else (.[0].tagName + "\t" + .[0].publishedAt) end')"; then
	echo "release-due: gh release list failed" >&2
	exit 2
fi
if [ -z "$latest" ]; then
	echo "due"
	exit 0
fi

tag="${latest%%$'\t'*}"
last="${latest#*$'\t'}"

if ! git rev-parse --verify "refs/tags/$tag" >/dev/null 2>&1; then
	echo "release-due: latest release tag '$tag' not found locally" >&2
	exit 2
fi
if [ "$(git rev-list --count "$tag"..HEAD)" -eq 0 ]; then
	echo "no-new-commits"
	exit 0
fi

if last_epoch=$(TZ=UTC date -j -f "%Y-%m-%dT%H:%M:%SZ" "$last" +%s 2>/dev/null); then
	:
elif last_epoch=$(date -u -d "$last" +%s 2>/dev/null); then
	:
else
	echo "release-due: cannot parse publishedAt '$last'" >&2
	exit 2
fi

now=$(date +%s)
if [ $((now - last_epoch)) -ge $((interval * 60)) ]; then
	echo "due"
else
	echo "not-due"
fi