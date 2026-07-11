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

Browser authentication cookie names and paths depend on the current request mode:

- HTTPS mode uses `__Host-mnemonas_access`, `__Host-mnemonas_refresh`, and `__Host-mnemonas_download_access`. All three cookies use `Secure`, `Path=/`, and no `Domain` attribute.
- Local HTTP mode uses `mnemonas_access`, `mnemonas_refresh`, and `mnemonas_download_access`. Access and download cookies use `/api/v1`; the refresh cookie uses `/api/v1/auth`.
- HTTPS requests parse only the `__Host-` names, and HTTP requests parse only the unprefixed names; neither mode falls back to the other namespace. Authentication is rejected when one request contains different values for the same cookie name, or when access and download cookies resolve to different accounts.

Login and refresh set the access and refresh cookies; the download-session endpoint sets the download cookie. Browser clients can send `X-MnemoNAS-Session-Mode: cookie`; in that mode the JSON response omits bearer tokens and returns only user/session metadata.

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
- `199 MnemoNAS "backup run completed with warnings"`
- `199 MnemoNAS "delete cleanup incomplete"`
- `199 MnemoNAS "trash delete cleanup incomplete"`

Only failures explicitly classified as persistence or cleanup warnings return a success status. Hard failures in a durable Trash transfer return `500 INTERNAL_ERROR` and preserve recovery evidence. Clients should inspect the HTTP `Warning` header in addition to the JSON body.

## MnemoNAS Path Convention

File, directory, favorite, activity-filter, `home_dir`, directory-quota, and directory access-rule fields use MnemoNAS logical absolute paths:

- Paths use `/` separators and normalize to a leading-`/` form.
- Control characters and standalone `.` or `..` path segments are invalid, while legal names containing repeated dots, such as `foo..txt`, remain valid.
- The root path `/` is valid only where an endpoint explicitly allows it.
- URL path parameters are encoded by path segment while preserving `/` separators.

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
      "home_dir": "/",
      "must_change_password": true
    }
  }
}
```

Cookie-session login sets the access and refresh cookies for the current HTTPS or HTTP mode. The refresh-cookie path covers the authentication endpoints so logout can still revoke the current session after the access cookie expires. With `X-MnemoNAS-Session-Mode: cookie`, the `data` object omits `access_token` and `refresh_token`.

Each login durably registers an active login session before the first tokens are issued. MnemoNAS retains at most 64 active sessions per user and 4096 active sessions globally. Login returns `429 REFRESH_SESSION_LIMIT` when either limit is reached.

Refresh accepts either a JSON refresh-token body for API clients or the Web UI refresh cookie for the current request mode. Refresh rotates the refresh token and sets new access/refresh cookies. Rotated tokens retain the absolute session expiry established at login instead of extending the session indefinitely. Each refresh token can succeed only once. Reusing a rotated token returns `401 TOKEN_REVOKED` and revokes child tokens already issued in the same login session.

A login session can rotate at most once every 30 seconds. An earlier rotation returns `429 REFRESH_RATE_LIMITED` with `Retry-After: 30`. Responses using the refresh cookie, or `X-MnemoNAS-Session-Mode: cookie`, omit bearer tokens from JSON.

If authentication-state persistence definitely fails before the atomic rename, issuance and rotation do not publish new cookies, and logout does not clear existing cookies. If the rename has committed but the parent-directory sync is uncertain, the mutation remains successful and the response includes `Warning: 199 MnemoNAS "auth state persistence incomplete"`. When the authentication time lease has expired and cannot be renewed, protected requests and refresh return `503 TOKEN_STATE_UNAVAILABLE` and preserve existing cookies.

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
      "home_dir": "/",
      "must_change_password": true
    }
  }
}
```

Logout removes the current login session from the authoritative registry. This immediately invalidates its access, refresh, and rotated tokens without affecting the same user's other independent logins. The Web UI submits session cookies; API clients may also submit `{"refresh_token":"<refresh-token>"}` in the request body. If the access cookie has expired, a valid refresh cookie or request-body refresh token still identifies and revokes the session. A definite revocation-persistence failure returns `500` and preserves session cookies for retry. A successful revocation, including one with an uncertain final directory sync, clears the access, refresh, and short-lived download cookies for the current request mode.

`POST /api/v1/auth/download-session` creates the short-lived download-session cookie for browser preview, thumbnail, and download flows that cannot attach `Authorization` headers.

The cookie is `HttpOnly`, `SameSite=Strict`, and expires with the current access token. HTTPS mode sets `__Host-mnemonas_download_access` with `Secure`, `Path=/`, and no `Domain` attribute. Local HTTP mode sets `mnemonas_download_access` with the `/api/v1` path.

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
  "new_password": "new_secure_password",
  "expected_user_id": "current_session_user_id"
}
```

`expected_user_id` is required and must match the current user ID in the authenticated request context. A mismatch returns `409 AUTH_SCOPE_CHANGED` without changing any account password.

Change password response example:

```json
{
  "success": true,
  "data": null,
  "message": "password changed successfully"
}
```

After a successful change, the server increments the account credential version, revokes every active session for the account, and clears the access, refresh, and download cookies for the current request mode. The response does not issue replacement tokens, so the client must sign in again with the new password.

When the request has been sent and the single-page application can still observe a transport interruption, in-app navigation, or a gateway or proxy failure response without a recognized MnemoNAS error code, the Web client treats the result as unconfirmed, clears browser authentication state, and displays sign-in guidance to try the new password first and the previous password only if the new password fails. A hard refresh, tab close, or browser-process termination can stop JavaScript before this handling runs, so immediate state clearing and guidance are not guaranteed; these interruptions must also be treated as unconfirmed outcomes.

If the password changed but session revocation or authentication-state persistence could not be fully confirmed, the endpoint still returns HTTP 200, adds `Warning: 199 MnemoNAS "auth state persistence incomplete"`, and returns:

```json
{
  "success": true,
  "data": {
    "warning": true
  },
  "message": "password changed with persistence warning"
}
```

Password-change errors:

| HTTP status | Code | Meaning |
|---|---|---|
| `400` | `MISSING_PASSWORD` | The current or new password is empty |
| `400` | `MISSING_EXPECTED_USER_ID` | The request does not include `expected_user_id` |
| `401` | `INVALID_PASSWORD` | The current password is incorrect |
| `400` | `PASSWORD_TOO_SHORT` | The new password is shorter than 8 UTF-8 bytes or contains only whitespace |
| `400` | `PASSWORD_TOO_LONG` | The new password exceeds 72 UTF-8 bytes |
| `400` | `PASSWORD_UNCHANGED` | The new password matches the current password |
| `409` | `AUTH_SCOPE_CHANGED` | `expected_user_id` does not match the current user in the authenticated request context |
| `500` | `PASSWORD_ERROR` | The password state could not be updated |

The authentication middleware can also return `MISSING_AUTH_HEADER`, `INVALID_AUTH_HEADER`, `INVALID_TOKEN`, `TOKEN_EXPIRED`, `TOKEN_REVOKED`, `USER_NOT_FOUND`, `USER_DISABLED`, or `TOKEN_STATE_UNAVAILABLE`. Apart from success and persistence-warning responses, the endpoint does not actively clear a valid session.

The automatically created bootstrap administrator returns `must_change_password=true`. Successful login and session refresh retain the server-side `initial-password.txt` file so the only persistent copy of the bootstrap credential is not lost before password change. While the flag is `true`, the authenticated session can access only current-user information, password change, and logout endpoints; other protected endpoints return `403 PASSWORD_CHANGE_REQUIRED`. After the current user sets a password different from the current password through `POST /api/v1/auth/password`, the flag becomes `false`, the initial-password file is removed, and the credential-version change invalidates previous access and refresh tokens. Reusing the current password returns `400 PASSWORD_UNCHANGED`. An administrator reset for another user restores the required-change state; an administrator resetting their own password follows the self-change behavior.

Login protection combines a credential-check limit with consecutive-failure lockout:

- Client address uses the direct peer by default.
- The server parses forwarded headers only when `server.trusted_proxy_hops` is configured and the request comes from loopback or a proxy address listed in `server.trusted_proxy_cidrs`.
- Each client IP can perform at most 12 password-credential checks in a fixed 10-second window. Excess requests return `429 LOGIN_RATE_LIMITED` before bcrypt runs.
- Requests admitted by the credential-check window are still counted by normalized username and client IP. Reaching the failure threshold applies a short lockout.
- When alert channels are configured, a rate-limited login sends a throttled `login_rate_limited` warning event.
- Event details contain only the `trigger`, `key_scope`, `username_status`, and `client_ip_scope` classification fields, never raw usernames, client addresses, passwords, or tokens.
- `username_status` is `unknown`, `invalid`, or `provided`; `client_ip_scope` is `public`, `private`, `loopback`, `link_local`, `multicast`, `unspecified`, or `unknown`.

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

User roles are `admin`, `user`, and `guest`. Non-admin users are scoped by `home_dir` and any matching directory access rules.

User responses include `id`, `username`, `email`, `role`, `groups`, `disabled`, `home_dir`, `created_at`, `updated_at`, optional `last_login_at`, `must_change_password`, optional `password_changed_at`, `quota_bytes`, and `used_bytes`. Password-change time is present only in administrator user-management responses, not normal login or current-user responses. List responses also return `quota_history_available` and `quota_history`; the server keeps aggregate quota-change snapshots with tiered retention: all changes from the latest 30 days, the latest daily snapshot within 1 year, the latest monthly snapshot within 3 years, and at most 512 retained entries. If the history file cannot be written, the availability flag is `false` and the user list still returns.

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
    "total": 1,
    "quota_history_available": true,
    "quota_history": [
      {
        "captured_at": "2024-01-15T10:00:00Z",
        "total_count": 1,
        "active_count": 1,
        "limited_count": 0,
        "warning_count": 0,
        "exceeded_count": 0,
        "attention_count": 0,
        "used_bytes": 0,
        "limited_used_bytes": 0,
        "quota_bytes": 0
      }
    ]
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

User creation and update fields use these rules:

- Usernames are limited to 255 characters and must not contain `/`, `\`, control characters, `.`, or `..`.
- Passwords must contain 8 through 72 UTF-8 bytes and must not consist only of whitespace.
- `home_dir` is optional at creation time and defaults to `/<username>` when omitted.
- When provided, `home_dir` is normalized to a clean absolute MnemoNAS path and must not be empty or contain `.` or `..` path segments or control characters.
- The `user` and `guest` roles cannot use `/` as `home_dir`; `admin` may use `/` for the global namespace.
- `quota_bytes` is optional, and `0` means unlimited.
- Group names are normalized to lowercase and may contain only letters, digits, `.`, `_`, and `-`.

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

User quotas:

- `quota_bytes = 0` means unlimited.
- When it is greater than zero, server-side quota checks apply to non-admin Web/API uploads, copies, moves, and trash restores.
- WebDAV PUT/COPY/MOVE writes also use this check when `webdav.auth_type = "users"` and the write target is inside that user's `home_dir`.
- Checks use the current logical size under the `home_dir`; use `storage.directory_quotas` to limit shared directories.
- Exceeding quota returns `507 Insufficient Storage` with code `QUOTA_EXCEEDED`.
- `details` contains `quota_type`, `quota_path`, `used_bytes`, `quota_bytes`, `required_bytes`, and `available_bytes`.
- When alert channels are enabled, Web/API upload, copy, move, and trash-restore quota denials also send a `quota_exceeded` warning event.
  Alert event details keep only the operation, `actor_scope`, quota type, and byte counts; they omit account names, the home directory, target path, and quota path.

Directory quotas:

- `storage.directory_quotas` can define hard limits for MnemoNAS logical directories.
- Matching Web/API uploads, copies, moves, trash restores, version restores, and WebDAV PUT/COPY/MOVE operations return the same `QUOTA_EXCEEDED` code.
- Directory quota denials add `quota_type="directory"` plus `quota_path` to `details`.
- Web/API directory quota denials, including version restores, also emit `quota_exceeded` alert events without exposing the matched directory path.

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
| `GET` | `/api/v1/setup/readiness` | Admin; read-only when auth is disabled | Get automatically verified setup readiness |
| `POST` | `/api/v1/setup/acknowledge` | Admin | Complete first-run setup |
| `POST` | `/api/v1/setup/defer` | Admin | Defer eligible backup requirements |
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
  "webdav_auth_type": "basic",
  "allow_unsafe_no_auth": false
}
```

Notes:

- The endpoint does not return initial usernames or passwords.
- `allow_unsafe_no_auth` only reports whether the dangerous configuration exception is enabled; public deployments should still rely on the security self-check, `mnemonas-doctor --public-domain`, and cloud-firewall review.
- The first-run Web administrator password is written only to `initial-password.txt` next to `auth.users_file`; the default path is `<storage.root>/.mnemonas/initial-password.txt`, and non-interactive startup logs only report that file path.
- `is_first_run` is `true` until setup is completed. It is `false` during an active deferral and automatically returns to `true` after the deadline. The administrator readiness endpoint recomputes evidence independently and can restore its prompt before the deadline.
- The endpoint returns a setup-specific flat JSON payload and does not use the common `data` wrapper.
- Responses include `Cache-Control: private, no-store` and vary on both `Cookie` and `Authorization`.

### Setup Readiness

Administrator readiness is computed from server-side evidence for accounts, the initial credential file, backups, and the security self-check. The API does not accept subjective completion flags from the browser.

```http
GET /api/v1/setup/readiness
```

The response uses the common `data` envelope. The full response structure is shown below. `title` and `message` are display strings that vary with the detected result and must not be used for program logic. The English example translates these display strings for readability.

```json
{
  "success": true,
  "data": {
    "lifecycle": "pending",
    "prompt": true,
    "generated_at": "2026-07-13T12:00:00Z",
    "overall_status": "action_required",
    "can_complete": false,
    "can_defer": true,
    "required": {
      "completed": 4,
      "total": 6
    },
    "recommended": {
      "completed": 2,
      "total": 4
    },
    "checks": [
      {
        "id": "admin_access",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "Administrator access available",
        "message": "At least one administrator account is active.",
        "action": "manage_users"
      },
      {
        "id": "bootstrap_credential",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "Initial password changed",
        "message": "All active administrators have completed their password change.",
        "action": "change_password"
      },
      {
        "id": "initial_password_file",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "Remove the initial password file",
        "message": "No initial password file remains on the server.",
        "action": "change_password"
      },
      {
        "id": "security_baseline",
        "requirement": "required",
        "status": "complete",
        "deferrable": false,
        "title": "Meet the security baseline",
        "message": "The security baseline has no blocking items.",
        "action": "review_security"
      },
      {
        "id": "backup_job",
        "requirement": "required",
        "status": "incomplete",
        "deferrable": true,
        "title": "Add an independent backup",
        "message": "No enabled backup job has been added.",
        "action": "create_backup"
      },
      {
        "id": "backup_success",
        "requirement": "required",
        "status": "incomplete",
        "deferrable": true,
        "title": "Complete the first backup",
        "message": "No currently valid successful backup exists.",
        "action": "run_backup"
      },
      {
        "id": "admin_redundancy",
        "requirement": "recommended",
        "status": "complete",
        "deferrable": false,
        "title": "Prepare a backup administrator",
        "message": "A backup administrator account is available.",
        "action": "manage_users"
      },
      {
        "id": "backup_schedule",
        "requirement": "recommended",
        "status": "incomplete",
        "deferrable": false,
        "title": "Enable automatic backups",
        "message": "Enable an automatic schedule for a backup job.",
        "action": "create_backup"
      },
      {
        "id": "restore_verification",
        "requirement": "recommended",
        "status": "incomplete",
        "deferrable": false,
        "title": "Verify restore capability",
        "message": "Run a restore drill and keep its verification result current.",
        "action": "run_restore_drill"
      },
      {
        "id": "security_recommendations",
        "requirement": "recommended",
        "status": "complete",
        "deferrable": false,
        "title": "Resolve security recommendations",
        "message": "All security self-checks pass.",
        "action": "review_security"
      }
    ],
    "summary": {
      "auth_enabled": true,
      "active_admin_count": 2,
      "password_change_required_admin_count": 0,
      "initial_password_file": "missing",
      "enabled_backup_job_count": 0,
      "security_status": "pass",
      "security_blocking_check_ids": []
    }
  },
  "timestamp": "2026-07-13T12:00:00Z"
}
```

The fixed enums are:

- `lifecycle`: `pending`, `deferred`, or `completed`.
- `overall_status`: `ready`, `action_required`, or `unavailable`.
- `requirement`: `required` or `recommended`.
- Check `status`: `complete`, `incomplete`, `unavailable`, or `not_applicable`.
- `action`: `change_password`, `manage_users`, `create_backup`, `run_backup`, `run_restore_drill`, or `review_security`.
- `summary.initial_password_file`: `missing`, `present`, or `unavailable`.
- `summary.security_status`: `pass`, `warning`, `block`, or `unavailable`.

The fixed checks and action mappings are:

| Check ID | Level | Deferrable | Possible `action` | Server evidence |
| --- | --- | --- | --- | --- |
| `admin_access` | `required` | No | `manage_users`, `review_security` | Authentication is enabled and at least one administrator is active |
| `bootstrap_credential` | `required` | No | `change_password`, `manage_users`, `review_security` | No active administrator requires a password change |
| `initial_password_file` | `required` | No | `change_password` | The initial password file is absent and its path check passes |
| `security_baseline` | `required` | No | `review_security` | The security self-check has no blocking items that must be resolved |
| `backup_job` | `required` | Yes | `create_backup` | An independent enabled backup job exists |
| `backup_success` | `required` | Yes | `run_backup` | A currently valid successful backup exists |
| `admin_redundancy` | `recommended` | No | `manage_users` | At least two administrators are active |
| `backup_schedule` | `recommended` | No | `create_backup` | An enabled scheduled backup job exists |
| `restore_verification` | `recommended` | No | `run_restore_drill` | A currently valid restore-verification record exists |
| `security_recommendations` | `recommended` | No | `review_security` | The security self-check has no warning or blocking items |

`required.completed` and `recommended.completed` count checks with `complete` or `not_applicable` status. `completed_at`, `deferred_until`, `summary.latest_backup_success_at`, and `summary.latest_restore_verification_at` are optional RFC 3339 timestamps returned when the corresponding evidence exists. `generated_at` is the time at which the server recomputed readiness.

The response does not expose usernames, user IDs, file paths, backup destinations, or raw security-check details.

Responses include `Cache-Control: private, no-store` and vary on both `Cookie` and `Authorization`.

When authentication is disabled, the sanitized status remains readable, but `admin_access` cannot pass and both mutation endpoints return `403`.

### Complete First-run Setup

The server recomputes every required check before persisting completion. Incomplete recommended checks do not block completion.

```http
POST /api/v1/setup/acknowledge
```

Request body: `{}`.

A successful response returns the same complete `data` structure as readiness and includes `message: "setup completed"`. `lifecycle` is `completed`; `prompt`, `can_complete`, and `can_defer` are all `false`; and `completed_at` is the first completion time. A repeated call returns `message: "setup already completed"` and preserves the first `completed_at`.

Failure behavior:

- Returns `401` or `403` when the caller is unauthenticated or is not an administrator.
- Returns `409 SETUP_NOT_READY` when a required check is incomplete. `details.required_check_ids` contains check IDs only.
- Returns `503 SETUP_READINESS_UNAVAILABLE` when required evidence cannot be read.
- Repeated completion returns `200`.

### Defer First-run Setup

Deferral is allowed only when the backup job or first successful backup remains incomplete and every other required check passes.

```http
POST /api/v1/setup/defer
Content-Type: application/json

{"remind_in_days": 7}
```

`remind_in_days` must be an integer from `1` through `30`. A successful response returns the same complete `data` structure as readiness and includes `message: "setup deferred"`; `lifecycle` is `deferred`, `prompt` is `false`, and `deferred_until` contains the new reminder deadline. Deferral does not write a completion time. After the deadline, `lifecycle` automatically returns to `pending`. Ineligible requests return `409 SETUP_DEFER_FORBIDDEN`.

`GET /api/v1/stats` returns availability flags for each stats group.

Admin responses can include disk mount metadata from Linux mountinfo:

- `disk_mount_point`
- `disk_mount_source`
- `disk_mount_options`

These fields help confirm the filesystem, device, or dataset hosting MnemoNAS:

- Secret-like path segments in `disk_mount_point` are redacted.
- URL userinfo and secret-like parameters in `disk_mount_source` are redacted.
- Sensitive mount option values such as credentials, usernames, passwords, keys, and tokens are redacted.

Admin responses can also include `directory_quota_stats_available` and `directory_quotas`.

Each directory-quota entry contains `path`, `quota_bytes`, `used_bytes`, `available_bytes`, `usage_ratio`, `exists`, and `status`. Directory quota `status` is one of `normal`, `warning`, `exceeded`, or `missing`.

When auth is enabled, home-scoped non-admin users do not receive global disk, CAS, file-count, or directory-quota stats.

## File Operations

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/files/{path}` | List directory or get file metadata |
| `POST` | `/api/v1/files/{path}` | Upload or overwrite file |
| `POST` | `/api/v1/files-delete-intents` | Capture an atomic confirmation snapshot of deletion targets and the current policy |
| `DELETE` | `/api/v1/files/{path}` | Delete to Trash or permanently under a confirmed target snapshot and current policy |
| `POST` | `/api/v1/files-move` | Move or rename resource |
| `POST` | `/api/v1/files-copy` | Copy file or directory recursively |
| `GET` | `/api/v1/download/{path}` | Authenticated file download or ZIP archive download |
| `POST` | `/api/v1/directories/{path}` | Create directory |

Directory listing permissions:

- For non-admin callers, directory listing applies the same `home_dir` and most-specific `storage.directory_access_rules` checks to the requested directory and its immediate children.
- Children without read access are omitted from the response.
- Requests for the root directory `/` return only the user's `home_dir` and top-level entries for readable shared directories, not other global-root contents.
- When only a nested shared directory is granted, existing ancestor directories may be used for read-only navigation; creating, moving, or copying under those ancestors still requires explicit write grants.

List responses include `capabilities` for the current directory and for each returned item:

- `read` means the path can be listed or opened for navigation.
- `concreteRead` means exact-resource read actions such as download, copy source, share, or favorite are allowed.
- `write` means mutation actions are allowed for that path or container.

For example, root may report `write: true` when upload or create operations are allowed under root while still reporting `concreteRead: false` because root itself is not a downloadable or copyable resource.

Each real file or directory item also contains `deleteIdentityToken`. On Linux and macOS, this field is an opaque 64-character lowercase hexadecimal SHA-256 value bound to the filesystem device, inode, ctime, type and permission bits, size, and nanosecond modification time. Replacing a path with a new object changes the token even when the type, size, and modification time remain the same. The field is `null` when the platform cannot provide the required object identity; a delete intent cannot be created for that item.

The top-level list `data` also includes the current delete policy:

- `deleteMode`: `trash` moves an item to Trash, while `permanent` deletes it directly.
- `deletePolicyToken`: the 64-character lowercase hexadecimal SHA-256 identifier for the complete current delete policy. This value is opaque and is not used to derive settings.
- `trashRetentionDays`: the expiry interval assigned to new Trash items under the current policy. `0` makes a new item immediately eligible for cleanup.
- `trashAutoCleanupEnabled`: whether the periodic retention sweep is enabled. When it is `false`, expired Trash items are not removed by a periodic task.

Clients must treat these four fields as one policy snapshot. A client must not issue a delete request when the policy is missing or unrecognized. `deletePolicyToken` covers settings that change deletion consequences, including the delete mode, Trash retention period, background sweep interval, and Trash capacity limit.

Deletion confirmation begins with `POST /api/v1/files-delete-intents`. The request body accepts only `targets`, with 1 to 1000 unique, non-root, non-nested targets. Each target must include `path` and the `observedIdentityToken` copied without modification from the same current list item:

```json
{
  "targets": [
    {
      "path": "/documents/report.pdf",
      "observedIdentityToken": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    },
    {
      "path": "/photos/2026",
      "observedIdentityToken": "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    }
  ]
}
```

A missing, `null`, or empty `targets` value, a missing target field, an empty or malformed token, an uppercase or non-hexadecimal token, a duplicate or nested path, the legacy `paths` field, or any unknown field returns `400 Bad Request`. Under one filesystem read lock, the server checks the root object in this order: write access, mount boundary, file type, and observed identity. An identity mismatch returns `409 DELETE_TARGET_CHANGED` with only `path` in `details`; the server does not read file contents, traverse a directory, or mutate data. If the server cannot currently derive object identities, it returns `503 Service Unavailable`.

The response returns `deleteMode`, `deletePolicyToken`, `trashRetentionDays`, `trashAutoCleanupEnabled`, and `targets` in request order. Each target contains `path`, `name`, `isDir`, `size`, UTC RFC 3339 nanosecond `modTime`, `deleteIdentityToken`, and `deleteTargetToken`; `deleteIdentityToken` equals the corresponding request `observedIdentityToken`. `deleteTargetToken` is an opaque 64-character lowercase hexadecimal value covering the target path and its complete current tree: entry paths, object identities, types, sizes, nanosecond modification times, and file contents. No partial confirmation result is returned when a target is absent, a parent has the wrong type, or the caller lacks write access to any target-tree entry. A target tree containing a symlink, FIFO, Unix socket, other non-regular file, or nested mount point below the workspace root returns `409 Conflict`. The nested-mount check includes bind mounts and targets located inside a nested mounted directory.

`DELETE /api/v1/files/{path}` requires exactly one `expected_delete_mode`, one `expected_delete_policy_token`, and one `expected_delete_target_token` query parameter. The mode and both tokens must be copied without modification from the same delete-intent response. `expected_delete_mode` must be the strict value `trash` or `permanent`; both tokens must be 64-character lowercase hexadecimal values.

A missing or empty `expected_delete_mode` returns `400 MISSING_EXPECTED_DELETE_MODE`. A duplicate value, malformed query encoding, case mismatch, surrounding whitespace, or unknown value returns `400 INVALID_EXPECTED_DELETE_MODE`. A missing or empty `expected_delete_policy_token` returns `400 MISSING_EXPECTED_DELETE_POLICY_TOKEN`. A duplicate value, incorrect length, uppercase or non-hexadecimal character, surrounding whitespace, or malformed query encoding returns `400 INVALID_EXPECTED_DELETE_POLICY_TOKEN`. A missing or empty `expected_delete_target_token` returns `400 MISSING_EXPECTED_DELETE_TARGET_TOKEN`. A duplicate value, incorrect length, uppercase or non-hexadecimal character, surrounding whitespace, or malformed query encoding returns `400 INVALID_EXPECTED_DELETE_TARGET_TOKEN`.

Under one storage write lock, the server first compares the expected policy, then rechecks write access and the target token for every current tree entry, and finally performs the deletion. A complete-policy change returns `409 DELETE_POLICY_CHANGED`; `details` contains `expected_delete_mode`, `expected_delete_policy_token`, `actual_delete_mode`, `actual_delete_policy_token`, `trash_retention_days`, and `trash_auto_cleanup_enabled`. If the policy is unchanged but the target path, content, or tree has changed, the server returns `409 DELETE_TARGET_CHANGED`. When a current target token is available, `details` contains `path`, `expected_delete_target_token`, and `actual_delete_target_token`. If the confirmed target disappeared or a parent is no longer a directory, `details` contains `path` and `expected_delete_target_token` and omits the unavailable `actual_delete_target_token`. Mount-point and special-file conflicts remain their respective `409 Conflict` responses. Policy and target conflicts decided before atomic object capture do not commit workspace, index, version, share, favorite, Trash, or activity changes. The caller should refresh the list, obtain a new delete intent, and confirm again.

After policy, target-token, or WebDAV-condition checks pass, the server captures the current object at a random stage under the same source parent with an atomic no-replace rename and continues from that path. A new object created at the original logical path during this process is not copied, overwritten, or removed. In `permanent` mode, final physical cleanup moves the verified stage into a random quarantine with permissions no broader than mode `0700` and removes entries relative to a server-held directory handle. In `trash` mode, the server records `prepared`, `copying`, `ready`, `committed`, and `completed` checkpoints under `.mnemonas/trash/.transactions/`. A canonical mode-`0600` owner marker binds each new container to the `prepared` journal and its persistent identity. The `copying` checkpoint binds the synced operation-owned container before payload copying, which starts only after marker removal and directory sync complete. `ready` is published only after full replica verification. The matching `trash_operations` outbox row is committed atomically with Trash metadata and file-index changes. Share and favorite participants retain operation-scoped receipts and original delete ownership so recovery is idempotent. Explicit share or favorite mutations after delete completion block an older restore from overwriting newer intent.

If rollback cannot complete safely before logical commit, REST returns `500 INTERNAL_ERROR` and does not record delete activity. Internal stages or paired evidence may remain for recovery, but host paths are not exposed in the API response. In `permanent` mode, incomplete quarantine cleanup after logical commit returns `200 OK` with `warning=true`, `199 MnemoNAS "delete cleanup incomplete"`, and `cleanup_warning=true` in delete activity. In `trash` mode, only an explicitly classified persistence-only failure returns success with `199 MnemoNAS "workspace mutation persistence incomplete"` and `persistence_warning=true`. If terminal journal removal cannot be confirmed durable after the visible mutation completes, the server retains or recreates the `completed` checkpoint and activates the recovery gate. The current response still uses the persistence warning, but new storage mutations remain blocked until recovery succeeds. Capacity cleanup runs only after the durable transfer and reliable terminal journal cleanup complete; its failure returns success with `199 MnemoNAS "trash delete cleanup incomplete"` and `trash_cleanup_warning=true`. Applicable persistence and capacity warnings may be combined.

A hard journal, participant, receipt, outbox, source, or destination failure in `trash` mode returns `500 INTERNAL_ERROR`, may occur after the durable commit point, preserves recovery evidence, and blocks subsequent storage mutations until recovery succeeds. A `500` response after that point does not imply rollback: a transfer with a matching `trash_operations` row rolls forward. Clients should refresh Trash and the destination after recovery before retrying. WebDAV returns `204 No Content` for the corresponding warning-only results and `500 Internal Server Error` for hard transfer failures.

The server rereads the host mount table during delete-intent creation, deletion revalidation, cross-root copying, and checked removal. An unreadable mount table, invalid mount path, or nested mount below the workspace root stops processing. A conflict before object capture returns `409 Conflict`. In `permanent` mode, a captured object that cannot be rolled back safely becomes a recovery residual, while the same condition after logical commit becomes a cleanup warning. In `trash` mode, a hard failure after checkpoint publication retains durable evidence and requires recovery. A copied destination is removed only while its boundary remains verifiable and is otherwise retained to avoid crossing a new mount. The workspace root itself may be a mount point; this restriction applies only to nested mounts below it.

The storage write lock serializes only operations performed through MnemoNAS. Direct filesystem writes by another process with the same UID and concurrent mounts by a privileged process remain outside this boundary. The `.transactions` checkpoints, `trash_operations` outbox, owner markers, and participant receipts provide startup recovery for process crashes and power loss during live Trash transfers when their evidence remains valid. A marker that is corrupt because a crash occurred after its creation but before its complete write and sync cannot prove container ownership; recovery preserves the evidence and prevents writable startup until manual reconciliation. Object capture and no-replace rollback may change ctime or parent-directory timestamps. The server does not move or delete an unknown replacement or automatically remove internal residue whose ownership cannot be verified.

In `trash` mode, a file or complete directory tree is moved to Trash. After the persisted expiry associated with `trashRetentionDays` is reached, an enabled retention sweep removes it; a Trash capacity limit may permanently remove older items before expiry. In `permanent` mode, the item does not enter Trash and cannot be restored from it; deleting a non-empty directory in this mode returns `409 Conflict`.

`GET /api/v1/download/{path}` returns file bytes by default. Supported query parameters:

- `download=true`: at most once, forces an attachment filename.
- `version=<hash>`: at most once, downloads a historical version.
- `archive=zip`: at most once, downloads the target path as a ZIP archive.

ZIP archive behavior:

- Works for directories and individual files, and cannot be combined with `version`.
- Requires concrete read access for the target and every included entry; read-only navigation ancestors cannot be archived.
- Is capped at 10000 entries and 20 GiB of file content.
- Entry-count or content-size limit violations return `413 Request Entity Too Large`.
- Duplicate archive entry names or entry snapshot changes detected before streaming return `409 Conflict`.
- Archive entry names reject path traversal, absolute paths, backslashes, colons, and control characters to avoid cross-platform extraction ambiguity.
- Archive attachment filenames use the target path basename; the root path uses `mnemonas-files.zip`, and names that already end with `.zip` do not receive a duplicate suffix.
- Current-file and historical-version downloads support Range requests; ZIP archive downloads do not guarantee Range support.

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

This REST endpoint copies either one file or one directory tree. Source and target paths must differ, the target path must not already exist, and directory copies cannot target a descendant of the source directory. Use WebDAV `COPY` when `Overwrite: T/F` semantics are required.

When a copy has completed but a later workspace persistence step fails, the endpoint still returns `201 Created` with `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"`. The response body includes `data.warning: true` and `message: "resource copied with persistence warning"`.

Other file mutations may return success with a `Warning` header if the file operation succeeded but later metadata, activity, or cleanup work did not fully complete.

## Thumbnails

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/thumbnails/{path}` | Get generated thumbnail for an image or supported preview |

Download-session cookies are used for preview and thumbnail flows where browser media elements cannot attach Authorization headers.

`POST /api/v1/auth/download-session` can be authenticated by the Web UI session cookie or by `Authorization: Bearer <access-token>`. HTTPS mode sets `__Host-mnemonas_download_access`; local HTTP mode sets `mnemonas_download_access`.

The cookie is `HttpOnly` and `SameSite=Strict`. HTTPS mode uses `Secure`, `Path=/`, and no `Domain` attribute; HTTP mode uses the `/api/v1` path. Thumbnail responses are generated images and include `nosniff` plus a sandbox CSP.

`GET /api/v1/thumbnails/{path}` accepts an optional `size` query parameter at most once. Supported values are `small` or `s` for 150 px, `medium` or `m` for 300 px, and `large` or `l` for 600 px. Omitted `size` defaults to `medium`.

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
- `path`: file path (required, at most once)

The `path` value must identify a non-root file path. Root or root-equivalent values return `400 Bad Request` with `invalid path`. Copyable shell or browser examples should URL-encode the query value. For example, `/documents/report.txt` is sent as `%2Fdocuments%2Freport.txt`.

When the version content has already been restored but final workspace metadata persistence fails, the API still returns `200 OK` with `Warning: 199 MnemoNAS "workspace mutation persistence incomplete"` and the response `message` set to `version restored with persistence warning`.

Successful restores write a `restore` activity entry with `details.restore_source` set to `version` and `details.hash` set to the restored version hash. Workspace persistence warnings also include `details.persistence_warning="true"`.

Example request:

```bash
MNEMONAS_ACCESS_TOKEN="<access-token>"
curl_auth_config="$(mktemp)"
trap 'rm -f "$curl_auth_config"' EXIT
chmod 600 "$curl_auth_config"
printf 'header = "Authorization: Bearer %s"\n' "$MNEMONAS_ACCESS_TOKEN" > "$curl_auth_config"

curl -X POST \
  --config "$curl_auth_config" \
  "http://localhost:8080/api/v1/versions/<hash>/restore?path=%2Fdocuments%2Freport.txt"
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
| `POST` | `/api/v1/trash/empty` | Permanently delete confirmed Trash items |

Trash visibility follows the current user's configured `home_dir` boundary.

Every listed item includes a random `id` that remains immutable for the item's lifetime and its persisted `expiresAt` value. This RFC 3339 timestamp is assigned when the item enters Trash; changing `retentionDays` later does not rewrite existing item expiry. The list-level `retentionDays` describes only the current policy for new deletions, while `trashAutoCleanupEnabled` reports whether the background retention sweep is enabled. The size limit may still permanently remove older items before `expiresAt`.

When the retention sweep is enabled, the same periodic task removes expired file versions and Trash items whose `expiresAt` has been reached. When it is disabled, items can still be removed explicitly through permanent-delete endpoints.

Permanent Trash deletion checks for nested mounts below the Trash root before staging and recursive removal. An item with share or favorite restore data also receives a read-only validation of its canonical payload, participant stores, and recovery evidence before any physical mutation. `DELETE /api/v1/trash/{id}` preserves item content and metadata when a nested mount exists, the mount table cannot be verified, or participant preflight fails. Mount conflicts return `409 Conflict`; participant unavailability is handled as a server error.

After content, SQLite metadata, and version cleanup complete, the server uses the original delete operation ID in the restore payload to remove exact share and favorite ownership before removing the purge journals. It retries the first participant persistence warning once. If the retry succeeds, the item remains deleted and the response carries a cleanup warning. A persistent warning or hard failure also preserves the committed deletion, but retains the `committed` purge journal and activates the recovery gate so later storage mutations remain blocked until recovery succeeds.

`POST /api/v1/trash/empty` permanently deletes only the items explicitly confirmed in the request. The body is a JSON object containing only `ids`:

```json
{
  "ids": ["7d29d7827f68f1a3", "4fdd157f624d892b"]
}
```

`ids` must contain between 1 and 1000 unique strings. Each ID must be between 1 and 128 bytes in UTF-8 and may contain only ASCII letters, digits, `-`, and `_`. A body above the JSON size limit returns `413 Payload Too Large` with code `PAYLOAD_TOO_LARGE`. Unknown fields, trailing JSON, an empty array, duplicate IDs, or an invalid ID format return `400 Bad Request` with code `INVALID_TRASH_SELECTION` and do not delete any item.

Under one storage write lock, the server loads the current Trash contents and preflights access rules for the restored logical paths of every selected item that still exists before deleting anything. Any preflight failure terminates the request without deleting a selected item. A top-level path that is not visible to the current account returns `404 Not Found`; a writable top level with a descendant denied by an access rule returns `403 Forbidden`. After preflight succeeds, selected IDs are processed in request order. Items added after the request and all unselected items remain unchanged; selected IDs that no longer exist are classified as `skipped`.

The `deleted`, `remaining`, and `skipped` response arrays form a complete, non-overlapping partition of the input IDs, with each array retaining original request order. The corresponding `*_count` fields equal the three array lengths. If a hard failure occurs during execution, unprocessed items that still exist are classified as `remaining`, and the response still reports completed deletions. `partial` is `true` if and only if `remaining` or `skipped` is non-empty. `warning` is always a boolean and reports only incomplete physical cleanup after a committed deletion; it does not represent a missing selection or an execution failure.

```json
{
  "success": true,
  "data": {
    "deleted": ["7d29d7827f68f1a3"],
    "remaining": ["4fdd157f624d892b"],
    "skipped": [],
    "deleted_count": 1,
    "remaining_count": 1,
    "skipped_count": 0,
    "partial": true,
    "warning": false
  },
  "message": "trash selection emptied partially",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

`POST /api/v1/trash/{id}/restore` restores the item to its original path by default.

Custom restore targets:

- A `path` query parameter, when specified at most once, restores the item to a custom target path.
- The custom target must be writable, must be a non-root path, its direct parent directory must already exist, and the target itself must not already exist.
- Root or root-equivalent custom targets return `400 Bad Request` with `invalid path`.
- If the direct parent directory is absent, the endpoint returns `409 Conflict` and does not create intermediate directories.

If the trash item has historical versions and the original path is occupied by a live file, or another trash item still references an overlapping source or target version metadata path, the endpoint returns `409 Conflict` before quota checks and does not emit a quota alert.

Directory restores also check descendant paths for overlapping version metadata.

A successful restore returns `200 OK`. If the visible restore completes but only participant-store persistence or terminal journal cleanup remains uncertain, the response contains `data.warning=true`, the message `file restored with persistence warning`, and `199 MnemoNAS "workspace mutation persistence incomplete"`; restore activity contains `persistence_warning=true`. Terminal journal cleanup uncertainty retains or recreates the `completed` checkpoint and blocks new storage mutations until recovery succeeds. Share and favorite paths are relocated from the operation snapshot through original delete ownership and operation-scoped receipts. Replay of the same operation does not overwrite later changes, and explicit share updates, share deletion, or favorite mutations after delete completion block an older restore from reviving that state.

A hard journal, participant, receipt, outbox, source, or destination failure returns `500 INTERNAL_ERROR`. This response may occur after the durable commit point and does not imply that the Trash item remains present or the destination remains absent. A transfer with a matching `trash_operations` row rolls forward. Recovery of a `completed` operation verifies the filesystem result and retries only receipt completion, outbox acknowledgement, and journal cleanup; it does not replay participant Apply. Clients should refresh Trash and the destination after recovery before retrying.

## Search

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/search?q={query}` | Search files by name |

Search results are scoped by configured `home_dir`.

Query parameters:

- `q`: Required search term, up to 100 characters. It must appear exactly once.
- `limit`: Maximum result count. The default is 50 and the maximum is 100. It may appear at most once.

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

`GET /api/v1/shares` lists shares for the current requester. Admin callers may set `all=true` at most once to list all users' shares.

`GET /api/v1/shares/policy` returns `default_expires_in`, `default_max_access`, and `policy_rules` entries with `path`, `require_password`, `max_expires_in`, `max_access`, `allowed_users`, `allowed_groups`, and `allowed_roles`.

Create-share field rules:

- `type` is `file` or `folder`; an omitted value defaults to `file`.
- `permission` currently accepts `read` or an omitted value.
- `password` is optional; non-empty share passwords are limited to 72 bytes.
- If `expires_in` or `max_access` is omitted, the server applies `share.default_expires_in` and `share.default_max_access`.
- If the path matches `share.policy_rules`, the most specific path rule wins.
- `require_password` rejects passwordless requests, while `max_expires_in` and `max_access` cap values above the rule limit.
- If `allowed_users`, `allowed_groups`, or `allowed_roles` is non-empty, non-admin callers must match one configured user, group, or role. Non-matching callers receive `403 Forbidden` with `SHARE_POLICY_PRINCIPAL_FORBIDDEN`.

Authenticated share responses include `risk.level` (`none`, `low`, `medium`, `high`) plus optional reason objects.

Risk reasons identify passwordless, long-lived, broad-folder, unlimited, stale, or soon-expiring links. An enabled share that has never been accessed after 30 days is reported as `unused_enabled`; an enabled share whose last access is more than 90 days old is reported as `stale_enabled`.

Share expiry alerts:

- When `[alerts] enabled = true` and at least one alert channel is configured, the server scans hourly for enabled shares that expire within 72 hours.
- The scan sends an aggregate `share_expiring_soon` warning event.
- Within one process lifetime, the same share expiry timestamp is reminded once.
- Event `details` include `source = "share"`, share count, scan window, soonest expiry time, file/folder share counts, passwordless share count, and unlimited-access share count.
- Event details do not include share paths, share URLs, access passwords, or share IDs.

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

Share update rules:

- All update fields are optional; omitted fields normally keep their current values.
- An empty `password` clears the password, an empty `expires_in` clears expiry, and `permission` currently accepts only `read`.
- Updates to shares that match `share.policy_rules` must also satisfy the path rule.
- `require_password` rejects updates that would leave a matching share passwordless.
- `max_expires_in` and `max_access` cap explicit values that clear or exceed the configured limit.
- They also cap omitted fields when the stored share currently has no corresponding limit or exceeds the path rule.
- If `allowed_users`, `allowed_groups`, or `allowed_roles` is non-empty, non-admin callers must match one configured user, group, or role. Non-matching callers receive `403 Forbidden` with `SHARE_POLICY_PRINCIPAL_FORBIDDEN`.

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
- Root-folder public shares report `file_name` as the stable display name `mnemonas-share` instead of `/`.
- Authorized zero-byte files return `file_size: 0`; authorized empty folders return `folder_items: 0`.
- When `max_access > 0` and `access_count` has reached the limit, public access returns `410 Gone` with `SHARE_ACCESS_LIMIT_REACHED`.
- Shares are expired once the current time reaches or passes `expires_at`; expired shares return `410 Gone` with `SHARE_EXPIRED`.
- Disabled shares return `410 Gone` with `SHARE_DISABLED`.
- Shares created by a disabled or deleted owner return `404 Not Found` with `SHARE_NOT_FOUND` for public metadata, downloads, and folder listings.
- `access_count` increments on downloads and folder-listing requests. Password validation through `POST /api/v1/public/shares/{share_id}/access` and the compatibility path `POST /s/{share_id}` does not increment it.
- Subpaths in `items?path=` and `download/{path}` are relative to the shared folder root.
  The folder-listing `path` query parameter may be specified at most once.
  Control characters and standalone `.` or `..` path segments are invalid, while legal names containing repeated dots, such as `foo..txt`, remain valid.
  Invalid subpaths do not increment `access_count`.
- Folder-listing response `path` and `items[].path` values are canonical paths relative to the shared folder root and do not start with `/`; the root-folder response uses an empty `path`. Responses include only direct children of the current directory that remain visible to the share owner.
- Once a download or folder-listing response has started writing to the client, that request remains counted even if the later stream fails.
- Public share downloads honor HTTP Range requests when the backing file reader supports seeking.
  Local MnemoNAS storage supports this path for resumable downloads and browser media playback.
  Range responses increment `access_count` only when they serve at least one content byte; normal full downloads of zero-byte files still count.
- Set `archive=zip` at most once on public download endpoints to download a shared folder root, subfolder, or file as a ZIP archive.
  Public ZIP archives return `application/zip`, do not guarantee Range support, skip entries no longer visible to the share owner, and are capped at 10000 entries and 20 GiB of file content.
  Entry-count or content-size limit violations return `413 Request Entity Too Large` with an archive error code; duplicate archive entry names or entry snapshot changes detected before streaming return `409 Conflict` with an archive error code.
  Archive entry names reject path traversal, absolute paths, backslashes, colons, and control characters to avoid cross-platform extraction ambiguity.
  Archive attachment filenames use the archived target name; a shared root path of `/` uses `mnemonas-share.zip`, and names that already end with `.zip` do not receive a duplicate suffix.
- Unsatisfiable Range requests that return `416 Requested Range Not Satisfiable`, and zero-length Range requests such as `bytes=-0`, do not increment `access_count`.
- Successful password validation sets an `HttpOnly`, `SameSite=Strict` access cookie; later downloads and folder-listing requests use the cookie rather than a password query parameter.
- Public share metadata, password-validation responses, folder-listing responses, and public-download JSON error responses include `Cache-Control: private, no-cache`, `Vary: Cookie`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`.
- Repeated password failures return `429 Too Many Requests` with `SHARE_PASSWORD_RATE_LIMITED`.
- Password failure rate limiting is keyed by share ID and client address. Forwarded headers are ignored by default and are used only when `server.trusted_proxy_hops > 0` and the direct peer is loopback or belongs to `server.trusted_proxy_cidrs`.
- Compatibility paths `GET /s/{share_id}` and `POST /s/{share_id}` return the same public JSON behavior for direct script or non-SPA use.
- Compatibility paths `GET /s/{share_id}/items`, `GET /s/{share_id}/download`, and `GET /s/{share_id}/download/{path}` provide the same folder-listing and download behavior for direct script or non-SPA use.

## Favorites

Favorite paths must normalize to a non-root absolute path:

- Empty values and the root path are rejected with `400 Bad Request` and `MISSING_PATH`.
- Values containing standalone `.` or `..` path segments are rejected with `400 Bad Request` and `INVALID_PATH`.
- The single-path check endpoint accepts the `path` query parameter at most once.
- The `path` query value should be URL-encoded in copyable URLs. For example, `/documents/file.pdf` is sent as `%2Fdocuments%2Ffile.pdf`.
- This validation runs before non-admin `home_dir` authorization.
- Adding a favorite requires the target path to exist at commit time and permit concrete read access for the current user. A missing target returns `404 Not Found` with `FILE_NOT_FOUND`. The path check and favorite persistence run under the same storage mutation lease to prevent a concurrent deletion from leaving a stale favorite.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/favorites` | List favorites |
| `POST` | `/api/v1/favorites` | Add favorite |
| `GET` | `/api/v1/favorites/check?path=%2Fdocuments%2Ffile.pdf` | Check one path |
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
- Manual and scheduled Scrub runs write `scrub` activity entries.
  Scrub failures, object verification problems, and incomplete result persistence send `scrub_run` events through configured Webhook, Telegram, WeCom, DingTalk, or SMTP alert channels.
  Alert details use counts, status, public error types, and public messages; they do not include object hashes or lower-level error text.
- Share creation, deletion, and enabled-state updates write `share` or `unshare` activity entries. Their `details` include review metadata such as share type, permission, password requirement, expiry, and access limit; enabled-state updates also include `enabled` and `previous_enabled`. These details do not include share passwords, public URLs, or share IDs.
- Version restores write `restore` activity with `details.restore_source="version"` for the version-history source and `details.hash` for the restored version hash.
- When the activity log is not configured, the API returns an empty list.
- When the activity log is configured but failed to initialize or is currently unavailable, the API returns `503 Service Unavailable`.

```
GET /api/v1/activity
```

Query parameters:

Each listed query parameter may appear at most once.

- `limit`: Result count. The default is 50 and the maximum is 500.
- `offset`: Pagination offset.
- `action`: Filter by action type.
  Current values are `upload`, `download`, `delete`, `rename`, `move`, `copy`, `create`, `restore`, `share`, `unshare`, `favorite`, `unfavorite`, `favorite_note_update`, `login`, `logout`, `trash_restore`, `trash_delete`, `trash_empty`, `disk_health`, and `scrub`.
- `action_group`: Filter by review group. Current values are `share` for share/unshare events and `risk` for delete, move, rename, version restore, trash restore, share, unshare, permanent trash delete, and trash empty events.
- `path`: Filter by path or directory. The filter matches the path itself, descendants, and path-like activity details such as `from` and `to`.
- `user`: Filter by user.
- `since`: Return entries at or after this RFC3339 timestamp.
- `until`: Return entries at or before this RFC3339 timestamp.

`action` and `action_group` can be combined; the result is their intersection.

`path` is normalized using MnemoNAS absolute-path rules and returns `400 Bad Request` when it contains traversal segments. Invalid `action` or `action_group` values, invalid time formats, or a `since` value later than `until`, return `400 Bad Request`.

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
- `risk_summary` summarizes high-risk actions, including delete, move, rename, share, unshare, permanent trash delete, and trash empty.
  `max_10m` is the highest number of matching high-risk actions in any 10-minute window, while `max_10m_started_at` and `max_10m_ended_at` identify the window for focused review.
- When the activity log is not configured, the API returns zero statistics.
- When the activity log is configured but failed to initialize or is currently unavailable, the API returns `503 Service Unavailable`.

```
GET /api/v1/activity/stats
```

Query parameters:

Each listed query parameter may appear at most once.

- `action`: Filter by action type. Uses the same values as the list endpoint.
- `action_group`: Filter by review group. Current values are `share` and `risk`.
- `path`: Filter by path or directory. The filter matches the path itself, descendants, and path-like activity details such as `from` and `to`.
- `user`: Filter by user.
- `since`: Count entries at or after this RFC3339 timestamp.
- `until`: Count entries at or before this RFC3339 timestamp.

`action`, `action_group`, `path`, `user`, `since`, and `until` use the same error handling as the list endpoint.

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

### List Activity Review Records (Admin)

Return persisted activity review disposition records. The version-history and Trash pages write matching `restore` or `trash_restore` activity entries into `restored` disposition records; share-review records may include `share_disposition_details` with redacted share-disposition clues.

```
GET /api/v1/activity/reviews
```

Query parameters:

- `limit`: Result count. The default is 20 and the maximum is 100.
- `offset`: Pagination offset.
- `reviewer`: Filter by reviewer.
- `activity_entry_id`: Return only review records linked to the given activity entry ID.
- `disposition_status`: Filter by disposition status. Allowed values are `documented`, `confirmed`, `restored`, `disabled`, and `needs_follow_up`.
- `action_group`: Filter by the action group present in the review record action counts. Allowed values are `share` and `risk`.
- `since`: Return review records at or after this RFC3339 timestamp.
- `until`: Return review records at or before this RFC3339 timestamp.

Invalid time formats, a `since` value later than `until`, a non-canonical `activity_entry_id`, an unsupported `disposition_status`, or an unsupported `action_group` return `400 Bad Request`.

Response example:

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "review-123",
        "reviewed_at": "2024-01-15T10:05:00Z",
        "reviewer": "admin",
        "note": "Deleted files were confirmed restored from trash",
        "scope_label": "concentrated window",
        "filter_summary": "group risk changes",
        "disposition_status": "restored",
        "action_counts": {
          "delete": 2,
          "move": 1
        },
        "review_count": 3,
        "total_count": 5,
        "path_count": 2,
        "user_count": 1,
        "path_samples": ["/docs/deleted.txt", "/docs/moved.txt"],
        "user_samples": ["admin"],
        "activity_entry_ids": ["act-delete-1", "act-move-1"]
      }
    ],
    "total": 1,
    "limit": 20,
    "offset": 0
  },
  "timestamp": "2024-01-15T10:05:00Z"
}
```

### Create Activity Review Record (Admin)

Record an activity review disposition. The server uses the current authenticated account as `reviewer` and sets `reviewed_at`.

```
POST /api/v1/activity/reviews
```

Request body:

```json
{
  "note": "Deleted files were confirmed restored from trash",
  "scope_label": "current page",
  "filter_summary": "group risk changes",
  "disposition_status": "restored",
  "action_counts": {
    "delete": 2,
    "move": 1
  },
  "review_count": 3,
  "total_count": 5,
  "path_count": 2,
  "user_count": 1,
  "path_samples": ["/docs/deleted.txt", "/docs/moved.txt"],
  "user_samples": ["admin"],
  "activity_entry_ids": ["act-delete-1", "act-move-1"]
}
```

Notes:

- `note`, `scope_label`, and `activity_entry_ids` are required. `review_count` must be greater than zero, and `total_count` must not be lower than `review_count`.
- `note`, `scope_label`, `filter_summary`, and `user_samples` must not contain control characters. The server-generated `reviewer` field uses the same text constraint.
- `disposition_status` is optional and defaults to `documented`. Allowed values are `documented`, `confirmed`, `restored`, `disabled`, and `needs_follow_up`.
- `action_counts` is optional. Keys must be known activity action types, values must be positive integers, and the sum must equal `review_count`.
- `path_samples` and `user_samples` are optional and accept at most 10 entries each. Paths are normalized with the same logical path rules as activity entries, and duplicate samples are rejected.
- `share_disposition_details` is optional and accepts at most 10 entries. Each entry may include `path`, `type` (`file` or `folder`), `enabled`, `risk_level` (`none`, `low`, `medium`, `high`), `reason_summary`, `suggested_action`, `access_summary`, and `expires_at`; the field records redacted share risk and disposition guidance without share IDs, URLs, or passwords.
- When the activity log is not configured, failed to initialize, or is currently unavailable, the API returns `503 Service Unavailable`.

### Update Activity Review Record Disposition (Admin)

Update the current disposition status of a persisted activity review record, optionally replacing its disposition note.

The server replaces `reviewer` with the current authenticated account and updates `reviewed_at` to the status write-back time; when `note` is omitted, the previous note is preserved. Samples, counts, and linked activity entries remain unchanged.

```
PATCH /api/v1/activity/reviews/{id}
```

Request body:

```json
{
  "disposition_status": "disabled",
  "note": "The share link was disabled and the access entry point was verified"
}
```

Notes:

- `disposition_status` is required. Allowed values are `documented`, `confirmed`, `restored`, `disabled`, and `needs_follow_up`.
- `note` is optional. When provided, it must be non-empty text without control characters; the server trims surrounding whitespace and applies the activity review note length limit.
- A non-canonical `{id}` or unsupported `disposition_status` returns `400 Bad Request`.
- A missing review record returns `404 Not Found`.
- When the activity log is not configured, failed to initialize, or is currently unavailable, the API returns `503 Service Unavailable`.

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
| `GET` | `/api/v1/settings/access-reviews` | List recent directory-access review records |
| `POST` | `/api/v1/settings/access-reviews` | Persist one directory-access review record |
| `DELETE` | `/api/v1/settings/access-reviews` | Clear directory-access review records |
| `POST` | `/api/v1/settings/alerts/test` | Send a test alert through saved alert channels |
| `GET` | `/api/v1/settings/security-check` | Run public-access security self-check |
| `PUT` | `/api/v1/settings` | Update settings |
| `GET` | `/api/v1/settings/webdav-credentials` | Get current WebDAV credential status |

Settings updates can change the following configuration at runtime:

- Directory quotas, directory access rules, WebDAV prefix, read-only mode, auth mode, share configuration, favorite configuration, and retention/versioning policies.
- Web UI auth token lifetimes. `auth.access_token_ttl` must be a Go duration of at least `30s`; `auth.refresh_token_ttl` must be a positive Go duration. New login sessions use the updated refresh lifetime. Refreshes in an existing session use the current access lifetime but retain the absolute session expiry established at login. Already issued tokens keep their existing expiry.
- Webhook, Telegram, WeCom, DingTalk, and SMTP email notification settings.
- Disk-health temperature thresholds and media-wear thresholds.
- Scheduled Scrub maintenance. Updates immediately replace the running background scheduler.
- Dataplane connection settings. Server listener/TLS changes and CDC chunk-size changes are saved but require restarting the affected service before they take effect.

Directory quota and access-rule updates are hot-applied to the Web/API and WebDAV runtime.

Path field rules:

- `path` fields in `storage.directory_quotas`, `storage.directory_access_rules`, and `share.policy_rules` use the same MnemoNAS logical-path rules.
- Paths must start with `/` and must not contain Windows or UNC syntax, backslashes, query or fragment characters, control characters, or `.`/`..` path segments.
- The Settings API trims surrounding whitespace and normalizes duplicate and trailing slashes; paths containing `.` or `..` are not folded and are rejected.
- The Web settings page wraps paths containing spaces or double quotes in double quotes in directory-quota line-based inputs; literal double quotes inside the path are escaped as `\"`, for example `"/Family Photos" 500 GB`.
- Directory access rules and share path policies use structured path inputs, so paths containing spaces or literal double quotes are entered directly without manual line quoting.

The Web settings page derives a share-policy coverage summary from the current draft before save. It shows default expiry, default access limits, path-policy count, password-required path count, creator/maintainer-scope path count, attention items for loose defaults or path policies, and cleanup suggestions for root-wide rules, most-specific path rules that do not inherit ancestor limits, descendant rules that loosen ancestor expiration, access-count, or creator-scope limits, and duplicate-equivalent rules.

This summary is for pre-save review only; enforced behavior still comes from the server policy after the Settings API save succeeds.

After a successful settings save, actual changes to `storage.directory_access_rules` or share policy fields submit a `settings_policy_changed` warning event to the alert runtime.

Triggering fields include `share.enabled`, `share.default_expires_in`, `share.default_max_access`, and `share.policy_rules`.

Event `details` include `source = "settings"`, `changed_sections`, booleans for the changed directory-access and share-policy fields, and rule counts. They do not include rule paths, `share.base_url`, alert-channel secrets, or user/member details.

Normalized no-op submissions do not emit this event. Alert delivery failures are logged and do not fail the settings save.

### Settings Validation Rules

`PUT /api/v1/settings` validates settings by field group. Invalid settings return `400 Bad Request` without mutating the running config.

- Server listener: `server.host` must be empty, `*`, a valid hostname, IPv4, or IPv6 literal, without a port, whitespace, or control characters. Set the port through `server.port`.
- Reverse proxy: `server.trusted_proxy_hops` controls whether forwarded headers from trusted reverse proxies are honored when evaluating HTTPS request semantics. `server.trusted_proxy_cidrs` lists non-loopback proxy IPs or CIDRs allowed to supply those headers.
- Web UI auth: `auth.access_token_ttl` must be at least `30s`, and `auth.refresh_token_ttl` must be positive. Public deployments should keep access tokens at or below `1h` and refresh tokens at or below `720h`.
- Storage rules: `storage.root` remains read-only through the Settings API. `storage.directory_quotas` accepts MnemoNAS logical paths with positive `quota_bytes`.
  `storage.directory_access_rules` accepts MnemoNAS logical paths plus read/write grants for `*_users`, `*_groups`, and `*_roles`; the most specific matching rule wins, and write grants also allow reads.
- WebDAV auth: `webdav.auth_type` supports `users`, `basic`, and `none`; blank values are normalized to `basic`, and `users` requires app auth to remain enabled.
- WebDAV prefix: `webdav.prefix` is normalized to a `/`-prefixed URL path, must not contain backslash, `?`, `#`, or control characters, and when enabled must not overlap `/`, `/api`, `/s`, or `/health`.
- WebDAV password: omitting `webdav.password` preserves the existing WebDAV password, while submitting an empty string switches Basic Auth back to the generated password from `secrets.json`.
- URL fields: non-empty `share.base_url`, `alerts.webhook_url`, `alerts.wecom_webhook_url`, and `alerts.dingtalk_webhook_url` values must be absolute `http` or `https` URLs with a valid host name or IP address.
  `share.base_url` also must not contain userinfo, query strings, fragments, encoded query or fragment markers, backslashes, duplicate path slashes, or `.`/`..` path segments.
- Share policy: `share.default_expires_in` must be empty, `0`, or a non-negative Go duration string; `share.default_max_access` must be zero or greater.
  `share.policy_rules` entries must use MnemoNAS logical paths and set at least one of `require_password`, `max_expires_in`, `max_access`, `allowed_users`, `allowed_groups`, or `allowed_roles`. Allowed-scope fields are trimmed, deduplicated, and normalized to lowercase; roles accept only `admin`, `user`, or `guest`.
- Alert Webhook: `webhook_method` supports `GET` and `POST`. Custom webhook headers use `"Key: Value"` strings with valid HTTP token names, case-insensitively unique names, and values without newlines or control characters. Webhook, Telegram, WeCom, and DingTalk outbound requests do not follow HTTP redirects; `3xx` responses are treated as delivery failures.
- Storage alerts: `storage_alert` deliveries keep capacity metrics and `path_scope = "configured_storage_root"` but set `path` to `<omitted>`, and text channels do not include the raw storage root path.
- Secret responses: `GET /api/v1/settings` does not return Webhook URL/header values, WeCom webhook URLs, or DingTalk webhook URLs.
  `alerts.webhook_url`, `alerts.webhook_headers`, `alerts.wecom_webhook_url`, and `alerts.dingtalk_webhook_url` use `<redacted>` placeholders for configured values, and `*_configured` booleans indicate whether those values exist.
- Secret updates: `PUT /api/v1/settings` can submit real Webhook URL/header values, WeCom webhook URLs, and DingTalk webhook URLs to update the configuration; submitting the same `<redacted>` placeholder preserves the corresponding existing value.
  Omitting `alerts.telegram_bot_token` or `alerts.smtp_password` preserves the stored secret; submitting an empty string clears the corresponding stored secret.
- Telegram: clearing `alerts.telegram_bot_token` is invalid while `alerts.telegram_enabled` remains true.
  When `alerts.telegram_enabled` is true, `telegram_bot_token` and `telegram_chat_id` are required; the bot token cannot contain whitespace, `/`, `?`, or `#` and is never returned by settings or diagnostics responses.
- WeCom and DingTalk: when `alerts.wecom_enabled` is true, `wecom_webhook_url` is required and is never returned by settings or diagnostics responses. When `alerts.dingtalk_enabled` is true, `dingtalk_webhook_url` is required and is never returned by settings or diagnostics responses.
- Email alerts: when `alerts.email_enabled` is true, `smtp_host`, `smtp_from`, and at least one `smtp_to` recipient are required; `smtp_port` must be 1-65535, and sender/recipient values must be valid email addresses.
- Disk health: `disk_health.command` must be a single executable name or absolute path, `disk_health.media_wear_critical_percent` must not be lower than `disk_health.media_wear_warning_percent`, and each `disk_health.devices[].path` must be absolute.
- Maintenance: `maintenance.scrub.schedule_interval` and `maintenance.scrub.retry_interval` must be positive duration strings, and `maintenance.scrub.max_retries` must be zero or greater.
- Dataplane: `dataplane.grpc_address` must be a valid `host:port` address with port 1-65535 and no whitespace or control characters.
  CDC chunk sizes must satisfy `65536 <= min_chunk_size < avg_chunk_size < max_chunk_size <= 67108864`.

### Send Test Alert

```
POST /api/v1/settings/alerts/test
```

**Requires administrator access**

The endpoint sends one `alert_test` warning event through the currently saved alert channels. Calls require:

- `[alerts] enabled = true`.
- At least one configured Webhook, Telegram, WeCom, DingTalk, or SMTP email channel.
- An available alert runtime.

The WeCom and DingTalk channels count as configured only when the channel is enabled and the webhook URL is non-empty. The SMTP email channel counts as configured only when email alerts are enabled and SMTP host, port, sender, and at least one non-empty recipient are present.

Test event details contain only `trigger = "manual_test"`, `source = "settings"`, and the channel list; Webhook, Telegram, WeCom, DingTalk, and SMTP secrets are not included.

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

`POST /api/v1/settings/access-check` accepts `{"username":"alice","path":"/team/report.pdf"}` and returns `read` and `write` decisions.

Each decision includes `allowed`, `source`, optional `message`, and the `matched_rule` when a directory access rule decided the result.

`source` can be `admin`, `home_dir`, `directory_access_rule`, `invalid_home_dir`, `user_disabled`, `user_not_found`, or `auth_disabled`.

Nested directory grants allow a read-only navigation ancestor only when the descendant directory currently exists; in that case, `matched_rule` points to that descendant rule.

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

`POST /api/v1/settings/access-report` accepts `{"path":"/team/report.pdf"}` and returns the same read/write decisions for every user.

The response includes a `summary` with user count, read allows/denials, write allows/denials, and related share counts.

The optional `rule_effects` list summarizes the read/write allow and denial counts for directory-access rules that actually affected this check. `index` is the zero-based position in the saved or submitted `directory_access_rules` array, and `user_samples` contains representative users for impact review.

The optional `shares` list reports shares that exactly match the path, parent folder shares that cover it, and child shares under the checked directory. It is intended for administrator permission checks before changing shared-directory or share rules.

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
    "rule_effects": [
      {
        "path": "/team",
        "index": 0,
        "read_allowed": 1,
        "read_denied": 1,
        "write_allowed": 1,
        "write_denied": 1,
        "user_samples": ["alice", "bob"]
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

`POST /api/v1/settings/access-preview` accepts `{"path":"/team/report.pdf","directory_access_rules":[...]}` and returns the same user matrix and related-share impact using only the supplied unsaved rules.

It does not persist settings and returns `preview: true`. Nested directory grants are also evaluated as read-only navigation ancestors only when the descendant directory currently exists, so the preview can be used to check the actual impact before saving family or small-team shared-directory rules.

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
    ],
    "rule_effects": [
      {
        "path": "/team",
        "index": 0,
        "read_allowed": 1,
        "read_denied": 0,
        "write_allowed": 0,
        "write_denied": 1,
        "user_samples": ["alice"]
      }
    ]
  }
}
```

### Directory-Access Review History

`GET /api/v1/settings/access-reviews` returns recent directory-access review records and supports `limit` and `offset` query parameters. `limit` accepts 1-100 and defaults to 20.

`POST /api/v1/settings/access-reviews` accepts the directory-access matrix or unsaved-rule preview summary generated by the Settings page. The server uses the current authenticated account as `reviewer`, sets `reviewed_at`, and persists at most the latest 100 records. A new record replaces an older record with the same reviewer, path, title, and preview flag.

`DELETE /api/v1/settings/access-reviews` clears persisted directory-access review records.

Create request example:

```json
{
  "title": "User matrix",
  "path": "/team/report.pdf",
  "preview": false,
  "users": 2,
  "read_allowed": 1,
  "read_denied": 1,
  "write_allowed": 1,
  "write_denied": 1,
  "related_shares": 1,
  "active_related_shares": 1,
  "password_protected_shares": 1,
  "report_text": "Directory access review record\nPath: /team/report.pdf"
}
```

List response example:

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "review-id",
        "reviewed_at": "2026-06-20T08:30:00Z",
        "reviewer": "admin",
        "title": "User matrix",
        "path": "/team/report.pdf",
        "preview": false,
        "users": 2,
        "read_allowed": 1,
        "read_denied": 1,
        "write_allowed": 1,
        "write_denied": 1,
        "related_shares": 1,
        "active_related_shares": 1,
        "password_protected_shares": 1,
        "report_text": "Directory access review record\nPath: /team/report.pdf"
      }
    ],
    "total": 1,
    "limit": 20,
    "offset": 0
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
      "webdav_prefix": "/dav",
      "webdav_auth_type": "basic",
      "smb_enabled": false,
      "allow_unsafe_no_auth": false,
      "share_enabled": false,
      "share_default_expires_in": "168h",
      "share_default_max_access": 0
    }
  }
}
```

`data.status` and `checks[].status` use `pass`, `warning`, or `block`. For the aggregate status, `block` dominates `warning`, and `warning` dominates `pass`.

Current check IDs include
`auth_enabled`, `session_token_ttl`, `login_rate_limit`, `browser_session_boundary`, `public_share_boundary`, `unsafe_no_auth_override`,
`config_file_access`, `secrets_file_access`, `users_file_access`, `https_request`, `public_http_exposure`, `trusted_proxy_or_tls`,
`forwarded_proto_trust`, `server_listen`, `admin_accounts`, `dataplane_listen`, `dataplane_http_listen`, `webdav_prefix`, `webdav_auth`,
`smb_preview`, `share_base_url`, `share_default_policy`, `backup_local_destinations`, and `initial_password_file`.

Grouped by scope:

- Auth and session: `auth_enabled`, `session_token_ttl`, `login_rate_limit`, `browser_session_boundary`, `unsafe_no_auth_override`, `admin_accounts`, and `initial_password_file`.
- Public entry and proxy: `https_request`, `public_http_exposure`, `trusted_proxy_or_tls`, `forwarded_proto_trust`, and `server_listen`.
- Runtime file permissions: `config_file_access`, `secrets_file_access`, and `users_file_access`.
- Dataplane and protocol entry points: `dataplane_listen`, `dataplane_http_listen`, `webdav_prefix`, `webdav_auth`, and `smb_preview`.
- Share and backup policy: `public_share_boundary`, `share_base_url`, `share_default_policy`, and `backup_local_destinations`.

Important check semantics:

- `session_token_ttl` checks the Web UI access-token and refresh-token lifetimes.
  Public deployments should keep `auth.access_token_ttl <= 1h` and `auth.refresh_token_ttl <= 720h`; longer values are reported as `warning`.
  Details include only TTL text, seconds, and long-TTL booleans, never token contents.
- `login_rate_limit` checks consecutive failed-login throttling for the Web UI.
  With authentication enabled, each client IP can perform at most 12 credential checks in 10 seconds. Consecutive failures are separately counted by username and client IP; reaching the threshold applies a short lockout and emits the `login_rate_limited` alert event.
  Details include `credential_check_limit=12`, `credential_check_window=10s`, `credential_check_window_seconds=10`, `credential_check_scope=client_ip`, the failure threshold, counting window, lock duration, alert cooldown, and key scope, never usernames, passwords, or tokens.
- `browser_session_boundary` checks whether the current browser access path will set the `Secure` flag on Web UI session cookies and download cookies, and confirms that same-origin metadata validation is enabled for browser write requests.
  It reports `warning` when Web login authentication is disabled or the current request is not recognized as HTTPS.
  Details report HTTPS `__Host-` names, path, Domain, and request-mode isolation through `session_cookie_host_prefix`, `session_cookie_name_prefix`, `session_cookie_path`, `session_cookie_domain_set`, and `request_mode_cookie_name_isolation`. They also include request scheme, proxy trust, and same-origin validation booleans, never token or cookie values.
- `public_share_boundary` checks public-share access cookies, password-failure throttling, and public-share JSON response cache boundaries when sharing is enabled.
  Invalid HttpOnly, SameSite, cookie-path scoping, failure-throttling, `Cache-Control: private`, `Cache-Control: no-cache`, `Vary: Cookie`, `nosniff`, or `Referrer-Policy: no-referrer` boundaries are reported as `block`.
  Only when those boundaries are valid and the current request is not recognized as HTTPS are password-protected share cookies without `Secure` reported as `warning`.
  Details include only cookie attributes and path-scope state, public-share JSON cache and referrer boundary state, `Vary: Cookie`, `nosniff`, and password-failure rate-limit parameters, never share passwords, cookie values, or share IDs.
- `config_file_access` checks the runtime config file path.
  Empty, missing, or unconfirmed paths are reported as `warning`; symlink path components, symlink files, or non-regular files are reported as `block`; group or other-user access on the file is reported as `warning`.
  Details include observable path, mode, type, symlink component, and group/other access fields through `details.path`, `details.mode`, `details.path_kind`, `details.symlink_component`, and `details.group_or_other_access`.
  `details.path_kind` can be `missing`, `regular`, `symlink`, `symlink_component`, or `not_regular`.
- `secrets_file_access` checks `secrets.json` when WebDAV uses the generated Basic Auth password from that file.
  If the generated password is not required, the check is `pass`; empty, missing, or unconfirmed paths, symlink path components, symlink files, or non-regular files are reported as `block`; group or other-user access on the file is reported as `warning`.
  Details include only observable metadata through `details.path`, `details.mode`, `details.path_kind`, `details.symlink_component`, `details.group_or_other_access`, `details.generated_webdav_password_required`, `details.webdav_enabled`, and `details.webdav_auth_type`, never password contents.
- `users_file_access` checks the runtime users file path.
  Missing paths, unreadable directories, symlink path components, symlink directories, unreadable files, symlink files, or non-regular files are reported as `block`; group or other-user access on the directory or file is reported as `warning`.
  Details include observable path, directory, mode, type, symlink component, and group/other access fields.
  These fields are exposed through `details.path`, `details.dir`, `details.file_mode`, `details.dir_mode`, `details.file_kind`, `details.dir_kind`, `details.symlink_component`, `details.file_group_or_other_access`, and `details.dir_group_or_other_access`.
  Symlink path components return `details.dir_kind` as `symlink_component`.
- `admin_accounts` checks the number of enabled administrators.
  Disabled authentication or an unreadable user store is `warning`, zero enabled administrators is `block`, one enabled administrator is `warning`, and two or more is `pass`.
  When readable, `details.active_admins` contains the enabled administrator count.
- `initial_password_file` checks `initial-password.txt` next to `auth.users_file`.
  An absent file is reported as `pass` with `details.path_kind="missing"`; a remaining regular file, symlink, symlink path component, or non-regular file is reported as `block`.
  Details include the checked path in `details.path` and, when observable, mode/type metadata through `details.mode`, `details.path_kind`, and `details.symlink_component`.
  Symlink, symlink path-component, or non-regular cases return `details.path_kind` as `symlink`, `symlink_component`, or `not_regular`.
- `webdav_prefix` checks the WebDAV URL prefix when WebDAV is enabled.
  Empty prefixes, root prefixes, invalid path characters, or prefixes under `/api`, `/s`, or `/health` are reported as `block` with `details.prefix_risk` and `details.normalized_prefix`.
- `webdav_auth` checks the authentication mode.
  `auth_type = "none"` is reported as `block` on non-loopback listeners.
  Global Basic Auth passwords that are explicit common placeholders or shorter than 16 characters are reported as `warning` with `password_source` and a `password_risk` type, never the password value.
  When Basic Auth uses the generated password, an unavailable runtime password is reported as `block` with `generated_password_available=false`, and a weak generated password is reported as `warning`.
- `forwarded_proto_trust` checks `X-Forwarded-Proto` against trusted-proxy settings.
  The header without `trusted_proxy_hops` is a `warning`, the header from an untrusted direct peer is a `block`, and a trusted direct peer forwarding a value other than `https` is a `warning`.
- `share_base_url` checks the public share-link base URL when sharing is enabled.
  HTTP, a non-443 HTTPS port, URL userinfo, query strings, fragments, encoded query or fragment markers, backslashes, duplicated path slashes, `.`/`..` path segments, or an invalid host name is reported as `block`.
  Empty values, a different host, or a base path ending in the `/s` sharing route remain manual-review warnings.
- `share_default_policy` checks the default expiry and default access count for newly created shares.
  When sharing is enabled, no default expiry, values longer than `720h`, or unlimited default access counts are `warning`; negative defaults are `block`.
  Details include only default expiry/access-limit metadata and policy-rule count.
- `backup_local_destinations` checks enabled local backup job destinations.
  No local jobs or all local jobs disabled is `pass`; an empty or relative target, a target inside the backup source or `storage.root`, a symlink path component, symlink target, or non-directory target is `block`; a missing, unconfirmed, or non-writable target is `warning`.
  Details include observable metadata through `details.job_id`, `details.destination`, `details.source`, `details.storage_root`, `details.destination_kind`, `details.symlink_component`, `details.local_job_count`, and `details.enabled_local_job_count`.

The endpoint can verify only what the MnemoNAS process can observe: runtime configuration and the current request's proxy/TLS semantics. It cannot directly verify cloud security groups, public routing, externally exposed ports, or certificate-chain validity.

Public deployments should still run `sudo mnemonas-doctor --public-domain <domain>` on the server and confirm in the cloud console that only `80/443` are publicly reachable.

If the runtime users-file path is empty, `initial_password_file` returns `block` with an empty `details.path` instead of probing the current working directory.

## Maintenance

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/maintenance/scrub` | Get latest scrub results |
| `POST` | `/api/v1/maintenance/scrub` | Start scrub |
| `GET` | `/api/v1/maintenance/disk-health` | Run and return disk SMART/temperature health |
| `GET` | `/api/v1/maintenance/objects` | List storage objects |
| `POST` | `/api/v1/maintenance/gc` | Run garbage collection |
| `GET` | `/api/v1/maintenance/backups` | List configured backup jobs |
| `POST` | `/api/v1/maintenance/backups` | Create a local full-storage backup job |
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

`POST /api/v1/maintenance/gc` starts garbage collection for unreferenced data chunks. Query parameters:

- `dry_run`: optional boolean, at most once. The default is `true`; deletion only runs when this value is explicitly `false`.
- `grace_period_hours`: optional non-negative integer, at most once. The default is `24`; objects created inside the grace period are skipped.

When `dry_run=false` and some deletions fail, the response includes `failed_count` and `delete_failures`.

`POST /api/v1/maintenance/backups` creates a local backup job whose source is the current `storage.root`. The server generates the job ID. This endpoint does not accept job type, source path, remote repository, or external-command fields. Example request:

```json
{
  "name": "External disk backup",
  "destination": "/mnt/backup-drive/mnemonas",
  "schedule_interval": "24h",
  "max_snapshots": 7,
  "include_config": true,
  "verify_after_backup": true
}
```

`name` and `destination` are required. `destination` must be a safe absolute path outside `storage.root`; it cannot be a protected system directory, regular file, or path that traverses a symlink. The optional fields default to `24h`, `7`, `true`, and `true`, respectively. Set `schedule_interval` to `"0"` for a manual-only job. A successful request returns `201 Created`, the job view, and a `Location` header for the job-status endpoint. The configuration is safely persisted to `config.toml` before the job is added to the running backup manager, so no service restart is required.

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
        "title": "Target path isolation",
        "detail": "The target directory is outside the current data directory, backup source, and local backup destinations."
      },
      {
        "id": "target_state",
        "status": "passed",
        "title": "Target directory state",
        "detail": "The target directory does not exist yet; restore writes to a temporary directory first, then installs it at that path."
      },
      {
        "id": "backup_content",
        "status": "passed",
        "title": "Backup content",
        "detail": "The preview found 42 files and expects to restore 1 MB."
      },
      {
        "id": "target_capacity",
        "status": "passed",
        "title": "Target capacity",
        "detail": "The target filesystem has 100 GB available and the restore is expected to write 1 MB."
      },
      {
        "id": "config_restore",
        "status": "passed",
        "title": "Config file",
        "detail": "The local snapshot contains a config file, which will be restored to .mnemonas-restore/config.toml."
      }
    ],
    "warnings": [],
    "cutover_checklist": ["Run read-only verification on the restored directory first"],
    "rollback_checklist": ["If cutover fails, stop services and point storage.root back to the previous directory"]
  }
}
```

`restore-preview` reuses explicit restore target safety validation and returns `preflight_checks`, `warnings`, `cutover_checklist`, and `rollback_checklist`.

Preflight covers target isolation, `target_state`, backup content, target filesystem capacity, and config handling. `target_state` distinguishes two allowed states: the target directory does not exist, or the target directory already exists and is empty.

Missing targets use the parent directory for the capacity probe; existing empty target directories use the target directory's filesystem.

`preflight_checks[].status` can be `passed`, `warning`, or `failed`. `status = "warning"` means restore can continue after review; `status = "failed"` prevents the Maintenance page from starting restore and is rejected by server-side preflight before `/restore` writes data.

`warnings` aggregates warning and failed preflight details for preview cards, batch previews, and restore history.

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

Maintenance and diagnostic behavior:

- Scrub object errors return stable public `errors[].message` values; lower-level IO, path, and verification details are kept in server logs.
- Manual scrub runs write `scrub` activity-log entries. When `[maintenance.scrub] enabled = true`, the server runs full Scrub jobs in the background as the system user according to `schedule_interval`; failed runs retry after `retry_interval` up to `max_retries`.
- Scheduled runs use the same maintenance history, activity-log details, result shape, and alert events as manual runs.
  Scrub failures, object verification problems, and incomplete result persistence send `scrub_run` events through configured Webhook/Telegram/WeCom/DingTalk/SMTP alert channels; alert details omit object hashes and lower-level error text.
- `GET /api/v1/maintenance/disk-health` uses `[disk_health]` and `smartctl --json --all` to report `disabled`, `ok`, `warning`, `critical`, or `unavailable`.
  Missing devices, SMART failures, serial mismatches, critical temperatures, NVMe critical warnings, exhausted spare capacity, media-wear thresholds, and media errors affect device status.
- Periodic checks that find warning, critical, or unavailable status write a `disk_health` activity-log entry at `/system/disk-health` for the `system` user.
  When alert channels are configured, periodic disk-health checks send `disk_health` events for warning, critical, and unavailable states.
- Activity entries use the configured device `name` in summaries; alert-event details use only aggregate counts and do not include device names, full device paths, serial numbers, or warning text.
  Full device paths and SMART details are returned only by the administrator maintenance endpoint.
- `GET /api/v1/maintenance/objects` accepts optional `limit` and `cursor` query parameters.
  `limit` defaults to 1000 and may not exceed 1000; `cursor` comes from the previous `next_cursor` and must be a 64-character hexadecimal object hash when non-empty.
  `limit` and `cursor` may each appear at most once.

Diagnostic responses:

- `GET /api/v1/diagnostics` and `/diagnostics-export` include sanitized filesystem stats.
  When `filesystem.disk_stats_available=true`, `filesystem.disk_*` can include capacity values, `disk_filesystem_type`, redacted Linux mountinfo metadata, and `disk_native_data_checksum_support`.
- Both diagnostic endpoints set `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer` because diagnostics can contain operational state. `/diagnostics-export` returns an attachment, sets root `schema_version = 1`, and uses UTC for `export_time` plus the attachment filename timestamp.
- Diagnostic responses expose only alert-channel booleans for Webhook, Telegram, WeCom, DingTalk, and SMTP email.
  The SMTP email boolean is true only when email alerts are enabled and SMTP host, port, sender, and at least one non-empty recipient are present.
- Diagnostics never include Webhook URL/header values, Telegram bot tokens, WeCom webhook URLs, DingTalk webhook URLs, SMTP host, SMTP username, SMTP password, sender address, or recipient addresses.
- Diagnostic responses include a sanitized `maintenance` summary with `history_ready`, `[maintenance.scrub]` schedule settings, the latest Scrub status/time, and the retry count for the latest failed Scrub.
- Diagnostic responses include sanitized `smb` preview state. Current builds do not bundle an SMB/Samba runtime, so `runtime_available=false` means the configured SMB shares are not mountable.
  Diagnostics expose share counts, runtime state, and the stable "use WebDAV for current LAN mounts" guidance, but never SMB credential contents.

Backup job types and command execution:

- Backup endpoints operate on jobs configured under `[[backup.jobs]]`. Supported job types are `local`, `restic`, and `rclone`.
- Local jobs copy into `destination/<job-id>/snapshots/<run-id>/` and can prune old snapshots by `max_snapshots` and `max_age`. The v2 `manifest.json` records regular files and `data/` directories separately and binds the exact directory topology, empty directories, and POSIX `rwx` permission bits; setuid, setgid, sticky, and other special permission bits are rejected. It does not record ACLs, extended attributes, or ownership. The structural `config/` directory is not part of the directory list and uses mode `0700` during restore.
- Restic jobs invoke `restic -r <repository> --password-file <password_file> backup <source>` and optionally `restic check`.
  rclone jobs invoke `rclone sync <source> <remote>` and optionally `rclone check --one-way`.
- External commands are executed without a shell. `command` must be a bare executable name or absolute path, and `extra_args` are appended to backup commands but cannot override job identity. Rclone currently accepts only `--fast-list`; restore commands do not reuse backup-specific extra args.
- Backup runs reject symlinks in the `source` tree; `rclone` restore drills apply the same check before remote verification.
- `restic.password_file` and `rclone.config_file` are required for their respective job types. Each must be a non-symlink regular file no larger than 4 MiB, and both the file and its private runtime snapshot must remain outside `source` and `storage.root`.
- `restic.repository` accepts only an absolute local path outside the source and `storage.root`, or an explicit `rest:http://` or `rest:https://` REST server URL.
- `rclone.remote` must reference a named remote in `config_file` whose section contains `type`. The file must be a static, self-contained plaintext config; it cannot enable `env_auth`, contain `${...}` expansion, or use a non-empty option whose name contains `_file`, `_path`, `command`, `agent`, `ssh`, or `token`. The current API does not support encrypted rclone configs, environment credentials, external credential sources, or automatic token write-back.
- Remote commands read only a mode-0600 credential snapshot created under a mode-0700 directory in the resolved system temporary directory. Child processes do not inherit cloud-provider credentials, proxy settings, SSH agents, or `RESTIC_*` and `RCLONE_*` variables that can override the explicit job identity. Repositories, remotes, and credential files come only from the validated job configuration.

Backup redaction and alert boundaries:

- API job views, run results, restore or preview results, restore reports, and batch restore results redact userinfo, tokens, passwords, secrets, and key parameters embedded in display fields.
  Affected fields include `repository`, `remote`, `destination`, `target_path`, `snapshot_path`, `manifest_path`, and `config_path`.
- The same redaction patterns apply to API-visible backup `error_message`, `warnings`, and preflight details.
- Backup alert events do not include sources, destinations, restore target paths, snapshot or manifest paths, or raw warning/error text.
  They retain only summary fields such as status, trigger, counts, timestamps, failure category, and markers for omitted location or error details.
- Restic/rclone commands still receive the original configured values. Clients that chain `restore-preview`, `restore`, and `restore-verify` should retain and reuse the original request `target_path`; a redacted response `target_path` is intended only for display.
- Job view `restore_report_findings` and downloaded restore-report `findings` text use the same backup credential redaction rules.
- Restore-report download responses set `Cache-Control: no-store`, `Pragma: no-cache`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer` because reports can include restore status and operational decisions.

Scheduling, retention, and status:

- Jobs may define `disabled`, `schedule_interval`, `schedule_window_start`, `schedule_window_end`, `stale_after`, `restore_drill_stale_after`, `max_snapshots`, `max_age`, and `retention_policy`.
- A positive `schedule_interval` enables the in-process scheduler. If both schedule-window fields are set, automatic runs only start inside that server-local `HH:MM` window, while manual run-now operations are unaffected.
- Job views include backup `health_status` (`ok`, `manual`, `running`, `due`, `stale`, `failed`, or `disabled`), `retention_status`, and `restore_drill_status` plus optional messages.
- Successful backups run a retention check automatically, and `POST /retention-check` can run it manually.
  Local checks count the local snapshot range, restic checks run `restic snapshots --json --tag mnemonas --tag job:<id>`, and rclone checks run `rclone lsjson <remote> --recursive --files-only`.
- Local automatic pruning always keeps the current snapshot and snapshots still referenced by completed restore history, and applies `max_snapshots` and `max_age` only to v2 snapshots whose manifest identity and snapshot tree layout validate. V1 snapshots remain pinned, are excluded from v2 count and age calculations, and produce a manual-disposition warning in retention results.
- Results persist as `last_retention_check` and feed `retention_status`/`retention_message`.
  `retention_policy` marks restic/rclone remote retention as externally confirmed; otherwise remote jobs report a retention warning.
- The backup manager holds a lifetime lock on `<storage.root>/.mnemonas/backup/backup-state.lock`, allowing only one writer for a state root. On Unix, the state root must be owned by the effective service account or `root`, must not be writable by group or other users, and its ancestors must not allow another local account to replace the state root. Backup endpoints return `503` when the manager cannot acquire this lock.
- The manager retains the filesystem identity of the locked state root and verifies the original path during API availability checks and around state writes. Moving, removing, or replacing the state root immediately quarantines the manager; it does not follow the new path or write into the replacement directory. An operation's atomically committed `completed` result remains completed. Its `warnings[]` explicitly states that the state-directory identity changed, the current result was generated, later backup work stopped, and the operator must inspect the state directory and restart the service. Later backup API requests return `503`. An ordinary hard persistence failure before atomic commit returns `500`.
- A `local` job uses `<destination>/<job-id>/.mnemonas-target.lock` for backup, restore preview, restore drill, restore, post-restore read-only verification, and retention-check operations. An operation returns `409` when another process holds the local target lock. If target-lock release cannot be confirmed, the operation returns `500`; a concurrent business conflict such as “no snapshots” does not downgrade that response to `409`. On Unix, the job directory must be owned by the effective service account or `root` and must not be writable by group or other users. Its ancestors must have trusted owners; a writable ancestor is accepted only when the sticky bit is set and prevents replacement of a trusted child directory. An unsafe target rejects the operation with `500`.
- External disks using FAT, exFAT, or another filesystem that does not persist Unix ownership and mode require secure `uid`, `gid`, `dmask`, and `fmask` mount values. For example, use the actual service UID/GID with `uid=<mnemonas-uid>,gid=<mnemonas-gid>,dmask=0077,fmask=0177`. A `local` operation is rejected when the target has neither reliable Unix mode nor equivalent mount constraints.
- A v2 local manifest requires the source, snapshot, and restore target to provide stable POSIX `rwx` permission semantics. Native Windows cannot preserve these bits losslessly, so MnemoNAS rejects v2 local backup, restore preview, restore, restore-drill, restore-verification, and retention operations on native Windows.
- Remote jobs do not create a local target lock. Restic jobs rely on native repository locking, while rclone has no generic distributed mutex that applies to every remote. External scheduling must serialize backup, restore, and verification operations when multiple jobs or instances share the same rclone path.

Restore drills and restore reports:

- `restore_drill_stale_after` defaults to 30 days when empty or omitted and drives restore-drill reminder status.
  When alert channels are configured, stale or missing restore drills send rate-limited `backup_restore_drill` warning events with `trigger=restore_drill_reminder`. The manager persists `last_restore_drill_reminder_at` only after a notification channel returns success. Delivery failure does not advance the cooldown; successful delivery followed by marker-persistence failure is retried on the next scheduler pass and may produce a duplicate.
- Restore-drill history is capped to the latest 20 entries and records status, file/byte counts, artifact paths, failure messages, and stable `failure_category` values for failed drills.
  Current categories are `no_snapshot`, `unsupported_job_type`, `unsafe_path`, `integrity_check`, `external_command`, `cancelled`, `io`, and `unknown`.
- Failure categories are forwarded to alert event details.
  Job views also return `restore_drill_stats`, which summarizes total runs, successes, failures, success rate, consecutive successes or failures, latest success/failure time, latest failure message, and latest failure category across that retained window.
- Restore history is also capped to the latest 20 entries and records target path, status, file/byte counts, preflight checks, warnings, rollback/cutover checklists, and failure messages.
  `last_restore_verify` persists the latest read-only post-restore verification result after page refresh.
- Job views return `last_matching_restore_verify` when the latest restore has a matching read-only verification, and `restore_report_findings` with the same pending findings used by restore reports.
- `GET /restore-report` downloads an `application/json` attachment with the job view, latest backup, retention check, restore drill, restore-drill history and stats, latest restore, latest restore verification, matched verification, restore history, and findings.

Backup alert events:

- When `[alerts] enabled = true` and alert channels are configured, backup failures, explicit restore failures or warnings, and post-restore read-only verification failures or warnings send events.
  Restore-drill failures or warnings, stale/missing restore-drill reminders, retention-check failures/warnings, and backup-warning runs also send events.
- Event types are `backup_run`, `backup_restore`, `backup_restore_verify`, `backup_restore_drill`, or `backup_retention_check`, with level `warning` or `critical`.
- The `message` is a fixed public summary and does not include job names, paths, or raw error text.
- Non-empty `details` summary fields can include job ID, run ID, job type, trigger, status, timestamps, file/byte/snapshot counts, warning count, error-message presence, failure category, and whether location details were omitted.
  They do not include job names, sources, backup targets, restore target paths, snapshot paths, manifest paths, raw warnings, or raw error text.
- A custom `backup.Notifier` receives synchronous `NotifyBackupEvent` calls with a 10-second deadline and manager-shutdown cancellation. Implementations must observe `ctx.Done()` and return promptly after cancellation or deadline expiry; otherwise they can block the current operation or service shutdown. The built-in SMTP transport uses a 30-second default timeout and honors an earlier upstream deadline, so backup events remain subject to the 10-second notification budget.

Backup operation semantics:

- `POST /run` accepts an empty body or `{}`. The endpoint persists the `running` state before creating a local snapshot or starting a remote command. If that write fails, it returns `500` without backup-target side effects.
  After a local snapshot syncs its files, manifest, and directories and is published to the final directory, the endpoint commits the successful run with manifest evidence before snapshot cleanup and retention checking. If successful-state persistence fails before atomic replacement, it returns `500` with the failed run in `details`, preserves the previously bound successful snapshot, and does not use the unbound final snapshot for restore.
  If the backup is committed but the state parent-directory sync, later cleanup, retention check, or result persistence is incomplete, the endpoint returns `200 OK`; the result remains `status="completed"` and sets `warning=true` and `warnings[]`. The response also sets `message="backup completed with warnings"` and `Warning: 199 MnemoNAS "backup run completed with warnings"`. Successful responses without warnings continue to use `message="backup completed"`.
  If the state-directory identity changes after the current result is atomically committed, the run uses the same warning-success response, but the manager is quarantined and later requests return `503`. An ordinary hard persistence failure does not use this warning behavior and returns `500`.
- `POST /retention-check` accepts an empty body or `{}` and returns `snapshot_count`, `file_count`, `total_bytes`, snapshot time range, `warning`, and `warnings`; failures return `500` with the failed check in `details`.
- `POST /restore-drill` accepts optional `{"keep_artifact": true}`.
  Local jobs check and reconstruct the complete directory set from the trusted v2 manifest, then verify directory topology, empty directories, POSIX `rwx` permission bits, and file evidence; they do not derive the directory baseline from the snapshot's current directory tree. Restic jobs run `restic check`, and rclone jobs run `rclone check --one-way`.
  For local jobs with the default non-retained artifact behavior, if snapshot verification completes but temporary restore-directory cleanup fails, the response remains `status="completed"` and sets `warning=true`, populates `warnings[]`, and returns `artifact_kept=true` with `restored_path` for the Maintenance page. Warning text does not include raw paths or lower-level error text.
- `POST /restore-preview` validates the same target rules as restore but does not create target data or write restore history.
  It returns `preflight_checks`, `warnings`, `cutover_checklist`, and `rollback_checklist` for target isolation, target state, backup content, target filesystem capacity, and config handling.
- Local jobs summarize only the v2 manifest bound to `status.json.last_successful_run`; they do not scan snapshot directories to infer the latest snapshot. The manifest path, size, digest, job ID, run ID, and summary fields must match the evidence saved by the successful run. A new successful local backup is required for a v1 manifest or when that evidence is absent. Restic jobs run `restic ls latest --json --tag mnemonas --tag job:<id> --path <source>`, and rclone jobs run `rclone lsjson <remote> --recursive --files-only`.

Batch restore:

- `POST /batch-restore-preview` accepts `{"items":[{"job_id":"external-disk","target_path":"/absolute/restore/a","include_config":true}]}` with at most 20 items.
  It rejects duplicate or nested target paths in the same batch and returns per-item preview status, `error_message`, and warnings without writing target data or restore history.
- `POST /batch-restore` uses the same request shape, executes items sequentially, and runs read-only `restore-verify` after every successful restore.
  Responses return per-item `restore`, `verify`, `warnings`, and `error_message` fields.
- Top-level `total_files` and `verified_bytes` aggregate completed items' read-only verification results. Batch restore error and warning text uses the same remote-target credential redaction.
- Partial failures return overall `status="completed"` with `warning=true`; all item failures return `status="failed"`, so clients must inspect `items[]`.
- That aggregation behavior applies only to ordinary item failures. A state-persistence failure or unconfirmed target-lock release makes the batch endpoint return `500`; a changed state-directory identity or quarantined manager returns `503`. Error-response `details` preserves the batch and item results generated so far.
- For the batch preview response, per-item preview warnings are reported under `items[].preview.warnings`; aggregate messages are reported under top-level `warnings`.

Explicit restore and read-only verification:

- `POST /restore` supports local, restic, and rclone jobs and requires `{"target_path": "/absolute/restore/path", "include_config": true}`.
- The target must be an absolute server-side POSIX path that starts with `/`, must not contain control characters, backslashes, or `.`/`..` path segments, and must not be the filesystem root or a protected system directory.
  It must also be outside `storage.root`, the backup source, and any local backup destination or repository.
- Windows and UNC paths are not valid server restore targets. The parent must exist, and the target must not exist or must be empty.
- The server reruns the same restore preflight before writing; failed preflight checks reject the restore and are persisted with the failed restore result.
- Local restore treats the trusted manifest as authoritative for directories, reconstructs the exact `data/` topology, empty directories, and POSIX `rwx` permission bits at the target root, then copies files and verifies their size, mode, and SHA-256. It does not read current snapshot-directory permissions to determine the restored result. Before installation, a final explicit-target tree check rejects extra or missing entries, permission drift, and case-folding collisions. When requested, config is restored to `.mnemonas-restore/config.toml`, and the structural `.mnemonas-restore/` directory uses fixed mode `0700`.
- When the same manifest contains a restorable config and its `data/` tree occupies the `.mnemonas-restore` file or directory namespace, including a case-only equivalent, `restore-preview` and `restore` fail on the namespace conflict for `include_config=true`. A data-only restore is not subject to this reserved config namespace.
- Explicit restore uses a private staging directory adjacent to the target and rechecks its filesystem identity at installation and cleanup boundaries. On Unix, the staging directory and its ancestors must also satisfy trusted-owner and non-replaceability constraints; an identity change fails the operation as an unsafe path.
- Restic restore runs `restic restore latest --target <staging> --tag mnemonas --tag job:<id> --path <source>`.
  The server installs the restored source directory contents into the target root after rejecting restored symlinks and special files.
- Rclone restore runs `rclone copy <remote> <staging>` and then `rclone check <remote> <staging> --one-way`.
  Restored symlinks and special files are rejected before the server installs the staging directory into the target path.
- `include_config` has no special handling for restic or rclone jobs. Restore start and completion are persisted, and failed restore attempts are also recorded for later troubleshooting.
- The v2 manifest directory-topology and permission semantics apply only to local jobs. Restic and rclone continue to use their respective backend snapshot, copy, and verification semantics.
- `POST /restore-verify` requires an existing target directory, applies the same server-side POSIX path rule, protected-path boundaries, and control-character or dot-segment rejection, and does not modify data.
  It persists the latest verification report as `last_restore_verify` and reports file/byte counts plus whether key directories or files were found.
- Verification fields include `.mnemonas-restore/config.toml`, `files/`, `.mnemonas/`, `.mnemonas/index.db`, and `.mnemonas/objects`; warnings call out symlinks, special files, or targets that do not look like a complete `storage.root`.
- For local jobs, it first uses the trusted v2 manifest bound to the latest successful restore for the same target, otherwise the manifest bound to the latest successful local backup, and returns the comparison `snapshot_path` and `manifest_path`. File and directory comparisons treat the manifest as authoritative; a missing directory, extra directory, or POSIX `rwx` permission drift produces a warning.
- Restore records retain internal manifest size and raw-byte digest evidence so `restore-verify` can bind the trusted baseline to the same restore; changing only JSON whitespace or field order invalidates that binding. This evidence is not part of the public `RestoreResult` response fields, and clients must not depend on or submit it. When an older restore record lacks internal evidence, the current trusted v2 backup can first be restored again; a new local backup is required first only when the current backup is also v1 or lacks trusted run evidence. When the matching restore required config restoration, a missing or mismatched `.mnemonas-restore/config.toml` or a structural directory whose mode is not exactly `0700` produces a verification warning.

Errors and boundary conditions:

- Invalid restore `target_path` values and invalid batch restore request entries return `400`.
- Backup task execution failures caused by configured paths, backup source contents, or external commands return `500` with the failed run, drill, or restore result in `details`.
- Unknown jobs return `404`; disabled jobs, an operation already running for the same job, a local target lock held by another process, local restore/restore-drill operations without any completed snapshot, and non-empty restore targets return `409`.
- Restore target paths containing backslashes are rejected as invalid Windows or UNC-style syntax for `restore-preview`, `restore`, and `restore-verify`.
- Restic preview and rclone preview or retention listings reject unsafe output file paths, including empty paths, control characters, backslashes, Windows/UNC syntax, `.`/`..` path segments, or absolute paths outside the configured source boundary.
- Backup, restore, restore-drill, read-only verification, and retention-check operations persist a `running` record before execution.
  During service startup, `running` records left by a previous process exit are marked failed and written back to the state file.
- Job views and restore reports associate `last_restore_verify` with `last_restore` only when the latest restore completed successfully, the target path matches, and the verification timestamp is not earlier than the latest restore completion time.
  Job views expose `last_matching_restore_verify` and `restore_report_findings` for the same matched verification and pending findings as restore reports.
- Job views and restore reports copy the matched result into `last_matching_restore_verify`; otherwise the field is omitted and findings state that the latest restore still needs a matching read-only verification.
  When the latest restore is still running, restore report findings state that the restore has not completed and avoid attaching older verification results to that restore.
- Local backup destinations reject existing symlink path components. Local restore previews, restores, and restore drills recheck that destination before reading snapshot manifests or creating drill artifacts.
  The same symlink path-component check applies to `POST /restore-preview`, `POST /restore`, and `POST /restore-verify` target paths.

## WebDAV

WebDAV is served at:

```text
http://localhost:8080/dav
```

WebDAV access and method semantics:

- For day-to-day or production mounts, set `webdav.auth_type = "users"` to mount with MnemoNAS user accounts and per-user `home_dir` boundaries.
  Top-level navigation entries for granted shared directories are also listed at the WebDAV root for regular users.
- Accounts with `must_change_password=true` cannot mount through `users` mode; WebDAV authentication becomes available after self-service password change.
- The root example config keeps legacy global Basic Auth as a compatibility baseline; that mode uses service credentials from `[webdav]` or generated credentials in `secrets.json`.
- Ancestor entries synthesized for nested grants are read-only navigation; writes still require a matching write grant.
- Supported core methods include `OPTIONS`, `PROPFIND`, `GET`, `HEAD`, `PUT`, `DELETE`, `MKCOL`, `MOVE`, `COPY`, simplified `PROPPATCH`, simplified `LOCK`, and simplified `UNLOCK`.
- HTTP conditional headers follow entity-tag comparison rules: `If-Match` uses strong comparison, so a weak entity-tag cannot satisfy it, while `If-None-Match` uses weak comparison. Both accept a standalone `*` or a strict comma-separated list of non-empty entity-tags. A conditional `DELETE` revalidates write access for the target tree under the storage write lock before it reads the required target attributes and evaluates the condition; failed authorization does not hash target content or delete the target.
- WebDAV `DELETE` follows the current deletion policy. In `permanent` mode, incomplete quarantine cleanup after logical commit returns `204 No Content` with `delete cleanup incomplete`. In `trash` mode, a persistence-only participant or receipt failure returns `204 No Content` with `workspace mutation persistence incomplete`, and capacity cleanup after a completed transfer uses `trash delete cleanup incomplete`. A hard durable-transfer failure returns `500 Internal Server Error`, preserves recovery evidence, and blocks later storage mutations until recovery succeeds.
- `MKCOL` returns `409 Conflict` when the direct parent directory does not exist, and returns `405 Method Not Allowed` with `Allow` when the target already exists.
- Unsupported WebDAV methods return `405 Method Not Allowed` with an `Allow` response header listing the methods available to the current scope.
  Read-only mounts and read-only users list only `OPTIONS`, `GET`, `HEAD`, and `PROPFIND`.
- For WebDAV `MOVE`, a destination path that does not exist but retains historical version metadata returns `409 Conflict`.
  Directory moves also check descendant version metadata under the destination path. This target conflict is returned before user-quota or directory-quota checks.
- Browser requests with `Origin`, `Referer`, or `Sec-Fetch-Site` metadata are same-origin checked for WebDAV write methods.
  Script and WebDAV clients normally do not send those browser-origin headers.
- WebDAV file and directory-listing responses include `nosniff` and a sandbox CSP to reduce script execution when user files are opened in the browser.

See [WebDAV compatibility](webdav-compatibility.en.md).

## Error Codes

Common error-code categories:

| Category | Examples |
| --- | --- |
| Auth | `UNAUTHORIZED`, `LOGIN_RATE_LIMITED`, `REFRESH_RATE_LIMITED`, `REFRESH_SESSION_LIMIT`, `TOKEN_EXPIRED`, `TOKEN_REVOKED`, `TOKEN_STATE_UNAVAILABLE`, `PASSWORD_CHANGE_REQUIRED`, `PASSWORD_UNCHANGED` |
| Request | `BAD_REQUEST`, `INVALID_REQUEST_BODY`, `VALIDATION_ERROR` |
| File | `NOT_FOUND`, `CONFLICT`, `FILE_TOO_LARGE` |
| Share | `SHARE_NOT_FOUND`, `SHARE_EXPIRED`, `SHARE_PASSWORD_RATE_LIMITED` |
| Service | `SERVICE_UNAVAILABLE`, `INTERNAL_ERROR` |

Use the HTTP status code for broad control flow and the JSON error code for user-facing or branch-specific handling.

## Version Notes

This document describes the current main-branch REST API. Released versions, compatibility notes, and change history are tracked by Git tags and [CHANGELOG](../CHANGELOG.en.md).
