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

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"
	if grep -Fq -- "$unexpected" "$path"; then
		fail "$path contains unsafe text: $unexpected"
	fi
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

make_fake_benchmark_curl() {
	local bin_dir="$1"
	local invoked_log="$2"
	mkdir -p "$bin_dir"
	cat > "$bin_dir/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CURL_INVOKED_LOG"
case " $* " in
  *"/api/v1/auth/login"*)
    printf '{\n  "success": true,\n  "data": {\n    "access_token": "access.pretty",\n    "refresh_token": "refresh.pretty"\n  }\n}\n'
    exit 0
    ;;
  *"/api/v1/metrics"*)
    printf '{"success":true,"data":{"requests":{"total":1,"error_rate":0},"latency":{"avg_ms":1.5,"max_ms":2.5}}}\n'
    exit 0
    ;;
  *"%{http_code}"*|*"PROPFIND"*)
    printf '207'
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

run_refuse_ambiguous_base_url_test() {
	local case_dir="$TMP_ROOT/refuse-ambiguous-base-url"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/storage/files/benchmark-test"
	printf 'keep\n' > "$case_dir/storage/files/benchmark-test/sentinel.txt"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/userinfo.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://user:route-secret@127.0.0.1:9"
	assert_file_contains "$case_dir/userinfo.log" "base URL must not contain embedded credentials"
	assert_file_not_contains "$case_dir/userinfo.log" "route-secret"

	run_expect_failure "$case_dir/query.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9?token=route-secret"
	assert_file_contains "$case_dir/query.log" "base URL must not contain query strings or fragments"
	assert_file_not_contains "$case_dir/query.log" "route-secret"

	run_expect_failure "$case_dir/fragment.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9#route-secret"
	assert_file_contains "$case_dir/fragment.log" "base URL must not contain query strings or fragments"
	assert_file_not_contains "$case_dir/fragment.log" "route-secret"

	run_expect_failure "$case_dir/backslash.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" 'http://127.0.0.1:9\dav'
	assert_file_contains "$case_dir/backslash.log" "base URL must not contain backslashes"

	run_expect_failure "$case_dir/encoded-slash.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9/dav%2Froot"
	assert_file_contains "$case_dir/encoded-slash.log" "base URL must not contain encoded slashes or backslashes"

	run_expect_failure "$case_dir/encoded-dot.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9/%2e%2e/root"
	assert_file_contains "$case_dir/encoded-dot.log" "base URL must not contain dot segments"

	run_expect_failure "$case_dir/empty-host.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http:///dav"
	assert_file_contains "$case_dir/empty-host.log" "base URL must include a host"
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

run_refuse_newline_storage_test() {
	local case_dir="$TMP_ROOT/refuse-newline-storage"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local storage_root
	mkdir -p "$case_dir"
	make_fake_curl "$fake_bin" "$invoked_log"

	storage_root="$case_dir/storage"$'\n'"escape"
	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$storage_root" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT cannot contain newline characters"
	assert_not_exists "$invoked_log"
}

run_refuse_control_character_storage_test() {
	local case_dir="$TMP_ROOT/refuse-control-character-storage"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local storage_root
	mkdir -p "$case_dir"
	make_fake_curl "$fake_bin" "$invoked_log"

	storage_root="$case_dir/storage"$'\a'"escape"
	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$storage_root" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_STORAGE_ROOT cannot contain control characters"
	assert_not_exists "$invoked_log"
	assert_not_exists "$storage_root"
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

run_webdav_secret_json_escape_test() {
	local case_dir="$TMP_ROOT/webdav-secret-json-escape"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local secret='quote"slash\value'
	mkdir -p "$case_dir/storage"
	make_fake_curl "$fake_bin" "$invoked_log"

	cat > "$case_dir/config.toml" <<EOF
[webdav]
auth_type = "basic"
username = "admin"
password = ""
EOF
	printf '{"webdav_password": "quote\\"slash\\\\value"}\n' > "$case_dir/secrets.json"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$invoked_log" "admin:$secret"
}

run_webdav_config_toml_escape_test() {
	local case_dir="$TMP_ROOT/webdav-config-toml-escape"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local secret='quote"slash\value'
	mkdir -p "$case_dir/storage"
	make_fake_curl "$fake_bin" "$invoked_log"

	cat > "$case_dir/config.toml" <<EOF
[webdav]
auth_type = "basic"
username = "admin"
password = "quote\\"slash\\\\value"
EOF

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		SECRETS_FILE="$case_dir/secrets.json" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$invoked_log" "admin:$secret"
}

run_webdav_users_env_credentials_test() {
	local case_dir="$TMP_ROOT/webdav-users-env"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local secret='mnemonas-user-secret'
	mkdir -p "$case_dir/storage"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		MNEMONAS_WEBDAV_AUTH_TYPE=" Users " \
		MNEMONAS_WEBDAV_USERNAME="family-user" \
		MNEMONAS_WEBDAV_PASSWORD="$secret" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9/"

	assert_file_contains "$invoked_log" "family-user:$secret"
	assert_file_contains "$invoked_log" "-u family-user:$secret -sS -o /dev/null -w %{http_code} -X PROPFIND"
	assert_file_contains "$invoked_log" "http://127.0.0.1:9/dav/"
}

run_webdav_users_missing_credentials_test() {
	local case_dir="$TMP_ROOT/webdav-users-missing"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	mkdir -p "$case_dir/storage"
	make_fake_curl "$fake_bin" "$invoked_log"

	run_expect_failure "$case_dir/out.log" env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		MNEMONAS_WEBDAV_AUTH_TYPE="users" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9"

	assert_file_contains "$case_dir/out.log" "WebDAV users auth requires MNEMONAS_WEBDAV_USERNAME and MNEMONAS_WEBDAV_PASSWORD"
	assert_not_exists "$invoked_log"
}

run_admin_login_json_escape_and_pretty_response_test() {
	local case_dir="$TMP_ROOT/admin-login-json"
	local fake_bin="$case_dir/bin"
	local invoked_log="$case_dir/curl.log"
	local secret='quote space"slash\value'
	mkdir -p "$case_dir/storage/.mnemonas"
	make_fake_benchmark_curl "$fake_bin" "$invoked_log"

	cat > "$case_dir/config.toml" <<'EOF'
[auth]
enabled = true
EOF
	printf 'Password: %s\n' "$secret" > "$case_dir/storage/.mnemonas/initial-password.txt"

	env \
		HOME="$case_dir/home" \
		PATH="$fake_bin:$PATH" \
		CURL_INVOKED_LOG="$invoked_log" \
		MNEMONAS_STORAGE_ROOT="$case_dir/storage" \
		CONFIG_FILE="$case_dir/config.toml" \
		INITIAL_PASSWORD_FILE="$case_dir/storage/.mnemonas/initial-password.txt" \
		bash "$REPO_ROOT/scripts/benchmark.sh" "http://127.0.0.1:9" > "$case_dir/out.log" 2>&1

	assert_file_contains "$invoked_log" '{"username":"admin","password":"quote space\"slash\\value"}'
	assert_file_contains "$invoked_log" "Authorization: Bearer access.pretty"
}

run_refuse_traversal_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-traversal-isolated-root"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="/tmp/../var/lib/mnemonas-benchmark" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_ROOT must not contain '..' path segments"
}

run_refuse_newline_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-newline-isolated-root"
	local bench_root
	mkdir -p "$case_dir"

	bench_root="/tmp/mnemonas-benchmark"$'\n'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$bench_root" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_ROOT cannot contain newline characters"
}

run_refuse_control_character_isolated_root_test() {
	local case_dir="$TMP_ROOT/refuse-control-character-isolated-root"
	local bench_root
	mkdir -p "$case_dir"

	bench_root="$case_dir/root"$'\a'"escape"
	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$bench_root" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_ROOT cannot contain control characters"
	assert_not_exists "$bench_root"
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

run_refuse_non_loopback_isolated_host_test() {
	local case_dir="$TMP_ROOT/refuse-non-loopback-isolated-host"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$case_dir/root" \
		MNEMONAS_BENCH_NASD_HOST="0.0.0.0" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_NASD_HOST must be loopback-only"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_loopback_name_spoof_isolated_host_test() {
	local case_dir="$TMP_ROOT/refuse-loopback-name-spoof-isolated-host"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$case_dir/root" \
		MNEMONAS_BENCH_NASD_HOST="127.example.com" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_NASD_HOST must be loopback-only"
	assert_not_exists "$case_dir/root/backend"
}

run_refuse_non_loopback_isolated_dataplane_test() {
	local case_dir="$TMP_ROOT/refuse-non-loopback-isolated-dataplane"
	mkdir -p "$case_dir"

	run_expect_failure "$case_dir/out.log" env \
		MNEMONAS_BENCH_ROOT="$case_dir/root" \
		MNEMONAS_BENCH_DATAPLANE_HTTP="0.0.0.0:19193" \
		MNEMONAS_BENCH_DATAPLANE_GRPC="127.0.0.1:70000" \
		bash "$REPO_ROOT/scripts/run-benchmark-isolated.sh"

	assert_file_contains "$case_dir/out.log" "MNEMONAS_BENCH_DATAPLANE_HTTP must be loopback-only"
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

run_benchmark_docs_avoid_weak_webdav_env_credentials_test() {
	assert_file_not_contains "$REPO_ROOT/docs/development.md" 'MNEMONAS_WEBDAV_USERNAME="webdav"'
	assert_file_not_contains "$REPO_ROOT/docs/development.md" 'MNEMONAS_WEBDAV_PASSWORD="secret"'
	assert_file_not_contains "$REPO_ROOT/docs/development.en.md" 'MNEMONAS_WEBDAV_USERNAME="webdav"'
	assert_file_not_contains "$REPO_ROOT/docs/development.en.md" 'MNEMONAS_WEBDAV_PASSWORD="secret"'
	assert_file_contains "$REPO_ROOT/docs/development.md" 'MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>"'
	assert_file_contains "$REPO_ROOT/docs/development.md" 'MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>"'
	assert_file_contains "$REPO_ROOT/docs/development.en.md" 'MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>"'
	assert_file_contains "$REPO_ROOT/docs/development.en.md" 'MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>"'
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
run_refuse_ambiguous_base_url_test
run_refuse_unisolated_storage_test
run_refuse_traversal_storage_test
run_refuse_newline_storage_test
run_refuse_control_character_storage_test
run_refuse_relative_storage_test
run_refuse_protected_storage_with_override_test
run_refuse_symlink_storage_test
run_webdav_secret_json_escape_test
run_webdav_config_toml_escape_test
run_webdav_users_missing_credentials_test
run_webdav_users_env_credentials_test
run_admin_login_json_escape_and_pretty_response_test
run_refuse_traversal_isolated_root_test
run_refuse_newline_isolated_root_test
run_refuse_control_character_isolated_root_test
run_refuse_symlink_isolated_root_test
run_refuse_invalid_isolated_addr_test
run_refuse_non_loopback_isolated_host_test
run_refuse_loopback_name_spoof_isolated_host_test
run_refuse_non_loopback_isolated_dataplane_test
run_refuse_invalid_isolated_ready_attempts_test
run_refuse_default_personal_storage_test
run_benchmark_docs_avoid_weak_webdav_env_credentials_test
run_isolated_target_reaches_health_check_test

printf '[benchmark-safety-test] all checks passed\n'
