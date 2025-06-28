#!/bin/bash
# Performance benchmark script for MnemoNAS WebDAV
# Tests PROPFIND response time with varying directory sizes

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BASE_URL_ARG="${1:-}"
BASE_URL_EXPLICIT="${BASE_URL+x}"
STORAGE_ROOT_EXPLICIT="${MNEMONAS_STORAGE_ROOT+x}"
BASE_URL="${BASE_URL_ARG:-${BASE_URL:-http://localhost:8080}}"
DAV_URL="$BASE_URL/dav"
STORAGE_ROOT="${MNEMONAS_STORAGE_ROOT:-$HOME/.mnemonas}"
TEST_DIR="$STORAGE_ROOT/files/benchmark-test"
CONFIG_FILE="${CONFIG_FILE:-$STORAGE_ROOT/config.toml}"
SECRETS_FILE="${SECRETS_FILE:-$STORAGE_ROOT/secrets.json}"
INTERNAL_DIR="${INTERNAL_DIR:-$STORAGE_ROOT/.mnemonas}"
INITIAL_PASSWORD_FILE="${INITIAL_PASSWORD_FILE:-$INTERNAL_DIR/initial-password.txt}"
WEBDAV_AUTH_ARGS=()
ADMIN_ACCESS_TOKEN="${MNEMONAS_ACCESS_TOKEN:-}"
ALLOW_REAL_STORAGE="${ALLOW_REAL_STORAGE:-0}"
CLEANUP_ENABLED=0

require_safe_http_url() {
    local value="$1"
    local label="$2"

    if [[ -z "$value" ]]; then
        echo "ERROR: $label must not be empty" >&2
        exit 1
    fi
    if [[ "$value" == *[[:space:]]* ]]; then
        echo "ERROR: $label must not contain whitespace: $value" >&2
        exit 1
    fi
    if [[ ! "$value" =~ ^https?://[^[:space:]]+$ ]]; then
        echo "ERROR: $label must be an http(s) URL: $value" >&2
        exit 1
    fi
}

require_explicit_benchmark_target() {
    if [[ -z "$BASE_URL_ARG" && -z "$BASE_URL_EXPLICIT" ]]; then
        echo "ERROR: explicit base URL is required for scripts/benchmark.sh" >&2
        echo "Use 'make bench' or './scripts/run-benchmark-isolated.sh' for the default isolated target." >&2
        exit 1
    fi
    if [[ -z "$BASE_URL" ]]; then
        echo "ERROR: base URL must not be empty" >&2
        exit 1
    fi
    require_safe_http_url "$BASE_URL" "base URL"

    if [[ -z "$STORAGE_ROOT_EXPLICIT" ]]; then
        echo "ERROR: explicit MNEMONAS_STORAGE_ROOT is required for scripts/benchmark.sh" >&2
        echo "The benchmark creates and deletes files under MNEMONAS_STORAGE_ROOT/files/benchmark-test." >&2
        exit 1
    fi
    if [[ -z "$STORAGE_ROOT" ]]; then
        echo "ERROR: MNEMONAS_STORAGE_ROOT must not be empty" >&2
        exit 1
    fi

    if path_has_parent_segment "$STORAGE_ROOT"; then
        echo "ERROR: MNEMONAS_STORAGE_ROOT must not contain '..' path segments: $STORAGE_ROOT" >&2
        exit 1
    fi
    if [[ "$STORAGE_ROOT" != /* ]]; then
        echo "ERROR: MNEMONAS_STORAGE_ROOT must be an absolute path: $STORAGE_ROOT" >&2
        exit 1
    fi
    if is_protected_storage_root "$STORAGE_ROOT"; then
        echo "ERROR: MNEMONAS_STORAGE_ROOT points at a protected system directory: $STORAGE_ROOT" >&2
        exit 1
    fi
    require_no_symlink_components "$STORAGE_ROOT" "MNEMONAS_STORAGE_ROOT"

    if [[ "$ALLOW_REAL_STORAGE" != "1" ]]; then
        case "$STORAGE_ROOT" in
            /tmp/*|"$PROJECT_ROOT"/*) ;;
            *)
                echo "ERROR: MNEMONAS_STORAGE_ROOT must be under /tmp or this checkout unless ALLOW_REAL_STORAGE=1 is set: $STORAGE_ROOT" >&2
                exit 1
                ;;
        esac
    fi

    if [[ -n "${HOME:-}" && "$STORAGE_ROOT" == "$HOME/.mnemonas" && "$ALLOW_REAL_STORAGE" != "1" ]]; then
        echo "ERROR: refusing to benchmark against default personal storage root: $STORAGE_ROOT" >&2
        echo "Use 'make bench' or set ALLOW_REAL_STORAGE=1 only for an intentionally disposable target." >&2
        exit 1
    fi
}

path_has_parent_segment() {
    local candidate="$1"
    local segment
    local -a segments
    IFS='/' read -r -a segments <<< "$candidate"
    for segment in "${segments[@]}"; do
        if [[ "$segment" == ".." ]]; then
            return 0
        fi
    done
    return 1
}

require_no_symlink_components() {
    local value="$1"
    local label="$2"
    local trimmed="$value"
    local current="."
    local -a segments

    if [[ "$value" == /* ]]; then
        trimmed="${value#/}"
        current="/"
    fi
    trimmed="${trimmed%/}"

    IFS='/' read -r -a segments <<< "$trimmed"
    for segment in "${segments[@]}"; do
        [[ -n "$segment" && "$segment" != "." ]] || continue
        if [[ "$current" == "/" ]]; then
            current="/$segment"
        else
            current="$current/$segment"
        fi
        if [[ -L "$current" ]]; then
            echo "ERROR: $label must not contain symlink path components: $current" >&2
            exit 1
        fi
        [[ -e "$current" ]] || break
    done
}

normalize_absolute_path() {
    local value="$1"

    while [[ "$value" != "/" && "$value" == */ ]]; do
        value="${value%/}"
    done
    printf '%s\n' "$value"
}

is_protected_storage_root() {
    local value

    value="$(normalize_absolute_path "$1")"
    case "$value" in
        /|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

read_config_value() {
    local section=$1
    local key=$2

    if [[ ! -f "$CONFIG_FILE" ]]; then
        return 0
    fi

    awk -v section="[$section]" -v key="$key" '
        function strip_comment(text,    i, c, quote, escaped, out) {
            quote = ""
            escaped = 0
            out = ""
            for (i = 1; i <= length(text); i++) {
                c = substr(text, i, 1)
                if (quote == "\"") {
                    out = out c
                    if (escaped) {
                        escaped = 0
                        continue
                    }
                    if (c == "\\") {
                        escaped = 1
                        continue
                    }
                    if (c == quote) {
                        quote = ""
                    }
                    continue
                }
                if (quote == "\047") {
                    out = out c
                    if (c == quote) {
                        quote = ""
                    }
                    continue
                }
                if (c == "\"" || c == "\047") {
                    quote = c
                    out = out c
                    continue
                }
                if (c == "#") {
                    break
                }
                out = out c
            }
            return out
        }
        {
            line = strip_comment($0)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
            section_line = line
            if (section_line ~ /^\[/) {
                sub(/^\[[[:space:]]*/, "[", section_line)
                sub(/[[:space:]]*\]$/, "]", section_line)
                gsub(/[[:space:]]*\.[[:space:]]*/, ".", section_line)
            }
        }
        section_line == section { in_section = 1; next }
        section_line ~ /^\[/ { in_section = 0 }
        in_section && line ~ "^[[:space:]]*" key "[[:space:]]*=" {
            sub(/^[^=]*=[[:space:]]*/, "", line)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
            gsub(/^"/, "", line)
            gsub(/"$/, "", line)
            gsub(/^\047/, "", line)
            gsub(/\047$/, "", line)
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
    if [[ "$CLEANUP_ENABLED" != "1" ]]; then
        return
    fi
    rm -rf -- "$TEST_DIR"
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

require_explicit_benchmark_target
CLEANUP_ENABLED=1
configure_webdav_auth
configure_admin_auth

# Function to create test files
create_test_files() {
    local count=$1
    local dir="$TEST_DIR/dir-$count"
    
    echo "Creating $count test files in $dir..."
    mkdir -p "$dir"
    
    for i in $(seq 1 "$count"); do
        printf 'benchmark file %05d\n' "$i" > "$dir/file-$(printf '%05d' "$i").txt"
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
        local start
        local end
        start=$(date +%s%N)
        run_propfind "$path" "$depth"
        end=$(date +%s%N)
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
    
    local start
    local end
    start=$(date +%s%N)
    webdav_curl -s "$DAV_URL$path" > /dev/null 2>&1 || true
    end=$(date +%s%N)
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
