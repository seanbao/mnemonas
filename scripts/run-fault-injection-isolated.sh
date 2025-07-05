#!/usr/bin/env bash
# Start an isolated MnemoNAS backend and run destructive fault-injection tests.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

FAULT_ROOT="${MNEMONAS_FAULT_ROOT:-/tmp/mnemonas-fault-injection}"
BACKEND_ROOT="$FAULT_ROOT/backend"
STORAGE_ROOT="$BACKEND_ROOT/storage"
CONFIG_FILE="$BACKEND_ROOT/config.toml"
LOG_DIR="$BACKEND_ROOT/logs"
BIN_DIR="$BACKEND_ROOT/bin"
NASD_BIN="$BIN_DIR/nasd"
SECRETS_FILE="$STORAGE_ROOT/secrets.json"
INITIAL_PASSWORD_FILE="$BACKEND_ROOT/initial-password.txt"
OBJECTS_DIR="$STORAGE_ROOT/.mnemonas/objects"
INDEX_DB="$STORAGE_ROOT/.mnemonas/index.db"
NASD_PID_FILE="$STORAGE_ROOT/.mnemonas/nasd.pid"

NASD_HOST="${MNEMONAS_FAULT_NASD_HOST:-127.0.0.1}"
NASD_PORT="${MNEMONAS_FAULT_NASD_PORT:-18280}"
DATAPLANE_HTTP="${MNEMONAS_FAULT_DATAPLANE_HTTP:-127.0.0.1:19291}"
DATAPLANE_GRPC="${MNEMONAS_FAULT_DATAPLANE_GRPC:-127.0.0.1:19290}"
READY_ATTEMPTS="${MNEMONAS_FAULT_READY_ATTEMPTS:-180}"
KEEP_ROOT="${MNEMONAS_FAULT_KEEP_ROOT:-0}"
BASE_URL=""

fail() {
  printf '[fault-injection-isolated] ERROR: %s\n' "$*" >&2
  exit 1
}

log() {
  printf '[fault-injection-isolated] %s\n' "$*"
}

require_no_control_characters() {
  local value="$1"
  local label="$2"

  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    fail "$label cannot contain newline characters: $value"
  fi
  if [[ "$value" == *[[:cntrl:]]* ]]; then
    fail "$label cannot contain control characters: $value"
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
  local segment
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
      fail "$label must not contain symlink path components: $current"
    fi
    [[ -e "$current" ]] || break
  done
}

require_safe_fault_root() {
  require_no_control_characters "$FAULT_ROOT" "MNEMONAS_FAULT_ROOT"
  if path_has_parent_segment "$FAULT_ROOT"; then
    fail "MNEMONAS_FAULT_ROOT must not contain '..' path segments: $FAULT_ROOT"
  fi
  if [[ "$FAULT_ROOT" != /* ]]; then
    fail "MNEMONAS_FAULT_ROOT must be an absolute path: $FAULT_ROOT"
  fi
  case "$FAULT_ROOT" in
    /tmp/*|"$PROJECT_ROOT"/*) ;;
    *)
      fail "MNEMONAS_FAULT_ROOT must be under /tmp or this checkout: $FAULT_ROOT"
      ;;
  esac
  require_no_symlink_components "$FAULT_ROOT" "MNEMONAS_FAULT_ROOT"
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

http_host_for_url() {
  local host="$1"
  if [[ "$host" == *:* && "$host" != \[*\] ]]; then
    printf '[%s]\n' "$host"
    return 0
  fi
  printf '%s\n' "$host"
}

require_safe_tcp_port() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  [[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be numeric: $value"
  (( 10#$value >= 1 && 10#$value <= 65535 )) || fail "$label must be between 1 and 65535: $value"
}

require_safe_tcp_host() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"
  is_valid_tcp_host "$value" || fail "$label host is invalid: $value"
}

require_safe_tcp_addr() {
  local value="$1"
  local label="$2"
  local host=""
  local port=""

  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *[[:space:]]* ]] || fail "$label cannot contain whitespace: $value"
  [[ "$value" != *[[:cntrl:]]* ]] || fail "$label cannot contain control characters: $value"

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  else
    fail "$label must be a host:port address: $value"
  fi

  is_valid_tcp_host "$host" || fail "$label host is invalid: $value"
  require_safe_tcp_port "$port" "$label port"
}

require_loopback_host() {
  local value="$1"
  local label="$2"

  is_loopback_host "$value" || fail "$label must be loopback-only for isolated fault-injection backends: $value"
}

require_loopback_tcp_addr() {
  local value="$1"
  local label="$2"
  local host

  host="$(tcp_addr_host "$value")" || fail "$label must be a host:port address: $value"
  is_loopback_host "$host" || fail "$label must be loopback-only for isolated fault-injection backends: $value"
}

require_positive_integer() {
  local value="$1"
  local label="$2"

  [[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a positive integer: $value"
  (( 10#$value > 0 )) || fail "$label must be a positive integer: $value"
}

show_backend_logs() {
  local log_path

  for log_path in "$LOG_DIR/dataplane.log" "$LOG_DIR/nasd.log"; do
    if [[ -f "$log_path" ]]; then
      printf -- '---- %s ----\n' "$log_path" >&2
      tail -80 "$log_path" >&2 || true
    fi
  done
}

wait_for_url() {
  local url="$1"
  local label="$2"
  local attempts="$READY_ATTEMPTS"

  for _ in $(seq 1 "$attempts"); do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi

    case "$label" in
      dataplane)
        if [[ -n "${dataplane_pid:-}" ]] && ! kill -0 "$dataplane_pid" 2>/dev/null; then
          wait "$dataplane_pid" || true
          show_backend_logs
          fail "dataplane exited before it was ready"
        fi
        ;;
      nasd)
        if [[ -n "${nasd_pid:-}" ]] && ! kill -0 "$nasd_pid" 2>/dev/null; then
          wait "$nasd_pid" || true
          show_backend_logs
          fail "nasd exited before it was ready"
        fi
        ;;
    esac

    sleep 0.5
  done

  show_backend_logs
  fail "$label did not become ready at $url"
}

write_config() {
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
}

build_nasd() {
  log "Building isolated nasd binary..."
  mkdir -p "$BIN_DIR"
  (
    cd "$PROJECT_ROOT"
    env GOSUMDB="${GOSUMDB:-sum.golang.org}" GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" CGO_ENABLED="${CGO_ENABLED:-1}" \
      go build -o "$NASD_BIN" ./cmd/nasd
  )
}

start_dataplane() {
  log "Starting isolated dataplane at $DATAPLANE_HTTP / $DATAPLANE_GRPC..."
  if [[ -x "$PROJECT_ROOT/bin/dataplane" ]]; then
    "$PROJECT_ROOT/bin/dataplane" --listen "$DATAPLANE_HTTP" --grpc "$DATAPLANE_GRPC" --data-dir "$OBJECTS_DIR" > "$LOG_DIR/dataplane.log" 2>&1 &
  else
    (
      cd "$PROJECT_ROOT/dataplane"
      cargo run --quiet --locked -- --listen "$DATAPLANE_HTTP" --grpc "$DATAPLANE_GRPC" --data-dir "$OBJECTS_DIR"
    ) > "$LOG_DIR/dataplane.log" 2>&1 &
  fi
  dataplane_pid=$!
  wait_for_url "http://${DATAPLANE_HTTP}/health" "dataplane"
  disown "$dataplane_pid" 2>/dev/null || true
}

start_nasd() {
  log "Starting isolated nasd at $BASE_URL..."
  "$NASD_BIN" --config "$CONFIG_FILE" > "$LOG_DIR/nasd.log" 2>&1 &
  nasd_pid=$!
  mkdir -p "$(dirname "$NASD_PID_FILE")"
  printf '%s\n' "$nasd_pid" > "$NASD_PID_FILE"
  wait_for_url "$BASE_URL/health" "nasd"
  disown "$nasd_pid" 2>/dev/null || true
}

kill_pid() {
  local pid="$1"
  local label="$2"

  [[ "$pid" =~ ^[0-9]+$ ]] || return 0
  if kill -0 "$pid" 2>/dev/null; then
    log "Stopping $label PID $pid"
    kill "$pid" 2>/dev/null || true
  fi
}

cleanup() {
  local status=$?
  local pid
  local -a pids=()

  set +e
  if [[ -f "$NASD_PID_FILE" ]]; then
    pid="$(sed -n '1p' "$NASD_PID_FILE")"
    kill_pid "$pid" "nasd"
  fi
  if [[ -n "${nasd_pid:-}" ]]; then
    kill_pid "$nasd_pid" "initial nasd"
  fi
  if command -v pgrep >/dev/null 2>&1; then
    mapfile -t pids < <(pgrep -f -- "$NASD_BIN --config $CONFIG_FILE" || true)
    for pid in "${pids[@]}"; do
      kill_pid "$pid" "matching nasd"
    done
  fi
  if [[ -n "${dataplane_pid:-}" ]]; then
    kill_pid "$dataplane_pid" "dataplane"
  fi
  if [[ -n "${nasd_pid:-}" ]]; then
    wait "$nasd_pid" 2>/dev/null || true
  fi
  if [[ -n "${dataplane_pid:-}" ]]; then
    wait "$dataplane_pid" 2>/dev/null || true
  fi

  if [[ "$status" -eq 0 && "$KEEP_ROOT" != "1" ]]; then
    rm -rf -- "$FAULT_ROOT"
  elif [[ "$KEEP_ROOT" == "1" || "$status" -ne 0 ]]; then
    log "Preserving isolated fault-injection root: $FAULT_ROOT"
  fi

  return "$status"
}

require_safe_fault_root
require_safe_tcp_host "$NASD_HOST" "MNEMONAS_FAULT_NASD_HOST"
require_loopback_host "$NASD_HOST" "MNEMONAS_FAULT_NASD_HOST"
require_safe_tcp_port "$NASD_PORT" "MNEMONAS_FAULT_NASD_PORT"
require_safe_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_FAULT_DATAPLANE_HTTP"
require_loopback_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_FAULT_DATAPLANE_HTTP"
require_safe_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_FAULT_DATAPLANE_GRPC"
require_loopback_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_FAULT_DATAPLANE_GRPC"
require_positive_integer "$READY_ATTEMPTS" "MNEMONAS_FAULT_READY_ATTEMPTS"
BASE_URL="http://$(http_host_for_url "$NASD_HOST"):${NASD_PORT}"

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

rm -rf -- "$BACKEND_ROOT"
mkdir -p "$STORAGE_ROOT" "$LOG_DIR"
write_config
build_nasd
start_dataplane
start_nasd

if [[ ! -f "$STORAGE_ROOT/.mnemonas/initial-password.txt" ]]; then
  show_backend_logs
  fail "initial admin password file was not created"
fi
cp "$STORAGE_ROOT/.mnemonas/initial-password.txt" "$INITIAL_PASSWORD_FILE"

log "Running destructive fault-injection tests against isolated backend: $BASE_URL"
(
  cd "$PROJECT_ROOT"
  env \
    MNEMONAS_LIVE_FAULTS=1 \
    FAULT_INJECTION_ASSUME_YES=1 \
    RUN_CORRUPTION_TESTS="${RUN_CORRUPTION_TESTS:-1}" \
    FAULT_UPLOAD_SIZE_MB="${FAULT_UPLOAD_SIZE_MB:-16}" \
    BASE_URL="$BASE_URL" \
    STORAGE_ROOT="$STORAGE_ROOT" \
    INTERNAL_DIR="$STORAGE_ROOT/.mnemonas" \
    CONFIG_FILE="$CONFIG_FILE" \
    SECRETS_FILE="$SECRETS_FILE" \
    INITIAL_PASSWORD_FILE="$INITIAL_PASSWORD_FILE" \
    OBJECTS_DIR="$OBJECTS_DIR" \
    INDEX_DB="$INDEX_DB" \
    NASD_BIN="$NASD_BIN" \
    NASD_PID="$nasd_pid" \
    NASD_PID_FILE="$NASD_PID_FILE" \
    "$PROJECT_ROOT/scripts/fault-injection-test.sh" "$@"
)
