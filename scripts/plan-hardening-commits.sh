#!/usr/bin/env bash

set -euo pipefail

FORMAT="text"
FAIL_ON_MANUAL=0
SELECTED_GROUP=""

usage() {
	cat <<'EOF'
Usage: scripts/plan-hardening-commits.sh [--commands|--checks|--messages] [--fail-on-manual] [--group GROUP]

Print a read-only grouping plan for the current hardening worktree.

Options:
  --commands        Print grouped git-add command blocks instead of file lists.
  --checks          Print suggested validation commands for each group.
  --messages        Print suggested Conventional Commit messages for each group.
  --fail-on-manual  Exit non-zero when paths fall into review(manual).
  --group GROUP     Print only one group. Valid groups:
                    docs, build-ci, feat-api, feat-core, feat-web,
                    build-docker-deploy, build-dataplane, review-manual.
  -h, --help        Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--commands)
			FORMAT="commands"
			shift
			;;
		--checks)
			FORMAT="checks"
			shift
			;;
		--messages)
			FORMAT="messages"
			shift
			;;
		--fail-on-manual)
			FAIL_ON_MANUAL=1
			shift
			;;
		--group)
			[[ $# -ge 2 ]] || {
				printf 'plan-hardening-commits: --group requires a group id\n' >&2
				exit 2
			}
			SELECTED_GROUP="$2"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf 'plan-hardening-commits: unknown argument: %s\n' "$1" >&2
			exit 2
			;;
	esac
done

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	printf 'plan-hardening-commits: must run inside a git repository\n' >&2
	exit 1
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

declare -a GROUP_IDS=(
	"docs"
	"build-ci"
	"feat-api"
	"feat-core"
	"feat-web"
	"build-docker-deploy"
	"build-dataplane"
	"review-manual"
)

declare -A GROUP_TITLES=(
	["docs"]="docs: documentation compaction and bilingual index"
	["build-ci"]="build(ci): local and CI gates"
	["feat-api"]="feat(api): path, archive, WebDAV, share, and access boundaries"
	["feat-core"]="feat(core): auth, backup, storage, workspace, and runtime boundaries"
	["feat-web"]="feat(web): visible frontend experience and client contracts"
	["build-docker-deploy"]="build(docker-deploy): containers, deployment, and public entry"
	["build-dataplane"]="build(dataplane): Rust and proto-generator baseline"
	["review-manual"]="review(manual): paths that need manual grouping"
)

declare -A GROUP_VALIDATION=(
	["docs"]="make docs-check; ./scripts/check-yaml-configs.sh .github/ISSUE_TEMPLATE/*.yml .github/ISSUE_TEMPLATE/*.yaml; git diff --check"
	["build-ci"]="make scripts-check; ./scripts/verify-changed.sh --dry-run; ./scripts/check-secret-leaks.sh"
	["feat-api"]="Go race tests from make verify-changed; path, archive, WebDAV, share, and access-rule tests"
	["feat-core"]="Go race tests from make verify-changed; backup restore, quota, workspace, and storage-boundary tests"
	["feat-web"]="Frontend lint, typecheck, unit tests, build, and Playwright E2E"
	["build-docker-deploy"]="Docker build and smoke; public-access, reverse-proxy, systemd, release-package, and release-artifact regressions"
	["build-dataplane"]="Rust fmt, test, and clippy; proto generator tests and clippy"
	["review-manual"]="Inspect manually, assign to a group, then rerun this planner"
)

declare -A GROUP_MESSAGES=(
	["docs"]="docs: streamline bilingual documentation"
	["build-ci"]="build(ci): harden validation gates"
	["feat-api"]="feat(api): harden access and sharing boundaries"
	["feat-core"]="feat(core): harden auth and storage boundaries"
	["feat-web"]="feat(web): harden visible workflows"
	["build-docker-deploy"]="build(docker-deploy): harden deployment paths"
	["build-dataplane"]="build(dataplane): harden Rust validation"
	["review-manual"]="chore(review): classify remaining hardening paths"
)

is_known_group() {
	local candidate="$1"
	local group

	for group in "${GROUP_IDS[@]}"; do
		if [[ "$group" == "$candidate" ]]; then
			return 0
		fi
	done

	return 1
}

if [[ -n "$SELECTED_GROUP" ]] && ! is_known_group "$SELECTED_GROUP"; then
	printf 'plan-hardening-commits: unknown group: %s\n' "$SELECTED_GROUP" >&2
	printf 'plan-hardening-commits: run with --help to list valid groups\n' >&2
	exit 2
fi

declare -A FILE_SEEN=()
declare -A GROUP_COUNTS=()
declare -A GROUP_FILES=()
declare -A GROUP_RAW_FILES=()
total_files=0

add_file() {
	local file="$1"
	[[ -n "$file" ]] || return 0
	[[ -z "${FILE_SEEN[$file]+x}" ]] || return 0
	FILE_SEEN["$file"]=1
	total_files=$((total_files + 1))
	classify_file "$file"
}

append_to_group() {
	local group="$1"
	local file="$2"
	local quoted

	GROUP_COUNTS["$group"]=$(( ${GROUP_COUNTS[$group]:-0} + 1 ))
	printf -v quoted '%q' "$file"
	GROUP_FILES["$group"]+="$quoted"$'\n'
	GROUP_RAW_FILES["$group"]+="$file"$'\n'
}

classify_file() {
	local file="$1"

	case "$file" in
		README.md|README.en.md|CHANGELOG.md|CHANGELOG.en.md|CODE_OF_CONDUCT.md|CODE_OF_CONDUCT.zh-CN.md|CONTRIBUTING.md|CONTRIBUTING.en.md|SECURITY.md|SECURITY.zh-CN.md|SUPPORT.md|SUPPORT.en.md|.github/copilot-instructions.md|.github/pull_request_template.md|.github/ISSUE_TEMPLATE/*|docs/*|web/README.md|web/README.en.md|deploy/public-access/README.md|deploy/public-access/README.en.md)
			append_to_group "docs" "$file"
			return 0
			;;
		dataplane/*|tools/proto-gen/*)
			append_to_group "build-dataplane" "$file"
			return 0
			;;
		web/src/*|web/e2e/*|web/package.json|web/package-lock.json|web/playwright.config.*|web/vitest.config.*|web/tsconfig*.json)
			append_to_group "feat-web" "$file"
			return 0
			;;
		internal/api/*|internal/share/*|internal/webdav/*)
			append_to_group "feat-api" "$file"
			return 0
			;;
		cmd/*|go.mod|go.sum|internal/activity/*|internal/alerts/*|internal/auth/*|internal/backup/*|internal/caslayout/*|internal/config/*|internal/diskhealth/*|internal/favorites/*|internal/smbcred/*|internal/smbgateway/*|internal/storage/*|internal/thumbnail/*|internal/versionstore/*|internal/workspace/*)
			append_to_group "feat-core" "$file"
			return 0
			;;
		Dockerfile|.dockerignore|docker-compose.yml|docker-compose.yaml|mnemonas.example.toml|deploy/public-access/*|scripts/install-systemd.sh|scripts/uninstall-systemd.sh|scripts/mnemonas-doctor.sh|scripts/mnemonas-docker-preflight.sh|scripts/docker-quickstart.sh|scripts/docker-smoke.sh|scripts/docker-start.sh|scripts/setup-reverse-proxy.sh|scripts/public-go-live-smoke.sh|scripts/mnemonas-dataplane-start.sh|scripts/verify-release-artifacts.sh|scripts/release-readiness.sh|scripts/dev.sh|scripts/benchmark.sh|scripts/test-systemd-*.sh|scripts/test-docker-*.sh|scripts/test-dataplane-start.sh|scripts/test-dev-safety.sh|scripts/test-reverse-proxy-safety.sh|scripts/test-public-access-templates.sh|scripts/test-public-go-live-smoke.sh|scripts/test-release-package.sh|scripts/test-release-artifacts.sh|scripts/test-release-readiness.sh|scripts/test-benchmark-safety.sh)
			append_to_group "build-docker-deploy" "$file"
			return 0
			;;
		.github/workflows/*|Makefile|scripts/check-*.sh|scripts/test-*.sh|scripts/verify-changed.sh|scripts/plan-hardening-commits.sh|scripts/e2e-test.sh|scripts/fault-injection-test.sh|scripts/torture-test.sh|scripts/run-*.sh|scripts/webdav-client-smoke.sh|scripts/with-test-dataplane.sh|web/scripts/*|web/.husky/*)
			append_to_group "build-ci" "$file"
			return 0
			;;
	esac

	append_to_group "review-manual" "$file"
}

collect_changed_files() {
	if git rev-parse --verify HEAD >/dev/null 2>&1; then
		while IFS= read -r -d '' file; do
			add_file "$file"
		done < <(git diff --name-only -z --diff-filter=ACMRDT HEAD --)
	else
		while IFS= read -r -d '' file; do
			add_file "$file"
		done < <(git ls-files -z)
	fi

	while IFS= read -r -d '' file; do
		add_file "$file"
	done < <(git ls-files --others --exclude-standard -z)
}

print_file_list() {
	local group="$1"
	local count="${GROUP_COUNTS[$group]:-0}"
	(( count > 0 )) || return 0

	printf '\n## %s (%d file(s))\n' "${GROUP_TITLES[$group]}" "$count"
	printf 'Validation: %s\n' "${GROUP_VALIDATION[$group]}"
	while IFS= read -r file; do
		[[ -n "$file" ]] || continue
		printf '  %s\n' "$file"
	done <<<"${GROUP_FILES[$group]}"
}

print_command_block() {
	local group="$1"
	local count="${GROUP_COUNTS[$group]:-0}"
	local first=1
	(( count > 0 )) || return 0

	printf '\n# %s (%d file(s))\n' "${GROUP_TITLES[$group]}" "$count"
	printf '# Validation: %s\n' "${GROUP_VALIDATION[$group]}"
	printf 'git add --'
	while IFS= read -r file; do
		[[ -n "$file" ]] || continue
		if [[ "$first" == "1" ]]; then
			printf ' \\\n  %s' "$file"
			first=0
		else
			printf ' \\\n  %s' "$file"
		fi
	done <<<"${GROUP_FILES[$group]}"
	printf '\n'
}

print_group_diff_check() {
	local group="$1"
	local first=1

	printf 'git diff --check --'
	while IFS= read -r file; do
		[[ -n "$file" ]] || continue
		if [[ "$first" == "1" ]]; then
			printf ' \\\n  %s' "$file"
			first=0
		else
			printf ' \\\n  %s' "$file"
		fi
	done <<<"${GROUP_FILES[$group]}"
	printf '\n'
}

group_has_file_pattern() {
	local group="$1"
	local pattern="$2"
	local file

	while IFS= read -r file; do
		[[ -n "$file" ]] || continue
		# shellcheck disable=SC2254 # pattern is an intentional glob selector.
		case "$file" in
			$pattern)
				return 0
				;;
		esac
	done <<<"${GROUP_RAW_FILES[$group]:-}"

	return 1
}

print_dependency_checks() {
	local group="$1"

	if group_has_file_pattern "$group" "web/package.json" || group_has_file_pattern "$group" "web/package-lock.json"; then
		printf 'make security-check NPM_AUDIT=1\n'
		return 0
	fi

	if group_has_file_pattern "$group" "go.mod" \
		|| group_has_file_pattern "$group" "go.sum" \
		|| group_has_file_pattern "$group" "dataplane/Cargo.toml" \
		|| group_has_file_pattern "$group" "dataplane/Cargo.lock" \
		|| group_has_file_pattern "$group" "tools/proto-gen/Cargo.toml" \
		|| group_has_file_pattern "$group" "tools/proto-gen/Cargo.lock"; then
		printf 'make security-check\n'
	fi
}

print_check_block() {
	local group="$1"
	local count="${GROUP_COUNTS[$group]:-0}"
	(( count > 0 )) || return 0

	printf '\n# %s (%d file(s))\n' "${GROUP_TITLES[$group]}" "$count"
	case "$group" in
		docs)
			printf 'make docs-check\n'
			print_group_diff_check "$group"
			;;
		build-ci)
			printf 'make scripts-check\n'
			printf './scripts/verify-changed.sh --dry-run\n'
			printf './scripts/check-secret-leaks.sh\n'
			printf './scripts/plan-hardening-commits.sh --fail-on-manual\n'
			print_group_diff_check "$group"
			;;
		feat-api)
			printf 'GOTOOLCHAIN=local CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -race ./internal/api ./internal/share ./internal/webdav\n'
			print_group_diff_check "$group"
			;;
		feat-core)
			printf 'GOTOOLCHAIN=local CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -race ./cmd/healthcheck ./cmd/nasd ./internal/activity ./internal/alerts ./internal/auth ./internal/backup ./internal/caslayout ./internal/config ./internal/diskhealth ./internal/favorites ./internal/smbcred ./internal/smbgateway ./internal/storage ./internal/thumbnail ./internal/versionstore ./internal/workspace\n'
			print_dependency_checks "$group"
			print_group_diff_check "$group"
			;;
		feat-web)
			printf 'cd web && npm run lint\n'
			printf 'cd web && npm run typecheck\n'
			printf 'cd web && npm run test:run\n'
			printf 'cd web && npm run build\n'
			printf 'cd web && npm run test:e2e\n'
			print_dependency_checks "$group"
			print_group_diff_check "$group"
			;;
		build-docker-deploy)
			printf 'make scripts-check\n'
			printf 'env GOTOOLCHAIN=local go run ./cmd/nasd --check-config --config mnemonas.example.toml\n'
			# shellcheck disable=SC2016 # Keep VERIFY_CHANGED_DOCKER_TIMEOUT for the generated command.
			printf 'if command -v timeout >/dev/null 2>&1; then timeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check; elif command -v gtimeout >/dev/null 2>&1; then gtimeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check; else printf "docker validation requires timeout or gtimeout\\n" >&2; exit 127; fi\n'
			print_group_diff_check "$group"
			;;
		build-dataplane)
			printf 'cd dataplane && cargo fmt --check\n'
			printf 'cd dataplane && cargo test --locked\n'
			printf 'cd dataplane && cargo clippy --all-targets --locked -- -D warnings\n'
			printf 'cargo fmt --manifest-path tools/proto-gen/Cargo.toml --check\n'
			printf 'cargo test --manifest-path tools/proto-gen/Cargo.toml --locked\n'
			printf 'cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings\n'
			print_dependency_checks "$group"
			print_group_diff_check "$group"
			;;
		review-manual)
			printf '# Assign these paths to an existing group, then rerun this planner.\n'
			print_group_diff_check "$group"
			;;
	esac
}

print_message_block() {
	local group="$1"
	local count="${GROUP_COUNTS[$group]:-0}"
	(( count > 0 )) || return 0

	printf '\n# %s (%d file(s))\n' "${GROUP_TITLES[$group]}" "$count"
	printf '%s\n' "${GROUP_MESSAGES[$group]}"
}

collect_changed_files

if (( total_files == 0 )); then
	printf '[hardening-commit-plan] no changed files detected\n'
	exit 0
fi

printf '[hardening-commit-plan] grouped %d changed file(s)\n' "$total_files"

if [[ -n "$SELECTED_GROUP" ]]; then
	if (( ${GROUP_COUNTS[$SELECTED_GROUP]:-0} == 0 )); then
		printf '[hardening-commit-plan] group %s has no changed files\n' "$SELECTED_GROUP"
	else
		printf '[hardening-commit-plan] showing group %s\n' "$SELECTED_GROUP"
	fi
fi

for group in "${GROUP_IDS[@]}"; do
	if [[ -n "$SELECTED_GROUP" && "$group" != "$SELECTED_GROUP" ]]; then
		continue
	fi
	case "$FORMAT" in
		commands)
			print_command_block "$group"
			;;
		checks)
			print_check_block "$group"
			;;
		messages)
			print_message_block "$group"
			;;
		text)
			print_file_list "$group"
			;;
	esac
done

if (( ${GROUP_COUNTS["review-manual"]:-0} > 0 )); then
	printf '\n[hardening-commit-plan] review-manual contains %d path(s); assign them before final commit splitting.\n' "${GROUP_COUNTS["review-manual"]}" >&2
	if [[ "$FAIL_ON_MANUAL" == "1" ]]; then
		exit 1
	fi
fi
