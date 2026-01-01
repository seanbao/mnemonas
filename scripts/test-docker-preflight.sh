#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

# Keep warning-count assertions independent from the runner's free space.
export MIN_FREE_BYTES=1

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

run_data_dir_control_character_test() {
	local case_dir="$TMP_ROOT/data-control"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local data_dir
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	data_dir="$case_dir/home/data"$'\a'"escape"
	mkdir -p "$data_dir"
	chmod 750 "$data_dir"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		DATA_DIR="$data_dir" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a data directory with a control character"
	assert_file_contains "$out" "Data directory cannot contain control characters"
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

run_sensitive_files_private_test() {
	local case_dir="$TMP_ROOT/sensitive-private"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	mkdir -p "$case_dir/home/.mnemonas/.mnemonas"
	chmod 700 "$case_dir/home/.mnemonas/.mnemonas"
	printf '[]\n' > "$case_dir/home/.mnemonas/.mnemonas/users.json"
	printf 'Password: test-password\n' > "$case_dir/home/.mnemonas/.mnemonas/initial-password.txt"
	printf '{"jwt_secret":"secret","webdav_password":"password"}\n' > "$case_dir/home/.mnemonas/secrets.json"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data"
TOML
	chmod 600 \
		"$case_dir/home/.mnemonas/.mnemonas/users.json" \
		"$case_dir/home/.mnemonas/.mnemonas/initial-password.txt" \
		"$case_dir/home/.mnemonas/secrets.json" \
		"$case_dir/home/.mnemonas/config.toml"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Internal metadata directory is private to its owner"
	assert_file_contains "$out" "Users file is private to its owner"
	assert_file_contains "$out" "Initial admin password file is private to its owner"
	assert_file_contains "$out" "Generated secrets file is private to its owner"
	assert_file_contains "$out" "Docker config file is private to its owner"
	assert_file_contains "$out" "Summary: 0 failure(s), 0 warning(s)"
}

run_sensitive_file_permission_warning_test() {
	local case_dir="$TMP_ROOT/sensitive-permission"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	mkdir -p "$case_dir/home/.mnemonas/.mnemonas"
	chmod 700 "$case_dir/home/.mnemonas/.mnemonas"
	printf 'Password: test-password\n' > "$case_dir/home/.mnemonas/.mnemonas/initial-password.txt"
	chmod 0644 "$case_dir/home/.mnemonas/.mnemonas/initial-password.txt"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Initial admin password file allows group or other access"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_configured_auth_file_permission_warning_test() {
	local case_dir="$TMP_ROOT/configured-auth-permission"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	mkdir -p "$case_dir/home/.mnemonas/custom-auth"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "~/custom-auth/users.json"
TOML
	printf '[]\n' > "$case_dir/home/.mnemonas/custom-auth/users.json"
	printf 'Password: test-password\n' > "$case_dir/home/.mnemonas/custom-auth/initial-password.txt"
	chmod 0600 "$case_dir/home/.mnemonas/config.toml"
	chmod 0600 "$case_dir/home/.mnemonas/custom-auth/users.json"
	chmod 0644 "$case_dir/home/.mnemonas/custom-auth/initial-password.txt"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Configured users file is private to its owner"
	assert_file_contains "$out" "Configured initial admin password file allows group or other access"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_unmapped_configured_auth_file_warning_test() {
	local case_dir="$TMP_ROOT/unmapped-configured-auth"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data"

[auth]
users_file = "/run/mnemonas/users.json"
TOML
	chmod 0600 "$case_dir/home/.mnemonas/config.toml"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Configured auth.users_file is outside the /data mount"
	assert_file_contains "$out" "/run/mnemonas/users.json"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_config_file_permission_warning_test() {
	local case_dir="$TMP_ROOT/config-permission"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data"
TOML
	chmod 0644 "$case_dir/home/.mnemonas/config.toml"

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Docker config file allows group or other access"
	assert_file_contains "$out" "Existing Docker config uses [storage].root = /data"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_sensitive_file_symlink_test() {
	local case_dir="$TMP_ROOT/sensitive-symlink"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	printf '{"jwt_secret":"secret"}\n' > "$case_dir/secrets-real.json"
	ln -s "$case_dir/secrets-real.json" "$case_dir/home/.mnemonas/secrets.json"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a symlink generated secrets file"
	assert_file_contains "$out" "Generated secrets file must not be a symlink"
}

run_config_file_symlink_test() {
	local case_dir="$TMP_ROOT/config-symlink"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/config-real.toml" <<'TOML'
[storage]
root = "/data"
TOML
	ln -s "$case_dir/config-real.toml" "$case_dir/home/.mnemonas/config.toml"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a symlink Docker config file"
	assert_file_contains "$out" "Docker config file must not be a symlink"
	assert_file_not_contains "$out" "Existing Docker config uses [storage].root"
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

run_mnemonas_data_dir_env_override_test() {
	local case_dir="$TMP_ROOT/mnemonas-data-dir-env"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local env_file_data="$case_dir/env-file-data"
	local override_data="$case_dir/override-data"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	mkdir -p "$env_file_data" "$override_data"
	chmod 750 "$env_file_data" "$override_data"
	cat >> "$case_dir/repo/.env" <<ENV
MNEMONAS_DATA_DIR=$env_file_data
ENV

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		MNEMONAS_DATA_DIR="$override_data" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Data dir:   $override_data"
	assert_file_contains "$out" "Data directory exists: $override_data"
	assert_file_not_contains "$out" "Data directory exists: $env_file_data"
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

run_invalid_toml_config_test() {
	local case_dir="$TMP_ROOT/invalid-toml-config"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage
root = "/data"
TOML
	chmod 0600 "$case_dir/home/.mnemonas/config.toml"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted invalid TOML config"
	assert_file_contains "$out" "Docker config file is not valid TOML"
	assert_file_not_contains "$out" "does not set [storage].root"
}

run_relative_storage_root_config_test() {
	local case_dir="$TMP_ROOT/relative-root-config"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "data"
TOML
	chmod 0600 "$case_dir/home/.mnemonas/config.toml"

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a relative Docker storage root"
	assert_file_contains "$out" "sets a relative [storage].root: data"
	assert_file_contains "$out" "root = \"/data\""
}

run_custom_storage_root_warning_test() {
	local case_dir="$TMP_ROOT/custom-root"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
cat > "$case_dir/home/.mnemonas/config.toml" <<'TOML'
[storage]
root = "/data\u0023root" # TOML escapes may encode characters in quoted values
TOML
	chmod 0600 "$case_dir/home/.mnemonas/config.toml"

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
	assert_file_contains "$out" "Release image tag is pinned"
	assert_file_contains "$out" "Summary: 0 failure(s), 0 warning(s)"
}

run_release_image_latest_warning_test() {
	local case_dir="$TMP_ROOT/release-image-latest"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<'ENV'
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas:latest
ENV

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "MNEMONAS_IMAGE uses the moving 'latest' tag"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_release_image_missing_tag_warning_test() {
	local case_dir="$TMP_ROOT/release-image-missing-tag"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<'ENV'
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas
ENV

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "MNEMONAS_IMAGE has no explicit tag or digest"
	assert_file_contains "$out" "Summary: 0 failure(s), 1 warning(s)"
}

run_release_image_digest_test() {
	local case_dir="$TMP_ROOT/release-image-digest"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<'ENV'
MNEMONAS_IMAGE=ghcr.io/seanbao/mnemonas@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
ENV

	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"

	assert_file_contains "$out" "Release image is pinned by digest"
	assert_file_contains "$out" "Summary: 0 failure(s), 0 warning(s)"
}

run_invalid_release_image_test() {
	local name="$1"
	local image_assignment="$2"
	local expected="$3"
	local case_dir="$TMP_ROOT/invalid-release-image-$name"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<ENV
MNEMONAS_IMAGE=$image_assignment
ENV

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted invalid MNEMONAS_IMAGE for $name"
	assert_file_contains "$out" "$expected"
}

run_release_image_url_redaction_test() {
	local case_dir="$TMP_ROOT/release-image-url-redaction"
	local fake_bin="$case_dir/bin"
	local out="$case_dir/out.log"
	local status
	make_case "$case_dir"
	make_fake_bin "$fake_bin"
	cat >> "$case_dir/repo/.env" <<'ENV'
MNEMONAS_IMAGE="https://user:super-secret@example.com/mnemonas:tag?token=also-secret#frag"
ENV

	set +e
	PATH="$fake_bin:$PATH" \
		REPO_ROOT="$case_dir/repo" \
		HOME="$case_dir/home" \
		bash "$PROJECT_ROOT/scripts/mnemonas-docker-preflight.sh" > "$out"
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "preflight accepted a URL-shaped MNEMONAS_IMAGE"
	assert_file_contains "$out" "MNEMONAS_IMAGE must be a Docker image reference, not a URL"
	assert_file_not_contains "$out" "user:super-secret"
	assert_file_not_contains "$out" "super-secret"
	assert_file_not_contains "$out" "token=also-secret"
	assert_file_not_contains "$out" "also-secret"
	assert_file_not_contains "$out" "frag"
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
run_data_dir_control_character_test
run_symlink_data_dir_test
run_sensitive_files_private_test
run_sensitive_file_permission_warning_test
run_configured_auth_file_permission_warning_test
run_unmapped_configured_auth_file_warning_test
run_config_file_permission_warning_test
run_sensitive_file_symlink_test
run_config_file_symlink_test
run_busy_port_test
run_custom_host_port_test
run_mnemonas_data_dir_env_override_test
run_invalid_existing_config_test
run_invalid_toml_config_test
run_relative_storage_root_config_test
run_custom_storage_root_warning_test
run_release_image_without_buildx_test
run_release_image_latest_warning_test
run_release_image_missing_tag_warning_test
run_release_image_digest_test
run_invalid_release_image_test "dash" "-bad" "MNEMONAS_IMAGE must not start with '-'"
run_invalid_release_image_test "whitespace" '"ghcr.io/seanbao/mnemonas:v1 bad"' "MNEMONAS_IMAGE must not contain whitespace or control characters"
run_invalid_release_image_test "invalid-digest" "ghcr.io/seanbao/mnemonas@sha256:not-hex" "MNEMONAS_IMAGE digest must use sha256:<64 hex chars>"
run_invalid_release_image_test "invalid-tag" "ghcr.io/seanbao/mnemonas:-badtag" "MNEMONAS_IMAGE tag is not Docker-compatible"
run_release_image_url_redaction_test
run_invalid_min_free_bytes_test

printf '[docker-preflight-test] all checks passed\n'
