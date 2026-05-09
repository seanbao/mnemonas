#!/usr/bin/env bash

set -euo pipefail

BASE_REF="master"
ALLOW_DIRTY=0
ALLOW_POST_VALIDATION_CHANGES=0
CHECK_CHECKLIST=1
COMMIT_MESSAGE_TMPDIR=""
VALIDATION_TARGET_SHORT=""

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
  --allow-post-validation-changes  Allow non-release-documentation changes after the recorded validation target for a draft summary.
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

require_workflow_job_contains() {
	local path="$1"
	local job="$2"
	local expected="$3"
	local job_block

	job_block="$(awk -v job="$job" '
		$0 == "  " job ":" {
			in_job = 1
			found = 1
			print
			next
		}
		in_job && $0 ~ /^  [A-Za-z0-9_-]+:/ {
			exit
		}
		in_job {
			print
		}
		END {
			if (!found) {
				exit 1
			}
		}
	' "$path")" || fail "$path is missing required job: $job"

	grep -Fq -- "$expected" <<<"$job_block" || {
		printf 'release-readiness: %s job %s is missing required text:\n%s\n' "$path" "$job" "$expected" >&2
		exit 1
	}
}

check_support_routes() {
	local webdav_report_url="https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml"

	require_file_contains "SUPPORT.md" "$webdav_report_url"
	require_file_contains "SUPPORT.en.md" "$webdav_report_url"
	require_file_contains "SUPPORT.md" "[SECURITY.zh-CN.md](SECURITY.zh-CN.md)"
	require_file_contains "SUPPORT.en.md" "[SECURITY.md](SECURITY.md)"
	require_file_contains "SUPPORT.md" "不要公开提交漏洞细节"
	require_file_contains "SUPPORT.en.md" "Do not post exploit details publicly"
}

check_security_policy() {
	require_file_contains "SECURITY.md" "**DO NOT** open a public GitHub issue for security vulnerabilities."
	require_file_contains "SECURITY.md" "Use GitHub's **Private vulnerability reporting** feature"
	require_file_contains "SECURITY.md" "A dedicated security email should only be added here after the mailbox is configured and monitored."
	require_file_contains "SECURITY.md" "Dataplane gRPC/HTTP ports \`9090/9091\` should not be exposed to public or untrusted networks"
	require_file_contains "SECURITY.md" "make security-check NPM_AUDIT=1"
	require_file_contains "SECURITY.md" "MnemoNAS is not designed for direct internet exposure without a hardened proxy/VPN layer"

	require_file_contains "SECURITY.zh-CN.md" "**不要**为安全漏洞创建公开 GitHub Issue。"
	require_file_contains "SECURITY.zh-CN.md" "优先使用本仓库的 GitHub **Private vulnerability reporting** 功能。"
	require_file_contains "SECURITY.zh-CN.md" "只有在专用安全邮箱已经配置并持续监控后，才应把邮箱地址加入本文件。"
	require_file_contains "SECURITY.zh-CN.md" "dataplane gRPC/HTTP 端口 \`9090/9091\` 不应暴露到公网或不可信网络"
	require_file_contains "SECURITY.zh-CN.md" "make security-check NPM_AUDIT=1"
	require_file_contains "SECURITY.zh-CN.md" "不建议在没有加固代理/VPN 的情况下直接暴露到公网"
}

check_issue_template_config() {
	require_file_contains ".github/ISSUE_TEMPLATE/config.yml" "blank_issues_enabled: false"
	require_file_contains ".github/ISSUE_TEMPLATE/config.yml" "https://github.com/seanbao/mnemonas/security/policy"
	require_file_contains ".github/ISSUE_TEMPLATE/config.yml" "https://github.com/seanbao/mnemonas/blob/master/SUPPORT.md"
}

dependabot_has_update() {
	local ecosystem="$1"
	local directory="$2"
	local path=".github/dependabot.yml"

	awk -v expected_ecosystem="$ecosystem" -v expected_directory="$directory" '
		function clean_scalar(line, value) {
			value = line
			sub(/^[^:]*:[[:space:]]*/, "", value)
			sub(/[[:space:]]+#.*$/, "", value)
			gsub(/"/, "", value)
			gsub(/\047/, "", value)
			sub(/^[[:space:]]+/, "", value)
			sub(/[[:space:]]+$/, "", value)
			return value
		}

		function flush_update() {
			if (in_update && ecosystem == expected_ecosystem && directory == expected_directory) {
				found = 1
			}
		}

		/^[[:space:]]*-[[:space:]]*package-ecosystem:[[:space:]]*/ {
			flush_update()
			in_update = 1
			ecosystem = clean_scalar($0)
			directory = ""
			next
		}

		in_update && /^[[:space:]]*directory:[[:space:]]*/ {
			directory = clean_scalar($0)
			next
		}

		END {
			flush_update()
			exit(found ? 0 : 1)
		}
	' "$path"
}

require_dependabot_update() {
	local ecosystem="$1"
	local directory="$2"

	if ! dependabot_has_update "$ecosystem" "$directory"; then
		fail "missing required Dependabot update: $ecosystem $directory"
	fi
}

check_dependabot_config() {
	require_file_contains ".github/dependabot.yml" "version: 2"
	require_file_contains ".github/dependabot.yml" "updates:"

	require_dependabot_update "gomod" "/"
	require_dependabot_update "cargo" "/dataplane"
	require_dependabot_update "cargo" "/tools/proto-gen"
	require_dependabot_update "npm" "/web"
	require_dependabot_update "github-actions" "/"
	require_dependabot_update "docker" "/"
}

check_torture_workflow() {
	local path=".github/workflows/torture.yml"

	require_file_contains "$path" "workflow_dispatch:"
	require_file_contains "$path" "schedule:"
	require_file_contains "$path" "cron:"
	require_file_contains "$path" "permissions:"
	require_file_contains "$path" "contents: read"
	require_file_contains "$path" "RUN_LIVE_FAULTS: '0'"
	require_file_contains "$path" "run: make test-torture"
}

check_ci_workflow() {
	local path=".github/workflows/ci.yml"

	require_file_contains "$path" "pull_request:"
	require_file_contains "$path" "permissions:"
	require_file_contains "$path" "contents: read"
	require_file_contains "$path" "persist-credentials: false"
	require_file_contains "$path" "run: make workflows-check"
	require_file_contains "$path" "run: make scripts-check"
	require_file_contains "$path" "run: make docs-check"
	require_file_contains "$path" "run: make toolchains-check"
	require_file_contains "$path" "go test -v -race -coverprofile=coverage.out"
	require_file_contains "$path" "npm audit --audit-level=\"\${{ env.NPM_AUDIT_LEVEL }}\""
	require_file_contains "$path" "run: npm run test:e2e"
	require_file_contains "$path" "run: ./scripts/docker-smoke.sh mnemonas:test"
}

check_release_workflow() {
	local path=".github/workflows/release.yml"

	require_file_contains "$path" "- 'v*'"
	require_file_contains "$path" "permissions:"
	require_file_contains "$path" "contents: read"
	require_file_contains "$path" "packages: write"
	require_file_contains "$path" "contents: write"
	require_file_contains "$path" "persist-credentials: false"
	require_file_contains "$path" "run: ./scripts/check-release-tag.sh \"\$GITHUB_REF_NAME\""
	require_file_contains "$path" "run: ./scripts/docker-smoke.sh mnemonas:release-smoke"
	require_file_contains "$path" "./scripts/verify-release-artifacts.sh \\"
	require_file_contains "$path" "--require-targets"
	require_file_contains "$path" "uses: softprops/action-gh-release@v2"
	require_file_contains "$path" "prerelease: \${{ contains(github.ref_name, '-') }}"
	require_workflow_job_contains "$path" "release" "packages: read"
	require_workflow_job_contains "$path" "release" "uses: docker/login-action@v3"
	require_workflow_job_contains "$path" "release" "--check-image"
}

check_makefile_targets() {
	local path="Makefile"

	require_file_contains "$path" "GO_TEST_TIMEOUT ?= 20m"
	require_file_contains "$path" "go-packages:"
	require_file_contains "$path" "workflows-check:"
	require_file_contains "$path" "scripts-check:"
	require_file_contains "$path" "toolchains-check:"
	require_file_contains "$path" "docs-check:"
	require_file_contains "$path" "security-check:"
	require_file_contains "$path" "test:"
	require_file_contains "$path" "test-torture:"
	require_file_contains "$path" "./scripts/torture-test.sh"
	require_file_contains "$path" "docker-check: docker docker-smoke"
	require_file_contains "$path" "check: workflows-check scripts-check toolchains-check docs-check lint test"
	require_file_contains "$path" "verify-changed:"
	require_file_contains "$path" "./scripts/verify-changed.sh"
	require_file_contains "$path" "quick-check:"
}

check_issue_templates() {
	require_file_contains ".github/ISSUE_TEMPLATE/bug_report.yml" "Sensitive values such as passwords, tokens, cookies, private URLs, and internal addresses must be removed before posting logs."
	require_file_contains ".github/ISSUE_TEMPLATE/bug_report.yml" "Relevant sanitized logs, \`mnemonas-doctor\`, Docker preflight, browser console output, screenshots, or request IDs."
	require_file_contains ".github/ISSUE_TEMPLATE/bug_report.yml" "Security-sensitive exploit details are not posted publicly."

	require_file_contains ".github/ISSUE_TEMPLATE/feature_request.yml" "Security, data, deployment, and compatibility implications should be called out explicitly."
	require_file_contains ".github/ISSUE_TEMPLATE/feature_request.yml" "Data migration, security, deployment, performance, or client-compatibility concerns."

	require_file_contains ".github/ISSUE_TEMPLATE/question.yml" "Remove passwords, tokens, cookies, private URLs, internal addresses, and private file names before posting logs or configuration snippets."
	require_file_contains ".github/ISSUE_TEMPLATE/question.yml" "Sanitized command output, logs, screenshots, or configuration excerpts."
	require_file_contains ".github/ISSUE_TEMPLATE/question.yml" "Logs and configuration snippets are sanitized."

	require_file_contains ".github/ISSUE_TEMPLATE/webdav_compatibility.yml" "Remove passwords, tokens, cookies, private URLs, internal addresses, and private file names before posting logs or screenshots."
	require_file_contains ".github/ISSUE_TEMPLATE/webdav_compatibility.yml" "Sanitized \`mnemonas-doctor\`, client logs, server logs, request IDs, screenshots, or diagnostic bundle notes."
	require_file_contains ".github/ISSUE_TEMPLATE/webdav_compatibility.yml" "Security-sensitive exploit details are not posted publicly."
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
		"Makefile"
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
		".github/dependabot.yml"
		".github/ISSUE_TEMPLATE/config.yml"
		".github/ISSUE_TEMPLATE/bug_report.yml"
		".github/ISSUE_TEMPLATE/feature_request.yml"
		".github/ISSUE_TEMPLATE/question.yml"
		".github/ISSUE_TEMPLATE/webdav_compatibility.yml"
		".github/pull_request_template.md"
		".github/workflows/ci.yml"
		".github/workflows/release.yml"
		".github/workflows/torture.yml"
	)

	for path in "${required_files[@]}"; do
		require_community_file "$path"
	done

	check_support_routes
	check_security_policy
	check_dependabot_config
	check_ci_workflow
	check_release_workflow
	check_torture_workflow
	check_makefile_targets
	check_issue_template_config
	check_issue_templates
	check_pull_request_template

	print_kv "community" "required community health files, dependency-update baseline, CI/release/torture workflow and Makefile target baselines, support/security routes, and issue template safety guidance present"
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
	local path_target_full
	local target=""
	local target_full=""
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
		if ! git rev-parse --verify --quiet "$path_target^{commit}" >/dev/null; then
			fail "validation evidence target does not resolve: $path records $path_target"
		fi
		path_target_full="$(git rev-parse "$path_target^{commit}")"
		if [[ -z "$target" ]]; then
			target="$path_target"
			target_full="$path_target_full"
			continue
		fi
		[[ "$path_target_full" == "$target_full" ]] || fail "validation evidence target mismatch: $path records $path_target, expected $target"
	done

	if [[ -z "$target" ]]; then
		print_kv "validation" "full gate evidence target not recorded"
		return
	fi

	local target_short
	local head_full
	target_short="$(git rev-parse --short=12 "$target_full")"
	head_full="$(git rev-parse HEAD)"
	VALIDATION_TARGET_SHORT="$target_short"

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
	if [[ "$files_since" != "0" && "$release_docs_only" -eq 0 && "$ALLOW_POST_VALIDATION_CHANGES" -eq 1 ]]; then
		print_kv "validation-warning" "draft override allowed non-release-documentation changes after validation target $target_short; rerun full branch validation before release"
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
		"make security-check NPM_AUDIT=1"
		"make docker-check"
		"mnemonas-doctor --public-domain"
		"./scripts/public-go-live-smoke.sh"
		"./scripts/backup-restore-drill-smoke.sh"
		"cloud-firewall-checklist"
		"./scripts/test-release-tag.sh"
		"./scripts/test-release-package.sh"
		"./scripts/test-release-artifacts.sh"
		"./scripts/verify-published-release.sh"
		"--version <tag>"
		"--repository seanbao/mnemonas"
	)

	for path in "${release_note_files[@]}"; do
		[[ -f "$path" ]] || fail "missing required release-notes file: $path"
		if [[ -n "${VALIDATION_TARGET_SHORT:-}" ]]; then
			require_file_contains "$path" "$VALIDATION_TARGET_SHORT"
		fi
		for expected in "${required_texts[@]}"; do
			require_file_contains "$path" "$expected"
		done
	done
	require_file_contains "docs/release-notes.md" "L1 私有文件云盘"
	require_file_contains "docs/release-notes.md" "不应作为重要数据的唯一长期副本"
	require_file_contains "docs/release-notes.md" "外部备份"
	require_file_contains "docs/release-notes.en.md" "L1 private file cloud"
	require_file_contains "docs/release-notes.en.md" "not as the only long-term copy of important data"
	require_file_contains "docs/release-notes.en.md" "external backups"

	print_kv "release-notes" "release-note validation target and verification commands present"
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

base_full="$(git rev-parse "$BASE_REF^{commit}")"
base_short="$(git rev-parse --short=12 "$base_full")"
if ! git merge-base --is-ancestor "$base_full" HEAD; then
	fail "base ref is not an ancestor of HEAD: $BASE_REF ($base_short)"
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
	artifact_verify_cmd="./scripts/verify-published-release.sh --version <tag> --repository seanbao/mnemonas"

	require_file_contains "CHANGELOG.md" "$verify_changed_cmd"
	require_file_contains "CHANGELOG.en.md" "$verify_changed_cmd"
	require_file_contains "CHANGELOG.md" "make docs-check"
	require_file_contains "CHANGELOG.en.md" "make docs-check"
	require_file_contains "CHANGELOG.md" "make scripts-check"
	require_file_contains "CHANGELOG.en.md" "make scripts-check"
	require_file_contains "CHANGELOG.md" "make security-check NPM_AUDIT=1"
	require_file_contains "CHANGELOG.en.md" "make security-check NPM_AUDIT=1"
	require_file_contains "CHANGELOG.md" "make docker-check"
	require_file_contains "CHANGELOG.en.md" "make docker-check"
	require_file_contains "CHANGELOG.md" "mnemonas-doctor --public-domain"
	require_file_contains "CHANGELOG.en.md" "mnemonas-doctor --public-domain"
	require_file_contains "CHANGELOG.md" "./scripts/public-go-live-smoke.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/public-go-live-smoke.sh"
	require_file_contains "CHANGELOG.md" "./scripts/backup-restore-drill-smoke.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/backup-restore-drill-smoke.sh"
	require_file_contains "CHANGELOG.md" "cloud-firewall-checklist"
	require_file_contains "CHANGELOG.en.md" "cloud-firewall-checklist"
	require_file_contains "CHANGELOG.md" "./scripts/release-readiness.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/release-readiness.sh"
	require_file_contains "CHANGELOG.md" "./scripts/plan-hardening-commits.sh --fail-on-manual"
	require_file_contains "CHANGELOG.en.md" "./scripts/plan-hardening-commits.sh --fail-on-manual"
	require_file_contains "CHANGELOG.md" "./scripts/check-release-tag.sh <tag>"
	require_file_contains "CHANGELOG.en.md" "./scripts/check-release-tag.sh <tag>"
	require_file_contains "CHANGELOG.md" "./scripts/test-release-tag.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/test-release-tag.sh"
	require_file_contains "CHANGELOG.md" "./scripts/test-release-package.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/test-release-package.sh"
	require_file_contains "CHANGELOG.md" "./scripts/test-release-artifacts.sh"
	require_file_contains "CHANGELOG.en.md" "./scripts/test-release-artifacts.sh"
	require_file_contains "CHANGELOG.md" "$artifact_verify_cmd"
	require_file_contains "CHANGELOG.en.md" "$artifact_verify_cmd"
	check_release_notes
	print_kv "checklist" "release commands present in CHANGELOG.md and CHANGELOG.en.md"
fi

print_kv "status" "release readiness summary completed"
