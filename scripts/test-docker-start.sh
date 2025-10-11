#!/usr/bin/env bash
# shellcheck disable=SC2016,SC2088

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[docker-start-test] ERROR: %s\n' "$*" >&2
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

assert_mode() {
	local path="$1"
	local expected="$2"
	local actual
	actual="$(stat -c '%a' "$path")"
	[[ "$actual" == "$expected" ]] || fail "$path mode is $actual, want $expected"
}

make_fake_app() {
	local dir="$1"
	mkdir -p "$dir"
	write_executable "$dir/dataplane" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/dataplane.args"' \
		'trap "exit 0" TERM INT' \
		'while true; do sleep 1; done'
	write_executable "$dir/nasd" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_DIR/nasd.args"' \
		'exit 0'
	cp "$REPO_ROOT/mnemonas.example.toml" "$dir/mnemonas.example.toml"
}

run_default_config_test() {
	local case_dir="$TMP_ROOT/default-config"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"

	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log"

	assert_file_contains "$data_dir/config.toml" "root = \"$data_dir\""
	assert_file_contains "$capture_dir/dataplane.args" "--data-dir $data_dir/.mnemonas/objects"
	assert_file_contains "$capture_dir/dataplane.args" "--min-chunk-size 262144"
	assert_file_contains "$capture_dir/dataplane.args" "--avg-chunk-size 1048576"
	assert_file_contains "$capture_dir/dataplane.args" "--max-chunk-size 4194304"
	assert_file_contains "$capture_dir/nasd.args" "--config $data_dir/config.toml"
	assert_mode "$data_dir" "750"
	assert_mode "$data_dir/files" "750"
	assert_mode "$data_dir/.mnemonas" "700"
	assert_mode "$data_dir/.mnemonas/objects" "700"
}

run_mismatched_storage_root_warning_test() {
	local case_dir="$TMP_ROOT/mismatched-root"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local configured_dir="$data_dir/nested#quoted"
	local capture_dir="$case_dir/capture"
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[ storage ] # storage root may have comments in hand-edited TOML
root = '$configured_dir' # keep hashes inside quoted values

[ dataplane ] # dataplane endpoint
grpc_address = '127.0.0.1:9090'
EOF

	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1

	assert_file_contains "$case_dir/start.log" "[WARN] Configured [storage].root is $configured_dir, but Docker STORAGE_ROOT is $data_dir"
	assert_file_contains "$capture_dir/dataplane.args" "--data-dir $configured_dir/.mnemonas/objects"
	assert_file_contains "$capture_dir/nasd.args" "--config $data_dir/config.toml"
	assert_mode "$configured_dir" "750"
	assert_mode "$configured_dir/files" "750"
	assert_mode "$configured_dir/.mnemonas" "700"
	assert_mode "$configured_dir/.mnemonas/objects" "700"
}

run_toml_escaped_storage_root_test() {
	local case_dir="$TMP_ROOT/toml-escaped-root"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local configured_dir="$data_dir/space value"
	local capture_dir="$case_dir/capture"
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir/space\u0020value"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1

	assert_file_contains "$case_dir/start.log" "[WARN] Configured [storage].root is $configured_dir, but Docker STORAGE_ROOT is $data_dir"
	assert_file_contains "$capture_dir/dataplane.args" "--data-dir $configured_dir/.mnemonas/objects"
	assert_mode "$configured_dir" "750"
	assert_mode "$configured_dir/files" "750"
	assert_mode "$configured_dir/.mnemonas" "700"
	assert_mode "$configured_dir/.mnemonas/objects" "700"
}

run_missing_storage_root_test() {
	local case_dir="$TMP_ROOT/missing-root"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<'EOF'
[server]
host = "0.0.0.0"
port = 8080

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted a config without storage.root"
	assert_file_contains "$case_dir/start.log" 'does not set [storage].root'
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite missing storage.root"
}

run_protected_storage_root_test() {
	local case_dir="$TMP_ROOT/protected-root"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<'EOF'
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "/"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted protected storage.root"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare protected storage.root"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite protected storage.root"
}

run_normalized_protected_storage_root_test() {
	local case_dir="$TMP_ROOT/normalized-protected-root"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<'EOF'
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "/etc//"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted normalized protected storage.root"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare protected storage.root"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite normalized protected storage.root"
}

run_storage_root_traversal_test() {
	local case_dir="$TMP_ROOT/storage-root-traversal"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir/../escape"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted storage.root with parent directory segment"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare storage.root with parent directory segments"
	[[ ! -d "$case_dir/escape" ]] || fail "traversal storage root was created"
}

run_storage_root_newline_test() {
	local case_dir="$TMP_ROOT/storage-root-newline"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local injected_root
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	injected_root="$data_dir"$'\n'"root = \"/tmp\""

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$injected_root" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted storage.root with newline"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare storage.root with newline characters"
	[[ ! -f "$data_dir/config.toml" ]] || fail "config file was written after rejecting newline storage root"
}

run_storage_root_control_character_test() {
	local case_dir="$TMP_ROOT/storage-root-control"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local injected_root
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	injected_root="$data_dir"$'\a'"root"

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$injected_root" \
		CONFIG_PATH="$injected_root/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted storage.root with control character"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare storage.root with control characters"
	[[ ! -f "$injected_root/config.toml" ]] || fail "config file was written after rejecting control-character storage root"
}

run_storage_root_symlink_test() {
	local case_dir="$TMP_ROOT/storage-root-symlink"
	local app_dir="$case_dir/app"
	local target_dir="$case_dir/target-data"
	local link_dir="$case_dir/link-data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$target_dir" "$capture_dir"
	ln -s "$target_dir" "$link_dir"
	make_fake_app "$app_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$link_dir" \
		CONFIG_PATH="$link_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted a symlink STORAGE_ROOT"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare storage.root with symlink path component"
	[[ ! -f "$target_dir/config.toml" ]] || fail "config file was written through a symlink storage root"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite symlink storage root"
}

run_managed_subdir_symlink_test() {
	local case_dir="$TMP_ROOT/managed-subdir-symlink"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local outside_internal="$case_dir/outside-internal"
	local outside_files="$case_dir/outside-files"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$outside_internal" "$outside_files" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	ln -s "$outside_internal" "$data_dir/.mnemonas"
	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/internal.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted a symlink internal metadata root"
	assert_file_contains "$case_dir/internal.log" "Refusing to prepare DATAPLANE_DATA_DIR with symlink path component"
	[[ ! -d "$outside_internal/objects" ]] || fail "objects directory was created through a symlink internal metadata root"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite symlink internal metadata root"

	rm -f "$data_dir/.mnemonas"
	ln -s "$outside_files" "$data_dir/files"
	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/files.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted a symlink files directory"
	assert_file_contains "$case_dir/files.log" "Refusing to prepare storage files directory with symlink path component"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite symlink files directory"
}

run_config_path_scope_test() {
  local case_dir="$TMP_ROOT/config-path-scope"
  local app_dir="$case_dir/app"
  local data_dir="$case_dir/data"
	local outside_dir="$case_dir/outside"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$outside_dir" "$capture_dir"
	make_fake_app "$app_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$outside_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted CONFIG_PATH outside STORAGE_ROOT"
	assert_file_contains "$case_dir/start.log" "CONFIG_PATH must stay under STORAGE_ROOT in Docker"
	[[ ! -f "$outside_dir/config.toml" ]] || fail "config file was written outside STORAGE_ROOT"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite out-of-scope CONFIG_PATH"
}

run_config_path_directory_test() {
	local case_dir="$TMP_ROOT/config-path-directory"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted CONFIG_PATH pointing at STORAGE_ROOT"
	assert_file_contains "$case_dir/start.log" "CONFIG_PATH must point at a file under STORAGE_ROOT"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite CONFIG_PATH directory"
}

run_tilde_paths_are_expanded_test() {
	local case_dir="$TMP_ROOT/tilde-paths"
	local app_dir="$case_dir/app"
	local fake_home="$case_dir/home"
	local data_dir="$fake_home/.mnemonas"
	local capture_dir="$case_dir/capture"
	mkdir -p "$fake_home" "$capture_dir"
	make_fake_app "$app_dir"

	HOME="$fake_home" \
		CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="~/.mnemonas" \
		CONFIG_PATH="~/.mnemonas/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log"

	assert_file_contains "$data_dir/config.toml" "root = \"$data_dir\""
	assert_file_contains "$capture_dir/nasd.args" "--config $data_dir/config.toml"
	assert_file_contains "$capture_dir/dataplane.args" "--data-dir $data_dir/.mnemonas/objects"
	[[ ! -e "$REPO_ROOT/~/.mnemonas/config.toml" ]] || fail "config file was written to a literal tilde path"
}

run_dataplane_data_dir_traversal_test() {
	local case_dir="$TMP_ROOT/dataplane-data-dir-traversal"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		DATAPLANE_DATA_DIR="$data_dir/.mnemonas/../objects" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted DATAPLANE_DATA_DIR with parent directory segment"
	assert_file_contains "$case_dir/start.log" "Refusing to prepare DATAPLANE_DATA_DIR with parent directory segments"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite invalid DATAPLANE_DATA_DIR"
}

run_dataplane_addr_validation_test() {
	local case_dir="$TMP_ROOT/dataplane-addr-validation"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane]
grpc_address = "127.0.0.1:70000"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		DATAPLANE_HTTP_ADDR=$'127.0.0.1:9091\n--log-level=debug' \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted invalid dataplane HTTP address"
	assert_file_contains "$case_dir/start.log" "DATAPLANE_HTTP_ADDR must not contain whitespace"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite invalid dataplane HTTP address"

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		DATAPLANE_HTTP_ADDR="127.0.0.1:9091" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start-grpc.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted invalid dataplane gRPC port"
	assert_file_contains "$case_dir/start-grpc.log" "DATAPLANE_GRPC_ADDR port must be between 1 and 65535"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite invalid dataplane gRPC port"
}

run_dataplane_grpc_env_mismatch_test() {
	local case_dir="$TMP_ROOT/dataplane-grpc-env-mismatch"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane]
grpc_address = "127.0.0.1:9090"
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		DATAPLANE_GRPC_ADDR="127.0.0.1:19090" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted divergent DATAPLANE_GRPC_ADDR"
	assert_file_contains "$case_dir/start.log" "DATAPLANE_GRPC_ADDR does not match [dataplane].grpc_address"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite divergent DATAPLANE_GRPC_ADDR"
	[[ ! -f "$capture_dir/nasd.args" ]] || fail "nasd started despite divergent DATAPLANE_GRPC_ADDR"
}

run_dataplane_exposure_warning_test() {
	local case_dir="$TMP_ROOT/dataplane-exposure"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane]
grpc_address = "0.0.0.0:19090"

[ dataplane . cdc ]
min_chunk_size = 65_536
avg_chunk_size = 1_048_576
max_chunk_size = 4_194_304
EOF

	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		DATAPLANE_HTTP_ADDR="0.0.0.0:19091" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1

	assert_file_contains "$case_dir/start.log" "dataplane gRPC address is 0.0.0.0:19090"
	assert_file_contains "$case_dir/start.log" "dataplane HTTP address is 0.0.0.0:19091"
	assert_file_contains "$capture_dir/dataplane.args" "--grpc 0.0.0.0:19090"
	assert_file_contains "$capture_dir/dataplane.args" "--listen 0.0.0.0:19091"
	assert_file_contains "$capture_dir/dataplane.args" "--min-chunk-size 65536"
	assert_file_contains "$capture_dir/dataplane.args" "--avg-chunk-size 1048576"
	assert_file_contains "$capture_dir/dataplane.args" "--max-chunk-size 4194304"
}

run_invalid_cdc_range_test() {
	local case_dir="$TMP_ROOT/invalid-cdc-range"
	local app_dir="$case_dir/app"
	local data_dir="$case_dir/data"
	local capture_dir="$case_dir/capture"
	local status
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[storage]
root = "$data_dir"

[dataplane.cdc]
min_chunk_size = 32_768
avg_chunk_size = 262_144
max_chunk_size = 1_048_576
EOF

	set +e
	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "docker-start accepted undersized min chunk"
	assert_file_contains "$case_dir/start.log" "min_chunk_size: must be at least 65536 bytes"
	[[ ! -f "$capture_dir/dataplane.args" ]] || fail "dataplane started despite invalid CDC range"
	[[ ! -f "$capture_dir/nasd.args" ]] || fail "nasd started despite invalid CDC range"
}

run_manual_docker_run_docs_prepare_data_dir_test() {
	local file
	for file in "$REPO_ROOT/docs/docker-deployment.md" "$REPO_ROOT/docs/docker-deployment.en.md"; do
		assert_file_contains "$file" 'mkdir -p "$HOME/.mnemonas"'
		assert_file_contains "$file" "/data/config.toml"
	done
}

run_dockerfile_default_config_permission_test() {
	assert_file_contains "$REPO_ROOT/Dockerfile" "COPY --chmod=0644 mnemonas.example.toml /app/mnemonas.example.toml"
}

run_default_config_test
run_mismatched_storage_root_warning_test
run_toml_escaped_storage_root_test
run_missing_storage_root_test
run_protected_storage_root_test
run_normalized_protected_storage_root_test
run_storage_root_traversal_test
run_storage_root_newline_test
run_storage_root_control_character_test
run_storage_root_symlink_test
run_managed_subdir_symlink_test
run_config_path_scope_test
run_config_path_directory_test
run_tilde_paths_are_expanded_test
run_dataplane_data_dir_traversal_test
run_dataplane_addr_validation_test
run_dataplane_grpc_env_mismatch_test
run_invalid_cdc_range_test
run_dataplane_exposure_warning_test
run_manual_docker_run_docs_prepare_data_dir_test
run_dockerfile_default_config_permission_test

printf '[docker-start-test] all checks passed\n'
