#!/usr/bin/env bash

set -euo pipefail

BASE_REF="master"
ALLOW_DIRTY=0
ALLOW_POST_VALIDATION_CHANGES=0
CHECK_CHECKLIST=1
COMMIT_MESSAGE_TMPDIR=""

cleanup() {
	if [[ -n "${COMMIT_MESSAGE_TMPDIR:-}" ]]; then
		rm -rf -- "$COMMIT_MESSAGE_TMPDIR"
	fi
}

trap cleanup EXIT

usage() {
	cat <<'EOF'
Usage: scripts/release-readiness.sh [--base REF] [--allow-dirty] [--allow-post-validation-changes] [--skip-checklist]

Print a read-only release readiness summary for the current branch.

Options:
  --base REF                      Compare the current branch against REF. Defaults to master.
  --allow-dirty                   Print a draft summary even when the worktree is dirty.
  --allow-post-validation-changes  Allow non-release-documentation changes after the recorded validation target.
  --skip-checklist                Skip release checklist and release-note command assertions.
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
		--allow-post-validation-changes)
			ALLOW_POST_VALIDATION_CHANGES=1
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

check_support_routes() {
	local webdav_report_url="https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml"

	require_file_contains "SUPPORT.md" "$webdav_report_url"
	require_file_contains "SUPPORT.en.md" "$webdav_report_url"
}

check_issue_template_config() {
	require_file_contains ".github/ISSUE_TEMPLATE/config.yml" "https://github.com/seanbao/mnemonas/security/policy"
	require_file_contains ".github/ISSUE_TEMPLATE/config.yml" "https://github.com/seanbao/mnemonas/blob/master/SUPPORT.md"
}

check_pull_request_template() {
	local path=".github/pull_request_template.md"
	local expected_sections=(
		"## Scope / 范围"
		"## User-Visible Behavior / 用户可见行为"
		"## Data, Security, And Deployment Impact / 数据、安全与部署影响"
		"## Validation / 验证"
		"## Residual Risk / 残余风险"
	)
	local expected_commands=(
		"make verify-changed"
		"make docs-check"
		"make scripts-check"
	)
	local expected

	for expected in "${expected_sections[@]}"; do
		require_file_contains "$path" "$expected"
	done
	for expected in "${expected_commands[@]}"; do
		require_file_contains "$path" "$expected"
	done
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
		".github/ISSUE_TEMPLATE/webdav_compatibility.yml"
		".github/pull_request_template.md"
	)

	for path in "${required_files[@]}"; do
		require_community_file "$path"
	done

	check_support_routes
	check_issue_template_config
	check_pull_request_template

	print_kv "community" "required community health files and collaboration routes present"
}

extract_validation_target() {
	local path="$1"

	sed -nE 's/.*(validation target|验证目标)[^0-9a-fA-F]+([0-9a-fA-F]{7,40}).*/\2/p' "$path" \
		| head -n 1 \
		| tr '[:upper:]' '[:lower:]'
}

is_validation_evidence_path() {
	case "$1" in
		docs/hardening-progress.md|\
		docs/hardening-progress.en.md|\
		docs/hardening-review-summary.md|\
		docs/hardening-review-summary.en.md)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

is_release_documentation_path() {
	case "$1" in
		CHANGELOG.md|\
		CHANGELOG.en.md|\
		docs/release-notes.md|\
		docs/release-notes.en.md)
			return 0
			;;
		*)
			is_validation_evidence_path "$1"
			;;
	esac
}

check_validation_evidence() {
	local path
	local path_target
	local target=""
	local evidence_files=(
		"docs/hardening-progress.md"
		"docs/hardening-progress.en.md"
		"docs/hardening-review-summary.md"
		"docs/hardening-review-summary.en.md"
	)

	for path in "${evidence_files[@]}"; do
		[[ -f "$path" ]] || fail "missing validation evidence file: $path"
		path_target="$(extract_validation_target "$path")"
		[[ -n "$path_target" ]] || fail "validation evidence target not recorded in: $path"
		if [[ -z "$target" ]]; then
			target="$path_target"
			continue
		fi
		[[ "$path_target" == "$target" ]] || fail "validation evidence target mismatch: $path records $path_target, expected $target"
	done

	if [[ -z "$target" ]]; then
		print_kv "validation" "full gate evidence target not recorded"
		return
	fi

	if ! git rev-parse --verify --quiet "$target^{commit}" >/dev/null; then
		fail "validation evidence target does not resolve: $target"
	fi

	local target_full
	local target_short
	local head_full
	target_full="$(git rev-parse "$target^{commit}")"
	target_short="$(git rev-parse --short=12 "$target_full")"
	head_full="$(git rev-parse HEAD)"

	if ! git merge-base --is-ancestor "$target_full" HEAD; then
		fail "validation evidence target is not an ancestor of HEAD: $target_short"
	fi

	if [[ "$target_full" == "$head_full" ]]; then
		print_kv "validation" "full gate evidence matches HEAD ($target_short)"
		return
	fi

	local commits_since
	local files_since
	local since_shortstat
	local evidence_only=1
	local release_docs_only=1
	commits_since="$(git rev-list --count "$target_full..HEAD")"
	files_since="$(git diff --name-only "$target_full..HEAD" | wc -l | tr -d '[:space:]')"
	since_shortstat="$(git diff --shortstat "$target_full..HEAD")"
	[[ -n "$since_shortstat" ]] || since_shortstat="no file changes"
	while IFS= read -r path; do
		[[ -n "$path" ]] || continue
		if ! is_validation_evidence_path "$path"; then
			evidence_only=0
		fi
		if ! is_release_documentation_path "$path"; then
			release_docs_only=0
		fi
	done < <(git diff --name-only "$target_full..HEAD")

	if [[ "$files_since" != "0" && "$evidence_only" -eq 1 ]]; then
		print_kv "validation" "full gate evidence at $target_short; only validation evidence docs changed since target ($commits_since commits, $files_since files)"
	elif [[ "$files_since" != "0" && "$release_docs_only" -eq 1 ]]; then
		print_kv "validation" "full gate evidence at $target_short; only release documentation changed since target ($commits_since commits, $files_since files)"
	else
		print_kv "validation" "full gate evidence at $target_short; $commits_since commits and $files_since files changed since target"
	fi
	print_kv "validation-diff" "$since_shortstat"

	if [[ "$files_since" != "0" && "$release_docs_only" -eq 0 && "$ALLOW_POST_VALIDATION_CHANGES" -eq 0 ]]; then
		fail "non-release-documentation changes exist after validation target $target_short; rerun full branch validation or pass --allow-post-validation-changes for a draft summary"
	fi
}

check_release_notes() {
	local path
	local expected
	local release_note_files=(
		"docs/release-notes.md"
		"docs/release-notes.en.md"
	)
	local required_texts=(
		"GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master"
		"make docs-check"
		"make scripts-check"
		"./scripts/test-release-tag.sh"
		"./scripts/test-release-package.sh"
		"./scripts/test-release-artifacts.sh"
		"gh release download"
		"./scripts/verify-release-artifacts.sh"
		"--require-targets"
		"--check-image"
	)

	for path in "${release_note_files[@]}"; do
		[[ -f "$path" ]] || fail "missing required release-notes file: $path"
		for expected in "${required_texts[@]}"; do
			require_file_contains "$path" "$expected"
		done
	done

	print_kv "release-notes" "release-note verification commands present"
}

check_branch_commit_messages() {
	local commit
	local subject
	local output
	local commits_checked=0
	local message_file

	COMMIT_MESSAGE_TMPDIR="$(mktemp -d)"
	message_file="$COMMIT_MESSAGE_TMPDIR/message"

	while IFS= read -r commit; do
		[[ -n "$commit" ]] || continue
		subject="$(git log -1 --format=%s "$commit")"
		case "$subject" in
			fixup!\ *|squash!\ *)
				fail "temporary autosquash commit remains on release branch: $(git rev-parse --short=12 "$commit") $subject"
				;;
		esac
		git log -1 --format=%B "$commit" >"$message_file"
		if ! output="$(./scripts/check-commit-message.sh "$message_file" 2>&1)"; then
			printf '%s\n' "$output" >&2
			fail "commit message does not follow project convention: $(git rev-parse --short=12 "$commit") $subject"
		fi
		commits_checked=$((commits_checked + 1))
	done < <(git rev-list --reverse "$BASE_REF..HEAD")

	if [[ "$commits_checked" -eq 0 ]]; then
		print_kv "commit-messages" "no branch commits to check"
	else
		print_kv "commit-messages" "$commits_checked commit subject(s) follow Conventional Commits; no temporary autosquash commits"
	fi
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

check_branch_commit_messages
check_community_files
check_validation_evidence

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
	check_release_notes
	print_kv "checklist" "release commands present in CHANGELOG.md and CHANGELOG.en.md"
fi

print_kv "status" "release readiness summary completed"
