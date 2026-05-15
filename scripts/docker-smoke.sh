#!/usr/bin/env bash

set -euo pipefail

IMAGE="${1:-${MNEMONAS_DOCKER_SMOKE_IMAGE:-mnemonas:latest}}"
HOST="${MNEMONAS_DOCKER_SMOKE_HOST:-127.0.0.1}"
PORT="${MNEMONAS_DOCKER_SMOKE_PORT:-}"
CONTAINER_NAME="${MNEMONAS_DOCKER_SMOKE_CONTAINER:-mnemonas-smoke-$$}"
EXPECTED_VERSION="${MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION:-}"
RETRIES="${MNEMONAS_DOCKER_SMOKE_RETRIES:-40}"
SLEEP_SECONDS="${MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS:-1}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-3}"
CURL_MAX_TIME="${CURL_MAX_TIME:-10}"
CONTAINER_STARTED=0
PUBLISH_ARG=""
BASE_URL=""

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

require_safe_loopback_host() {
	local value="$1"

	is_ipv4_loopback_host "$value" && return 0
	fail "MNEMONAS_DOCKER_SMOKE_HOST must be a 127.0.0.0/8 loopback address: $value"
}

require_safe_container_name() {
	local value="$1"

	[[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || fail "invalid container name: $value"
}

require_safe_image_ref() {
	local value="$1"

	[[ -n "$value" ]] || fail "Docker image reference must not be empty"
	[[ "$value" != -* ]] || fail "Docker image reference must not start with '-': $value"
	[[ "$value" != *[[:cntrl:][:space:]]* ]] || fail "Docker image reference must not contain whitespace or control characters: $value"
}

require_command docker
require_command curl
require_safe_image_ref "$IMAGE"
require_safe_loopback_host "$HOST"
if [[ -n "$PORT" ]]; then
	require_positive_integer "MNEMONAS_DOCKER_SMOKE_PORT" "$PORT"
	(( PORT <= 65535 )) || fail "MNEMONAS_DOCKER_SMOKE_PORT must be <= 65535: $PORT"
	PUBLISH_ARG="${HOST}:${PORT}:8080"
else
	PUBLISH_ARG="${HOST}::8080"
fi
require_positive_integer "MNEMONAS_DOCKER_SMOKE_RETRIES" "$RETRIES"
require_positive_integer "MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS" "$SLEEP_SECONDS"
require_positive_integer "CURL_CONNECT_TIMEOUT" "$CURL_CONNECT_TIMEOUT"
require_positive_integer "CURL_MAX_TIME" "$CURL_MAX_TIME"
require_safe_container_name "$CONTAINER_NAME"

resolve_base_url() {
	local published=""
	local mapped_host=""
	local mapped_port=""

	if [[ -n "$PORT" ]]; then
		BASE_URL="http://${HOST}:${PORT}"
		return 0
	fi

	published="$(docker port "$CONTAINER_NAME" 8080/tcp || true)"
	[[ -n "$published" ]] || fail "docker did not publish 8080/tcp for ${CONTAINER_NAME}"
	published="${published%%$'\n'*}"
	published="${published##* }"
	mapped_host="${published%:*}"
	mapped_port="${published##*:}"
	[[ -n "$mapped_host" && -n "$mapped_port" && "$mapped_host" != "$published" ]] || fail "unexpected docker port output: $published"

	require_safe_loopback_host "$mapped_host"
	require_positive_integer "docker published port" "$mapped_port"
	(( mapped_port <= 65535 )) || fail "docker published port must be <= 65535: $mapped_port"

	BASE_URL="http://${mapped_host}:${mapped_port}"
}

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

docker run -d --name "$CONTAINER_NAME" -p "$PUBLISH_ARG" "$IMAGE" >/dev/null
CONTAINER_STARTED=1
resolve_base_url

for ((attempt = 1; attempt <= RETRIES; attempt++)); do
	if health_json="$(curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" "${BASE_URL}/health" 2>/dev/null)"; then
		if [[ -n "$EXPECTED_VERSION" && -z "$health_json" ]]; then
			fail "health endpoint returned empty response while expecting version ${EXPECTED_VERSION}"
		fi
		if [[ -n "$EXPECTED_VERSION" && "$health_json" != *"\"version\":\"${EXPECTED_VERSION}\""* ]]; then
			fail "health endpoint version did not match ${EXPECTED_VERSION}: ${health_json}"
		fi
		if ! curl -fsS --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" -H 'Accept: text/html' "${BASE_URL}/" | grep -q 'id="root"'; then
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
