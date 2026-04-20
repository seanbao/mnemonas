#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[docker-smoke-test] ERROR: %s\n' "$*" >&2
	exit 1
}

assert_file_contains() {
	local path="$1"
	local expected="$2"

	grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_file_not_exists() {
	local path="$1"

	[[ ! -e "$path" ]] || fail "$path exists unexpectedly"
}

write_executable() {
	local path="$1"
	shift

	printf '%s\n' "$@" > "$path"
	chmod +x "$path"
}

setup_fake_tools() {
	local bin_dir="$1"
	mkdir -p "$bin_dir"

	write_executable "$bin_dir/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'mkdir -p "$FAKE_DOCKER_STATE"' \
		'case "$1" in' \
		'  run)' \
		'    shift' \
		'    printf "%s\n" "$*" > "$FAKE_DOCKER_STATE/run.args"' \
		'    [[ "${FAKE_DOCKER_RUN_FAIL:-0}" == "1" ]] && { printf "fake docker run failed\n" >&2; exit 125; }' \
		'    name=""' \
		'    while [[ "$#" -gt 0 ]]; do' \
		'      case "$1" in' \
		'        --name)' \
		'          name="$2"; shift 2 ;;' \
		'        -p)' \
		'          printf "%s\n" "$2" > "$FAKE_DOCKER_STATE/port.args"; shift 2 ;;' \
		'        -d)' \
		'          shift ;;' \
		'        *)' \
		'          printf "%s\n" "$1" > "$FAKE_DOCKER_STATE/image.args"; shift ;;' \
		'      esac' \
		'    done' \
		'    printf "%s\n" "$name" > "$FAKE_DOCKER_STATE/container.name"' \
		'    [[ "${FAKE_DOCKER_START_RUNNING:-1}" == "1" ]] && touch "$FAKE_DOCKER_STATE/running"' \
		'    printf "fake-container-id\n" ;;' \
		'  ps)' \
		'    if [[ -f "$FAKE_DOCKER_STATE/running" ]]; then' \
		'      cat "$FAKE_DOCKER_STATE/container.name"' \
		'    fi ;;' \
		'  port)' \
		'    printf "%s\n" "$*" > "$FAKE_DOCKER_STATE/port-query.args"' \
		'    printf "%s\n" "${FAKE_DOCKER_PORT_OUTPUT:-127.0.0.1:19181}" ;;' \
		'  logs)' \
		'    printf "%s\n" "$*" > "$FAKE_DOCKER_STATE/logs.args"' \
		'    printf "fake container logs\n" ;;' \
		'  rm)' \
		'    printf "%s\n" "$*" > "$FAKE_DOCKER_STATE/rm.args"' \
		'    rm -f "$FAKE_DOCKER_STATE/running" ;;' \
		'  *)' \
		'    printf "unexpected docker command: %s\n" "$*" >&2' \
		'    exit 99 ;;' \
		'esac'

	write_executable "$bin_dir/curl" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" >> "$FAKE_DOCKER_STATE/curl.args"' \
		'for arg in "$@"; do' \
		'  case "$arg" in' \
		'    --connect-timeout=*|--max-time=*)' \
		'      printf "fake curl rejected non-portable timeout syntax: %s\n" "$arg" >&2' \
		'      exit 2 ;;' \
		'  esac' \
		'done' \
		'url="${*: -1}"' \
		'if [[ "$url" == */health ]]; then' \
		'  [[ "${FAKE_CURL_HEALTH_FAIL:-0}" == "1" ]] && exit 7' \
		'  if [[ -v FAKE_CURL_HEALTH ]]; then' \
		'    printf "%s\n" "$FAKE_CURL_HEALTH"' \
		'  else' \
		'    printf "%s\n" "{\"version\":\"ci\"}"' \
		'  fi' \
		'elif [[ "$url" == */ ]]; then' \
		'  [[ "${FAKE_CURL_ROOT_FAIL:-0}" == "1" ]] && exit 22' \
		'  printf "%s\n" "${FAKE_CURL_ROOT:-<div id=\"root\"></div>}"' \
		'else' \
		'  exit 22' \
		'fi'
}

run_smoke_passes_and_cleans_container() {
	local case_dir="$TMP_ROOT/pass"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-test" \
		MNEMONAS_DOCKER_SMOKE_PORT="19080" \
		MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION="ci" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out"

	assert_file_contains "$out" "[docker-smoke] mnemonas:test passed health and frontend checks"
	assert_file_contains "$state_dir/run.args" "--name"
	assert_file_contains "$state_dir/run.args" "mnemonas-test"
	assert_file_contains "$state_dir/port.args" "127.0.0.1:19080:8080"
	assert_file_contains "$state_dir/image.args" "mnemonas:test"
	assert_file_contains "$state_dir/curl.args" "--connect-timeout 3"
	assert_file_contains "$state_dir/curl.args" "--max-time 10"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-test"
	assert_file_not_exists "$state_dir/running"
}

run_smoke_uses_dynamic_loopback_port_by_default() {
	local case_dir="$TMP_ROOT/dynamic-port"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-dynamic-test" \
		MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION="ci" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out"

	assert_file_contains "$out" "[docker-smoke] mnemonas:test passed health and frontend checks at http://127.0.0.1:19181"
	assert_file_contains "$state_dir/port.args" "127.0.0.1::8080"
	assert_file_contains "$state_dir/port-query.args" "mnemonas-dynamic-test 8080/tcp"
	assert_file_contains "$state_dir/curl.args" "http://127.0.0.1:19181/health"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-dynamic-test"
	assert_file_not_exists "$state_dir/running"
}

run_smoke_uses_curl_timeout_overrides() {
	local case_dir="$TMP_ROOT/curl-timeout-overrides"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-timeout-test" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS="1" \
		CURL_CONNECT_TIMEOUT="7" \
		CURL_MAX_TIME="13" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out"

	assert_file_contains "$out" "[docker-smoke] mnemonas:test passed health and frontend checks"
	assert_file_contains "$state_dir/curl.args" "--connect-timeout 7"
	assert_file_contains "$state_dir/curl.args" "--max-time 13"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-timeout-test"
}

run_smoke_accepts_empty_health_body_without_expected_version() {
	local case_dir="$TMP_ROOT/empty-health-no-version"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_CURL_HEALTH="" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-empty-health-test" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		MNEMONAS_DOCKER_SMOKE_SLEEP_SECONDS="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out"

	assert_file_contains "$out" "[docker-smoke] mnemonas:test passed health and frontend checks"
	assert_file_contains "$state_dir/curl.args" "http://127.0.0.1:19181/health"
	assert_file_contains "$state_dir/curl.args" "http://127.0.0.1:19181/"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-empty-health-test"
	assert_file_not_exists "$state_dir/running"
}

run_smoke_requires_health_body_for_expected_version() {
	local case_dir="$TMP_ROOT/empty-health-with-version"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_CURL_HEALTH="" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-empty-version-test" \
		MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION="ci" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted empty health response with an expected version"
	assert_file_contains "$out" "health endpoint returned empty response while expecting version ci"
	assert_file_contains "$out" "fake container logs"
	assert_file_contains "$state_dir/logs.args" "mnemonas-empty-version-test"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-empty-version-test"
}

run_smoke_rejects_version_mismatch_and_prints_logs() {
	local case_dir="$TMP_ROOT/version-mismatch"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_CURL_HEALTH='{"version":"dev"}' \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-version-test" \
		MNEMONAS_DOCKER_SMOKE_EXPECT_VERSION="ci" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted mismatched health version"
	assert_file_contains "$out" "health endpoint version did not match ci"
	assert_file_contains "$out" "fake container logs"
	assert_file_contains "$state_dir/logs.args" "mnemonas-version-test"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-version-test"
}

run_smoke_fails_when_container_exits_before_health() {
	local case_dir="$TMP_ROOT/exited"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_CURL_HEALTH_FAIL="1" \
		FAKE_DOCKER_START_RUNNING="0" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-exited-test" \
		MNEMONAS_DOCKER_SMOKE_RETRIES="1" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted exited container"
	assert_file_contains "$out" "container exited before becoming healthy"
	assert_file_contains "$out" "fake container logs"
	assert_file_contains "$state_dir/rm.args" "-f mnemonas-exited-test"
}

run_smoke_does_not_remove_existing_container_when_run_fails() {
	local case_dir="$TMP_ROOT/run-fails"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		FAKE_DOCKER_RUN_FAIL="1" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-existing-test" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted failed docker run"
	assert_file_contains "$out" "fake docker run failed"
	assert_file_not_exists "$state_dir/logs.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_rejects_invalid_loopback_host_before_run() {
	local case_dir="$TMP_ROOT/invalid-host"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_HOST="127.example.com" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-invalid-host-test" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted non-numeric loopback host"
	assert_file_contains "$out" "MNEMONAS_DOCKER_SMOKE_HOST must be a 127.0.0.0/8 loopback address"
	assert_file_not_exists "$state_dir/run.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_rejects_host_with_whitespace_before_run() {
	local case_dir="$TMP_ROOT/whitespace-host"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_HOST="127.0.0.1 bad" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-whitespace-host-test" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted loopback host with whitespace"
	assert_file_contains "$out" "MNEMONAS_DOCKER_SMOKE_HOST must be a 127.0.0.0/8 loopback address"
	assert_file_not_exists "$state_dir/run.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_rejects_image_reference_that_starts_with_dash() {
	local case_dir="$TMP_ROOT/dash-image"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-dash-image-test" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" --privileged > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted an option-like image reference"
	assert_file_contains "$out" "Docker image reference must not start with '-'"
	assert_file_not_exists "$state_dir/run.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_rejects_image_reference_with_whitespace() {
	local case_dir="$TMP_ROOT/whitespace-image"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-whitespace-image-test" \
		MNEMONAS_DOCKER_SMOKE_IMAGE="mnemonas:test bad" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted an image reference with whitespace"
	assert_file_contains "$out" "Docker image reference must not contain whitespace or control characters"
	assert_file_not_exists "$state_dir/run.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_rejects_invalid_curl_timeout_before_run() {
	local case_dir="$TMP_ROOT/invalid-curl-timeout"
	local fake_bin="$case_dir/bin"
	local state_dir="$case_dir/state"
	local out="$case_dir/out.log"
	local status
	mkdir -p "$state_dir"
	setup_fake_tools "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		FAKE_DOCKER_STATE="$state_dir" \
		CURL_CONNECT_TIMEOUT="0" \
		MNEMONAS_DOCKER_SMOKE_CONTAINER="mnemonas-invalid-timeout-test" \
		bash "$REPO_ROOT/scripts/docker-smoke.sh" mnemonas:test > "$out" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker smoke accepted invalid curl connect timeout"
	assert_file_contains "$out" "CURL_CONNECT_TIMEOUT must be a positive integer"
	assert_file_not_exists "$state_dir/run.args"
	assert_file_not_exists "$state_dir/rm.args"
}

run_smoke_passes_and_cleans_container
run_smoke_uses_dynamic_loopback_port_by_default
run_smoke_uses_curl_timeout_overrides
run_smoke_accepts_empty_health_body_without_expected_version
run_smoke_requires_health_body_for_expected_version
run_smoke_rejects_version_mismatch_and_prints_logs
run_smoke_fails_when_container_exits_before_health
run_smoke_does_not_remove_existing_container_when_run_fails
run_smoke_rejects_invalid_loopback_host_before_run
run_smoke_rejects_host_with_whitespace_before_run
run_smoke_rejects_image_reference_that_starts_with_dash
run_smoke_rejects_image_reference_with_whitespace
run_smoke_rejects_invalid_curl_timeout_before_run

printf '[docker-smoke-test] all checks passed\n'
