#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -eq 0 ]; then
    echo "usage: $0 <command> [args...]" >&2
    exit 1
fi

PROJECT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DATAPLANE_DIR="$PROJECT_ROOT/dataplane"
GRPC_ADDR="${MNEMONAS_TEST_DATAPLANE_ADDR:-127.0.0.1:19090}"
HTTP_ADDR="${MNEMONAS_TEST_DATAPLANE_HTTP_ADDR:-127.0.0.1:19091}"
DATAPLANE_PID=""

fail() {
    echo "with-test-dataplane: $*" >&2
    exit 1
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
    (( 10#$port >= 1 && 10#$port <= 65535 )) || fail "$label port must be between 1 and 65535: $value"
}

require_loopback_tcp_addr() {
    local value="$1"
    local label="$2"
    local host

    host="$(tcp_addr_host "$value")" || fail "$label must be a host:port address: $value"
    is_loopback_host "$host" || fail "$label must be loopback-only for test dataplane: $value"
}

require_safe_tcp_addr "$GRPC_ADDR" "MNEMONAS_TEST_DATAPLANE_ADDR"
require_loopback_tcp_addr "$GRPC_ADDR" "MNEMONAS_TEST_DATAPLANE_ADDR"
require_safe_tcp_addr "$HTTP_ADDR" "MNEMONAS_TEST_DATAPLANE_HTTP_ADDR"
require_loopback_tcp_addr "$HTTP_ADDR" "MNEMONAS_TEST_DATAPLANE_HTTP_ADDR"

HEALTH_URL="http://$HTTP_ADDR/health"
DATA_DIR=$(mktemp -d "${TMPDIR:-/tmp}/mnemonas-test-dataplane.XXXXXX")
LOG_FILE=$(mktemp "${TMPDIR:-/tmp}/mnemonas-test-dataplane-log.XXXXXX")

cleanup() {
    if [ -n "$DATAPLANE_PID" ] && kill -0 "$DATAPLANE_PID" >/dev/null 2>&1; then
        kill "$DATAPLANE_PID" >/dev/null 2>&1 || true
        wait "$DATAPLANE_PID" >/dev/null 2>&1 || true
    fi
    rm -rf -- "$DATA_DIR"
    rm -f -- "$LOG_FILE"
}

trap cleanup EXIT INT TERM

if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    echo "test dataplane endpoint $HTTP_ADDR is already in use; stop the existing service or override MNEMONAS_TEST_DATAPLANE_ADDR/MNEMONAS_TEST_DATAPLANE_HTTP_ADDR" >&2
    exit 1
fi

cd "$DATAPLANE_DIR"
cargo build --quiet --bin dataplane
./target/debug/dataplane --grpc "$GRPC_ADDR" --listen "$HTTP_ADDR" --data-dir "$DATA_DIR" --log-level warn >"$LOG_FILE" 2>&1 &
DATAPLANE_PID=$!
cd "$PROJECT_ROOT"

for _ in $(seq 1 40); do
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
        export MNEMONAS_TEST_DATAPLANE_ADDR="$GRPC_ADDR"
        export MNEMONAS_TEST_DATAPLANE_HTTP_ADDR="$HTTP_ADDR"
        "$@"
        exit $?
    fi

    if ! kill -0 "$DATAPLANE_PID" >/dev/null 2>&1; then
        echo "failed to start test dataplane" >&2
        cat "$LOG_FILE" >&2
        exit 1
    fi

    sleep 0.25
done

echo "timed out waiting for test dataplane at $HEALTH_URL" >&2
cat "$LOG_FILE" >&2
exit 1
