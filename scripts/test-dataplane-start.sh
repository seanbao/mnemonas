#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

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
root = "$case_dir/storage"

[ dataplane ]
grpc_address = "127.0.0.1:19090"

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
	assert_file_contains "$capture_path" "--data-dir $case_dir/storage/.mnemonas/objects"
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

run_underscore_chunk_values_test
run_invalid_chunk_value_test
run_quoted_hash_storage_root_test

printf '[dataplane-start-test] all checks passed\n'
