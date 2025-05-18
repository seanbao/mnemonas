#!/usr/bin/env bash
# shellcheck disable=SC2016

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

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
	local capture_dir="$case_dir/capture"
	mkdir -p "$data_dir" "$capture_dir"
	make_fake_app "$app_dir"
	cat > "$data_dir/config.toml" <<EOF
[server]
host = "0.0.0.0"
port = 8080

[ storage ] # storage root may have comments in hand-edited TOML
root = '$data_dir/nested'

[ dataplane ] # dataplane endpoint
grpc_address = '127.0.0.1:9090'
EOF

	CAPTURE_DIR="$capture_dir" \
		APP_DIR="$app_dir" \
		STORAGE_ROOT="$data_dir" \
		CONFIG_PATH="$data_dir/config.toml" \
		bash "$REPO_ROOT/scripts/docker-start.sh" > "$case_dir/start.log" 2>&1

	assert_file_contains "$case_dir/start.log" "[WARN] Configured [storage].root is $data_dir/nested, but Docker STORAGE_ROOT is $data_dir"
	assert_file_contains "$capture_dir/dataplane.args" "--data-dir $data_dir/nested/.mnemonas/objects"
	assert_file_contains "$capture_dir/nasd.args" "--config $data_dir/config.toml"
	assert_mode "$data_dir/nested" "750"
	assert_mode "$data_dir/nested/files" "750"
	assert_mode "$data_dir/nested/.mnemonas" "700"
	assert_mode "$data_dir/nested/.mnemonas/objects" "700"
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

run_default_config_test
run_mismatched_storage_root_warning_test
run_missing_storage_root_test
run_dataplane_exposure_warning_test

printf '[docker-start-test] all checks passed\n'
