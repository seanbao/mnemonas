# MnemoNAS Configuration Reference

English | [简体中文](configuration.md)

MnemoNAS uses TOML configuration. This reference covers config discovery, validation, complete examples, and all main config sections.

## Config File Locations

`nasd` looks for configuration in this order:

1. `nasd --config /path/to/config.toml`
2. `$HOME/.mnemonas/config.toml`

If no file is found, defaults are used.

The Ubuntu/systemd installer writes `/etc/mnemonas/config.toml` and points the systemd unit to it with `--config`.

Config files can contain sensitive values such as `auth.jwt_secret`, WebDAV passwords, alert webhook headers, and Telegram bot tokens. MnemoNAS saves config files with `0600` permissions and tightens existing config files when they are loaded.

## Validate Configuration

After editing config:

```bash
nasd --check-config --config /etc/mnemonas/config.toml
```

This validates TOML, ports, durations, paths, and other hard errors. Disabling `auth.enabled` or using `webdav.auth_type = "none"` while the HTTP server listens beyond loopback is rejected by default; set `security.allow_unsafe_no_auth = true` only when an outer network boundary deliberately restricts access. Dataplane gRPC bound externally still prints a deployable but risky warning.

Treat `warning:` lines as pre-deployment checks for long-running systems.

## Complete Example

```toml
[server]
host = "0.0.0.0"
port = 8080
read_timeout = "30s"
write_timeout = "60s"
idle_timeout = "120s"
trusted_proxy_hops = 0
trusted_proxy_cidrs = []

[server.tls]
enabled = false
cert_file = ""
key_file = ""
auto_generate = true
cert_dir = ""

[storage]
root = "~/.mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 10737418240
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [
  ".md", ".txt", ".org", ".rst", ".tex",
  ".go", ".rs", ".py", ".ts", ".js", ".tsx", ".jsx",
  ".c", ".cpp", ".h", ".java", ".kt", ".swift",
  ".toml", ".yaml", ".yml", ".json", ".xml",
  ".sh", ".bash", ".zsh", ".fish",
]
auto_versioned_filenames = [
  "Makefile", "Dockerfile", "Vagrantfile",
  "LICENSE", "README", "CHANGELOG",
  ".gitignore", ".dockerignore", ".editorconfig",
]
max_versioned_size = 104857600

[storage.trash]
enabled = true
retention_days = 30
max_size = 10737418240

[dataplane]
grpc_address = "127.0.0.1:9090"
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

[smb]
enabled = false
listen = "127.0.0.1:1445"
server_name = "mnemonas"
gateway_socket = ""
credential_file = ""
signing_required = true
encryption_required = false

# [[smb.shares]]
# name = "homes"
# path = "/"
# read_only = false
# allowed_roles = ["admin", "user"]
# allowed_users = []

[backup]
# [[backup.jobs]]
# id = "external-disk"
# name = "External disk backup"
# type = "local"
# source = ""
# destination = "/mnt/backup-drive/mnemonas"
# schedule_interval = "24h"
# schedule_window_start = "02:00"
# schedule_window_end = "05:00"
# stale_after = "72h"
# restore_drill_stale_after = "720h"
# max_snapshots = 7
# max_age = "720h"
# include_config = true
# verify_after_backup = true

[auth]
enabled = true
jwt_secret = ""
access_token_ttl = "15m"
refresh_token_ttl = "168h"
users_file = ""

[share]
enabled = false
store_file = ""
base_url = ""
default_expires_in = "168h"
default_max_access = 0

# [[share.policy_rules]]
# path = "/Family"
# require_password = true
# max_expires_in = "24h"
# max_access = 20

[favorites]
enabled = true
store_file = ""

[alerts]
enabled = false
check_interval = "1h"
threshold_pct = 90.0
critical_pct = 95.0
min_free_bytes = 10737418240
cooldown_period = "4h"
webhook_url = ""
webhook_method = "POST"
webhook_headers = []
telegram_enabled = false
telegram_bot_token = ""
telegram_chat_id = ""
email_enabled = false
smtp_host = ""
smtp_port = 587
smtp_username = ""
smtp_password = ""
smtp_from = ""
smtp_to = []

[disk_health]
enabled = false
check_interval = "1h"
probe_timeout = "15s"
cooldown_period = "4h"
command = "smartctl"
temperature_warning_c = 50
temperature_critical_c = 60
media_wear_warning_percent = 80
media_wear_critical_percent = 100

# [[disk_health.devices]]
# name = "data-disk"
# path = "/dev/disk/by-id/ata-example"
# type = "sat"
# serial = ""

[maintenance.scrub]
enabled = false
schedule_interval = "168h"
retry_interval = "1h"
max_retries = 1

[security]
allow_unsafe_no_auth = false

[log]
level = "info"
format = "console"
output = "stdout"
time_format = "RFC3339"
```

## `[server]`

Controls the main HTTP server for Web UI, REST API, and WebDAV.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `host` | string | `"0.0.0.0"` | Listen host; must be empty, `*`, a valid hostname, IPv4, or IPv6 literal, without a port, whitespace, or control characters. Use `127.0.0.1` or `::1` for local-only |
| `port` | int | `8080` | HTTP port |
| `read_timeout` | duration | `"30s"` | Request-read timeout |
| `write_timeout` | duration | `"60s"` | Response-write timeout |
| `idle_timeout` | duration | `"120s"` | Keep-alive idle timeout |
| `trusted_proxy_hops` | int | `0` | Number of trusted reverse proxy hops used to interpret forwarded headers |
| `trusted_proxy_cidrs` | string[] | `[]` | Direct-peer IP addresses or CIDRs for trusted reverse proxies; loopback peers are always trusted |

Example:

```toml
[server]
host = "127.0.0.1"
port = 8443
read_timeout = "60s"
write_timeout = "120s"
trusted_proxy_hops = 1
trusted_proxy_cidrs = ["10.0.0.0/8"]
```

`trusted_proxy_hops = 0` ignores client-supplied forwarded headers. Set it only when MnemoNAS is behind trusted proxies. Direct peers from `127.0.0.1` or `::1` are trusted automatically. Proxies reached through Docker bridge networks, internal load balancers, or other non-loopback addresses must be listed in `trusted_proxy_cidrs`.

`server.host` contains only the listen host; put the port in `server.port`. IPv6 may be written as `::1` or `[::1]`, and the runtime normalizes it for `net.JoinHostPort`. `*` and an empty string both mean wildcard listen.

## `[server.tls]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable built-in HTTPS |
| `cert_file` | string | `""` | Certificate path |
| `key_file` | string | `""` | Private key path |
| `auto_generate` | bool | `true` | Generate a self-signed cert when paths are empty |
| `cert_dir` | string | `<storage.root>/.mnemonas/certs` | Generated-cert directory |

Set `cert_file` and `key_file` together or leave both empty; when set, they must point to different files. When both are empty, MnemoNAS uses `cert_dir/server.crt` and `cert_dir/server.key`. If `auto_generate = false`, those files must already exist.

For public deployments, a reverse proxy such as Caddy or Nginx is usually preferred.

## `[storage]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `root` | string | `~/.mnemonas` | Storage root; user files live under `root/files` |
| `directory_quotas` | array | `[]` | Directory quota entries with `path` and `quota_bytes` |
| `directory_access_rules` | array | `[]` | Directory read/write grants for users, groups, and roles |

Rules:

- `root` must not be `/`.
- Startup tightens permissions on `root`, `files`, and internal directories.
- Move the full storage root when migrating data.
- The systemd-installed `mnemonas-dataplane-start` helper rejects `storage.root` and `DATAPLANE_DATA_DIR` values with line breaks, parent-directory segments, symlink path components, or protected system directories before starting the dataplane.
- `directory_quotas` use MnemoNAS logical paths such as `/team`. Uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations check current logical bytes before writing. Use `/` for a global hard limit. Admins can view current usage, remaining bytes, and status for each directory quota on the storage page.
- `directory_access_rules` use clean absolute MnemoNAS paths such as `/team`. Each rule can grant `read_users`, `write_users`, `read_groups`, `write_groups`, `read_roles`, and `write_roles`. The most specific matching rule wins. Write grants also allow reads; write operations require an explicit write grant. Non-admin Web/API, WebDAV `users` mode, search, shares, favorites, trash, and activity views use the same decision path. Paths without a matching rule fall back to the user's `home_dir` boundary. Web/API root listings return only the user's `home_dir` and top-level entries for readable shared directories. When only a nested directory is granted, Web/API and WebDAV may expose existing ancestor directories as read-only navigation entries; direct children remain filtered by their own rules, and writes under those ancestors still require explicit write grants.

Example:

```toml
[storage]
root = "~/.mnemonas"
directory_quotas = [
  { path = "/team", quota_bytes = 1099511627776 }, # 1 TiB
]
directory_access_rules = [
  { path = "/team", read_groups = ["family"], write_groups = ["editors"] },
  { path = "/team/public", read_roles = ["user"], write_users = ["alice"] },
]
```

## `[storage.retention]`

Controls version cleanup.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `max_versions` | int | `50` | Maximum versions per file; `0` means unlimited |
| `max_age` | duration | `"2160h"` | Maximum version age; `0` keeps forever |
| `min_free_space` | uint64 | `10737418240` | Minimum free bytes before forced cleanup |
| `gc_interval` | duration | `"24h"` | Automatic cleanup interval; `"0"` disables it |

Priority:

1. The newest version is always kept.
2. Versions older than `max_age` may be deleted.
3. Versions over `max_versions` may be deleted.
4. When free space is below `min_free_space`, the oldest versions are cleaned first.

## `[storage.trash]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable trash instead of permanent delete |
| `retention_days` | int | `30` | Days to keep trash items |
| `max_size` | int64 | `10737418240` | Trash size limit in bytes |

When new trash content would exceed `max_size`, older trash items are removed first. If the newest item is larger than the limit, it is still kept and total trash size may temporarily exceed the limit.

## `[storage.versioning]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `auto_versioned_extensions` | string[] | common text/code extensions | Extensions eligible for automatic versioning |
| `auto_versioned_filenames` | string[] | common config filenames | Filenames eligible for automatic versioning |
| `max_versioned_size` | int64 | `104857600` | Maximum automatically versioned file size |

Example:

```toml
[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go"]
auto_versioned_filenames = ["README", "LICENSE"]
max_versioned_size = 104857600
```

## `[dataplane]`

The Rust dataplane gRPC address is for internal `nasd` communication. Do not expose it to untrusted networks.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust dataplane gRPC address; must be `host:port` with port 1-65535 and no whitespace or control characters |
| `timeout` | duration | `"30s"` | Connect/reconnect timeout budget |
| `max_retries` | int | `3` | Maximum connection retries |

Example:

```toml
[dataplane]
grpc_address = "127.0.0.1:9090"
timeout = "60s"
max_retries = 5
```

## `[dataplane.cdc]`

Content-defined chunking settings affect deduplication and metadata overhead.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `min_chunk_size` | uint32 | `262144` | Minimum chunk size, 256KB; floored at `65536` (64KB) |
| `avg_chunk_size` | uint32 | `1048576` | Average chunk size, 1MB |
| `max_chunk_size` | uint32 | `4194304` | Maximum chunk size, 4MB; capped at `67108864` (64MB) |

Tuning guide:

| Scenario | Recommended Shape |
| --- | --- |
| Small files | min 64KB, avg 256KB, max 1MB |
| Mixed/default | min 256KB, avg 1MB, max 4MB |
| Large files/backups | min 512KB, avg 2MB, max 8MB |

Constraint:

```text
65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864
```

The dataplane reads these values on startup. Restart dataplane after changing them. The 64MB cap prevents oversized streaming chunk buffers.

## `[webdav]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable WebDAV |
| `prefix` | string | `"/dav"` | WebDAV URL prefix; normalized to a `/`-prefixed path and must not contain backslash, `?`, `#`, or control characters. When enabled it must not be `/`, `/api`, `/s`, `/health`, or a child of those reserved routes |
| `read_only` | bool | `false` | Reject write methods |
| `auth_type` | string | `"basic"` | `users`, `basic`, or `none`; blank values are normalized to `basic` |
| `username` | string | `""` | Basic Auth username; empty uses runtime default `admin` |
| `password` | string | `""` | Basic Auth password; empty uses the generated value from `secrets.json` |

Runtime behavior:

- Settings API updates can switch prefix, read-only mode, and auth config without full restart.
- Empty username with Basic Auth uses the runtime default `admin`.
- `auth_type = "users"` uses MnemoNAS app users over HTTP Basic. Admins see the global namespace; regular users see their `home_dir` as the WebDAV root, with top-level navigation entries for granted shared directories also listed at the root. Ancestor entries synthesized for nested grants are read-only navigation; writes still require a matching write grant. Guest users are read-only; user quotas are enforced for PUT/COPY/MOVE writes into `home_dir`.
- Empty password with Basic Auth uses the generated password from `secrets.json`. The generated password is a 16-character human-readable value with lowercase letters, uppercase letters, and digits, excluding ambiguous characters.
- WebDAV is matched before the API and frontend handlers, so enabled prefixes cannot overlap reserved application routes.
- `auth_type = "basic"` is the compatibility mode: one global service credential, without app-level `home_dir` isolation.

## `[smb]`

The current release does not start an SMB/Samba listener. This section is a preview contract for a future SMB gateway sidecar. If `enabled = true`, `nasd --check-config` prints a preview warning, and the health page plus diagnostics export report SMB runtime as unavailable. Use WebDAV for current LAN mounts.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Preview switch; does not start an SMB service in this build |
| `listen` | string | `"127.0.0.1:1445"` | Reserved sidecar listen address |
| `server_name` | string | `"mnemonas"` | Reserved SMB server name |
| `gateway_socket` | string | `<storage.root>/.mnemonas/run/smb-gateway.sock` | Reserved MnemoNAS gateway Unix socket |
| `credential_file` | string | `<storage.root>/.mnemonas/smb-credentials.json` | Reserved SMB-only credential file; does not reuse Web login passwords |
| `signing_required` | bool | `true` | Reserved SMB signing policy |
| `encryption_required` | bool | `false` | Reserved SMB encryption policy |
| `[[smb.shares]]` | array | `[]` | Reserved share mapping; required when the preview switch is enabled |

Share paths must be absolute MnemoNAS virtual paths such as `/` or `/team/docs`. The intended sidecar path is to keep file access behind MnemoNAS authorization, `home_dir`, and gateway APIs instead of sharing `files/` directly through Samba and bypassing version history, trash, and activity history.

## `[auth]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable Web UI/API authentication |
| `jwt_secret` | string | generated | JWT signing secret. Leave empty to use the persistent generated secret in `secrets.json`; explicit values must be at least 32 bytes |
| `access_token_ttl` | duration | `"15m"` | Access-token lifetime |
| `refresh_token_ttl` | duration | `"168h"` | Refresh-token lifetime |
| `users_file` | string | `<storage.root>/.mnemonas/users.json` | User data file |

On first startup without a users file, MnemoNAS creates an administrator and writes the initial password to:

```text
<storage.root>/.mnemonas/initial-password.txt
```

The file is removed after first successful login for that administrator.

## `[share]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable share links |
| `store_file` | string | `<storage.root>/.mnemonas/shares.json` | Share metadata file |
| `base_url` | string | `""` | Base URL used when returning share URLs; non-empty values must be absolute `http` or `https` URLs without userinfo, query strings, or fragments, and with a valid host name |
| `default_expires_in` | duration | `168h` | Default expiration for newly-created shares; `0` or empty means no default expiration |
| `default_max_access` | int | `0` | Default access-count limit for newly-created shares; `0` means unlimited |
| `[[share.policy_rules]]` | array | `[]` | Stricter share constraints for a MnemoNAS path; the most specific matching path wins |

Example:

```toml
[share]
enabled = true
base_url = "https://nas.example.com"
default_expires_in = "168h"
default_max_access = 0

[[share.policy_rules]]
path = "/Family"
require_password = true
max_expires_in = "24h"
max_access = 20
```

`base_url` affects the URL returned by the API. It does not change the share ID itself. Empty values return relative `/s/{id}` URLs. Non-empty values must be absolute `http` or `https` URLs without usernames, passwords, other userinfo, query strings, or fragments. The host must be a valid domain name or IP address; empty labels, underscores, and extra trailing dots are rejected. Default expiration and access-count limits affect only future shares; explicit `expires_in` or `max_access` values in a create request take precedence. Policy rules can set `require_password`, `max_expires_in`, and `max_access`. When a rule matches, passwordless create requests and updates that would leave an existing share passwordless are rejected if required. Expiration or access-count values above the configured limits, explicit update requests that clear those limits, and updates to existing matching shares whose stored expiry or access-count constraints are missing or above the rule limit are capped.

## `[security]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `allow_unsafe_no_auth` | bool | `false` | Allow Web UI/API auth or WebDAV authentication to be disabled while HTTP listens beyond loopback |

By default, `auth.enabled = false` or enabled WebDAV with `webdav.auth_type = "none"` fails validation when `server.host` listens beyond loopback. Set this to `true` only when a firewall, container port binding, or reverse proxy deliberately limits access; MnemoNAS will still print a security warning.

## `[favorites]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable favorites |
| `store_file` | string | `<storage.root>/.mnemonas/favorites.json` | Favorites metadata file |

## `[alerts]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable free-space alerts |
| `check_interval` | duration | `"1h"` | Check interval |
| `threshold_pct` | float | `90.0` | Warning threshold |
| `critical_pct` | float | `95.0` | Critical threshold |
| `min_free_bytes` | uint64 | `10737418240` | Minimum free bytes |
| `cooldown_period` | duration | `"4h"` | Alert cooldown |
| `webhook_url` | string | `""` | Alert webhook URL; non-empty values must be absolute `http` or `https` URLs |
| `webhook_method` | string | `"POST"` | `POST` sends JSON; `GET` encodes fields into query |
| `webhook_headers` | string[] | `[]` | Additional headers, `"Key: Value"`; names must be valid HTTP tokens and values cannot contain newlines or control characters |
| `telegram_enabled` | bool | `false` | Enable Telegram bot notifications |
| `telegram_bot_token` | string | `""` | Telegram bot token; never returned in diagnostics or settings responses |
| `telegram_chat_id` | string | `""` | Telegram chat ID or `@channel` username |
| `email_enabled` | bool | `false` | Enable SMTP email notifications |
| `smtp_host` | string | `""` | SMTP host without port |
| `smtp_port` | int | `587` | SMTP port |
| `smtp_username` | string | `""` | SMTP username |
| `smtp_password` | string | `""` | SMTP password or app password |
| `smtp_from` | string | `""` | Sender address, such as `MnemoNAS <alerts@example.com>` |
| `smtp_to` | string[] | `[]` | Recipient addresses |

Health pages and diagnostics show alert state and whether Webhook, Telegram, or email notifications are configured. The email channel is marked configured only when email alerts are enabled and SMTP host, port, sender, and at least one recipient are present. Diagnostics do not expose webhook URL, webhook headers, `telegram_bot_token`, SMTP host, SMTP username, `smtp_password`, sender address, or recipient addresses. The same notification channels are used for backup failures, restore-drill failures, stale or missing restore-drill reminders, disk-health anomalies, scrub anomalies, and login rate-limit events. Successful and failed webhook logs record only the URL scheme and host, not paths, query strings, credentials, or GET payload fields. Telegram send errors do not include the bot token.

## `[disk_health]`

Disk health uses `smartctl --json --all` to collect SMART self-assessment, temperature, power-on hours, and device presence for configured devices. It is disabled by default. Install `smartmontools` first, and ensure the user running `nasd` can read the configured devices.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable periodic disk health checks |
| `check_interval` | duration | `"1h"` | Background check interval |
| `probe_timeout` | duration | `"15s"` | Timeout for each single-device `smartctl` probe |
| `cooldown_period` | duration | `"4h"` | Minimum repeat interval for the same alert status |
| `command` | string | `"smartctl"` | Bare executable name or absolute path; whitespace and shell arguments are rejected |
| `temperature_warning_c` | int | `50` | Default warning temperature threshold in Celsius |
| `temperature_critical_c` | int | `60` | Default critical temperature threshold in Celsius |
| `media_wear_warning_percent` | int | `80` | Warning threshold for media lifetime used percentage; `0` uses the default |
| `media_wear_critical_percent` | int | `100` | Critical threshold for media lifetime used percentage; `0` uses the default |
| `devices` | array | `[]` | Devices to monitor |

`[[disk_health.devices]]` fields:

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `name` | string | `""` | Display name in the Web UI |
| `path` | string | required | Absolute device path; stable `/dev/disk/by-id/...` paths are recommended |
| `type` | string | `""` | Optional `smartctl --device` value such as `sat`, `scsi`, `nvme`, or a USB bridge type |
| `serial` | string | `""` | Optional expected serial number; mismatches are reported as critical to detect replacement or path drift |
| `temperature_warning_c` | int | global value | Per-device warning threshold override |
| `temperature_critical_c` | int | global value | Per-device critical threshold override |

Runtime behavior:

- `GET /api/v1/maintenance/disk-health` runs an immediate probe and returns full device details.
- Diagnostics and diagnostic exports include only a sanitized summary, not serial numbers.
- Background checks that find `warning`, `critical`, or `unavailable` status write a `disk_health` activity-log entry as the system user, with repeats limited by `cooldown_period`.
- NVMe `percentage_used`, `available_spare`, `critical_warning`, `media_errors`, and common ATA lifetime attributes participate in status evaluation.
- When `[alerts] enabled = true` and Webhook, Telegram, or SMTP email is configured, missing devices, SMART failures, high temperature, serial mismatch, or unavailable SMART data send a `disk_health` event.
- Missing device paths are `critical`; unavailable `smartctl` or invalid JSON is `unavailable`.

## `[maintenance.scrub]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable background scheduled Scrub runs |
| `schedule_interval` | duration | `"168h"` | Regular Scrub interval |
| `retry_interval` | duration | `"1h"` | Automatic retry interval after a failed Scrub |
| `max_retries` | int | `1` | Maximum automatic retries after one failure; `0` disables retries |

When enabled, the server triggers full Scrub runs in the background as the system user. Completion, failure, object anomalies, and result-persistence warnings continue to use maintenance history, activity logs, and configured alert channels. After a failed Scrub, the scheduler first retries according to `retry_interval` up to `max_retries`; after that, the next regular attempt follows `schedule_interval`. These fields can also be updated from the Web settings page or Settings API, and saving them immediately replaces the running background scheduler.

```toml
[maintenance.scrub]
enabled = true
schedule_interval = "168h"
retry_interval = "1h"
max_retries = 1
```

## `[log]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `level` | string | `"info"` | `debug`, `info`, `warn`, `error` |
| `format` | string | `"console"` | `console` or `json` |
| `output` | string | `"stdout"` | `stdout`, `stderr`, or file path |
| `time_format` | string | `"RFC3339"` | Timestamp format |

Example:

```toml
[log]
level = "debug"
format = "json"
output = "/var/log/mnemonas/server.log"
time_format = "RFC3339"
```

## Duration Format

Durations use Go duration syntax:

| Unit | Symbol | Example |
| --- | --- | --- |
| nanosecond | `ns` | `100ns` |
| microsecond | `us` | `500us` |
| millisecond | `ms` | `200ms` |
| second | `s` | `30s` |
| minute | `m` | `5m` |
| hour | `h` | `24h` |

Combined forms such as `1h30m` are supported.

## Environment Overrides

Environment-variable config overrides are planned but not currently supported for TOML values. Do not write `${...}` in TOML and expect runtime expansion.

## Common Scenarios

### Local Development

```toml
[server]
host = "127.0.0.1"
port = 8080

[storage]
root = "~/.mnemonas"

[webdav]
enabled = true
auth_type = "none"

[auth]
enabled = false

[log]
level = "debug"
```

Disable auth only for local development.

### Production-Like

```toml
[server]
host = "0.0.0.0"
port = 8080
read_timeout = "60s"
write_timeout = "120s"

[storage]
root = "/srv/mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 107374182400

[webdav]
enabled = true
auth_type = "basic"
username = "admin"
password = ""

[log]
level = "info"
format = "json"
output = "/var/log/mnemonas/server.log"
```

### Read-Only Archive

```toml
[webdav]
enabled = true
read_only = true
auth_type = "basic"
password = ""

[storage.retention]
gc_interval = "0"
```

## Related Documents

- [Architecture](architecture.en.md)
- [Docker deployment](docker-deployment.en.md)
- [WebDAV compatibility](webdav-compatibility.en.md)
- [Security hardening](security.en.md)
