#!/usr/bin/env bash
# Start an isolated MnemoNAS backend and run the WebDAV benchmark against it.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BENCH_ROOT="${MNEMONAS_BENCH_ROOT:-/tmp/mnemonas-benchmark}"
BACKEND_ROOT="$BENCH_ROOT/backend"
STORAGE_ROOT="$BACKEND_ROOT/storage"
CONFIG_FILE="$BACKEND_ROOT/config.toml"
SECRETS_FILE="$STORAGE_ROOT/secrets.json"
INITIAL_PASSWORD_FILE="$BACKEND_ROOT/e2e-password.txt"
READY_FILE="$BACKEND_ROOT/public-share-id.txt"
LOG_DIR="$BACKEND_ROOT/logs"

NASD_HOST="${MNEMONAS_BENCH_NASD_HOST:-127.0.0.1}"
NASD_PORT="${MNEMONAS_BENCH_NASD_PORT:-18181}"
DATAPLANE_HTTP="${MNEMONAS_BENCH_DATAPLANE_HTTP:-127.0.0.1:19193}"
DATAPLANE_GRPC="${MNEMONAS_BENCH_DATAPLANE_GRPC:-127.0.0.1:19192}"
BASE_URL=""
READY_ATTEMPTS="${MNEMONAS_BENCH_READY_ATTEMPTS:-180}"

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

require_safe_bench_root() {
  require_no_control_characters "$BENCH_ROOT" "MNEMONAS_BENCH_ROOT"

  if path_has_parent_segment "$BENCH_ROOT"; then
    echo "MNEMONAS_BENCH_ROOT must not contain '..' path segments: $BENCH_ROOT" >&2
    exit 1
  fi

  case "$BENCH_ROOT" in
    /tmp/*|"$PROJECT_ROOT"/*) ;;
    *)
      echo "MNEMONAS_BENCH_ROOT must be under /tmp or this checkout: $BENCH_ROOT" >&2
      exit 1
      ;;
  esac
  require_no_symlink_components "$BENCH_ROOT" "MNEMONAS_BENCH_ROOT"
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

http_host_for_url() {
  local host="$1"
  if [[ "$host" == *:* && "$host" != \[*\] ]]; then
    printf '[%s]\n' "$host"
    return 0
  fi
  printf '%s\n' "$host"
}

require_safe_tcp_host() {
  local value="$1"
  local label="$2"

  [[ -n "$value" ]] || { echo "$label cannot be empty" >&2; exit 1; }
  [[ "$value" != *[[:space:]]* ]] || { echo "$label cannot contain whitespace: $value" >&2; exit 1; }
  [[ "$value" != *[[:cntrl:]]* ]] || { echo "$label cannot contain control characters: $value" >&2; exit 1; }
  is_valid_tcp_host "$value" || { echo "$label host is invalid: $value" >&2; exit 1; }
}

require_loopback_host() {
  local value="$1"
  local label="$2"

  is_loopback_host "$value" || { echo "$label must be loopback-only for isolated test backends: $value" >&2; exit 1; }
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

require_loopback_tcp_addr() {
  local value="$1"
  local label="$2"
  local host

  host="$(tcp_addr_host "$value")" || { echo "$label must be a host:port address: $value" >&2; exit 1; }
  is_loopback_host "$host" || { echo "$label must be loopback-only for isolated test backends: $value" >&2; exit 1; }
}

require_positive_integer() {
  local value="$1"
  local label="$2"

  [[ "$value" =~ ^[0-9]+$ ]] || { echo "$label must be a positive integer: $value" >&2; exit 1; }
  (( 10#$value > 0 )) || { echo "$label must be a positive integer: $value" >&2; exit 1; }
}

show_backend_logs() {
  local log
  for log in "$LOG_DIR/dataplane.log" "$LOG_DIR/nasd.log"; do
    if [[ -f "$log" ]]; then
      echo "---- $log ----" >&2
      tail -80 "$log" >&2 || true
    fi
  done
}

wait_for_backend_ready() {
  for _ in $(seq 1 "$READY_ATTEMPTS"); do
    if [[ -f "$READY_FILE" ]] && curl -sf "$BASE_URL/health" >/dev/null 2>&1; then
      return 0
    fi

    if ! kill -0 "$backend_pid" 2>/dev/null; then
      set +e
      wait "$backend_pid"
      local status=$?
      set -e
      echo "isolated benchmark backend exited before it was ready (status $status)" >&2
      show_backend_logs
      exit "$status"
    fi

    sleep 0.5
  done

  echo "isolated benchmark backend did not become ready at $BASE_URL" >&2
  show_backend_logs
  return 1
}

cleanup() {
  if [[ -n "${backend_pid:-}" ]]; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
  fi
}

require_safe_bench_root
require_safe_tcp_host "$NASD_HOST" "MNEMONAS_BENCH_NASD_HOST"
require_loopback_host "$NASD_HOST" "MNEMONAS_BENCH_NASD_HOST"
require_safe_tcp_port "$NASD_PORT" "MNEMONAS_BENCH_NASD_PORT"
require_safe_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_BENCH_DATAPLANE_HTTP"
require_loopback_tcp_addr "$DATAPLANE_HTTP" "MNEMONAS_BENCH_DATAPLANE_HTTP"
require_safe_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_BENCH_DATAPLANE_GRPC"
require_loopback_tcp_addr "$DATAPLANE_GRPC" "MNEMONAS_BENCH_DATAPLANE_GRPC"
require_positive_integer "$READY_ATTEMPTS" "MNEMONAS_BENCH_READY_ATTEMPTS"
BASE_URL="http://$(http_host_for_url "$NASD_HOST"):${NASD_PORT}"

(
  cd "$PROJECT_ROOT"
  MNEMONAS_E2E_ROOT="$BENCH_ROOT" \
    MNEMONAS_E2E_NASD_HOST="$NASD_HOST" \
    MNEMONAS_E2E_NASD_PORT="$NASD_PORT" \
    MNEMONAS_E2E_DATAPLANE_HTTP="$DATAPLANE_HTTP" \
    MNEMONAS_E2E_DATAPLANE_GRPC="$DATAPLANE_GRPC" \
    GOSUMDB="${GOSUMDB:-sum.golang.org}" \
    GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" \
    bash ./web/scripts/start-e2e-backend.sh
) &
backend_pid=$!

trap cleanup EXIT INT TERM

wait_for_backend_ready
echo "Running benchmark against isolated backend: $BASE_URL"

MNEMONAS_STORAGE_ROOT="$STORAGE_ROOT" \
  CONFIG_FILE="$CONFIG_FILE" \
  SECRETS_FILE="$SECRETS_FILE" \
  INITIAL_PASSWORD_FILE="$INITIAL_PASSWORD_FILE" \
  "$PROJECT_ROOT/scripts/benchmark.sh" "$BASE_URL" "$@"
