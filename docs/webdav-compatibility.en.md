<!-- markdownlint-disable MD032 MD060 -->

# WebDAV Client Compatibility

English | [简体中文](webdav-compatibility.md)

This document records MnemoNAS WebDAV protocol coverage and expected client compatibility. Client versions, operating-system policy, and network configuration can affect behavior, so real-client regression testing should continue around releases.

REST API resource copying is available at `/api/v1/files-copy`, but the WebDAV `Overwrite: T/F` behavior applies only to the WebDAV `COPY` method.

Some write requests may return a successful status after the visible mutation is committed while a later persistence or cleanup step fails. In that case, MnemoNAS sends an HTTP `Warning` header rather than rewriting the committed mutation as a full failure. Covered warning values include `199 MnemoNAS "workspace mutation persistence incomplete"`, `199 MnemoNAS "delete cleanup incomplete"`, and `199 MnemoNAS "trash delete cleanup incomplete"`. For `DELETE`, `delete cleanup incomplete` applies to permanent-mode quarantine cleanup, while `trash delete cleanup incomplete` applies to capacity cleanup after a live Trash transfer has completed. If a live Trash transfer's visible mutation has completed but durable terminal journal cleanup cannot be confirmed, the response still uses `workspace mutation persistence incomplete` and activates the recovery gate; later writes may fail until recovery succeeds. Other hard journal, participant, receipt, outbox, source, or destination failures in a live Trash transfer return `500 Internal Server Error`, preserve recovery evidence, and likewise block later storage mutations.

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
| `PUT` | Supported | Full overwrite writes; the direct parent must exist and intermediate collections are not created implicitly; conditional `If-Match`, `If-None-Match`, and `If-Unmodified-Since`; partial `Content-Range` PUT returns `400` |
| `DELETE` | Supported | Uses the current deletion policy, either Trash or permanent; collections require or imply `Depth: infinity` |
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
- `scripts/webdav-client-smoke.sh` can run an independent curl protocol smoke against a running service, covering `OPTIONS`, `MKCOL`, `PUT`, `PROPFIND`, `GET`, `HEAD`, `COPY`, `MOVE`, and `DELETE`, including content validation after COPY/MOVE and read, write, and delete checks for URL-encoded space paths;
- With `RUN_RCLONE_WEBDAV=1`, the low-level E2E runner also executes a WebDAV client smoke for upload, download, move/rename, list, and cleanup operations when `rclone` is installed.

The matrix below still tracks remaining real-client validation work for desktop, mobile, and media clients.

### Linux

| Client | Version | Status | Notes |
| --- | --- | --- | --- |
| Nautilus / GNOME Files | 45+ | Expected | Uses GVfs DAV support |
| Dolphin | 23+ | Expected | Built-in WebDAV support |
| davfs2 | 1.6+ | Expected | Mounts as local directory |
| rclone | 1.60+ | Verified | Optional `RUN_RCLONE_WEBDAV=1` E2E coverage for upload, download, move/rename, list, and cleanup operations |

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

## Real-Client Validation Standard

Before changing a matrix client from Expected or Needs validation to Verified, keep reviewable validation evidence. Evidence may come from local test logs, Issues, release validation notes, or maintainer test notes.

Minimum validation flow:

1. Run the curl protocol smoke first: `scripts/webdav-client-smoke.sh`. Environments with `rclone` installed should also set `RUN_RCLONE_WEBDAV=1` for the optional E2E path.
2. Record Client name and version, Operating system and version, WebDAV authentication mode, URL prefix, reverse proxy, TLS, and network location.
3. Verify connect or mount, browse directories, upload, download, rename, and delete operations, plus persistent visibility after reconnecting.
4. For large-file transfer, media seeking, offline sync, or background-sync clients, record whether the relevant operation passed or remained limited.
5. When the result comes from an external user, prefer the WebDAV compatibility report form and include reproduction steps and a diagnostic bundle.

## Known Limits

### Virtual Locks

MnemoNAS returns WebDAV lock tokens for client compatibility, but it is not a full collaborative locking system.

- Locks support `Depth: 0` and `Depth: infinity`.
- Missing `Depth` is treated as `infinity`.
- Locking non-existing resources returns `404 Not Found`.
- Refresh requests require an empty body and a matching lock token.
- `UNLOCK` requires the `Lock-Token` header.
- Expiry is currently one hour.
- Runtime WebDAV configuration rebuilds preserve unexpired DAV lock tokens, storage paths, depths, and expiry timestamps. The replacement handler is published immediately, so later requests use the updated prefix, authentication, access rules, quotas, and read-only setting. In-flight requests finish on their retired handler generation, which closes asynchronously after its active references reach zero. A closed handler instance cannot be published again; production handlers record this constraint only through a lightweight, process-unique lifecycle ID, without retaining retired passwords, access rules, or directory-property caches. All live generations share one path-lock table, DAV-lock table, lock-expiry cleanup loop, and quota coordinator. External renames and deletes invalidate directory-property caches in every live generation while mutating the shared DAV-lock table only once; delete failure runs independent lock rollbacks in reverse order.
- Locks are not persisted across processes.

Office-style applications may still report conflicts if multiple clients edit the same file.

### Large Uploads

- WebDAV PUT refreshes the connection read deadline from `server.read_timeout` before each request-body read. WebDAV responses refresh the connection write deadline from `server.write_timeout` before each write.
- PUT performs its initial permission, parent-directory, DAV-lock, conditional-header, and target-identity checks under the path lock, then releases that lock while reading the network request body. At EOF, including a final read that returns data and EOF together, the server reacquires the path lock and reauthenticates the user. Before commit, it uses the access-rule snapshot captured at admission to recheck the write permission for the current identity, role, and home directory, and also rechecks the parent directory and current DAV locks. A failed recheck discards the staged content. This boundary lets unrelated path operations proceed during a slow upload without allowing lock-state changes to bypass final commit checks.
- Web/API and WebDAV share process-level quota reservations and a mutation-commit gate. A known-length PUT reserves only the overwrite delta. An unknown-length PUT grows its reservation in batches of at most 64 MiB instead of claiming all remaining capacity in the scope. Each batch recomputes current usage and other outstanding reservations. An overwriting MOVE also reserves the old destination backup bytes that remain in the logical directory during commit. PUT acquires the commit gate after the request body reaches EOF and holds it until the storage transaction returns. Success, failure, condition conflict, or cancellation releases the reservation before releasing the commit gate. The admission-time quota-rule snapshot applies to that in-flight request; hot updates apply to later requests.
- PUT returns `409 Conflict` before reading the request body when the direct parent is absent or is not a directory; clients must create collections with `MKCOL` first. Exhausted concurrent write-staging slots return `503 Service Unavailable` with `Retry-After: 1`. Insufficient host capacity to stage the transaction safely returns `507 Insufficient Storage` without `Retry-After`. A target inside a nested mount below `files/`, an unverifiable mount table, or unsupported cross-root rename semantics makes PUT return `503 Service Unavailable` with an explicit atomic-write layout error. A layout error detected before request-body reading neither consumes the body nor modifies the target. The server evaluates write preconditions before reading the request body and binds the observed target deletion identity to final publication. Publication atomically exchanges an existing target and uses no-replace publication for a new target. If the target changes while the body is being read, or a new target appears before publication, a request with `If-Match`, `If-None-Match`, or `If-Unmodified-Since` returns `412 Precondition Failed`; an unconditional request returns `409 Conflict`, without overwriting or removing the newer target. The server persists a `prepared` decision before visible publication, and only a `committed` decision rolls forward; all other decisions roll back. If the journal or transaction-participant outcome cannot be confirmed, recovery evidence remains across restarts, the write gate is activated, and PUT returns `503 Service Unavailable`.
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

The script creates a temporary collection, verifies basic read, write, URL-encoded space path, copy, move, post-move content consistency, and delete operations, and removes the temporary data. `WEBDAV_URL` must be an HTTP(S) WebDAV root URL without whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or `.`/`..` path segments; pass credentials through environment variables. Each curl request uses `CURL_CONNECT_TIMEOUT=10` and `CURL_MAX_TIME=30` by default; increase these environment variables on high-latency links. It is a protocol-level regression check and does not replace real-client validation for Finder, Windows File Explorer, mobile file managers, or media players.

### davfs2 Example

```text
# /etc/davfs2/secrets
http://localhost:8080/dav <mnemonas-or-webdav-username> <mnemonas-or-webdav-password>
```

```bash
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
```

## Reporting Compatibility Problems

Use the [WebDAV compatibility report form](../.github/ISSUE_TEMPLATE/webdav_compatibility.yml) to submit client compatibility results. Reports should include:

- Client name and version.
- Operating system and version.
- WebDAV authentication mode, access path, reverse proxy, and TLS context.
- Tested operations, such as connect, browse, upload, download, rename, delete, large-file transfer, media seeking, or offline sync.
- Reproduction steps.
- Diagnostic bundle exported from the Web UI when possible.
