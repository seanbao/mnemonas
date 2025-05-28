# MnemoNAS Configuration Reference

English | [简体中文](configuration.md)

MnemoNAS uses TOML configuration. This reference covers config discovery, validation, complete examples, and all main config sections.

## Config File Locations

`nasd` looks for configuration in this order:

1. `nasd --config /path/to/config.toml`
2. `$HOME/.mnemonas/config.toml`

If no file is found, defaults are used.

The Ubuntu/systemd installer writes `/etc/mnemonas/config.toml` and points the systemd unit to it with `--config`.

Config files can contain sensitive values such as `auth.jwt_secret`, WebDAV passwords, and alert webhook headers. MnemoNAS saves config files with `0600` permissions and tightens existing config files when they are loaded.

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

[server.tls]
enabled = false
cert_file = ""
key_file = ""
auto_generate = true
cert_dir = "~/.mnemonas/.mnemonas/certs"

[storage]
root = "~/.mnemonas"

[storage.retention]
max_versions = 50
max_age = "2160h"
min_free_space = 10737418240
gc_interval = "24h"

[storage.versioning]
auto_versioned_extensions = [".md", ".txt", ".go", ".rs", ".toml", ".yaml", ".json"]
auto_versioned_filenames = ["README", "LICENSE", "CHANGELOG", "Dockerfile", "Makefile"]
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
| `host` | string | `"0.0.0.0"` | Listen address; use `127.0.0.1` for local-only |
| `port` | int | `8080` | HTTP port |
| `read_timeout` | duration | `"30s"` | Request-read timeout |
| `write_timeout` | duration | `"60s"` | Response-write timeout |
| `idle_timeout` | duration | `"120s"` | Keep-alive idle timeout |
| `trusted_proxy_hops` | int | `0` | Number of trusted reverse proxy hops used to interpret forwarded headers |

Example:

```toml
[server]
host = "127.0.0.1"
port = 8443
read_timeout = "60s"
write_timeout = "120s"
trusted_proxy_hops = 1
```

`trusted_proxy_hops = 0` ignores client-supplied forwarded headers. Set it only when MnemoNAS is behind trusted proxies.

## `[server.tls]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable built-in HTTPS |
| `cert_file` | string | `""` | Certificate path |
| `key_file` | string | `""` | Private key path |
| `auto_generate` | bool | `true` | Generate a self-signed cert when paths are empty |
| `cert_dir` | string | `~/.mnemonas/.mnemonas/certs` | Generated-cert directory |

For public deployments, a reverse proxy such as Caddy or Nginx is usually preferred.

## `[storage]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `root` | string | `~/.mnemonas` | Storage root; user files live under `root/files` |

Rules:

- `root` must not be `/`.
- Startup tightens permissions on `root`, `files`, and internal directories.
- Move the full storage root when migrating data.

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
| `grpc_address` | string | `"127.0.0.1:9090"` | Rust dataplane gRPC address |
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
| `min_chunk_size` | uint32 | `262144` | Minimum chunk size, 256KB |
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
min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864
```

The dataplane reads these values on startup. Restart dataplane after changing them. The 64MB cap prevents oversized streaming chunk buffers.

## `[webdav]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable WebDAV |
| `prefix` | string | `"/dav"` | WebDAV URL prefix |
| `read_only` | bool | `false` | Reject write methods |
| `auth_type` | string | `"basic"` | `basic` or `none` |
| `username` | string | `""` | Basic Auth username; empty uses runtime default `admin` |
| `password` | string | `""` | Basic Auth password; empty uses/generated from `secrets.json` |

Runtime behavior:

- Settings API updates can switch prefix, read-only mode, and auth config without full restart.
- Empty password with Basic Auth keeps using the generated password.
- WebDAV Basic Auth is a global service credential and does not carry app-level user identity.

## `[auth]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable Web UI/API authentication |
| `jwt_secret` | string | generated | JWT signing secret |
| `access_token_ttl` | duration | `"15m"` | Access-token lifetime |
| `refresh_token_ttl` | duration | `"168h"` | Refresh-token lifetime |
| `users_file` | string | under `storage.root/.mnemonas` | User data file |

On first startup without a users file, MnemoNAS creates an administrator and writes the initial password to:

```text
<storage.root>/.mnemonas/initial-password.txt
```

The file is removed after first successful login for that administrator.

## `[share]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Enable share links |
| `store_file` | string | under `storage.root/.mnemonas` | Share metadata file |
| `base_url` | string | `""` | Base URL used when returning share URLs; non-empty values must be absolute `http` or `https` URLs |

`base_url` affects the URL returned by the API. It does not change the share ID itself. Empty values return relative `/s/{id}` URLs.

## `[security]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `allow_unsafe_no_auth` | bool | `false` | Allow Web UI/API auth or WebDAV Basic Auth to be disabled while HTTP listens beyond loopback |

By default, `auth.enabled = false` or enabled WebDAV with `webdav.auth_type = "none"` fails validation when `server.host` listens beyond loopback. Set this to `true` only when a firewall, container port binding, or reverse proxy deliberately limits access; MnemoNAS will still print a security warning.

## `[favorites]`

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | bool | `true` | Enable favorites |
| `store_file` | string | under `storage.root/.mnemonas` | Favorites metadata file |

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
| `webhook_headers` | string[] | `[]` | Additional headers, `"Key:Value"` |

Health pages and diagnostics show alert state but do not expose webhook URL or headers.

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
