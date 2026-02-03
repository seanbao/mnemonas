#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail() {
	printf 'release-tag-check: %s\n' "$*" >&2
	exit 1
}

# shellcheck source=scripts/release-version.sh
. "$SCRIPT_DIR/release-version.sh"

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
validate_docker_release_version "$tag" "release tag" "release tag is required" 0

printf '[release-tag-check] valid release tag: %s\n' "$tag"
