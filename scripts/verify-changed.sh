#!/usr/bin/env bash

set -euo pipefail

DRY_RUN="${VERIFY_CHANGED_DRY_RUN:-0}"
MODE="worktree"
BASE="${VERIFY_CHANGED_BASE:-}"
PRINT_FILES=0

usage() {
	cat <<'EOF'
Usage: scripts/verify-changed.sh [options]

Select validation commands from changed files.

Options:
  --staged       Inspect staged changes only.
  --base REF     Inspect changes from REF...HEAD.
  --dry-run      Print selected commands without running them.
  --list-files   Print changed files before selected commands.
  -h, --help     Show this help.

Environment:
  VERIFY_CHANGED_BASE     Default base ref for --base mode.
  VERIFY_CHANGED_DRY_RUN  Set to 1 to print commands only.
  VERIFY_CHANGED_DOCKER_TIMEOUT
                          Timeout for Docker image builds, default: 45m.
                          Uses timeout, or gtimeout from GNU coreutils.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--staged)
			MODE="staged"
			shift
			;;
		--base)
			[[ $# -ge 2 ]] || {
				printf 'verify-changed: --base requires a ref\n' >&2
				exit 2
			}
			MODE="base"
			BASE="$2"
			shift 2
			;;
		--dry-run)
			DRY_RUN=1
			shift
			;;
		--list-files)
			PRINT_FILES=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf 'verify-changed: unknown argument: %s\n' "$1" >&2
			exit 2
			;;
	esac
done

if [[ -n "$BASE" && "$MODE" == "worktree" ]]; then
	MODE="base"
fi

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	printf 'verify-changed: must run inside a git repository\n' >&2
	exit 1
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

declare -a RAW_FILES=()
declare -A SEEN_FILES=()
declare -a FILES=()
untracked_changed=0

add_file() {
	local file="$1"
	[[ -n "$file" ]] || return 0
	[[ -z "${SEEN_FILES[$file]+x}" ]] || return 0
	SEEN_FILES["$file"]=1
	FILES+=("$file")
}

collect_diff_files() {
	local status
	local file

	while IFS= read -r -d '' status; do
		case "$status" in
			R*|C*)
				IFS= read -r -d '' file || break
				RAW_FILES+=("$file")
				IFS= read -r -d '' file || break
				RAW_FILES+=("$file")
				;;
			*)
				IFS= read -r -d '' file || break
				RAW_FILES+=("$file")
				;;
		esac
	done < <(git diff --name-status -z --diff-filter=ACMRDT "$@")
}

case "$MODE" in
	staged)
		collect_diff_files --cached
		;;
	base)
		[[ -n "$BASE" ]] || {
			printf 'verify-changed: base ref is empty\n' >&2
			exit 2
		}
		collect_diff_files "$BASE"...HEAD
		;;
	worktree)
		if git rev-parse --verify HEAD >/dev/null 2>&1; then
			collect_diff_files HEAD --
		else
			while IFS= read -r -d '' file; do
				RAW_FILES+=("$file")
			done < <(git ls-files -z)
		fi
		while IFS= read -r -d '' file; do
			RAW_FILES+=("$file")
			untracked_changed=1
		done < <(git ls-files --others --exclude-standard -z)
		;;
	*)
		printf 'verify-changed: unsupported mode: %s\n' "$MODE" >&2
		exit 2
		;;
esac

for file in "${RAW_FILES[@]}"; do
	add_file "$file"
done

if [[ "${#FILES[@]}" -eq 0 ]]; then
	printf 'verify-changed: no changed files detected\n'
	exit 0
fi

if [[ "$PRINT_FILES" == "1" ]]; then
	printf 'Changed files:\n'
	printf '  %s\n' "${FILES[@]}"
fi

go_changed=0
rust_changed=0
proto_tool_changed=0
web_changed=0
web_e2e_changed=0
proto_changed=0
scripts_changed=0
workflows_changed=0
docker_changed=0
docker_template_changed=0
docs_changed=0
example_config_changed=0
lint_config_changed=0
yaml_config_changed=0
public_access_template_changed=0
precommit_changed=0
agents_changed=0
makefile_changed=0
toolchain_changed=0
dependency_manifest_changed=0
web_dependency_manifest_changed=0

for file in "${FILES[@]}"; do
	case "$file" in
		*.go|go.mod|go.sum|.go-version)
			go_changed=1
			;;
	esac

	case "$file" in
		*.md|scripts/check-doc-links.sh)
			docs_changed=1
			;;
	esac

	case "$file" in
		mnemonas.example.toml)
			example_config_changed=1
			;;
	esac

	case "$file" in
		.golangci.yml|.golangci.yaml)
			lint_config_changed=1
			;;
	esac

	case "$file" in
		.github/dependabot.yml|.github/dependabot.yaml|codecov.yml|codecov.yaml)
			yaml_config_changed=1
			;;
	esac

	case "$file" in
		deploy/public-access/*)
			public_access_template_changed=1
			;;
	esac

	case "$file" in
		dataplane/*)
			rust_changed=1
			;;
		tools/proto-gen/*)
			proto_tool_changed=1
			;;
		proto/*.proto)
			proto_changed=1
			;;
		web/e2e/*|web/playwright.config.*|web/tsconfig.e2e.json)
			web_changed=1
			web_e2e_changed=1
			;;
		.nvmrc)
			web_changed=1
			;;
		web/*)
			web_changed=1
			;;
	esac

	case "$file" in
		scripts/*.sh|web/scripts/*|web/.husky/*)
			scripts_changed=1
			;;
		.github/workflows/*)
			workflows_changed=1
			;;
		Dockerfile|.dockerignore|docker-compose.yml|docker-compose.yaml|deploy/*docker-compose.yml|deploy/*docker-compose.yaml)
			docker_changed=1
			;;
	esac

	case "$file" in
		.env.example|docker-compose.yml|docker-compose.yaml|deploy/*docker-compose.yml|deploy/*docker-compose.yaml)
			docker_template_changed=1
			;;
	esac

	case "$file" in
		.pre-commit-config.yaml)
			precommit_changed=1
			;;
		.agents/*|AGENTS.md)
			agents_changed=1
			;;
		Makefile)
			makefile_changed=1
			;;
	esac

	case "$file" in
		.go-version|.nvmrc|web/.nvmrc|go.mod|go.sum|dataplane/Cargo.toml|tools/proto-gen/Cargo.toml|Dockerfile|.env.example|docker-compose.yml|docker-compose.yaml|web/package.json|web/package-lock.json|.github/workflows/*.yml|.github/workflows/*.yaml|README.md|README.en.md|docs/development.md|docs/development.en.md|docs/docker-deployment.md|docs/docker-deployment.en.md)
			toolchain_changed=1
			;;
	esac

	case "$file" in
		go.mod|go.sum|dataplane/Cargo.toml|dataplane/Cargo.lock|tools/proto-gen/Cargo.toml|tools/proto-gen/Cargo.lock|web/package.json|web/package-lock.json)
			dependency_manifest_changed=1
			;;
	esac

	case "$file" in
		web/package.json|web/package-lock.json)
			web_dependency_manifest_changed=1
			;;
	esac
done

declare -a COMMAND_LABELS=()
declare -a COMMANDS=()
declare -A SEEN_COMMANDS=()
DIFF_CHECK_COMMAND="git diff --check"
# shellcheck disable=SC2016 # Expand VERIFY_CHANGED_DOCKER_TIMEOUT when the selected command runs.
DOCKER_BUILD_COMMAND='if command -v timeout >/dev/null 2>&1; then timeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check; elif command -v gtimeout >/dev/null 2>&1; then gtimeout "${VERIFY_CHANGED_DOCKER_TIMEOUT:-45m}" make docker-check; else printf "%s\n" "verify-changed: Docker build and smoke validation requires timeout or gtimeout; install GNU coreutils or run make docker-check manually" >&2; exit 127; fi'

case "$MODE" in
	staged)
		DIFF_CHECK_COMMAND="git diff --cached --check"
		;;
	base)
		printf -v QUOTED_BASE '%q' "$BASE"
		DIFF_CHECK_COMMAND="git diff --check ${QUOTED_BASE}...HEAD"
		;;
esac

add_command() {
	local label="$1"
	local command="$2"
	[[ -z "${SEEN_COMMANDS[$command]+x}" ]] || return 0
	SEEN_COMMANDS["$command"]=1
	COMMAND_LABELS+=("$label")
	COMMANDS+=("$command")
}

add_command "Check diff whitespace" "$DIFF_CHECK_COMMAND"

if [[ "$MODE" == "worktree" && "$untracked_changed" == "1" ]]; then
	add_command "Check untracked file whitespace" "./scripts/check-untracked-whitespace.sh"
fi

add_command "Check obvious secret leaks" "./scripts/check-secret-leaks.sh"

if [[ "$agents_changed" == "1" ]]; then
	add_command "Validate agent plugin JSON" "if [ -f .agents/plugins/marketplace.json ]; then python3 -m json.tool .agents/plugins/marketplace.json >/dev/null; fi; if [ -d .agents/plugins ]; then find .agents/plugins -path '*/.codex-plugin/plugin.json' -type f -print0 | xargs -0 -r -n1 python3 -m json.tool >/dev/null; fi"
fi

if [[ "$precommit_changed" == "1" ]]; then
	add_command "Validate pre-commit config" "./scripts/check-yaml-configs.sh .pre-commit-config.yaml && if [ -f .pre-commit-config.yaml ] && command -v pre-commit >/dev/null 2>&1; then pre-commit validate-config; fi"
fi

if [[ "$workflows_changed" == "1" ]]; then
	add_command "Validate GitHub workflows" "make workflows-check"
fi

if [[ "$scripts_changed" == "1" ]]; then
	add_command "Validate shell scripts" "make scripts-check"
fi

if [[ "$lint_config_changed" == "1" ]]; then
	add_command "Run linters" "make lint"
fi

if [[ "$yaml_config_changed" == "1" ]]; then
	add_command "Validate YAML config" "./scripts/check-yaml-configs.sh .github/dependabot.yml .github/dependabot.yaml codecov.yml codecov.yaml"
fi

if [[ "$makefile_changed" == "1" ]]; then
	add_command "Run full project check" "make check"
fi

if [[ "$toolchain_changed" == "1" ]]; then
	add_command "Validate toolchain versions" "make toolchains-check"
fi

if [[ "$dependency_manifest_changed" == "1" ]]; then
	if [[ "$web_dependency_manifest_changed" == "1" ]]; then
		add_command "Run dependency security checks" "make security-check NPM_AUDIT=1"
	else
		add_command "Run dependency security checks" "make security-check"
	fi
fi

if [[ "$example_config_changed" == "1" ]]; then
	add_command "Validate example config" "env GOTOOLCHAIN=local go run ./cmd/nasd --check-config --config mnemonas.example.toml"
fi

if [[ "$docker_template_changed" == "1" ]]; then
	add_command "Validate Docker templates" "./scripts/test-docker-start.sh && ./scripts/test-docker-preflight.sh && ./scripts/test-docker-quickstart.sh"
fi

if [[ "$public_access_template_changed" == "1" ]]; then
	add_command "Validate public access templates" "./scripts/test-public-access-templates.sh"
fi

if [[ "$proto_changed" == "1" || "$proto_tool_changed" == "1" ]]; then
	add_command "Regenerate protobuf and check generated output stability" "tmp=\"\$(mktemp -d)\"; trap 'rm -rf -- \"\$tmp\"' EXIT; git diff -- proto/dataplane.pb.go proto/dataplane_grpc.pb.go dataplane/src/proto/mnemonas.dataplane.v1.rs > \"\$tmp/before.diff\"; make proto; git diff -- proto/dataplane.pb.go proto/dataplane_grpc.pb.go dataplane/src/proto/mnemonas.dataplane.v1.rs > \"\$tmp/after.diff\"; if ! cmp -s \"\$tmp/before.diff\" \"\$tmp/after.diff\"; then printf '%s\n' 'generated protobuf files changed after regeneration; run make proto and keep generated files updated' >&2; diff -u \"\$tmp/before.diff\" \"\$tmp/after.diff\" >&2 || true; exit 1; fi"
fi

if [[ "$makefile_changed" != "1" && ( "$go_changed" == "1" || "$proto_changed" == "1" ) ]]; then
	add_command "Run quick Go/Rust checks" "make quick-check"
fi

if [[ "$rust_changed" == "1" ]]; then
	add_command "Check dataplane Rust formatting" "cd dataplane && cargo fmt --check"
	add_command "Run dataplane tests" "cd dataplane && cargo test --locked"
	add_command "Run dataplane clippy" "cd dataplane && cargo clippy --all-targets --locked -- -D warnings"
fi

if [[ "$proto_tool_changed" == "1" ]]; then
	add_command "Check proto generator Rust formatting" "cargo fmt --manifest-path tools/proto-gen/Cargo.toml --check"
	add_command "Run proto generator tests" "cargo test --manifest-path tools/proto-gen/Cargo.toml --locked"
	add_command "Run proto generator clippy" "cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings"
fi

if [[ "$web_changed" == "1" ]]; then
	add_command "Run frontend lint" "cd web && npm run lint"
	add_command "Run frontend typecheck" "cd web && npm run typecheck"
	add_command "Run frontend unit tests" "cd web && npm run test:run"
	add_command "Build frontend" "cd web && npm run build"
fi

if [[ "$web_e2e_changed" == "1" ]]; then
	add_command "Run frontend E2E" "cd web && npm run test:e2e"
fi

if [[ "$docker_changed" == "1" ]]; then
	add_command "Build and smoke test Docker image" "$DOCKER_BUILD_COMMAND"
fi

if [[ "$docs_changed" == "1" && "$makefile_changed" != "1" ]]; then
	add_command "Validate documentation links" "make docs-check"
fi

if [[ "${#COMMANDS[@]}" -eq 0 ]]; then
	printf 'verify-changed: no automated validation selected for changed files\n'
	printf 'verify-changed: review docs/config-only changes manually\n'
	exit 0
fi

printf 'Selected validation commands:\n'
for i in "${!COMMANDS[@]}"; do
	printf '  - %s: %s\n' "${COMMAND_LABELS[$i]}" "${COMMANDS[$i]}"
done

if [[ "$DRY_RUN" == "1" ]]; then
	exit 0
fi

for i in "${!COMMANDS[@]}"; do
	printf '\n==> %s\n' "${COMMAND_LABELS[$i]}"
	bash -lc "${COMMANDS[$i]}"
done
