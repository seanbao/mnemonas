#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fail() {
	printf '[dataplane-start-test] ERROR: %s\n' "$*" >&2
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

assert_not_exists() {
	local path="$1"
	[[ ! -e "$path" ]] || fail "$path unexpectedly exists"
}

run_underscore_chunk_values_test() {
	local case_dir="$TMP_ROOT/underscore-chunks"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[ storage ]
root = "$case_dir/storage\u002droot"

[ dataplane ]
grpc_address = "127.0.0.1\u003a19090"

[ dataplane . cdc ]
min_chunk_size = 65_536
avg_chunk_size = 1_048_576
max_chunk_size = 4_194_304
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh"

	assert_file_contains "$capture_path" "--grpc 127.0.0.1:19090"
	assert_file_contains "$capture_path" "--data-dir $case_dir/storage-root/.mnemonas/objects"
	assert_file_contains "$capture_path" "--min-chunk-size 65536"
	assert_file_contains "$capture_path" "--avg-chunk-size 1048576"
	assert_file_contains "$capture_path" "--max-chunk-size 4194304"
}

run_invalid_chunk_value_test() {
	local case_dir="$TMP_ROOT/invalid-chunks"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"

[dataplane.cdc]
min_chunk_size = 65__536
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "invalid chunk size was accepted"
	assert_file_contains "$case_dir/out.log" "invalid [dataplane.cdc].min_chunk_size value"
	assert_not_exists "$capture_path"
}

run_min_chunk_floor_test() {
	local case_dir="$TMP_ROOT/min-chunk-floor"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"

[dataplane.cdc]
min_chunk_size = 32_768
avg_chunk_size = 262_144
max_chunk_size = 1_048_576
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "undersized min chunk was accepted"
	assert_file_contains "$case_dir/out.log" "min_chunk_size must be at least 65536 bytes"
	assert_not_exists "$capture_path"
}

run_defaulted_chunk_order_test() {
	local case_dir="$TMP_ROOT/defaulted-chunk-order"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"

[dataplane.cdc]
min_chunk_size = 2_097_152
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "chunk order using default avg was accepted"
	assert_file_contains "$case_dir/out.log" "min_chunk_size must be less than avg_chunk_size"
	assert_not_exists "$capture_path"
}

run_invalid_grpc_address_test() {
	local case_dir="$TMP_ROOT/invalid-grpc-address"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"

[dataplane]
grpc_address = "bad/host:9090"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "invalid dataplane grpc address was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_GRPC_ADDR host is invalid"
	assert_not_exists "$capture_path"
}

run_invalid_http_address_test() {
	local case_dir="$TMP_ROOT/invalid-http-address"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		DATAPLANE_HTTP_ADDR=$'127.0.0.1:9091\n--log-level=debug' \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "invalid dataplane http address was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_HTTP_ADDR must not contain whitespace"
	assert_not_exists "$capture_path"
}

run_quoted_hash_storage_root_test() {
	local case_dir="$TMP_ROOT/hash-root"
	local config_path="$case_dir/config.toml"
	local storage_dir="$case_dir/storage#with-hash"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[ storage ]
root = "$storage_dir" # inline comments should not truncate quoted values

[ dataplane ]
grpc_address = "127.0.0.1:19091" # regular inline comment
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh"

	assert_file_contains "$capture_path" "--grpc 127.0.0.1:19091"
	assert_file_contains "$capture_path" "--data-dir $storage_dir/.mnemonas/objects"
}

run_protected_storage_root_test() {
	local case_dir="$TMP_ROOT/protected-storage-root"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "/tmp"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "protected storage root was accepted"
	assert_file_contains "$case_dir/out.log" "storage.root points at a protected system directory"
	assert_not_exists "$capture_path"
}

run_storage_root_traversal_test() {
	local case_dir="$TMP_ROOT/storage-root-traversal"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/data/../escape"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "storage root traversal was accepted"
	assert_file_contains "$case_dir/out.log" "storage.root cannot contain parent directory segments"
	assert_not_exists "$capture_path"
}

run_storage_root_newline_test() {
	local case_dir="$TMP_ROOT/storage-root-newline"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local storage_root
	local status
	mkdir -p "$case_dir"
	storage_root="$case_dir/storage"$'\n'"escape"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		STORAGE_ROOT="$storage_root" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "storage root newline was accepted"
	assert_file_contains "$case_dir/out.log" "storage.root cannot contain newline characters"
	assert_not_exists "$capture_path"
}

run_storage_root_control_character_test() {
	local case_dir="$TMP_ROOT/storage-root-control"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local storage_root
	local status
	mkdir -p "$case_dir"
	storage_root="$case_dir/storage"$'\a'"escape"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		STORAGE_ROOT="$storage_root" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "storage root control character was accepted"
	assert_file_contains "$case_dir/out.log" "storage.root cannot contain control characters"
	assert_not_exists "$capture_path"
}

run_config_path_symlink_test() {
	local case_dir="$TMP_ROOT/config-path-symlink"
	local real_dir="$case_dir/real"
	local link_dir="$case_dir/link"
	local config_path="$link_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$real_dir"
	ln -s "$real_dir" "$link_dir"

	cat > "$real_dir/config.toml" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "CONFIG_PATH through symlink component was accepted"
	assert_file_contains "$case_dir/out.log" "CONFIG_PATH must not contain symlink path components"
	assert_not_exists "$capture_path"
}

run_storage_root_symlink_test() {
	local case_dir="$TMP_ROOT/storage-root-symlink"
	local config_path="$case_dir/config.toml"
	local real_storage="$case_dir/real-storage"
	local storage_link="$case_dir/storage-link"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir" "$real_storage"
	ln -s "$real_storage" "$storage_link"

	cat > "$config_path" <<EOF
[storage]
root = "$storage_link"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "storage.root symlink component was accepted"
	assert_file_contains "$case_dir/out.log" "storage.root must not contain symlink path components"
	assert_not_exists "$capture_path"
}

run_protected_dataplane_data_dir_test() {
	local case_dir="$TMP_ROOT/protected-dataplane-dir"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		DATAPLANE_DATA_DIR="/tmp//" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "protected DATAPLANE_DATA_DIR was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_DATA_DIR points at a protected system directory"
	assert_not_exists "$capture_path"
}

run_dataplane_data_dir_traversal_test() {
	local case_dir="$TMP_ROOT/dataplane-dir-traversal"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		DATAPLANE_DATA_DIR="$case_dir/data/../objects" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "DATAPLANE_DATA_DIR traversal was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_DATA_DIR cannot contain parent directory segments"
	assert_not_exists "$capture_path"
}

run_dataplane_data_dir_newline_test() {
	local case_dir="$TMP_ROOT/dataplane-dir-newline"
	local config_path="$case_dir/config.toml"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local data_dir
	local status
	mkdir -p "$case_dir"
	data_dir="$case_dir/objects"$'\n'"escape"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		DATAPLANE_DATA_DIR="$data_dir" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "DATAPLANE_DATA_DIR newline was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_DATA_DIR cannot contain newline characters"
	assert_not_exists "$capture_path"
}

run_dataplane_data_dir_symlink_test() {
	local case_dir="$TMP_ROOT/dataplane-dir-symlink"
	local config_path="$case_dir/config.toml"
	local real_objects="$case_dir/real-objects"
	local objects_link="$case_dir/objects-link"
	local capture_path="$case_dir/dataplane.args"
	local dataplane_bin="$case_dir/capture-dataplane"
	local status
	mkdir -p "$case_dir" "$real_objects"
	ln -s "$real_objects" "$objects_link"

	cat > "$config_path" <<EOF
[storage]
root = "$case_dir/storage"
EOF

	write_executable "$dataplane_bin" \
		'#!/usr/bin/env bash' \
		'printf "%s\n" "$*" > "$CAPTURE_PATH"'

	set +e
	CAPTURE_PATH="$capture_path" \
		CONFIG_PATH="$config_path" \
		DATAPLANE_BIN="$dataplane_bin" \
		DATAPLANE_DATA_DIR="$objects_link" \
		bash "$REPO_ROOT/scripts/mnemonas-dataplane-start.sh" > "$case_dir/out.log" 2>&1
	status=$?
	set -e

	[[ "$status" -ne 0 ]] || fail "DATAPLANE_DATA_DIR symlink component was accepted"
	assert_file_contains "$case_dir/out.log" "DATAPLANE_DATA_DIR must not contain symlink path components"
	assert_not_exists "$capture_path"
}

run_underscore_chunk_values_test
run_invalid_chunk_value_test
run_min_chunk_floor_test
run_defaulted_chunk_order_test
run_invalid_grpc_address_test
run_invalid_http_address_test
run_quoted_hash_storage_root_test
run_protected_storage_root_test
run_storage_root_traversal_test
run_storage_root_newline_test
run_storage_root_control_character_test
run_config_path_symlink_test
run_storage_root_symlink_test
run_protected_dataplane_data_dir_test
run_dataplane_data_dir_traversal_test
run_dataplane_data_dir_newline_test
run_dataplane_data_dir_symlink_test

printf '[dataplane-start-test] all checks passed\n'
