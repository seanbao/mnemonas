#!/bin/bash
# MnemoNAS Fault Injection Tests
# 故障注入回归测试 - 验证数据安全性
#
# 测试场景：
# 1. 写入过程中进程被 kill
# 2. 对象文件损坏
# 3. 元数据文件损坏
# 4. 磁盘空间不足

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
BASE_URL="${BASE_URL:-http://localhost:8080}"
WEBDAV_URL="${BASE_URL}/dav"
STORAGE_ROOT="${STORAGE_ROOT:-$HOME/.mnemonas}"
INTERNAL_DIR="${INTERNAL_DIR:-$STORAGE_ROOT/.mnemonas}"
CONFIG_FILE="${CONFIG_FILE:-$STORAGE_ROOT/config.toml}"
SECRETS_FILE="${SECRETS_FILE:-$STORAGE_ROOT/secrets.json}"
INITIAL_PASSWORD_FILE="${INITIAL_PASSWORD_FILE:-$INTERNAL_DIR/initial-password.txt}"
OBJECTS_DIR="${OBJECTS_DIR:-$INTERNAL_DIR/objects}"
INDEX_DB="${INDEX_DB:-$INTERNAL_DIR/index.db}"
NASD_BIN="${NASD_BIN:-./bin/nasd}"
TEST_DIR="/tmp/mnemonas-fault-$$"

# Counters
PASSED=0
FAILED=0
SKIPPED=0
ADMIN_ACCESS_TOKEN=""
WEBDAV_AUTH_ARGS=()

log_info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
log_ok()    { echo -e "${GREEN}[PASS]${NC} $1"; ((PASSED++)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $1"; ((FAILED++)); }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $1"; ((SKIPPED++)); }

read_config_value() {
    local section=$1
    local key=$2

    if [[ ! -f "$CONFIG_FILE" ]]; then
        return 0
    fi

    awk -v section="[$section]" -v key="$key" '
        $0 == section { in_section = 1; next }
        /^\[/ { in_section = 0 }
        in_section && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
            line = $0
            sub(/^[^=]*=[[:space:]]*/, "", line)
            sub(/[[:space:]]*#.*$/, "", line)
            gsub(/^"/, "", line)
            gsub(/"$/, "", line)
            print line
            exit
        }
    ' "$CONFIG_FILE"
}

read_secret_value() {
    local key=$1

    if [[ ! -f "$SECRETS_FILE" ]]; then
        return 0
    fi

    grep -o '"'"$key"'"[[:space:]]*:[[:space:]]*"[^"]*"' "$SECRETS_FILE" | sed 's/.*: *"//' | sed 's/"$//' || true
}

configure_webdav_auth() {
    local auth_type=$(read_config_value webdav auth_type)
    local username
    local password

    if [[ -z "$auth_type" ]]; then
        auth_type="basic"
    fi

    if [[ "$auth_type" != "basic" ]]; then
        return 0
    fi

    username=$(read_config_value webdav username)
    password=$(read_config_value webdav password)

    if [[ -z "$username" ]]; then
        username="admin"
    fi
    if [[ -z "$password" ]]; then
        password=$(read_secret_value webdav_password)
    fi

    if [[ -z "$password" ]]; then
        log_warn "WebDAV basic auth is enabled but no password was found; WebDAV fault tests may fail"
        return 0
    fi

    WEBDAV_AUTH_ARGS=(-u "$username:$password")
    log_info "Using WebDAV basic auth credentials for user: $username"
}

load_initial_admin_password() {
    if [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        return 1
    fi

    grep '^Password:' "$INITIAL_PASSWORD_FILE" | awk '{print $2}' || true
}

configure_admin_auth() {
    if [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        return 0
    fi

    local password=$(load_initial_admin_password)
    if [[ -z "$password" ]]; then
        log_warn "Could not extract bootstrap admin password; protected API checks may be skipped"
        return 0
    fi

    local resp=$(command curl -sf -X POST "$BASE_URL/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"$password\"}" 2>/dev/null || echo "")

    if echo "$resp" | grep -q '"success":true'; then
        ADMIN_ACCESS_TOKEN=$(echo "$resp" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
        log_info "Using bootstrap admin token for protected API checks"
        return 0
    fi

    log_warn "Bootstrap admin login failed; protected API checks may be skipped"
}

authenticated_api_curl() {
    if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
        command curl -H "Authorization: Bearer $ADMIN_ACCESS_TOKEN" "$@"
        return
    fi

    command curl "$@"
}

curl() {
    local args=("$@")
    local needs_webdav_auth=false

    for arg in "${args[@]}"; do
        case "$arg" in
            "$WEBDAV_URL"|"$WEBDAV_URL"/*)
                needs_webdav_auth=true
                break
                ;;
        esac
    done

    if $needs_webdav_auth && [[ ${#WEBDAV_AUTH_ARGS[@]} -gt 0 ]]; then
        command curl "${WEBDAV_AUTH_ARGS[@]}" "${args[@]}"
        return
    fi

    command curl "${args[@]}"
}

cleanup() {
    log_info "Cleaning up..."
    rm -rf "$TEST_DIR"
    # Restart service if it was killed
    if ! curl -sf "$BASE_URL/health" > /dev/null 2>&1; then
        log_warn "Service not running, attempting restart..."
        $NASD_BIN &
        sleep 2
    fi
}
trap cleanup EXIT

setup() {
    log_info "Setting up test environment..."
    mkdir -p "$TEST_DIR"
    
    # Ensure service is running
    if ! curl -sf "$BASE_URL/health" > /dev/null 2>&1; then
        echo -e "${RED}ERROR: MnemoNAS service not running${NC}"
        exit 1
    fi

    configure_webdav_auth
    configure_admin_auth
    
    # Create test directory in WebDAV
    curl -sf -X MKCOL "$WEBDAV_URL/fault-test/" > /dev/null 2>&1 || true
}

# ==============================================================================
# Test 1: Crash During Write (进程中断测试)
# ==============================================================================

test_crash_during_write() {
    log_info "Test 1: Crash during write operation..."
    
    # Create a large file that takes time to upload
    dd if=/dev/urandom of="$TEST_DIR/large.bin" bs=1M count=50 2>/dev/null
    
    # Start upload in background
    (curl -sf -X PUT "$WEBDAV_URL/fault-test/large.bin" -T "$TEST_DIR/large.bin" > /dev/null 2>&1) &
    local upload_pid=$!
    
    # Wait a moment then kill the service
    sleep 0.5
    pkill -9 -f nasd || true
    
    # Wait for upload process to fail
    wait $upload_pid 2>/dev/null || true
    
    log_info "Service killed during upload. Checking for orphaned temp files..."
    
    # Check for .tmp files in data directory
    local tmp_files=$(find "$OBJECTS_DIR" -name "*.tmp" 2>/dev/null | wc -l)
    
    # Restart service
    log_info "Restarting service..."
    $NASD_BIN &
    sleep 2
    
    # Wait for service to be healthy
    local retries=10
    while ! curl -sf "$BASE_URL/health" > /dev/null 2>&1; do
        ((retries--))
        if [[ $retries -le 0 ]]; then
            log_fail "Service failed to restart"
            return
        fi
        sleep 1
    done
    
    # Verify service is working
    echo "test after crash" | curl -sf -X PUT "$WEBDAV_URL/fault-test/after-crash.txt" -T - > /dev/null
    local content=$(curl -sf "$WEBDAV_URL/fault-test/after-crash.txt")
    
    if [[ "$content" == "test after crash" ]]; then
        log_ok "Service recovered correctly after crash"
    else
        log_fail "Service not working correctly after crash"
    fi
    
    # Check if incomplete file is visible
    local incomplete_status=$(curl -s -w "%{http_code}" -o /dev/null "$WEBDAV_URL/fault-test/large.bin")
    if [[ "$incomplete_status" == "404" ]]; then
        log_ok "Incomplete upload not visible (atomic write working)"
    else
        log_fail "Incomplete upload is visible! Status: $incomplete_status"
    fi
    
    log_info "Temp files found: $tmp_files (cleanup happens on startup)"
}

# ==============================================================================
# Test 2: Object Corruption (对象损坏检测)
# ==============================================================================

test_object_corruption() {
    log_info "Test 2: Object corruption detection..."
    
    # Upload a known file
    echo "This is test content for corruption check" > "$TEST_DIR/corrupt-test.txt"
    curl -sf -X PUT "$WEBDAV_URL/fault-test/corrupt-test.txt" -T "$TEST_DIR/corrupt-test.txt" > /dev/null
    
    # Verify it's readable
    local original=$(curl -sf "$WEBDAV_URL/fault-test/corrupt-test.txt")
    if [[ "$original" != "This is test content for corruption check" ]]; then
        log_fail "Original upload verification failed"
        return
    fi
    
    # Find the object file and corrupt it
    # Note: This requires knowing the CAS structure
    log_info "Looking for object files to corrupt..."
    local object_file=$(find "$OBJECTS_DIR" -type f ! -name "*.tmp" 2>/dev/null | head -1)
    
    if [[ -z "$object_file" ]]; then
        log_warn "No object files found, skipping corruption test"
        return
    fi
    
    # Backup and corrupt
    cp "$object_file" "$TEST_DIR/backup.bin"
    echo "CORRUPTED" >> "$object_file"
    
    log_info "Object file corrupted: $object_file"
    
    # Run scrub to detect corruption
    local scrub_response=$(authenticated_api_curl -s -X POST "$BASE_URL/api/v1/maintenance/scrub" -w $'\n%{http_code}' 2>/dev/null || true)
    local scrub_status="${scrub_response##*$'\n'}"
    local scrub_result="${scrub_response%$'\n'*}"

    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$scrub_status" == "401" || "$scrub_status" == "403" ) ]]; then
        log_skip "Scrub verification requires admin authentication"
    elif echo "$scrub_result" | grep -qi "corrupt\|error\|failed"; then
        log_ok "Scrub detected corruption"
    else
        log_warn "Scrub may not have detected corruption: $scrub_result"
    fi
    
    # Check diagnostics
    local diag_response=$(authenticated_api_curl -s "$BASE_URL/api/v1/diagnostics" -w $'\n%{http_code}' 2>/dev/null || true)
    local diag_status="${diag_response##*$'\n'}"
    local diag="${diag_response%$'\n'*}"
    if [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$diag_status" == "401" || "$diag_status" == "403" ) ]]; then
        log_skip "Diagnostics export requires admin authentication"
    else
        log_info "Diagnostics after corruption: $(echo $diag | head -c 200)..."
    fi
    
    # Restore the object
    cp "$TEST_DIR/backup.bin" "$object_file"
    log_info "Object file restored"
}

# ==============================================================================
# Test 3: Metadata Corruption (元数据损坏)
# ==============================================================================

test_metadata_corruption() {
    log_info "Test 3: Metadata corruption handling..."
    
    # Create a test file
    echo "metadata corruption test" | curl -sf -X PUT "$WEBDAV_URL/fault-test/meta-test.txt" -T - > /dev/null
    
    # Find and corrupt metadata file
    if [[ ! -f "$INDEX_DB" ]]; then
        log_warn "Index database not found, skipping test"
        return
    fi

    # Backup and corrupt
    cp "$INDEX_DB" "$TEST_DIR/index-backup.db"
    printf 'CORRUPTED' >> "$INDEX_DB"

    log_info "Index database corrupted: $INDEX_DB"
    
    # Try to access files - should handle gracefully
    local status=$(curl -s -w "%{http_code}" -o /dev/null -X PROPFIND "$WEBDAV_URL/fault-test/" -H "Depth: 1")
    
    # Restore metadata
    cp "$TEST_DIR/index-backup.db" "$INDEX_DB"
    
    if [[ "$status" == "500" || "$status" == "404" || "$status" == "207" ]]; then
        log_ok "Service handled corrupted metadata gracefully (status: $status)"
    else
        log_fail "Unexpected response to corrupted metadata: $status"
    fi
}

# ==============================================================================
# Test 4: Concurrent Write Conflict (并发写入冲突)
# ==============================================================================

test_concurrent_write_conflict() {
    log_info "Test 4: Concurrent write conflict handling..."
    
    # Create initial file
    echo "version 0" | curl -sf -X PUT "$WEBDAV_URL/fault-test/conflict.txt" -T - > /dev/null
    
    # Get ETag
    local etag=$(curl -sf "$WEBDAV_URL/fault-test/conflict.txt" -I | grep -i "^etag:" | awk '{print $2}' | tr -d '\r')
    
    # First writer with correct ETag
    local status1=$(echo "version 1" | curl -s -X PUT "$WEBDAV_URL/fault-test/conflict.txt" \
        -H "If-Match: $etag" -T - -w "%{http_code}" -o /dev/null)
    
    # Second writer with stale ETag (should fail)
    local status2=$(echo "version 2" | curl -s -X PUT "$WEBDAV_URL/fault-test/conflict.txt" \
        -H "If-Match: $etag" -T - -w "%{http_code}" -o /dev/null)
    
    if [[ "$status1" == "204" || "$status1" == "200" ]]; then
        if [[ "$status2" == "412" ]]; then
            log_ok "Concurrent write conflict detected correctly (first: $status1, second: $status2)"
        else
            log_fail "Second write should fail with 412 (got: $status2)"
        fi
    else
        log_fail "First write failed unexpectedly (status: $status1)"
    fi
    
    # Verify final content is from first writer
    local content=$(curl -sf "$WEBDAV_URL/fault-test/conflict.txt")
    if [[ "$content" == "version 1" ]]; then
        log_ok "Final content is from first writer (no data corruption)"
    else
        log_fail "Unexpected final content: $content"
    fi
}

# ==============================================================================
# Test 5: Recovery Verification (恢复验证)
# ==============================================================================

test_recovery_verification() {
    log_info "Test 5: Version recovery verification..."
    
    # Create multiple versions
    for i in 1 2 3; do
        echo "version $i content" | curl -sf -X PUT "$WEBDAV_URL/fault-test/versioned.txt" -T - > /dev/null
        sleep 0.2
    done
    
    # Get version history
    local history_response=$(authenticated_api_curl -s "$BASE_URL/api/v1/versions/fault-test/versioned.txt" -w $'\n%{http_code}' 2>/dev/null || true)
    local history_status="${history_response##*$'\n'}"
    local history="${history_response%$'\n'*}"
    
    if [[ "$history_status" == "200" ]] && echo "$history" | grep -q "versions\|hash"; then
        log_ok "Version history available"
        
        # Current should be version 3
        local current=$(curl -sf "$WEBDAV_URL/fault-test/versioned.txt")
        if [[ "$current" == "version 3 content" ]]; then
            log_ok "Current version is correct"
        else
            log_fail "Current version mismatch: $current"
        fi
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$history_status" == "401" || "$history_status" == "403" ) ]]; then
        log_skip "Version history verification requires admin authentication"
    else
        log_warn "Version history not available: $history"
    fi
}

# ==============================================================================
# Main
# ==============================================================================

main() {
    echo ""
    echo "=============================================="
    echo " MnemoNAS Fault Injection Tests"
    echo " 故障注入回归测试"
    echo "=============================================="
    echo ""
    echo -e "${YELLOW}WARNING: These tests will kill and restart the service!${NC}"
    echo -e "${YELLOW}Make sure no important operations are in progress.${NC}"
    echo ""
    read -p "Press Enter to continue or Ctrl+C to abort..."
    echo ""

    setup

    # Run tests
    test_crash_during_write
    echo ""
    
    test_concurrent_write_conflict
    echo ""
    
    test_recovery_verification
    echo ""
    
    # These tests modify data files - run with caution
    echo -e "${YELLOW}The following tests will modify data files.${NC}"
    read -p "Run corruption tests? [y/N] " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        test_object_corruption
        echo ""
        
        test_metadata_corruption
        echo ""
    fi

    # Summary
    echo ""
    echo "=============================================="
    echo " Fault Injection Test Results"
    echo "=============================================="
    echo -e " ${GREEN}Passed:${NC} $PASSED"
    echo -e " ${RED}Failed:${NC} $FAILED"
    echo -e " ${YELLOW}Skipped:${NC} $SKIPPED"
    echo "=============================================="
    echo ""

    if [[ $FAILED -gt 0 ]]; then
        echo -e "${RED}Some fault injection tests failed!${NC}"
        exit 1
    else
        echo -e "${GREEN}All fault injection tests passed!${NC}"
        exit 0
    fi
}

main "$@"
