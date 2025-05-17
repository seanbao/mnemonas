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
BASE_URL="http://${NASD_HOST}:${NASD_PORT}"
READY_ATTEMPTS="${MNEMONAS_BENCH_READY_ATTEMPTS:-180}"

require_safe_bench_root() {
  case "$BENCH_ROOT" in
    /tmp/*|"$PROJECT_ROOT"/*) ;;
    *)
      echo "MNEMONAS_BENCH_ROOT must be under /tmp or this checkout: $BENCH_ROOT" >&2
      exit 1
      ;;
  esac
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
