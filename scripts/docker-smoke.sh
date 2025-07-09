#!/usr/bin/env bash

set -euo pipefail

IMAGE="${1:-${MNEMONAS_DOCKER_SMOKE_IMAGE:-mnemonas:latest}}"
HOST="${MNEMONAS_DOCKER_SMOKE_HOST:-127.0.0.1}"
PORT="${MNEMONAS_DOCKER_SMOKE_PORT:-18080}"
CONTAINER_NAME="${MNEMONAS_DOCKER_SMOKE_CONTAINER:-mnemonas-smoke-$$}"
EXPECTED_VERSION="${MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION:-}"
RETRIES="${MNEMONAS_DOCKER_SMOKE_RETRIES:-40}"
SLEEP_SECONDS="${MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS:-1}"
CONTAINER_STARTED=0

fail() {
	printf 'docker-smoke: %s\n' "$*" >&2
	exit 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

require_positive_integer() {
	local label="$1"
	local value="$2"

	[[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a positive integer: $value"
	(( value > 0 )) || fail "$label must be a positive integer: $value"
}

require_safe_loopback_host() {
	local value="$1"

	case "$value" in
		127.*)
			return 0
			;;
	esac
	fail "MNEMONAS_DOCKER_SMOKE_HOST must be a 127.0.0.0/8 loopback address: $value"
}

require_safe_container_name() {
	local value="$1"

	[[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || fail "invalid container name: $value"
}

require_command docker
require_command curl
require_safe_loopback_host "$HOST"
require_positive_integer "MNEMONAS_DOCKER_SMOKE_PORT" "$PORT"
(( PORT <= 65535 )) || fail "MNEMONAS_DOCKER_SMOKE_PORT must be <= 65535: $PORT"
require_positive_integer "MNEMONAS_DOCKER_SMOKE_RETRIES" "$RETRIES"
require_positive_integer "MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS" "$SLEEP_SECONDS"
require_safe_container_name "$CONTAINER_NAME"

BASE_URL="http://${HOST}:${PORT}"

# shellcheck disable=SC2317 # Invoked indirectly by the EXIT trap.
cleanup() {
	local status=$?
	if [[ "$CONTAINER_STARTED" != "1" ]]; then
		exit "$status"
	fi
	if [[ "$status" -ne 0 ]]; then
		docker logs "$CONTAINER_NAME" || true
	fi
	docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT

docker run -d --name "$CONTAINER_NAME" -p "${HOST}:${PORT}:8080" "$IMAGE" >/dev/null
CONTAINER_STARTED=1

for ((attempt = 1; attempt <= RETRIES; attempt++)); do
	health_json="$(curl -fsS "${BASE_URL}/health" 2>/dev/null || true)"
	if [[ -n "$health_json" ]]; then
		if [[ -n "$EXPECTED_VERSION" && "$health_json" != *"\"version\":\"${EXPECTED_VERSION}\""* ]]; then
			fail "health endpoint version did not match ${EXPECTED_VERSION}: ${health_json}"
		fi
		if ! curl -fsS -H 'Accept: text/html' "${BASE_URL}/" | grep -q 'id="root"'; then
			fail "frontend root did not contain id=\"root\""
		fi
		printf '[docker-smoke] %s passed health and frontend checks at %s\n' "$IMAGE" "$BASE_URL"
		exit 0
	fi

	if ! docker ps --format '{{.Names}}' | grep -Fxq "$CONTAINER_NAME"; then
		fail "container exited before becoming healthy"
	fi

	sleep "$SLEEP_SECONDS"
done

fail "timed out waiting for health endpoint at ${BASE_URL}/health"
