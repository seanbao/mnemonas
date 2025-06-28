#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[fault-injection-safety-test] ERROR: %s\n' "$*" >&2
	exit 1
}

write_executable() {
	local path="$1"
	shift
	printf '%s\n' "$@" > "$path"
	chmod +x "$path"
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

make_fake_nasd() {
	local path="$1"
	write_executable "$path" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" >> "$NASD_INVOKED_LOG"'
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

run_default_disabled_test() {
	local case_dir="$TMP_ROOT/default-disabled"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$case_dir/storage" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "live fault injection is disabled"
	assert_not_exists "$invoked_log"
}

run_missing_explicit_target_test() {
	local case_dir="$TMP_ROOT/missing-explicit-target"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "explicit BASE_URL STORAGE_ROOT NASD_BIN required"
}

run_refuse_unisolated_storage_test() {
	local case_dir="$TMP_ROOT/refuse-unisolated-storage"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/var/lib/mnemonas-fault-test" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must be under /tmp or this checkout"
	assert_not_exists "$invoked_log"
}

run_refuse_invalid_base_url_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-base-url"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir/storage"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="file:///etc/passwd" \
		STORAGE_ROOT="$case_dir/storage" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "BASE_URL must be an http(s) URL"
	assert_not_exists "$invoked_log"
}

run_refuse_invalid_nasd_pid_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-nasd-pid"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir/storage"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$case_dir/storage" \
		NASD_BIN="$fake_nasd" \
		NASD_PID="abc" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "NASD_PID must be a numeric PID"
	assert_not_exists "$invoked_log"
}

run_refuse_traversal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-traversal-storage"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/tmp/../var/lib/mnemonas-fault-test" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must not contain '..' path segments"
	assert_not_exists "$invoked_log"
}

run_refuse_relative_storage_test() {
	local case_dir="$TMP_ROOT/refuse-relative-storage"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="relative-storage" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must be an absolute path"
	assert_not_exists "$invoked_log"
}

run_refuse_protected_storage_with_override_test() {
	local case_dir="$TMP_ROOT/refuse-protected-storage"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/tmp" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT points at a protected system directory"
	assert_not_exists "$invoked_log"
}

run_refuse_symlink_storage_test() {
	local case_dir="$TMP_ROOT/refuse-symlink-storage"
	local target_dir="$case_dir/target"
	local link_dir="$case_dir/link"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$target_dir"
	ln -s "$target_dir" "$link_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$link_dir" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must not contain symlink path components"
	assert_not_exists "$invoked_log"
}

run_refuse_default_personal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-default-storage"
	local fake_home="$case_dir/home"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$fake_home"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		HOME="$fake_home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$fake_home/.mnemonas" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "refusing to run against default personal storage root"
	assert_not_exists "$invoked_log"
}

run_noninteractive_confirmation_test() {
	local case_dir="$TMP_ROOT/noninteractive-confirmation"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$case_dir/storage" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "non-interactive live fault injection requires FAULT_INJECTION_ASSUME_YES=1"
	assert_not_exists "$invoked_log"
}

run_health_failure_does_not_restart_test() {
	local case_dir="$TMP_ROOT/health-failure"
	local fake_nasd="$case_dir/nasd"
	local invoked_log="$case_dir/nasd.log"
	mkdir -p "$case_dir"
	make_fake_nasd "$fake_nasd"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_LIVE_FAULTS=1 \
		FAULT_INJECTION_ASSUME_YES=1 \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$case_dir/storage" \
		NASD_BIN="$fake_nasd" \
		NASD_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/fault-injection-test.sh"

	assert_file_contains "$case_dir/out.log" "MnemoNAS service not running at http://127.0.0.1:9"
	assert_not_exists "$invoked_log"
}

run_default_disabled_test
run_missing_explicit_target_test
run_refuse_unisolated_storage_test
run_refuse_invalid_base_url_test
run_refuse_invalid_nasd_pid_test
run_refuse_traversal_storage_test
run_refuse_relative_storage_test
run_refuse_protected_storage_with_override_test
run_refuse_symlink_storage_test
run_refuse_default_personal_storage_test
run_noninteractive_confirmation_test
run_health_failure_does_not_restart_test

printf '[fault-injection-safety-test] all checks passed\n'
