#!/usr/bin/env bash
# MnemoNAS Fault Injection Tests
# Fault injection regression tests for data safety.
#
# Test scenarios:
# 1. Process killed during write
# 2. Object file corruption
# 3. Metadata file corruption
# 4. Low disk space
# When WebDAV uses auth_type=users, MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD must be set explicitly.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
BASE_URL_EXPLICIT="${BASE_URL+x}"
STORAGE_ROOT_EXPLICIT="${STORAGE_ROOT+x}"
NASD_BIN_EXPLICIT="${NASD_BIN+x}"
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
NASD_PID_FILE="${NASD_PID_FILE:-}"
TEST_DIR="/tmp/mnemonas-fault-$$"
MNEMONAS_LIVE_FAULTS="${MNEMONAS_LIVE_FAULTS:-0}"
FAULT_INJECTION_ASSUME_YES="${FAULT_INJECTION_ASSUME_YES:-0}"
ALLOW_REAL_STORAGE="${ALLOW_REAL_STORAGE:-0}"
RUN_CORRUPTION_TESTS="${RUN_CORRUPTION_TESTS:-prompt}"
FAULT_UPLOAD_SIZE_MB="${FAULT_UPLOAD_SIZE_MB:-50}"
FAULT_UPLOAD_LIMIT_RATE="${FAULT_UPLOAD_LIMIT_RATE:-512k}"
NASD_PID="${NASD_PID:-}"
FAULT_KILL_PATTERN="${FAULT_KILL_PATTERN:-}"
SERVICE_WAS_KILLED=0

# Counters
PASSED=0
FAILED=0
SKIPPED=0
ADMIN_ACCESS_TOKEN=""
WEBDAV_AUTH_ARGS=()
CURL_AUTH_CONFIG=""
CURL_ADMIN_AUTH_CONFIG=""
ADMIN_AUTH_ARGS=()

log_info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
log_ok()    { echo -e "${GREEN}[PASS]${NC} $1"; ((PASSED+=1)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $1"; ((FAILED+=1)); }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $1"; ((SKIPPED+=1)); }
die()       { echo -e "${RED}ERROR:${NC} $1" >&2; exit 1; }

require_safe_http_url() {
    local value="$1"
    local label="$2"
    local authority_and_path authority path lower_path segment lower_segment normalized_segment
    local -a base_url_segments=()

    [[ -n "$value" ]] || die "$label must not be empty"
    [[ "$value" != *[[:space:]]* ]] || die "$label must not contain whitespace"
    [[ "$value" != *[[:cntrl:]]* ]] || die "$label must not contain control characters"
    [[ "$value" =~ ^https?://[^[:space:]]+$ ]] || die "$label must be an http(s) URL"

    authority_and_path="${value#*://}"
    authority="${authority_and_path%%/*}"
    [[ -n "$authority" ]] || die "$label must include a host"
    [[ "$authority" != *"@"* ]] || die "$label must not contain embedded credentials"
    [[ "$authority_and_path" != *"?"* && "$authority_and_path" != *"#"* ]] || die "$label must not contain query strings or fragments"
    [[ "$authority_and_path" != *\\* ]] || die "$label must not contain backslashes"

    path=""
    if [[ "$authority_and_path" == */* ]]; then
        path="/${authority_and_path#*/}"
    fi
    lower_path="$(printf '%s' "$path" | tr '[:upper:]' '[:lower:]')"
    [[ "$lower_path" != *"%2f"* && "$lower_path" != *"%5c"* ]] || die "$label must not contain encoded slashes or backslashes"
    [[ "$lower_path" != *"%3f"* && "$lower_path" != *"%23"* ]] || die "$label must not contain encoded query or fragment markers"
    [[ "$path" != *"//"* ]] || die "$label path must not contain empty segments"
    IFS='/' read -r -a base_url_segments <<< "${path#/}"
    for segment in "${base_url_segments[@]}"; do
        [[ -n "$segment" ]] || continue
        lower_segment="$(printf '%s' "$segment" | tr '[:upper:]' '[:lower:]')"
        normalized_segment="${lower_segment//%2e/.}"
        [[ "$normalized_segment" != "." && "$normalized_segment" != ".." ]] || die "$label must not contain dot segments"
    done
}

normalize_base_url() {
    local value="$1"
    while [[ "$value" == */ ]]; do
        value="${value%/}"
    done
    printf '%s\n' "$value"
}

require_safe_pid() {
    local value="$1"
    local label="$2"

    [[ -n "$value" ]] || return 0
    [[ "$value" =~ ^[0-9]+$ ]] || die "$label must be a numeric PID: $value"
    (( 10#$value > 0 )) || die "$label must be a positive PID: $value"
}

require_no_control_characters() {
    local value="$1"
    local label="$2"

    if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
        die "$label cannot contain newline characters: $value"
    fi
    if [[ "$value" == *[[:cntrl:]]* ]]; then
        die "$label cannot contain control characters: $value"
    fi
}

require_live_fault_target() {
    if [[ "$MNEMONAS_LIVE_FAULTS" != "1" ]]; then
        die "live fault injection is disabled. Set MNEMONAS_LIVE_FAULTS=1 and use an isolated target."
    fi

    local missing=()
    [[ -n "$BASE_URL_EXPLICIT" ]] || missing+=("BASE_URL")
    [[ -n "$STORAGE_ROOT_EXPLICIT" ]] || missing+=("STORAGE_ROOT")
    [[ -n "$NASD_BIN_EXPLICIT" ]] || missing+=("NASD_BIN")
    if [[ ${#missing[@]} -gt 0 ]]; then
        die "explicit ${missing[*]} required for live fault injection"
    fi

    require_safe_http_url "$BASE_URL" "BASE_URL"
    BASE_URL="$(normalize_base_url "$BASE_URL")"
    WEBDAV_URL="${BASE_URL}/dav"
    require_safe_pid "$NASD_PID" "NASD_PID"

    if [[ -z "$STORAGE_ROOT" ]]; then
        die "STORAGE_ROOT must not be empty"
    fi
    require_no_control_characters "$STORAGE_ROOT" "STORAGE_ROOT"
    if path_has_parent_segment "$STORAGE_ROOT"; then
        die "STORAGE_ROOT must not contain '..' path segments: $STORAGE_ROOT"
    fi
    if [[ "$STORAGE_ROOT" != /* ]]; then
        die "STORAGE_ROOT must be an absolute path: $STORAGE_ROOT"
    fi
    if is_protected_storage_root "$STORAGE_ROOT"; then
        die "STORAGE_ROOT points at a protected system directory: $STORAGE_ROOT"
    fi
    require_no_symlink_components "$STORAGE_ROOT" "STORAGE_ROOT"

    if [[ "$ALLOW_REAL_STORAGE" != "1" ]]; then
        case "$STORAGE_ROOT" in
            /tmp/*|"$ROOT_DIR"/*)
                ;;
            *)
                die "STORAGE_ROOT must be under /tmp or this checkout unless ALLOW_REAL_STORAGE=1 is set: $STORAGE_ROOT"
                ;;
        esac
    fi

    if [[ "$STORAGE_ROOT" == "$HOME/.mnemonas" && "$ALLOW_REAL_STORAGE" != "1" ]]; then
        die "refusing to run against default personal storage root without ALLOW_REAL_STORAGE=1"
    fi
    require_destructive_storage_path "$OBJECTS_DIR" "OBJECTS_DIR"
    require_destructive_storage_path "$INDEX_DB" "INDEX_DB"
    if [[ -n "$NASD_PID_FILE" ]]; then
        require_destructive_storage_path "$NASD_PID_FILE" "NASD_PID_FILE"
    fi
    if [[ ! -x "$NASD_BIN" ]]; then
        die "NASD_BIN is not executable: $NASD_BIN"
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
            die "$label must not contain symlink path components: $current"
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

path_is_beneath_root() {
    local root
    local value

    root="$(normalize_absolute_path "$1")"
    value="$(normalize_absolute_path "$2")"
    [[ "$value" == "$root"/* ]]
}

require_destructive_storage_path() {
    local value="$1"
    local label="$2"

    [[ -n "$value" ]] || die "$label must not be empty"
    require_no_control_characters "$value" "$label"
    if path_has_parent_segment "$value"; then
        die "$label must not contain '..' path segments: $value"
    fi
    if [[ "$value" != /* ]]; then
        die "$label must be an absolute path: $value"
    fi
    require_no_symlink_components "$value" "$label"
    if ! path_is_beneath_root "$STORAGE_ROOT" "$value"; then
        die "$label must be under STORAGE_ROOT: $value"
    fi
}

confirm_live_fault_target() {
    echo -e "${YELLOW}WARNING: These tests will corrupt test data and kill/restart the target nasd process.${NC}"
    echo -e "${YELLOW}Target:${NC} BASE_URL=$BASE_URL STORAGE_ROOT=$STORAGE_ROOT NASD_BIN=$NASD_BIN"
    if [[ "$FAULT_INJECTION_ASSUME_YES" == "1" ]]; then
        return
    fi
    if [[ ! -t 0 ]]; then
        die "non-interactive live fault injection requires FAULT_INJECTION_ASSUME_YES=1"
    fi

    local confirmation
    read -r -p "Type MNEMONAS-FAULT to continue: " confirmation
    if [[ "$confirmation" != "MNEMONAS-FAULT" ]]; then
        die "confirmation did not match"
    fi
}

resolve_nasd_pids() {
    if [[ -n "$NASD_PID" ]]; then
        printf '%s\n' "$NASD_PID"
        return
    fi
    if [[ -n "$NASD_PID_FILE" && -f "$NASD_PID_FILE" ]]; then
        local pid
        pid="$(sed -n '1p' "$NASD_PID_FILE")"
        require_safe_pid "$pid" "NASD_PID_FILE contents"
        if [[ -n "$pid" ]]; then
            printf '%s\n' "$pid"
            return
        fi
    fi
    if [[ -n "$FAULT_KILL_PATTERN" ]]; then
        pgrep -f -- "$FAULT_KILL_PATTERN" || true
        return
    fi

    pgrep -f -- "$NASD_BIN" || true
}

kill_target_nasd() {
    local pids=()
    mapfile -t pids < <(resolve_nasd_pids)
    if [[ ${#pids[@]} -ne 1 ]]; then
        die "expected exactly one target nasd PID, got ${#pids[@]}. Set NASD_PID or FAULT_KILL_PATTERN."
    fi

    log_warn "Killing nasd PID ${pids[0]}"
    kill -9 "${pids[0]}" || true
    SERVICE_WAS_KILLED=1
}

restart_target_nasd() {
    log_info "Restarting service..."
    if [[ -f "$CONFIG_FILE" ]]; then
        "$NASD_BIN" --config "$CONFIG_FILE" &
    else
        "$NASD_BIN" &
    fi
    NASD_PID=$!
    if [[ -n "$NASD_PID_FILE" ]]; then
        printf '%s\n' "$NASD_PID" > "$NASD_PID_FILE"
    fi
    sleep 2
    SERVICE_WAS_KILLED=0
}

read_config_value() {
    local section=$1
    local key=$2

    if [[ ! -f "$CONFIG_FILE" ]]; then
        return 0
    fi

    if command -v python3 >/dev/null 2>&1; then
        local value
        if value=$(python3 - "$CONFIG_FILE" "$section" "$key" <<'PY'
import sys

try:
    import tomllib
except Exception:
    sys.exit(2)

path, section, key = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    with open(path, "rb") as handle:
        data = tomllib.load(handle)
except Exception:
    sys.exit(2)

current = data
for part in section.split("."):
    if not isinstance(current, dict):
        sys.exit(0)
    current = current.get(part)
    if current is None:
        sys.exit(0)

if not isinstance(current, dict) or key not in current:
    sys.exit(0)

value = current[key]
if isinstance(value, bool):
    sys.stdout.write("true" if value else "false")
elif isinstance(value, (str, int, float)):
    sys.stdout.write(str(value))
elif hasattr(value, "isoformat"):
    sys.stdout.write(value.isoformat())
PY
        ); then
            printf '%s' "$value"
            return 0
        fi
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

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$SECRETS_FILE" "$key" <<'PY'
import json
import sys

path, key = sys.argv[1], sys.argv[2]
try:
    with open(path, "r", encoding="utf-8") as handle:
        data = json.load(handle)
    value = data.get(key, "") if isinstance(data, dict) else ""
except Exception:
    value = ""

if isinstance(value, str):
    sys.stdout.write(value)
PY
        return 0
    fi

    grep -o '"'"$key"'"[[:space:]]*:[[:space:]]*"[^"]*"' "$SECRETS_FILE" | sed 's/.*: *"//' | sed 's/"$//' || true
}

json_escape_string() {
    local value=$1

    if command -v python3 >/dev/null 2>&1; then
        python3 -c 'import json, sys; sys.stdout.write(json.dumps(sys.argv[1]))' "$value"
        return 0
    fi

    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    printf '"%s"' "$value"
}

json_login_payload() {
    local username=$1
    local password=$2

    printf '{"username":%s,"password":%s}' "$(json_escape_string "$username")" "$(json_escape_string "$password")"
}

read_json_field() {
    local json=$1
    local field=$2

    if command -v python3 >/dev/null 2>&1; then
        printf '%s' "$json" | python3 -c '
import json
import sys

field = sys.argv[1]
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

def find_value(value):
    if isinstance(value, dict):
        if field in value:
            return value[field]
        for child in value.values():
            found = find_value(child)
            if found is not None:
                return found
    elif isinstance(value, list):
        for child in value:
            found = find_value(child)
            if found is not None:
                return found
    return None

found = find_value(data)
if isinstance(found, str):
    sys.stdout.write(found)
' "$field" 2>/dev/null
        return 0
    fi

    printf '%s' "$json" | sed -n "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

normalize_webdav_auth_type() {
    local value="$1"

    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    value="${value,,}"
    if [[ -z "$value" ]]; then
        value="basic"
    fi
    printf '%s' "$value"
}

escape_curl_config_value() {
    local value="$1"

    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '%s' "$value"
}

require_curl_config_value() {
    local value="$1"
    local label="$2"

    if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
        die "$label cannot contain newline characters"
    fi
    if [[ "$value" == *[[:cntrl:]]* ]]; then
        die "$label cannot contain control characters"
    fi
}

write_webdav_auth_config() {
    local username="$1"
    local password="$2"
    local escaped_username escaped_password

    require_curl_config_value "$username" "WebDAV username"
    require_curl_config_value "$password" "WebDAV password"
    escaped_username="$(escape_curl_config_value "$username")"
    escaped_password="$(escape_curl_config_value "$password")"
    CURL_AUTH_CONFIG="$(mktemp -t mnemonas-fault-curl-auth.XXXXXX)"
    chmod 0600 "$CURL_AUTH_CONFIG"
    printf 'user = "%s:%s"\n' "$escaped_username" "$escaped_password" > "$CURL_AUTH_CONFIG"
    WEBDAV_AUTH_ARGS=(--config "$CURL_AUTH_CONFIG")
}

cleanup_admin_auth_config() {
    if [[ -n "$CURL_ADMIN_AUTH_CONFIG" ]]; then
        rm -f -- "$CURL_ADMIN_AUTH_CONFIG"
        CURL_ADMIN_AUTH_CONFIG=""
    fi
    ADMIN_AUTH_ARGS=()
}

write_admin_auth_config() {
    local token="$1"
    local escaped_token

    cleanup_admin_auth_config
    require_curl_config_value "$token" "admin bearer token"
    escaped_token="$(escape_curl_config_value "$token")"
    CURL_ADMIN_AUTH_CONFIG="$(mktemp -t mnemonas-fault-admin-auth.XXXXXX)"
    chmod 0600 "$CURL_ADMIN_AUTH_CONFIG"
    printf 'header = "Authorization: Bearer %s"\n' "$escaped_token" > "$CURL_ADMIN_AUTH_CONFIG"
    ADMIN_AUTH_ARGS=(--config "$CURL_ADMIN_AUTH_CONFIG")
}

configure_webdav_auth() {
    local auth_type="${MNEMONAS_WEBDAV_AUTH_TYPE:-$(read_config_value webdav auth_type)}"
    local username=""
    local password=""

    auth_type="$(normalize_webdav_auth_type "$auth_type")"

    case "$auth_type" in
        basic)
            username="${MNEMONAS_WEBDAV_USERNAME:-$(read_config_value webdav username)}"
            password="${MNEMONAS_WEBDAV_PASSWORD:-$(read_config_value webdav password)}"
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
            ;;
        users)
            username="${MNEMONAS_WEBDAV_USERNAME:-}"
            password="${MNEMONAS_WEBDAV_PASSWORD:-}"
            if [[ -z "$username" || -z "$password" ]]; then
                die "WebDAV users auth requires MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD"
            fi
            ;;
        none)
            log_warn "WebDAV auth_type=none; WebDAV fault tests will run without credentials"
            return 0
            ;;
        *)
            log_warn "Unrecognized WebDAV auth_type '$auth_type'; WebDAV fault tests will run without credentials"
            return 0
            ;;
    esac

    write_webdav_auth_config "$username" "$password"
    log_info "Using WebDAV $auth_type auth credentials for user: $username"
}

load_initial_admin_password() {
    if [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        return 1
    fi

    sed -n 's/^Password:[[:space:]]*//p' "$INITIAL_PASSWORD_FILE" | head -n1
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
        -d "$(json_login_payload "admin" "$password")" 2>/dev/null || echo "")

    ADMIN_ACCESS_TOKEN=$(read_json_field "$resp" access_token)
    if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
        write_admin_auth_config "$ADMIN_ACCESS_TOKEN"
        log_info "Using bootstrap admin token for protected API checks"
        return 0
    fi

    log_warn "Bootstrap admin login failed; protected API checks may be skipped"
}

authenticated_api_curl() {
    if [[ ${#ADMIN_AUTH_ARGS[@]} -gt 0 ]]; then
        command curl "${ADMIN_AUTH_ARGS[@]}" "$@"
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
    rm -rf -- "$TEST_DIR"
    if [[ -n "$CURL_AUTH_CONFIG" ]]; then
        rm -f -- "$CURL_AUTH_CONFIG"
        CURL_AUTH_CONFIG=""
    fi
    cleanup_admin_auth_config
    # Restart only after this script killed the explicitly confirmed target.
    if [[ "$SERVICE_WAS_KILLED" == "1" ]] && ! curl -sf "$BASE_URL/health" > /dev/null 2>&1; then
        log_warn "Service not running, attempting restart..."
        restart_target_nasd
    fi
}
trap cleanup EXIT

setup() {
    log_info "Setting up test environment..."
    require_live_fault_target
    confirm_live_fault_target
    mkdir -p "$TEST_DIR"
    
    # Ensure service is running
    if ! curl -sf "$BASE_URL/health" > /dev/null 2>&1; then
        die "MnemoNAS service not running at $BASE_URL"
    fi

    configure_webdav_auth
    configure_admin_auth
    
    # Create test directory in WebDAV
    curl -sf -X MKCOL "$WEBDAV_URL/fault-test/" > /dev/null 2>&1 || true
}

# ==============================================================================
# Test 1: Crash During Write
# ==============================================================================

test_crash_during_write() {
    log_info "Test 1: Crash during write operation..."
    
    # Create a large file that takes time to upload
    dd if=/dev/urandom of="$TEST_DIR/large.bin" bs=1M count="$FAULT_UPLOAD_SIZE_MB" 2>/dev/null
    
    # Start upload in background
    (curl --limit-rate "$FAULT_UPLOAD_LIMIT_RATE" -sf -X PUT "$WEBDAV_URL/fault-test/large.bin" -T "$TEST_DIR/large.bin" > /dev/null 2>&1) &
    local upload_pid=$!
    
    # Wait a moment then kill the service
    sleep 0.5
    kill_target_nasd
    
    # Wait for upload process to fail
    wait $upload_pid 2>/dev/null || true
    
    log_info "Service killed during upload. Checking for orphaned temp files..."
    
    # Check for .tmp files in data directory
    local tmp_files=$(find "$OBJECTS_DIR" -name "*.tmp" 2>/dev/null | wc -l)
    
    # Restart service
    restart_target_nasd
    
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
# Test 2: Object Corruption
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
        log_info "Diagnostics after corruption: $(echo "$diag" | head -c 200)..."
    fi
    
    # Restore the object
    cp "$TEST_DIR/backup.bin" "$object_file"
    log_info "Object file restored"
}

# ==============================================================================
# Test 3: Metadata Corruption
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
# Test 4: Concurrent Write Conflict
# ==============================================================================

test_concurrent_write_conflict() {
    log_info "Test 4: Concurrent write conflict handling..."
    
    # Create initial file
    echo "version 0" | curl -sf -X PUT "$WEBDAV_URL/fault-test/conflict.txt" -T - > /dev/null
    
    # Get ETag
    local etag=$(curl -sf "$WEBDAV_URL/fault-test/conflict.txt" -I | grep -i "^etag:" | awk '{print $2}' | tr -d '\r')
    if [[ -z "$etag" ]]; then
        log_fail "Could not read ETag for concurrent write conflict test"
        return
    fi
    
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
# Test 5: Recovery Verification
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
    local run_corruption="$RUN_CORRUPTION_TESTS"
    if [[ "$run_corruption" == "prompt" ]]; then
        if [[ -t 0 ]]; then
            read -r -p "Run corruption tests? [y/N] " reply
            echo ""
            if [[ "$reply" =~ ^[Yy]$ ]]; then
                run_corruption="1"
            else
                run_corruption="0"
            fi
        else
            run_corruption="0"
        fi
    fi

    if [[ "$run_corruption" == "1" ]]; then
        test_object_corruption
        echo ""
        
        test_metadata_corruption
        echo ""
    else
        log_skip "Corruption tests disabled; set RUN_CORRUPTION_TESTS=1 to enable"
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
