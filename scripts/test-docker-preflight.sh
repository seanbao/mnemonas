#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[docker-preflight-test] ERROR: %s\n' "$*" >&2
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

assert_file_not_contains() {
	local path="$1"
	local unexpected="$2"
	if grep -Fq -- "$unexpected" "$path"; then
		fail "$path unexpectedly contains: $unexpected"
	fi
}

make_fake_bin() {
	local bin_dir="$1"
	mkdir -p "$bin_dir"

	write_executable "$bin_dir/docker" \
		'#!/usr/bin/env bash' \
		'set -euo pipefail' \
		'case "${1:-}" in' \
		'  --version)' \
		'    echo "Docker version 28.2.2, build test"' \
		'    exit 0' \
		'    ;;' \
		'  info)' \
		'    exit 0' \
		'    ;;' \
		'  compose)' \
		'    if [[ "${FAKE_DOCKER_NO_COMPOSE:-0}" == "1" ]]; then' \
		'      echo "docker: unknown command: docker compose" >&2' \
		'      exit 1' \
		'    fi' \
		'    if [[ "${2:-}" == "version" ]]; then' \
		'      echo "Docker Compose version v2.39.4"' \
		'      exit 0' \
		'    fi' \
		'    for arg in "$@"; do' \
		'      if [[ "$arg" == "config" ]]; then' \
		'        exit 0' \
		'      fi' \
		'    done' \
		'    ;;' \
		'  buildx)' \
		'    if [[ "${FAKE_DOCKER_NO_BUILDX:-0}" == "1" ]]; then' \
		'      exit 1' \
		'    fi' \
		'    if [[ "${2:-}" == "version" ]]; then' \
		'      echo "github.com/docker/buildx v0.29.1"' \
		'      exit 0' \
		'    fi' \
		'    ;;' \
		'esac' \
		'echo "unexpected docker args: $*" >&2' \
		'exit 7'

	write_executable "$bin_dir/ss" \
		'#!/usr/bin/env bash' \
		'if [[ "${FAKE_SS_PORT_BUSY:-0}" == "1" ]]; then' \
		'  echo "LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:*"' \
		'fi'
}

make_case() {
	local case_dir="$1"
	local repo_dir="$case_dir/repo"
	local home_dir="$case_dir/home"
	local data_dir="$home_dir/.mnemonas"
	mkdir -p "$repo_dir" "$data_dir"
	chmod 750 "$data_dir"

	cat > "$repo_dir/docker-compose.yml" <<'COMPOSE'
services:
  mnemonas:
    image: mnemonas:local
    ports:
      - "${MNEMONAS_HTTP_PORT:-8080}:8080"
COMPOSE
	cat > "$repo_dir/.env" <<ENV
MNEMONAS_UID=$(id -u)
MNEMONAS_GID=$(id -g)
MNEMONAS_HTTP_PORT=8080
ENV
}

run_success_test() {
	local case_dir="$TMP_ROOT/success"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Docker Compose v2 available"
	assert_file_contains "$out" "Docker Compose config renders successfully"
	assert_file_contains "$out" "Summary: 0 failure(s)"
}

run_missing_compose_test() {
	local case_dir="$TMP_ROOT/missing-compose"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		FAKE_DOCKER_NO_COMPOSE=1 \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a missing Compose v2 plugin"
	assert_file_contains "$out" "Docker Compose v2 plugin is missing"
}

run_missing_data_dir_test() {
	local case_dir="$TMP_ROOT/missing-data"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	rm -rf -- "$case_dir/home/.mnemonas"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a missing data directory"
	assert_file_contains "$out" "Data directory missing"
}

run_relative_data_dir_test() {
	local case_dir="$TMP_ROOT/relative-data"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		DATA_DIR="relative/data" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a relative data directory"
	assert_file_contains "$out" "Data directory must be an absolute path"
}

run_protected_data_dir_test() {
	local case_dir="$TMP_ROOT/protected-data"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		DATA_DIR="/" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a protected data directory"
	assert_file_contains "$out" "Data directory points at a protected system directory"
}

run_data_dir_traversal_test() {
	local case_dir="$TMP_ROOT/data-traversal"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		DATA_DIR="$case_dir/home/.mnemonas/../escape" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a data directory with parent segments"
	assert_file_contains "$out" "Data directory cannot contain parent directory segments"
}

run_symlink_data_dir_test() {
	local case_dir="$TMP_ROOT/symlink-data"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local target_dir="$case_dir/target-data"
	local link_dir="$case_dir/home/.mnemonas"
	local status
	make_case "$case_dir"
	rm -rf -- "$link_dir"
	mkdir -p "$target_dir"
	ln -s "$target_dir" "$link_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a symlink data directory"
	assert_file_contains "$out" "Data directory must not contain symlink path components"
}

run_busy_port_test() {
	local case_dir="$TMP_ROOT/busy-port"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		FAKE_SS_PORT_BUSY=1 \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a busy host port"
	assert_file_contains "$out" "Host port 8080 is already listening"
}

run_custom_host_port_test() {
	local case_dir="$TMP_ROOT/custom-port"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	sed -i 's/^MNEMONAS_HTTP_PORT=.*/MNEMONAS_HTTP_PORT=18080/' "$case_dir/repo/.env"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		FAKE_SS_PORT_BUSY=1 \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Host HTTP port configured: 18080"
	assert_file_contains "$out" "Host port 18080 is available"
	assert_file_contains "$out" "Summary: 0 failure(s)"
}

run_invalid_existing_config_test() {
	local case_dir="$TMP_ROOT/invalid-config"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[server]
port = 8080
TOML

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted an existing Docker config without storage.root"
	assert_file_contains "$out" "does not set [storage].root"
}

run_custom_storage_root_warning_test() {
	local case_dir="$TMP_ROOT/custom-root"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data#root" # keep hashes inside quoted values
TOML

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "[storage].root = /data#root"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_release_image_without_buildx_test() {
	local case_dir="$TMP_ROOT/release-image-no-buildx"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<'ENV'
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:v1.2.3
ENV

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		FAKE_DOCKER_NO_BUILDX=1 \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Docker Buildx plugin is not required for release image"
	assert_file_not_contains "$out" "Docker Buildx plugin is missing"
	assert_file_contains "$out" "Summary: 0 failure(s), 0 warning(s)"
}

run_invalid_min_free_bytes_test() {
	local case_dir="$TMP_ROOT/invalid-min-free"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		MIN_FREE_BYTES="not-a-number" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted invalid MIN_FREE_BYTES"
	assert_file_contains "$out" "MIN_FREE_BYTES must be a positive integer"
}

run_success_test
run_missing_compose_test
run_missing_data_dir_test
run_relative_data_dir_test
run_protected_data_dir_test
run_data_dir_traversal_test
run_symlink_data_dir_test
run_busy_port_test
run_custom_host_port_test
run_invalid_existing_config_test
run_custom_storage_root_warning_test
run_release_image_without_buildx_test
run_invalid_min_free_bytes_test

printf '[docker-preflight-test] all checks passed\n'
