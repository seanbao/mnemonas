# MnemoNAS Repository Review

Date: 2026-05-07

This document is a living review map for a full code and documentation audit. It records the module map, high-risk call chains, initial risk queue, and follow-up deep dives.

## Scope

- Go control plane: `cmd/`, `internal/`, `proto/`.
- Rust dataplane: `dataplane/`, `tools/proto-gen/`.
- Web UI: `web/src`, `web/e2e`, frontend package scripts.
- Operations: `Dockerfile`, `docker-compose.yml`, `scripts/`, `.github/workflows`.
- Documentation: root docs, `docs/`, `web/README*`.

Generated/build/vendor outputs are excluded from review scope unless they affect tooling behavior:
`.git`, `bin`, `logs`, `web/node_modules`, `web/dist`, `web/coverage`, `web/playwright-report`, `web/test-results`, `dataplane/target`, `tools/proto-gen/target`.

## Module Map

### Runtime Processes

- `cmd/nasd`: Go control plane, HTTP server, static Web UI, REST API, WebDAV gateway, config/runtime wiring.
- `dataplane`: Rust gRPC/HTTP data plane for CAS, CDC, object listing, GC, scrub-related object operations.
- `web`: React SPA served by `nasd` or Vite in development.
- `cmd/healthcheck`: small health probe binary.

### Go Internal Packages

- `internal/api`: central REST router and handlers; auth gating, home-dir authorization, file/version/trash/share/settings/maintenance/activity/metrics endpoints.
- `internal/auth`: user store, JWT issuance/validation/revocation, login rate limiting, role middleware, browser session cookies.
- `internal/storage`: orchestration layer over native workspace files, version metadata, trash, path-change hooks, retention and GC.
- `internal/workspace`: safe native filesystem wrapper using `os.Root`, path normalization, temp-file writes, rename/copy/delete operations.
- `internal/versionstore`: SQLite metadata, version records, trash metadata, object-store abstraction backed by dataplane.
- `internal/dataplane`: Go gRPC client and timeout/retry boundary to Rust dataplane.
- `internal/webdav`: RFC 4918 WebDAV handler, PROPFIND/PUT/DELETE/MOVE/COPY/LOCK/UNLOCK, lock cache and property cache.
- `internal/share`: public share metadata, password validation, access cookies, public share file listing/download.
- `internal/favorites`: per-user/user-scope favorite paths and notes.
- `internal/activity`: append-only-ish activity log with filtering/stats.
- `internal/maintenance`: scrub run state and persisted results.
- `internal/thumbnail`: generated thumbnail cache and image processing boundary.
- `internal/config`: TOML config, defaults, validation, generated secrets.
- `internal/tls`: TLS config and self-signed cert generation.
- `internal/requestip`: trusted proxy/client IP parsing.
- `internal/alerts`, `internal/metrics`, `internal/caslayout`: alerting, metrics payloads, CAS layout helpers.

### Web UI Modules

- `web/src/api`: typed REST clients and auth/session behavior.
- `web/src/stores`: Zustand state for auth, files, clipboard, theme.
- `web/src/pages`: user-facing screens for files, trash, versions, users, settings, health, storage, activity, shares, search.
- `web/src/components`: file dialogs, previews, layout, auth guard, share UI, common UI.
- `web/src/lib`: path/user-scope helpers, preview URL builders, query retry, storage stats, batch operations.
- `web/e2e`: Playwright acceptance/runtime/layout/auth coverage.

### Docs/Ops

- `docs/*.md`: bilingual API, architecture, security, deployment, config, storage, testing and extension docs.
- `scripts/*.sh`: development, Docker/systemd install, E2E, torture, benchmark, fault-injection, reverse proxy setup.
- `.github/workflows`: CI and release automation.

## Primary Call Chains

### Startup

1. `cmd/nasd/main.go` loads config and generated secrets.
2. It validates storage/log/TLS settings, starts or connects dataplane, and creates `api.Server`.
3. `internal/api.NewServer` initializes storage, thumbnail, maintenance, activity, auth, share, favorites, alert/retention monitors.
4. `Server.setupRoutes` wires public routes, auth routes, protected `/api/v1`, share routes, WebDAV, and frontend fallback.

### Web Login And Session

1. `web/src/api/auth.ts::login` posts credentials with `X-MnemoNAS-Session-Mode: cookie`.
2. `internal/auth.Handler.HandleLogin` authenticates user, issues token pair, sets `mnemonas_access` and `mnemonas_refresh` HttpOnly cookies.
3. Web stores only a non-secret session marker/user cache, then calls `/api/v1/auth/download-session`.
4. `auth.Middleware.RequireAuth` accepts the primary access cookie and records the access token in request context.
5. `HandleCreateDownloadSession` sets scoped `mnemonas_download_access` for download/thumbnail media requests.

### API File Upload

1. Web `uploadFile` sends `XMLHttpRequest` with `withCredentials=true`.
2. `auth.Middleware.RequireAuth` validates cookie or bearer token.
3. `api.handleUploadFile` validates and authorizes path, then calls `storage.FileSystem.WriteFile`.
4. `storage` captures versions when policy says so, writes via `workspace`, updates metadata/activity/path hooks, and reports visible-mutation warnings when final durability steps fail.

### Download / Preview / Thumbnail

1. Web creates URL-only media/download requests.
2. `RequireAuth` accepts bearer, primary access cookie, or scoped `mnemonas_download_access` only for `GET/HEAD /api/v1/download` and `/api/v1/thumbnails`.
3. Download streams from workspace or versionstore; thumbnail reads source through storage and cache.

### Version / Trash / GC

1. Writes/delete/move update native workspace plus versionstore metadata.
2. Delete moves user content to trash when enabled and stores restore metadata.
3. Restore uses trash metadata plus path-change hooks for share/favorite repair.
4. GC computes referenced version hashes, then deletes unreferenced CAS chunks through dataplane.

### Public Shares

1. Authenticated users/admins create/update/delete shares via `/api/v1/shares`.
2. Public `/s/*` and `/api/v1/public/shares/*` access share metadata.
3. Password-protected shares issue scoped HttpOnly access cookies after password verification.
4. Public list/download paths must stay confined to share root.

### WebDAV

1. WebDAV request reaches switchable runtime handler mounted from config.
2. Basic auth is applied when enabled.
3. Handler maps WebDAV paths to storage operations and lock metadata.
4. Storage path-change hooks keep shares/favorites coherent after rename/delete.

## Initial Risk Queue

| ID | Area | Risk | Current Signal | Priority |
| --- | --- | --- | --- | --- |
| R1 | Path and filesystem safety | Path traversal, symlink races, unsafe `RemoveAll`/rename/copy boundaries can destroy or expose host files. | Heavy use of `os.Root`, explicit tests and validation exist; still central blast radius. | Critical |
| R2 | Auth/session/CSRF | Cookie-based Web session changes require consistent SameSite, logout cleanup, refresh behavior, and no token leakage to JS. | Web now uses HttpOnly cookies; same-origin Origin/Referer enforcement added for unsafe browser requests. | Critical |
| R3 | Public share isolation | Public share path joining, password cookies, rate limits and listing/download boundaries must not escape share root. | Dedicated share package and tests exist; needs deep path review. | Critical |
| R4 | Version/trash consistency | Cross-store operations can leave native files, SQLite metadata, share/favorite references, and CAS chunks inconsistent. | Visible warning model exists; need enumerate all mutation rollback paths. | High |
| R5 | WebDAV correctness | LOCK/MOVE/COPY/delete semantics can bypass home-dir/auth/path hooks or corrupt lock state. | Large tests exist; high protocol complexity. | High |
| R6 | Dataplane object integrity | CAS hash validation, chunk dedupe, GC and rebuild-index behavior guard data integrity. | Rust tests cover hash/path issues; inspect gRPC edge cases. | High |
| R7 | Config/secrets/deployment | Defaults, generated secrets, file permissions, TLS/proxy headers, Docker/systemd scripts can create insecure deployments. | Many tests/docs exist; shell scripts need focused audit. | High |
| R8 | Frontend auth/API ergonomics | UI can desync auth state, expose stale data, or send unsafe requests after session expiry. | Auth tests pass after cookie migration; inspect all API clients. | Medium |
| R9 | Docs drift | Bilingual docs may contradict actual defaults/routes/security model. | Recent auth docs changed; full docs need consistency pass. | Medium |
| R10 | Test/tooling boundaries | `go list ./...` includes an npm package's Go module unless using Makefile filters. | `make go-packages` excludes it; docs/scripts should consistently use Makefile. | Low |

## Deep Dive Log

### R1 Path And Filesystem Safety

Reviewed:

- `internal/api` validates every file path through `validatePath`, rejects NUL and `..`, normalizes backslashes, blocks root mutations, and applies `authorizeUserPath` for non-admin users.
- `internal/workspace` uses `os.OpenRoot`, rejects symlink roots/parents, validates workspace names, and performs temp-file write/copy flows rather than ad-hoc host paths.
- `internal/storage` keeps native workspace operations behind the workspace abstraction and models post-mutation cleanup failures as warning responses instead of silent success.
- Trash list/get/restore/delete paths re-authorize `OriginalPath`; empty-trash has a scoped branch for non-admin users.

Findings:

- No confirmed traversal or symlink-escape bug found in the reviewed path stack.
- Coverage gap: non-admin empty-trash scoping was implemented but not pinned by a route-level regression test.

Fix:

- Added `TestServer_EmptyTrash_FiltersResultsByHomeDirForNonAdmin`.

### R2 Auth, Session, And CSRF

Reviewed:

- Web login/refresh sends `X-MnemoNAS-Session-Mode: cookie`; JSON bearer tokens are omitted in cookie mode.
- `web/src/api/auth.ts` stores only `mnemonas_session` and `mnemonas_user`; legacy `mnemonas_token` and `mnemonas_refresh_token` are removed during auth flows.
- `authFetch` and upload XHR use same-origin credentials instead of `Authorization` headers.
- Server sets `mnemonas_access`, `mnemonas_refresh`, and scoped `mnemonas_download_access` as HttpOnly, SameSite=Lax cookies.
- Logout route uses `OptionalAuth`, so expired or missing access cookies still reach the handler and can be cleared.
- Download-session cookies are accepted only for `GET/HEAD /api/v1/download` and `/api/v1/thumbnails`.

Findings:

- Main Web session migration away from `localStorage` is complete in production code.
- SameSite=Lax alone leaves same-site cross-origin subdomains as a residual CSRF surface for unsafe methods.
- Logout cookie cleanup behavior existed but lacked an API-router-level regression test.

Fixes:

- Added global unsafe-method Origin/Referer host enforcement when auth is enabled. Requests with mismatched browser metadata return `403`; scripts without browser origin metadata and explicit bearer API clients remain compatible.
- Added `TestServer_RejectsCrossOriginUnsafeBrowserRequests`.
- Added `TestServer_Logout_ClearsCookiesWithoutValidAuth`.
- Updated Vite dev proxy to preserve browser `Host`, keeping local dev/E2E proxy traffic compatible with the server-side same-origin check.

### R3 Public Share Isolation

Reviewed:

- Share create/update routes validate absolute paths and the API wrapper enforces owner home-dir scope for non-admin users.
- Public list/download normalizes relative paths and checks confinement with `isWithinSharePath`.
- `ListShareItems` rejects entries that cannot be represented relative to the share root.
- Password-protected shares issue share-scoped HttpOnly cookies; failed attempts are keyed by share ID plus trusted client IP.

Findings:

- Existing tests cover traversal attempts, outside-root entries, password cookies, rate limits, forwarded-header spoofing, and stream failure access-count rollback.
- No confirmed public share escape found in this pass.

### R4 Version, Trash, And CAS Consistency

Reviewed:

- Native mutations update versionstore/trash metadata and path-change hooks for share/favorite repair.
- Restore/delete/empty-trash expose partial success and cleanup-warning response headers instead of hiding inconsistent post-commit cleanup failures.
- GC lists referenced version hashes before deleting unreferenced CAS chunks.

Findings:

- The warning model is explicit and covered in many storage/API tests.
- Remaining risk is operational: warnings require operators or UI to surface and act on them; this should stay in release criteria.

### R5 WebDAV

Reviewed:

- Basic auth is constant-time when configured; anonymous WebDAV is allowed only when config selects `none`.
- Prefix/Destination parsing rejects cross-host, prefix mismatch, encoded traversal, and directory move/copy into self.
- Read-only mode blocks mutating methods.
- LOCK/UNLOCK state, copy/move overwrite behavior, WebDAV preconditions, traversal, and Destination semantics have focused tests.

Findings:

- No confirmed WebDAV path bypass found in this pass.
- Protocol complexity remains high; real-client regression around releases is still warranted.

### R6 Dataplane Object Integrity

Reviewed:

- CAS hash inputs are validated as 64-byte hex BLAKE3 before path mapping.
- Rebuild-index skips symlinks, temp files, wrong-shard objects, and invalid hash paths.
- Put/delete use per-hash locks and parent directory syncs; put-file stream rollback deletes chunks created before failure.
- gRPC methods validate object hashes for get/has/delete/get-file/scrub/list cursor.

Findings:

- Direct gRPC `ListObjects` accepted `limit=0` and arbitrarily large limits, while the Go API already capped this at 1..1000.

Fix:

- Added dataplane-side `ListObjects` limit validation and `test_list_objects_rejects_invalid_limits`.

### R7 Config, Secrets, And Deployment

Reviewed:

- Config validation rejects unsafe no-auth and unauthenticated WebDAV beyond loopback unless explicit unsafe override is set.
- Secrets and initial password files are stored under the internal data root; docs and scripts instruct operators to read them locally rather than logging plaintext.
- Reverse-proxy behavior defaults to not trusting forwarded headers; `trusted_proxy_hops` must be explicitly set.
- Systemd/Docker scripts have safety tests and doctor/preflight checks.

Findings:

- No immediate deployment script destructive bug found from static review.
- Docs correctly warn that cookie `Secure` detection and client-IP rate limits depend on trusted proxy configuration.

### R8 Frontend Auth/API Ergonomics

Reviewed:

- Production Web code no longer sends bearer tokens from localStorage.
- Upload uses `withCredentials=true`; ordinary API clients use `authFetch` with same-origin credentials.
- Share public API uses same-origin credentials for share-cookie access.
- Vite dev proxy preserves the frontend Host so proxied API requests keep browser origin metadata aligned with backend same-origin validation.

Findings:

- `localStorage` remains intentionally used for user display cache, non-secret session marker, theme, and legacy token cleanup.
- No confirmed Web token leakage found in production code.

### R9 Docs Drift

Reviewed:

- API/security docs describe HttpOnly cookie mode, bearer API compatibility, logout cleanup, download-session cookie scope, and reverse-proxy `Secure` behavior.
- Development/testing docs warn not to use raw `go test ./...` / `go list ./...` after installing frontend dependencies.

Fix:

- Updated Chinese and English security docs for unsafe-method Origin/Referer enforcement.

### R10 Tooling Boundary

Reviewed:

- `make go-packages` filters `web/node_modules` and generated `proto` packages before Go test/lint operations.
- CI and development docs use `make --no-print-directory go-packages`.

Finding:

- Raw `go list ./...` remains unsafe after `web/node_modules` exists; use Makefile targets for repository-wide Go checks.
