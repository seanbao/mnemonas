#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[e2e-safety-test] ERROR: %s\n' "$*" >&2
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
	mkdir -p "$case_dir/home"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "explicit BASE_URL STORAGE_ROOT CONFIG_FILE SECRETS_FILE INITIAL_PASSWORD_FILE required"
	assert_not_exists "$invoked_log"
}

run_refuse_unisolated_storage_test() {
	local case_dir="$TMP_ROOT/refuse-unisolated-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/var/lib/mnemonas-e2e" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must be under /tmp or this checkout"
}

run_refuse_invalid_base_url_test() {
	local case_dir="$TMP_ROOT/refuse-invalid-base-url"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/storage"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		BASE_URL="file:///etc/passwd" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "BASE_URL must be an http(s) URL"
	assert_not_exists "$invoked_log"
}

run_refuse_traversal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-traversal-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/tmp/../var/lib/mnemonas-e2e" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must not contain '..' path segments"
}

run_refuse_relative_storage_test() {
	local case_dir="$TMP_ROOT/refuse-relative-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="relative-storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must be an absolute path"
}

run_refuse_protected_storage_with_override_test() {
	local case_dir="$TMP_ROOT/refuse-protected-storage"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="/tmp" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		ALLOW_REAL_STORAGE=1 \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT points at a protected system directory"
}

run_refuse_symlink_storage_test() {
	local case_dir="$TMP_ROOT/refuse-symlink-storage"
	local target_dir="$case_dir/target"
	local link_dir="$case_dir/link"
	mkdir -p "$target_dir"
	ln -s "$target_dir" "$link_dir"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$link_dir" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT must not contain symlink path components"
}

run_refuse_traversal_isolated_root_test() {
  local case_dir="$TMP_ROOT/refuse-traversal-isolated-root"
  mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="/tmp/../var/lib/mnemonas-e2e" \
		bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT must not contain '..' path segments"
}

run_refuse_symlink_isolated_root_test() {
  local case_dir="$TMP_ROOT/refuse-symlink-isolated-root"
  local target_dir="$case_dir/target"
  local link_dir="$case_dir/link"
  mkdir -p "$target_dir"
  ln -s "$target_dir" "$link_dir"

  run_expect_failure "$case_dir/out.log" env \
    MNEMONAS_E2E_ROOT="$link_dir" \
    bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT must not contain symlink path components"
  assert_not_exists "$target_dir/backend"
}

run_refuse_invalid_isolated_addr_test() {
  local case_dir="$TMP_ROOT/refuse-invalid-isolated-addr"
  mkdir -p "$case_dir"

  run_expect_failure "$case_dir/out.log" env \
    MNEMONAS_E2E_ROOT="$case_dir/root" \
    MNEMONAS_E2E_DATAPLANE_HTTP=$'127.0.0.1:19191\n-XPOST' \
    bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_DATAPLANE_HTTP cannot contain whitespace"
  assert_not_exists "$case_dir/root/backend"
}

run_refuse_invalid_isolated_ready_attempts_test() {
  local case_dir="$TMP_ROOT/refuse-invalid-isolated-ready-attempts"
  mkdir -p "$case_dir"

  run_expect_failure "$case_dir/out.log" env \
    MNEMONAS_E2E_ROOT="$case_dir/root" \
    MNEMONAS_E2E_READY_ATTEMPTS=0 \
    bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_READY_ATTEMPTS must be a positive integer"
  assert_not_exists "$case_dir/root/backend"
}

run_refuse_invalid_playwright_backend_addr_test() {
  local case_dir="$TMP_ROOT/refuse-invalid-playwright-backend-addr"
  mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$case_dir/root" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_DATAPLANE_GRPC port must be between 1 and 65535"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_symlink_playwright_backend_root_test() {
	local case_dir="$TMP_ROOT/refuse-symlink-playwright-root"
	local target_dir="$case_dir/target"
	local link_dir="$case_dir/link"
	mkdir -p "$target_dir"
	ln -s "$target_dir" "$link_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$link_dir" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT must not contain symlink path components"
	assert_not_exists "$target_dir/backend"
}

run_refuse_default_personal_storage_test() {
	local case_dir="$TMP_ROOT/refuse-default-storage"
	local fake_home="$case_dir/home"
	mkdir -p "$fake_home"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$fake_home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$fake_home/.mnemonas" \
		CONFIG_FILE="$fake_home/.mnemonas/config.toml" \
		SECRETS_FILE="$fake_home/.mnemonas/secrets.json" \
		INITIAL_PASSWORD_FILE="$fake_home/.mnemonas/.mnemonas/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "refusing to run E2E tests against default personal storage root"
}

run_isolated_target_reaches_health_check_test() {
	local case_dir="$TMP_ROOT/isolated-target"
	mkdir -p "$case_dir/storage"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "MnemoNAS service not running at http://127.0.0.1:9"
}

run_missing_explicit_target_test
run_refuse_unisolated_storage_test
run_refuse_invalid_base_url_test
run_refuse_traversal_storage_test
run_refuse_relative_storage_test
run_refuse_protected_storage_with_override_test
run_refuse_symlink_storage_test
run_refuse_traversal_isolated_root_test
run_refuse_symlink_isolated_root_test
run_refuse_invalid_isolated_addr_test
run_refuse_invalid_isolated_ready_attempts_test
run_refuse_invalid_playwright_backend_addr_test
run_refuse_symlink_playwright_backend_root_test
run_refuse_default_personal_storage_test
run_isolated_target_reaches_health_check_test

printf '[e2e-safety-test] all checks passed\n'
