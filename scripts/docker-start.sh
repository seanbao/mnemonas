#!/bin/bash
set -e

CONFIG_PATH="${CONFIG_PATH:-/root/.mnemonas/config.toml}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"

read_config_value() {
	local section=$1
	local key=$2

	if [[ ! -f "$CONFIG_PATH" ]]; then
		return 0
	fi

	awk -v section="[$section]" -v key="$key" '
		$0 == section { in_section = 1; next }
		/^\[/ { in_section = 0 }
		in_section && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
			line = $0
			sub(/^[^=]*=[[:space:]]*/, "", line)
			sub(/[[:space:]]*#.*$/, "", line)
			gsub(/^"/, "", line)
			gsub(/"$/, "", line)
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
		"~")
			echo "$HOME"
			;;
		"~/"*)
			echo "$HOME/${path#~/}"
			;;
		*)
			echo "$path"
			;;
	esac
}

storage_root=$(expand_path "$(read_config_value storage root)")
dataplane_grpc_addr="${DATAPLANE_GRPC_ADDR:-$(read_config_value dataplane grpc_address)}"
dataplane_data_dir="${DATAPLANE_DATA_DIR:-$storage_root/.mnemonas/objects}"

if [[ -z "$dataplane_grpc_addr" ]]; then
	dataplane_grpc_addr="127.0.0.1:9090"
fi

echo "[INFO] Starting dataplane on $dataplane_grpc_addr with data dir $dataplane_data_dir"
/app/dataplane --listen "$DATAPLANE_HTTP_ADDR" --grpc "$dataplane_grpc_addr" --data-dir "$dataplane_data_dir" &

# Give the dataplane a short window to bind before starting nasd.
sleep 1

echo "[INFO] Starting nasd with config $CONFIG_PATH"
exec /app/nasd --config "$CONFIG_PATH"