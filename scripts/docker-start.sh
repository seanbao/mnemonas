#!/bin/bash
set -euo pipefail

STORAGE_ROOT="${STORAGE_ROOT:-/data}"
CONFIG_PATH="${CONFIG_PATH:-$STORAGE_ROOT/config.toml}"
APP_DIR="${APP_DIR:-/app}"
DEFAULT_CONFIG_PATH="${DEFAULT_CONFIG_PATH:-$APP_DIR/mnemonas.example.toml}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"

terminate_children() {
	if [[ -n "${nasd_pid:-}" ]]; then
		kill "$nasd_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "${dataplane_pid:-}" ]]; then
		kill "$dataplane_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "${nasd_pid:-}" ]]; then
		wait "$nasd_pid" >/dev/null 2>&1 || true
	fi
	if [[ -n "${dataplane_pid:-}" ]]; then
		wait "$dataplane_pid" >/dev/null 2>&1 || true
	fi
}

# shellcheck disable=SC2317 # Invoked indirectly by the INT/TERM trap.
handle_signal() {
	terminate_children
	exit 143
}

trap handle_signal INT TERM

ensure_config() {
	local storage_root_escaped

	if [[ -f "$CONFIG_PATH" ]]; then
		return 0
	fi
	if [[ ! -f "$DEFAULT_CONFIG_PATH" ]]; then
		echo "[ERROR] Config $CONFIG_PATH is missing and default config $DEFAULT_CONFIG_PATH was not found" >&2
		return 1
	fi

	echo "[INFO] Config $CONFIG_PATH not found; creating it from $DEFAULT_CONFIG_PATH"
	mkdir -p "$(dirname "$CONFIG_PATH")"
	cp "$DEFAULT_CONFIG_PATH" "$CONFIG_PATH"
	storage_root_escaped="$(printf '%s' "$STORAGE_ROOT" | sed -e 's/[&|\\]/\\&/g')"
	sed -i "s|^root = \".*\"|root = \"$storage_root_escaped\"|" "$CONFIG_PATH"
	chmod 600 "$CONFIG_PATH" || true
}

read_config_value() {
	local section=$1
	local key=$2

	if [[ ! -f "$CONFIG_PATH" ]]; then
		return 0
	fi

	awk -v section="[$section]" -v key="$key" '
		{
			line = $0
			sub(/[[:space:]]*#.*$/, "", line)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
			section_line = line
			if (section_line ~ /^\[/) {
				sub(/^\[[[:space:]]*/, "[", section_line)
				sub(/[[:space:]]*\]$/, "]", section_line)
				gsub(/[[:space:]]*\.[[:space:]]*/, ".", section_line)
			}
		}
		section_line == section { in_section = 1; next }
		section_line ~ /^\[/ { in_section = 0 }
		in_section && line ~ "^[[:space:]]*" key "[[:space:]]*=" {
			sub(/^[^=]*=[[:space:]]*/, "", line)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
			gsub(/^"/, "", line)
			gsub(/"$/, "", line)
			gsub(/^\047/, "", line)
			gsub(/\047$/, "", line)
			print line
			exit
		}
	' "$CONFIG_PATH"
}

expand_path() {
	local path=$1

	case "$path" in
		"")
			echo "$HOME/.mnemonas"
			;;
		\~)
			echo "$HOME"
			;;
		\~/*)
			echo "$HOME/${path#\~/}"
			;;
		*)
			echo "$path"
			;;
	esac
}

normalize_compare_path() {
	local path="${1%/}"
	if [[ -z "$path" ]]; then
		path="/"
	fi
	echo "$path"
}

endpoint_host() {
	local endpoint="$1"
	local host="$endpoint"
	if [[ "$host" == *:* ]]; then
		host="${host%:*}"
	fi
	host="${host#\[}"
	host="${host%\]}"
	printf '%s\n' "$host"
}

is_loopback_host() {
	local host="$1"
	case "$host" in
		localhost|ip6-localhost|127.*|::1)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

warn_if_non_loopback_endpoint() {
	local endpoint="$1"
	local label="$2"
	local host

	host="$(endpoint_host "$endpoint")"
	if ! is_loopback_host "$host"; then
		echo "[WARN] $label is $endpoint; dataplane ports do not provide external authentication and should stay on 127.0.0.1 unless isolated by a trusted private network" >&2
	fi
}

require_configured_storage_root() {
	local configured_root=$1

	if [[ -n "$configured_root" ]]; then
		return 0
	fi

	echo "[ERROR] $CONFIG_PATH does not set [storage].root; set root = \"$STORAGE_ROOT\" for Docker deployments" >&2
	return 1
}

append_configured_uint_arg() {
	local -n target_args=$1
	local flag=$2
	local section=$3
	local key=$4
	local value
	local normalized_value

	value="$(read_config_value "$section" "$key")"
	if [[ -z "$value" ]]; then
		return 0
	fi
	if [[ ! "$value" =~ ^[0-9](_?[0-9])*$ ]]; then
		echo "[ERROR] $CONFIG_PATH has invalid [$section].$key value: $value" >&2
		return 1
	fi
	normalized_value="${value//_/}"
	target_args+=("$flag" "$normalized_value")
}

ensure_config

storage_root_config="$(read_config_value storage root)"
require_configured_storage_root "$storage_root_config"
storage_root=$(expand_path "$storage_root_config")
expected_storage_root=$(expand_path "$STORAGE_ROOT")
dataplane_grpc_addr="${DATAPLANE_GRPC_ADDR:-$(read_config_value dataplane grpc_address)}"
dataplane_data_dir="${DATAPLANE_DATA_DIR:-$storage_root/.mnemonas/objects}"
dataplane_args=("$APP_DIR/dataplane" --listen "$DATAPLANE_HTTP_ADDR" --grpc "$dataplane_grpc_addr" --data-dir "$dataplane_data_dir")

if [[ "$(normalize_compare_path "$storage_root")" != "$(normalize_compare_path "$expected_storage_root")" ]]; then
	echo "[WARN] Configured [storage].root is $storage_root, but Docker STORAGE_ROOT is $expected_storage_root. Data and initial password files will be under the configured root." >&2
fi

mkdir -p "$storage_root/files" "$storage_root/.mnemonas/objects"
if ! chmod 750 "$storage_root" "$storage_root/files"; then
	echo "[WARN] Could not tighten permissions on $storage_root or $storage_root/files" >&2
fi
if ! chmod 700 "$storage_root/.mnemonas" "$storage_root/.mnemonas/objects"; then
	echo "[WARN] Could not tighten permissions on $storage_root/.mnemonas or $storage_root/.mnemonas/objects" >&2
fi

if [[ -z "$dataplane_grpc_addr" ]]; then
	dataplane_grpc_addr="127.0.0.1:9090"
	dataplane_args=("$APP_DIR/dataplane" --listen "$DATAPLANE_HTTP_ADDR" --grpc "$dataplane_grpc_addr" --data-dir "$dataplane_data_dir")
fi
warn_if_non_loopback_endpoint "$dataplane_grpc_addr" "dataplane gRPC address"
warn_if_non_loopback_endpoint "$DATAPLANE_HTTP_ADDR" "dataplane HTTP address"
append_configured_uint_arg dataplane_args --min-chunk-size dataplane.cdc min_chunk_size
append_configured_uint_arg dataplane_args --avg-chunk-size dataplane.cdc avg_chunk_size
append_configured_uint_arg dataplane_args --max-chunk-size dataplane.cdc max_chunk_size

echo "[INFO] Starting dataplane on $dataplane_grpc_addr with data dir $dataplane_data_dir"
"${dataplane_args[@]}" &
dataplane_pid=$!

# Give the dataplane a short window to bind before starting nasd.
sleep 1
if ! kill -0 "$dataplane_pid" >/dev/null 2>&1; then
	echo "[ERROR] dataplane exited before nasd startup" >&2
	wait "$dataplane_pid"
fi

echo "[INFO] Starting nasd with config $CONFIG_PATH"
"$APP_DIR/nasd" --config "$CONFIG_PATH" &
nasd_pid=$!

set +e
wait -n "$dataplane_pid" "$nasd_pid"
status=$?
set -e

terminate_children
exit "$status"
