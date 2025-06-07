<!-- markdownlint-disable MD022 MD031 MD032 MD036 MD040 MD060 -->

# MnemoNAS API Reference

English | [简体中文](api-reference.md)

This reference describes MnemoNAS REST API conventions, endpoint groups, and request/response shapes. The default base URL is:

```text
http://localhost:8080
```

Most endpoints use JSON. File upload and download endpoints use file payloads or streamed responses.

JSON request bodies are parsed strictly. Write endpoints reject unknown fields and multiple concatenated JSON values with `400 invalid request body`.

## Authentication

When Web UI/API authentication is enabled, the Web UI uses same-origin `HttpOnly` cookies for its primary session. API clients can still include:

```http
Authorization: Bearer <access_token>
```

Login and refresh set `mnemonas_access` and `mnemonas_refresh` cookies. Browser clients can send `X-MnemoNAS-Session-Mode: cookie`; in that mode the JSON response omits bearer tokens and returns only user/session metadata.

WebDAV Basic Auth is separate from the Web UI/API JWT flow.

## Response Formats

Most `/api/v1` success responses:

```json
{
  "success": true,
  "data": {},
  "message": "ok",
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

Most `/api/v1` error responses:

```json
{
  "code": "BAD_REQUEST",
  "message": "error description",
  "details": {},
  "request_id": "optional",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

Auth and public-share errors use:

```json
{
  "success": false,
  "error": {
    "code": "ERROR_CODE",
    "message": "error description"
  }
}
```

Authenticated share and favorite management endpoints use `success + data (+ message)`. Public share endpoints under `/api/v1/public/shares/*` return raw JSON objects or arrays on success and structured `success: false` errors on failure.

## HTTP Status Codes

| Code | Meaning |
| --- | --- |
| `200` | Success |
| `201` | Created |
| `400` | Invalid request |
| `401` | Not authenticated or token expired |
| `403` | Authenticated but forbidden |
| `404` | Not found |
| `409` | Resource conflict or operation not executable |
| `410` | Resource unavailable, expired, disabled, or access limit reached |
| `413` | File too large |
| `429` | Rate limited |
| `500` | Internal error |
| `503` | Service dependency unavailable |

## Warning Header

Some write endpoints may commit the visible mutation but fail a later persistence or cleanup step. They then return a success status with an HTTP `Warning` header, for example:

- `199 MnemoNAS "activity log persistence failed"`
- `199 MnemoNAS "auth state persistence incomplete"`
- `199 MnemoNAS "workspace mutation persistence incomplete"`
- `199 MnemoNAS "share persistence incomplete"`
- `199 MnemoNAS "favorites persistence incomplete"`
- `199 MnemoNAS "scrub result persistence incomplete"`
- `199 MnemoNAS "trash restore metadata reconciliation failed"`
- `199 MnemoNAS "delete cleanup incomplete"`
- `199 MnemoNAS "trash delete cleanup incomplete"`

Clients should inspect the HTTP `Warning` header in addition to the JSON body.

## Auth Endpoints

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | Log in with username and password |
| `POST` | `/api/v1/auth/refresh` | Exchange refresh token for a new access token |
| `GET` | `/api/v1/auth/me` | Get current user |
| `POST` | `/api/v1/auth/logout` | Log out |
| `POST` | `/api/v1/auth/download-session` | Create short-lived download-session cookie |
| `POST` | `/api/v1/auth/password` | Change current user's password |

Login request:

```json
{
  "username": "admin",
  "password": "your_password"
}
```

Login response for API clients:

```json
{
  "success": true,
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "eyJ...",
    "expires_at": "2024-01-15T10:15:00Z",
    "token_type": "Bearer",
    "user": {
      "id": "user-123",
      "username": "admin",
      "role": "admin",
      "home_dir": "/"
    }
  }
}
```

Cookie-session login also sets `mnemonas_access` and `mnemonas_refresh`. With `X-MnemoNAS-Session-Mode: cookie`, the `data` object omits `access_token` and `refresh_token`.

Refresh accepts either a JSON refresh token body for API clients or the `mnemonas_refresh` cookie for the Web UI. Refresh rotates the refresh token and sets new access/refresh cookies. Responses using the refresh cookie, or `X-MnemoNAS-Session-Mode: cookie`, omit bearer tokens from JSON.

Logout revokes the current access token when a valid bearer token or session cookie is present and clears `mnemonas_access`, `mnemonas_refresh`, and the short-lived `mnemonas_download_access` cookie. It still attempts cookie cleanup when the access cookie is expired.

Failed login attempts are rate-limited by username and client address. Client address uses the direct peer unless `server.trusted_proxy_hops` is configured and the request comes from a trusted private/loopback proxy. When alert channels are configured, a rate-limited login sends a throttled `login_rate_limited` warning event containing only the username and client address, never passwords or tokens.

## Admin User Endpoints

Admin role required.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/admin/users` | List users |
| `POST` | `/api/v1/admin/users` | Create user |
| `DELETE` | `/api/v1/admin/users/{id}` | Delete user |
| `POST` | `/api/v1/admin/users/{id}/reset-password` | Reset user password |
| `PUT` | `/api/v1/admin/users/{id}/status` | Enable or disable user |

User roles are `admin`, `user`, and `guest`. Non-admin users are scoped by `home_dir`.

Usernames are limited to 255 characters and must not contain `/`, `\`, control characters, `.`, or `..`. Passwords must be 8 to 72 bytes.

## System Endpoints

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/health` | No | Health check |
| `GET` | `/api/v1/version` | Usually no | Version/build info |
| `GET` | `/api/v1/setup/` | Depends on setup state | Initial setup status |
| `POST` | `/api/v1/setup/acknowledge` | Yes | Acknowledge initial info |
| `GET` | `/api/v1/stats` | Yes | Storage statistics |
| `GET` | `/api/v1/diagnostics` | Admin | Diagnostic information |
| `GET` | `/api/v1/metrics` | Admin when auth enabled | JSON metrics |

Prometheus cannot directly scrape `/api/v1/metrics` as native exposition format. Use a JSON exporter or conversion layer.

## File Operations

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/files/{path}` | List directory or get file metadata |
| `POST` | `/api/v1/files/{path}` | Upload or overwrite file |
| `DELETE` | `/api/v1/files/{path}` | Delete to trash when trash is enabled |
| `POST` | `/api/v1/files-move` | Move or rename resource |
| `POST` | `/api/v1/files-copy` | Copy file or directory recursively |
| `GET` | `/api/v1/download/{path}` | Authenticated file download |
| `POST` | `/api/v1/directories/{path}` | Create directory |

Move request:

```json
{
  "source": "/documents/old.txt",
  "destination": "/documents/new.txt"
}
```

Copy request:

```json
{
  "source": "/documents/report.txt",
  "destination": "/archive/report.txt",
  "overwrite": false
}
```

Some file mutations may return success with a `Warning` header if the file operation succeeded but later metadata, activity, or cleanup work did not fully complete.

## Thumbnails

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/thumbnails/{path}` | Get generated thumbnail for an image or supported preview |

Download-session cookies are used for preview and thumbnail flows where browser media elements cannot attach Authorization headers. `POST /api/v1/auth/download-session` can be authenticated by the Web UI session cookie or by `Authorization: Bearer <access-token>` and sets `mnemonas_download_access` scoped to `/api/v1`. Thumbnail responses are generated images and include `nosniff` plus a sandbox CSP.

Thumbnail generation rejects sources larger than 100 MiB, image dimensions above 10000x10000, or images above 50 million pixels.

## Version History

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/versions/{path}` | List versions for a file |
| `POST` | `/api/v1/versions/{hash}/restore` | Restore a version to the requested path |

Restore request often includes the target path as a query parameter:

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=/documents/report.txt"
```

## Trash

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/trash` | List trash items |
| `GET` | `/api/v1/trash/{id}` | Get trash item detail |
| `POST` | `/api/v1/trash/{id}/restore` | Restore trash item |
| `DELETE` | `/api/v1/trash/{id}` | Permanently delete one item |
| `DELETE` | `/api/v1/trash` | Empty trash |

Trash visibility follows the current user's configured `home_dir` boundary.

## Search

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/search?q={query}` | Search files by name |

Search results are scoped by configured `home_dir`.

## Share Links

Authenticated management endpoints:

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/shares` | Create share |
| `GET` | `/api/v1/shares` | List shares |
| `GET` | `/api/v1/shares/{id}` | Get share detail |
| `PUT` | `/api/v1/shares/{id}` | Update share |
| `DELETE` | `/api/v1/shares/{id}` | Delete share |

Create request:

```json
{
  "path": "/documents/report.pdf",
  "password": "optional-password",
  "expires_at": "2024-12-31T23:59:59Z",
  "max_accesses": 100
}
```

`password` is optional; non-empty share passwords are limited to 72 bytes.

Public endpoints:

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/public/shares/{share_id}` | Get public share metadata |
| `POST` | `/api/v1/public/shares/{share_id}/access` | Submit password and receive share cookie |
| `GET` | `/api/v1/public/shares/{share_id}/download` | Download shared file |
| `GET` | `/api/v1/public/shares/{share_id}/items?path=subdir` | List shared directory items |
| `GET` | `/api/v1/public/shares/{share_id}/download/{path}` | Download item from shared directory |

Password-protected shares use an `HttpOnly` cookie after password validation. Failed password attempts are rate-limited.

## Favorites

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/favorites` | List favorites |
| `POST` | `/api/v1/favorites` | Add favorite |
| `GET` | `/api/v1/favorites/check?path=/documents/file.pdf` | Check one path |
| `POST` | `/api/v1/favorites/check-batch` | Check multiple paths |
| `DELETE` | `/api/v1/favorites/{path}` | Remove favorite |
| `PATCH` | `/api/v1/favorites/{path}` | Update note |

Add request:

```json
{
  "path": "/documents/report.pdf",
  "note": "quarterly report"
}
```

## Activity Log

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/activity` | List activity entries |
| `GET` | `/api/v1/activity/stats` | Activity statistics |
| `DELETE` | `/api/v1/activity` | Clear activity log; admin only |

Activity visibility follows user scope. Admins can see all activity. System events, such as periodic `disk_health` checks, are also written to the activity log.

## Settings

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/settings` | Get current settings |
| `GET` | `/api/v1/settings/security-check` | Run public-access security self-check |
| `PUT` | `/api/v1/settings` | Update settings |
| `GET` | `/api/v1/settings/webdav-credentials` | Get current WebDAV credential status |

Settings updates can change WebDAV prefix, read-only mode, share configuration, favorite configuration, alert configuration, disk-health monitoring, scheduled Scrub maintenance, dataplane connection settings, and retention/versioning policies at runtime. Alert updates include Webhook, Telegram, and SMTP email notification settings; disk-health updates include temperature and media-wear thresholds. Scheduled Scrub updates immediately replace the running background scheduler. Server listener/TLS changes and CDC chunk-size changes are saved but require restarting the affected service before they take effect.

`server.host` must be empty, `*`, a valid hostname, IPv4, or IPv6 literal, without a port, whitespace, or control characters; set the port through `server.port`. `server.trusted_proxy_hops` controls whether forwarded headers from trusted reverse proxies are honored when evaluating HTTPS request semantics. `webdav.prefix` is normalized to a `/`-prefixed URL path, must not contain backslash, `?`, `#`, or control characters, and when enabled must not overlap `/`, `/api`, `/s`, or `/health`. Non-empty `share.base_url` and `alerts.webhook_url` values must be absolute `http` or `https` URLs. Alert `webhook_method` supports `GET` and `POST`; custom webhook headers use `"Key: Value"` strings with valid HTTP token names and values without newlines or control characters. When `alerts.telegram_enabled` is true, `telegram_bot_token` and `telegram_chat_id` are required; the bot token cannot contain whitespace, `/`, `?`, or `#` and is never returned by settings or diagnostics responses. When `alerts.email_enabled` is true, `smtp_host`, `smtp_from`, and at least one `smtp_to` recipient are required, `smtp_port` must be 1-65535, and sender/recipient values must be valid email addresses. `disk_health.command` must be a single executable name or absolute path, `disk_health.media_wear_critical_percent` must not be lower than `disk_health.media_wear_warning_percent`, and each `disk_health.devices[].path` must be absolute. `maintenance.scrub.schedule_interval` and `maintenance.scrub.retry_interval` must be positive duration strings, and `maintenance.scrub.max_retries` must be zero or greater. `dataplane.grpc_address` must be a valid `host:port` address with port 1-65535 and no whitespace or control characters. CDC chunk sizes must satisfy `65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864`. Invalid settings return `400 Bad Request` without mutating the running config.

### Public-Access Security Self-Check

`GET /api/v1/settings/security-check` requires administrator access. It returns the runtime checks used by the Web UI security self-check and can also be consumed by deployment automation:

```json
{
  "success": true,
  "data": {
    "status": "warning",
    "generated_at": "2026-05-09T10:00:00Z",
    "checks": [
      {
        "id": "https_request",
        "status": "warning",
        "title": "Current request is not HTTPS",
        "message": "Public access should use built-in TLS or a trusted HTTPS reverse proxy.",
        "details": {
          "direct_tls": false,
          "forwarded_proto": "",
          "trusted_forwarded_source": true
        }
      }
    ],
    "request": {
      "scheme": "http",
      "direct_tls": false,
      "host": "localhost:8080",
      "remote_ip": "127.0.0.1",
      "trusted_forwarded_source": true,
      "forwarded_proto": ""
    },
    "config": {
      "auth_enabled": true,
      "server_host": "0.0.0.0",
      "server_port": 8080,
      "tls_enabled": false,
      "trusted_proxy_hops": 0,
      "dataplane_grpc_addr": "127.0.0.1:9090",
      "webdav_enabled": true,
      "webdav_auth_type": "basic",
      "share_enabled": false
    }
  }
}
```

`data.status` and `checks[].status` use `pass`, `warning`, or `block`; `block` dominates `warning`, and `warning` dominates `pass` for the aggregate status. Current check IDs include `auth_enabled`, `https_request`, `trusted_proxy_or_tls`, `server_listen`, `dataplane_listen`, `webdav_auth`, `smb_preview`, `share_base_url`, and `initial_password_file`. The endpoint can verify only what the MnemoNAS process can observe: runtime configuration and the current request's proxy/TLS semantics. It cannot directly verify cloud security groups, public routing, externally exposed ports, or certificate-chain validity. Public deployments should still run `sudo mnemonas-doctor --public-domain <domain>` on the server and confirm in the cloud console that only `80/443` are publicly reachable.

## Maintenance

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/maintenance/scrub` | Get latest scrub results |
| `POST` | `/api/v1/maintenance/scrub` | Start scrub |
| `GET` | `/api/v1/maintenance/disk-health` | Run and return disk SMART/temperature health |
| `GET` | `/api/v1/maintenance/objects` | List storage objects |
| `POST` | `/api/v1/maintenance/gc` | Run garbage collection |
| `GET` | `/api/v1/maintenance/backups` | List configured backup jobs |
| `GET` | `/api/v1/maintenance/backups/{id}` | Get one backup job status |
| `POST` | `/api/v1/maintenance/backups/{id}/run` | Run a backup job now |
| `POST` | `/api/v1/maintenance/backups/{id}/restore` | Restore a supported backup job into a safe target directory |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-drill` | Restore-drill the latest completed snapshot |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-preview` | Preview an explicit restore without writing target data |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-verify` | Verify a restored target directory without modifying it |
| `GET` | `/api/v1/diagnostics-export` | Export diagnostic bundle |

Maintenance endpoints are admin-oriented and may be long-running. The Web UI exposes the same operations from maintenance pages.
Scrub object errors return stable public `errors[].message` values; lower-level IO, path, and verification details are kept in server logs.
Manual scrub runs write `scrub` activity-log entries. When `[maintenance.scrub] enabled = true`, the server runs full Scrub jobs in the background as the system user according to `schedule_interval`; failed runs retry after `retry_interval` up to `max_retries`. Scheduled runs use the same maintenance history, activity-log details, result shape, and alert events as manual runs. Scrub failures, object verification problems, and incomplete result persistence send `scrub_run` events through configured Webhook/Telegram/SMTP alert channels.
`GET /api/v1/maintenance/disk-health` uses `[disk_health]` and `smartctl --json --all` to report `disabled`, `ok`, `warning`, `critical`, or `unavailable`. Missing devices, SMART failures, serial mismatches, critical temperatures, NVMe critical warnings, exhausted spare capacity, media-wear thresholds, and media errors affect device status. Periodic checks that find warning, critical, or unavailable status write a `disk_health` activity-log entry at `/system/disk-health` for the `system` user. When `[alerts]` has Webhook, Telegram, or SMTP email configured, periodic disk-health checks send `disk_health` events for warning, critical, and unavailable states.
`GET /api/v1/diagnostics` and `/diagnostics-export` include a sanitized `maintenance` summary with `history_ready`, `[maintenance.scrub]` schedule settings, the latest Scrub status/time, and the retry count for the latest failed Scrub.
`GET /api/v1/diagnostics` and `/diagnostics-export` include sanitized `smb` preview state. Current builds do not start an SMB/Samba listener, so `runtime_available=false` means the configured SMB shares are not mountable; diagnostics expose share counts and runtime state but never SMB credential contents.
`GET /api/v1/maintenance/objects` accepts an optional `cursor` query parameter from the previous `next_cursor`; non-empty cursors must be 64-character hexadecimal object hashes.
Backup endpoints operate on jobs configured under `[[backup.jobs]]`. Supported job types are `local`, `restic`, and `rclone`. Local jobs copy into `destination/<job-id>/snapshots/<run-id>/` and can prune old snapshots by `max_snapshots` and `max_age`. Restic jobs invoke `restic -r <repository> --password-file <password_file> backup <source>` and optionally `restic check`; rclone jobs invoke `rclone sync <source> <remote>` and optionally `rclone check --one-way`. External commands are executed without a shell; `command` must be a bare executable name or absolute path, and `extra_args` are appended to backup commands as argv entries. Restore commands do not reuse backup-specific extra args. Jobs may define `disabled`, `schedule_interval`, `schedule_window_start`, `schedule_window_end`, `stale_after`, `restore_drill_stale_after`, `max_snapshots`, `max_age`, and `retention_policy`; a positive `schedule_interval` enables the in-process scheduler. If both schedule-window fields are set, automatic runs only start inside that server-local `HH:MM` window, while manual run-now operations are unaffected. Job views include backup `health_status` (`ok`, `manual`, `running`, `due`, `stale`, `failed`, or `disabled`), `retention_status`, and `restore_drill_status` plus optional messages. `retention_policy` marks restic/rclone remote retention as externally confirmed; otherwise remote jobs report a retention warning. `restore_drill_stale_after` defaults to 30 days and drives restore-drill reminder status; when alert channels are configured, stale or missing restore drills send rate-limited `backup_restore_drill` warning events with `trigger=restore_drill_reminder` and persist `last_restore_drill_reminder_at`. Restore history is capped to the latest 20 entries and records target path, status, file/byte counts, and failure messages. When `[alerts] enabled = true` and Webhook, Telegram, or SMTP email is configured, backup failures, restore-drill failures, stale/missing restore-drill reminders, and backup-warning runs send events with type `backup_run` or `backup_restore_drill`, level `warning` or `critical`, and task/run/error details. `POST /run` accepts an empty body or `{}`. `POST /restore-drill` accepts optional `{"keep_artifact": true}`; local jobs temporarily restore and verify the latest snapshot, restic jobs run `restic check`, and rclone jobs run `rclone check --one-way`. `POST /restore-preview` validates the same target rules as restore but does not create target data or write restore history; local jobs summarize the latest manifest, restic jobs run `restic ls latest --json --tag mnemonas --tag job:<id> --path <source>`, and rclone jobs run `rclone lsjson <remote> --recursive --files-only`. `POST /restore` supports local, restic, and rclone jobs and requires `{"target_path": "/absolute/restore/path", "include_config": true}`. The target must be outside `storage.root`, the backup source, and any local backup destination or repository. Its parent must exist, and the target must not exist or must be empty. Local restore copies snapshot `data/` contents into the target root, verifies size and SHA-256, and restores config to `.mnemonas-restore/config.toml` when requested. Restic restore runs `restic restore latest --target <staging> --tag mnemonas --tag job:<id> --path <source>`, then installs the restored source directory contents into the target root. Rclone restore runs `rclone copy <remote> <target>` and then `rclone check <remote> <target> --one-way`; `include_config` has no special handling for restic or rclone jobs. Restore start and completion are persisted, and failed restore attempts are also recorded for later troubleshooting. `POST /restore-verify` requires an existing target directory, applies the same protected-path boundaries, does not modify data, and reports file/byte counts plus whether `.mnemonas-restore/config.toml`, `files/`, `.mnemonas/`, `.mnemonas/index.db`, and `.mnemonas/objects` were found; warnings call out symlinks, special files, or targets that do not look like a complete `storage.root`. Backup failures return `500` with the failed run result in `details`; unknown jobs return `404`; disabled jobs, concurrent operations, local restore/restore-drill operations without any completed snapshot, and non-empty restore targets return `409`.

## WebDAV

WebDAV is served at:

```text
http://localhost:8080/dav
```

By default it uses Basic Auth with credentials from `[webdav]` or generated credentials in `secrets.json`.

Supported core methods include `OPTIONS`, `PROPFIND`, `GET`, `HEAD`, `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, simplified `PROPPATCH`, simplified `LOCK`, and simplified `UNLOCK`.

Browser requests with `Origin`, `Referer`, or `Sec-Fetch-Site` metadata are same-origin checked for WebDAV write methods. Script and WebDAV clients normally do not send those browser-origin headers. WebDAV file and directory-listing responses include `nosniff` and a sandbox CSP to reduce script execution when user files are opened in the browser.

See [WebDAV compatibility](webdav-compatibility.en.md).

## Error Codes

Common error-code categories:

| Category | Examples |
| --- | --- |
| Auth | `UNAUTHORIZED`, `LOGIN_RATE_LIMITED`, `TOKEN_EXPIRED` |
| Request | `BAD_REQUEST`, `INVALID_REQUEST_BODY`, `VALIDATION_ERROR` |
| File | `NOT_FOUND`, `CONFLICT`, `FILE_TOO_LARGE` |
| Share | `SHARE_NOT_FOUND`, `SHARE_EXPIRED`, `SHARE_PASSWORD_RATE_LIMITED` |
| Service | `SERVICE_UNAVAILABLE`, `INTERNAL_ERROR` |

Use the HTTP status code for broad control flow and the JSON error code for user-facing or branch-specific handling.

## Version Notes

This document describes the current main-branch REST API. Released versions, compatibility notes, and change history are tracked by Git tags and [CHANGELOG](../CHANGELOG.en.md).
