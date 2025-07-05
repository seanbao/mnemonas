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

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"
	if grep -Fq -- "$unexpected" "$path"; then
		fail "$path contains unsafe text: $unexpected"
	fi
}

extract_first_toml_block() {
	local path="$1"
	local out="$2"

	awk '
		/^```toml$/ {
			if (!seen) {
				seen = 1
				in_block = 1
				next
			}
		}
		in_block && /^```$/ {
			exit
		}
		in_block {
			print
		}
	' "$path" > "$out"
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
	"$@" </dev/null > "$out" 2>&1
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

run_refuse_newline_storage_test() {
	local case_dir="$TMP_ROOT/refuse-newline-storage"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local storage_root
	mkdir -p "$case_dir"
	make_fake_curl "$fake_bin" "$invoked_log"

	storage_root="/tmp/mnemonas-e2e"$'\n'"escape"
	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$storage_root" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT cannot contain newline characters"
	assert_not_exists "$invoked_log"
}

run_refuse_control_character_storage_test() {
	local case_dir="$TMP_ROOT/refuse-control-character-storage"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local storage_root
	mkdir -p "$case_dir"
	make_fake_curl "$fake_bin" "$invoked_log"

	storage_root="/tmp/mnemonas-e2e"$'\a'"escape"
	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		BASE_URL="http://127.0.0.1:9" \
		STORAGE_ROOT="$storage_root" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "STORAGE_ROOT cannot contain control characters"
	assert_not_exists "$invoked_log"
	assert_not_exists "$storage_root"
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

run_refuse_newline_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-newline-isolated-root"
	local e2e_root
	mkdir -p "$case_dir"

	e2e_root="/tmp/mnemonas-e2e"$'\n'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$e2e_root" \
		MNEMONAS_E2E_DATAPLANE_HTTP=$'127.0.0.1:19191\n-XPOST' \
		bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT cannot contain newline characters"
}

run_refuse_control_character_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-control-character-isolated-root"
	local e2e_root
	mkdir -p "$case_dir"

	e2e_root="$case_dir/root"$'\a'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$e2e_root" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT cannot contain control characters"
	assert_not_exists "$e2e_root"
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

run_refuse_non_loopback_isolated_host_test() {
  local case_dir="$TMP_ROOT/refuse-non-loopback-isolated-host"
  mkdir -p "$case_dir"

  run_expect_failure "$case_dir/out.log" env \
    MNEMONAS_E2E_ROOT="$case_dir/root" \
    MNEMONAS_E2E_NASD_HOST="0.0.0.0" \
    MNEMONAS_E2E_DATAPLANE_HTTP=$'127.0.0.1:19191\n-XPOST' \
    bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_NASD_HOST must be loopback-only"
  assert_not_exists "$case_dir/root/backend"
}

run_refuse_loopback_name_spoof_isolated_host_test() {
	local case_dir="$TMP_ROOT/refuse-loopback-name-spoof-isolated-host"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$case_dir/root" \
		MNEMONAS_E2E_NASD_HOST="127.example.com" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_NASD_HOST must be loopback-only"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_non_loopback_isolated_dataplane_test() {
  local case_dir="$TMP_ROOT/refuse-non-loopback-isolated-dataplane"
  mkdir -p "$case_dir"

  run_expect_failure "$case_dir/out.log" env \
    MNEMONAS_E2E_ROOT="$case_dir/root" \
    MNEMONAS_E2E_DATAPLANE_HTTP="0.0.0.0:19191" \
    MNEMONAS_E2E_DATAPLANE_GRPC=$'127.0.0.1:19190\n-XPOST' \
    bash "$REPO_ROOT/scripts/run-e2e-isolated.sh" --quick

  assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_DATAPLANE_HTTP must be loopback-only"
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

run_refuse_non_loopback_playwright_backend_host_test() {
	local case_dir="$TMP_ROOT/refuse-non-loopback-playwright-host"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$case_dir/root" \
		MNEMONAS_E2E_NASD_HOST="0.0.0.0" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_NASD_HOST must be loopback-only"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_loopback_name_spoof_playwright_backend_host_test() {
	local case_dir="$TMP_ROOT/refuse-loopback-name-spoof-playwright-host"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$case_dir/root" \
		MNEMONAS_E2E_NASD_HOST="127.example.com" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_NASD_HOST must be loopback-only"
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

run_refuse_newline_playwright_backend_root_test() {
	local case_dir="$TMP_ROOT/refuse-newline-playwright-root"
	local e2e_root
	mkdir -p "$case_dir"

	e2e_root="/tmp/mnemonas-playwright"$'\n'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$e2e_root" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT cannot contain newline characters"
}

run_refuse_control_character_playwright_backend_root_test() {
	local case_dir="$TMP_ROOT/refuse-control-character-playwright-root"
	local e2e_root
	mkdir -p "$case_dir"

	e2e_root="$case_dir/root"$'\a'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_E2E_ROOT="$e2e_root" \
		MNEMONAS_E2E_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_E2E_ROOT cannot contain control characters"
	assert_not_exists "$e2e_root"
}

run_playwright_backend_uses_long_access_ttl_test() {
	local backend_script="$REPO_ROOT/web/scripts/start-e2e-backend.sh"

	assert_file_contains "$backend_script" 'access_token_ttl = "2h"'
	assert_file_contains "$backend_script" 'refresh_token_ttl = "168h"'
}

run_playwright_defaults_use_low_collision_ports_test() {
	assert_file_contains "$REPO_ROOT/web/playwright.config.ts" "http://127.0.0.1:18180"
	assert_file_contains "$REPO_ROOT/web/playwright.config.ts" "http://127.0.0.1:14173"
	assert_file_contains "$REPO_ROOT/web/scripts/start-e2e-backend.sh" "NASD_PORT=\"\${MNEMONAS_E2E_NASD_PORT:-18180}\""
	assert_file_contains "$REPO_ROOT/web/scripts/start-e2e-backend.sh" "DATAPLANE_GRPC=\"\${MNEMONAS_E2E_DATAPLANE_GRPC:-127.0.0.1:19190}\""
	assert_file_contains "$REPO_ROOT/web/scripts/start-e2e-backend.sh" "DATAPLANE_HTTP=\"\${MNEMONAS_E2E_DATAPLANE_HTTP:-127.0.0.1:19191}\""
	assert_file_contains "$REPO_ROOT/scripts/run-e2e-isolated.sh" "NASD_PORT=\"\${MNEMONAS_E2E_NASD_PORT:-18180}\""
	assert_file_contains "$REPO_ROOT/web/README.md" "默认隔离端口为后端 \`18180\`、前端 \`14173\`"
	assert_file_contains "$REPO_ROOT/web/README.en.md" "The default isolated ports are backend \`18180\` and frontend \`14173\`"
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" "默认后端端口为 \`18180\`，默认前端端口为 \`14173\`"
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" "default backend port is \`18180\` and default frontend port is \`14173\`"
}

run_testing_strategy_uses_json_safe_login_payload_test() {
	local zh_doc="$REPO_ROOT/docs/testing-strategy.md"
	local en_doc="$REPO_ROOT/docs/testing-strategy.en.md"
	# shellcheck disable=SC2016 # Match the literal unsafe shell snippet from docs.
	local unsafe_payload='-d "{\"username\":\"admin\",\"password\":\"$password\"}"'

	if grep -Fq -- "$unsafe_payload" "$zh_doc" "$en_doc"; then
		fail "testing strategy docs still interpolate passwords into JSON login payloads"
	fi

	assert_file_contains "$zh_doc" 'json.dumps({"username": "admin", "password": os.environ["PASSWORD"]})'
	assert_file_contains "$en_doc" 'json.dumps({"username": "admin", "password": os.environ["PASSWORD"]})'
	assert_file_contains "$zh_doc" 'test_fresh_install_auth_enabled()'
	assert_file_contains "$en_doc" 'test_fresh_install_auth_enabled()'
	assert_file_contains "$zh_doc" '[ ! -f ~/.mnemonas/.mnemonas/initial-password.txt ] || fail "Password file not deleted after login"'
	assert_file_contains "$en_doc" '[ ! -f ~/.mnemonas/.mnemonas/initial-password.txt ] || fail "Password file not deleted after login"'
}

run_testing_strategy_uses_portable_toml_snippet_test() {
	local zh_doc="$REPO_ROOT/docs/testing-strategy.md"
	local en_doc="$REPO_ROOT/docs/testing-strategy.en.md"

	if grep -Eq "echo ['\"][^'\"]*\\\\n[^'\"]*['\"][[:space:]]*>.*config\\.toml" "$zh_doc" "$en_doc"; then
		fail "testing strategy docs still use echo with literal \\n to write TOML examples"
	fi

	assert_file_contains "$zh_doc" "cat > ~/.mnemonas/config.toml <<'TOML'"
	assert_file_contains "$en_doc" "cat > ~/.mnemonas/config.toml <<'TOML'"
}

run_configuration_complete_example_keeps_optional_arrays_commented_test() {
	local doc
	local block

	for doc in "$REPO_ROOT/docs/configuration.md" "$REPO_ROOT/docs/configuration.en.md"; do
		block="$TMP_ROOT/$(basename "$doc").complete-example.toml"
		extract_first_toml_block "$doc" "$block"

		if grep -Eq '^\[\[share\.policy_rules\]\]' "$block"; then
			fail "$doc complete example enables optional share policy rules"
		fi
		if grep -Eq '^\[\[disk_health\.devices\]\]' "$block"; then
			fail "$doc complete example enables optional disk-health devices"
		fi

		assert_file_contains "$block" '# [[share.policy_rules]]'
		assert_file_contains "$block" '# [[disk_health.devices]]'
	done
}

run_configuration_docs_have_single_section_headings_test() {
  local zh_doc="$REPO_ROOT/docs/configuration.md"
  local en_doc="$REPO_ROOT/docs/configuration.en.md"
  local section count
	local -a sections=(
		server
		storage
		dataplane
		webdav
		smb
		auth
		share
		security
		favorites
		alerts
		disk_health
		maintenance.scrub
		log
	)

	for section in "${sections[@]}"; do
		count="$(grep -Ec "^### \\[$section\\]" "$zh_doc" || true)"
		[[ "$count" -eq 1 ]] || fail "$zh_doc should contain one heading for [$section], found $count"

		count="$(grep -Ec "^## \`\\[$section\\]\`$" "$en_doc" || true)"
		[[ "$count" -eq 1 ]] || fail "$en_doc should contain one heading for [$section], found $count"
  done
}

run_configuration_default_paths_follow_storage_root_test() {
  local file
  local -a checked_files=(
    "$REPO_ROOT/docs/configuration.md"
    "$REPO_ROOT/docs/configuration.en.md"
    "$REPO_ROOT/docs/api-reference.md"
    "$REPO_ROOT/docs/api-reference.en.md"
    "$REPO_ROOT/mnemonas.example.toml"
  )

  for file in "${checked_files[@]}"; do
    if grep -Eq '[~]/.mnemonas/.mnemonas/(certs|users\.json|shares\.json|favorites\.json|run/smb-gateway\.sock|smb-credentials\.json)' "$file"; then
      fail "$file still documents storage-root-relative defaults as fixed ~/.mnemonas/.mnemonas paths"
    fi
  done

  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`cert_dir\` | string | \`<storage.root>/.mnemonas/certs\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`gateway_socket\` | string | \`<storage.root>/.mnemonas/run/smb-gateway.sock\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`credential_file\` | string | \`<storage.root>/.mnemonas/smb-credentials.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`users_file\` | string | \`<storage.root>/.mnemonas/users.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`store_file\` | string | \`<storage.root>/.mnemonas/shares.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.md" "| \`store_file\` | string | \`<storage.root>/.mnemonas/favorites.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`cert_dir\` | string | \`<storage.root>/.mnemonas/certs\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`gateway_socket\` | string | \`<storage.root>/.mnemonas/run/smb-gateway.sock\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`credential_file\` | string | \`<storage.root>/.mnemonas/smb-credentials.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`users_file\` | string | \`<storage.root>/.mnemonas/users.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`store_file\` | string | \`<storage.root>/.mnemonas/shares.json\` |"
  assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "| \`store_file\` | string | \`<storage.root>/.mnemonas/favorites.json\` |"
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'cert_dir = ""                # 证书存放目录（默认 <storage.root>/.mnemonas/certs）'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'gateway_socket = ""          # 默认 <storage.root>/.mnemonas/run/smb-gateway.sock'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'credential_file = ""         # 默认 <storage.root>/.mnemonas/smb-credentials.json，独立于 Web 登录密码'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'users_file = ""              # 用户数据文件路径（默认 <storage.root>/.mnemonas/users.json）'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'store_file = ""              # 分享数据文件路径（默认 <storage.root>/.mnemonas/shares.json）'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'store_file = ""              # 收藏数据文件路径（默认 <storage.root>/.mnemonas/favorites.json）'
  assert_file_contains "$REPO_ROOT/docs/api-reference.md" '"cert_dir": "/srv/mnemonas/.mnemonas/certs"'
  assert_file_contains "$REPO_ROOT/docs/api-reference.md" '"root": "/srv/mnemonas"'
}

run_docs_use_storage_root_config_placeholder_test() {
	local file
	local -a checked_files=(
		"$REPO_ROOT/docs/security.md"
		"$REPO_ROOT/docs/security.en.md"
		"$REPO_ROOT/docs/configuration.md"
		"$REPO_ROOT/docs/configuration.en.md"
		"$REPO_ROOT/docs/mounting-guide.md"
		"$REPO_ROOT/docs/mounting-guide.en.md"
		"$REPO_ROOT/docs/api-reference.md"
		"$REPO_ROOT/docs/api-reference.en.md"
	)

	for file in "${checked_files[@]}"; do
		if grep -Fq '<storage_root>' "$file"; then
			fail "$file uses <storage_root>; documentation should use the config key placeholder <storage.root>"
		fi
	done

	assert_file_contains "$REPO_ROOT/docs/security.md" '<storage.root>/secrets.json'
	assert_file_contains "$REPO_ROOT/docs/security.en.md" '<storage.root>/secrets.json'
	assert_file_contains "$REPO_ROOT/docs/security.md" '16 字符可读随机密码，至少包含小写字母、大写字母和数字'
	assert_file_contains "$REPO_ROOT/docs/security.en.md" '16-character human-readable WebDAV password'
	assert_file_contains "$REPO_ROOT/docs/security.en.md" 'includes lowercase letters, uppercase letters, and digits'
	assert_file_contains "$REPO_ROOT/docs/configuration.md" '首次启动会自动生成 16 字符可读密码，至少包含小写字母、大写字母和数字'
	assert_file_contains "$REPO_ROOT/docs/configuration.en.md" 'The generated password is a 16-character human-readable value with lowercase letters, uppercase letters, and digits'
	assert_file_contains "$REPO_ROOT/mnemonas.example.toml" '留空则首次启动时生成 16 字符可读密码并写入 secrets.json'
	assert_file_contains "$REPO_ROOT/docs/mounting-guide.md" '<storage.root>/secrets.json'
	assert_file_contains "$REPO_ROOT/docs/mounting-guide.en.md" '<storage.root>/secrets.json'
}

run_web_readmes_avoid_e2e_placeholder_password_test() {
	assert_file_not_contains "$REPO_ROOT/web/README.md" "change-this-test-password"
	assert_file_not_contains "$REPO_ROOT/web/README.en.md" "change-this-test-password"
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.md" 'export E2E_PASSWORD_FILE="$HOME/.mnemonas/.mnemonas/initial-password.txt"'
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.en.md" 'export E2E_PASSWORD_FILE="$HOME/.mnemonas/.mnemonas/initial-password.txt"'
	assert_file_contains "$REPO_ROOT/web/README.md" '# export E2E_PASSWORD="<admin-password>"'
	assert_file_contains "$REPO_ROOT/web/README.en.md" '# export E2E_PASSWORD="<admin-password>"'
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
run_refuse_newline_storage_test
run_refuse_control_character_storage_test
run_refuse_relative_storage_test
run_refuse_protected_storage_with_override_test
run_refuse_symlink_storage_test
run_refuse_traversal_isolated_root_test
run_refuse_newline_isolated_root_test
run_refuse_control_character_isolated_root_test
run_refuse_symlink_isolated_root_test
run_refuse_invalid_isolated_addr_test
run_refuse_non_loopback_isolated_host_test
run_refuse_loopback_name_spoof_isolated_host_test
run_refuse_non_loopback_isolated_dataplane_test
run_refuse_invalid_isolated_ready_attempts_test
run_refuse_invalid_playwright_backend_addr_test
run_refuse_non_loopback_playwright_backend_host_test
run_refuse_loopback_name_spoof_playwright_backend_host_test
run_refuse_symlink_playwright_backend_root_test
run_refuse_newline_playwright_backend_root_test
run_refuse_control_character_playwright_backend_root_test
run_playwright_defaults_use_low_collision_ports_test
run_playwright_backend_uses_long_access_ttl_test
run_testing_strategy_uses_json_safe_login_payload_test
run_testing_strategy_uses_portable_toml_snippet_test
run_configuration_complete_example_keeps_optional_arrays_commented_test
run_configuration_docs_have_single_section_headings_test
run_configuration_default_paths_follow_storage_root_test
run_docs_use_storage_root_config_placeholder_test
run_web_readmes_avoid_e2e_placeholder_password_test
run_refuse_default_personal_storage_test
run_isolated_target_reaches_health_check_test

printf '[e2e-safety-test] all checks passed\n'
