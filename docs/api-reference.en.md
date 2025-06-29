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

WebDAV `auth_type = "users"` accepts MnemoNAS user credentials over HTTP Basic and applies role, group, `home_dir`, directory access-rule, home-scoped user-quota, and directory-quota boundaries. WebDAV `auth_type = "basic"` remains a separate global service credential mode.

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
| `507` | User or directory quota exceeded |
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
  "password": "example_password"
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
      "email": "admin@example.com",
      "role": "admin",
      "groups": ["family"],
      "home_dir": "/"
    }
  }
}
```

Cookie-session login also sets `mnemonas_access` and `mnemonas_refresh`. With `X-MnemoNAS-Session-Mode: cookie`, the `data` object omits `access_token` and `refresh_token`.

Refresh accepts either a JSON refresh token body for API clients or the `mnemonas_refresh` cookie for the Web UI. Refresh rotates the refresh token and sets new access/refresh cookies. Responses using the refresh cookie, or `X-MnemoNAS-Session-Mode: cookie`, omit bearer tokens from JSON.

Current user response example:

```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "admin",
      "email": "admin@example.com",
      "role": "admin",
      "groups": ["family"],
      "home_dir": "/"
    }
  }
}
```

Logout revokes the current access token when a valid bearer token or session cookie is present and clears `mnemonas_access`, `mnemonas_refresh`, and the short-lived `mnemonas_download_access` cookie. It still attempts cookie cleanup when the access cookie is expired.

`POST /api/v1/auth/download-session` creates the short-lived download-session cookie for browser preview, thumbnail, and download flows that cannot attach `Authorization` headers. The cookie is `HttpOnly`, `SameSite=Strict`, scoped to `/api/v1`, expires with the current access token, and uses `Secure` when the backend identifies the request as HTTPS.

Logout response example:

```json
{
  "success": true,
  "data": null,
  "message": "logged out successfully"
}
```

Download-session response example:

```json
{
  "success": true,
  "data": null
}
```

Change password request:

```json
{
  "old_password": "current_password",
  "new_password": "new_secure_password"
}
```

Change password response example:

```json
{
  "success": true,
  "data": null,
  "message": "password changed successfully"
}
```

Failed login attempts are rate-limited by username and client address. Client address uses the direct peer unless `server.trusted_proxy_hops` is configured and the request comes from loopback or a proxy address listed in `server.trusted_proxy_cidrs`. When alert channels are configured, a rate-limited login sends a throttled `login_rate_limited` warning event containing only a label-sanitized attempted username and the client address, never passwords or tokens. Empty usernames are reported as `unknown`; invalid or oversized usernames are reported as `invalid username`.

## Admin User Endpoints

Admin role required.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/admin/users` | List users |
| `POST` | `/api/v1/admin/users` | Create user |
| `PUT` | `/api/v1/admin/users/{id}` | Update user metadata, role, home directory, or quota |
| `DELETE` | `/api/v1/admin/users/{id}` | Delete user |
| `POST` | `/api/v1/admin/users/{id}/reset-password` | Reset user password |
| `POST` | `/api/v1/admin/users/{id}/revoke-sessions` | Revoke the user's active sessions |
| `PUT` | `/api/v1/admin/users/{id}/status` | Enable or disable user |

User roles are `admin`, `user`, and `guest`. Non-admin users are scoped by `home_dir` and any matching directory access rules. User responses include `id`, `username`, `email`, `role`, `groups`, `disabled`, `home_dir`, `created_at`, `updated_at`, optional `last_login_at`, `quota_bytes`, and `used_bytes`.

List response example:

```json
{
  "success": true,
  "data": {
    "users": [
      {
        "id": "user-123",
        "username": "admin",
        "email": "admin@example.com",
        "role": "admin",
        "groups": ["family"],
        "disabled": false,
        "home_dir": "/",
        "created_at": "2024-01-01T00:00:00Z",
        "updated_at": "2024-01-15T10:00:00Z",
        "last_login_at": "2024-01-15T10:00:00Z",
        "quota_bytes": 0,
        "used_bytes": 0
      }
    ],
    "total": 1
  }
}
```

Create request example:

```json
{
  "username": "alice",
  "password": "example_password",
  "email": "alice@example.com",
  "role": "user",
  "groups": ["family"],
  "home_dir": "/alice",
  "quota_bytes": 10737418240
}
```

Create response example:

```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "alice",
      "email": "alice@example.com",
      "role": "user",
      "groups": ["family"],
      "disabled": false,
      "home_dir": "/alice",
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-01T00:00:00Z",
      "quota_bytes": 10737418240,
      "used_bytes": 0
    }
  }
}
```

`POST /api/v1/admin/users/{id}/revoke-sessions` invalidates that user's existing Web cookie sessions, access tokens, and refresh tokens without changing the user's password or enabled state. The user must sign in again on the next request.

Usernames are limited to 255 characters and must not contain `/`, `\`, control characters, `.`, or `..`. Passwords must be 8 to 72 bytes.
`home_dir` is optional at creation time and defaults to `/<username>` when omitted. When provided, it is normalized to a clean absolute MnemoNAS path and must not be empty or contain `..` path segments or control characters. The `user` and `guest` roles cannot use `/` as `home_dir`; `admin` may use `/` for the global namespace. `quota_bytes` is optional, and `0` means unlimited.
Group names are normalized to lowercase and may contain only letters, digits, `.`, `_`, and `-`.

`PUT /api/v1/admin/users/{id}` accepts at least one of:

```json
{
  "email": "user@example.com",
  "role": "user",
  "groups": ["family", "editors"],
  "home_dir": "/alice",
  "quota_bytes": 10737418240
}
```

Update response example:

```json
{
  "success": true,
  "data": {
    "user": {
      "id": "user-123",
      "username": "alice",
      "email": "alice@example.com",
      "role": "user",
      "groups": ["family", "editors"],
      "disabled": false,
      "home_dir": "/alice",
      "created_at": "2024-01-01T00:00:00Z",
      "updated_at": "2024-01-02T00:00:00Z",
      "last_login_at": "2024-01-15T10:00:00Z",
      "quota_bytes": 10737418240,
      "used_bytes": 536870912
    }
  },
  "message": "user updated successfully"
}
```

Delete response example:

```json
{
  "success": true,
  "data": null,
  "message": "user deleted successfully"
}
```

After a user is deleted or disabled, public shares created by that user no longer expose metadata, downloads, or folder listings. Those public requests return `404 Not Found` with `SHARE_NOT_FOUND` so the link does not reveal whether an owner account used to exist.

Reset password response example:

```json
{
  "success": true,
  "data": null,
  "message": "password reset successfully"
}
```

Revoke sessions response example:

```json
{
  "success": true,
  "data": {
    "revoked": true
  },
  "message": "user sessions revoked successfully"
}
```

Enable or disable response example:

```json
{
  "success": true,
  "data": {
    "disabled": true
  },
  "message": "user status updated successfully"
}
```

`quota_bytes = 0` means unlimited. When it is greater than zero, server-side quota checks apply to non-admin Web/API uploads, copies, moves, trash restores, and WebDAV PUT/COPY/MOVE writes when `webdav.auth_type = "users"` and the write target is inside that user's `home_dir`. Checks use the current logical size under the `home_dir`; use `storage.directory_quotas` to limit shared directories. Exceeding quota returns `507 Insufficient Storage` with code `QUOTA_EXCEEDED` and details containing `used_bytes`, `quota_bytes`, `required_bytes`, and `available_bytes`. When alert channels are enabled, Web/API upload, copy, move, and trash-restore quota denials also send a `quota_exceeded` warning event with user, home directory, operation, target path, and quota byte details.

`storage.directory_quotas` can define hard limits for MnemoNAS logical directories. Matching Web/API uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations return the same `QUOTA_EXCEEDED` code and add `quota_type="directory"` plus `quota_path` to `details`. Web/API directory quota denials also emit `quota_exceeded` alert events.

`storage.directory_access_rules` can grant shared-directory read/write access by user, group, or role. For non-admin users, a matching rule uses the most specific path and overrides the fallback `home_dir` boundary for that path. Write grants also allow reads; write operations require a write grant.

`webdav.auth_type = "basic"` remains a global service credential compatibility mode and does not carry an application `home_dir` user identity.

Changing the current administrator's own role to a non-admin role is rejected with `SELF_ROLE_CHANGE`. Role or status updates that would remove the last enabled administrator are rejected with `LAST_ADMIN`.

## System Endpoints

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/health` | No | Health check |
| `HEAD` | `/health` | No | Health check status and headers without a response body |
| `GET` | `/api/v1/version` | Usually no | Version/build info |
| `GET` | `/api/v1/setup/` | No | Initial setup status |
| `POST` | `/api/v1/setup/acknowledge` | Admin when auth enabled | Acknowledge initial info |
| `GET` | `/api/v1/stats` | Yes | Storage statistics |
| `GET` | `/api/v1/diagnostics` | Admin | Diagnostic information |
| `GET` | `/api/v1/diagnostics-export` | Admin | Sanitized diagnostic bundle download |
| `GET` | `/api/v1/metrics` | Admin when auth enabled | JSON metrics |

Prometheus cannot directly scrape `/api/v1/metrics` as native exposition format. Use a JSON exporter or conversion layer.

Health check response:

```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:00:00Z",
  "uptime": "24h30m15s",
  "uptime_secs": 88215,
  "version": "<version>",
  "dataplane": {
    "healthy": true,
    "version": "<dataplane-version>",
    "uptime": 86400
  }
}
```

`uptime` keeps the Go duration string, while `uptime_secs` provides whole seconds for stable client display. `status` is downgraded to `degraded` when configured data plane, thumbnail cache, maintenance history, activity log, favorites store, or WebDAV credential subsystems fail to initialize.

### Setup Status

Returns first-run setup status.

```http
GET /api/v1/setup/
```

Example response:

```json
{
  "success": true,
  "is_first_run": true,
  "auth_enabled": true,
  "share_enabled": true,
  "webdav_enabled": true,
  "webdav_auth_type": "basic"
}
```

Notes:

- The endpoint does not return initial usernames or passwords.
- The first-run Web administrator password is written only to `initial-password.txt` next to `auth.users_file`; the default path is `<storage.root>/.mnemonas/initial-password.txt`, and non-interactive startup logs only report that file path.
- The endpoint returns a setup-specific flat JSON payload and does not use the common `data` wrapper.

### Acknowledge Setup Information

Marks the first-run setup information as shown. Later `GET /api/v1/setup/` responses return `is_first_run=false`.

```http
POST /api/v1/setup/acknowledge
```

Authentication:

- When authentication is enabled, administrator access is required.
- When authentication is disabled, the endpoint can be called anonymously.

Request body: none.

Example response:

```json
{
  "success": true,
  "message": "setup acknowledged"
}
```

Failure behavior:

- Returns `401` when authentication is enabled and the caller is not logged in.
- Returns `403` when authentication is enabled and the caller is not an administrator.
- Returns `503` with message `setup acknowledge unavailable` when runtime secrets are unavailable.
- This endpoint also returns setup-specific JSON and does not use the common `data` wrapper.

`GET /api/v1/stats` returns availability flags for each stats group. Admin responses can include disk mount metadata from Linux mountinfo: `disk_mount_point`, `disk_mount_source`, and `disk_mount_options`, which help confirm the filesystem/device or dataset hosting MnemoNAS. Sensitive mount option values such as credentials, usernames, passwords, keys, and tokens are redacted. Admin responses can also include `directory_quota_stats_available` and `directory_quotas` entries with `path`, `quota_bytes`, `used_bytes`, `available_bytes`, `usage_ratio`, `exists`, and `status`. Directory quota `status` is one of `normal`, `warning`, `exceeded`, or `missing`. When auth is enabled, home-scoped non-admin users do not receive global disk, CAS, file-count, or directory-quota stats.

## File Operations

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/files/{path}` | List directory or get file metadata |
| `POST` | `/api/v1/files/{path}` | Upload or overwrite file |
| `DELETE` | `/api/v1/files/{path}` | Delete to trash when trash is enabled |
| `POST` | `/api/v1/files-move` | Move or rename resource |
| `POST` | `/api/v1/files-copy` | Copy file or directory recursively |
| `GET` | `/api/v1/download/{path}` | Authenticated file download or ZIP archive download |
| `POST` | `/api/v1/directories/{path}` | Create directory |

For non-admin callers, directory listing applies the same `home_dir` and most-specific `storage.directory_access_rules` checks to the requested directory and its immediate children; children without read access are omitted from the response. Requests for the root directory `/` return only the user's `home_dir` and top-level entries for readable shared directories, not other global-root contents. When only a nested shared directory is granted, existing ancestor directories may be used for read-only navigation; creating, moving, or copying under those ancestors still requires explicit write grants.

List responses include `capabilities` for the current directory and for each returned item. `read` means the path can be listed or opened for navigation, `concreteRead` means exact-resource read actions such as download, copy source, share, or favorite are allowed, and `write` means mutation actions are allowed for that path or container. For example, root may report `write: true` when upload or create operations are allowed under root while still reporting `concreteRead: false` because root itself is not a downloadable or copyable resource.

`GET /api/v1/download/{path}` returns file bytes by default. Set `download=true` to force an attachment filename. Set `archive=zip` to download the target path as a ZIP archive; this works for directories and individual files, cannot be combined with `version`, requires concrete read access for the target and every included entry, and does not allow read-only navigation ancestors to be archived. ZIP archives are capped at 10000 entries and 20 GiB of file content. Current-file and historical-version downloads support Range requests; ZIP archive downloads do not guarantee Range support.

`POST /api/v1/files/{path}` requires `{path}` to identify a non-root file path. Root or root-equivalent upload targets return `400 Bad Request` with `invalid path`.

`POST /api/v1/directories/{path}` creates one directory when the direct parent directory already exists. If the direct parent directory is absent, the endpoint returns `409 Conflict` and does not create intermediate directories.

Move request:

```json
{
  "from": "/documents/old.txt",
  "to": "/documents/new.txt"
}
```

The target path must not already exist or retain historical version metadata. Directory moves include descendant paths in this target metadata check. Target conflicts return `409 Conflict` before quota checks and do not emit quota alerts.

When a move or rename has completed but a later workspace persistence step fails, the endpoint still returns `200 OK` with `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`. The response body includes `data.warning: true` and `message: "resource moved with persistence warning"`.

Copy request:

```json
{
  "from": "/documents/report.txt",
  "to": "/archive/report.txt"
}
```

Some file mutations may return success with a `Warning` header if the file operation succeeded but later metadata, activity, or cleanup work did not fully complete.

## Thumbnails

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/thumbnails/{path}` | Get generated thumbnail for an image or supported preview |

Download-session cookies are used for preview and thumbnail flows where browser media elements cannot attach Authorization headers. `POST /api/v1/auth/download-session` can be authenticated by the Web UI session cookie or by `Authorization: Bearer <access-token>` and sets `mnemonas_download_access` as an `HttpOnly`, `SameSite=Strict` cookie scoped to `/api/v1`. Thumbnail responses are generated images and include `nosniff` plus a sandbox CSP.

Thumbnail generation rejects sources larger than 100 MiB, image dimensions above 10000x10000, or images above 50 million pixels.

## Version History

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/versions/{path}` | List versions for a file |
| `POST` | `/api/v1/versions/{hash}/restore` | Restore a version to the requested path |

### Restore Version

Restore a file to a specific historical version.

**Authentication**: Required

**Permission**: Admin

```text
POST /api/v1/versions/{hash}/restore
```

**Query parameters**:
- `path`: file path (required)

The `path` value must identify a non-root file path. Root or root-equivalent values return `400 Bad Request` with `invalid path`.

When the version content has already been restored but final workspace metadata persistence fails, the API still returns `200 OK` with `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"` and the response `message` set to `version restored with persistence warning`.

Example request:

```bash
curl -X POST \
  -H "Authorization: Bearer <access-token>" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=/documents/report.txt"
```

Example response:

```json
{
  "success": true,
  "data": {
    "path": "/documents/report.txt",
    "restored": "abc123..."
  },
  "message": "version restored successfully",
  "timestamp": "2024-01-15T10:00:00Z"
}
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

`POST /api/v1/trash/{id}/restore` restores the item to its original path by default. A `path` query parameter restores the item to a custom target path. The custom target must be writable, must be a non-root path, its direct parent directory must already exist, and the target itself must not already exist. Root or root-equivalent custom targets return `400 Bad Request` with `invalid path`. If the direct parent directory is absent, the endpoint returns `409 Conflict` and does not create intermediate directories. If the trash item has historical versions and the original path is occupied by a live file, or another trash item still references an overlapping source or target version metadata path, including a descendant path for directory restores, the endpoint returns `409 Conflict` before quota checks and does not emit a quota alert.

## Search

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/search?q={query}` | Search files by name |

Search results are scoped by configured `home_dir`.

Query parameters:

- `q`: Required search term, up to 100 characters.
- `limit`: Maximum result count. The default is 50 and the maximum is 100.

Search response:

```json
{
  "success": true,
  "data": {
    "query": "report",
    "results": [
      {
        "name": "report.pdf",
        "path": "/documents/report.pdf",
        "isDir": false,
        "size": 1048576,
        "modTime": "2024-01-15T10:00:00Z"
      }
    ],
    "count": 1
  }
}
```

## Share Links

Authenticated management endpoints:

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/shares` | Create share |
| `GET` | `/api/v1/shares` | List shares |
| `GET` | `/api/v1/shares/policy` | Get default policy for newly-created shares |
| `GET` | `/api/v1/shares/{id}` | Get share detail |
| `PUT` | `/api/v1/shares/{id}` | Update share |
| `DELETE` | `/api/v1/shares/{id}` | Delete share |

Create request:

```json
{
  "path": "/documents/report.pdf",
  "type": "file",
  "password": "optional-password",
  "expires_in": "72h",
  "permission": "read",
  "max_access": 100,
  "description": ""
}
```

`GET /api/v1/shares/policy` returns `default_expires_in`, `default_max_access`, and `policy_rules` entries with `path`, `require_password`, `max_expires_in`, and `max_access`.

`type` is `file` or `folder`; an omitted value defaults to `file`. `permission` currently accepts `read` or an omitted value. `password` is optional; non-empty share passwords are limited to 72 bytes. If `expires_in` or `max_access` is omitted, the server applies `share.default_expires_in` and `share.default_max_access`. If the path matches `share.policy_rules`, the most specific path rule wins: `require_password` rejects passwordless requests, while `max_expires_in` and `max_access` cap values above the rule limit. Authenticated share responses include `risk.level` (`none`, `low`, `medium`, `high`) plus optional reason objects so admins can find passwordless, long-lived, broad-folder, unlimited, stale, or soon-expiring links. An enabled share that has never been accessed after 30 days is reported as `unused_enabled`; an enabled share whose last access is more than 90 days old is reported as `stale_enabled`.

Update request:

```json
{
  "enabled": true,
  "password": "optional-password",
  "expires_in": "",
  "permission": "read",
  "max_access": 0,
  "description": ""
}
```

All update fields are optional; omitted fields normally keep their current values. An empty `password` clears the password, an empty `expires_in` clears expiry, and `permission` currently accepts only `read`. Updates to shares that match `share.policy_rules` must also satisfy the path rule. `require_password` rejects updates that would leave a matching share passwordless. `max_expires_in` and `max_access` cap explicit values that clear or exceed the configured limit, and they also cap omitted fields when the stored share currently has no corresponding limit or exceeds the path rule.

Public endpoints:

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/public/shares/{share_id}` | Get public share metadata |
| `POST` | `/api/v1/public/shares/{share_id}/access` | Submit password and receive share cookie |
| `GET` | `/api/v1/public/shares/{share_id}/download` | Download shared file or shared folder ZIP archive |
| `GET` | `/api/v1/public/shares/{share_id}/items?path=subdir` | List shared directory items |
| `GET` | `/api/v1/public/shares/{share_id}/download/{path}` | Download item or ZIP archive from shared directory |

Password-protected shares use an `HttpOnly`, `SameSite=Strict` cookie after password validation. Failed password attempts are rate-limited.

Public share behavior:

- Password-protected shares without a valid access cookie return only `id`, `type`, `has_password`, and `permission`; they do not return `description` or file/folder metadata.
- Public shares, and password-protected shares with a valid access cookie, return `description` and `file_name`, `file_size`, or `folder_items` metadata where applicable.
- Authorized zero-byte files return `file_size: 0`; authorized empty folders return `folder_items: 0`.
- When `max_access > 0` and `access_count` has reached the limit, public access returns `410 Gone` with `SHARE_ACCESS_LIMIT_REACHED`.
- Expired shares return `410 Gone` with `SHARE_EXPIRED`.
- Disabled shares return `410 Gone` with `SHARE_DISABLED`.
- Shares created by a disabled or deleted owner return `404 Not Found` with `SHARE_NOT_FOUND` for public metadata, downloads, and folder listings.
- `access_count` increments on downloads and folder-listing requests. Password validation through `POST /api/v1/public/shares/{share_id}/access` and the compatibility path `POST /s/{share_id}` does not increment it.
- Once a download or folder-listing response has started writing to the client, that request remains counted even if the later stream fails.
- Public share downloads honor HTTP Range requests when the backing file reader supports seeking. Local MnemoNAS storage supports this path for resumable downloads and browser media playback.
- Set `archive=zip` on public download endpoints to download a shared folder root, subfolder, or file as a ZIP archive. Public ZIP archives return `application/zip`, do not guarantee Range support, skip entries no longer visible to the share owner, and are capped at 10000 entries and 20 GiB of file content.
- Unsatisfiable Range requests that return `416 Requested Range Not Satisfiable` do not increment `access_count`.
- Successful password validation sets an `HttpOnly`, `SameSite=Strict` access cookie; later downloads and folder-listing requests use the cookie rather than a password query parameter.
- Public share metadata, password-validation responses, folder-listing responses, and public-download JSON error responses include `Cache-Control: private, no-cache`, `Vary: Cookie`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`.
- Repeated password failures return `429 Too Many Requests` with `SHARE_PASSWORD_RATE_LIMITED`.
- Password failure rate limiting is keyed by share ID and client address. Forwarded headers are ignored by default and are used only when `server.trusted_proxy_hops > 0` and the direct peer is loopback or belongs to `server.trusted_proxy_cidrs`.
- Compatibility paths `GET /s/{share_id}` and `POST /s/{share_id}` return the same public JSON behavior for direct script or non-SPA use.
- Compatibility paths `GET /s/{share_id}/items`, `GET /s/{share_id}/download`, and `GET /s/{share_id}/download/{path}` provide the same folder-listing and download behavior for direct script or non-SPA use.

## Favorites

Favorite paths must normalize to a non-root absolute path. Empty values, the root path, and root-equivalent values such as `.` are rejected with `400 Bad Request` and `MISSING_PATH` before non-admin `home_dir` authorization.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/favorites` | List favorites |
| `POST` | `/api/v1/favorites` | Add favorite |
| `GET` | `/api/v1/favorites/check?path=/documents/file.pdf` | Check one path |
| `POST` | `/api/v1/favorites/check-batch` | Check multiple paths |
| `DELETE` | `/api/v1/favorites/{path}` | Remove favorite |
| `PATCH` | `/api/v1/favorites/{path}` | Update note |

List response:

```json
{
  "success": true,
  "data": {
    "favorites": [
      {
        "path": "/documents/important.pdf",
        "user_id": "user-123",
        "created_at": "2024-01-15T10:00:00Z",
        "note": ""
      }
    ],
    "count": 1
  }
}
```

Add request:

```json
{
  "path": "/documents/report.pdf",
  "note": "quarterly report"
}
```

Add response:

```json
{
  "success": true,
  "data": {
    "path": "/documents/report.pdf",
    "user_id": "user-123",
    "created_at": "2024-01-15T10:00:00Z",
    "note": "quarterly report"
  }
}
```

Check response:

```json
{
  "success": true,
  "data": {
    "path": "/documents/file.pdf",
    "is_favorite": true
  }
}
```

Batch check request:

```json
{
  "paths": ["/file1.txt", "/file2.pdf"]
}
```

Batch check response:

```json
{
  "success": true,
  "data": {
    "favorites": {
      "/file1.txt": true,
      "/file2.pdf": false
    }
  }
}
```

For `DELETE /api/v1/favorites/{path}` and `PATCH /api/v1/favorites/{path}`, `{path}` is URL-encoded by path segment while preserving `/` separators. Successful remove and note-update responses include `success: true`, `data: null`, and a status message.

Remove response:

```json
{
  "success": true,
  "data": null,
  "message": "favorite removed successfully"
}
```

Note update response:

```json
{
  "success": true,
  "data": null,
  "message": "favorite note updated successfully"
}
```

## Activity Log

### List Activity

Return user activity entries.

Notes:

- When authentication is enabled, admins can view the full activity log. Non-admin users receive only entries visible to the current account, and the `user` query parameter cannot bypass that scope.
- System events are also written to the activity log, including periodic `disk_health` checks.
- Manual and scheduled Scrub runs write `scrub` activity entries; Scrub failures, object verification problems, and incomplete result persistence send `scrub_run` events through configured Webhook, Telegram, or SMTP alert channels.
- `share` and `unshare` activity `details` include review metadata such as share type, permission, password requirement, expiry, and access limit; they do not include share passwords, public URLs, or share IDs.
- When the activity log is not configured, the API returns an empty list.
- When the activity log is configured but failed to initialize or is currently unavailable, the API returns `503 Service Unavailable`.

```
GET /api/v1/activity
```

Query parameters:

- `limit`: Result count. The default is 50 and the maximum is 500.
- `offset`: Pagination offset.
- `action`: Filter by action type. Current values are `upload`, `download`, `delete`, `rename`, `move`, `copy`, `create`, `restore`, `share`, `unshare`, `favorite`, `unfavorite`, `favorite_note_update`, `login`, `logout`, `trash_restore`, `trash_delete`, `trash_empty`, `disk_health`, and `scrub`.
- `action_group`: Filter by review group. Current values are `share` for share/unshare events and `risk` for delete, move, rename, share, unshare, permanent trash delete, and trash empty events.
- `path`: Filter by path or directory. The filter matches the path itself, descendants, and path-like activity details such as `from` and `to`.
- `user`: Filter by user.
- `since`: Return entries at or after this RFC3339 timestamp.
- `until`: Return entries at or before this RFC3339 timestamp.

`action` and `action_group` can be combined; the result is their intersection. `path` is normalized using MnemoNAS absolute-path rules and returns `400 Bad Request` when it contains traversal segments. Invalid `action` or `action_group` values, invalid time formats, or a `since` value later than `until`, return `400 Bad Request`.

For non-admin users, `path=/` means the current account's visible scope. A `path` filter outside that scope returns an empty list and does not reveal matches from hidden activity details.

Response example:

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "act-123",
        "timestamp": "2024-01-15T10:00:00Z",
        "action": "upload",
        "path": "/documents/file.pdf",
        "user": "admin",
        "ip": "127.0.0.1",
        "details": {
          "to": "/documents/new.pdf"
        }
      }
    ],
    "total": 100,
    "limit": 50,
    "offset": 0
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### Activity Statistics

Notes:

- When authentication is enabled, admins receive global statistics. Non-admin users receive statistics for the current account's visible activity entries.
- The statistics endpoint supports the same `action`, `action_group`, `path`, `user`, `since`, and `until` query parameters as the list endpoint. When filters are present, `total`, `today`, `by_action`, `by_user`, and `risk_summary` are computed from the filtered records.
- `risk_summary` summarizes high-risk actions, including delete, move, rename, share, unshare, permanent trash delete, and trash empty. `max_10m` is the highest number of matching high-risk actions in any 10-minute window, while `max_10m_started_at` and `max_10m_ended_at` identify the window for focused review.
- When the activity log is not configured, the API returns zero statistics.
- When the activity log is configured but failed to initialize or is currently unavailable, the API returns `503 Service Unavailable`.

```
GET /api/v1/activity/stats
```

Query parameters:

- `action`: Filter by action type. Uses the same values as the list endpoint.
- `action_group`: Filter by review group. Current values are `share` and `risk`.
- `path`: Filter by path or directory. The filter matches the path itself, descendants, and path-like activity details such as `from` and `to`.
- `user`: Filter by user.
- `since`: Count entries at or after this RFC3339 timestamp.
- `until`: Count entries at or before this RFC3339 timestamp.

`action`, `action_group`, `path`, `since`, and `until` use the same error handling as the list endpoint.

For non-admin users, `path=/` counts only records in the current account's visible scope. An inaccessible `path` filter returns zero statistics and does not count records matched only by hidden activity detail paths.

Response example:

```json
{
  "success": true,
  "data": {
    "total": 100,
    "today": 10,
    "by_action": {
      "upload": 50,
      "delete": 10
    },
    "by_user": {
      "admin": 60
    },
    "risk_summary": {
      "total": 14,
      "today": 3,
      "max_10m": 5,
      "max_10m_started_at": "2024-01-15T09:00:00Z",
      "max_10m_ended_at": "2024-01-15T09:08:00Z"
    }
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

### Clear Activity Log (Admin)

```
DELETE /api/v1/activity
```

Response example:

```json
{
  "success": true,
  "data": {
    "message": "Activity log cleared"
  },
  "timestamp": "2024-01-15T10:00:00Z"
}
```

Notes:

- When the activity log is configured but failed to initialize or is currently unavailable, the API returns `503 Service Unavailable` instead of reporting a successful clear.

## Settings

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/settings` | Get current settings |
| `POST` | `/api/v1/settings/access-check` | Check effective read/write access for a user and path |
| `POST` | `/api/v1/settings/access-preview` | Preview a read/write access matrix using unsaved directory rules |
| `POST` | `/api/v1/settings/access-report` | Build a read/write access matrix for all users on one path |
| `POST` | `/api/v1/settings/alerts/test` | Send a test alert through saved alert channels |
| `GET` | `/api/v1/settings/security-check` | Run public-access security self-check |
| `PUT` | `/api/v1/settings` | Update settings |
| `GET` | `/api/v1/settings/webdav-credentials` | Get current WebDAV credential status |

Settings updates can change directory quotas, directory access rules, WebDAV prefix, read-only mode, auth mode, share configuration, favorite configuration, alert configuration, disk-health monitoring, scheduled Scrub maintenance, dataplane connection settings, and retention/versioning policies at runtime. Alert updates include Webhook, Telegram, and SMTP email notification settings; disk-health updates include temperature and media-wear thresholds. Scheduled Scrub updates immediately replace the running background scheduler. Directory quota and access-rule updates are hot-applied to the Web/API and WebDAV runtime. Server listener/TLS changes and CDC chunk-size changes are saved but require restarting the affected service before they take effect.

`server.host` must be empty, `*`, a valid hostname, IPv4, or IPv6 literal, without a port, whitespace, or control characters; set the port through `server.port`. `server.trusted_proxy_hops` controls whether forwarded headers from trusted reverse proxies are honored when evaluating HTTPS request semantics, and `server.trusted_proxy_cidrs` lists non-loopback proxy IPs or CIDRs allowed to supply those headers. `storage.root` remains read-only through the settings API, but `storage.directory_quotas` accepts entries with a clean absolute MnemoNAS path and positive `quota_bytes`. `storage.directory_access_rules` accepts clean absolute MnemoNAS paths plus read/write grants for `*_users`, `*_groups`, and `*_roles`; the most specific matching rule wins, and write grants also allow reads. `webdav.auth_type` supports `users`, `basic`, and `none`; blank values are normalized to `basic`, and `users` requires app auth to remain enabled. `webdav.prefix` is normalized to a `/`-prefixed URL path, must not contain backslash, `?`, `#`, or control characters, and when enabled must not overlap `/`, `/api`, `/s`, or `/health`. Omitting `webdav.password` preserves the existing WebDAV password, while submitting an empty string switches Basic Auth back to the generated password from `secrets.json`. Non-empty `share.base_url` and `alerts.webhook_url` values must be absolute `http` or `https` URLs; `share.base_url` must not contain userinfo, query strings, or fragments, and must use a valid host name or IP address. `share.default_expires_in` must be empty, `0`, or a non-negative Go duration string; `share.default_max_access` must be zero or greater. `share.policy_rules` entries must use clean absolute MnemoNAS paths and set at least one of `require_password`, `max_expires_in`, or `max_access`. Alert `webhook_method` supports `GET` and `POST`; custom webhook headers use `"Key: Value"` strings with valid HTTP token names, case-insensitively unique names, and values without newlines or control characters. `GET /api/v1/settings` does not return Webhook URL or header values; `alerts.webhook_url` and `alerts.webhook_headers` use `<redacted>` placeholders for configured values, and `alerts.webhook_url_configured` plus `alerts.webhook_headers_configured` indicate whether those values exist. `PUT /api/v1/settings` can submit real Webhook URL/header values to update the configuration; submitting the same `<redacted>` placeholder preserves the corresponding existing value. Omitting `alerts.telegram_bot_token` or `alerts.smtp_password` preserves the stored secret; submitting an empty string clears the corresponding stored secret. Clearing `alerts.telegram_bot_token` is invalid while `alerts.telegram_enabled` remains true. When `alerts.telegram_enabled` is true, `telegram_bot_token` and `telegram_chat_id` are required; the bot token cannot contain whitespace, `/`, `?`, or `#` and is never returned by settings or diagnostics responses. When `alerts.email_enabled` is true, `smtp_host`, `smtp_from`, and at least one `smtp_to` recipient are required; `smtp_port` must be 1-65535, and sender/recipient values must be valid email addresses. `disk_health.command` must be a single executable name or absolute path, `disk_health.media_wear_critical_percent` must not be lower than `disk_health.media_wear_warning_percent`, and each `disk_health.devices[].path` must be absolute. `maintenance.scrub.schedule_interval` and `maintenance.scrub.retry_interval` must be positive duration strings, and `maintenance.scrub.max_retries` must be zero or greater. `dataplane.grpc_address` must be a valid `host:port` address with port 1-65535 and no whitespace or control characters. CDC chunk sizes must satisfy `65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864`. Invalid settings return `400 Bad Request` without mutating the running config.

### Send Test Alert

```
POST /api/v1/settings/alerts/test
```

**Requires administrator access**

The endpoint sends one `alert_test` warning event through the currently saved alert channels. It requires `[alerts] enabled = true`, at least one configured Webhook, Telegram, or SMTP email channel, and an available alert runtime. The SMTP email channel counts as configured only when email alerts are enabled and SMTP host, port, sender, and at least one non-empty recipient are present. Test event details contain only `trigger = "manual_test"`, `source = "settings"`, and the channel list; Webhook, Telegram, and SMTP secrets are not included.

Example response:
```json
{
  "success": true,
  "data": {
    "event_type": "alert_test",
    "channels": ["webhook", "email"]
  },
  "message": "test alert sent"
}
```

Disabled alerts or missing channels return `409 Conflict`; unavailable alert runtime returns `503 Service Unavailable`; delivery failures return a generic `500` error without exposing channel secrets.

`POST /api/v1/settings/access-check` accepts `{"username":"alice","path":"/team/report.pdf"}` and returns `read` and `write` decisions. Each decision includes `allowed`, `source`, optional `message`, and the `matched_rule` when a directory access rule decided the result. `source` can be `admin`, `home_dir`, `directory_access_rule`, `invalid_home_dir`, `user_disabled`, `user_not_found`, or `auth_disabled`. When a nested directory grant allows a read-only navigation ancestor, `matched_rule` points to that descendant rule.

Access-check response:

```json
{
  "success": true,
  "data": {
    "username": "alice",
    "user_id": "u1",
    "role": "user",
    "groups": ["family"],
    "home_dir": "/users/alice",
    "path": "/team/report.pdf",
    "read": {
      "mode": "read",
      "allowed": true,
      "source": "directory_access_rule",
      "message": "directory access rule grants read",
      "matched_rule": {
        "path": "/team",
        "read_groups": ["family"]
      }
    },
    "write": {
      "mode": "write",
      "allowed": false,
      "source": "directory_access_rule",
      "message": "directory access rule does not grant write",
      "matched_rule": {
        "path": "/team",
        "read_groups": ["family"]
      }
    }
  }
}
```

`POST /api/v1/settings/access-report` accepts `{"path":"/team/report.pdf"}` and returns the same read/write decisions for every user plus a `summary` with user count, read allows/denials, write allows/denials, and related share counts. The optional `shares` list reports shares that exactly match the path, parent folder shares that cover it, and child shares under the checked directory. It is intended for administrator permission checks before changing shared-directory or share rules.

Access-report response:

```json
{
  "success": true,
  "data": {
    "path": "/team/report.pdf",
    "summary": {
      "users": 2,
      "read_allowed": 1,
      "read_denied": 1,
      "write_allowed": 1,
      "write_denied": 1,
      "related_shares": 1,
      "active_related_shares": 1,
      "password_protected_shares": 1
    },
    "users": [
      {
        "username": "alice",
        "user_id": "u1",
        "role": "user",
        "groups": ["family"],
        "home_dir": "/users/alice",
        "path": "/team/report.pdf",
        "read": { "mode": "read", "allowed": true, "source": "directory_access_rule" },
        "write": { "mode": "write", "allowed": true, "source": "directory_access_rule" }
      }
    ],
    "shares": [
      {
        "id": "share-id",
        "path": "/team",
        "type": "folder",
        "created_by": "u1",
        "relation": "covers_path",
        "enabled": true,
        "active": true,
        "has_password": true,
        "access_count": 0,
        "max_access": 0,
        "url": "/s/share-id"
      }
    ]
  }
}
```

`POST /api/v1/settings/access-preview` accepts `{"path":"/team/report.pdf","directory_access_rules":[...]}` and returns the same user matrix and related-share impact using only the supplied unsaved rules. It does not persist settings and returns `preview: true`. Nested directory grants are also evaluated as read-only navigation ancestors, so the preview can be used before saving family or small-team shared-directory rules.

Access-preview response:

```json
{
  "success": true,
  "data": {
    "path": "/team/report.pdf",
    "preview": true,
    "summary": {
      "users": 1,
      "read_allowed": 1,
      "read_denied": 0,
      "write_allowed": 0,
      "write_denied": 1,
      "related_shares": 0,
      "active_related_shares": 0,
      "password_protected_shares": 0
    },
    "users": [
      {
        "username": "alice",
        "user_id": "u1",
        "role": "user",
        "groups": ["family"],
        "home_dir": "/users/alice",
        "path": "/team/report.pdf",
        "read": { "mode": "read", "allowed": true, "source": "directory_access_rule" },
        "write": { "mode": "write", "allowed": false, "source": "directory_access_rule" }
      }
    ]
  }
}
```

WebDAV credentials response:

```json
{
  "success": true,
  "data": {
    "enabled": true,
    "url": "/dav/",
    "auth_type": "basic",
    "username": "admin",
    "password": "***"
  }
}
```

The `password` field is present only when the running WebDAV service uses an automatically generated Basic Auth password. Custom WebDAV passwords are not returned.

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
      "trusted_proxy_cidrs": [],
      "dataplane_grpc_addr": "127.0.0.1:9090",
      "dataplane_http_addr": "127.0.0.1:9091",
      "webdav_enabled": true,
      "webdav_auth_type": "basic",
      "smb_enabled": false,
      "allow_unsafe_no_auth": false,
      "share_enabled": false
    }
  }
}
```

`data.status` and `checks[].status` use `pass`, `warning`, or `block`; `block` dominates `warning`, and `warning` dominates `pass` for the aggregate status. Current check IDs include `auth_enabled`, `unsafe_no_auth_override`, `https_request`, `public_http_exposure`, `trusted_proxy_or_tls`, `forwarded_proto_trust`, `server_listen`, `admin_accounts`, `dataplane_listen`, `dataplane_http_listen`, `webdav_auth`, `smb_preview`, `share_base_url`, and `initial_password_file`. When WebDAV is enabled, `webdav_auth` checks the authentication mode; `auth_type = "none"` is reported as `block` on non-loopback listeners, and global Basic Auth passwords that are explicit common placeholders or shorter than 16 characters are reported as `warning` with only a `password_risk` type, never the password value. `forwarded_proto_trust` checks `X-Forwarded-Proto` against trusted-proxy settings: the header without `trusted_proxy_hops` is a `warning`, the header from an untrusted direct peer is a `block`, and a trusted direct peer forwarding a value other than `https` is a `warning`. When sharing is enabled, `share_base_url` checks the public share-link base URL; HTTP, a non-443 HTTPS port, URL userinfo, query strings, fragments, or an invalid host name is reported as `block`, while empty values, a different host, or a base path ending in the `/s` sharing route remain manual-review warnings. The endpoint can verify only what the MnemoNAS process can observe: runtime configuration and the current request's proxy/TLS semantics. It cannot directly verify cloud security groups, public routing, externally exposed ports, or certificate-chain validity. Public deployments should still run `sudo mnemonas-doctor --public-domain <domain>` on the server and confirm in the cloud console that only `80/443` are publicly reachable.

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
| `POST` | `/api/v1/maintenance/backups/{id}/retention-check` | Check local or remote retention state |
| `POST` | `/api/v1/maintenance/backups/batch-restore-preview` | Preview multiple explicit restore targets without writing data |
| `POST` | `/api/v1/maintenance/backups/batch-restore` | Restore multiple backup jobs or targets sequentially |
| `POST` | `/api/v1/maintenance/backups/{id}/restore` | Restore a supported backup job into a safe target directory |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-drill` | Restore-drill the latest completed snapshot |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-preview` | Preview an explicit restore without writing target data |
| `GET` | `/api/v1/maintenance/backups/{id}/restore-report` | Download a JSON restore summary for one backup job |
| `POST` | `/api/v1/maintenance/backups/{id}/restore-verify` | Verify a restored target directory without modifying it |
| `GET` | `/api/v1/diagnostics-export` | Export diagnostic bundle |

Restore preview request:
```json
{
  "target_path": "/mnt/restore/mnemonas",
  "include_config": true
}
```

Restore preview response:
```json
{
  "success": true,
  "data": {
    "id": "20260509T035900.000000000Z",
    "job_id": "external-disk",
    "status": "completed",
    "started_at": "2026-05-09T03:59:00Z",
    "finished_at": "2026-05-09T03:59:01Z",
    "duration_ms": 1000,
    "source": "/srv/mnemonas",
    "destination": "/mnt/backup-drive/mnemonas",
    "target_path": "/mnt/restore/mnemonas",
    "file_count": 42,
    "total_bytes": 1048576,
    "config_available": true,
    "config_included": true,
    "sample_paths": ["docs/note.txt", ".mnemonas-restore/config.toml"],
    "preflight_checks": [
      {
        "id": "target_scope",
        "status": "passed",
        "title": "目标路径隔离",
        "detail": "目标目录位于当前数据目录、备份来源和本地备份目标之外。"
      },
      {
        "id": "target_state",
        "status": "passed",
        "title": "目标目录状态",
        "detail": "目标目录尚不存在；恢复会先写入临时目录，再安装到该路径。"
      },
      {
        "id": "backup_content",
        "status": "passed",
        "title": "备份内容",
        "detail": "预览发现 42 个文件，预计恢复 1 MB。"
      },
      {
        "id": "target_capacity",
        "status": "passed",
        "title": "目标容量",
        "detail": "目标文件系统可用空间 100 GB，预计恢复 1 MB。"
      },
      {
        "id": "config_restore",
        "status": "passed",
        "title": "配置文件",
        "detail": "本地快照包含配置文件，并将恢复到 .mnemonas-restore/config.toml。"
      }
    ],
    "warnings": [],
    "cutover_checklist": ["Run read-only verification on the restored directory first"],
    "rollback_checklist": ["If cutover fails, stop services and point storage.root back to the previous directory"]
  }
}
```

`restore-preview` reuses explicit restore target safety validation and returns `preflight_checks`, `warnings`, `cutover_checklist`, and `rollback_checklist`. Preflight covers target isolation, `target_state`, backup content, target filesystem capacity, and config handling. `target_state` distinguishes two allowed states: the target directory does not exist, or the target directory already exists and is empty. Missing targets use the parent directory for the capacity probe; existing empty target directories use the target directory's filesystem. `preflight_checks[].status` can be `passed`, `warning`, or `failed`; `status = "warning"` means restore can continue after review, while `status = "failed"` prevents the Maintenance page from starting restore and is rejected by server-side preflight before `/restore` writes data. `warnings` aggregates warning and failed preflight details for preview cards, batch previews, and restore history.

Batch preview response:
```json
{
  "success": true,
  "data": {
    "id": "20260509T035901.000000000Z",
    "status": "completed",
    "started_at": "2026-05-09T03:59:01Z",
    "finished_at": "2026-05-09T03:59:02Z",
    "duration_ms": 1000,
    "items": [
      {
        "index": 0,
        "job_id": "external-disk",
        "target_path": "/mnt/restore/mnemonas-a",
        "include_config": true,
        "status": "completed",
        "preview": {
          "id": "20260509T035900.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T03:59:00Z",
          "finished_at": "2026-05-09T03:59:01Z",
          "duration_ms": 1000,
          "source": "/srv/mnemonas",
          "destination": "/mnt/backup-drive/mnemonas",
          "target_path": "/mnt/restore/mnemonas-a",
          "file_count": 12,
          "total_bytes": 4096,
          "config_available": true,
          "config_included": true,
          "warnings": []
        }
      }
    ],
    "total_files": 12,
    "total_bytes": 4096,
    "warning": false,
    "warnings": []
  }
}
```

Batch restore response:
```json
{
  "success": true,
  "data": {
    "id": "20260509T040001.000000000Z",
    "status": "completed",
    "started_at": "2026-05-09T04:00:01Z",
    "finished_at": "2026-05-09T04:00:02Z",
    "duration_ms": 1000,
    "items": [
      {
        "index": 0,
        "job_id": "external-disk",
        "target_path": "/mnt/restore/mnemonas-a",
        "include_config": true,
        "status": "completed",
        "restore": {
          "id": "20260509T040000.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T04:00:00Z",
          "finished_at": "2026-05-09T04:00:01Z",
          "duration_ms": 1000,
          "target_path": "/mnt/restore/mnemonas-a",
          "config_restored": true,
          "file_count": 12,
          "verified_bytes": 4096
        },
        "verify": {
          "id": "20260509T040005.000000000Z",
          "job_id": "external-disk",
          "status": "completed",
          "started_at": "2026-05-09T04:00:05Z",
          "finished_at": "2026-05-09T04:00:06Z",
          "duration_ms": 1000,
          "source": "/srv/mnemonas",
          "destination": "/mnt/backup-drive/mnemonas",
          "target_path": "/mnt/restore/mnemonas-a",
          "file_count": 12,
          "verified_bytes": 4096,
          "config_found": true,
          "files_dir_found": true,
          "internal_dir_found": true,
          "index_found": true,
          "objects_dir_found": true,
          "looks_like_storage_root": true
        },
        "warnings": []
      }
    ],
    "total_files": 12,
    "verified_bytes": 4096,
    "warning": false,
    "warnings": []
  }
}
```

Maintenance endpoints are admin-oriented and may be long-running. The Web UI exposes the same operations from maintenance pages.
Scrub object errors return stable public `errors[].message` values; lower-level IO, path, and verification details are kept in server logs.
Manual scrub runs write `scrub` activity-log entries. When `[maintenance.scrub] enabled = true`, the server runs full Scrub jobs in the background as the system user according to `schedule_interval`; failed runs retry after `retry_interval` up to `max_retries`. Scheduled runs use the same maintenance history, activity-log details, result shape, and alert events as manual runs. Scrub failures, object verification problems, and incomplete result persistence send `scrub_run` events through configured Webhook/Telegram/SMTP alert channels.
`GET /api/v1/maintenance/disk-health` uses `[disk_health]` and `smartctl --json --all` to report `disabled`, `ok`, `warning`, `critical`, or `unavailable`. Missing devices, SMART failures, serial mismatches, critical temperatures, NVMe critical warnings, exhausted spare capacity, media-wear thresholds, and media errors affect device status. Periodic checks that find warning, critical, or unavailable status write a `disk_health` activity-log entry at `/system/disk-health` for the `system` user. When `[alerts]` has Webhook, Telegram, or SMTP email configured, periodic disk-health checks send `disk_health` events for warning, critical, and unavailable states. Activity entries and alert events use the configured device `name` in summaries; unnamed devices use a generic label and do not include full device paths or serial numbers. Full device paths and SMART details are returned only by the administrator maintenance endpoint.
`GET /api/v1/diagnostics` and `/diagnostics-export` include sanitized filesystem stats. When `filesystem.disk_stats_available=true`, `filesystem.disk_*` can include capacity values, `disk_filesystem_type`, Linux mountinfo metadata (`disk_mount_point`, `disk_mount_source`, and redacted `disk_mount_options`), and `disk_native_data_checksum_support`. Both endpoints set `Cache-Control: no-store` because diagnostics can contain operational state. `/diagnostics-export` returns an attachment and sets root `schema_version = 1`.
`GET /api/v1/diagnostics` and `/diagnostics-export` expose only alert-channel booleans for Webhook, Telegram, and SMTP email. The SMTP email boolean is true only when email alerts are enabled and SMTP host, port, sender, and at least one non-empty recipient are present. Diagnostics never include Webhook URL/header values, Telegram bot tokens, SMTP host, SMTP username, SMTP password, sender address, or recipient addresses.
`GET /api/v1/diagnostics` and `/diagnostics-export` include a sanitized `maintenance` summary with `history_ready`, `[maintenance.scrub]` schedule settings, the latest Scrub status/time, and the retry count for the latest failed Scrub.
`GET /api/v1/diagnostics` and `/diagnostics-export` include sanitized `smb` preview state. Current builds do not start an SMB/Samba listener, so `runtime_available=false` means the configured SMB shares are not mountable; diagnostics expose share counts and runtime state but never SMB credential contents.
`GET /api/v1/maintenance/objects` accepts an optional `cursor` query parameter from the previous `next_cursor`; non-empty cursors must be 64-character hexadecimal object hashes.
Backup endpoints operate on jobs configured under `[[backup.jobs]]`. Supported job types are `local`, `restic`, and `rclone`. Local jobs copy into `destination/<job-id>/snapshots/<run-id>/` and can prune old snapshots by `max_snapshots` and `max_age`. Restic jobs invoke `restic -r <repository> --password-file <password_file> backup <source>` and optionally `restic check`; rclone jobs invoke `rclone sync <source> <remote>` and optionally `rclone check --one-way`. External commands are executed without a shell; `command` must be a bare executable name or absolute path, and `extra_args` are appended to backup commands as argv entries. Restore commands do not reuse backup-specific extra args. Backup runs reject symlinks in the `source` tree; `rclone` restore drills apply the same check before remote verification. `password_file` and `config_file` must be regular files outside `source` and `storage.root`. API job views, run results, restore or preview results, restore reports, batch restore results, and backup alert events redact userinfo, tokens, passwords, secrets, and key parameters embedded in display fields such as `repository`, `remote`, `destination`, `target_path`, `snapshot_path`, `manifest_path`, and `config_path`; the same patterns are redacted from API-visible backup `error_message`, `warnings`, preflight details, and alert-event error details. Restic/rclone commands still receive the original configured values. Clients that chain `restore-preview`, `restore`, and `restore-verify` should retain and reuse the original request `target_path`; a redacted response `target_path` is intended only for display. Jobs may define `disabled`, `schedule_interval`, `schedule_window_start`, `schedule_window_end`, `stale_after`, `restore_drill_stale_after`, `max_snapshots`, `max_age`, and `retention_policy`; a positive `schedule_interval` enables the in-process scheduler. If both schedule-window fields are set, automatic runs only start inside that server-local `HH:MM` window, while manual run-now operations are unaffected. Job views include backup `health_status` (`ok`, `manual`, `running`, `due`, `stale`, `failed`, or `disabled`), `retention_status`, and `restore_drill_status` plus optional messages. Successful backups now run a retention check automatically, and `POST /retention-check` can run it manually. Local checks count the local snapshot range, restic checks run `restic snapshots --json --tag mnemonas --tag job:<id>`, and rclone checks run `rclone lsjson <remote> --recursive --files-only`; results persist as `last_retention_check` and feed `retention_status`/`retention_message`. `retention_policy` marks restic/rclone remote retention as externally confirmed; otherwise remote jobs report a retention warning. `restore_drill_stale_after` defaults to 30 days when empty or omitted and drives restore-drill reminder status; when alert channels are configured, stale or missing restore drills send rate-limited `backup_restore_drill` warning events with `trigger=restore_drill_reminder` and persist `last_restore_drill_reminder_at`. Restore-drill history is capped to the latest 20 entries and records status, file/byte counts, artifact paths, failure messages, and stable `failure_category` values for failed drills. Current categories are `no_snapshot`, `unsupported_job_type`, `unsafe_path`, `integrity_check`, `external_command`, `cancelled`, `io`, and `unknown`, and they are forwarded to alert event details. Job views also return `restore_drill_stats`, which summarizes total runs, successes, failures, success rate, consecutive successes or failures, latest success/failure time, latest failure message, and latest failure category across that retained window. Restore history is also capped to the latest 20 entries and records target path, status, file/byte counts, preflight checks, warnings, rollback/cutover checklists, and failure messages; `last_restore_verify` persists the latest read-only post-restore verification result after page refresh. Job views return `last_matching_restore_verify` when the latest restore has a matching read-only verification, and `restore_report_findings` with the same pending findings used by restore reports. `GET /restore-report` downloads an `application/json` attachment with the job view, latest backup, retention check, restore drill, restore-drill history and stats, latest restore, latest restore verification, `last_matching_restore_verify` for the latest restore when available, restore history, and findings for handoff or incident records. When `[alerts] enabled = true` and Webhook, Telegram, or SMTP email is configured, backup failures, explicit restore failures or warnings, post-restore read-only verification failures or warnings, restore-drill failures, stale/missing restore-drill reminders, retention-check failures/warnings, and backup-warning runs send events with type `backup_run`, `backup_restore`, `backup_restore_verify`, `backup_restore_drill`, or `backup_retention_check`, level `warning` or `critical`, and task/run/error details with empty or zero-value fields omitted, including `target_path` when relevant, redacted error text, backup target values, and manifest path values. `POST /run` accepts an empty body or `{}`. `POST /retention-check` accepts an empty body or `{}` and returns `snapshot_count`, `file_count`, `total_bytes`, snapshot time range, `warning`, and `warnings`; failures return `500` with the failed check in `details`. `POST /restore-drill` accepts optional `{"keep_artifact": true}`; local jobs temporarily restore and verify the latest snapshot, restic jobs run `restic check`, and rclone jobs run `rclone check --one-way`. `POST /restore-preview` validates the same target rules as restore but does not create target data or write restore history; it returns `preflight_checks`, `warnings`, `cutover_checklist`, and `rollback_checklist` for target isolation, target state, backup content, target filesystem capacity, and config handling. Local jobs summarize the latest manifest, restic jobs run `restic ls latest --json --tag mnemonas --tag job:<id> --path <source>`, and rclone jobs run `rclone lsjson <remote> --recursive --files-only`. `POST /batch-restore-preview` accepts `{"items":[{"job_id":"external-disk","target_path":"/absolute/restore/a","include_config":true}]}` with at most 20 items, rejects duplicate or nested target paths in the same batch, and returns per-item preview status, `error_message`, and warnings without writing target data or restore history. `POST /batch-restore` uses the same request shape, executes items sequentially, runs read-only `restore-verify` after every successful restore, and returns per-item `restore`, `verify`, `warnings`, and `error_message` fields. Top-level `total_files` and `verified_bytes` aggregate completed items' read-only verification results. Batch restore error and warning text uses the same remote-target credential redaction. Partial failures return overall `status="completed"` with `warning=true`; all item failures return `status="failed"`, so clients must inspect `items[]`. `POST /restore` supports local, restic, and rclone jobs and requires `{"target_path": "/absolute/restore/path", "include_config": true}`. The target must not contain control characters and must be outside `storage.root`, the backup source, and any local backup destination or repository. Its parent must exist, and the target must not exist or must be empty. The server reruns the same restore preflight before writing; failed preflight checks reject the restore and are persisted with the failed restore result. Local restore copies snapshot `data/` contents into the target root, verifies size and SHA-256, and restores config to `.mnemonas-restore/config.toml` when requested. Restic restore runs `restic restore latest --target <staging> --tag mnemonas --tag job:<id> --path <source>`, then installs the restored source directory contents into the target root after rejecting restored symlinks and special files. Rclone restore runs `rclone copy <remote> <staging>` and then `rclone check <remote> <staging> --one-way`; restored symlinks and special files are rejected before the server installs the staging directory into the target path. `include_config` has no special handling for restic or rclone jobs. Restore start and completion are persisted, and failed restore attempts are also recorded for later troubleshooting. `POST /restore-verify` requires an existing target directory, applies the same protected-path boundaries and control-character rejection, does not modify data, persists the latest verification report as `last_restore_verify`, and reports file/byte counts plus whether `.mnemonas-restore/config.toml`, `files/`, `.mnemonas/`, `.mnemonas/index.db`, and `.mnemonas/objects` were found; warnings call out symlinks, special files, or targets that do not look like a complete `storage.root`. For local jobs it compares against the latest successful restore snapshot for the same target when available, otherwise the latest local snapshot, and returns the comparison `snapshot_path` and `manifest_path`. Invalid restore `target_path` values and invalid batch restore request entries return `400`; backup task execution failures caused by configured paths, backup source contents, or external commands return `500` with the failed run, drill, or restore result in `details`; unknown jobs return `404`; disabled jobs, concurrent operations, local restore/restore-drill operations without any completed snapshot, and non-empty restore targets return `409`.
Backup, restore, restore-drill, read-only verification, and retention-check operations persist a `running` record before execution. During service startup, `running` records left by a previous process exit are marked failed and written back to the state file.
Job views and restore reports associate `last_restore_verify` with `last_restore` only when the latest restore completed successfully, the target path matches, and the verification timestamp is not earlier than the latest restore completion time. Job views expose `last_matching_restore_verify` and `restore_report_findings` for the same matched verification and pending findings as restore report `last_matching_restore_verify` and `findings`. Job views and restore reports copy the matched result into `last_matching_restore_verify`; otherwise the field is omitted and findings state that the latest restore still needs a matching read-only verification. When the latest restore is still running, restore report findings state that the restore has not completed and avoid attaching older verification results to that restore.
For the batch preview response, per-item preview warnings are reported under `items[].preview.warnings`; aggregate messages are reported under top-level `warnings`.

Local backup destinations reject existing symlink path components. Local restore previews, restores, and restore drills recheck that destination before reading snapshot manifests or creating drill artifacts. The same symlink path-component check applies to `POST /restore-preview`, `POST /restore`, and `POST /restore-verify` target paths.

## WebDAV

WebDAV is served at:

```text
http://localhost:8080/dav
```

By default it uses the legacy global Basic Auth credentials from `[webdav]` or generated credentials in `secrets.json`. Set `webdav.auth_type = "users"` to mount with MnemoNAS user accounts and per-user `home_dir` boundaries; top-level navigation entries for granted shared directories are also listed at the WebDAV root for regular users. Ancestor entries synthesized for nested grants are read-only navigation; writes still require a matching write grant.

Supported core methods include `OPTIONS`, `PROPFIND`, `GET`, `HEAD`, `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, simplified `PROPPATCH`, simplified `LOCK`, and simplified `UNLOCK`. `MKCOL` returns `409 Conflict` when the direct parent directory does not exist.

For WebDAV `MOVE`, a destination path that does not exist but retains historical version metadata returns `409 Conflict`; directory moves also check descendant version metadata under the destination path. This target conflict is returned before user-quota or directory-quota checks.

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
