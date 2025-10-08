#!/usr/bin/env bash
# shellcheck disable=SC2317

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

E2E_ROOT="${MNEMONAS_E2E_ROOT:-/tmp/mnemonas-playwright}"
BACKEND_ROOT="$E2E_ROOT/backend"
STORAGE_ROOT="$BACKEND_ROOT/storage"
CONFIG_FILE="$BACKEND_ROOT/config.toml"
LOG_DIR="$BACKEND_ROOT/logs"
E2E_PASSWORD_FILE="$BACKEND_ROOT/e2e-password.txt"
PUBLIC_SHARE_ID_FILE="$BACKEND_ROOT/public-share-id.txt"
PROTECTED_SHARE_ID_FILE="$BACKEND_ROOT/protected-share-id.txt"
PROTECTED_SHARE_PASSWORD_FILE="$BACKEND_ROOT/protected-share-password.txt"
DISABLED_SHARE_ID_FILE="$BACKEND_ROOT/disabled-share-id.txt"
FOLDER_SHARE_ID_FILE="$BACKEND_ROOT/folder-share-id.txt"

DATAPLANE_HTTP="${MNEMONAS_E2E_DATAPLANE_HTTP:-127.0.0.1:19191}"
DATAPLANE_GRPC="${MNEMONAS_E2E_DATAPLANE_GRPC:-127.0.0.1:19190}"
NASD_HOST="${MNEMONAS_E2E_NASD_HOST:-127.0.0.1}"
NASD_PORT="${MNEMONAS_E2E_NASD_PORT:-18180}"
NASD_BASE_URL=""

export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

cleanup() {
  if [[ -n "${nasd_pid:-}" ]]; then
    kill "$nasd_pid" 2>/dev/null || true
  fi
  if [[ -n "${dataplane_pid:-}" ]]; then
    kill "$dataplane_pid" 2>/dev/null || true
  fi
}

extract_json_field() {
  local json="$1"
  local field="$2"

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

http_host_for_url() {
  local host="$1"
  if [[ "$host" == *:* && "$host" != \[*\] ]]; then
    printf '[%s]\n' "$host"
    return 0
  fi
  printf '%s\n' "$host"
}

seed_e2e_fixtures() {
  local login_response token share_response share_id protected_share_id disabled_share_id folder_share_id
  local protected_share_password="playwright-secret"

  if [[ ! -f "$E2E_PASSWORD_FILE" ]]; then
    echo "missing E2E password file: $E2E_PASSWORD_FILE" >&2
    return 1
  fi

  local password
  password=$(sed -n 's/^Password:\s*//p' "$E2E_PASSWORD_FILE" | head -n1)
  if [[ -z "$password" ]]; then
    echo "failed to parse E2E password file: $E2E_PASSWORD_FILE" >&2
    return 1
  fi

  login_response=$(curl -sf -X POST "$NASD_BASE_URL/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "$(json_login_payload "admin" "$password")")
  token=$(extract_json_field "$login_response" 'access_token')
  if [[ -z "$token" ]]; then
    echo "failed to retrieve E2E auth token" >&2
    return 1
  fi

  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-trash-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for trash e2e' >/dev/null

  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for public share e2e' >/dev/null

  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-protected-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for protected public share e2e' >/dev/null

  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-disabled-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for disabled public share e2e' >/dev/null

  curl -sf -X POST "$NASD_BASE_URL/api/v1/directories/e2e-folder-share" \
    -H "Authorization: Bearer $token" >/dev/null
  curl -sf -X POST "$NASD_BASE_URL/api/v1/directories/e2e-folder-share/subdir" \
    -H "Authorization: Bearer $token" >/dev/null
  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-folder-share/root-note.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for shared folder root file' >/dev/null
  curl -sf -X POST "$NASD_BASE_URL/api/v1/files/e2e-folder-share/subdir/nested-note.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for shared folder nested file' >/dev/null

  share_response=$(curl -sf -X POST "$NASD_BASE_URL/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-share-fixture.txt","type":"file","permission":"read","description":"playwright public share fixture"}')
  share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$share_id" ]]; then
    echo "failed to create public share fixture" >&2
    return 1
  fi
  printf '%s\n' "$share_id" > "$PUBLIC_SHARE_ID_FILE"

  share_response=$(curl -sf -X POST "$NASD_BASE_URL/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d "{\"path\":\"/e2e-protected-share-fixture.txt\",\"type\":\"file\",\"permission\":\"read\",\"password\":$(json_escape_string "$protected_share_password"),\"description\":\"playwright protected public share fixture\"}")
  protected_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$protected_share_id" ]]; then
    echo "failed to create protected public share fixture" >&2
    return 1
  fi
  printf '%s\n' "$protected_share_id" > "$PROTECTED_SHARE_ID_FILE"
  printf '%s\n' "$protected_share_password" > "$PROTECTED_SHARE_PASSWORD_FILE"

  share_response=$(curl -sf -X POST "$NASD_BASE_URL/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-disabled-share-fixture.txt","type":"file","permission":"read","description":"playwright disabled public share fixture"}')
  disabled_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$disabled_share_id" ]]; then
    echo "failed to create disabled public share fixture" >&2
    return 1
  fi

  curl -sf -X PUT "$NASD_BASE_URL/api/v1/shares/${disabled_share_id}" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"enabled":false}' >/dev/null
  printf '%s\n' "$disabled_share_id" > "$DISABLED_SHARE_ID_FILE"

  share_response=$(curl -sf -X POST "$NASD_BASE_URL/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-folder-share","type":"folder","permission":"read","description":"playwright public folder share fixture"}')
  folder_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$folder_share_id" ]]; then
    echo "failed to create public folder share fixture" >&2
    return 1
  fi
  printf '%s\n' "$folder_share_id" > "$FOLDER_SHARE_ID_FILE"

  curl -sf -X DELETE "$NASD_BASE_URL/api/v1/files/e2e-trash-fixture.txt" \
    -H "Authorization: Bearer $token" >/dev/null
}

wait_for_url() {
  local url="$1"
  local attempts="${2:-120}"

  for _ in $(seq 1 "$attempts"); do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done

  return 1
}

require_no_control_characters() {
  local value="$1"
  local label="$2"

  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    echo "$label cannot contain newline characters: $value" >&2
    exit 1
  fi
  if [[ "$value" == *[[:cntrl:]]* ]]; then
    echo "$label cannot contain control characters: $value" >&2
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
      echo "$label must not contain symlink path components: $current" >&2
      exit 1
    fi
    [[ -e "$current" ]] || break
  done
}

is_valid_tcp_host() {
  local host="$1"
  local label
  local -a labels

  host="${host%.}"
  [[ -n "$host" ]] || return 1
  [[ "$host" != *"["* && "$host" != *"]"* ]] || return 1

  if [[ "$host" == *:* ]]; then
    [[ "$host" =~ ^[0-9A-Fa-f:.]+$ ]]
    return
  fi

  [[ "${#host}" -le 253 ]] || return 1
  IFS='.' read -r -a labels <<< "$host"
  for label in "${labels[@]}"; do
    [[ -n "$label" && "${#label}" -le 63 ]] || return 1
    [[ "$label" != -* && "$label" != *- ]] || return 1
    [[ "$label" =~ ^[A-Za-z0-9-]+$ ]] || return 1
  done
  return 0
}

is_ipv4_loopback_host() {
  local host="$1"
  local octet
  local -a octets

  [[ "$host" =~ ^127\.([0-9]{1,3}\.){2}[0-9]{1,3}$ ]] || return 1
  IFS='.' read -r -a octets <<< "$host"
  for octet in "${octets[@]}"; do
    [[ ${#octet} -le 3 ]] || return 1
    (( 10#$octet >= 0 && 10#$octet <= 255 )) || return 1
  done
  return 0
}

is_loopback_host() {
  local host="$1"

  case "$host" in
    localhost|ip6-localhost|::1)
      return 0
      ;;
  esac
  is_ipv4_loopback_host "$host"
}

tcp_addr_host() {
  local value="$1"

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

require_safe_tcp_port() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || { echo "$label cannot be empty" >&2; exit 1; }
  [[ "$value" != *[[:space:]]* ]] || { echo "$label cannot contain whitespace: $value" >&2; exit 1; }
  [[ "$value" != *[[:cntrl:]]* ]] || { echo "$label cannot contain control characters: $value" >&2; exit 1; }
  [[ "$value" =~ ^[0-9]+$ ]] || { echo "$label must be numeric: $value" >&2; exit 1; }
  (( 10#$value >= 1 && 10#$value <= 65535 )) || { echo "$label must be between 1 and 65535: $value" >&2; exit 1; }
}

require_safe_tcp_addr() {
  local value="$1"
  local label="$2"
  local host=""
  local port=""

  [[ -n "$value" ]] || { echo "$label cannot be empty" >&2; exit 1; }
  [[ "$value" != *[[:space:]]* ]] || { echo "$label cannot contain whitespace: $value" >&2; exit 1; }
  [[ "$value" != *[[:cntrl:]]* ]] || { echo "$label cannot contain control characters: $value" >&2; exit 1; }

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  else
    echo "$label must be a host:port address: $value" >&2
    exit 1
  fi

  is_valid_tcp_host "$host" || { echo "$label host is invalid: $value" >&2; exit 1; }
  require_safe_tcp_port "$port" "$label port"
}

require_loopback_host() {
  local value="$1"
  local label="$2"

  is_loopback_host "$value" || { echo "$label must be loopback-only for isolated test backends: $value" >&2; exit 1; }
}

require_loopback_tcp_addr() {
  local value="$1"
  local label="$2"
  local host

  host="$(tcp_addr_host "$value")" || { echo "$label must be a host:port address: $value" >&2; exit 1; }
  is_loopback_host "$host" || { echo "$label must be loopback-only for isolated test backends: $value" >&2; exit 1; }
}

require_safe_e2e_root() {
  require_no_control_characters "$E2E_ROOT" "MNEMONAS_E2E_ROOT"

  if path_has_parent_segment "$E2E_ROOT"; then
    echo "MNEMONAS_E2E_ROOT must not contain '..' path segments: $E2E_ROOT" >&2
    exit 1
  fi

  case "$E2E_ROOT" in
    /tmp/*|"$PROJECT_ROOT"/*) ;;
    *)
      echo "MNEMONAS_E2E_ROOT must be under /tmp or this checkout: $E2E_ROOT" >&2
      exit 1
      ;;
  esac
  require_no_symlink_components "$E2E_ROOT" "MNEMONAS_E2E_ROOT"
}

trap cleanup EXIT INT TERM

require_safe_e2e_root
[[ "$NASD_HOST" != *[[:space:]]* ]] || { echo "MNEMONAS_E2E_NASD_HOST cannot contain whitespace: $NASD_HOST" >&2; exit 1; }
[[ "$NASD_HOST" != *[[:cntrl:]]* ]] || { echo "MNEMONAS_E2E_NASD_HOST cannot contain control characters: $NASD_HOST" >&2; exit 1; }
is_valid_tcp_host "$NASD_HOST" || { echo "MNEMONAS_E2E_NASD_HOST host is invalid: $NASD_HOST" >&2; exit 1; }
require_loopback_host "$NASD_HOST" "MNEMONAS_E2E_NASD_HOST"
require_safe_tcp_port "$NASD_PORT" "MNEMONAS_E2E_NASD_PORT"
require_safe_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_E2E_DATAPLANE_HTTP"
require_loopback_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_E2E_DATAPLANE_HTTP"
require_safe_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_E2E_DATAPLANE_GRPC"
require_loopback_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_E2E_DATAPLANE_GRPC"
NASD_BASE_URL="http://$(http_host_for_url "$NASD_HOST"):${NASD_PORT}"
rm -rf "$BACKEND_ROOT"
mkdir -p "$STORAGE_ROOT" "$LOG_DIR"

cat > "$CONFIG_FILE" <<EOF
[server]
host = "$NASD_HOST"
port = $NASD_PORT
read_timeout = "30s"
write_timeout = "60s"
idle_timeout = "120s"

[server.tls]
enabled = false
auto_generate = false

[storage]
root = "$STORAGE_ROOT"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 0
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go", ".rs", ".toml", ".yaml", ".json"]
auto_versioned_filenames = ["README", "LICENSE", "CHANGELOG", "Dockerfile", "Makefile"]
max_versioned_size = 104857600

[storage.trash]
enabled = true
retention_days = 30
max_size = 1073741824

[dataplane]
grpc_address = "$DATAPLANE_GRPC"
timeout = "30s"
max_retries = 3

[dataplane.cdc]
min_chunk_size = 262144
avg_chunk_size = 1048576
max_chunk_size = 4194304

[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "admin"
password = ""

[auth]
enabled = true
jwt_secret = ""
access_token_ttl = "2h"
refresh_token_ttl = "168h"
users_file = ""

[share]
enabled = true
store_file = ""
base_url = ""

[favorites]
enabled = true
store_file = ""

[alerts]
enabled = false
check_interval = "1h"
threshold_pct = 90.0
critical_pct = 95.0
min_free_bytes = 0
cooldown_period = "4h"
webhook_url = ""
webhook_method = "POST"

[log]
level = "warn"
format = "console"
output = "stdout"
time_format = "RFC3339"
EOF

(
  cd "$PROJECT_ROOT/dataplane"
  cargo run --quiet -- --listen "$DATAPLANE_HTTP" --grpc "$DATAPLANE_GRPC" --data-dir "$STORAGE_ROOT/.mnemonas/objects" > "$LOG_DIR/dataplane.log" 2>&1
) &
dataplane_pid=$!

if ! wait_for_url "http://${DATAPLANE_HTTP}/health"; then
  echo "dataplane failed to start; see $LOG_DIR/dataplane.log" >&2
  exit 1
fi

(
  cd "$PROJECT_ROOT"
  CGO_ENABLED=1 go run ./cmd/nasd --config "$CONFIG_FILE" > "$LOG_DIR/nasd.log" 2>&1
) &
nasd_pid=$!

if ! wait_for_url "$NASD_BASE_URL/health"; then
  echo "nasd failed to start; see $LOG_DIR/nasd.log" >&2
  exit 1
fi

if [[ -f "$STORAGE_ROOT/.mnemonas/initial-password.txt" ]]; then
  cp "$STORAGE_ROOT/.mnemonas/initial-password.txt" "$E2E_PASSWORD_FILE"
fi

if ! seed_e2e_fixtures; then
  echo "failed to seed E2E fixtures; see $LOG_DIR/nasd.log" >&2
  exit 1
fi

echo "MnemoNAS Playwright backend ready at $NASD_BASE_URL"

wait -n "$dataplane_pid" "$nasd_pid"
exit $?
