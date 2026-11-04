#!/usr/bin/env bash
# Print "due" when the last GitHub Release is older than the batch interval,
# "not-due" when it is too recent. Used by the issue-fixer runbook before a
# batch tag. The interval defaults to 60 minutes; override with
# RELEASE_INTERVAL_MINUTES.
set -euo pipefail
cd "$(dirname "$0")/.."

interval="${RELEASE_INTERVAL_MINUTES:-60}"
if ! printf '%s' "$interval" | grep -Eq '^[0-9]+$'; then
	echo "release-due: invalid RELEASE_INTERVAL_MINUTES=$interval" >&2
	exit 2
fi

last="$(gh release list --repo qq540491950/go-workbuddy --limit 1 \
	--json publishedAt --jq '.[0].publishedAt // empty' 2>/dev/null || true)"
if [ -z "$last" ]; then
	echo "due"
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