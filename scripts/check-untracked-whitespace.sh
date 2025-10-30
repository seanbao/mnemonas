#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if ! command -v python3 >/dev/null 2>&1; then
  printf 'check-untracked-whitespace: python3 is required\n' >&2
  exit 1
fi

declare -a files=()
if [[ "$#" -gt 0 ]]; then
  files=("$@")
else
  while IFS= read -r -d '' file; do
    files+=("$file")
  done < <(git ls-files --others --exclude-standard -z)
fi

if [[ "${#files[@]}" -eq 0 ]]; then
  printf '[untracked-whitespace-check] checked 0 text file(s)\n'
  exit 0
fi

python3 - "${files[@]}" <<'PY'
import pathlib
import re
import sys

TEXT_SUFFIXES = {
    ".bash",
    ".cjs",
    ".css",
    ".go",
    ".html",
    ".js",
    ".json",
    ".jsx",
    ".lock",
    ".md",
    ".mjs",
    ".rs",
    ".sh",
    ".toml",
    ".ts",
    ".tsx",
    ".txt",
    ".yaml",
    ".yml",
}
TEXT_NAMES = {
    ".dockerignore",
    ".editorconfig",
    ".env",
    ".env.example",
    ".gitignore",
    ".go-version",
    ".npmrc",
    ".nvmrc",
    ".prettierignore",
    ".shellcheckrc",
    "Dockerfile",
    "Makefile",
}
TEXT_PREFIXES = (
    "scripts/",
    "web/.husky/",
    "web/scripts/",
)
SPACE_BEFORE_TAB = re.compile(rb"^[ \t]* \t")


def should_check(path: pathlib.Path) -> bool:
    normalized = path.as_posix()
    return (
        path.name in TEXT_NAMES
        or path.suffix in TEXT_SUFFIXES
        or any(normalized.startswith(prefix) for prefix in TEXT_PREFIXES)
    )


errors = []
checked = 0

for raw_path in sys.argv[1:]:
    path = pathlib.Path(raw_path)
    if path.is_symlink() or not path.exists() or path.is_dir() or not should_check(path):
        continue

    data = path.read_bytes()
    if b"\0" in data:
        continue

    checked += 1
    for line_number, raw_line in enumerate(data.splitlines(keepends=True), 1):
        line = raw_line.rstrip(b"\r\n")
        if line.endswith((b" ", b"\t")):
            errors.append(f"{path}:{line_number}: trailing whitespace")
        if SPACE_BEFORE_TAB.match(line):
            errors.append(f"{path}:{line_number}: space before tab in indent")

if errors:
    for error in errors:
        print(error, file=sys.stderr)
    sys.exit(1)

print(f"[untracked-whitespace-check] checked {checked} text file(s)")
PY
