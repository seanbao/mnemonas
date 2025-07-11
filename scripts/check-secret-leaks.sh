#!/usr/bin/env bash

set -euo pipefail

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
	printf 'secret-leak-check: must run inside a git repository\n' >&2
	exit 1
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

MAX_FILE_BYTES="${MNEMONAS_SECRET_SCAN_MAX_FILE_BYTES:-5242880}"

declare -a PATTERN_LABELS=(
	"private key block"
	"GitHub token"
	"AWS access key"
	"Slack token"
	"OpenAI API key"
	"Google API key"
	"Anthropic API key"
	"npm token"
)
declare -a PATTERNS=(
	'-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----'
	'gh[pousr]_[A-Za-z0-9_]{36,}|github_pat_[A-Za-z0-9_]{20,}'
	'(A3T[A-Z0-9]|AKIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[0-9A-Z]{16}'
	'xox[baprs]-[A-Za-z0-9-]{20,}'
	'sk-(proj-)?[A-Za-z0-9_-]{32,}'
	'AIza[0-9A-Za-z_-]{35}'
	'sk-ant-[A-Za-z0-9_-]{30,}'
	'npm_[A-Za-z0-9]{36,}'
)

should_skip_path() {
	local path="$1"

	case "$path" in
		.git/*|node_modules/*|web/node_modules/*|web/dist/*|web/coverage/*|web/test-results/*|web/playwright-report/*|web/.vite/*|web/.vitest/*|coverage/*|target/*|dataplane/target/*|tools/proto-gen/target/*|bin/*)
			return 0
			;;
		*.png|*.jpg|*.jpeg|*.gif|*.webp|*.ico|*.pdf|*.zip|*.gz|*.tar|*.tgz|*.xz|*.bz2|*.woff|*.woff2|*.ttf|*.eot|*.mp4|*.mov|*.wasm)
			return 0
			;;
	esac

	return 1
}

scan_file() {
	local file="$1"
	local size
	local i
	local matches
	local label
	local pattern

	[[ -f "$file" ]] || return 0
	if should_skip_path "$file"; then
		skipped_files=$((skipped_files + 1))
		return 0
	fi

	if [[ "$MAX_FILE_BYTES" =~ ^[0-9]+$ ]]; then
		size="$(stat -c '%s' -- "$file" 2>/dev/null || printf '0')"
		if (( size > MAX_FILE_BYTES )); then
			skipped_files=$((skipped_files + 1))
			return 0
		fi
	fi

	if ! LC_ALL=C grep -Iq . -- "$file" 2>/dev/null; then
		return 0
	fi

	checked_files=$((checked_files + 1))

	for i in "${!PATTERNS[@]}"; do
		label="${PATTERN_LABELS[$i]}"
		pattern="${PATTERNS[$i]}"
		if matches="$(LC_ALL=C awk -v display_file="$file" -v pattern="$pattern" '
			$0 ~ pattern {
				printf "%s:%d\n", display_file, FNR
				found = 1
			}
			END {
				exit found ? 0 : 1
			}
		' "./$file" 2>/dev/null)"; then
			while IFS= read -r location; do
				[[ -n "$location" ]] || continue
				printf '%s: potential secret leak (%s)\n' "$location" "$label" >&2
			done <<<"$matches"
			leaks_found=1
		fi
	done
}

declare -a files=()
checked_files=0
skipped_files=0
leaks_found=0

while IFS= read -r -d '' file; do
	files+=("$file")
done < <(git ls-files -z)

while IFS= read -r -d '' file; do
	files+=("$file")
done < <(git ls-files --others --exclude-standard -z)

for file in "${files[@]}"; do
	scan_file "$file"
done

if (( leaks_found != 0 )); then
	printf '[secret-leak-check] failed: potential secrets found. Remove real secrets or replace them with documented placeholders.\n' >&2
	exit 1
fi

printf '[secret-leak-check] checked %d files (%d skipped).\n' "$checked_files" "$skipped_files"
