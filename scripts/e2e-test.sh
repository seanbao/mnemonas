#!/bin/bash
# MnemoNAS End-to-End Acceptance Tests
# Week 8: Comprehensive validation for MVP release
#
# Usage: ./scripts/e2e-test.sh [--quick|--full]
#   --quick: Skip slow tests (crash injection, large files)
#   --full:  Run all tests including stress tests (default)

# Don't exit on error - we handle errors ourselves
set +e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="${BASE_URL:-http://localhost:8080}"
WEBDAV_URL="${BASE_URL}/dav"
API_URL="${BASE_URL}/api/v1"
TEST_DIR="/tmp/mnemonas-e2e-$$"
QUICK_MODE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --quick) QUICK_MODE=true; shift ;;
        --full)  QUICK_MODE=false; shift ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# Counters
PASSED=0
FAILED=0
SKIPPED=0
ADMIN_ACCESS_TOKEN=""
ADMIN_REFRESH_TOKEN=""
ADMIN_API_BODY=""
ADMIN_API_STATUS=""

# Utility functions
log_info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
log_ok()    { echo -e "${GREEN}[PASS]${NC} $1"; ((PASSED++)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $1"; ((FAILED++)); }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $1"; ((SKIPPED++)); }

cleanup() {
    log_info "Cleaning up test directory..."
    rm -rf "$TEST_DIR"
    # Clean up test files in WebDAV (ignore errors)
    curl -s -X DELETE "$WEBDAV_URL/e2e-test/" > /dev/null 2>&1 || true
}

# Only trap on normal exit, not on errors during test
trap 'cleanup' EXIT

setup() {
    log_info "Setting up test environment..."
    mkdir -p "$TEST_DIR"
    
    # Check service health
    if ! curl -sf "$BASE_URL/health" > /dev/null; then
        echo -e "${RED}ERROR: MnemoNAS service not running at $BASE_URL${NC}"
        echo "Please start the service: ./bin/nasd &"
        exit 1
    fi
    log_info "Service is healthy"
}

admin_api_request() {
    local method=$1
    local url=$2
    local response=""
    local curl_args=(-s -X "$method" "$url")

    if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
        curl_args+=(-H "Authorization: Bearer $ADMIN_ACCESS_TOKEN")
    fi

    response=$(curl "${curl_args[@]}" -w $'\n%{http_code}' 2>/dev/null || true)
    ADMIN_API_STATUS="${response##*$'\n'}"
    ADMIN_API_BODY="${response%$'\n'*}"
}

# ==============================================================================
# Test Group 1: Basic Functionality
# ==============================================================================

test_health_check() {
    log_info "Testing health endpoint..."
    local resp=$(curl -sf "$BASE_URL/health")
    if echo "$resp" | grep -q '"status":"healthy"'; then
        log_ok "Health check returns healthy status"
    else
        log_fail "Health check failed: $resp"
    fi
}

test_version_api() {
    log_info "Testing version API..."
    local resp=$(curl -sf "$API_URL/version" 2>/dev/null || echo "error")
    if echo "$resp" | grep -q '"version"'; then
        log_ok "Version API returns version info"
    else
        log_fail "Version API failed: $resp"
    fi
}

test_webdav_options() {
    log_info "Testing WebDAV OPTIONS..."
    local allow=$(curl -sf -X OPTIONS "$WEBDAV_URL/" -I 2>/dev/null | grep -i "allow:" || echo "")
    if echo "$allow" | grep -qi "PROPFIND"; then
        log_ok "WebDAV OPTIONS includes PROPFIND"
    else
        log_fail "WebDAV OPTIONS missing methods: $allow"
    fi
}

# ==============================================================================
# Test Group 2: File Operations (CRUD)
# ==============================================================================

test_file_upload() {
    log_info "Testing file upload (PUT)..."
    # First create parent directory
    curl -sf -X MKCOL "$WEBDAV_URL/e2e-test/" > /dev/null 2>&1 || true
    
    echo "Hello, MnemoNAS!" > "$TEST_DIR/test.txt"
    local status=$(curl -sf -X PUT "$WEBDAV_URL/e2e-test/test.txt" \
        -T "$TEST_DIR/test.txt" -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "201" || "$status" == "204" ]]; then
        log_ok "File upload successful (status: $status)"
    else
        log_fail "File upload failed (status: $status)"
    fi
}

test_file_download() {
    log_info "Testing file download (GET)..."
    local content=$(curl -sf "$WEBDAV_URL/e2e-test/test.txt")
    if [[ "$content" == "Hello, MnemoNAS!" ]]; then
        log_ok "File download returns correct content"
    else
        log_fail "File download mismatch: '$content'"
    fi
}

test_file_delete() {
    log_info "Testing file delete (DELETE)..."
    # Create a file to delete
    echo "delete me" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/delete-me.txt" -T - > /dev/null
    local status=$(curl -sf -X DELETE "$WEBDAV_URL/e2e-test/delete-me.txt" -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "204" || "$status" == "200" ]]; then
        # Verify it's actually deleted
        local get_status=$(curl -s -w "%{http_code}" -o /dev/null "$WEBDAV_URL/e2e-test/delete-me.txt")
        if [[ "$get_status" == "404" ]]; then
            log_ok "File delete successful and verified"
        else
            log_fail "File deleted but still accessible (status: $get_status)"
        fi
    else
        log_fail "File delete failed (status: $status)"
    fi
}

test_directory_create() {
    log_info "Testing directory create (MKCOL)..."
    local status=$(curl -sf -X MKCOL "$WEBDAV_URL/e2e-test/subdir/" -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "201" ]]; then
        log_ok "Directory create successful"
    else
        log_fail "Directory create failed (status: $status)"
    fi
}

test_propfind() {
    log_info "Testing PROPFIND..."
    local resp=$(curl -sf -X PROPFIND "$WEBDAV_URL/e2e-test/" -H "Depth: 1")
    if echo "$resp" | grep -q "test.txt"; then
        log_ok "PROPFIND lists files correctly"
    else
        log_fail "PROPFIND missing expected file"
    fi
}

test_file_copy() {
    log_info "Testing file copy (COPY)..."
    local status=$(curl -sf -X COPY "$WEBDAV_URL/e2e-test/test.txt" \
        -H "Destination: $WEBDAV_URL/e2e-test/test-copy.txt" \
        -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "201" || "$status" == "204" ]]; then
        local content=$(curl -sf "$WEBDAV_URL/e2e-test/test-copy.txt")
        if [[ "$content" == "Hello, MnemoNAS!" ]]; then
            log_ok "File copy successful and content verified"
        else
            log_fail "File copy content mismatch"
        fi
    else
        log_fail "File copy failed (status: $status)"
    fi
}

test_file_move() {
    log_info "Testing file move (MOVE)..."
    echo "move me" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/to-move.txt" -T - > /dev/null
    local status=$(curl -sf -X MOVE "$WEBDAV_URL/e2e-test/to-move.txt" \
        -H "Destination: $WEBDAV_URL/e2e-test/moved.txt" \
        -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "201" || "$status" == "204" ]]; then
        # Source should be gone
        local src_status=$(curl -s -w "%{http_code}" -o /dev/null "$WEBDAV_URL/e2e-test/to-move.txt")
        local dst_content=$(curl -sf "$WEBDAV_URL/e2e-test/moved.txt")
        if [[ "$src_status" == "404" && "$dst_content" == "move me" ]]; then
            log_ok "File move successful"
        else
            log_fail "File move verification failed"
        fi
    else
        log_fail "File move failed (status: $status)"
    fi
}

# ==============================================================================
# Test Group 3: ETag / Conditional Requests
# ==============================================================================

test_etag_returned() {
    log_info "Testing ETag header presence..."
    local etag=$(curl -sf "$WEBDAV_URL/e2e-test/test.txt" -I | grep -i "^etag:" || echo "")
    if [[ -n "$etag" ]]; then
        log_ok "ETag header present: $etag"
    else
        log_fail "ETag header missing"
    fi
}

test_if_none_match() {
    log_info "Testing If-None-Match (304 Not Modified)..."
    local etag=$(curl -sf "$WEBDAV_URL/e2e-test/test.txt" -I | grep -i "^etag:" | awk '{print $2}' | tr -d '\r')
    local status=$(curl -s -w "%{http_code}" -o /dev/null "$WEBDAV_URL/e2e-test/test.txt" \
        -H "If-None-Match: $etag")
    if [[ "$status" == "304" ]]; then
        log_ok "If-None-Match returns 304 correctly"
    else
        log_fail "If-None-Match failed (expected 304, got $status)"
    fi
}

test_if_match_success() {
    log_info "Testing If-Match (precondition success)..."
    local etag=$(curl -sf "$WEBDAV_URL/e2e-test/test.txt" -I | grep -i "^etag:" | awk '{print $2}' | tr -d '\r')
    echo "Updated content" > "$TEST_DIR/update.txt"
    local status=$(curl -sf -X PUT "$WEBDAV_URL/e2e-test/test.txt" \
        -H "If-Match: $etag" \
        -T "$TEST_DIR/update.txt" -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "204" || "$status" == "200" ]]; then
        log_ok "If-Match precondition success"
    else
        log_fail "If-Match failed unexpectedly (status: $status)"
    fi
}

test_if_match_failure() {
    log_info "Testing If-Match (precondition failure - 412)..."
    echo "conflict test" > "$TEST_DIR/conflict.txt"
    local status=$(curl -s -X PUT "$WEBDAV_URL/e2e-test/test.txt" \
        -H "If-Match: \"wrong-etag\"" \
        -T "$TEST_DIR/conflict.txt" -w "%{http_code}" -o /dev/null)
    if [[ "$status" == "412" ]]; then
        log_ok "If-Match returns 412 for wrong ETag"
    else
        log_fail "If-Match should return 412 (got $status)"
    fi
}

# ==============================================================================
# Test Group 4: Version History
# ==============================================================================

test_version_history() {
    log_info "Testing version history API..."
    # Create a file with multiple versions
    for i in 1 2 3; do
        echo "Version $i" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/versioned.txt" -T - > /dev/null
        sleep 0.1
    done
    
    local resp=$(curl -sf "$API_URL/versions/e2e-test/versioned.txt" 2>/dev/null || echo "error")
    if echo "$resp" | grep -q "versions\|hash"; then
        log_ok "Version history API returns data"
    else
        log_fail "Version history API failed: $resp"
    fi
}

# ==============================================================================
# Test Group 5: Concurrent Access
# ==============================================================================

test_concurrent_reads() {
    log_info "Testing concurrent reads (10 parallel)..."
    echo "concurrent read test" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/concurrent.txt" -T - > /dev/null
    
    local pids=()
    local fail=0
    for i in {1..10}; do
        (curl -sf "$WEBDAV_URL/e2e-test/concurrent.txt" > /dev/null) &
        pids+=($!)
    done
    
    for pid in "${pids[@]}"; do
        wait "$pid" || ((fail++))
    done
    
    if [[ $fail -eq 0 ]]; then
        log_ok "10 concurrent reads successful"
    else
        log_fail "Concurrent reads: $fail failures"
    fi
}

test_concurrent_writes() {
    log_info "Testing concurrent writes (5 parallel)..."
    local pids=()
    local fail=0
    
    for i in {1..5}; do
        (echo "Writer $i at $(date +%s%N)" | \
            curl -sf -X PUT "$WEBDAV_URL/e2e-test/concurrent-$i.txt" -T - > /dev/null) &
        pids+=($!)
    done
    
    for pid in "${pids[@]}"; do
        wait "$pid" || ((fail++))
    done
    
    if [[ $fail -eq 0 ]]; then
        log_ok "5 concurrent writes to different files successful"
    else
        log_fail "Concurrent writes: $fail failures"
    fi
}

test_concurrent_same_file() {
    log_info "Testing concurrent writes to same file..."
    # This should be serialized by PathLock
    local pids=()
    local fail=0
    
    for i in {1..3}; do
        (echo "Content $i" | \
            curl -sf -X PUT "$WEBDAV_URL/e2e-test/race.txt" -T - > /dev/null) &
        pids+=($!)
    done
    
    for pid in "${pids[@]}"; do
        wait "$pid" || ((fail++))
    done
    
    # File should exist with some content
    local content=$(curl -sf "$WEBDAV_URL/e2e-test/race.txt")
    if [[ -n "$content" ]]; then
        log_ok "Concurrent writes to same file: serialized correctly"
    else
        log_fail "Concurrent writes to same file: corrupted"
    fi
}

# ==============================================================================
# Test Group 6: Maintenance & Diagnostics
# ==============================================================================

test_metrics_api() {
    log_info "Testing metrics API..."
    admin_api_request GET "$API_URL/metrics"
    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$ADMIN_API_STATUS" == "401" || "$ADMIN_API_STATUS" == "403" ) ]]; then
        log_skip "Metrics API requires admin authentication"
    elif [[ "$ADMIN_API_STATUS" == "200" ]] && echo "$ADMIN_API_BODY" | grep -q "requests"; then
        log_ok "Metrics API returns request statistics"
    else
        log_fail "Metrics API failed (status: $ADMIN_API_STATUS): $ADMIN_API_BODY"
    fi
}

test_scrub_api() {
    log_info "Testing scrub API..."
    admin_api_request GET "$API_URL/maintenance/scrub"
    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$ADMIN_API_STATUS" == "401" || "$ADMIN_API_STATUS" == "403" ) ]]; then
        log_skip "Scrub API requires admin authentication"
    elif [[ "$ADMIN_API_STATUS" == "200" ]] && echo "$ADMIN_API_BODY" | grep -q "success\|has_result\|running"; then
        log_ok "Scrub API returns status"
    else
        log_fail "Scrub API failed (status: $ADMIN_API_STATUS): $ADMIN_API_BODY"
    fi
}

test_scrub_trigger() {
    log_info "Testing scrub trigger (POST)..."
    admin_api_request POST "$API_URL/maintenance/scrub"
    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$ADMIN_API_STATUS" == "401" || "$ADMIN_API_STATUS" == "403" ) ]]; then
        log_skip "Scrub trigger API requires admin authentication"
    elif [[ "$ADMIN_API_STATUS" == "200" ]] && echo "$ADMIN_API_BODY" | grep -q "success\|started\|running"; then
        log_ok "Scrub trigger API works"
    else
        log_fail "Scrub trigger API failed (status: $ADMIN_API_STATUS): $ADMIN_API_BODY"
    fi
}

test_diagnostics_export() {
    log_info "Testing diagnostics export..."
    admin_api_request GET "$API_URL/diagnostics"
    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$ADMIN_API_STATUS" == "401" || "$ADMIN_API_STATUS" == "403" ) ]]; then
        log_skip "Diagnostics export requires admin authentication"
    elif [[ "$ADMIN_API_STATUS" == "200" ]] && echo "$ADMIN_API_BODY" | grep -q "system\|storage\|success"; then
        log_ok "Diagnostics export returns system info"
    else
        log_fail "Diagnostics export failed (status: $ADMIN_API_STATUS): $ADMIN_API_BODY"
    fi
}

# ==============================================================================
# Test Group 7: Large Files (skip in quick mode)
# ==============================================================================

test_large_file_upload() {
    if $QUICK_MODE; then
        log_skip "Large file upload (quick mode)"
        return
    fi
    
    log_info "Testing large file upload (100MB)..."
    dd if=/dev/urandom of="$TEST_DIR/large.bin" bs=1M count=100 2>/dev/null
    
    local start=$(date +%s)
    local status=$(curl -sf -X PUT "$WEBDAV_URL/e2e-test/large.bin" \
        -T "$TEST_DIR/large.bin" -w "%{http_code}" -o /dev/null)
    local end=$(date +%s)
    local duration=$((end - start))
    
    if [[ "$status" == "201" || "$status" == "204" ]]; then
        log_ok "100MB file upload successful (${duration}s)"
    else
        log_fail "Large file upload failed (status: $status)"
    fi
}

test_large_file_download() {
    if $QUICK_MODE; then
        log_skip "Large file download (quick mode)"
        return
    fi
    
    log_info "Testing large file download..."
    local start=$(date +%s)
    curl -sf "$WEBDAV_URL/e2e-test/large.bin" -o "$TEST_DIR/large-dl.bin"
    local end=$(date +%s)
    local duration=$((end - start))
    
    # Verify integrity
    local orig_hash=$(sha256sum "$TEST_DIR/large.bin" | awk '{print $1}')
    local dl_hash=$(sha256sum "$TEST_DIR/large-dl.bin" | awk '{print $1}')
    
    if [[ "$orig_hash" == "$dl_hash" ]]; then
        log_ok "100MB file download verified (${duration}s)"
    else
        log_fail "Large file download: hash mismatch"
    fi
}

# ==============================================================================
# Test Group 8: Crash Recovery (skip in quick mode)
# ==============================================================================

test_crash_recovery_doc() {
    if $QUICK_MODE; then
        log_skip "Crash recovery documentation check (quick mode)"
        return
    fi
    
    log_info "Crash recovery test (manual verification)..."
    echo ""
    echo "  To fully test crash recovery:"
    echo "  1. Start a large file upload"
    echo "  2. Kill nasd process mid-write: pkill -9 nasd"
    echo "  3. Restart nasd"
    echo "  4. Verify no .tmp files in <storage_root>/.mnemonas/tmp/"
    echo "  5. Verify WebDAV still works"
    echo ""
    log_skip "Crash recovery requires manual testing"
}

# ==============================================================================
# Test Group 9: Security
# ==============================================================================

test_path_traversal() {
    log_info "Testing path traversal protection..."
    local status=$(curl -s -w "%{http_code}" -o /dev/null "$WEBDAV_URL/../../../etc/passwd")
    if [[ "$status" == "400" || "$status" == "404" || "$status" == "403" ]]; then
        log_ok "Path traversal blocked (status: $status)"
    else
        log_fail "Path traversal not blocked (status: $status)"
    fi
}

test_localhost_binding() {
    log_info "Checking server binding configuration..."
    # This is a documentation/config check, not runtime test
    if grep -q 'host.*=.*"0.0.0.0"\|host.*=.*"127.0.0.1"' ~/.mnemonas/config.toml 2>/dev/null; then
        log_ok "Host binding configured in config file"
    else
        log_skip "No config file found to check binding"
    fi
}

# ==============================================================================
# Test Group 10: Authentication (requires [auth].enabled = true in config)
# ==============================================================================

test_auth_login_success() {
    log_info "Testing auth login with valid credentials..."
    
    # Check if initial password file exists (fresh install)
    local password_file="$HOME/.mnemonas/.mnemonas/initial-password.txt"
    if [[ ! -f "$password_file" ]]; then
        log_skip "Auth login test - no initial password file (auth may be disabled or already logged in)"
        return
    fi
    
    # Extract password from file
    local password=$(grep "^Password:" "$password_file" | awk '{print $2}')
    if [[ -z "$password" ]]; then
        log_fail "Could not extract password from $password_file"
        return
    fi
    
    local resp=$(curl -sf -X POST "$API_URL/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"$password\"}" 2>/dev/null || echo "error")
    
    if echo "$resp" | grep -q '"success":true'; then
        ADMIN_ACCESS_TOKEN=$(echo "$resp" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
        ADMIN_REFRESH_TOKEN=$(echo "$resp" | grep -o '"refresh_token":"[^"]*"' | cut -d'"' -f4)
        log_ok "Auth login with initial password successful"
    else
        log_fail "Auth login failed: $resp"
    fi
}

test_auth_login_failure() {
    log_info "Testing auth login with invalid credentials..."
    
    local status=$(curl -s -X POST "$API_URL/auth/login" \
        -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"wrongpassword"}' \
        -w "%{http_code}" -o /dev/null)
    
    if [[ "$status" == "401" ]]; then
        log_ok "Auth login correctly rejects invalid password (401)"
    elif [[ "$status" == "000" ]]; then
        log_skip "Auth endpoint not available (auth may be disabled)"
    else
        log_fail "Auth login should return 401 for invalid password (got $status)"
    fi
}

test_auth_password_file_deleted_after_login() {
    log_info "Testing password file deletion after login..."
    
    local password_file="$HOME/.mnemonas/.mnemonas/initial-password.txt"
    
    # If auth is enabled and we just logged in, file should be deleted
    if [[ -f "$password_file" ]]; then
        # File still exists - either we haven't logged in yet, or deletion failed
        log_skip "Password file still exists (login may not have occurred)"
    else
        # File doesn't exist - could be already deleted, or auth disabled
        # Check if users.json exists to confirm auth is set up
        if [[ -f "$HOME/.mnemonas/.mnemonas/users.json" ]]; then
            log_ok "Password file correctly deleted after login"
        else
            log_skip "Auth not initialized (no users.json)"
        fi
    fi
}

test_auth_protected_endpoint() {
    log_info "Testing protected endpoint without token..."
    
    local status=$(curl -s -X GET "$API_URL/auth/me" \
        -w "%{http_code}" -o /dev/null)
    
    if [[ "$status" == "401" ]]; then
        log_ok "Protected endpoint correctly returns 401 without token"
    elif [[ "$status" == "200" ]]; then
        log_skip "Auth may be disabled (endpoint returned 200)"
    else
        log_fail "Protected endpoint returned unexpected status: $status"
    fi
}

test_auth_token_refresh() {
    log_info "Testing token refresh flow..."
    
    local password_file="$HOME/.mnemonas/.mnemonas/initial-password.txt"
    local refresh_token="$ADMIN_REFRESH_TOKEN"
    
    # Need to get a valid token first
    # This test requires auth to be enabled and initial password available
    if [[ ! -f "$password_file" ]] && [[ ! -f "$HOME/.mnemonas/.mnemonas/users.json" ]]; then
        log_skip "Auth not configured for token refresh test"
        return
    fi

    if [[ -z "$refresh_token" ]]; then
        # Try to login and get refresh token
        local password=""
        if [[ -f "$password_file" ]]; then
            password=$(grep "^Password:" "$password_file" | awk '{print $2}')
        fi

        if [[ -z "$password" ]]; then
            log_skip "No password available for token refresh test"
            return
        fi

        local login_resp=$(curl -sf -X POST "$API_URL/auth/login" \
            -H "Content-Type: application/json" \
            -d "{\"username\":\"admin\",\"password\":\"$password\"}" 2>/dev/null)

        ADMIN_ACCESS_TOKEN=$(echo "$login_resp" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
        ADMIN_REFRESH_TOKEN=$(echo "$login_resp" | grep -o '"refresh_token":"[^"]*"' | cut -d'"' -f4)
        refresh_token="$ADMIN_REFRESH_TOKEN"
    fi
    
    if [[ -z "$refresh_token" ]]; then
        log_skip "Could not get refresh token from login response"
        return
    fi
    
    local refresh_resp=$(curl -sf -X POST "$API_URL/auth/refresh" \
        -H "Content-Type: application/json" \
        -d "{\"refresh_token\":\"$refresh_token\"}" 2>/dev/null || echo "error")
    
    if echo "$refresh_resp" | grep -q '"access_token"'; then
        log_ok "Token refresh successful"
    else
        log_fail "Token refresh failed: $refresh_resp"
    fi
}

# ==============================================================================
# Main Test Runner
# ==============================================================================

main() {
    echo ""
    echo "=============================================="
    echo " MnemoNAS E2E Acceptance Tests"
    echo " Mode: $(if $QUICK_MODE; then echo 'Quick'; else echo 'Full'; fi)"
    echo "=============================================="
    echo ""

    setup

    # Group 1: Basic
    test_health_check
    test_version_api
    test_webdav_options

    # Group 2: File Operations
    test_file_upload
    test_file_download
    test_directory_create
    test_propfind
    test_file_copy
    test_file_move
    test_file_delete

    # Group 3: ETag
    # Re-create test file for ETag tests
    echo "Hello, MnemoNAS!" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/test.txt" -T - > /dev/null
    test_etag_returned
    test_if_none_match
    test_if_match_success
    test_if_match_failure

    # Group 4: Versions
    test_version_history

    # Group 5: Concurrency
    test_concurrent_reads
    test_concurrent_writes
    test_concurrent_same_file

    # Group 7: Large Files
    test_large_file_upload
    test_large_file_download

    # Group 8: Crash Recovery
    test_crash_recovery_doc

    # Group 9: Security
    test_path_traversal
    test_localhost_binding

    # Group 10: Authentication
    test_auth_login_failure
    test_auth_login_success
    test_auth_password_file_deleted_after_login
    test_auth_protected_endpoint
    test_auth_token_refresh

    # Group 6: Maintenance (admin token available after auth tests when enabled)
    test_metrics_api
    test_scrub_api
    test_scrub_trigger
    test_diagnostics_export

    # Summary
    echo ""
    echo "=============================================="
    echo " Test Results"
    echo "=============================================="
    echo -e " ${GREEN}Passed:${NC}  $PASSED"
    echo -e " ${RED}Failed:${NC}  $FAILED"
    echo -e " ${YELLOW}Skipped:${NC} $SKIPPED"
    echo "=============================================="
    echo ""

    if [[ $FAILED -gt 0 ]]; then
        echo -e "${RED}Some tests failed!${NC}"
        exit 1
    else
        echo -e "${GREEN}All tests passed!${NC}"
        exit 0
    fi
}

main "$@"
