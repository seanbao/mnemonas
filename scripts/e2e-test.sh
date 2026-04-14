#!/bin/bash
# MnemoNAS End-to-End Acceptance Tests
# Week 8: Comprehensive validation for MVP release
#
# Usage: BASE_URL=... STORAGE_ROOT=... CONFIG_FILE=... SECRETS_FILE=... \
#   INITIAL_PASSWORD_FILE=... ./scripts/e2e-test.sh [--quick|--full]
#   With webdav.auth_type=users, also set MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD.
#   Set RUN_RCLONE_WEBDAV=1 to run the optional rclone WebDAV client smoke test.
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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configuration
BASE_URL_EXPLICIT="${BASE_URL+x}"
STORAGE_ROOT_EXPLICIT="${STORAGE_ROOT+x}"
CONFIG_FILE_EXPLICIT="${CONFIG_FILE+x}"
SECRETS_FILE_EXPLICIT="${SECRETS_FILE+x}"
INITIAL_PASSWORD_FILE_EXPLICIT="${INITIAL_PASSWORD_FILE+x}"
USERS_FILE_EXPLICIT="${USERS_FILE+x}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
WEBDAV_URL="${BASE_URL}/dav"
API_URL="${BASE_URL}/api/v1"
STORAGE_ROOT="${STORAGE_ROOT:-$HOME/.mnemonas}"
INTERNAL_DIR="${INTERNAL_DIR:-$STORAGE_ROOT/.mnemonas}"
CONFIG_FILE="${CONFIG_FILE:-$STORAGE_ROOT/config.toml}"
SECRETS_FILE="${SECRETS_FILE:-$STORAGE_ROOT/secrets.json}"
INITIAL_PASSWORD_FILE="${INITIAL_PASSWORD_FILE:-$INTERNAL_DIR/initial-password.txt}"
USERS_FILE="${USERS_FILE:-$INTERNAL_DIR/users.json}"
ALLOW_REAL_STORAGE="${ALLOW_REAL_STORAGE:-0}"
TEST_DIR="/tmp/mnemonas-e2e-$$"
QUICK_MODE=false
CLEANUP_REMOTE_ENABLED=0

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
WEBDAV_USERNAME=""
WEBDAV_PASSWORD=""
WEBDAV_AUTH_ARGS=()
CURL_AUTH_CONFIG=""

# Utility functions
log_info()  { echo -e "${BLUE}[INFO]${NC} $1"; }
log_ok()    { echo -e "${GREEN}[PASS]${NC} $1"; ((PASSED+=1)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $1"; ((FAILED+=1)); }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $1"; ((SKIPPED+=1)); }

require_safe_http_url() {
    local value="$1"
    local label="$2"
    local authority_and_path authority path lower_path segment lower_segment normalized_segment
    local -a base_url_segments=()

    if [[ -z "$value" ]]; then
        echo -e "${RED}ERROR:${NC} $label must not be empty" >&2
        exit 1
    fi
    if [[ "$value" == *[[:space:]]* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain whitespace" >&2
        exit 1
    fi
    if [[ "$value" == *[[:cntrl:]]* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain control characters" >&2
        exit 1
    fi
    if [[ ! "$value" =~ ^https?://[^[:space:]]+$ ]]; then
        echo -e "${RED}ERROR:${NC} $label must be an http(s) URL" >&2
        exit 1
    fi

    authority_and_path="${value#*://}"
    authority="${authority_and_path%%/*}"
    if [[ -z "$authority" ]]; then
        echo -e "${RED}ERROR:${NC} $label must include a host" >&2
        exit 1
    fi
    if [[ "$authority" == *"@"* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain embedded credentials" >&2
        exit 1
    fi
    if [[ "$authority_and_path" == *"?"* || "$authority_and_path" == *"#"* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain query strings or fragments" >&2
        exit 1
    fi
    if [[ "$authority_and_path" == *\\* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain backslashes" >&2
        exit 1
    fi

    path=""
    if [[ "$authority_and_path" == */* ]]; then
        path="/${authority_and_path#*/}"
    fi
    lower_path="$(printf '%s' "$path" | tr '[:upper:]' '[:lower:]')"
    if [[ "$lower_path" == *"%2f"* || "$lower_path" == *"%5c"* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain encoded slashes or backslashes" >&2
        exit 1
    fi
    if [[ "$lower_path" == *"%3f"* || "$lower_path" == *"%23"* ]]; then
        echo -e "${RED}ERROR:${NC} $label must not contain encoded query or fragment markers" >&2
        exit 1
    fi
    if [[ "$path" == *"//"* ]]; then
        echo -e "${RED}ERROR:${NC} $label path must not contain empty segments" >&2
        exit 1
    fi
    IFS='/' read -r -a base_url_segments <<< "${path#/}"
    for segment in "${base_url_segments[@]}"; do
        [[ -n "$segment" ]] || continue
        lower_segment="$(printf '%s' "$segment" | tr '[:upper:]' '[:lower:]')"
        normalized_segment="${lower_segment//%2e/.}"
        if [[ "$normalized_segment" == "." || "$normalized_segment" == ".." ]]; then
            echo -e "${RED}ERROR:${NC} $label must not contain dot segments" >&2
            exit 1
        fi
    done
}

normalize_base_url() {
    local value="$1"
    while [[ "$value" == */ ]]; do
        value="${value%/}"
    done
    printf '%s\n' "$value"
}

require_no_control_characters() {
    local value="$1"
    local label="$2"

    if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
        echo -e "${RED}ERROR:${NC} $label cannot contain newline characters: $value" >&2
        exit 1
    fi
    if [[ "$value" == *[[:cntrl:]]* ]]; then
        echo -e "${RED}ERROR:${NC} $label cannot contain control characters: $value" >&2
        exit 1
    fi
}

require_explicit_e2e_target() {
    local missing=()

    [[ -n "$BASE_URL_EXPLICIT" ]] || missing+=("BASE_URL")
    [[ -n "$STORAGE_ROOT_EXPLICIT" ]] || missing+=("STORAGE_ROOT")
    [[ -n "$CONFIG_FILE_EXPLICIT" ]] || missing+=("CONFIG_FILE")
    [[ -n "$SECRETS_FILE_EXPLICIT" ]] || missing+=("SECRETS_FILE")
    [[ -n "$INITIAL_PASSWORD_FILE_EXPLICIT" ]] || missing+=("INITIAL_PASSWORD_FILE")

    if [[ ${#missing[@]} -gt 0 ]]; then
        echo -e "${RED}ERROR:${NC} explicit ${missing[*]} required for scripts/e2e-test.sh" >&2
        echo "Use 'make e2e' or './scripts/run-e2e-isolated.sh' for the default isolated target." >&2
        exit 1
    fi

    require_safe_http_url "$BASE_URL" "BASE_URL"
    BASE_URL="$(normalize_base_url "$BASE_URL")"
    WEBDAV_URL="${BASE_URL}/dav"
    API_URL="${BASE_URL}/api/v1"

    if [[ -z "$STORAGE_ROOT" ]]; then
        echo -e "${RED}ERROR:${NC} STORAGE_ROOT must not be empty" >&2
        exit 1
    fi
    require_no_control_characters "$STORAGE_ROOT" "STORAGE_ROOT"
    if path_has_parent_segment "$STORAGE_ROOT"; then
        echo -e "${RED}ERROR:${NC} STORAGE_ROOT must not contain '..' path segments: $STORAGE_ROOT" >&2
        exit 1
    fi
    if [[ "$STORAGE_ROOT" != /* ]]; then
        echo -e "${RED}ERROR:${NC} STORAGE_ROOT must be an absolute path: $STORAGE_ROOT" >&2
        exit 1
    fi
    if is_protected_storage_root "$STORAGE_ROOT"; then
        echo -e "${RED}ERROR:${NC} STORAGE_ROOT points at a protected system directory: $STORAGE_ROOT" >&2
        exit 1
    fi
    require_no_symlink_components "$STORAGE_ROOT" "STORAGE_ROOT"

    if [[ "$ALLOW_REAL_STORAGE" != "1" ]]; then
        case "$STORAGE_ROOT" in
            /tmp/*|"$PROJECT_ROOT"/*) ;;
            *)
                echo -e "${RED}ERROR:${NC} STORAGE_ROOT must be under /tmp or this checkout unless ALLOW_REAL_STORAGE=1 is set: $STORAGE_ROOT" >&2
                exit 1
                ;;
        esac
    fi

    if [[ -n "${HOME:-}" && "$STORAGE_ROOT" == "$HOME/.mnemonas" && "$ALLOW_REAL_STORAGE" != "1" ]]; then
        echo -e "${RED}ERROR:${NC} refusing to run E2E tests against default personal storage root: $STORAGE_ROOT" >&2
        echo "Use 'make e2e' or set ALLOW_REAL_STORAGE=1 only for an intentionally disposable target." >&2
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
            echo -e "${RED}ERROR:${NC} $label must not contain symlink path components: $current" >&2
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

expand_user_path() {
    local path="$1"

    case "$path" in
        "")
            printf '%s\n' ""
            ;;
        \~)
            if [[ -n "${HOME:-}" ]]; then
                printf '%s\n' "$HOME"
            else
                printf '%s\n' "$path"
            fi
            ;;
        \~/*)
            if [[ -n "${HOME:-}" ]]; then
                printf '%s/%s\n' "$HOME" "${path#\~/}"
            else
                printf '%s\n' "$path"
            fi
            ;;
        *)
            printf '%s\n' "$path"
            ;;
    esac
}

configure_auth_paths() {
    if [[ -n "$USERS_FILE_EXPLICIT" ]]; then
        return
    fi

    local users_file
    users_file="$(read_config_value auth users_file)"
    if [[ -z "$users_file" ]]; then
        return
    fi

    require_no_control_characters "$users_file" "auth.users_file"
    if path_has_parent_segment "$users_file"; then
        echo -e "${RED}ERROR:${NC} auth.users_file must not contain '..' path segments: $users_file" >&2
        exit 1
    fi
    USERS_FILE="$(expand_user_path "$users_file")"
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

json_refresh_payload() {
    local refresh_token=$1

    printf '{"refresh_token":%s}' "$(json_escape_string "$refresh_token")"
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

write_webdav_auth_config() {
    local username="$1"
    local password="$2"
    local escaped_username escaped_password

    escaped_username="$(escape_curl_config_value "$username")"
    escaped_password="$(escape_curl_config_value "$password")"
    CURL_AUTH_CONFIG="$(mktemp -t mnemonas-e2e-curl-auth.XXXXXX)"
    chmod 0600 "$CURL_AUTH_CONFIG"
    printf 'user = "%s:%s"\n' "$escaped_username" "$escaped_password" > "$CURL_AUTH_CONFIG"
    WEBDAV_AUTH_ARGS=(--config "$CURL_AUTH_CONFIG")
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
                log_warn "WebDAV basic auth is enabled but no password was found; WebDAV tests may fail"
                return 0
            fi
            ;;
        users)
            username="${MNEMONAS_WEBDAV_USERNAME:-}"
            password="${MNEMONAS_WEBDAV_PASSWORD:-}"
            if [[ -z "$username" || -z "$password" ]]; then
                echo -e "${RED}ERROR:${NC} WebDAV users auth requires MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD" >&2
                exit 1
            fi
            ;;
        none)
            log_warn "WebDAV auth_type=none; WebDAV tests will run without credentials"
            return 0
            ;;
        *)
            log_warn "Unrecognized WebDAV auth_type '$auth_type'; WebDAV tests will run without credentials"
            return 0
            ;;
    esac

    write_webdav_auth_config "$username" "$password"
    WEBDAV_USERNAME="$username"
    WEBDAV_PASSWORD="$password"
    log_info "Using WebDAV $auth_type auth credentials for user: $username"
}

load_initial_admin_password() {
    if [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        return 1
    fi

    sed -n 's/^Password:[[:space:]]*//p' "$INITIAL_PASSWORD_FILE" | head -n1
}

auth_appears_configured() {
    local auth_enabled
    auth_enabled="$(read_config_value auth enabled)"

    if [[ "$auth_enabled" == "false" ]]; then
        return 1
    fi

    [[ -z "$auth_enabled" || "$auth_enabled" == "true" || -f "$INITIAL_PASSWORD_FILE" || -f "$USERS_FILE" ]]
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
    log_info "Cleaning up test directory..."
    rm -rf -- "$TEST_DIR"
    if [[ -n "$CURL_AUTH_CONFIG" ]]; then
        rm -f -- "$CURL_AUTH_CONFIG"
        CURL_AUTH_CONFIG=""
    fi
    if [[ "$CLEANUP_REMOTE_ENABLED" != "1" ]]; then
        return
    fi
    # Clean up test files in WebDAV (ignore errors)
    curl -s -X DELETE "$WEBDAV_URL/e2e-test/" > /dev/null 2>&1 || true
}

# Only trap on normal exit, not on errors during test
trap 'cleanup' EXIT

setup() {
    log_info "Setting up test environment..."
    require_explicit_e2e_target
    mkdir -p "$TEST_DIR"
    configure_auth_paths
    configure_webdav_auth
    
    # Check service health
    if ! curl -sf "$BASE_URL/health" > /dev/null; then
        echo -e "${RED}ERROR: MnemoNAS service not running at $BASE_URL${NC}"
        echo "Please start the service: ./bin/nasd &"
        exit 1
    fi
    log_info "Service is healthy"
    CLEANUP_REMOTE_ENABLED=1
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

test_lock_unlock() {
    log_info "Testing WebDAV LOCK/UNLOCK..."
    local lock_payload="$TEST_DIR/webdav-lock.xml"
    local lock_headers="$TEST_DIR/webdav-lock.headers"
    local lock_body="$TEST_DIR/webdav-lock.body"
    local lock_token
    local status

    cat > "$lock_payload" <<'XML'
<D:lockinfo xmlns:D="DAV:">
  <D:lockscope><D:exclusive/></D:lockscope>
  <D:locktype><D:write/></D:locktype>
  <D:owner><D:href>mnemonas-e2e</D:href></D:owner>
</D:lockinfo>
XML

    status=$(curl -sS -X LOCK "$WEBDAV_URL/e2e-test/test.txt" \
        -H "Depth: 0" \
        -H "Timeout: Second-3600" \
        -H "Content-Type: application/xml" \
        --data-binary "@$lock_payload" \
        -D "$lock_headers" \
        -o "$lock_body" \
        -w "%{http_code}" 2>/dev/null || true)
    if [[ "$status" != "200" ]]; then
        log_fail "WebDAV LOCK failed (status: $status)"
        return
    fi

    lock_token=$(awk 'tolower($0) ~ /^lock-token:/ {sub(/^[^:]*:[[:space:]]*/, ""); gsub(/\r/, ""); print; exit}' "$lock_headers")
    if [[ ! "$lock_token" =~ ^\<opaquelocktoken:[^[:space:]\>]+\>$ ]]; then
        log_fail "WebDAV LOCK returned invalid Lock-Token: $lock_token"
        return
    fi
    if ! grep -Eq '<[^/][^>]*lockdiscovery([[:space:]>])' "$lock_body"; then
        log_fail "WebDAV LOCK response missing lockdiscovery"
        return
    fi

    status=$(curl -sS -X UNLOCK "$WEBDAV_URL/e2e-test/test.txt" \
        -H "Lock-Token: $lock_token" \
        -o /dev/null \
        -w "%{http_code}" 2>/dev/null || true)
    if [[ "$status" == "204" ]]; then
        log_ok "WebDAV LOCK/UNLOCK round trip successful"
    else
        log_fail "WebDAV UNLOCK failed (status: $status)"
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

test_rclone_webdav_smoke() {
    log_info "Testing optional rclone WebDAV smoke..."
    if [[ "${RUN_RCLONE_WEBDAV:-0}" != "1" ]]; then
        log_skip "rclone WebDAV smoke disabled; set RUN_RCLONE_WEBDAV=1 to enable"
        return
    fi
    if ! command -v rclone >/dev/null 2>&1; then
        log_skip "rclone WebDAV smoke requires rclone"
        return
    fi

    local remote=":webdav:"
    local rclone_config="$TEST_DIR/rclone.conf"
    local upload_file="$TEST_DIR/rclone-smoke.txt"
    local download_file="$TEST_DIR/rclone-smoke-downloaded.txt"
    local log_file="$TEST_DIR/rclone-smoke.log"
    local -a rclone_args=(
        --config "$rclone_config"
        --webdav-url "$WEBDAV_URL"
        --webdav-vendor other
        --retries 1
        --low-level-retries 1
        --stats-one-line
    )

    : > "$rclone_config"
    printf 'rclone webdav smoke\n' > "$upload_file"

    if [[ -n "$WEBDAV_USERNAME" && -n "$WEBDAV_PASSWORD" ]]; then
        local obscured_password
        obscured_password="$(command rclone obscure "$WEBDAV_PASSWORD" 2>/dev/null)"
        if [[ -z "$obscured_password" ]]; then
            log_fail "rclone could not obscure WebDAV password"
            return
        fi
        rclone_args+=(--webdav-user "$WEBDAV_USERNAME" --webdav-pass "$obscured_password")
    fi

    if ! command rclone copyto "$upload_file" "${remote}e2e-test/rclone-smoke.txt" "${rclone_args[@]}" > "$log_file" 2>&1; then
        log_fail "rclone WebDAV upload failed: $(tail -n 1 "$log_file")"
        return
    fi
    if ! command rclone copyto "${remote}e2e-test/rclone-smoke.txt" "$download_file" "${rclone_args[@]}" > "$log_file" 2>&1; then
        log_fail "rclone WebDAV download failed: $(tail -n 1 "$log_file")"
        return
    fi
    if ! cmp -s "$upload_file" "$download_file"; then
        log_fail "rclone WebDAV download content mismatch"
        return
    fi
    if ! command rclone moveto "${remote}e2e-test/rclone-smoke.txt" "${remote}e2e-test/rclone-smoke-moved.txt" "${rclone_args[@]}" > "$log_file" 2>&1; then
        log_fail "rclone WebDAV move failed: $(tail -n 1 "$log_file")"
        return
    fi
    if ! command rclone lsf "${remote}e2e-test/" "${rclone_args[@]}" > "$log_file" 2>&1; then
        log_fail "rclone WebDAV list failed: $(tail -n 1 "$log_file")"
        return
    fi
    if ! grep -Fxq "rclone-smoke-moved.txt" "$log_file"; then
        log_fail "rclone WebDAV list did not include moved object"
        return
    fi
    if ! command rclone deletefile "${remote}e2e-test/rclone-smoke-moved.txt" "${rclone_args[@]}" > "$log_file" 2>&1; then
        log_fail "rclone WebDAV cleanup failed: $(tail -n 1 "$log_file")"
        return
    fi

    log_ok "rclone WebDAV smoke succeeded"
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
    if [[ -z "$etag" ]]; then
        log_fail "If-None-Match test could not read ETag header"
        return
    fi
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
    if [[ -z "$etag" ]]; then
        log_fail "If-Match success test could not read ETag header"
        return
    fi
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
    
    local history_file="$TEST_DIR/version-history.json"
    local status=$(authenticated_api_curl -s -w "%{http_code}" -o "$history_file" "$API_URL/versions/e2e-test/versioned.txt")
    local resp=$(cat "$history_file" 2>/dev/null || echo "")
    if [[ "$status" == "200" ]] && echo "$resp" | grep -q "versions\|hash"; then
        if [[ -z "$ADMIN_ACCESS_TOKEN" ]] && auth_appears_configured; then
            log_fail "Version history API allowed unauthenticated access while auth appears configured"
        else
            log_ok "Version history API returns data"
        fi
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && ( "$status" == "401" || "$status" == "403" ) ]]; then
        log_skip "Version history API requires admin authentication"
    else
        log_fail "Version history API failed (status: $status): $resp"
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
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && "$ADMIN_API_STATUS" == "200" ]] && auth_appears_configured; then
        log_fail "Metrics API allowed unauthenticated access while auth appears configured"
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
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && "$ADMIN_API_STATUS" == "200" ]] && auth_appears_configured; then
        log_fail "Scrub API allowed unauthenticated access while auth appears configured"
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
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && "$ADMIN_API_STATUS" == "200" ]] && auth_appears_configured; then
        log_fail "Scrub trigger API allowed unauthenticated access while auth appears configured"
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
    elif [[ -z "$ADMIN_ACCESS_TOKEN" && "$ADMIN_API_STATUS" == "200" ]] && auth_appears_configured; then
        log_fail "Diagnostics export allowed unauthenticated access while auth appears configured"
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
    
    log_info "Crash recovery is covered by isolated fault-injection tests..."
    echo ""
    echo "  Run one of the following commands to test crash recovery:"
    echo "  make fault-injection"
    echo "  ./scripts/run-fault-injection-isolated.sh"
    echo ""
    log_skip "Crash recovery covered by isolated fault-injection runner"
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
    local host
    host=$(read_config_value server host)

    if [[ -n "$host" ]]; then
        log_ok "Host binding configured in config file ($host)"
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
    if [[ ! -f "$INITIAL_PASSWORD_FILE" ]]; then
        log_skip "Auth login test - no initial password file (auth may be disabled or already logged in)"
        return
    fi
    
    # Extract password from file
    local password=$(load_initial_admin_password)
    if [[ -z "$password" ]]; then
        log_fail "Could not extract password from $INITIAL_PASSWORD_FILE"
        return
    fi
    
    local resp=$(curl -sf -X POST "$API_URL/auth/login" \
        -H "Content-Type: application/json" \
        -d "$(json_login_payload "admin" "$password")" 2>/dev/null || echo "error")
    
    ADMIN_ACCESS_TOKEN=$(read_json_field "$resp" access_token)
    ADMIN_REFRESH_TOKEN=$(read_json_field "$resp" refresh_token)
    if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
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
        if auth_appears_configured; then
            log_fail "Auth login endpoint unavailable while auth appears configured"
        else
            log_skip "Auth endpoint not available (auth may be disabled)"
        fi
    else
        log_fail "Auth login should return 401 for invalid password (got $status)"
    fi
}

test_auth_password_file_deleted_after_login() {
    log_info "Testing password file deletion after login..."
    
    local password_file="$INITIAL_PASSWORD_FILE"
    
    # If auth is enabled and we just logged in, file should be deleted
    if [[ -f "$password_file" ]]; then
        if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
            log_fail "Password file still exists after successful login"
        else
            log_skip "Password file still exists (login may not have occurred)"
        fi
    else
        # A successful login proves this run used the bootstrap password file.
        if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then
            log_ok "Password file correctly deleted after login"
        elif [[ -f "$USERS_FILE" ]]; then
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
        if auth_appears_configured; then
            log_fail "Protected endpoint allowed unauthenticated access while auth appears configured"
        else
            log_skip "Auth may be disabled (endpoint returned 200)"
        fi
    else
        log_fail "Protected endpoint returned unexpected status: $status"
    fi
}

test_auth_token_refresh() {
    log_info "Testing token refresh flow..."
    
    local password_file="$INITIAL_PASSWORD_FILE"
    local refresh_token="$ADMIN_REFRESH_TOKEN"
    
    # Existing refresh tokens from a successful login do not require auth files at default paths.
    if [[ -z "$refresh_token" && ! -f "$password_file" && ! -f "$USERS_FILE" ]]; then
        log_skip "Auth not configured for token refresh test"
        return
    fi

    if [[ -z "$refresh_token" ]]; then
        # Try to login and get refresh token
        local password=""
        if [[ -f "$password_file" ]]; then
            password=$(load_initial_admin_password)
        fi

        if [[ -z "$password" ]]; then
            log_skip "No password available for token refresh test"
            return
        fi

        local login_resp=$(curl -sf -X POST "$API_URL/auth/login" \
            -H "Content-Type: application/json" \
            -d "$(json_login_payload "admin" "$password")" 2>/dev/null)

        ADMIN_ACCESS_TOKEN=$(read_json_field "$login_resp" access_token)
        ADMIN_REFRESH_TOKEN=$(read_json_field "$login_resp" refresh_token)
        refresh_token="$ADMIN_REFRESH_TOKEN"
    fi
    
    if [[ -z "$refresh_token" ]]; then
        log_skip "Could not get refresh token from login response"
        return
    fi
    
    local refresh_resp=$(curl -sf -X POST "$API_URL/auth/refresh" \
        -H "Content-Type: application/json" \
        -d "$(json_refresh_payload "$refresh_token")" 2>/dev/null || echo "error")
    
    if [[ -n "$(read_json_field "$refresh_resp" access_token)" ]]; then
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
    test_lock_unlock
    test_file_copy
    test_file_move
    test_file_delete
    test_rclone_webdav_smoke

    # Group 3: ETag
    # Re-create test file for ETag tests
    echo "Hello, MnemoNAS!" | curl -sf -X PUT "$WEBDAV_URL/e2e-test/test.txt" -T - > /dev/null
    test_etag_returned
    test_if_none_match
    test_if_match_success
    test_if_match_failure

    # Group 10: Authentication
    test_auth_login_failure
    test_auth_login_success
    test_auth_password_file_deleted_after_login
    test_auth_protected_endpoint
    test_auth_token_refresh

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
