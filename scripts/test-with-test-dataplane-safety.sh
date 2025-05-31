#!/usr/bin/env bash

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

run_expect_failure() {
  local out="$1"
  shift
  local status

  set +e
  "$@" > "$out" 2>&1
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

run_invalid_grpc_address_test
run_invalid_http_address_test

printf '[with-test-dataplane-safety-test] all checks passed\n'
