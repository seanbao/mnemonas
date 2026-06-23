# MnemoNAS Configuration Reference

English | [简体中文](configuration.md)

MnemoNAS uses TOML configuration. This reference covers config discovery, validation, complete examples, and all main config sections.

## Config File Locations

`nasd` looks for configuration in this order:

1. `nasd --config /path/to/config.toml`
2. `$HOME/.mnemonas/config.toml`

If no file is found, defaults are used.

The Ubuntu/systemd installer writes `/etc/mnemonas/config.toml` and points the systemd unit to it with `--config`.

Config files can contain sensitive values such as `auth.jwt_secret`, WebDAV passwords, alert webhook headers, Telegram bot tokens, WeCom webhook URLs, and DingTalk webhook URLs. MnemoNAS saves config files with `0600` permissions and tightens existing config files when they are loaded.

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
# allowed_users = ["alice"]
# allowed_groups = ["family"]
# allowed_roles = ["user"]

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
wecom_enabled = false
wecom_webhook_url = ""
dingtalk_enabled = false
dingtalk_webhook_url = ""
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
time_format = "2006-01-02T15:04:05Z07:00"
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
- `path` fields in `directory_quotas`, `directory_access_rules`, and share policy rules use MnemoNAS logical paths. Paths must start with `/` and must not contain Windows or UNC syntax, backslashes, query or fragment characters, control characters, or `.`/`..` path segments. Configuration loading and the Settings API normalize duplicate and trailing slashes; paths containing `.` or `..` are not folded and are rejected.
- `directory_quotas` use MnemoNAS logical paths such as `/team`. Uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations check current logical bytes before writing. Use `/` for a global hard limit.
- The storage page shows aggregate directory-quota usage, warning, exceeded, and missing-path counts, a prioritized directory-quota attention list, and current usage, remaining bytes, and status for each directory quota. The storage-health summary combines capacity, native-checksum, and directory-quota risks with a suggested next-step summary.
- Before saving, the Web settings page summarizes added, changed, and removed directory quotas by comparing the saved quotas with the current draft. In line-based inputs, paths containing spaces or double quotes are wrapped in double quotes; literal double quotes inside the path are escaped as `\"`, for example `"/Family Photos" 500 GB`.
- `directory_access_rules` use clean absolute MnemoNAS paths such as `/team`. Each rule can grant `read_users`, `write_users`, `read_groups`, `write_groups`, `read_roles`, and `write_roles`.
- The most specific matching rule wins. Write grants also allow reads; write operations require an explicit write grant. Non-admin Web/API, WebDAV `users` mode, search, shares, favorites, trash, and activity views use the same decision path. Paths without a matching rule fall back to the user's `home_dir` boundary.
- Web/API root listings return only the user's `home_dir` and top-level entries for readable shared directories. When only a nested directory is granted, Web/API and WebDAV may expose existing ancestor directories as read-only navigation entries; direct children remain filtered by their own rules, and writes under those ancestors still require explicit write grants.
- Before saving, the Web settings page summarizes added, changed, and removed directory access rules by comparing the saved rules with the current draft, and shows a draft coverage summary for rule count, read/write principals, write-enabled paths, and attention items such as root-path or broad role grants. User matrices and unsaved-rule previews can copy directory-access review records and keep backend-persisted recent review history, falling back to current-browser records when server history is unavailable. The page uses a structured rule editor; enter MnemoNAS logical paths directly in the path field, including spaces or literal double quotes, without manual line quoting.

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

The Web Trash page shows a cross-directory restore review before batch restore, covering affected directories, the auto-cleanup window, conflict handling, and execution results, with a copyable review record for pre-restore confirmation. After single-item or batch restore succeeds, the page associates matching `trash_restore` activity entries with an activity review record in the `restored` disposition state; unavailable activity logging or missing matching restore activity does not block the restore itself.

## `[storage.versioning]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `auto_versioned_extensions` | string[] | common text/code extensions | Extensions eligible for automatic versioning |
| `auto_versioned_filenames` | string[] | common config filenames | Filenames eligible for automatic versioning |
| `max_versioned_size` | int64 | `104857600` | Maximum automatically versioned file size |

The Web version-history page shows a pre-submit review for the target file, overwrite impact, safety retention, execution checks, and conflict handling before version restore, with a copyable review record for pre-restore confirmation. After version restore succeeds, the page associates matching `restore` activity entries by path and version hash with an activity review record in the `restored` disposition state; unavailable activity logging or missing matching restore activity does not block the version restore itself.

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

Configure algorithm parameters for the Rust dataplane FastCDC file API. Current Go version history still uses whole-object CAS snapshots, so these settings only affect new writes that use that dataplane file API and do not mean version history has block-level deduplication enabled.

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
- Empty password with Basic Auth uses the generated password from `secrets.json`. The generated password is a 16-character human-readable value with lowercase letters, uppercase letters, and digits, excluding ambiguous characters. In public deployments, this file should be a non-symlink regular file with private permissions.
- WebDAV is matched before the API and frontend handlers, so enabled prefixes cannot overlap reserved application routes.
- `auth_type = "basic"` is the compatibility mode: one global service credential, without app-level `home_dir` isolation.

Security guidance:

- Production deployments should prefer `auth_type = "users"`. Basic Auth should be limited to legacy clients or dedicated service credentials and use a password-manager value when configured manually.
- HTTPS should terminate at a reverse proxy for network exposure.
- `read_only = true` can reduce write exposure for read-only mounts.

Example:

```toml
[webdav]
enabled = true
prefix = "/dav"
read_only = false
auth_type = "basic"
username = "webdav-service"
password = "" # leave empty to use generated credentials; use a password-manager value for custom credentials
```

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

## `[backup]`

Backup jobs are not configured by default. `[[backup.jobs]]` supports `local`, `restic`, and `rclone` jobs triggered from the Maintenance page, API, or background scheduler.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `[[backup.jobs]]` | array | `[]` | Backup job list |

`[[backup.jobs]]` fields:

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `id` | string | required | Job identifier; only ASCII letters, digits, `-`, `_`, and `.` are allowed, with a maximum of 64 characters, and it must not be `.` or `..` |
| `name` | string | required | Display name on the Maintenance page |
| `type` | string | `"local"` | `local`, `restic`, or `rclone`; blank values are normalized to `local` |
| `source` | string | `storage.root` | Backup source directory; must be absolute. Blank values use `storage.root` |
| `destination` | string | `""` | Local destination for `local` jobs; must be absolute and must not be inside the source or `storage.root` |
| `repository` | string | `""` | Restic repository; required for `restic` jobs |
| `remote` | string | `""` | Rclone remote path; required for `rclone` jobs |
| `command` | string | job type | `restic` or `rclone` executable; blank values use the job type name. Non-blank values must be a bare executable name or absolute path without whitespace or control characters |
| `password_file` | string | `""` | Restic password file; required for `restic` jobs, must be an existing non-symlink regular file, and must be outside the source and `storage.root` |
| `config_file` | string | `""` | Rclone config file; optional, must be an existing non-symlink regular file, and must be outside the source and `storage.root` |
| `extra_args` | string[] | `[]` | Additional argv entries appended to backup commands; empty entries and control characters are rejected. Restore commands do not reuse these arguments |
| `disabled` | bool | `false` | Disable the job; disabled jobs are not scheduled and cannot be run manually |
| `schedule_interval` | duration | `"0"` | Automatic schedule interval; `0` or blank means manual only |
| `schedule_window_start` | string | `""` | Automatic schedule window start, using `HH:MM` |
| `schedule_window_end` | string | `""` | Automatic schedule window end, using `HH:MM`; windows may cross midnight |
| `stale_after` | duration | `schedule_interval * 2` | Backup-success freshness threshold; explicit values apply only when greater than `0`. When omitted and automatic scheduling is enabled, the runtime uses twice the schedule interval |
| `restore_drill_stale_after` | duration | `"720h"` | Restore-drill reminder threshold; blank or omitted values use 30 days |
| `retention_policy` | string | `""` | External retention-policy note; missing values on `restic` and `rclone` jobs produce retention-check warnings |
| `max_snapshots` | int | `0` | Maximum local snapshots to retain for `local` jobs; `0` disables count-based cleanup |
| `max_age` | duration | `"0"` | Maximum local snapshot age for `local` jobs; `0` disables age-based cleanup |
| `include_config` | bool | `false` | Whether `local` backups copy the current config file |
| `verify_after_backup` | bool | `false` | Whether to verify after backup; `local` checks snapshot file hashes, `restic` runs `restic check`, and `rclone` runs `rclone check --one-way` |
| `exclude` | string[] | `[]` | Exclude patterns; empty entries and control characters are rejected |

Runtime behavior:

- `local` jobs create snapshots under `destination/<job-id>/snapshots/<run-id>/`. The destination must be outside `storage.root` and the backup source, and path components must not cross boundaries through symlinks.
- `restic` and `rclone` jobs invoke external commands as argv without shell command construction. `command`, credential files, `extra_args`, `exclude`, and remote locations are handled according to config validation rules.
- `schedule_window_start` and `schedule_window_end` constrain only automatic scheduling. Manual runs from the Maintenance page or API are not constrained by the window. The window uses server local time.
- `restore_drill_stale_after` controls missing or stale restore-drill reminders. Blank, `"0"`, or omitted values are treated as 30 days at runtime.
- Local retention cleanup always keeps the current snapshot. Actual retention for `restic` and `rclone` is managed by the external tool or cloud lifecycle rules, and `retention_policy` records that the deployment-side policy has been confirmed.
- The Maintenance page generates a single-job pre-submit restore review covering the target directory, restore contents, config-file handling, preflight result, write boundary, and post-restore checks, with a copyable review record for pre-restore confirmation.
- Backup, restore, restore-drill, restore-verify, and retention-check alerts reuse `[alerts]` channels. External notifications do not expose job names, source paths, target paths, snapshot paths, raw warnings, or low-level error text.

```toml
[backup]

[[backup.jobs]]
id = "external-disk"
name = "External disk backup"
type = "local"
source = ""
destination = "/mnt/backup-drive/mnemonas"
schedule_interval = "24h"
schedule_window_start = "02:00"
schedule_window_end = "05:00"
stale_after = "72h"
restore_drill_stale_after = "720h"
max_snapshots = 7
max_age = "720h"
include_config = true
verify_after_backup = true
exclude = [".mnemonas/thumbnails"]
```

## `[auth]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable Web UI/API authentication |
| `jwt_secret` | string | generated | JWT signing secret. Leave empty to use the persistent generated secret in `secrets.json`; explicit values must be at least 32 bytes |
| `access_token_ttl` | duration | `"15m"` | Access-token lifetime; public deployments should keep this at or below `1h` |
| `refresh_token_ttl` | duration | `"168h"` | Refresh-token lifetime; public deployments should keep this at or below `720h` (30 days) |
| `users_file` | string | `<storage.root>/.mnemonas/users.json` | User data file |

On first startup without a `users_file`, or when the file has no enabled administrator, MnemoNAS creates a default or recovery administrator and writes the initial password to `initial-password.txt` in the same directory as `users_file`. The default path is `<storage.root>/.mnemonas/initial-password.txt`. The file is removed after first successful login for that administrator. When authentication is enabled, `mnemonas-doctor` parses this user file and reports whether a usable administrator exists.

## `[share]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable share links |
| `store_file` | string | `<storage.root>/.mnemonas/shares.json` | Share metadata file |
| `base_url` | string | `""` | Base URL used when returning share URLs; non-empty values must be absolute `http` or `https` URLs without userinfo, query strings, fragments, encoded query or fragment markers, backslashes, duplicate path slashes, or `.`/`..` path segments, and with a valid host name |
| `default_expires_in` | duration | `168h` | Default expiration for newly-created shares; `0` or empty means no default expiration. Public deployments should keep an explicit default expiry at or below `720h` (30 days) |
| `default_max_access` | int | `0` | Default access-count limit for newly-created shares; `0` means unlimited |
| `[[share.policy_rules]]` | array | `[]` | Stricter share constraints and allowed creator/maintainer scope for a MnemoNAS path; the most specific matching path wins |

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
allowed_users = ["alice"]
allowed_groups = ["family"]
allowed_roles = ["user"]
```

`base_url` affects the URL returned by the API. It does not change the share ID itself. Empty values return relative `/s/{id}` URLs.

Non-empty `base_url` values must satisfy these rules:

- Use an absolute `http` or `https` URL.
- Do not include usernames, passwords, or other userinfo.
- Do not include query strings, fragments, encoded query or fragment markers, backslashes, duplicate path slashes, or `.`/`..` path segments.
- Use a valid domain name or IP address without empty labels or underscores.

A single FQDN trailing dot is treated as the same host, while repeated trailing dots are rejected. Encoded query or fragment markers, backslashes, duplicate path slashes, and dot segments are rejected because proxies or browsers may normalize the generated share address differently.

Public deployments that use a reverse-proxy application base path should set that base path itself, such as `https://nas.example.com/mnemonas`, and should not include the `/s` share route in `base_url`. Paths ending in `/s` produce a manual-review warning in the security self-check and public diagnostics.

Default expiration and access-count limits affect only future shares; explicit `expires_in` or `max_access` values in a create request take precedence.

The `path` field in each policy rule follows the same MnemoNAS logical-path rules as directory quotas and directory access rules. Policy rules can set `require_password`, `max_expires_in`, `max_access`, `allowed_users`, `allowed_groups`, and `allowed_roles`. When a rule matches, passwordless create requests and updates that would leave an existing share passwordless are rejected if required. Expiration or access-count values above the configured limits, explicit update requests that clear those limits, and updates to existing matching shares whose stored expiry or access-count constraints are missing or above the rule limit are capped.

`allowed_users`, `allowed_groups`, and `allowed_roles` restrict which authenticated caller may create or maintain share links under the path. User values match either user IDs or usernames, group values match user groups, and role values support `admin`, `user`, and `guest`. Administrators bypass this scope restriction so they can repair existing shares. Creator-scope enforcement is skipped when application authentication is disabled. This restriction affects authenticated share creation and maintenance only; it does not change public access boundaries for already-created share links.

The Web share-create dialog shows a pre-submit summary of policy source, password requirement, effective expiration, and effective access limit, including path-policy caps. The Web settings page shows a pre-save change summary for share enablement, base URL, default expiration, default access limit, and path policy rules compared with the saved configuration, and its coverage summary lists cleanup suggestions for root-wide rules, most-specific path rules that do not inherit ancestor limits, descendant rules that loosen ancestor expiration, access-count, or creator-scope limits, and duplicate-equivalent rules. The Web share list summarizes shares requiring review, passwordless links, broad-scope links, soon-expiring links, and stale links, with matching filters, current-scope review-record creation, share-type-filtered review-history handoff, expandable review details, high-risk disable actions, single-share enable or disable actions, and deletion actions. After high-risk disable, single-share disable, or share deletion actions succeed, matching `unshare` activity entries are associated with an access-closure execution-result review record. After a single share is re-enabled successfully, matching `share` activity entries are associated with a confirmed re-enable execution-result review record.

## `[security]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `allow_unsafe_no_auth` | bool | `false` | Allow Web UI/API auth or WebDAV authentication to be disabled while HTTP listens beyond loopback |

By default, `auth.enabled = false` or enabled WebDAV with `webdav.auth_type = "none"` fails validation when `server.host` listens beyond loopback. Set this to `true` only when a firewall, container port binding, or reverse proxy deliberately limits access; MnemoNAS will still print a security warning. `mnemonas-doctor` also reports these unauthenticated postures in ordinary deployment diagnostics so the outer network boundary can be reviewed after installation.

## `[favorites]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable favorites |
| `store_file` | string | `<storage.root>/.mnemonas/favorites.json` | Favorites metadata file |

Administrators can load, reset, and save the favorites switch independently on Favorites. The Web form submits only `favorites` and does not overwrite other runtime settings.

## `[alerts]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable free-space alerts |
| `check_interval` | duration | `"1h"` | Check interval |
| `threshold_pct` | float | `90.0` | Warning threshold |
| `critical_pct` | float | `95.0` | Critical threshold |
| `min_free_bytes` | uint64 | `10737418240` | Minimum free bytes |
| `cooldown_period` | duration | `"4h"` | Alert cooldown |
| `webhook_url` | string | `""` | Alert webhook URL; non-empty values must be absolute `http` or `https` URLs with a valid host name or IP address |
| `webhook_method` | string | `"POST"` | `POST` sends JSON; `GET` encodes fields into query |
| `webhook_headers` | string[] | `[]` | Additional headers, `"Key: Value"`; names must be valid HTTP tokens, cannot repeat case-insensitively, and values cannot contain newlines or control characters |
| `telegram_enabled` | bool | `false` | Enable Telegram bot notifications |
| `telegram_bot_token` | string | `""` | Telegram bot token; never returned in diagnostics or settings responses |
| `telegram_chat_id` | string | `""` | Telegram chat ID or `@channel` username |
| `wecom_enabled` | bool | `false` | Enable WeCom group robot notifications |
| `wecom_webhook_url` | string | `""` | WeCom group robot webhook URL; required when WeCom notifications are enabled, non-empty values must be absolute `http` or `https` URLs with a valid host name or IP address, and the value is never returned in diagnostics or settings responses |
| `dingtalk_enabled` | bool | `false` | Enable DingTalk group robot notifications |
| `dingtalk_webhook_url` | string | `""` | DingTalk group robot webhook URL; required when DingTalk notifications are enabled, non-empty values must be absolute `http` or `https` URLs with a valid host name or IP address, and the value is never returned in diagnostics or settings responses |
| `email_enabled` | bool | `false` | Enable SMTP email notifications |
| `smtp_host` | string | `""` | SMTP host without port |
| `smtp_port` | int | `587` | SMTP port |
| `smtp_username` | string | `""` | SMTP username |
| `smtp_password` | string | `""` | SMTP password or app password |
| `smtp_from` | string | `""` | Sender address, such as `MnemoNAS <alerts@example.com>` |
| `smtp_to` | string[] | `[]` | Recipient addresses |

Alert Webhook, Telegram, WeCom, and DingTalk outbound requests do not follow HTTP redirects; `3xx` responses are treated as delivery failures.

Health loads, validates, resets, and saves notification settings independently. The Web form submits only `alerts` and does not overwrite other runtime settings. Health and diagnostics also show alert state and whether Webhook, Telegram, WeCom, DingTalk, or email notifications are configured. The email channel is marked configured only when email alerts are enabled and SMTP host, port, sender, and at least one non-empty recipient are present.

Diagnostics do not expose webhook URL, webhook headers, `telegram_bot_token`, `wecom_webhook_url`, `dingtalk_webhook_url`, SMTP host, SMTP username, `smtp_password`, sender address, or recipient addresses.

The same notification channels are used for these events:

- Backup failures, backup-warning runs, explicit restore failures or warnings, post-restore read-only verification failures or warnings, restore-drill failures or warnings, stale or missing restore-drill reminders, and retention-check failures or warnings.
- Disk-health anomalies, scrub anomalies, login rate-limit events, and directory access or share policy changes.
- Aggregate reminders for enabled shares that expire within 72 hours.

Storage-capacity events use `storage_alert`. External payloads keep capacity metrics and `path_scope = "configured_storage_root"`, but set `path` to `<omitted>`, and text channels do not include the raw storage root path.

Backup-related event types include `backup_run`, `backup_restore`, `backup_restore_verify`, `backup_restore_drill`, and `backup_retention_check`. `scrub_run` details omit object hashes and lower-level error text; `login_rate_limited` details include only username status and client-address scope, not raw usernames or client addresses; share-related event types include `share_expiring_soon`, whose details use aggregate counts and do not include share paths, URLs, passwords, or IDs.

Administrators can send an `alert_test` event from Health or `POST /api/v1/settings/alerts/test` after saving the alert configuration. The Web UI does not send a test alert while changes are unsaved, alerts are disabled, or no enabled channel is configured.

Successful and failed webhook, WeCom, and DingTalk logs record only the URL scheme and host, not paths, query strings, credentials, or GET payload fields. Telegram send errors do not include the bot token. SMTP success logs do not record SMTP hosts or addresses, and SMTP failure errors do not echo SMTP hosts, usernames, passwords, senders, recipients, or raw server error text.

## `[disk_health]`

Disk health uses `smartctl --json --all` to collect SMART self-assessment, temperature, power-on hours, and device presence for configured devices. It is disabled by default. Install `smartmontools` first, and ensure the user running `nasd` can read the configured devices.

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable periodic disk health checks |
| `check_interval` | duration | `"1h"` | Background check interval |
| `probe_timeout` | duration | `"15s"` | Timeout for each single-device `smartctl` probe |
| `cooldown_period` | duration | `"4h"` | Minimum repeat interval for the same alert status |
| `command` | string | `"smartctl"` | Bare executable name or absolute path; whitespace, control characters, and shell arguments are rejected |
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

- Administrators can load, validate, reset, and save disk-health settings independently on Health. The Web form submits only `disk_health` and does not overwrite other runtime settings.
- `GET /api/v1/maintenance/disk-health` runs an immediate probe and returns full device details.
- Diagnostics and diagnostic exports include only a sanitized summary, not serial numbers.
- Background checks that find `warning`, `critical`, or `unavailable` status write a `disk_health` activity-log entry as the system user, with repeats limited by `cooldown_period`.
- NVMe `percentage_used`, `available_spare`, `critical_warning`, `media_errors`, and common ATA lifetime attributes participate in status evaluation.
- When `[alerts] enabled = true` and Webhook, Telegram, WeCom, DingTalk, or SMTP email is configured, missing devices, SMART failures, high temperature, serial mismatch, or unavailable SMART data send a `disk_health` event. Event details use aggregate counts and do not include device names, full device paths, serial numbers, or warning text.
- Missing device paths are `critical`; unavailable `smartctl` or invalid JSON is `unavailable`.

## `[maintenance.scrub]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable background scheduled Scrub runs |
| `schedule_interval` | duration | `"168h"` | Regular Scrub interval |
| `retry_interval` | duration | `"1h"` | Automatic retry interval after a failed Scrub |
| `max_retries` | int | `1` | Maximum automatic retries after one failure; `0` disables retries |

When enabled, the server triggers full Scrub runs in the background as the system user. Completion, failure, object anomalies, and result-persistence warnings continue to use maintenance history, activity logs, and configured alert channels. After a failed Scrub, the scheduler first retries according to `retry_interval` up to `max_retries`; after that, the next regular attempt follows `schedule_interval`. These fields can also be updated from Maintenance or the Settings API. The Web form submits only `maintenance.scrub`; saving immediately replaces the running background scheduler without overwriting other runtime settings.

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
| `time_format` | string | `"2006-01-02T15:04:05Z07:00"` | Timestamp format. Supports `RFC3339`, `RFC3339Nano`, `Unix`, `UnixMs`, `UnixMicro`, `UnixNano`, or a Go time layout |

Example:

```toml
[log]
level = "debug"
format = "json"
output = "/var/log/mnemonas/server.log"
time_format = "2006-01-02T15:04:05Z07:00"
```

Both `console` and `json` logs recognize these named formats. `Unix*` formats write numeric timestamps in `json` output; in `console` output they preserve the raw numeric timestamp so log collectors see the configured representation. Custom values are interpreted as Go `time.Format` layouts, for example `2006-01-02T15:04:05Z07:00`.

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

Disable auth only for local development. Setting only `webdav.auth_type = "none"` does not disable Web UI/API login; local deployments that intentionally run without authentication must also set `auth.enabled = false`.

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
min_free_space = 107374182400  # 100GB

[webdav]
enabled = true
auth_type = "users"  # prefer MnemoNAS app users for production deployments

[auth]
enabled = true

[log]
level = "info"
format = "json"
output = "/var/log/mnemonas/server.log"
```

When legacy clients or dedicated service credentials require global Basic Auth, set `auth_type = "basic"` and use a password-manager value or leave the password empty for first-start generation.

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
