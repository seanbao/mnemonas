#!/usr/bin/env bash

set -euo pipefail

BASE_REF="master"
ALLOW_DIRTY=0
CHECK_CHECKLIST=1

usage() {
	cat <<'EOF'
Usage: scripts/release-readiness.sh [--base REF] [--allow-dirty] [--skip-checklist]

Print a read-only release readiness summary for the current branch.

Options:
  --base REF        Compare the current branch against REF. Defaults to master.
  --allow-dirty    Print a draft summary even when the worktree is dirty.
  --skip-checklist  Skip release checklist command assertions.
  -h, --help        Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--base)
			[[ $# -ge 2 ]] || {
				printf 'release-readiness: --base requires a ref\n' >&2
				exit 2
			}
			BASE_REF="$2"
			shift 2
			;;
		--allow-dirty)
			ALLOW_DIRTY=1
			shift
			;;
		--skip-checklist)
			CHECK_CHECKLIST=0
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf 'release-readiness: unknown argument: %s\n' "$1" >&2
			exit 2
			;;
	esac
done

fail() {
	printf 'release-readiness: %s\n' "$1" >&2
	exit 1
}

print_kv() {
	printf '[release-readiness] %-18s %s\n' "$1:" "$2"
}

require_file_contains() {
	local path="$1"
	local expected="$2"

	[[ -f "$path" ]] || fail "missing required checklist file: $path"
	grep -Fq -- "$expected" "$path" || {
		printf 'release-readiness: %s is missing required text:\n%s\n' "$path" "$expected" >&2
		exit 1
	}
}

require_community_file() {
	local path="$1"

	[[ -f "$path" ]] || fail "missing required community file: $path"
}

check_community_files() {
	local path
	local required_files=(
		"README.md"
		"README.en.md"
		"LICENSE"
		"CHANGELOG.md"
		"CHANGELOG.en.md"
		"CONTRIBUTING.md"
		"CONTRIBUTING.en.md"
		"CODE_OF_CONDUCT.md"
		"CODE_OF_CONDUCT.zh-CN.md"
		"SUPPORT.md"
		"SUPPORT.en.md"
		"SECURITY.md"
		"SECURITY.zh-CN.md"
		".github/ISSUE_TEMPLATE/config.yml"
		".github/ISSUE_TEMPLATE/bug_report.yml"
		".github/ISSUE_TEMPLATE/feature_request.yml"
		".github/ISSUE_TEMPLATE/question.yml"
		".github/pull_request_template.md"
	)

	for path in "${required_files[@]}"; do
		require_community_file "$path"
	done

	print_kv "community" "required community health files present"
}

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	fail "must run inside a git repository"
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if ! git rev-parse --verify HEAD >/dev/null 2>&1; then
	fail "HEAD does not point to a commit"
fi

if ! git rev-parse --verify "$BASE_REF^{commit}" >/dev/null 2>&1; then
	fail "base ref does not exist: $BASE_REF"
fi

branch="$(git branch --show-current)"
[[ -n "$branch" ]] || branch="(detached)"
head_sha="$(git rev-parse --short=12 HEAD)"
commit_count="$(git rev-list --count "$BASE_REF..HEAD")"
shortstat="$(git diff --shortstat "$BASE_REF..HEAD")"
[[ -n "$shortstat" ]] || shortstat="no file changes"

status_output="$(git status --short)"
if [[ -n "$status_output" && "$ALLOW_DIRTY" -eq 0 ]]; then
	printf '%s\n' "$status_output" >&2
	fail "worktree has uncommitted changes; commit them or rerun with --allow-dirty for a draft summary"
fi

print_kv "branch" "$branch"
print_kv "head" "$head_sha"
print_kv "base" "$BASE_REF"
print_kv "commits" "$commit_count"
print_kv "diff" "$shortstat"

if [[ -n "$status_output" ]]; then
	print_kv "worktree" "dirty (draft summary)"
else
	print_kv "worktree" "clean"
fi

planner_output="$(./scripts/plan-hardening-commits.sh --fail-on-manual 2>&1)" || {
	printf '%s\n' "$planner_output" >&2
	fail "commit grouping planner reported unclassified paths"
}
while IFS= read -r line; do
	[[ -n "$line" ]] || continue
	printf '[release-readiness] planner          %s\n' "$line"
done <<<"$planner_output"

check_community_files

if [[ "$CHECK_CHECKLIST" -eq 1 ]]; then
	verify_changed_cmd="GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master"
	artifact_verify_cmd="./scripts/verify-release-artifacts.sh --version <tag> --repository seanbao/mnemonas --require-targets --check-image <artifact-dir>"

	require_file_contains "CHANGELOG.md" "$verify_changed_cmd"
	require_file_contains "CHANGELOG.en.md" "$verify_changed_cmd"
	require_file_contains "CHANGELOG.md" "make scripts-check"
	require_file_contains "CHANGELOG.en.md" "make scripts-check"
	require_file_contains "CHANGELOG.md" "./scripts/release-readiness.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/release-readiness.sh"
	require_file_contains "CHANGELOG.md" "./scripts/plan-hardening-commits.sh --fail-on-manual"
	require_file_contains "CHANGELOG.en.md" "./scripts/plan-hardening-commits.sh --fail-on-manual"
	require_file_contains "CHANGELOG.md" "$artifact_verify_cmd"
	require_file_contains "CHANGELOG.en.md" "$artifact_verify_cmd"
	print_kv "checklist" "release commands present in CHANGELOG.md and CHANGELOG.en.md"
fi

print_kv "status" "release readiness summary completed"
