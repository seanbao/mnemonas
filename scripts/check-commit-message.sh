#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'commit message check failed: %s\n' "$*" >&2
	printf 'Expected: <type>(optional-scope): <lowercase imperative subject>\n' >&2
	printf 'Example: fix(api): reject invalid share passwords\n' >&2
	exit 1
}

if [[ $# -ne 1 ]]; then
	fail "expected the commit message file path"
fi

message_file="$1"
[[ -f "$message_file" ]] || fail "message file not found: $message_file"

subject="$(awk '
	/^[[:space:]]*#/ { next }
	/^[[:space:]]*$/ { next }
	{ print; exit }
' "$message_file")"

[[ -n "$subject" ]] || fail "empty commit message"

case "$subject" in
	Merge\ *|Revert\ \"*|fixup!\ *|squash!\ *)
		exit 0
		;;
esac

if (( ${#subject} > 72 )); then
	fail "subject exceeds 72 characters"
fi

pattern='^(feat|fix|docs|test|refactor|perf|build|ci|style|chore|revert)(\([a-z0-9][a-z0-9._/-]*\))?!?: .+'
if [[ ! "$subject" =~ $pattern ]]; then
	fail "subject must use Conventional Commits"
fi

description="${subject#*: }"
first_char="${description:0:1}"
if [[ "$first_char" =~ [[:upper:]] ]]; then
	fail "subject description must start lowercase"
fi

if [[ "$subject" == *. ]]; then
	fail "subject must not end with a period"
fi
