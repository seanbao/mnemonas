#!/usr/bin/env bash
# shellcheck disable=SC2317

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

E2E_ROOT="${MNEMONAS_E2E_ROOT:-/tmp/mnemonas-playwright}"
BACKEND_ROOT="$E2E_ROOT/backend"
STORAGE_ROOT="$BACKEND_ROOT/storage"
CONFIG_FILE="$BACKEND_ROOT/config.toml"
LOG_DIR="$BACKEND_ROOT/logs"
E2E_PASSWORD_FILE="$BACKEND_ROOT/e2e-password.txt"
PUBLIC_SHARE_ID_FILE="$BACKEND_ROOT/public-share-id.txt"
PROTECTED_SHARE_ID_FILE="$BACKEND_ROOT/protected-share-id.txt"
PROTECTED_SHARE_PASSWORD_FILE="$BACKEND_ROOT/protected-share-password.txt"
DISABLED_SHARE_ID_FILE="$BACKEND_ROOT/disabled-share-id.txt"
FOLDER_SHARE_ID_FILE="$BACKEND_ROOT/folder-share-id.txt"

DATAPLANE_HTTP="${MNEMONAS_E2E_DATAPLANE_HTTP:-127.0.0.1:19091}"
DATAPLANE_GRPC="${MNEMONAS_E2E_DATAPLANE_GRPC:-127.0.0.1:19090}"
NASD_HOST="${MNEMONAS_E2E_NASD_HOST:-127.0.0.1}"
NASD_PORT="${MNEMONAS_E2E_NASD_PORT:-18080}"

export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

cleanup() {
  if [[ -n "${nasd_pid:-}" ]]; then
    kill "$nasd_pid" 2>/dev/null || true
  fi
  if [[ -n "${dataplane_pid:-}" ]]; then
    kill "$dataplane_pid" 2>/dev/null || true
  fi
}

extract_json_field() {
  local json="$1"
  local field="$2"
  printf '%s' "$json" | sed -n "s/.*\"${field}\":\"\([^\"]*\)\".*/\1/p"
}

seed_e2e_fixtures() {
  local login_response token share_response share_id protected_share_id disabled_share_id folder_share_id
  local protected_share_password="playwright-secret"

  if [[ ! -f "$E2E_PASSWORD_FILE" ]]; then
    echo "missing E2E password file: $E2E_PASSWORD_FILE" >&2
    return 1
  fi

  local password
  password=$(sed -n 's/^Password:\s*//p' "$E2E_PASSWORD_FILE" | head -n1)
  if [[ -z "$password" ]]; then
    echo "failed to parse E2E password file: $E2E_PASSWORD_FILE" >&2
    return 1
  fi

  login_response=$(curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"${password}\"}")
  token=$(extract_json_field "$login_response" 'access_token')
  if [[ -z "$token" ]]; then
    echo "failed to retrieve E2E auth token" >&2
    return 1
  fi

  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-trash-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for trash e2e' >/dev/null

  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for public share e2e' >/dev/null

  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-protected-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for protected public share e2e' >/dev/null

  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-disabled-share-fixture.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for disabled public share e2e' >/dev/null

  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/directories/e2e-folder-share" \
    -H "Authorization: Bearer $token" >/dev/null
  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/directories/e2e-folder-share/subdir" \
    -H "Authorization: Bearer $token" >/dev/null
  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-folder-share/root-note.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for shared folder root file' >/dev/null
  curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-folder-share/subdir/nested-note.txt" \
    -H "Authorization: Bearer $token" \
    --data-binary 'fixture for shared folder nested file' >/dev/null

  share_response=$(curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-share-fixture.txt","type":"file","permission":"read","description":"playwright public share fixture"}')
  share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$share_id" ]]; then
    echo "failed to create public share fixture" >&2
    return 1
  fi
  printf '%s\n' "$share_id" > "$PUBLIC_SHARE_ID_FILE"

  share_response=$(curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d "{\"path\":\"/e2e-protected-share-fixture.txt\",\"type\":\"file\",\"permission\":\"read\",\"password\":\"${protected_share_password}\",\"description\":\"playwright protected public share fixture\"}")
  protected_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$protected_share_id" ]]; then
    echo "failed to create protected public share fixture" >&2
    return 1
  fi
  printf '%s\n' "$protected_share_id" > "$PROTECTED_SHARE_ID_FILE"
  printf '%s\n' "$protected_share_password" > "$PROTECTED_SHARE_PASSWORD_FILE"

  share_response=$(curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-disabled-share-fixture.txt","type":"file","permission":"read","description":"playwright disabled public share fixture"}')
  disabled_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$disabled_share_id" ]]; then
    echo "failed to create disabled public share fixture" >&2
    return 1
  fi

  curl -sf -X PUT "http://${NASD_HOST}:${NASD_PORT}/api/v1/shares/${disabled_share_id}" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"enabled":false}' >/dev/null
  printf '%s\n' "$disabled_share_id" > "$DISABLED_SHARE_ID_FILE"

  share_response=$(curl -sf -X POST "http://${NASD_HOST}:${NASD_PORT}/api/v1/shares" \
    -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"path":"/e2e-folder-share","type":"folder","permission":"read","description":"playwright public folder share fixture"}')
  folder_share_id=$(extract_json_field "$share_response" 'id')
  if [[ -z "$folder_share_id" ]]; then
    echo "failed to create public folder share fixture" >&2
    return 1
  fi
  printf '%s\n' "$folder_share_id" > "$FOLDER_SHARE_ID_FILE"

  curl -sf -X DELETE "http://${NASD_HOST}:${NASD_PORT}/api/v1/files/e2e-trash-fixture.txt" \
    -H "Authorization: Bearer $token" >/dev/null
}

wait_for_url() {
  local url="$1"
  local attempts="${2:-120}"

  for _ in $(seq 1 "$attempts"); do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done

  return 1
}

trap cleanup EXIT INT TERM

rm -rf "$BACKEND_ROOT"
mkdir -p "$STORAGE_ROOT" "$LOG_DIR"

cat > "$CONFIG_FILE" <<EOF
[server]
host = "$NASD_HOST"
port = $NASD_PORT
read_timeout = "30s"
write_timeout = "60s"
idle_timeout = "120s"

[server.tls]
enabled = false
auto_generate = false

[storage]
root = "$STORAGE_ROOT"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 0
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go", ".rs", ".toml", ".yaml", ".json"]
auto_versioned_filenames = ["README", "LICENSE", "CHANGELOG", "Dockerfile", "Makefile"]
max_versioned_size = 104857600

[storage.trash]
enabled = true
retention_days = 30
max_size = 1073741824

[dataplane]
grpc_address = "$DATAPLANE_GRPC"
timeout = "30s"
max_retries = 3

[dataplane.cdc]
min_chunk_size = 262144
avg_chunk_size = 1048576
max_chunk_size = 4194304

[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "admin"
password = ""

[auth]
enabled = true
jwt_secret = ""
access_token_ttl = "15m"
refresh_token_ttl = "168h"
users_file = ""

[share]
enabled = true
store_file = ""
base_url = ""

[favorites]
enabled = true
store_file = ""

[alerts]
enabled = false
check_interval = "1h"
threshold_pct = 90.0
critical_pct = 95.0
min_free_bytes = 0
cooldown_period = "4h"
webhook_url = ""
webhook_method = "POST"

[log]
level = "warn"
format = "console"
output = "stdout"
time_format = "RFC3339"
EOF

(
  cd "$PROJECT_ROOT/dataplane"
  cargo run --quiet -- --listen "$DATAPLANE_HTTP" --grpc "$DATAPLANE_GRPC" --data-dir "$STORAGE_ROOT/.mnemonas/objects" > "$LOG_DIR/dataplane.log" 2>&1
) &
dataplane_pid=$!

if ! wait_for_url "http://${DATAPLANE_HTTP}/health"; then
  echo "dataplane failed to start; see $LOG_DIR/dataplane.log" >&2
  exit 1
fi

(
  cd "$PROJECT_ROOT"
  CGO_ENABLED=1 go run ./cmd/nasd --config "$CONFIG_FILE" > "$LOG_DIR/nasd.log" 2>&1
) &
nasd_pid=$!

if ! wait_for_url "http://${NASD_HOST}:${NASD_PORT}/health"; then
  echo "nasd failed to start; see $LOG_DIR/nasd.log" >&2
  exit 1
fi

if [[ -f "$STORAGE_ROOT/.mnemonas/initial-password.txt" ]]; then
  cp "$STORAGE_ROOT/.mnemonas/initial-password.txt" "$E2E_PASSWORD_FILE"
fi

if ! seed_e2e_fixtures; then
  echo "failed to seed E2E fixtures; see $LOG_DIR/nasd.log" >&2
  exit 1
fi

echo "MnemoNAS Playwright backend ready at http://${NASD_HOST}:${NASD_PORT}"

wait -n "$dataplane_pid" "$nasd_pid"
exit $?
