#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[docker-quickstart-test] ERROR: %s\n' "$*" >&2
	exit 1
}

write_executable() {
	local path="$1"
	shift
	printf '%s\n' "$@" > "$path"
	chmod +x "$path"
}

make_repo_case() {
	local repo_dir="$1"
	mkdir -p "$repo_dir/scripts"
	cp "$REPO_ROOT/.env.example" "$repo_dir/.env.example"
	cp "$REPO_ROOT/docker-compose.yml" "$repo_dir/docker-compose.yml"
	write_executable "$repo_dir/scripts/mnemonas-docker-preflight.sh" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "preflight repo=%s env=%s data=%s port=%s\n" "$REPO_ROOT" "$ENV_PATH" "$DATA_DIR" "$HOST_PORT" > "$CAPTURE_DIR/preflight.log"'
}

make_success_curl() {
	local bin_dir="$1"
	write_executable "$bin_dir/curl" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/curl.args"'
}

make_failing_curl() {
	local bin_dir="$1"
	write_executable "$bin_dir/curl" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/curl.args"' \
		'exit 7'
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
		fail "$path unexpectedly contains: $unexpected"
	fi
}

assert_mode() {
	local path="$1"
	local expected="$2"
	local actual
	actual="$(stat -c '%a' "$path")"
	[[ "$actual" == "$expected" ]] || fail "$path mode is $actual, want $expected"
}

run_prepare_test() {
	local case_dir="$TMP_ROOT/prepare"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18080 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$repo_dir/.env" "MNEMONAS_UID=$(id -u)"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_GID=$(id -g)"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_HTTP_PORT=18080"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_DATA_DIR=$data_dir"
	assert_file_contains "$capture_dir/preflight.log" "data=$data_dir port=18080"
	assert_file_contains "$case_dir/out.log" "Web UI:              http://localhost:18080"
	assert_file_contains "$case_dir/out.log" "Initial password:    $data_dir/.mnemonas/initial-password.txt"
	assert_file_contains "$case_dir/out.log" "Read password:       cat $data_dir/.mnemonas/initial-password.txt"
	assert_mode "$data_dir" "750"
}

run_existing_env_test() {
	local case_dir="$TMP_ROOT/existing-env"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data#existing"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"
	cat > "$repo_dir/.env" <<EOF
MNEMONAS_UID=999
MNEMONAS_GID=999
MNEMONAS_HTTP_PORT=19080
MNEMONAS_DATA_DIR="$data_dir" # keep hashes inside quoted values
EXTRA_VALUE=keep-me
EOF

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		DATA_DIR="" \
		HOST_PORT="" \
		MNEMONAS_DATA_DIR="" \
		MNEMONAS_HTTP_PORT="" \
		bash "$quickstart" > "$case_dir/out.log"

	assert_file_contains "$repo_dir/.env" "MNEMONAS_UID=$(id -u)"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_GID=$(id -g)"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_HTTP_PORT=19080"
	assert_file_contains "$repo_dir/.env" "MNEMONAS_DATA_DIR=\"$data_dir\""
	assert_file_contains "$repo_dir/.env" "EXTRA_VALUE=keep-me"
	assert_file_contains "$capture_dir/preflight.log" "data=$data_dir port=19080"
	assert_file_contains "$case_dir/out.log" "Web UI:              http://localhost:19080"
	assert_file_contains "$case_dir/out.log" "Status:              docker compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env ps"
	assert_mode "$data_dir" "750"
}

run_next_steps_quote_paths_test() {
	local case_dir="$TMP_ROOT/next steps quote"
	local repo_dir="$case_dir/repo path"
	local data_dir="$case_dir/data path"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local quoted_repo
	local quoted_env
	local quoted_password
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"
	quoted_repo="$(printf '%q' "$repo_dir/docker-compose.yml")"
	quoted_env="$(printf '%q' "$repo_dir/.env")"
	quoted_password="$(printf '%q' "$data_dir/.mnemonas/initial-password.txt")"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18085 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$case_dir/out.log" "Read password:       cat $quoted_password"
	assert_file_contains "$case_dir/out.log" "Status:              docker compose -f $quoted_repo --env-file $quoted_env ps"
	assert_file_contains "$case_dir/out.log" "Logs:                docker compose -f $quoted_repo --env-file $quoted_env logs -f"
}

run_custom_initial_password_path_test() {
	local case_dir="$TMP_ROOT/custom-initial-password"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$data_dir"
	make_repo_case "$repo_dir"
	cat > "$data_dir/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "/data/custom-auth/users.json"
TOML

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18087 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$case_dir/out.log" "Initial password:    $data_dir/custom-auth/initial-password.txt"
	assert_file_contains "$case_dir/out.log" "Read password:       cat $data_dir/custom-auth/initial-password.txt"
}

run_home_initial_password_path_test() {
	local case_dir="$TMP_ROOT/home-initial-password"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$data_dir"
	make_repo_case "$repo_dir"
	cat > "$data_dir/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "~/auth/users.json"
TOML

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18090 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$case_dir/out.log" "Initial password:    $data_dir/auth/initial-password.txt"
	assert_file_contains "$case_dir/out.log" "Read password:       cat $data_dir/auth/initial-password.txt"
}

run_initial_password_parent_segment_test() {
	local case_dir="$TMP_ROOT/initial-password-parent"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir" "$data_dir"
	make_repo_case "$repo_dir"
	cat > "$data_dir/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "/data/../outside/users.json"
TOML

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18092 \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "quickstart accepted auth.users_file with a parent directory segment"
	assert_file_contains "$case_dir/out.log" "auth.users_file cannot contain parent directory segments"
	assert_file_not_contains "$case_dir/out.log" "$data_dir/../outside"
}

run_unmapped_initial_password_path_test() {
	local case_dir="$TMP_ROOT/unmapped-initial-password"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local quoted_repo quoted_env quoted_password
	mkdir -p "$capture_dir" "$data_dir"
	make_repo_case "$repo_dir"
	cat > "$data_dir/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "/run/mnemonas/users.json"
TOML
	quoted_repo="$(printf '%q' "$repo_dir/docker-compose.yml")"
	quoted_env="$(printf '%q' "$repo_dir/.env")"
	quoted_password="$(printf '%q' "/run/mnemonas/initial-password.txt")"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--port 18088 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$case_dir/out.log" "Initial password:    /run/mnemonas/initial-password.txt (container path; not under /data)"
	assert_file_contains "$case_dir/out.log" "Read password:       docker compose -f $quoted_repo --env-file $quoted_env exec mnemonas cat $quoted_password"
}

run_start_test() {
	local case_dir="$TMP_ROOT/start"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	mkdir -p "$data_dir/.mnemonas"
	printf 'Password: test-password\n' > "$data_dir/.mnemonas/initial-password.txt"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_success_curl "$fake_bin"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--no-build \
			--port 18081 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$capture_dir/docker.args" "--no-build"
	assert_file_not_contains "$capture_dir/docker.args" "--build"
	assert_file_contains "$capture_dir/curl.args" "http://127.0.0.1:18081/health"
	assert_file_contains "$case_dir/out.log" "health check passed"
	assert_file_contains "$case_dir/out.log" "initial password file is available: $data_dir/.mnemonas/initial-password.txt"
}

run_start_custom_initial_password_path_test() {
	local case_dir="$TMP_ROOT/start-custom-initial-password"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$fake_bin" "$data_dir/custom-auth"
	make_repo_case "$repo_dir"
	cat > "$data_dir/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "/data/custom-auth/users.json"
TOML
	printf 'Password: test-password\n' > "$data_dir/custom-auth/initial-password.txt"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_success_curl "$fake_bin"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--no-build \
			--port 18089 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$case_dir/out.log" "initial password file is available: $data_dir/custom-auth/initial-password.txt"
}

run_start_release_image_test() {
	local case_dir="$TMP_ROOT/start-release-image"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	cat > "$repo_dir/.env" <<EOF
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:v1.2.3
MNEMONAS_UID=999
MNEMONAS_GID=999
MNEMONAS_HTTP_PORT=18082
MNEMONAS_DATA_DIR=$data_dir
EOF
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_success_curl "$fake_bin"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--skip-preflight > "$case_dir/out.log"

	assert_file_contains "$repo_dir/.env" "MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:v1.2.3"
	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$capture_dir/docker.args" "--pull missing --no-build"
	assert_file_not_contains "$capture_dir/docker.args" "--build"
	assert_file_contains "$capture_dir/curl.args" "http://127.0.0.1:18082/health"
	assert_file_contains "$case_dir/out.log" "initial password file is not present: $data_dir/.mnemonas/initial-password.txt"
}

run_start_release_template_test() {
	local case_dir="$TMP_ROOT/start-release-template"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	awk '
		/^MNEMONAS_IMAGE=/ { print "MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:1.2.3"; next }
		{ print }
	' "$repo_dir/.env.example" > "$repo_dir/.env.example.tmp"
	mv "$repo_dir/.env.example.tmp" "$repo_dir/.env.example"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_success_curl "$fake_bin"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--skip-preflight \
			--port 18083 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$repo_dir/.env" "MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:1.2.3"
	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$capture_dir/docker.args" "--pull missing --no-build"
	assert_file_not_contains "$capture_dir/docker.args" "--build"
	assert_file_contains "$capture_dir/curl.args" "http://127.0.0.1:18083/health"
}

run_start_compose_failure_test() {
	local case_dir="$TMP_ROOT/start-compose-failure"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"' \
		'exit 17'
	make_success_curl "$fake_bin"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--no-build \
			--port 18086 \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "quickstart accepted a failed docker compose start"
	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$case_dir/out.log" "docker compose failed to start MnemoNAS"
	assert_file_contains "$case_dir/out.log" "docker compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env ps"
	assert_file_contains "$case_dir/out.log" "logs --tail 100 mnemonas"
	[[ ! -f "$capture_dir/curl.args" ]] || fail "curl was called after docker compose failed"
}

run_start_health_failure_test() {
	local case_dir="$TMP_ROOT/start-health-failure"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_failing_curl "$fake_bin"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		HEALTH_TIMEOUT_SECONDS=1 \
		bash "$quickstart" \
			--start \
			--no-build \
			--port 18084 \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "quickstart accepted a failed post-start health check"
	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_contains "$capture_dir/curl.args" "http://127.0.0.1:18084/health"
	assert_file_contains "$case_dir/out.log" "health check did not pass within 1s"
	assert_file_contains "$case_dir/out.log" "logs --tail 100 mnemonas"
}

run_start_skip_health_check_test() {
	local case_dir="$TMP_ROOT/start-skip-health"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local fake_bin="$case_dir/bin"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	mkdir -p "$capture_dir" "$fake_bin"
	make_repo_case "$repo_dir"
	write_executable "$fake_bin/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/docker.args"'
	make_failing_curl "$fake_bin"

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		HEALTH_TIMEOUT_SECONDS=invalid \
		bash "$quickstart" \
			--start \
			--no-build \
			--skip-health-check \
			--port 18085 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	[[ ! -f "$capture_dir/curl.args" ]] || fail "curl was called despite --skip-health-check"
	assert_file_contains "$case_dir/out.log" "skipping post-start health check"
}

run_invalid_data_dir_test() {
	local case_dir="$TMP_ROOT/invalid-data-dir"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir relative/path > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "relative data dir was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir must be an absolute path"
}

run_protected_data_dir_test() {
	local case_dir="$TMP_ROOT/protected-data-dir"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir / > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "protected data dir was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir points at a protected system directory"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting protected data dir"
}

run_data_dir_traversal_test() {
	local case_dir="$TMP_ROOT/data-dir-traversal"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir "$case_dir/data/../escape" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "data dir with parent directory segment was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir cannot contain parent directory segments"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting traversal data dir"
}

run_data_dir_newline_test() {
	local case_dir="$TMP_ROOT/data-dir-newline"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local injected_data_dir
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	injected_data_dir="${case_dir}/data"$'\n'"MNEMONAS_HTTP_PORT=1"
	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir "$injected_data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "data dir with newline was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir cannot contain newline characters"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting newline data dir"
}

run_data_dir_control_character_test() {
	local case_dir="$TMP_ROOT/data-dir-control"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local injected_data_dir
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	injected_data_dir="${case_dir}/data"$'\a'"escape"
	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir "$injected_data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "data dir with control character was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir cannot contain control characters"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting control-character data dir"
}

run_data_dir_symlink_test() {
	local case_dir="$TMP_ROOT/data-dir-symlink"
	local repo_dir="$case_dir/repo"
	local target_dir="$case_dir/target-data"
	local link_dir="$case_dir/link-data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir" "$target_dir"
	make_repo_case "$repo_dir"
	ln -s "$target_dir" "$link_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir "$link_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "symlink data dir was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir must not contain symlink path components"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting symlink data dir"
}

run_normalized_protected_data_dir_test() {
	local case_dir="$TMP_ROOT/normalized-protected-data-dir"
	local repo_dir="$case_dir/repo"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--data-dir /tmp// > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "normalized protected data dir was accepted"
	assert_file_contains "$case_dir/out.log" "--data-dir points at a protected system directory"
	[[ ! -f "$repo_dir/.env" ]] || fail "env file was written after rejecting normalized protected data dir"
}

run_protected_env_path_test() {
	local case_dir="$TMP_ROOT/protected-env"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--env /etc/passwd \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "protected env path was accepted"
	assert_file_contains "$case_dir/out.log" "--env points at a protected system path"
	[[ ! -d "$data_dir" ]] || fail "data dir was created after rejecting protected env path"
}

run_env_symlink_test() {
	local case_dir="$TMP_ROOT/env-symlink"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local target_env="$case_dir/target.env"
	local link_env="$case_dir/link.env"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"
	printf 'SENTINEL=keep\n' > "$target_env"
	ln -s "$target_env" "$link_env"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--env "$link_env" \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "symlink env path was accepted"
	assert_file_contains "$case_dir/out.log" "--env must not be a symlink"
	assert_file_contains "$target_env" "SENTINEL=keep"
}

run_env_missing_parent_test() {
	local case_dir="$TMP_ROOT/env-missing-parent"
	local repo_dir="$case_dir/repo"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local quickstart="$REPO_ROOT/scripts/docker-quickstart.sh"
	local status
	mkdir -p "$capture_dir"
	make_repo_case "$repo_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		bash "$quickstart" \
			--env "$case_dir/missing/.env" \
			--data-dir "$data_dir" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "missing env parent was accepted"
	assert_file_contains "$case_dir/out.log" "--env parent directory does not exist"
	[[ ! -d "$data_dir" ]] || fail "data dir was created after rejecting missing env parent"
}

run_compose_data_volume_contract_test() {
	assert_file_contains "$REPO_ROOT/docker-compose.yml" 'image: ${MNEMONAS_IMAGE:-mnemonas:local}'
	assert_file_contains "$REPO_ROOT/docker-compose.yml" 'user: "${MNEMONAS_UID:-1000}:${MNEMONAS_GID:-1000}"'
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "init: true"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" '      - "${MNEMONAS_HTTP_PORT:-8080}:8080"'
	assert_file_not_contains "$REPO_ROOT/docker-compose.yml" "9090:"
	assert_file_not_contains "$REPO_ROOT/docker-compose.yml" "9091:"
	# Long bind syntax preserves host paths that contain ':' without changing the /data target.
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "type: bind"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" 'source: ${MNEMONAS_DATA_DIR:-${HOME}/.mnemonas}'
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "target: /data"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "create_host_path: true"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "restart: unless-stopped"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "healthcheck:"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" 'test: ["CMD", "/app/mnemonas-healthcheck"]'
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "interval: 30s"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "timeout: 10s"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "retries: 3"
	assert_file_contains "$REPO_ROOT/docker-compose.yml" "start_period: 10s"
}

run_prepare_test
run_existing_env_test
run_next_steps_quote_paths_test
run_custom_initial_password_path_test
run_home_initial_password_path_test
run_initial_password_parent_segment_test
run_unmapped_initial_password_path_test
run_start_test
run_start_custom_initial_password_path_test
run_start_release_image_test
run_start_release_template_test
run_start_compose_failure_test
run_start_health_failure_test
run_start_skip_health_check_test
run_invalid_data_dir_test
run_protected_data_dir_test
run_data_dir_traversal_test
run_data_dir_newline_test
run_data_dir_control_character_test
run_data_dir_symlink_test
run_normalized_protected_data_dir_test
run_protected_env_path_test
run_env_symlink_test
run_env_missing_parent_test
run_compose_data_volume_contract_test

printf '[docker-quickstart-test] all checks passed\n'
