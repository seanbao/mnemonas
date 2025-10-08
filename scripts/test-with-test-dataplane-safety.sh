#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
  printf '[with-test-dataplane-safety-test] ERROR: %s\n' "$*" >&2
  exit 1
}

assert_file_contains() {
  local path="$1"
  local expected="$2"

  grep -Fq -- "$expected" "$path" || fail "$path does not contain: $expected"
}

assert_not_exists() {
  local path="$1"

  [[ ! -e "$path" ]] || fail "$path unexpectedly exists"
}

write_executable() {
  local path="$1"
  shift

  printf '%s\n' "$@" > "$path"
  chmod +x "$path"
}

run_expect_failure() {
  local out="$1"
  shift
  local status

  set +e
  "$@" </dev/null > "$out" 2>&1
  status=$?
  set -e

  [[ "$status" -ne 0 ]] || fail "command unexpectedly succeeded: $*"
}

run_invalid_grpc_address_test() {
  local out="$TMP_ROOT/invalid-grpc.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_ADDR="127.0.0.1:70000" \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_ADDR port must be between 1 and 65535"
}

run_invalid_http_address_test() {
  local out="$TMP_ROOT/invalid-http.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR=$'127.0.0.1:19091\n-XPOST' \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_HTTP_ADDR cannot contain whitespace"
}

run_control_character_http_address_test() {
  local out="$TMP_ROOT/control-character-http.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR="127.0.0.1:19091"$'\a' \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_HTTP_ADDR cannot contain control characters"
}

run_non_loopback_grpc_address_test() {
  local out="$TMP_ROOT/non-loopback-grpc.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_ADDR="0.0.0.0:19090" \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR=$'127.0.0.1:19091\n-XPOST' \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_ADDR must be loopback-only"
}

run_loopback_name_spoof_grpc_address_test() {
  local out="$TMP_ROOT/loopback-name-spoof-grpc.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_ADDR="127.example.com:19090" \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR=$'127.0.0.1:19091\n-XPOST' \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_ADDR must be loopback-only"
}

run_non_loopback_http_address_test() {
  local case_dir="$TMP_ROOT/non-loopback-http"
  local fake_bin="$case_dir/bin"
  local out="$case_dir/out.log"
  mkdir -p "$fake_bin"

  write_executable "$fake_bin/cargo" \
    '#!/usr/bin/env bash' \
    'printf "cargo should not run\n" > "$CARGO_INVOKED_LOG"' \
    'exit 99'

  run_expect_failure "$out" env \
    PATH="$fake_bin:$PATH" \
    CARGO_INVOKED_LOG="$case_dir/cargo.log" \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR="0.0.0.0:19091" \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_HTTP_ADDR must be loopback-only"
  assert_not_exists "$case_dir/cargo.log"
}

run_conflicting_explicit_ports_test() {
  local out="$TMP_ROOT/conflicting-explicit-ports.log"

  run_expect_failure "$out" env \
    MNEMONAS_TEST_DATAPLANE_ADDR="127.0.0.1:19090" \
    MNEMONAS_TEST_DATAPLANE_HTTP_ADDR="127.0.0.1:19090" \
    bash "$REPO_ROOT/scripts/with-test-dataplane.sh" true

  assert_file_contains "$out" "MNEMONAS_TEST_DATAPLANE_ADDR and MNEMONAS_TEST_DATAPLANE_HTTP_ADDR must use different ports"
}

run_auto_port_docs_test() {
  assert_file_contains "$REPO_ROOT/scripts/with-test-dataplane.sh" "pick_loopback_port"
  assert_file_contains "$REPO_ROOT/docs/development.md" "未显式设置"
  assert_file_contains "$REPO_ROOT/docs/development.en.md" "When unset"
}

run_invalid_grpc_address_test
run_invalid_http_address_test
run_control_character_http_address_test
run_non_loopback_grpc_address_test
run_loopback_name_spoof_grpc_address_test
run_non_loopback_http_address_test
run_conflicting_explicit_ports_test
run_auto_port_docs_test

printf '[with-test-dataplane-safety-test] all checks passed\n'
