#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="${TMPDIR:-/tmp}/mnemonas-webdav-compat-docs-test-$$"

fail() {
    echo "[webdav-compat-docs-test] ERROR: $*" >&2
    exit 1
}

assert_file_contains() {
    local file="$1"
    local expected="$2"
    if ! grep -Fq -- "$expected" "$file"; then
        echo "Expected to find: $expected" >&2
        echo "--- $file ---" >&2
        cat "$file" >&2
        fail "missing expected text"
    fi
}

cleanup() {
    rm -rf "$TMP_ROOT"
}

run_checker() {
    local chinese_doc="$1"
    local english_doc="$2"
    WEBDAV_COMPATIBILITY_DOC="$chinese_doc" \
        WEBDAV_COMPATIBILITY_DOC_EN="$english_doc" \
        bash "$REPO_ROOT/scripts/check-webdav-compatibility-docs.sh"
}

run_expect_failure() {
    local output="$1"
    shift
    if "$@" > "$output" 2>&1; then
        cat "$output" >&2
        fail "expected command to fail"
    fi
}

prepare_case_docs() {
    local case_dir="$1"
    mkdir -p "$case_dir/docs"
    cp "$REPO_ROOT/docs/webdav-compatibility.md" "$case_dir/docs/webdav-compatibility.md"
    cp "$REPO_ROOT/docs/webdav-compatibility.en.md" "$case_dir/docs/webdav-compatibility.en.md"
}

run_success_test() {
    local case_dir="$TMP_ROOT/success"
    prepare_case_docs "$case_dir"
    run_checker "$case_dir/docs/webdav-compatibility.md" "$case_dir/docs/webdav-compatibility.en.md" > "$case_dir/out.log"
    assert_file_contains "$case_dir/out.log" "checked WebDAV compatibility matrix and validation standard"
}

run_missing_client_row_test() {
    local case_dir="$TMP_ROOT/missing-client-row"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/^\| Finder \|[^\n]*\n//m' "$case_dir/docs/webdav-compatibility.md"

    run_expect_failure "$case_dir/out.log" run_checker "$case_dir/docs/webdav-compatibility.md" "$case_dir/docs/webdav-compatibility.en.md"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV compatibility matrix row: Finder"
}

run_unknown_status_test() {
    local case_dir="$TMP_ROOT/unknown-status"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/\| Nautilus \/ GNOME Files \| 45\+ \| 预期可用 \|/\| Nautilus \/ GNOME Files \| 45+ \| 可能可用 \|/' "$case_dir/docs/webdav-compatibility.md"

    run_expect_failure "$case_dir/out.log" run_checker "$case_dir/docs/webdav-compatibility.md" "$case_dir/docs/webdav-compatibility.en.md"
    assert_file_contains "$case_dir/out.log" "unsupported WebDAV compatibility status for Nautilus / GNOME Files: 可能可用"
}

run_missing_validation_standard_test() {
    local case_dir="$TMP_ROOT/missing-validation-standard"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/## Real-Client Validation Standard/## Manual Validation/' "$case_dir/docs/webdav-compatibility.en.md"

    run_expect_failure "$case_dir/out.log" run_checker "$case_dir/docs/webdav-compatibility.md" "$case_dir/docs/webdav-compatibility.en.md"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV compatibility text: ## Real-Client Validation Standard"
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_success_test
run_missing_client_row_test
run_unknown_status_test
run_missing_validation_standard_test

printf '[webdav-compat-docs-test] all checks passed\n'
