<!-- markdownlint-disable MD032 MD060 -->

# WebDAV Client Compatibility

English | [简体中文](webdav-compatibility.md)

This document records MnemoNAS WebDAV protocol coverage and expected client compatibility. Client versions, operating-system policy, and network configuration can affect behavior, so real-client regression testing should continue around releases.

REST API resource copying is available at `/api/v1/files-copy`, but the WebDAV `Overwrite: T/F` behavior applies only to the WebDAV `COPY` method.

Some write requests may return a successful status after the visible mutation is committed while a later persistence or cleanup step fails. In that case, MnemoNAS sends an HTTP `Warning` header rather than rewriting the committed mutation as a full failure. Covered warning values include `199 MnemoNAS "workspace mutation persistence incomplete"`, `199 MnemoNAS "delete cleanup incomplete"`, and `199 MnemoNAS "trash delete cleanup incomplete"`.

Same-origin URI handling:

- `Destination` headers on `COPY` / `MOVE` and tagged URIs in lock-related `If` headers must point to the current WebDAV host.
- Absolute-path references with the WebDAV prefix, such as `/dav/path`, are also accepted.
- Bare relative references are rejected. Even references that look WebDAV-prefixed, such as `dav/path`, must be written as `/dav/path` or as a same-origin absolute URI.
- Default ports (HTTP 80 and HTTPS 443) may be omitted or written explicitly.
- Scheme-relative URIs such as `//host/dav/path` are accepted only when the host matches and both sides omit the port, or when both sides use the same explicit port.
- A single FQDN trailing dot on the host name is treated as the same host, while repeated trailing dots are rejected.
- URI paths are decoded once; control characters and `.` or `..` path segments are rejected, and backslashes are normalized as path separators before prefix and permission-boundary checks.

Authentication:

- `auth_type = "users"` accepts MnemoNAS user credentials.
- Regular users' mount root maps to their `home_dir`.
- Granted shared directories appear as top-level navigation entries at the mount root.
- Shared paths apply the matching directory access rules. Guest accounts are read-only.
- PUT/COPY/MOVE writes into `home_dir` enforce user quotas; shared-path capacity limits are handled by directory quotas.
- Ancestor entries synthesized for nested grants are read-only navigation. Writes still require a matching write grant.
- `auth_type = "basic"` remains the global service-credential compatibility mode.

Response security headers:

- File responses, directory HTML listings, and `PROPFIND` / `PROPPATCH` / `LOCK` XML responses set `X-Content-Type-Options: nosniff`.
- Responses that include user file names or paths also set a sandboxed `Content-Security-Policy` to limit script, object, and frame capabilities when a WebDAV URL is opened directly in a browser. Standard WebDAV clients generally ignore these browser security headers.

## Protocol Status

### Implemented Core Methods

| Method | Status | Notes |
| --- | --- | --- |
| `OPTIONS` | Supported | Returns `DAV: 1, 2`, `MS-Author-Via: DAV`, and the `Allow` method list; read-only mounts and read-only users list only read methods |
| `PROPFIND` | Supported | Supports `Depth: 0`, `1`, and `infinity` |
| `GET` | Supported | Supports Range, ETag, and conditional requests |
| `HEAD` | Supported | Returns file metadata |
| `PUT` | Supported | Full overwrite writes; conditional `If-Match` and `If-Unmodified-Since`; partial `Content-Range` PUT returns `400` |
| `DELETE` | Supported | Soft-deletes to trash; collections require or imply `Depth: infinity` |
| `MKCOL` | Supported | Creates directories; returns `409 Conflict` when the direct parent directory is absent, returns `405 Method Not Allowed` with `Allow` when the target already exists, and does not create intermediate directories |
| `MOVE` | Supported | Move/rename with `Overwrite: T/F`; collections require or imply `Depth: infinity`; after an overwrite is committed, backup cleanup failures return `204` with `Warning` |
| `COPY` | Supported | File and directory copy; `Overwrite: T/F`; collections support `Depth: 0` and `Depth: infinity`; recursive directory copies return success with `Warning` when only post-create persistence fails |
| `PROPPATCH` | Simplified | Parses the request and returns `207 Multi-Status` with `403 Forbidden` for property changes |
| `LOCK` | Simplified | Returns a virtual lock token; supports `Depth: 0` and `Depth: infinity`; one-hour expiry |
| `UNLOCK` | Simplified | Requires matching `Lock-Token`; expired locks are cleaned automatically |

### Unsupported Methods

Unsupported methods return `405 Method Not Allowed` with an `Allow` response header that lists the methods available to the current scope. Read-only mounts and read-only users list only `OPTIONS`, `GET`, `HEAD`, and `PROPFIND`.

| Method | Status | Notes |
| --- | --- | --- |
| `ACL` | Not supported | RFC 3744 extension |
| `SEARCH` | Not supported | RFC 5323 extension |

## Compatibility Matrix

Status meanings:

- Verified: covered by automation or real-client testing.
- Expected: should work based on standard WebDAV behavior but still needs real-client confirmation.
- Needs configuration: requires operating-system settings or has limited validation.

Current automation covers:

- `OPTIONS`, `MKCOL`, `PUT`, `PROPFIND`, `COPY`, and `MOVE`;
- conditional requests, Range/ETag, and LOCK/UNLOCK behavior;
- same-origin `Destination` parsing and lock `If` URI parsing;
- `scripts/webdav-client-smoke.sh` can run an independent curl protocol smoke against a running service, covering `OPTIONS`, `MKCOL`, `PUT`, `PROPFIND`, `GET`, `HEAD`, `COPY`, `MOVE`, and `DELETE`, including read, write, and delete checks for URL-encoded space paths;
- With `RUN_RCLONE_WEBDAV=1`, the low-level E2E runner also executes a WebDAV client smoke for upload, download, move/rename, and list operations when `rclone` is installed.

The matrix below still tracks remaining real-client validation work for desktop, mobile, and media clients.

### Linux

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| Nautilus / GNOME Files | 45+ | Expected | Uses GVfs DAV support |
| Dolphin | 23+ | Expected | Built-in WebDAV support |
| davfs2 | 1.6+ | Expected | Mounts as local directory |
| rclone | 1.60+ | Verified | Optional `RUN_RCLONE_WEBDAV=1` E2E coverage for upload, download, move/rename, and list operations |

### macOS

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| Finder | macOS 12+ | Expected | Connect with **Go** -> **Connect to Server** |
| Transmit | 5+ | Expected | Recommended for heavy transfers |
| Cyberduck | 8+ | Expected | Open-source browser |
| rclone | 1.60+ | Expected | CLI and mount support |

### Windows

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| File Explorer | Windows 10/11 | Needs configuration | Requires WebClient service; HTTP Basic Auth requires registry setting |
| WinSCP | 6+ | Expected | Recommended Windows client |
| Cyberduck | 8+ | Expected | Open-source browser |
| rclone | 1.60+ | Expected | Can mount as a drive |
| NetDrive | 3+ | Needs validation | Commercial client; behavior varies by version |

Known Windows File Explorer caveats:

- HTTPS is strongly preferred.
- Large transfers may time out.
- Third-party clients usually provide a better experience.

### iOS / iPadOS

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| Files | iOS 15+ | Expected | Native WebDAV support |
| Documents by Readdle | 8+ | Expected | Feature-rich file manager |
| FileBrowser | 14+ | Needs validation | Professional file manager |

### Android

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| Solid Explorer | 2.8+ | Expected | Recommended Android client |
| Total Commander + WebDAV plugin | - | Needs validation | Long-running file manager |
| FolderSync | 5+ | Needs validation | Sync client |
| rclone | - | Expected | Can run under Termux |

### Media Players

| Client | Platform | Status | Notes |
| --- | --- | --- | --- |
| Infuse | iOS/tvOS/macOS | Needs validation | Supports WebDAV sources |
| nPlayer | iOS/Android | Needs validation | Validate seeking and subtitle behavior |
| VLC | Cross-platform | Expected | Validate Range requests and seeking |
| Kodi | Cross-platform | Needs validation | Requires WebDAV source configuration |

## Known Limits

### Virtual Locks

MnemoNAS returns WebDAV lock tokens for client compatibility, but it is not a full collaborative locking system.

- Locks support `Depth: 0` and `Depth: infinity`.
- Missing `Depth` is treated as `infinity`.
- Locking non-existing resources returns `404 Not Found`.
- Refresh requests require an empty body and a matching lock token.
- `UNLOCK` requires the `Lock-Token` header.
- Expiry is currently one hour.
- Locks are not persisted across processes.

Office-style applications may still report conflicts if multiple clients edit the same file.

### Large Uploads

- Default write timeout is configurable.
- Files larger than 10GB are best handled with rclone or another robust client.
- Reverse proxies must allow large request bodies and long upload timeouts.

### Deep Directories

`PROPFIND Depth: infinity` can be slow on very large trees. Clients should prefer `Depth: 1` browsing.

## Performance Notes

- PROPFIND responses may be cached briefly.
- Range requests support resume and media seeking.
- ETag support helps clients avoid unnecessary downloads.
- Deduplicated content can reuse existing CAS objects, but clients still need to send the upload request.

## Configuration Examples

### rclone Example

```ini
[mnemonas]
type = webdav
url = http://localhost:8080/dav
vendor = other
user = <mnemonas-or-webdav-username>
pass = <obscured-mnemonas-or-webdav-password>
```

Generate `pass` with:

```bash
rclone obscure <mnemonas-or-webdav-password>
```

### curl Protocol Smoke

```bash
WEBDAV_URL=http://localhost:8080/dav \
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
./scripts/webdav-client-smoke.sh
```

The script creates a temporary collection, verifies basic read, write, URL-encoded space path, copy, move, and delete operations, and removes the temporary data. It is a protocol-level regression check and does not replace real-client validation for Finder, Windows File Explorer, mobile file managers, or media players.

### davfs2 Example

```text
# /etc/davfs2/secrets
http://localhost:8080/dav <mnemonas-or-webdav-username> <mnemonas-or-webdav-password>
```

```bash
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
```

## Reporting Compatibility Problems

Client compatibility reports should include:

- Client name and version.
- Operating system and version.
- Reproduction steps.
- Diagnostic bundle exported from the Web UI when possible.
