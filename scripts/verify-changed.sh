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

add_file() {
	local file="$1"
	[[ -n "$file" ]] || return 0
	[[ -z "${SEEN_FILES[$file]+x}" ]] || return 0
	SEEN_FILES["$file"]=1
	FILES+=("$file")
}

case "$MODE" in
	staged)
		while IFS= read -r file; do
			RAW_FILES+=("$file")
		done < <(git diff --cached --name-only --diff-filter=ACMRD)
		;;
	base)
		[[ -n "$BASE" ]] || {
			printf 'verify-changed: base ref is empty\n' >&2
			exit 2
		}
		while IFS= read -r file; do
			RAW_FILES+=("$file")
		done < <(git diff --name-only --diff-filter=ACMRD "$BASE"...HEAD)
		;;
	worktree)
		if git rev-parse --verify HEAD >/dev/null 2>&1; then
			while IFS= read -r file; do
				RAW_FILES+=("$file")
			done < <(git diff --name-only --diff-filter=ACMRD HEAD --)
		else
			while IFS= read -r file; do
				RAW_FILES+=("$file")
			done < <(git ls-files)
		fi
		while IFS= read -r file; do
			RAW_FILES+=("$file")
		done < <(git ls-files --others --exclude-standard)
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
precommit_changed=0
agents_changed=0
makefile_changed=0

for file in "${FILES[@]}"; do
	case "$file" in
		*.go|go.mod|go.sum)
			go_changed=1
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
		web/e2e/*|web/playwright.config.*)
			web_changed=1
			web_e2e_changed=1
			;;
		web/*)
			web_changed=1
			;;
	esac

	case "$file" in
		scripts/*.sh|web/scripts/*)
			scripts_changed=1
			;;
		.github/workflows/*)
			workflows_changed=1
			;;
		Dockerfile|docker-compose.yml|docker-compose.yaml|deploy/*docker-compose.yml)
			docker_changed=1
			;;
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
done

declare -a COMMAND_LABELS=()
declare -a COMMANDS=()
declare -A SEEN_COMMANDS=()

add_command() {
	local label="$1"
	local command="$2"
	[[ -z "${SEEN_COMMANDS[$command]+x}" ]] || return 0
	SEEN_COMMANDS["$command"]=1
	COMMAND_LABELS+=("$label")
	COMMANDS+=("$command")
}

if [[ "$agents_changed" == "1" ]]; then
	add_command "Validate agent plugin JSON" "if [ -f .agents/plugins/marketplace.json ]; then python3 -m json.tool .agents/plugins/marketplace.json >/dev/null; fi; if [ -d .agents/plugins ]; then find .agents/plugins -path '*/.codex-plugin/plugin.json' -type f -print0 | xargs -0 -r -n1 python3 -m json.tool >/dev/null; fi"
fi

if [[ "$precommit_changed" == "1" ]]; then
	add_command "Validate pre-commit config" "if command -v pre-commit >/dev/null 2>&1; then pre-commit validate-config; else python3 -c 'import yaml; yaml.safe_load(open(\".pre-commit-config.yaml\"))'; fi"
fi

if [[ "$workflows_changed" == "1" ]]; then
	add_command "Validate GitHub workflows" "make workflows-check"
fi

if [[ "$scripts_changed" == "1" ]]; then
	add_command "Validate shell scripts" "make scripts-check"
fi

if [[ "$proto_changed" == "1" ]]; then
	add_command "Regenerate protobuf and check generated output stability" "tmp=\"\$(mktemp -d)\"; trap 'rm -rf -- \"\$tmp\"' EXIT; git diff -- proto/dataplane.pb.go proto/dataplane_grpc.pb.go dataplane/src/proto/mnemonas.dataplane.v1.rs > \"\$tmp/before.diff\"; make proto; git diff -- proto/dataplane.pb.go proto/dataplane_grpc.pb.go dataplane/src/proto/mnemonas.dataplane.v1.rs > \"\$tmp/after.diff\"; if ! cmp -s \"\$tmp/before.diff\" \"\$tmp/after.diff\"; then printf '%s\n' 'generated protobuf files changed after regeneration; run make proto and keep generated files updated' >&2; diff -u \"\$tmp/before.diff\" \"\$tmp/after.diff\" >&2 || true; exit 1; fi"
fi

if [[ "$go_changed" == "1" || "$makefile_changed" == "1" ]]; then
	add_command "Run quick Go/Rust checks" "make quick-check"
fi

if [[ "$rust_changed" == "1" ]]; then
	add_command "Run dataplane tests" "cd dataplane && cargo test --locked"
	add_command "Run dataplane clippy" "cd dataplane && cargo clippy --all-targets --locked -- -D warnings"
fi

if [[ "$proto_tool_changed" == "1" ]]; then
	add_command "Run proto generator tests" "cargo test --manifest-path tools/proto-gen/Cargo.toml --locked"
	add_command "Run proto generator clippy" "cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings"
fi

if [[ "$web_changed" == "1" ]]; then
	add_command "Run frontend lint" "cd web && npm run lint"
	add_command "Run frontend unit tests" "cd web && npm run test:run"
	add_command "Build frontend" "cd web && npm run build"
fi

if [[ "$web_e2e_changed" == "1" ]]; then
	add_command "Run frontend E2E" "cd web && npm run test:e2e"
fi

if [[ "$docker_changed" == "1" ]]; then
	add_command "Build Docker image" "make docker"
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
