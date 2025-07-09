#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
ENV_PATH="${ENV_PATH:-$REPO_ROOT/.env}"
ENV_EXAMPLE="${ENV_EXAMPLE:-$REPO_ROOT/.env.example}"
DATA_DIR_EXPLICIT=0
HOST_PORT_EXPLICIT=0
if [[ -n "${DATA_DIR:-}" || -n "${MNEMONAS_DATA_DIR:-}" ]]; then
	DATA_DIR_EXPLICIT=1
fi
if [[ -n "${HOST_PORT:-}" || -n "${MNEMONAS_HTTP_PORT:-}" ]]; then
	HOST_PORT_EXPLICIT=1
fi
DATA_DIR="${DATA_DIR:-${MNEMONAS_DATA_DIR:-}}"
HOST_PORT="${HOST_PORT:-${MNEMONAS_HTTP_PORT:-}}"
RUN_PREFLIGHT=1
START_AFTER_PREPARE=0
BUILD_IMAGE=1

log() {
	printf '[mnemonas-docker] %s\n' "$*"
}

fail() {
	printf '[mnemonas-docker] ERROR: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
MnemoNAS Docker quickstart

Usage:
  ./scripts/docker-quickstart.sh [options]

Options:
  --start              Prepare, run preflight, then start with docker compose up -d
  --no-build           With --start, do not pass --build to docker compose up
  --skip-preflight     Do not run scripts/mnemonas-docker-preflight.sh
  --port PORT          Host HTTP port for Web UI, API, and WebDAV
  --data-dir PATH      Host data directory mounted into the container as /data
  --env PATH           Compose .env file to create/update
  -h, --help           Show this help

Environment:
  REPO_ROOT            Repository path, defaults to this script's parent directory
  ENV_PATH             Compose env file, defaults to <repo>/.env
  DATA_DIR             Host data directory, defaults to $HOME/.mnemonas
  HOST_PORT            Host HTTP port, defaults to 8080
EOF
}

require_port() {
	local value="$1"
	[[ "$value" =~ ^[0-9]+$ ]] || fail "--port must be a number from 1 to 65535: $value"
	(( 10#$value >= 1 && 10#$value <= 65535 )) || fail "--port must be a number from 1 to 65535: $value"
}

require_absolute_data_dir() {
	case "$DATA_DIR" in
		/*) ;;
		*) fail "--data-dir must be an absolute path because Docker Compose does not expand relative volume roots: $DATA_DIR" ;;
	esac
}

env_value() {
	local key="$1"
	local file="$2"

	[[ -f "$file" ]] || return 0
	awk -F= -v key="$key" '
		{
			line = $0
			sub(/[[:space:]]*#.*$/, "", line)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
			sub(/^export[[:space:]]+/, "", line)
		}
		line ~ "^[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=" {
			name = line
			sub(/[[:space:]]*=.*/, "", name)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
			if (name == key) {
				value = line
				sub(/^[^=]*=[[:space:]]*/, "", value)
				gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
				gsub(/^"/, "", value)
				gsub(/"$/, "", value)
				gsub(/^\047/, "", value)
				gsub(/\047$/, "", value)
				print value
				exit
			}
		}
	' "$file"
}

write_env_value() {
	local key="$1"
	local value="$2"
	local file="$3"
	local tmp

	tmp="$(mktemp -t mnemonas-env.XXXXXX)"
	awk -v key="$key" -v value="$value" '
		BEGIN { done = 0 }
		{
			line = $0
			match_line = line
			sub(/[[:space:]]*#.*$/, "", match_line)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", match_line)
			sub(/^export[[:space:]]+/, "", match_line)
			name = match_line
			sub(/[[:space:]]*=.*/, "", name)
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
			if (name == key && match_line ~ /^[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=/) {
				print key "=" value
				done = 1
				next
			}
			print line
		}
		END {
			if (!done) {
				print key "=" value
			}
		}
	' "$file" > "$tmp"
	mv "$tmp" "$file"
}

apply_existing_env_defaults() {
	local env_data_dir env_host_port

	[[ -f "$ENV_PATH" ]] || return 0
	env_data_dir="$(env_value MNEMONAS_DATA_DIR "$ENV_PATH")"
	env_host_port="$(env_value MNEMONAS_HTTP_PORT "$ENV_PATH")"
	if [[ "$DATA_DIR_EXPLICIT" != "1" && -n "$env_data_dir" ]]; then
		DATA_DIR="$env_data_dir"
	fi
	if [[ "$HOST_PORT_EXPLICIT" != "1" && -n "$env_host_port" ]]; then
		HOST_PORT="$env_host_port"
	fi
}

prepare_env() {
	local uid gid

	[[ -f "$REPO_ROOT/docker-compose.yml" ]] || fail "docker-compose.yml not found in $REPO_ROOT"
	if [[ ! -f "$ENV_PATH" ]]; then
		[[ -f "$ENV_EXAMPLE" ]] || fail ".env does not exist and template is missing: $ENV_EXAMPLE"
		log "creating $ENV_PATH from $ENV_EXAMPLE"
		cp "$ENV_EXAMPLE" "$ENV_PATH"
	else
		log "updating existing env file: $ENV_PATH"
	fi

	uid="$(id -u)"
	gid="$(id -g)"
	write_env_value MNEMONAS_UID "$uid" "$ENV_PATH"
	write_env_value MNEMONAS_GID "$gid" "$ENV_PATH"
	write_env_value MNEMONAS_HTTP_PORT "$HOST_PORT" "$ENV_PATH"
	write_env_value MNEMONAS_DATA_DIR "$DATA_DIR" "$ENV_PATH"
}

prepare_data_dir() {
	log "creating data directory: $DATA_DIR"
	mkdir -p "$DATA_DIR"
	chmod 750 "$DATA_DIR"
}

run_preflight() {
	if [[ "$RUN_PREFLIGHT" != "1" ]]; then
		log "skipping Docker preflight"
		return
	fi

	[[ -x "$REPO_ROOT/scripts/mnemonas-docker-preflight.sh" ]] || fail "preflight script is missing or not executable: $REPO_ROOT/scripts/mnemonas-docker-preflight.sh"
	log "running Docker preflight"
	env REPO_ROOT="$REPO_ROOT" ENV_PATH="$ENV_PATH" DATA_DIR="$DATA_DIR" HOST_PORT="$HOST_PORT" "$REPO_ROOT/scripts/mnemonas-docker-preflight.sh"
}

start_compose() {
	local args=(compose -f "$REPO_ROOT/docker-compose.yml" --env-file "$ENV_PATH" up -d)
	if [[ "$BUILD_IMAGE" == "1" ]]; then
		args+=(--build)
	fi

	log "starting MnemoNAS with docker ${args[*]}"
	docker "${args[@]}"
}

print_next_steps() {
	log "ready"
	printf '\n'
	printf 'Web UI:              http://localhost:%s\n' "$HOST_PORT"
	printf 'Initial password:    %s/.mnemonas/initial-password.txt\n' "$DATA_DIR"
	printf 'WebDAV URL:          http://localhost:%s/dav\n' "$HOST_PORT"
	printf 'Status:              docker compose -f %s --env-file %s ps\n' "$REPO_ROOT/docker-compose.yml" "$ENV_PATH"
	printf 'Logs:                docker compose -f %s --env-file %s logs -f\n' "$REPO_ROOT/docker-compose.yml" "$ENV_PATH"
	if [[ "$START_AFTER_PREPARE" != "1" ]]; then
		printf 'Start:               ./scripts/docker-quickstart.sh --start\n'
	fi
	printf '\n'
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--start)
			START_AFTER_PREPARE=1
			shift
			;;
		--no-build)
			BUILD_IMAGE=0
			shift
			;;
		--skip-preflight)
			RUN_PREFLIGHT=0
			shift
			;;
		--port)
			[[ $# -ge 2 ]] || fail "--port requires a value"
			HOST_PORT="$2"
			HOST_PORT_EXPLICIT=1
			shift 2
			;;
		--port=*)
			HOST_PORT="${1#*=}"
			HOST_PORT_EXPLICIT=1
			shift
			;;
		--data-dir)
			[[ $# -ge 2 ]] || fail "--data-dir requires a value"
			DATA_DIR="$2"
			DATA_DIR_EXPLICIT=1
			shift 2
			;;
		--data-dir=*)
			DATA_DIR="${1#*=}"
			DATA_DIR_EXPLICIT=1
			shift
			;;
		--env)
			[[ $# -ge 2 ]] || fail "--env requires a value"
			ENV_PATH="$2"
			shift 2
			;;
		--env=*)
			ENV_PATH="${1#*=}"
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			fail "unknown option: $1"
			;;
	esac
done

apply_existing_env_defaults
DATA_DIR="${DATA_DIR:-$HOME/.mnemonas}"
HOST_PORT="${HOST_PORT:-8080}"
require_port "$HOST_PORT"
require_absolute_data_dir
prepare_data_dir
prepare_env
run_preflight
if [[ "$START_AFTER_PREPARE" == "1" ]]; then
	start_compose
fi
print_next_steps
