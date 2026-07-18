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
    local chinese_readme="$3"
    local english_readme="$4"
    local chinese_mounting_guide="$5"
    local english_mounting_guide="$6"
    local chinese_faq="$7"
    local english_faq="$8"
    WEBDAV_COMPATIBILITY_DOC="$chinese_doc" \
        WEBDAV_COMPATIBILITY_DOC_EN="$english_doc" \
        WEBDAV_README="$chinese_readme" \
        WEBDAV_README_EN="$english_readme" \
        WEBDAV_MOUNTING_GUIDE="$chinese_mounting_guide" \
        WEBDAV_MOUNTING_GUIDE_EN="$english_mounting_guide" \
        WEBDAV_FAQ="$chinese_faq" \
        WEBDAV_FAQ_EN="$english_faq" \
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
    cp "$REPO_ROOT/docs/mounting-guide.md" "$case_dir/docs/mounting-guide.md"
    cp "$REPO_ROOT/docs/mounting-guide.en.md" "$case_dir/docs/mounting-guide.en.md"
    cp "$REPO_ROOT/docs/faq.md" "$case_dir/docs/faq.md"
    cp "$REPO_ROOT/docs/faq.en.md" "$case_dir/docs/faq.en.md"
    cp "$REPO_ROOT/README.md" "$case_dir/README.md"
    cp "$REPO_ROOT/README.en.md" "$case_dir/README.en.md"
}

run_case_checker() {
    local case_dir="$1"
    run_checker \
        "$case_dir/docs/webdav-compatibility.md" \
        "$case_dir/docs/webdav-compatibility.en.md" \
        "$case_dir/README.md" \
        "$case_dir/README.en.md" \
        "$case_dir/docs/mounting-guide.md" \
        "$case_dir/docs/mounting-guide.en.md" \
        "$case_dir/docs/faq.md" \
        "$case_dir/docs/faq.en.md"
}

run_success_test() {
    local case_dir="$TMP_ROOT/success"
    prepare_case_docs "$case_dir"
    run_case_checker "$case_dir" > "$case_dir/out.log"
    assert_file_contains "$case_dir/out.log" "checked WebDAV compatibility matrix, validation standard, README client summary, auth guidance, mounting guide note, and FAQ auth guidance"
}

run_missing_client_row_test() {
    local case_dir="$TMP_ROOT/missing-client-row"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/^\| Finder \|[^\n]*\n//m' "$case_dir/docs/webdav-compatibility.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV compatibility matrix row: Finder"
}

run_unknown_status_test() {
    local case_dir="$TMP_ROOT/unknown-status"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/\| Nautilus \/ GNOME Files \| 45\+ \| 预期可用 \|/\| Nautilus \/ GNOME Files \| 45+ \| 可能可用 \|/' "$case_dir/docs/webdav-compatibility.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "unsupported WebDAV compatibility status for Nautilus / GNOME Files: 可能可用"
}

run_missing_validation_standard_test() {
    local case_dir="$TMP_ROOT/missing-validation-standard"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/## Real-Client Validation Standard/## Manual Validation/' "$case_dir/docs/webdav-compatibility.en.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV compatibility text: ## Real-Client Validation Standard"
}

run_readme_overclaim_test() {
    local case_dir="$TMP_ROOT/readme-overclaim"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/\| Platform \| Common Client \| URL \|/\| Platform \| Recommended Client \| URL \|/' "$case_dir/README.en.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "avoid overclaiming WebDAV client support in README"
}

run_readme_missing_matrix_link_test() {
    local case_dir="$TMP_ROOT/readme-missing-matrix-link"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's{；兼容状态以 \[WebDAV 兼容性\]\(docs/webdav-compatibility\.md\) 矩阵为准}{}' "$case_dir/README.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required README WebDAV client-summary text: [WebDAV 兼容性](docs/webdav-compatibility.md)"
}

run_readme_top_overclaim_test() {
    local case_dir="$TMP_ROOT/readme-top-overclaim"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/WebDAV 协议入口覆盖主要访问路径，客户端兼容状态按矩阵持续跟踪/常见 WebDAV 客户端均可访问，不只是文件浏览器/' "$case_dir/README.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "avoid overclaiming WebDAV client support in README"
}

run_readme_missing_auth_guidance_test() {
    local case_dir="$TMP_ROOT/readme-missing-auth-guidance"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/User-boundary validation should switch to `users`/Manual deployments may choose another mode/' "$case_dir/README.en.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required README WebDAV client-summary text: User-boundary validation should switch to \`users\`"
}

run_mounting_guide_missing_matrix_note_test() {
    local case_dir="$TMP_ROOT/mounting-guide-missing-matrix-note"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's{\[WebDAV compatibility\]\(webdav-compatibility\.en\.md\)}{WebDAV compatibility}' "$case_dir/docs/mounting-guide.en.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV mounting-guide compatibility note: [WebDAV compatibility](webdav-compatibility.en.md)"
}

run_mounting_guide_missing_auth_guidance_test() {
    local case_dir="$TMP_ROOT/mounting-guide-missing-auth-guidance"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/开发验证挂载应改用 `users`/可按需要选择认证模式/' "$case_dir/docs/mounting-guide.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV mounting-guide compatibility note: 开发验证挂载应改用 \`users\`"
}

run_faq_missing_auth_guidance_test() {
    local case_dir="$TMP_ROOT/faq-missing-auth-guidance"
    prepare_case_docs "$case_dir"
    perl -0pi -e 's/WebDAV mounts used for development validation should prefer MnemoNAS user authentication/Operators can choose any authentication mode/' "$case_dir/docs/faq.en.md"

    run_expect_failure "$case_dir/out.log" run_case_checker "$case_dir"
    assert_file_contains "$case_dir/out.log" "missing required WebDAV FAQ auth guidance: WebDAV mounts used for development validation should prefer MnemoNAS user authentication"
}

trap cleanup EXIT
mkdir -p "$TMP_ROOT"

run_success_test
run_missing_client_row_test
run_unknown_status_test
run_missing_validation_standard_test
run_readme_overclaim_test
run_readme_missing_matrix_link_test
run_readme_top_overclaim_test
run_readme_missing_auth_guidance_test
run_mounting_guide_missing_matrix_note_test
run_mounting_guide_missing_auth_guidance_test
run_faq_missing_auth_guidance_test

printf '[webdav-compat-docs-test] all checks passed\n'
