#!/usr/bin/env bash

set -euo pipefail

CONFIG_PATH="${CONFIG_PATH:-/etc/mnemonas/config.toml}"
DATAPLANE_BIN="${DATAPLANE_BIN:-/usr/local/bin/dataplane}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-127.0.0.1:9090}"
MIN_CDC_CHUNK_SIZE=65536
MAX_CDC_CHUNK_SIZE=67108864
DEFAULT_CDC_MIN_CHUNK_SIZE=262144
DEFAULT_CDC_AVG_CHUNK_SIZE=1048576
DEFAULT_CDC_MAX_CHUNK_SIZE=4194304

fail() {
  printf '[mnemonas-dataplane-start] ERROR: %s\n' "$*" >&2
  exit 1
}

toml_value() {
  local section="$1"
  local key="$2"
  local file="$3"

  if [[ ! -f "$file" ]]; then
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
      gsub("^[[:space:]]+|[[:space:]]+$", "", line)
      section_line = line
      if (section_line ~ "^\\[") {
        sub("^\\[[[:space:]]*", "[", section_line)
        sub("[[:space:]]*\\]$", "]", section_line)
        gsub("[[:space:]]*\\.[[:space:]]*", ".", section_line)
      }
    }
    section_line == section {
      in_section = 1
      next
    }
    section_line ~ "^\\[" {
      in_section = 0
    }
    in_section && line ~ "^[[:space:]]*" key "[[:space:]]*=" {
      sub("^[[:space:]]*" key "[[:space:]]*=[[:space:]]*", "", line)
      gsub("^[[:space:]]+|[[:space:]]+$", "", line)
      gsub("^\"|\"$", "", line)
      gsub("^\047|\047$", "", line)
      print line
      exit
    }
  ' "$file"
}

expand_path() {
  local path=$1
  local home="${HOME:-}"

  case "$path" in
    "")
      [[ -n "$home" ]] || fail "HOME is required when storage.root is not configured"
      printf '%s\n' "$home/.mnemonas"
      ;;
    \~)
      [[ -n "$home" ]] || fail "HOME is required to expand ~"
      printf '%s\n' "$home"
      ;;
    \~/*)
      [[ -n "$home" ]] || fail "HOME is required to expand ~"
      printf '%s\n' "$home/${path#\~/}"
      ;;
    *)
      printf '%s\n' "$path"
      ;;
  esac
}

normalize_compare_path() {
  local path="$1"

  if command -v realpath >/dev/null 2>&1; then
    realpath -m -- "$path"
    return
  fi

  while [[ "$path" == *///* ]]; do
    path="${path//\/\//\/}"
  done
  while [[ "$path" != "/" && "$path" == */ ]]; do
    path="${path%/}"
  done
  [[ -n "$path" ]] || path="."
  printf '%s\n' "$path"
}

path_has_parent_segment() {
  local path="$1"
  local -a segments
  local segment

  IFS='/' read -r -a segments <<< "$path"
  for segment in "${segments[@]}"; do
    [[ "$segment" != ".." ]] || return 0
  done
  return 1
}

require_no_symlink_components() {
  local path="$1"
  local label="$2"
  local current="/"
  local remainder
  local segment
  local -a segments

  [[ "$path" == /* ]] || fail "$label must be an absolute path: $path"

  remainder="${path#/}"
  IFS='/' read -r -a segments <<< "$remainder"
  for segment in "${segments[@]}"; do
    [[ -n "$segment" && "$segment" != "." ]] || continue
    current="${current%/}/$segment"
    [[ ! -L "$current" ]] || fail "$label must not contain symlink path components: $current"
    [[ -e "$current" ]] || break
  done
}

is_protected_system_directory() {
  local normalized="$1"

  case "$normalized" in
    /|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/usr/local/share|/var)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

require_safe_tree_path() {
  local path="$1"
  local label="$2"
  local normalized

  [[ -n "$path" ]] || fail "$label cannot be empty"
  [[ "$path" == /* ]] || fail "$label must be an absolute path: $path"
  ! path_has_parent_segment "$path" || fail "$label cannot contain parent directory segments: $path"
  require_no_symlink_components "$path" "$label"

  normalized="$(normalize_compare_path "$path")"
  ! is_protected_system_directory "$normalized" || fail "$label points at a protected system directory: $path"
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

  [[ -n "$value" ]] || fail "$label cannot be empty"
  [[ "$value" != *$'\r'* && "$value" != *$'\n'* && "$value" != *$'\t'* && "$value" != *" "* ]] || fail "$label must not contain whitespace: $value"

  if [[ "$value" =~ ^\[([^][]+)\]:([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  elif [[ "$value" =~ ^([^:]+):([0-9]+)$ ]]; then
    host="${BASH_REMATCH[1]}"
    port="${BASH_REMATCH[2]}"
  else
    fail "$label must be a host:port address: $value"
  fi

  is_valid_tcp_host "$host" || fail "$label host is invalid: $value"
  (( 10#$port >= 1 && 10#$port <= 65535 )) || fail "$label port must be between 1 and 65535: $value"
}

append_configured_uint_arg() {
  local -n target_args=$1
  local flag=$2
  local section=$3
  local key=$4
  local value
  local normalized_value

  value="$(toml_value "$section" "$key" "$CONFIG_PATH")"
  if [[ -z "$value" ]]; then
    return 0
  fi
  if [[ ! "$value" =~ ^[0-9](_?[0-9])*$ ]]; then
    printf '[mnemonas-dataplane-start] ERROR: invalid [%s].%s value: %s\n' "$section" "$key" "$value" >&2
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
  local section="$1"
  local key="$2"
  local default_value="$3"
  local value

  value="$(toml_value "$section" "$key" "$CONFIG_PATH")"
  if [[ -z "$value" ]]; then
    printf '%s' "$default_value"
    return 0
  fi
  if [[ ! "$value" =~ ^[0-9](_?[0-9])*$ ]]; then
    printf '[mnemonas-dataplane-start] ERROR: invalid [%s].%s value: %s\n' "$section" "$key" "$value" >&2
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

  ! decimal_uint_lt "$min_chunk_size" "$MIN_CDC_CHUNK_SIZE" || fail "min_chunk_size must be at least $MIN_CDC_CHUNK_SIZE bytes"
  decimal_uint_lt "$min_chunk_size" "$avg_chunk_size" || fail "min_chunk_size must be less than avg_chunk_size"
  decimal_uint_lt "$avg_chunk_size" "$max_chunk_size" || fail "avg_chunk_size must be less than max_chunk_size"
  ! decimal_uint_gt "$max_chunk_size" "$MAX_CDC_CHUNK_SIZE" || fail "max_chunk_size must be at most $MAX_CDC_CHUNK_SIZE bytes"
}

[[ "$CONFIG_PATH" == /* ]] || fail "CONFIG_PATH must be an absolute path: $CONFIG_PATH"
! path_has_parent_segment "$CONFIG_PATH" || fail "CONFIG_PATH cannot contain parent directory segments: $CONFIG_PATH"
require_no_symlink_components "$CONFIG_PATH" "CONFIG_PATH"

storage_root="${STORAGE_ROOT:-$(toml_value storage root "$CONFIG_PATH")}"
storage_root="$(expand_path "$storage_root")"
dataplane_data_dir="${DATAPLANE_DATA_DIR:-$storage_root/.mnemonas/objects}"
require_safe_tree_path "$storage_root" "storage.root"
require_safe_tree_path "$dataplane_data_dir" "DATAPLANE_DATA_DIR"
configured_grpc_addr="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
if [[ -n "$configured_grpc_addr" ]]; then
  DATAPLANE_GRPC_ADDR="$configured_grpc_addr"
fi
require_safe_tcp_addr "$DATAPLANE_HTTP_ADDR" "DATAPLANE_HTTP_ADDR"
require_safe_tcp_addr "$DATAPLANE_GRPC_ADDR" "DATAPLANE_GRPC_ADDR"

dataplane_args=(
  "$DATAPLANE_BIN"
  --listen "$DATAPLANE_HTTP_ADDR"
  --grpc "$DATAPLANE_GRPC_ADDR"
  --data-dir "$dataplane_data_dir"
)
validate_cdc_chunk_sizes
append_configured_uint_arg dataplane_args --min-chunk-size dataplane.cdc min_chunk_size
append_configured_uint_arg dataplane_args --avg-chunk-size dataplane.cdc avg_chunk_size
append_configured_uint_arg dataplane_args --max-chunk-size dataplane.cdc max_chunk_size

exec "${dataplane_args[@]}"
