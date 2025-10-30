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

make_fake_e2e_curl() {
	local bin_dir="$1"
	local invoked_log="$2"
	mkdir -p "$bin_dir"
	cat > "$bin_dir/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CURL_INVOKED_LOG"
header_file=""
body_file=""
previous_arg=""
for arg in "$@"; do
  case "$previous_arg" in
    -D)
      header_file="$arg"
      previous_arg=""
      continue
      ;;
    -o)
      body_file="$arg"
      previous_arg=""
      continue
      ;;
  esac
  case "$arg" in
    -D|-o)
      previous_arg="$arg"
      ;;
    *)
      previous_arg=""
      ;;
  esac
done
case " $* " in
  *"/health"*)
    printf '{"status":"healthy"}\n'
    exit 0
    ;;
  *"/api/v1/version"*)
    printf '{"version":"test"}\n'
    exit 0
    ;;
  *"/api/v1/metrics"*)
    printf '{"success":true,"data":{"requests":{"total":1,"error_rate":0},"latency":{"avg_ms":1,"max_ms":1}}}\n'
    exit 0
    ;;
  *" -X LOCK "*)
    if [[ -n "$header_file" ]]; then
      printf 'Lock-Token: <opaquelocktoken:e2e-safety-token>\r\n' > "$header_file"
    fi
    if [[ -n "$body_file" ]]; then
      printf '<d:lockdiscovery xmlns:d="DAV:"></d:lockdiscovery>\n' > "$body_file"
    fi
    if [[ " $* " == *"%{http_code}"* ]]; then
      printf '200'
    fi
    exit 0
    ;;
  *" -X UNLOCK "*)
    if [[ " $* " == *"%{http_code}"* ]]; then
      printf '204'
    fi
    exit 0
    ;;
  *"%{http_code}"*)
    printf '404'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
EOF
	chmod +x "$bin_dir/curl"
	: > "$invoked_log"
	rm -f -- "$invoked_log"
}

make_fake_rclone() {
	local bin_dir="$1"
	local invoked_log="$2"
	mkdir -p "$bin_dir"
	cat > "$bin_dir/rclone" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  obscure)
    printf 'obscured-password\n'
    exit 0
    ;;
  copyto)
    printf '%s\n' "$*" >> "$RCLONE_INVOKED_LOG"
    if [[ "${2:-}" == :webdav:* && -n "${3:-}" ]]; then
      printf 'rclone webdav smoke\n' > "$3"
    fi
    exit 0
    ;;
  moveto)
    printf '%s\n' "$*" >> "$RCLONE_INVOKED_LOG"
    exit 0
    ;;
  lsf)
    printf '%s\n' "$*" >> "$RCLONE_INVOKED_LOG"
    printf 'rclone-smoke-moved.txt\n'
    exit 0
    ;;
  *)
    printf '%s\n' "$*" >> "$RCLONE_INVOKED_LOG"
    exit 0
    ;;
esac
EOF
	chmod +x "$bin_dir/rclone"
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
	assert_file_contains "$zh_doc" "initial_password_file=\"\${INITIAL_PASSWORD_FILE:-\$HOME/.mnemonas/.mnemonas/initial-password.txt}\""
	assert_file_contains "$en_doc" "initial_password_file=\"\${INITIAL_PASSWORD_FILE:-\$HOME/.mnemonas/.mnemonas/initial-password.txt}\""
	assert_file_contains "$zh_doc" "[ ! -f \"\$initial_password_file\" ] || fail \"Password file not deleted after login\""
	assert_file_contains "$en_doc" "[ ! -f \"\$initial_password_file\" ] || fail \"Password file not deleted after login\""
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$zh_doc" 'Playwright 凭据 helper 会依次尝试 `~/.mnemonas/.mnemonas/initial-password.txt` 和 `~/.mnemonas/initial-password.txt`'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$en_doc" 'Playwright credential helper tries `~/.mnemonas/.mnemonas/initial-password.txt` and then `~/.mnemonas/initial-password.txt`'
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
		count="$(grep -Ec "^## \`\\[$section\\]\`$" "$zh_doc" || true)"
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
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'cert_dir = ""                # Certificate directory; defaults to <storage.root>/.mnemonas/certs.'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'gateway_socket = ""          # Defaults to <storage.root>/.mnemonas/run/smb-gateway.sock.'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'credential_file = ""         # Defaults to <storage.root>/.mnemonas/smb-credentials.json and is separate from Web login passwords.'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'users_file = ""              # User data file path. Defaults to <storage.root>/.mnemonas/users.json.'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'store_file = ""              # Share data file path. Defaults to <storage.root>/.mnemonas/shares.json.'
  assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'store_file = ""              # Favorites data file path. Defaults to <storage.root>/.mnemonas/favorites.json.'
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
	assert_file_contains "$REPO_ROOT/mnemonas.example.toml" 'Leave empty to generate a readable 16-character password in secrets.json on first startup.'
	assert_file_contains "$REPO_ROOT/docs/mounting-guide.md" '<storage.root>/secrets.json'
	assert_file_contains "$REPO_ROOT/docs/mounting-guide.en.md" '<storage.root>/secrets.json'
}

run_configuration_common_scenarios_warn_about_auth_and_env_expansion_test() {
	assert_file_contains "$REPO_ROOT/docs/configuration.md" "只把 \`webdav.auth_type\` 设为 \`none\` 不会关闭 Web UI/API 登录"
	assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "Setting only \`webdav.auth_type = \"none\"\` does not disable Web UI/API login"
	assert_file_contains "$REPO_ROOT/docs/configuration.md" "当前配置文件不会展开环境变量；不要把 \`\${...}\` 写入 TOML 并期待运行时替换。"
	assert_file_contains "$REPO_ROOT/docs/configuration.en.md" "Configuration files do not expand environment variables. Do not write \`\${...}\` in TOML and expect runtime substitution."
}

run_backup_restore_docs_contract_test() {
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" '服务端 POSIX 绝对路径，不能包含控制字符、反斜杠'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" '路径段，不能是文件系统根目录或受保护系统目录'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'absolute server-side POSIX path that starts with'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'must not contain control characters, backslashes'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" '包含反斜杠的恢复目标路径会被'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" 'restore-preview'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" 'restore-verify'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'Restore target paths containing backslashes are rejected as invalid Windows or UNC-style syntax'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'restore-preview'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'restore-verify'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" 'restic 预览和 rclone 预览/保留检查会拒绝输出中的不安全文件路径，包括空路径、控制字符、反斜杠'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'Restic preview and rclone preview or retention listings reject unsafe output file paths, including empty paths, control characters, backslashes'
	assert_file_contains "$REPO_ROOT/docs/backup-guide.md" '预览/保留检查会拒绝输出中的不安全文件路径，包括空路径、控制字符、反斜杠'
	assert_file_contains "$REPO_ROOT/docs/backup-guide.en.md" 'Restic preview and rclone preview or retention listings reject unsafe output file paths, including empty paths, control characters, backslashes'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" 'restore_report_findings'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" '恢复报告下载中的'
	assert_file_contains "$REPO_ROOT/docs/api-reference.md" '同一套备份凭据脱敏规则'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'restore_report_findings'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'downloaded restore-report'
	assert_file_contains "$REPO_ROOT/docs/api-reference.en.md" 'same backup credential redaction rules'
	assert_file_contains "$REPO_ROOT/docs/backup-guide.md" 'API 可见的备份错误、警告和恢复报告 findings 文本'
	assert_file_contains "$REPO_ROOT/docs/backup-guide.md" '<redacted>'
	assert_file_contains "$REPO_ROOT/docs/backup-guide.en.md" 'API-visible backup error, warning, or restore-report findings text'
}

run_web_readmes_avoid_e2e_placeholder_password_test() {
	assert_file_not_contains "$REPO_ROOT/web/README.md" "change-this-test-password"
	assert_file_not_contains "$REPO_ROOT/web/README.en.md" "change-this-test-password"
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.md" 'export E2E_PASSWORD_FILE="$HOME/.mnemonas/.mnemonas/initial-password.txt"'
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.md" '# export E2E_PASSWORD_FILE="$HOME/.mnemonas/initial-password.txt"'
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.en.md" 'export E2E_PASSWORD_FILE="$HOME/.mnemonas/.mnemonas/initial-password.txt"'
	# shellcheck disable=SC2016 # Match the literal README snippet containing $HOME.
	assert_file_contains "$REPO_ROOT/web/README.en.md" '# export E2E_PASSWORD_FILE="$HOME/.mnemonas/initial-password.txt"'
	assert_file_contains "$REPO_ROOT/web/README.md" '# export E2E_PASSWORD="<admin-password>"'
	assert_file_contains "$REPO_ROOT/web/README.en.md" '# export E2E_PASSWORD="<admin-password>"'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/web/README.md" 'Playwright 会依次尝试读取 `~/.mnemonas/.mnemonas/initial-password.txt` 和 `~/.mnemonas/initial-password.txt`'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/web/README.en.md" 'Playwright tries `~/.mnemonas/.mnemonas/initial-password.txt` and then `~/.mnemonas/initial-password.txt`'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/development.md" '未设置 `E2E_PASSWORD_FILE` 时，Playwright 会按此顺序尝试这两个路径'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/development.en.md" 'Without `E2E_PASSWORD_FILE`, Playwright tries those two paths in that order'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/web/README.md" '显式设置 `E2E_PASSWORD_FILE` 时，该文件是权威来源'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/web/README.en.md" 'When `E2E_PASSWORD_FILE` is set explicitly, that file is authoritative'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" '显式设置 `E2E_PASSWORD_FILE` 时，该文件是权威来源'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" 'When `E2E_PASSWORD_FILE` is set explicitly, that file is authoritative'
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

run_playwright_auth_helper_fails_closed_test() {
	local helper="$REPO_ROOT/web/e2e/helpers/auth-check.ts"
	local setup="$REPO_ROOT/web/e2e/auth.setup.ts"
	local config="$REPO_ROOT/web/playwright.config.ts"

	assert_file_not_contains "$helper" "await waitForAuthSurface(page).catch(() => {})"
	assert_file_not_contains "$helper" "page.url().includes('/login') ? 'login' : 'app'"
	assert_file_contains "$helper" "throw error"
	assert_file_contains "$helper" "Set MNEMONAS_E2E_ALLOW_AUTH_SKIP=1 only when intentionally reusing an environment where protected-page checks may be skipped."
	assert_file_not_contains "$setup" "defaults to changeme"
	assert_file_contains "$setup" "E2E_PASSWORD_FILE: initial-password file used when E2E_PASSWORD is unset."
	assert_file_contains "$config" "const ALLOW_AUTH_SKIP = process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP ?? (REUSE_EXISTING_SERVER ? '1' : '0')"
	assert_file_contains "$config" "if (!REUSE_EXISTING_SERVER) {"
	assert_file_contains "$config" "process.env.E2E_PASSWORD_FILE ||= path.join(E2E_ROOT, 'backend', 'e2e-password.txt')"
	assert_file_contains "$config" "process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP = ALLOW_AUTH_SKIP"
	assert_file_not_contains "$config" "const ALLOW_AUTH_SKIP = process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP ?? '1'"
	assert_file_not_contains "$config" "process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP ||= '1'"
	assert_file_contains "$config" "Reused environments leave E2E_PASSWORD_FILE unset unless provided by the caller."
}

run_webdav_users_env_credentials_test() {
	local case_dir="$TMP_ROOT/webdav-users-env"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local secret="mnemonas-user-secret"
	mkdir -p "$case_dir/storage"
	make_fake_e2e_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		BASE_URL="http://127.0.0.1:18080" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		MNEMONAS_WEBDAV_AUTH_TYPE=" Users " \
		MNEMONAS_WEBDAV_USERNAME="family-user" \
		MNEMONAS_WEBDAV_PASSWORD="$secret" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "Using WebDAV users auth credentials for user: family-user"
	assert_file_contains "$invoked_log" "family-user:$secret"
	assert_file_contains "$invoked_log" "-u family-user:$secret -sS -X LOCK http://127.0.0.1:18080/dav/e2e-test/test.txt"
	assert_file_contains "$invoked_log" "-u family-user:$secret -sS -X UNLOCK http://127.0.0.1:18080/dav/e2e-test/test.txt"
}

run_webdav_users_missing_credentials_test() {
	local case_dir="$TMP_ROOT/webdav-users-missing"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/storage"
	make_fake_e2e_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		BASE_URL="http://127.0.0.1:18080" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		MNEMONAS_WEBDAV_AUTH_TYPE="users" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "WebDAV users auth requires MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD"
	assert_not_exists "$invoked_log"
}

run_rclone_webdav_docs_contract_test() {
	assert_file_contains "$REPO_ROOT/scripts/e2e-test.sh" "RUN_RCLONE_WEBDAV=1"
	assert_file_contains "$REPO_ROOT/docs/development.md" 'RUN_RCLONE_WEBDAV=1'
	assert_file_contains "$REPO_ROOT/docs/development.en.md" 'RUN_RCLONE_WEBDAV=1'
	assert_file_contains "$REPO_ROOT/docs/development.md" 'RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick'
	assert_file_contains "$REPO_ROOT/docs/development.en.md" 'RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick'
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" 'RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick'
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" 'RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick'
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" 'WebDAV client smoke'
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" 'WebDAV client smoke'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.md" '可选 `RUN_RCLONE_WEBDAV=1` E2E'
	# shellcheck disable=SC2016 # Match literal Markdown code spans.
	assert_file_contains "$REPO_ROOT/docs/webdav-compatibility.en.md" 'Optional `RUN_RCLONE_WEBDAV=1` E2E coverage'
}

run_webdav_lock_smoke_contract_test() {
	assert_file_contains "$REPO_ROOT/scripts/e2e-test.sh" "test_lock_unlock()"
	assert_file_contains "$REPO_ROOT/scripts/e2e-test.sh" "WebDAV LOCK/UNLOCK round trip successful"
	assert_file_contains "$REPO_ROOT/scripts/e2e-test.sh" "Lock-Token: \$lock_token"
	assert_file_contains "$REPO_ROOT/scripts/e2e-test.sh" "grep -Eq '<[^/][^>]*lockdiscovery([[:space:]>])'"
	assert_file_not_contains "$REPO_ROOT/scripts/e2e-test.sh" 'grep -q "<D:lockdiscovery>"'
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.md" "| WebDAV locks | LOCK/UNLOCK 虚拟锁 token 往返 | quick |"
	assert_file_contains "$REPO_ROOT/docs/testing-strategy.en.md" "| WebDAV locks | LOCK/UNLOCK virtual lock-token round trip | quick |"
}

run_etag_returned_fetches_header_once_test() {
	local count

	count="$(awk '
		/^test_etag_returned\(\) \{/ {
			in_function = 1
			next
		}
		in_function && /^}/ {
			in_function = 0
		}
		in_function && /local etag=\$\(curl -sf "\$WEBDAV_URL\/e2e-test\/test\.txt" -I/ {
			count++
		}
		END {
			print count + 0
		}
	' "$REPO_ROOT/scripts/e2e-test.sh")"
	[[ "$count" -eq 1 ]] || fail "test_etag_returned should fetch the ETag header once, found $count"
}

run_auth_password_file_deletion_fails_after_successful_login_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-password-file-deletion-function.txt"
	local token_check_count

	awk '
		/^test_auth_password_file_deleted_after_login\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	# shellcheck disable=SC2016 # Match literal script text.
	token_check_count="$(grep -Fc 'if [[ -n "$ADMIN_ACCESS_TOKEN" ]]; then' "$function_body" || true)"
	[[ "$token_check_count" -eq 2 ]] || fail "auth password deletion check should branch on ADMIN_ACCESS_TOKEN twice, found $token_check_count"
	assert_file_contains "$function_body" 'log_fail "Password file still exists after successful login"'
	assert_file_contains "$function_body" 'log_ok "Password file correctly deleted after login"'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'elif [[ -f "$USERS_FILE" ]]; then'
	assert_file_contains "$function_body" 'log_skip "Password file still exists (login may not have occurred)"'
}

run_auth_token_refresh_uses_existing_refresh_token_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-token-refresh-function.txt"

	awk '
		/^test_auth_token_refresh\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	assert_file_contains "$function_body" "local refresh_token=\"\$ADMIN_REFRESH_TOKEN\""
	assert_file_contains "$function_body" "if [[ -z \"\$refresh_token\" && ! -f \"\$password_file\" && ! -f \"\$USERS_FILE\" ]]; then"
	assert_file_contains "$function_body" 'log_skip "Auth not configured for token refresh test"'
}

run_auth_configured_helper_covers_files_and_config_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-configured-helper-function.txt"

	awk '
		/^auth_appears_configured\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'auth_enabled="$(read_config_value auth enabled)"'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'if [[ "$auth_enabled" == "false" ]]; then'
	assert_file_contains "$function_body" 'return 1'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" '[[ -z "$auth_enabled" || "$auth_enabled" == "true" || -f "$INITIAL_PASSWORD_FILE" || -f "$USERS_FILE" ]]'
}

run_auth_login_failure_does_not_skip_configured_auth_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-login-failure-function.txt"

	awk '
		/^test_auth_login_failure\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	assert_file_contains "$function_body" 'if auth_appears_configured; then'
	assert_file_contains "$function_body" 'log_fail "Auth login endpoint unavailable while auth appears configured"'
	assert_file_contains "$function_body" 'log_skip "Auth endpoint not available (auth may be disabled)"'
}

run_auth_protected_endpoint_does_not_skip_configured_auth_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-protected-endpoint-function.txt"

	awk '
		/^test_auth_protected_endpoint\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	assert_file_contains "$function_body" 'if auth_appears_configured; then'
	assert_file_contains "$function_body" 'log_fail "Protected endpoint allowed unauthenticated access while auth appears configured"'
	assert_file_contains "$function_body" 'log_skip "Auth may be disabled (endpoint returned 200)"'
}

run_admin_api_unauthenticated_success_fails_when_auth_configured_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body
	local function_name
	local failure_message

	while IFS='|' read -r function_name failure_message; do
		function_body="$TMP_ROOT/${function_name}-auth-boundary-function.txt"
		awk -v name="$function_name" '
			$0 == name "() {" {
				in_function = 1
			}
			in_function {
				print
			}
			in_function && /^}/ {
				exit
			}
		' "$script" > "$function_body"

		assert_file_contains "$function_body" 'auth_appears_configured'
		assert_file_contains "$function_body" "$failure_message"
	done <<'EOF'
test_version_history|log_fail "Version history API allowed unauthenticated access while auth appears configured"
test_metrics_api|log_fail "Metrics API allowed unauthenticated access while auth appears configured"
test_scrub_api|log_fail "Scrub API allowed unauthenticated access while auth appears configured"
test_scrub_trigger|log_fail "Scrub trigger API allowed unauthenticated access while auth appears configured"
test_diagnostics_export|log_fail "Diagnostics export allowed unauthenticated access while auth appears configured"
EOF
}

run_auth_users_file_follows_config_when_unset_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body="$TMP_ROOT/auth-paths-function.txt"
	local setup_body="$TMP_ROOT/setup-function.txt"

	awk '
		/^configure_auth_paths\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$function_body"

	awk '
		/^setup\(\) \{/ {
			in_function = 1
		}
		in_function {
			print
		}
		in_function && /^}/ {
			exit
		}
	' "$script" > "$setup_body"

	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$script" 'USERS_FILE_EXPLICIT="${USERS_FILE+x}"'
	assert_file_contains "$script" 'expand_user_path()'
	assert_file_contains "$script" '\~/*)'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'if [[ -n "$USERS_FILE_EXPLICIT" ]]; then'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'users_file="$(read_config_value auth users_file)"'
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'require_no_control_characters "$users_file" "auth.users_file"'
	assert_file_contains "$function_body" "auth.users_file must not contain '..' path segments"
	# shellcheck disable=SC2016 # Match literal script text.
	assert_file_contains "$function_body" 'USERS_FILE="$(expand_user_path "$users_file")"'

	local auth_line
	local webdav_line
	auth_line="$(grep -n 'configure_auth_paths' "$setup_body" | head -n1 | cut -d: -f1)"
	webdav_line="$(grep -n 'configure_webdav_auth' "$setup_body" | head -n1 | cut -d: -f1)"
	[[ -n "$auth_line" && -n "$webdav_line" ]] || fail "setup should call configure_auth_paths and configure_webdav_auth"
	[[ "$auth_line" -lt "$webdav_line" ]] || fail "setup should configure auth paths before WebDAV auth"
}

run_conditional_requests_fail_when_etag_missing_test() {
	local script="$REPO_ROOT/scripts/e2e-test.sh"
	local function_body
	local function_name
	local expected

	while IFS='|' read -r function_name expected; do
		function_body="$TMP_ROOT/${function_name}.txt"
		awk -v name="$function_name" '
			$0 == name "() {" {
				in_function = 1
			}
			in_function {
				print
			}
			in_function && /^}/ {
				exit
			}
		' "$script" > "$function_body"

		# shellcheck disable=SC2016 # Match literal script text.
		assert_file_contains "$function_body" 'if [[ -z "$etag" ]]; then'
		assert_file_contains "$function_body" "$expected"
		assert_file_contains "$function_body" "return"
	done <<'EOF'
test_if_none_match|log_fail "If-None-Match test could not read ETag header"
test_if_match_success|log_fail "If-Match success test could not read ETag header"
EOF
}

run_rclone_webdav_default_skip_test() {
	local case_dir="$TMP_ROOT/rclone-webdav-default-skip"
	local fake_bin="$case_dir/bin"
	local curl_log="$case_dir/curl.log"
	local rclone_log="$case_dir/rclone.log"
	mkdir -p "$case_dir/storage"
	make_fake_e2e_curl "$fake_bin" "$curl_log"
	make_fake_rclone "$fake_bin" "$rclone_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$curl_log" \
		RCLONE_INVOKED_LOG="$rclone_log" \
		BASE_URL="http://127.0.0.1:18080" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		MNEMONAS_WEBDAV_AUTH_TYPE="none" \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "rclone WebDAV smoke disabled; set RUN_RCLONE_WEBDAV=1 to enable"
	assert_not_exists "$rclone_log"
}

run_rclone_webdav_opt_in_uses_webdav_credentials_test() {
	local case_dir="$TMP_ROOT/rclone-webdav-opt-in"
	local fake_bin="$case_dir/bin"
	local curl_log="$case_dir/curl.log"
	local rclone_log="$case_dir/rclone.log"
	local secret="mnemonas-user-secret"
	mkdir -p "$case_dir/storage"
	make_fake_e2e_curl "$fake_bin" "$curl_log"
	make_fake_rclone "$fake_bin" "$rclone_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$curl_log" \
		RCLONE_INVOKED_LOG="$rclone_log" \
		BASE_URL="http://127.0.0.1:18080" \
		STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		INITIAL_PASSWORD_FILE="$case_dir/initial-password.txt" \
		MNEMONAS_WEBDAV_AUTH_TYPE="users" \
		MNEMONAS_WEBDAV_USERNAME="family-user" \
		MNEMONAS_WEBDAV_PASSWORD="$secret" \
		RUN_RCLONE_WEBDAV=1 \
		bash "$REPO_ROOT/scripts/e2e-test.sh" --quick

	assert_file_contains "$case_dir/out.log" "rclone WebDAV smoke succeeded"
	assert_file_contains "$rclone_log" "copyto"
	assert_file_contains "$rclone_log" ":webdav:e2e-test/rclone-smoke.txt"
	assert_file_contains "$rclone_log" ":webdav:e2e-test/rclone-smoke-moved.txt"
	assert_file_contains "$rclone_log" "--webdav-url http://127.0.0.1:18080/dav"
	assert_file_contains "$rclone_log" "--webdav-user family-user"
	assert_file_contains "$rclone_log" "--webdav-pass obscured-password"
	assert_file_not_contains "$rclone_log" "$secret"
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
run_configuration_common_scenarios_warn_about_auth_and_env_expansion_test
run_backup_restore_docs_contract_test
run_web_readmes_avoid_e2e_placeholder_password_test
run_refuse_default_personal_storage_test
run_isolated_target_reaches_health_check_test
run_playwright_auth_helper_fails_closed_test
run_webdav_users_missing_credentials_test
run_webdav_users_env_credentials_test
run_rclone_webdav_docs_contract_test
run_webdav_lock_smoke_contract_test
run_etag_returned_fetches_header_once_test
run_auth_password_file_deletion_fails_after_successful_login_test
run_auth_token_refresh_uses_existing_refresh_token_test
run_auth_configured_helper_covers_files_and_config_test
run_auth_login_failure_does_not_skip_configured_auth_test
run_auth_protected_endpoint_does_not_skip_configured_auth_test
run_admin_api_unauthenticated_success_fails_when_auth_configured_test
run_auth_users_file_follows_config_when_unset_test
run_conditional_requests_fail_when_etag_missing_test
run_rclone_webdav_default_skip_test
run_rclone_webdav_opt_in_uses_webdav_credentials_test

printf '[e2e-safety-test] all checks passed\n'
