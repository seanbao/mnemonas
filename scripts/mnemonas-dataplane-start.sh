#!/usr/bin/env bash

set -euo pipefail

CONFIG_PATH="${CONFIG_PATH:-/etc/mnemonas/config.toml}"
DATAPLANE_BIN="${DATAPLANE_BIN:-/usr/local/bin/dataplane}"
DATAPLANE_HTTP_ADDR="${DATAPLANE_HTTP_ADDR:-127.0.0.1:9091}"
DATAPLANE_GRPC_ADDR="${DATAPLANE_GRPC_ADDR:-127.0.0.1:9090}"

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

  case "$path" in
    "")
      printf '%s\n' "$HOME/.mnemonas"
      ;;
    \~)
      printf '%s\n' "$HOME"
      ;;
    \~/*)
      printf '%s\n' "$HOME/${path#\~/}"
      ;;
    *)
      printf '%s\n' "$path"
      ;;
  esac
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

storage_root="${STORAGE_ROOT:-$(toml_value storage root "$CONFIG_PATH")}"
storage_root="$(expand_path "$storage_root")"
dataplane_data_dir="${DATAPLANE_DATA_DIR:-$storage_root/.mnemonas/objects}"
configured_grpc_addr="$(toml_value dataplane grpc_address "$CONFIG_PATH")"
if [[ -n "$configured_grpc_addr" ]]; then
  DATAPLANE_GRPC_ADDR="$configured_grpc_addr"
fi

dataplane_args=(
  "$DATAPLANE_BIN"
  --listen "$DATAPLANE_HTTP_ADDR"
  --grpc "$DATAPLANE_GRPC_ADDR"
  --data-dir "$dataplane_data_dir"
)
append_configured_uint_arg dataplane_args --min-chunk-size dataplane.cdc min_chunk_size
append_configured_uint_arg dataplane_args --avg-chunk-size dataplane.cdc avg_chunk_size
append_configured_uint_arg dataplane_args --max-chunk-size dataplane.cdc max_chunk_size

exec "${dataplane_args[@]}"
