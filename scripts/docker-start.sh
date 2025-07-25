#!/bin/bash
set -euo pipefail

STORAGE_ROOT="${STORAGE_ROOT:-/data}"
CONFIG_PATH="${CONFIG_PATH:-$STORAGE_ROOT/config.toml}"
APP_DIR="${APP_DIR:-/app}"
DEFAULT_CONFIG_PATH="${DEFAULT_CONFIG_PATH:-$APP_DIR/mnemonas.example.toml}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"
MIN_CDC_CHUNK_SIZE=65536
MAX_CDC_CHUNK_SIZE=67108864
DEFAULT_CDC_MIN_CHUNK_SIZE=262144
DEFAULT_CDC_AVG_CHUNK_SIZE=1048576
DEFAULT_CDC_MAX_CHUNK_SIZE=4194304

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
	storage_root_escaped="$(printf '%s' "$STORAGE_ROOT" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/[&|\\]/\\&/g')"
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
		function strip_comment(text,    i, c, quote, escaped, out) {
			quote = ""
			escaped = 0
			out = ""
			for (i = 1; i <= length(text); i++) {
				c = substr(text, i, 1)
				if (quote == "\"") {
					out = out c
					if (escaped) {
						escaped = 0
						continue
					}
					if (c == "\\") {
						escaped = 1
						continue
					}
					if (c == quote) {
						quote = ""
					}
					continue
				}
				if (quote == "\047") {
					out = out c
					if (c == quote) {
						quote = ""
					}
					continue
				}
				if (c == "\"" || c == "\047") {
					quote = c
					out = out c
					continue
				}
				if (c == "#") {
					break
				}
				out = out c
			}
			return out
		}
		{
			line = strip_comment($0)
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
	local path="$1"
	while [[ "$path" != "/" && "$path" == */ ]]; do
		path="${path%/}"
	done
	if [[ -z "$path" ]]; then
		path="/"
	fi
	echo "$path"
}

path_has_parent_segment() {
	local value="$1"
	local trimmed="${value#/}"
	trimmed="${trimmed%/}"

	local -a segments
	IFS='/' read -r -a segments <<< "$trimmed"

	local segment
	for segment in "${segments[@]}"; do
		[[ "$segment" == ".." ]] && return 0
	done
	return 1
}

path_matches_or_contains() {
	local parent child
	parent="$(normalize_compare_path "$1")"
	child="$(normalize_compare_path "$2")"
	[[ "$parent" == "$child" ]] && return 0
	[[ "$parent" != "/" && "$child" == "$parent"/* ]]
}

require_no_symlink_components() {
	local value="$1"
	local label="$2"
	local trimmed="$value"
	local current="."
	local -a segments

	if [[ "$value" == /* ]]; then
		trimmed="${value#/}"
		current="/"
	fi
	trimmed="${trimmed%/}"

	IFS='/' read -r -a segments <<< "$trimmed"
	for segment in "${segments[@]}"; do
		[[ -n "$segment" && "$segment" != "." ]] || continue
		if [[ "$current" == "/" ]]; then
			current="/$segment"
		else
			current="$current/$segment"
		fi
		if [[ -L "$current" ]]; then
			echo "[ERROR] Refusing to prepare $label with symlink path component: $current" >&2
			return 1
		fi
		[[ -e "$current" ]] || break
	done
}

is_protected_storage_root() {
	local path
	path="$(normalize_compare_path "$1")"
	case "$path" in
		/|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
			return 0
			;;
	esac
	[[ "$path" == "$(normalize_compare_path "$APP_DIR")" ]]
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

require_safe_storage_root() {
	local storage_root=$1
	local label="${2:-storage.root}"

	if [[ "$storage_root" == *$'\n'* || "$storage_root" == *$'\r'* ]]; then
		echo "[ERROR] Refusing to prepare $label with newline characters" >&2
		return 1
	fi
	if path_has_parent_segment "$storage_root"; then
		echo "[ERROR] Refusing to prepare $label with parent directory segments: $storage_root" >&2
		return 1
	fi
	if [[ "$storage_root" != /* ]]; then
		echo "[ERROR] Refusing to prepare $label because it must be an absolute path in Docker: $storage_root" >&2
		return 1
	fi
	if ! require_no_symlink_components "$storage_root" "$label"; then
		return 1
	fi
	if is_protected_storage_root "$storage_root"; then
		echo "[ERROR] Refusing to prepare protected $label: $storage_root" >&2
		return 1
	fi
	if [[ "$storage_root" == *\"* || "$storage_root" == *\\* ]]; then
		echo "[ERROR] Refusing to prepare $label with quote or backslash characters: $storage_root" >&2
		return 1
	fi
}

require_safe_config_path() {
	local config_path="$1"
	local expected_storage_root="$2"

	if [[ -z "$config_path" ]]; then
		echo "[ERROR] CONFIG_PATH cannot be empty" >&2
		return 1
	fi
	if [[ "$config_path" == *$'\n'* || "$config_path" == *$'\r'* ]]; then
		echo "[ERROR] Refusing to prepare CONFIG_PATH with newline characters" >&2
		return 1
	fi
	if path_has_parent_segment "$config_path"; then
		echo "[ERROR] Refusing to prepare CONFIG_PATH with parent directory segments: $config_path" >&2
		return 1
	fi
	config_path="$(expand_path "$config_path")"
		if [[ "$config_path" != /* ]]; then
			echo "[ERROR] CONFIG_PATH must be an absolute path in Docker: $config_path" >&2
			return 1
		fi
		if [[ "$(normalize_compare_path "$config_path")" == "$(normalize_compare_path "$expected_storage_root")" ]]; then
			echo "[ERROR] CONFIG_PATH must point at a file under STORAGE_ROOT, not STORAGE_ROOT itself: $config_path" >&2
			return 1
		fi
		if [[ -d "$config_path" ]]; then
			echo "[ERROR] CONFIG_PATH must point at a file, not a directory: $config_path" >&2
			return 1
		fi
		if ! path_matches_or_contains "$expected_storage_root" "$config_path"; then
			echo "[ERROR] CONFIG_PATH must stay under STORAGE_ROOT in Docker: $config_path" >&2
			return 1
		fi
	if ! require_no_symlink_components "$config_path" "CONFIG_PATH"; then
		return 1
	fi
}

is_valid_tcp_host() {
	local host="$1"
	local label
	local -a labels

	host="${host%.}"
	[[ -n "$host" ]] || return 1
	[[ "$host" != *"["* && "$host" != *"]"* ]] || return 1

	if [[ "$host" == *:* ]]; then
		[[ "$host" =~ ^[0-9A-Fa-f:.]+$ ]]
		return
	fi

	[[ "${#host}" -le 253 ]] || return 1
	IFS='.' read -r -a labels <<< "$host"
	for label in "${labels[@]}"; do
		[[ -n "$label" && "${#label}" -le 63 ]] || return 1
		[[ "$label" != -* && "$label" != *- ]] || return 1
		[[ "$label" =~ ^[A-Za-z0-9-]+$ ]] || return 1
	done
	return 0
}

require_safe_tcp_addr() {
	local value="$1"
	local label="$2"
	local host=""
	local port=""

	[[ -n "$value" ]] || {
		echo "[ERROR] $label cannot be empty" >&2
		return 1
	}
	if [[ "$value" == *$'\r'* || "$value" == *$'\n'* || "$value" == *$'\t'* || "$value" == *" "* ]]; then
		echo "[ERROR] $label must not contain whitespace: $value" >&2
		return 1
	fi

	if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
		host="${BASH_REMATCH[1]}"
		port="${BASH_REMATCH[2]}"
	elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
		host="${BASH_REMATCH[1]}"
		port="${BASH_REMATCH[2]}"
	else
		echo "[ERROR] $label must be a host:port address: $value" >&2
		return 1
	fi

	if ! is_valid_tcp_host "$host"; then
		echo "[ERROR] $label host is invalid: $value" >&2
		return 1
	fi
	if ! (( 10#$port >= 1 && 10#$port <= 65535 )); then
		echo "[ERROR] $label port must be between 1 and 65535: $value" >&2
		return 1
	fi
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

trim_decimal_leading_zeroes() {
	local value="${1//_/}"

	while [[ "$value" == 0* && "$value" != "0" ]]; do
		value="${value#0}"
	done
	printf '%s' "$value"
}

decimal_uint_lt() {
	local left
	local right
	left="$(trim_decimal_leading_zeroes "$1")"
	right="$(trim_decimal_leading_zeroes "$2")"

	if (( ${#left} != ${#right} )); then
		(( ${#left} < ${#right} ))
		return
	fi
	[[ "$left" < "$right" ]]
}

decimal_uint_gt() {
	local left
	local right
	left="$(trim_decimal_leading_zeroes "$1")"
	right="$(trim_decimal_leading_zeroes "$2")"

	if (( ${#left} != ${#right} )); then
		(( ${#left} > ${#right} ))
		return
	fi
	[[ "$left" > "$right" ]]
}

configured_uint_or_default() {
	local section=$1
	local key=$2
	local default_value=$3
	local value

	value="$(read_config_value "$section" "$key")"
	if [[ -z "$value" ]]; then
		printf '%s' "$default_value"
		return 0
	fi
	if [[ ! "$value" =~ ^[0-9](_?[0-9])*$ ]]; then
		echo "[ERROR] $CONFIG_PATH has invalid [$section].$key value: $value" >&2
		return 1
	fi
	printf '%s' "${value//_/}"
}

validate_cdc_chunk_sizes() {
	local min_chunk_size
	local avg_chunk_size
	local max_chunk_size

	min_chunk_size="$(configured_uint_or_default dataplane.cdc min_chunk_size "$DEFAULT_CDC_MIN_CHUNK_SIZE")"
	avg_chunk_size="$(configured_uint_or_default dataplane.cdc avg_chunk_size "$DEFAULT_CDC_AVG_CHUNK_SIZE")"
	max_chunk_size="$(configured_uint_or_default dataplane.cdc max_chunk_size "$DEFAULT_CDC_MAX_CHUNK_SIZE")"

	if decimal_uint_lt "$min_chunk_size" "$MIN_CDC_CHUNK_SIZE"; then
		echo "[ERROR] $CONFIG_PATH has invalid [dataplane.cdc].min_chunk_size: must be at least $MIN_CDC_CHUNK_SIZE bytes" >&2
		return 1
	fi
	if ! decimal_uint_lt "$min_chunk_size" "$avg_chunk_size"; then
		echo "[ERROR] $CONFIG_PATH has invalid [dataplane.cdc] chunk sizes: min_chunk_size must be less than avg_chunk_size" >&2
		return 1
	fi
	if ! decimal_uint_lt "$avg_chunk_size" "$max_chunk_size"; then
		echo "[ERROR] $CONFIG_PATH has invalid [dataplane.cdc] chunk sizes: avg_chunk_size must be less than max_chunk_size" >&2
		return 1
	fi
	if decimal_uint_gt "$max_chunk_size" "$MAX_CDC_CHUNK_SIZE"; then
		echo "[ERROR] $CONFIG_PATH has invalid [dataplane.cdc].max_chunk_size: must be at most $MAX_CDC_CHUNK_SIZE bytes" >&2
		return 1
	fi
}

STORAGE_ROOT="$(expand_path "$STORAGE_ROOT")"
CONFIG_PATH="$(expand_path "$CONFIG_PATH")"
expected_storage_root="$STORAGE_ROOT"
require_safe_storage_root "$expected_storage_root"
require_safe_config_path "$CONFIG_PATH" "$expected_storage_root"
ensure_config

storage_root_config="$(read_config_value storage root)"
require_configured_storage_root "$storage_root_config"
storage_root=$(expand_path "$storage_root_config")
require_safe_storage_root "$storage_root"
dataplane_grpc_addr="${DATAPLANE_GRPC_ADDR:-$(read_config_value dataplane grpc_address)}"
dataplane_data_dir="$(expand_path "${DATAPLANE_DATA_DIR:-$storage_root/.mnemonas/objects}")"

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
fi
require_safe_storage_root "$dataplane_data_dir" "DATAPLANE_DATA_DIR"
require_safe_tcp_addr "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
require_safe_tcp_addr "$dataplane_grpc_addr" "DATAPLANE_GRPC_ADDR"
dataplane_args=("$APP_DIR/dataplane" --listen "$DATAPLANE_HTTP_ADDR" --grpc "$dataplane_grpc_addr" --data-dir "$dataplane_data_dir")
warn_if_non_loopback_endpoint "$dataplane_grpc_addr" "dataplane gRPC address"
warn_if_non_loopback_endpoint "$DATAPLANE_HTTP_ADDR" "dataplane HTTP address"
validate_cdc_chunk_sizes
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
