#!/usr/bin/env bash

set -u

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
COMPOSE_FILE="${COMPOSE_FILE:-$REPO_ROOT/docker-compose.yml}"
ENV_PATH="${ENV_PATH:-$REPO_ROOT/.env}"
DATA_DIR_SET="${DATA_DIR+x}"
if [[ -n "${MNEMONAS_DATA_DIR:-}" ]]; then
	DATA_DIR_SET=1
fi
HOST_PORT_SET="${HOST_PORT+x}"
DATA_DIR="${DATA_DIR:-${MNEMONAS_DATA_DIR:-$HOME/.mnemonas}}"
HOST_PORT="${HOST_PORT:-${MNEMONAS_HTTP_PORT:-}}"
MIN_FREE_BYTES="${MIN_FREE_BYTES:-10737418240}"

FAILURES=0
WARNINGS=0
DOCKER_CLI_AVAILABLE=0
COMPOSE_AVAILABLE=0
HOST_PORT_VALID=0

ok() {
	printf '[OK] %s\n' "$*"
}

warn() {
	WARNINGS=$((WARNINGS + 1))
	printf '[WARN] %s\n' "$*"
}

fail() {
	FAILURES=$((FAILURES + 1))
	printf '[FAIL] %s\n' "$*"
}

have() {
	command -v "$1" >/dev/null 2>&1
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

toml_value() {
	local section="$1"
	local key="$2"
	local file="$3"

	[[ -f "$file" ]] || return 0
	if command -v python3 >/dev/null 2>&1; then
		local value
		if value=$(python3 - "$file" "$section" "$key" <<'PY'
import sys

try:
    import tomllib
except Exception:
    sys.exit(2)

path, section, key = sys.argv[1], sys.argv[2], sys.argv[3]
try:
    with open(path, "rb") as handle:
        data = tomllib.load(handle)
except Exception:
    sys.exit(2)

current = data
for part in section.split("."):
    if not isinstance(current, dict):
        sys.exit(0)
    current = current.get(part)
    if current is None:
        sys.exit(0)

if not isinstance(current, dict) or key not in current:
    sys.exit(0)

value = current[key]
if isinstance(value, bool):
    sys.stdout.write("true" if value else "false")
elif isinstance(value, (str, int, float)):
    sys.stdout.write(str(value))
elif hasattr(value, "isoformat"):
    sys.stdout.write(value.isoformat())
PY
		); then
			printf '%s' "$value"
			return 0
		fi
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
		section_line == section {
			in_section = 1
			next
		}
		section_line ~ /^\[/ {
			in_section = 0
		}
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
	' "$file"
}

existing_path_for_df() {
	local path="$1"

	while [[ ! -e "$path" && "$path" != "/" ]]; do
		path="$(dirname "$path")"
	done
	if [[ -e "$path" ]]; then
		printf '%s\n' "$path"
	else
		printf '/\n'
	fi
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

is_protected_data_dir() {
	local path
	path="$(normalize_compare_path "$1")"
	case "$path" in
		/|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
			return 0
			;;
	esac
	return 1
}

check_no_symlink_components() {
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
			fail "$label must not contain symlink path components: $current"
			return 1
		fi
		[[ -e "$current" ]] || break
	done
}

human_bytes() {
	local bytes="$1"
	awk -v bytes="$bytes" '
		BEGIN {
			split("B KiB MiB GiB TiB", units, " ")
			value = bytes
			unit = 1
			while (value >= 1024 && unit < 5) {
				value = value / 1024
				unit++
			}
			if (unit == 1) {
				printf "%.0f %s", value, units[unit]
			} else {
				printf "%.1f %s", value, units[unit]
			}
		}
	'
}

check_file() {
	local path="$1"
	local label="$2"

	if [[ -f "$path" ]]; then
		ok "$label exists: $path"
	else
		fail "$label missing: $path"
	fi
}

check_docker() {
	if ! have docker; then
		fail "Docker CLI is not installed. Install Docker Engine before using Docker Compose."
		return
	fi
	DOCKER_CLI_AVAILABLE=1

	local docker_version
	docker_version="$(docker --version 2>/dev/null || true)"
	ok "Docker CLI available${docker_version:+: $docker_version}"

	if docker info >/dev/null 2>&1; then
		ok "Docker daemon is reachable"
	else
		fail "Docker daemon is not reachable. Start Docker or add this user to the docker group."
	fi

	if docker compose version >/dev/null 2>&1; then
		local compose_version
		COMPOSE_AVAILABLE=1
		compose_version="$(docker compose version 2>/dev/null || true)"
		ok "Docker Compose v2 available${compose_version:+: $compose_version}"
	else
		fail "Docker Compose v2 plugin is missing. On Ubuntu try: sudo apt install docker-compose-v2; with Docker's apt repo use: sudo apt install docker-compose-plugin docker-buildx-plugin"
	fi

	local image
	image="$(env_value MNEMONAS_IMAGE "$ENV_PATH")"
	if docker buildx version >/dev/null 2>&1; then
		ok "Docker Buildx plugin available"
	elif [[ -n "$image" && "$image" != "mnemonas:local" ]]; then
		ok "Docker Buildx plugin is not required for release image: $image"
	else
		warn "Docker Buildx plugin is missing. Builds still may work, but BuildKit caching and modern Docker workflows are less reliable."
	fi

	if have docker-compose; then
		warn "Legacy docker-compose command is installed. Use 'docker compose' v2 for this project."
	fi
}

check_env() {
	local current_uid current_gid
	current_uid="$(id -u)"
	current_gid="$(id -g)"
	MNEMONAS_UID_VALUE="$(env_value MNEMONAS_UID "$ENV_PATH")"
	MNEMONAS_GID_VALUE="$(env_value MNEMONAS_GID "$ENV_PATH")"
	MNEMONAS_HTTP_PORT_VALUE="$(env_value MNEMONAS_HTTP_PORT "$ENV_PATH")"

	if [[ -f "$ENV_PATH" ]]; then
		ok "Compose env file exists: $ENV_PATH"
	else
		warn "Compose env file is missing: $ENV_PATH. Copy .env.example to .env and set MNEMONAS_UID/MNEMONAS_GID before starting."
	fi

	if [[ -z "$HOST_PORT" ]]; then
		HOST_PORT="${MNEMONAS_HTTP_PORT_VALUE:-8080}"
	fi
	if [[ "$HOST_PORT" =~ ^[0-9]{1,5}$ ]] && (( 10#$HOST_PORT >= 1 && 10#$HOST_PORT <= 65535 )); then
		HOST_PORT_VALID=1
		ok "Host HTTP port configured: $HOST_PORT"
	else
		fail "Host HTTP port must be a number from 1 to 65535, got: ${HOST_PORT:-<empty>}"
	fi

	if [[ -z "$MNEMONAS_UID_VALUE" || -z "$MNEMONAS_GID_VALUE" ]]; then
		warn "MNEMONAS_UID/MNEMONAS_GID are not fully set; Compose will default to 1000:1000."
		return
	fi
	if [[ ! "$MNEMONAS_UID_VALUE" =~ ^[0-9]+$ || ! "$MNEMONAS_GID_VALUE" =~ ^[0-9]+$ ]]; then
		fail "MNEMONAS_UID/MNEMONAS_GID must be numeric, got $MNEMONAS_UID_VALUE:$MNEMONAS_GID_VALUE."
		return
	fi
	if [[ "$MNEMONAS_UID_VALUE" == "0" || "$MNEMONAS_GID_VALUE" == "0" ]]; then
		warn "Container is configured to run as root ($MNEMONAS_UID_VALUE:$MNEMONAS_GID_VALUE). Prefer your normal host UID/GID."
	fi
	if [[ "$MNEMONAS_UID_VALUE" == "$current_uid" && "$MNEMONAS_GID_VALUE" == "$current_gid" ]]; then
		ok "Container UID/GID match current user: $MNEMONAS_UID_VALUE:$MNEMONAS_GID_VALUE"
	else
		warn "Container UID/GID are $MNEMONAS_UID_VALUE:$MNEMONAS_GID_VALUE, current user is $current_uid:$current_gid. Make sure $DATA_DIR is writable by the configured numeric user."
	fi
}

image_tag_part() {
	local image_no_digest="${1%@*}"
	local last_component="${image_no_digest##*/}"

	if [[ "$last_component" == *:* ]]; then
		printf '%s\n' "${last_component##*:}"
	fi
}

check_image_reference() {
	local image tag
	image="$(env_value MNEMONAS_IMAGE "$ENV_PATH")"

	[[ -n "$image" && "$image" != "mnemonas:local" ]] || return
	if [[ "$image" == *@sha256:* ]]; then
		ok "Release image is pinned by digest: $image"
		return
	fi

	tag="$(image_tag_part "$image")"
	if [[ -z "$tag" ]]; then
		warn "MNEMONAS_IMAGE has no explicit tag or digest: $image. Use a version tag or digest before upgrading so rollback can return to a known image."
	elif [[ "$tag" == "latest" ]]; then
		warn "MNEMONAS_IMAGE uses the moving 'latest' tag. Use a version tag or digest for upgrade and rollback."
	else
		ok "Release image tag is pinned: $image"
	fi
}

check_numeric_config() {
	if [[ ! "$MIN_FREE_BYTES" =~ ^[0-9]+$ ]] || (( 10#$MIN_FREE_BYTES <= 0 )); then
		fail "MIN_FREE_BYTES must be a positive integer, got: ${MIN_FREE_BYTES:-<empty>}"
		MIN_FREE_BYTES=10737418240
	fi
}

mode_digit_has_write() {
	local digit="$1"
	(( (10#$digit & 2) != 0 ))
}

check_data_dir_writable_by_configured_user() {
	local path="$1"
	local uid="$2"
	local gid="$3"

	[[ -d "$path" ]] || return
	[[ -n "$uid" && -n "$gid" ]] || return
	[[ "$uid" =~ ^[0-9]+$ && "$gid" =~ ^[0-9]+$ ]] || return
	if ! have stat; then
		warn "Cannot inspect data directory ownership because stat is unavailable."
		return
	fi

	local stat_out owner group mode mode_tail owner_perm group_perm other_perm
	stat_out="$(stat -c '%u %g %a' "$path" 2>/dev/null || true)"
	[[ -n "$stat_out" ]] || return
	read -r owner group mode <<< "$stat_out"
	mode_tail="${mode: -3}"
	owner_perm="${mode_tail:0:1}"
	group_perm="${mode_tail:1:1}"
	other_perm="${mode_tail:2:1}"

	if [[ "$uid" == "$owner" ]] && mode_digit_has_write "$owner_perm"; then
		ok "Data directory is writable by configured owner UID $uid"
	elif [[ "$gid" == "$group" ]] && mode_digit_has_write "$group_perm"; then
		ok "Data directory is writable by configured group GID $gid"
	elif mode_digit_has_write "$other_perm"; then
		warn "Data directory is writable through other-user permissions (mode $mode). Prefer chown/chmod instead of world-writable storage."
	else
		fail "Data directory may not be writable by container UID/GID $uid:$gid: $path is owned by $owner:$group with mode $mode."
	fi

	if [[ "$other_perm" == "0" ]]; then
		ok "Data directory blocks other-user access"
	else
		warn "Data directory allows other-user access (mode $mode). Consider: chmod o-rwx '$path'"
	fi
}

check_private_existing_dir() {
	local path="$1"
	local label="$2"

	[[ -e "$path" || -L "$path" ]] || return
	if [[ -L "$path" ]]; then
		fail "$label must not be a symlink: $path"
		return
	fi
	if [[ ! -d "$path" ]]; then
		fail "$label must be a directory: $path"
		return
	fi
	if ! have stat; then
		warn "Cannot inspect $label permissions because stat is unavailable: $path"
		return
	fi

	local mode mode_tail group_perm other_perm
	mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
	[[ -n "$mode" ]] || return
	mode_tail="${mode: -3}"
	group_perm="${mode_tail:1:1}"
	other_perm="${mode_tail:2:1}"
	if [[ "$group_perm" == "0" && "$other_perm" == "0" ]]; then
		ok "$label is private to its owner: $path"
	else
		warn "$label allows group or other access (mode $mode). Consider: chmod 700 '$path'"
	fi
}

check_private_existing_file() {
	local path="$1"
	local label="$2"

	[[ -e "$path" || -L "$path" ]] || return
	if [[ -L "$path" ]]; then
		fail "$label must not be a symlink: $path"
		return
	fi
	if [[ ! -f "$path" ]]; then
		fail "$label must be a regular file: $path"
		return
	fi
	if ! have stat; then
		warn "Cannot inspect $label permissions because stat is unavailable: $path"
		return
	fi

	local mode mode_tail group_perm other_perm
	mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
	[[ -n "$mode" ]] || return
	mode_tail="${mode: -3}"
	group_perm="${mode_tail:1:1}"
	other_perm="${mode_tail:2:1}"
	if [[ "$group_perm" == "0" && "$other_perm" == "0" ]]; then
		ok "$label is private to its owner: $path"
	else
		warn "$label allows group or other access (mode $mode). Consider: chmod 600 '$path'"
	fi
}

check_toml_parse() {
	local path="$1"
	local label="$2"
	local parse_out status

	[[ -f "$path" ]] || return 0
	if ! have python3; then
		warn "Cannot parse $label as TOML because python3 is unavailable: $path"
		return 0
	fi

	parse_out="$(mktemp -t mnemonas-toml-parse.XXXXXX)"
	python3 - "$path" >"$parse_out" 2>&1 <<'PY'
import sys

try:
    import tomllib
except Exception:
    sys.exit(2)

path = sys.argv[1]
try:
    with open(path, "rb") as handle:
        tomllib.load(handle)
except Exception as exc:
    sys.stderr.write(f"{type(exc).__name__}: {exc}\n")
    sys.exit(1)
PY
	status=$?
	if [[ "$status" -eq 0 ]]; then
		ok "$label parses as TOML: $path"
		rm -f -- "$parse_out"
		return 0
	fi
	if [[ "$status" -eq 2 ]]; then
		warn "Cannot parse $label as TOML because python3 does not provide tomllib: $path"
		rm -f -- "$parse_out"
		return 0
	fi

	fail "$label is not valid TOML: $(tr '\n' ' ' < "$parse_out")"
	rm -f -- "$parse_out"
	return 1
}

check_sensitive_files() {
	[[ -d "$DATA_DIR" ]] || return

	check_private_existing_dir "$DATA_DIR/.mnemonas" "Internal metadata directory"
	check_private_existing_file "$DATA_DIR/.mnemonas/users.json" "Users file"
	check_private_existing_file "$DATA_DIR/.mnemonas/initial-password.txt" "Initial admin password file"
	check_private_existing_file "$DATA_DIR/secrets.json" "Generated secrets file"
	check_private_existing_file "$DATA_DIR/config.toml" "Docker config file"
}

validate_data_dir_path() {
	if [[ -z "$DATA_DIR" ]]; then
		fail "Data directory cannot be empty."
		return 1
	fi
	if [[ "$DATA_DIR" == *$'\n'* || "$DATA_DIR" == *$'\r'* ]]; then
		fail "Data directory cannot contain newline characters: $DATA_DIR"
		return 1
	fi
	if [[ "$DATA_DIR" == *[[:cntrl:]]* ]]; then
		fail "Data directory cannot contain control characters: $DATA_DIR"
		return 1
	fi
	if [[ "$DATA_DIR" != /* ]]; then
		fail "Data directory must be an absolute path because Docker Compose does not expand relative volume roots: $DATA_DIR"
		return 1
	fi
	if path_has_parent_segment "$DATA_DIR"; then
		fail "Data directory cannot contain parent directory segments: $DATA_DIR"
		return 1
	fi
	if is_protected_data_dir "$DATA_DIR"; then
		fail "Data directory points at a protected system directory and must not be bind-mounted as /data: $DATA_DIR"
		return 1
	fi
	return 0
}

check_data_dir() {
	local df_target available_kb available_bytes config_path configured_root

	if ! validate_data_dir_path; then
		return
	fi
	if ! check_no_symlink_components "$DATA_DIR" "Data directory"; then
		return
	fi

	if [[ -d "$DATA_DIR" ]]; then
		ok "Data directory exists: $DATA_DIR"
	else
		fail "Data directory missing: $DATA_DIR. Create it with: mkdir -p '$DATA_DIR' && chmod 750 '$DATA_DIR'"
	fi

	if [[ -d "$DATA_DIR" && -w "$DATA_DIR" ]]; then
		ok "Data directory is writable by current user"
	elif [[ -d "$DATA_DIR" ]]; then
		warn "Data directory is not writable by current user. This is only OK if it is writable by MNEMONAS_UID/MNEMONAS_GID."
	fi

	check_data_dir_writable_by_configured_user "$DATA_DIR" "${MNEMONAS_UID_VALUE:-}" "${MNEMONAS_GID_VALUE:-}"
	check_sensitive_files

	df_target="$(existing_path_for_df "$DATA_DIR")"
	if available_kb="$(df -Pk "$df_target" 2>/dev/null | awk 'NR == 2 { print $4 }')" && [[ -n "$available_kb" ]]; then
		available_bytes=$((available_kb * 1024))
		if (( available_bytes < MIN_FREE_BYTES )); then
			warn "Low free space near $DATA_DIR: $(human_bytes "$available_bytes") available. Recommended minimum is $(human_bytes "$MIN_FREE_BYTES")."
		else
			ok "Free space near $DATA_DIR: $(human_bytes "$available_bytes") available"
		fi
	fi

	config_path="$DATA_DIR/config.toml"
	if [[ ! -e "$config_path" && ! -L "$config_path" ]]; then
		ok "No existing Docker config found; first container start will create $config_path"
		return
	fi
	if [[ -L "$config_path" || ! -f "$config_path" ]]; then
		return
	fi
	if ! check_toml_parse "$config_path" "Docker config file"; then
		return
	fi

	configured_root="$(toml_value storage root "$config_path")"
	if [[ -z "$configured_root" ]]; then
		fail "$config_path exists but does not set [storage].root. For the default Compose file set: root = \"/data\""
	elif [[ "$configured_root" != /* ]]; then
		fail "$config_path sets a relative [storage].root: $configured_root. Docker deployments must use an absolute container path; for the default Compose file set: root = \"/data\""
	elif [[ "$configured_root" == "/data" ]]; then
		ok "Existing Docker config uses [storage].root = /data"
	else
		warn "$config_path uses [storage].root = $configured_root. Ensure docker-compose.yml mounts a host directory at that same container path, or data may be written into the container layer."
	fi
}

check_port() {
	local listeners

	if [[ "$HOST_PORT_VALID" != "1" ]]; then
		return
	fi
	if ! have ss; then
		warn "Cannot check host port $HOST_PORT because ss is unavailable."
		return
	fi

	listeners="$(ss -ltnH 2>/dev/null | awk -v suffix=":$HOST_PORT" '$4 ~ suffix "$" { print $4 }')"
	if [[ -n "$listeners" ]]; then
		fail "Host port $HOST_PORT is already listening: $listeners. Stop the service or change the Compose port mapping before starting MnemoNAS."
	else
		ok "Host port $HOST_PORT is available"
	fi
}

check_compose_config() {
	local config_out

	[[ -f "$COMPOSE_FILE" ]] || return
	if [[ "$DOCKER_CLI_AVAILABLE" != "1" || "$COMPOSE_AVAILABLE" != "1" ]]; then
		return
	fi
	config_out="$(mktemp -t mnemonas-compose-config.XXXXXX)"
	if [[ -f "$ENV_PATH" ]]; then
		if docker compose -f "$COMPOSE_FILE" --env-file "$ENV_PATH" config --quiet > "$config_out" 2>&1; then
			ok "Docker Compose config renders successfully"
		else
			fail "Docker Compose config failed: $(tr '\n' ' ' < "$config_out")"
		fi
	else
		if docker compose -f "$COMPOSE_FILE" config --quiet > "$config_out" 2>&1; then
			ok "Docker Compose config renders successfully"
		else
			fail "Docker Compose config failed: $(tr '\n' ' ' < "$config_out")"
		fi
	fi
	rm -f -- "$config_out"
}

apply_env_file_defaults() {
	local env_data_dir env_host_port

	env_data_dir="$(env_value MNEMONAS_DATA_DIR "$ENV_PATH")"
	env_host_port="$(env_value MNEMONAS_HTTP_PORT "$ENV_PATH")"
	if [[ -z "$DATA_DIR_SET" && -n "$env_data_dir" ]]; then
		DATA_DIR="$env_data_dir"
	fi
	if [[ -z "$HOST_PORT_SET" && -z "${MNEMONAS_HTTP_PORT:-}" && -n "$env_host_port" ]]; then
		HOST_PORT="$env_host_port"
	fi
}

apply_env_file_defaults

printf 'MnemoNAS Docker preflight\n'
printf 'Repository: %s\n' "$REPO_ROOT"
printf 'Data dir:   %s\n' "$DATA_DIR"
printf '\n'

check_numeric_config
check_file "$COMPOSE_FILE" "Compose file"
check_docker
check_env
check_image_reference
check_data_dir
check_port
check_compose_config

printf '\nSummary: %d failure(s), %d warning(s)\n' "$FAILURES" "$WARNINGS"
if (( FAILURES > 0 )); then
	exit 1
fi
