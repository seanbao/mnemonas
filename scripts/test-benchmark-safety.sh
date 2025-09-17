#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[benchmark-safety-test] ERROR: %s\n' "$*" >&2
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

assert_exists() {
	local path="$1"
	[[ -e "$path" ]] || fail "$path unexpectedly missing"
}

make_fake_curl() {
	local bin_dir="$1"
	local invoked_log="$2"
	mkdir -p "$bin_dir"
	cat > "$bin_dir/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CURL_INVOKED_LOG"
exit 7
EOF
	chmod +x "$bin_dir/curl"
	: > "$invoked_log"
	rm -f -- "$invoked_log"
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

run_missing_explicit_target_test() {
	local case_dir="$TMP_ROOT/missing-explicit-target"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/home/.mnemonas/files/benchmark-test"
	printf 'keep\n' > "$case_dir/home/.mnemonas/files/benchmark-test/sentinel.txt"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/benchmark.sh"

	assert_file_contains "$case_dir/out.log" "explicit base URL is required"
	assert_not_exists "$invoked_log"
	assert_exists "$case_dir/home/.mnemonas/files/benchmark-test/sentinel.txt"
}

run_missing_storage_root_test() {
	local case_dir="$TMP_ROOT/missing-storage-root"
	mkdir -p "$case_dir/home/.mnemonas/files/benchmark-test"
	printf 'keep\n' > "$case_dir/home/.mnemonas/files/benchmark-test/sentinel.txt"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "explicit MNEMONAS_STORAGE_ROOT is required"
	assert_exists "$case_dir/home/.mnemonas/files/benchmark-test/sentinel.txt"
}

run_refuse_invalid_base_url_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-base-url"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/storage/files/benchmark-test"
	printf 'keep\n' > "$case_dir/storage/files/benchmark-test/sentinel.txt"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "file:///etc/passwd"

	assert_file_contains "$case_dir/out.log" "base URL must be an http(s) URL"
	assert_not_exists "$invoked_log"
	assert_exists "$case_dir/storage/files/benchmark-test/sentinel.txt"
}

run_refuse_unisolated_storage_test() {
	local case_dir="$TMP_ROOT/refuse-unisolated-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		MNEMONAS_STORAGE_ROOT="/var/lib/mnemonas-benchmark" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT must be under /tmp or this checkout"
}

run_refuse_traversal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-traversal-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		MNEMONAS_STORAGE_ROOT="/tmp/../var/lib/mnemonas-benchmark" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT must not contain '..' path segments"
}

run_refuse_relative_storage_test() {
	local case_dir="$TMP_ROOT/refuse-relative-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_STORAGE_ROOT="relative-storage" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT must be an absolute path"
}

run_refuse_protected_storage_with_override_test() {
	local case_dir="$TMP_ROOT/refuse-protected-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_STORAGE_ROOT="/tmp" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT points at a protected system directory"
}

run_refuse_symlink_storage_test() {
	local case_dir="$TMP_ROOT/refuse-symlink-storage"
	local target_dir="$case_dir/target"
	local link_dir="$case_dir/link"
	mkdir -p "$target_dir/files/benchmark-test"
	printf 'keep\n' > "$target_dir/files/benchmark-test/sentinel.txt"
	ln -s "$target_dir" "$link_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		MNEMONAS_STORAGE_ROOT="$link_dir" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT must not contain symlink path components"
	assert_exists "$target_dir/files/benchmark-test/sentinel.txt"
}

run_refuse_traversal_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-traversal-isolated-root"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="/tmp/../var/lib/mnemonas-benchmark" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_ROOT must not contain '..' path segments"
}

run_refuse_symlink_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-symlink-isolated-root"
	local target_dir="$case_dir/target"
	local link_dir="$case_dir/link"
	mkdir -p "$target_dir"
	ln -s "$target_dir" "$link_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$link_dir" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_ROOT must not contain symlink path components"
	assert_not_exists "$target_dir/backend"
}

run_refuse_invalid_isolated_addr_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-isolated-addr"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$case_dir/root" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_DATAPLANE_GRPC port must be between 1 and 65535"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_invalid_isolated_ready_attempts_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-isolated-ready-attempts"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$case_dir/root" \
		MNEMONAS_BENCH_READY_ATTEMPTS=0 \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_READY_ATTEMPTS must be a positive integer"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_default_personal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-default-storage"
	local fake_home="$case_dir/home"
	mkdir -p "$fake_home"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$fake_home" \
		MNEMONAS_STORAGE_ROOT="$fake_home/.mnemonas" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "refusing to benchmark against default personal storage root"
}

run_isolated_target_reaches_health_check_test() {
	local case_dir="$TMP_ROOT/isolated-target"
	mkdir -p "$case_dir/storage"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "PROPFIND / failed to reach"
	assert_not_exists "$case_dir/storage/files/benchmark-test"
}

run_missing_explicit_target_test
run_missing_storage_root_test
run_refuse_invalid_base_url_test
run_refuse_unisolated_storage_test
run_refuse_traversal_storage_test
run_refuse_relative_storage_test
run_refuse_protected_storage_with_override_test
run_refuse_symlink_storage_test
run_refuse_traversal_isolated_root_test
run_refuse_symlink_isolated_root_test
run_refuse_invalid_isolated_addr_test
run_refuse_invalid_isolated_ready_attempts_test
run_refuse_default_personal_storage_test
run_isolated_target_reaches_health_check_test

printf '[benchmark-safety-test] all checks passed\n'
