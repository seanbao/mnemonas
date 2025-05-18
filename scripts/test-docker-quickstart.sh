#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

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

run_start_test() {
	local case_dir="$TMP_ROOT/start"
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

	CAPTURE_DIR="$capture_dir" \
		REPO_ROOT="$repo_dir" \
		PATH="$fake_bin:$PATH" \
		bash "$quickstart" \
			--start \
			--no-build \
			--port 18081 \
			--data-dir "$data_dir" > "$case_dir/out.log"

	assert_file_contains "$capture_dir/docker.args" "compose -f $repo_dir/docker-compose.yml --env-file $repo_dir/.env up -d"
	assert_file_not_contains "$capture_dir/docker.args" "--build"
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

run_prepare_test
run_existing_env_test
run_start_test
run_invalid_data_dir_test

printf '[docker-quickstart-test] all checks passed\n'
