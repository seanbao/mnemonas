#!/bin/bash
# Performance benchmark script for MnemoNAS WebDAV
# Tests PROPFIND response time with varying directory sizes

set -euo pipefail

BASE_URL="${1:-http://localhost:8080}"
DAV_URL="$BASE_URL/dav"
STORAGE_ROOT="${MNEMONAS_STORAGE_ROOT:-$HOME/.mnemonas}"
TEST_DIR="$STORAGE_ROOT/files/benchmark-test"
CONFIG_FILE="${CONFIG_FILE:-$STORAGE_ROOT/config.toml}"
SECRETS_FILE="${SECRETS_FILE:-$STORAGE_ROOT/secrets.json}"
INTERNAL_DIR="${INTERNAL_DIR:-$STORAGE_ROOT/.mnemonas}"
INITIAL_PASSWORD_FILE="${INITIAL_PASSWORD_FILE:-$INTERNAL_DIR/initial-password.txt}"
WEBDAV_AUTH_ARGS=()
ADMIN_ACCESS_TOKEN="${MNEMONAS_ACCESS_TOKEN:-}"

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
    local auth_type="${MNEMONAS_WEBDAV_AUTH_TYPE:-$(read_config_value webdav auth_type)}"
    local username="${MNEMONAS_WEBDAV_USERNAME:-$(read_config_value webdav username)}"
    local password="${MNEMONAS_WEBDAV_PASSWORD:-$(read_config_value webdav password)}"

    if [[ -z "$auth_type" ]] && [[ -n "$username$password" ]]; then
        auth_type="basic"
    fi
    if [[ "$auth_type" != "basic" ]]; then
        return 0
    fi

    if [[ -z "$username" ]]; then
        username="admin"
    fi
    if [[ -z "$password" ]]; then
        password=$(read_secret_value webdav_password)
    fi
    if [[ -z "$password" ]]; then
        echo "WebDAV basic auth is enabled but no password was found; set MNEMONAS_WEBDAV_PASSWORD or update $CONFIG_FILE" >&2
        return 0
    fi

    WEBDAV_AUTH_ARGS=(-u "$username:$password")
}

configure_admin_auth() {
    local auth_enabled="${MNEMONAS_AUTH_ENABLED:-$(read_config_value auth enabled)}"

    if [[ -n "$ADMIN_ACCESS_TOKEN" ]] || [[ "$auth_enabled" != "true" ]] || [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        return 0
    fi

    local password
    password=$(grep '^Password:' "$INITIAL_PASSWORD_FILE" | awk '{print $2}' || true)
    if [[ -z "$password" ]]; then
        return 0
    fi

    local resp
    resp=$(command curl -sf -X POST "$BASE_URL/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"admin\",\"password\":\"$password\"}" 2>/dev/null || echo "")
    if echo "$resp" | grep -q '"success":true'; then
        ADMIN_ACCESS_TOKEN=$(echo "$resp" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
    fi
}

webdav_curl() {
    if [[ ${#WEBDAV_AUTH_ARGS[@]} -gt 0 ]]; then
        command curl "${WEBDAV_AUTH_ARGS[@]}" "$@"
        return
    fi
    command curl "$@"
}

metrics_curl() {
    if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
        command curl -H "Authorization: Bearer $ADMIN_ACCESS_TOKEN" "$@"
        return
    fi
    command curl "$@"
}

cleanup() {
    rm -rf "$TEST_DIR"
}

trap cleanup EXIT

run_propfind() {
    local path=$1
    local depth=$2
    local status

    if ! status=$(webdav_curl -sS -o /dev/null -w "%{http_code}" -X PROPFIND -H "Depth: $depth" "$DAV_URL$path"); then
        echo "PROPFIND $path failed to reach $DAV_URL$path" >&2
        return 1
    fi
    if [ "$status" != "207" ]; then
        echo "PROPFIND $path returned unexpected HTTP status: $status" >&2
        return 1
    fi
}

echo "=== MnemoNAS WebDAV Performance Benchmark ==="
echo "Base URL: $BASE_URL"
echo "Storage Root: $STORAGE_ROOT"
echo ""

configure_webdav_auth
configure_admin_auth

# Function to create test files
create_test_files() {
    local count=$1
    local dir="$TEST_DIR/dir-$count"
    
    echo "Creating $count test files in $dir..."
    mkdir -p "$dir"
    
    for i in $(seq 1 $count); do
        printf 'benchmark file %05d\n' "$i" > "$dir/file-$(printf '%05d' $i).txt"
    done
}

# Function to benchmark PROPFIND
benchmark_propfind() {
    local path=$1
    local depth=$2
    local desc=$3
    
    echo -n "PROPFIND $desc (Depth: $depth): "
    
    # Warm up cache
    run_propfind "$path" "$depth"
    
    # Measure time (3 runs, take average)
    local total=0
    for i in 1 2 3; do
        local start=$(date +%s%N)
        run_propfind "$path" "$depth"
        local end=$(date +%s%N)
        local duration=$(( (end - start) / 1000000 ))
        total=$((total + duration))
    done
    
    local avg=$((total / 3))
    echo "${avg}ms (avg of 3 runs)"
}

# Function to benchmark GET
benchmark_get() {
    local path=$1
    local desc=$2
    
    echo -n "GET $desc: "
    
    local start=$(date +%s%N)
    webdav_curl -s "$DAV_URL$path" > /dev/null 2>&1 || true
    local end=$(date +%s%N)
    local duration=$(( (end - start) / 1000000 ))
    
    echo "${duration}ms"
}

# Clean up old test data
cleanup
mkdir -p "$TEST_DIR"

echo "--- Creating test directories ---"

# Create directories with different file counts
create_test_files 10
create_test_files 100
create_test_files 500
create_test_files 1000

echo ""
echo "--- PROPFIND Benchmarks ---"

# Test root directory
benchmark_propfind "/" "1" "root"

# Test different directory sizes
benchmark_propfind "/benchmark-test/dir-10" "1" "10 files"
benchmark_propfind "/benchmark-test/dir-100" "1" "100 files"
benchmark_propfind "/benchmark-test/dir-500" "1" "500 files"
benchmark_propfind "/benchmark-test/dir-1000" "1" "1000 files"

echo ""
echo "--- Cache Effect Test ---"

# First request (cache miss)
echo "Testing cache effect on 1000-file directory..."
echo -n "First request (cold): "
start=$(date +%s%N)
run_propfind "/benchmark-test/dir-1000" "1"
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

# Second request (cache hit)
echo -n "Second request (cached): "
start=$(date +%s%N)
run_propfind "/benchmark-test/dir-1000" "1"
end=$(date +%s%N)
echo "$(( (end - start) / 1000000 ))ms"

echo ""
echo "--- API Metrics ---"
if [[ "${MNEMONAS_AUTH_ENABLED:-$(read_config_value auth enabled)}" == "true" ]] && [[ -z "$ADMIN_ACCESS_TOKEN" ]]; then
    echo "(metrics request skipped: no admin token available; set MNEMONAS_ACCESS_TOKEN if bootstrap login is unavailable)"
elif metrics_json=$(metrics_curl -fsS "$BASE_URL/api/v1/metrics"); then
    printf '%s' "$metrics_json" | python3 -c "
import sys, json
data = json.load(sys.stdin)['data']
print(f\"Total requests: {data['requests']['total']}\")
print(f\"Avg latency: {data['latency']['avg_ms']:.2f}ms\")
print(f\"Max latency: {data['latency']['max_ms']:.2f}ms\")
print(f\"Error rate: {data['requests']['error_rate']*100:.2f}%\")
" 2>/dev/null || echo "(metrics parsing failed)"
else
    echo "(metrics request failed)"
fi

echo ""
echo "--- Cleanup ---"
cleanup
echo "Test files removed."

echo ""
echo "=== Benchmark Complete ==="
