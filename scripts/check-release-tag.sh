#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'release-tag-check: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Usage: scripts/check-release-tag.sh [TAG]

Validate a MnemoNAS release tag before building release artifacts.

If TAG is omitted, GITHUB_REF_NAME is used.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	usage
	exit 0
fi

[[ "$#" -le 1 ]] || fail "expected at most one tag argument"

tag="${1:-${GITHUB_REF_NAME:-}}"
[[ -n "$tag" ]] || fail "release tag is required"

if [[ "$tag" == *+* ]]; then
	fail "release tag must not include build metadata because Docker tags do not support '+'"
fi

pattern='^v([0-9]+)\.([0-9]+)\.([0-9]+)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
if [[ ! "$tag" =~ $pattern ]]; then
	fail "release tag must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-PRERELEASE"
fi

docker_tag="${tag#v}"
if ((${#docker_tag} > 128)); then
	fail "release tag without the v prefix must be at most 128 characters for Docker image tags: $tag"
fi

prerelease="${BASH_REMATCH[5]:-}"
for component in "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"; do
	if [[ "$component" =~ ^0[0-9]+$ ]]; then
		fail "release tag numeric components must not contain leading zeroes: $tag"
	fi
done

if [[ -n "$prerelease" ]]; then
	IFS='.' read -r -a identifiers <<<"$prerelease"
	for identifier in "${identifiers[@]}"; do
		if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" =~ ^0[0-9]+$ ]]; then
			fail "release tag numeric prerelease identifiers must not contain leading zeroes: $tag"
		fi
	done
fi

printf '[release-tag-check] valid release tag: %s\n' "$tag"
