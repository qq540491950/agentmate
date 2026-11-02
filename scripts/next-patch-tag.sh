#!/usr/bin/env bash
# Print the next patch tag (vX.Y.Z) based on the highest existing v* tag.
# Used by the automated issue-fixer runbook before publishing a patch release.
set -euo pipefail
cd "$(dirname "$0")/.."

git fetch --tags --quiet origin || true

latest="$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n 1)"
if [ -z "$latest" ]; then
	echo "v0.0.1"
	exit 0
fi

version="${latest#v}"
version="${version%%-*}" # drop a prerelease suffix
IFS=. read -r major minor patch <<<"$version"

if ! [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ && "$patch" =~ ^[0-9]+$ ]]; then
	echo "next-patch-tag: cannot parse tag '$latest'" >&2
	exit 1
fi

echo "v${major}.${minor}.$((patch + 1))"
