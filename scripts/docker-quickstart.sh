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

normalize_compare_path() {
	local path="$1"
	while [[ "$path" != "/" && "$path" == */ ]]; do
		path="${path%/}"
	done
	if [[ -z "$path" ]]; then
		path="/"
	fi
	printf '%s\n' "$path"
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
		[[ ! -L "$current" ]] || fail "$label must not contain symlink path components: $current"
		[[ -e "$current" ]] || break
	done
}

value_has_line_break() {
	local value="$1"
	[[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]
}

require_no_line_breaks() {
	local value="$1"
	local label="$2"
	! value_has_line_break "$value" || fail "$label cannot contain newline characters"
}

require_absolute_data_dir() {
	require_no_line_breaks "$DATA_DIR" "--data-dir"
	case "$DATA_DIR" in
		/*) ;;
		*) fail "--data-dir must be an absolute path because Docker Compose does not expand relative volume roots: $DATA_DIR" ;;
	esac
	! path_has_parent_segment "$DATA_DIR" || fail "--data-dir cannot contain parent directory segments: $DATA_DIR"
	require_no_symlink_components "$DATA_DIR" "--data-dir"
}

require_safe_data_dir() {
	local data_dir
	require_absolute_data_dir
	data_dir="$(normalize_compare_path "$DATA_DIR")"
	case "$data_dir" in
		/|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
			fail "--data-dir points at a protected system directory and will not be chmodded: $DATA_DIR"
			;;
	esac
}

require_safe_env_path() {
	[[ -n "$ENV_PATH" ]] || fail "--env cannot be empty"
	require_no_line_breaks "$ENV_PATH" "--env"
	! path_has_parent_segment "$ENV_PATH" || fail "--env cannot contain parent directory segments: $ENV_PATH"
	[[ ! -d "$ENV_PATH" ]] || fail "--env must point at a file, not a directory: $ENV_PATH"
	[[ ! -L "$ENV_PATH" ]] || fail "--env must not be a symlink: $ENV_PATH"
	require_no_symlink_components "$ENV_PATH" "--env"

	if [[ "$ENV_PATH" == /* ]]; then
		local env_path
		env_path="$(normalize_compare_path "$ENV_PATH")"
		case "$env_path" in
			/|/bin|/boot|/dev|/etc|/etc/*|/lib|/lib/*|/lib64|/lib64/*|/proc|/proc/*|/root|/root/*|/run|/run/*|/sbin|/sys|/sys/*|/usr|/usr/*|/var|/var/lib/*|/var/log/*|/var/run/*)
				fail "--env points at a protected system path and will not be overwritten: $ENV_PATH"
				;;
		esac
	fi
}

env_value() {
	local key="$1"
	local file="$2"

	[[ -f "$file" ]] || return 0
	awk -F= -v key="$key" '
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

env_assignment_value() {
	local value="$1"

	if [[ -z "$value" || "$value" == *[[:space:]#\"\\]* || "$value" == *"'"* ]]; then
		value="${value//\\/\\\\}"
		value="${value//\"/\\\"}"
		printf '"%s"\n' "$value"
	else
		printf '%s\n' "$value"
	fi
}

write_env_value() {
	local key="$1"
	local value
	local file="$3"
	local tmp

	require_no_line_breaks "$2" "$key"
	value="$(env_assignment_value "$2")"
	tmp="$(mktemp -t mnemonas-env.XXXXXX)"
	awk -v key="$key" -v value="$value" '
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
		BEGIN { done = 0 }
		{
			line = $0
			match_line = strip_comment(line)
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

require_safe_env_path
apply_existing_env_defaults
DATA_DIR="${DATA_DIR:-$HOME/.mnemonas}"
HOST_PORT="${HOST_PORT:-8080}"
require_port "$HOST_PORT"
require_safe_data_dir
prepare_data_dir
prepare_env
run_preflight
if [[ "$START_AFTER_PREPARE" == "1" ]]; then
	start_compose
fi
print_next_steps
