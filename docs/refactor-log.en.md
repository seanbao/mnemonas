# Client Refactor Log

English | [简体中文](refactor-log.md)

This document records Flutter-client refactor scope, verification evidence, and remaining work. It describes the current source tree and does not represent a usable release.

## Progress Baseline

As of 2026-07-19, engineering readiness for the complete Android, Linux, and Windows client objective is estimated at **74%**. The value uses the fixed weights below to compare later changes; it is not a release-completion percentage. Scores are rounded from the current source implementation, automated gates, and platform-validation evidence; they do not represent schedule completion, a stability service level, or the proportion of a usable release.

| Workstream | Weight | Current score | Current boundary |
| --- | ---: | ---: | --- |
| Cross-platform project and client structure | 12 | 10 | Flutter project, design system, and Android/Linux/Windows runners exist; native desktop validation is incomplete |
| Authentication, sessions, and context isolation | 14 | 12 | Revision/CAS, secure storage, single-use refresh-token rotation, and server/account isolation are implemented; a background credential broker is not |
| Files, search, Trash, and account workflows | 20 | 17 | Core file operations, bounded search, version history, safe Trash workflows, and account flows are connected; sharing and the remaining administration flows are incomplete |
| Transfer integrity and recovery | 20 | 18 | Download identity conditions and durable upload sessions are integrated with the client ledger; durable foreground tasks support pause, resume, authoritative offsets, idempotent commit, restart recovery, and confirmed task cleanup; native background execution and cross-process leases are incomplete |
| Android native capability and lifecycle | 16 | 11 | SAF import/export and safe foreground/background boundaries are implemented; an API 35 emulator and an API 36 physical device cover installation, sign-in, system-back hierarchy, session recovery after process restart, and a Debug update, while background execution, notification controls, and the full device matrix remain incomplete |
| Release engineering and platform security | 10 | 5 | Release builds have fail-closed signing validation, certificate-fingerprint constraints, and isolated Debug/Profile identities; temporary-key APK/AAB build and signature verification run in CI, while production-key custody, monotonic release versioning, release HTTPS policy, and a formal candidate remain incomplete |
| Linux and Windows validation | 8 | 1 | Runners and shared-code boundaries remain; native build, runtime, and distribution evidence is absent |
| **Total** | **100** | **74** | **Still under development, with no usable version** |

## Completed Refactors

### Client Foundation and Interaction

- Established one Flutter project, theme, and component boundary for Android, Linux, and Windows.
- Connected server setup, sign-in, device overview, file browsing, filename search, Trash, and account workflows.
- The Home and Files views distinguish initial loading, same-directory refresh, and cached data after a failed refresh. Stale directory content remains readable and is labelled explicitly instead of being presented as newly online data.
- File mutations use a controller-level single-flight lease and immediate presentation-layer debouncing. A confirmed mutation is not rewritten as a failure when the subsequent directory refresh fails, an unconfirmed result directs the operator to inspect the directory, and navigation is not pulled back to an older path by a late refresh.
- Directory responses must return a canonical path matching the requested path. An untrusted path is rejected as an invalid response before it reaches Widget construction. System back on the Files page handles selection cancellation, parent-directory navigation, and then root-route exit in that order.
- Extracted transfer records into a dedicated transfer center with state-based grouping and distinct pause, retry, destination-selection, cancel-and-delete, and history-clear actions. Destructive cleanup and unconfirmed-result removal require explicit confirmation.
- Entering the background synchronously claims active transfer-center tasks. Durable downloads, uploads, and Android document exports retain app-private payloads and resumable state; transient file previews, historical-version previews or downloads, and SAF upload preparation are cancelled with partial-file cleanup. No task resumes automatically on return.
- Documentation retains only a GitHub Issues feedback entry. README and development records state that no usable version exists.

### Session and Destructive-Operation Safety

- Session records use revision/CAS and atomic clearing to prevent mixed refresh-token generations.
- Server switching, account switching, sign-out, and late responses are isolated through a context epoch.
- Delete and Trash flows freeze policy, target identity, and exact IDs; an unconfirmed request outcome is not inferred as success.

### Version History and Restore

- The version-history entry is shown only for a versioned regular file with a canonical path and concrete-read permission; the directory entry does not need to contain a content hash in advance. The version API establishes the current BLAKE3 content identity, and an identity already present on the directory entry must match. Responses strictly validate the path, continuous sequence, hash, size, timestamp, and bounded clean comment.
- The current-version card displays identity and metadata only. Historical versions can be previewed or downloaded, while current-file open and download actions remain on the Files page. Historical downloads keep Bearer authentication inside the app and validate an exact strong ETag and length. Android writes to an app-private temporary file before opening the system save location.
- Before an administrator restore, the client reloads the parent directory and history, checks the current identity established by the initial version response and the selected history entry, and reuses the file-mutation single-flight lease. The restore request is never replayed automatically. Connection loss, timeout, unreadable responses, and structured `5xx` failures all become unconfirmed results.
- If a restore is confirmed but history refresh fails, the controller does not return the old history. The open sheet blocks further restore submissions and requires the directory to be refreshed before reopening. The server restore contract does not yet expose a current-identity CAS parameter, so a TOCTOU window remains between pre-submit validation and the write request.

### Download Recovery

- Current-file downloads now expose `HEAD`, `X-MnemoNAS-Download-Identity`, and `X-MnemoNAS-If-Download-Identity`.
- The client stores tasks in an app-private generation ledger without access or refresh tokens.
- Downloads use a stable partial file and durable offset. Resume requires the same object identity, `206`, exact `Content-Range`, and total size.
- After client restart, tasks first reconcile against the partial file's actual length. Incomplete tasks become paused, complete Android private payloads await a newly selected destination, and a possibly published but unconfirmed desktop result becomes `resultUnconfirmed`.
- Android first commits network content to an app-private payload and then enters an awaiting-destination state. The save dialog therefore does not create an empty document before network transfer. The selected target uses a temporary write grant for the current flow, and the task becomes complete only after native copy succeeds. After a client restart, the private payload remains and a destination must be selected again.

### Android File Selection

- Upload selection now uses native `ACTION_OPEN_DOCUMENT`, and Dart receives only URI metadata.
- The native layer copies one file at a time with a 64 KiB buffer, calls `flush` and `fd.sync`, and cleans partial files after failure.
- Upload import and download publication use only temporary URI grants for the current foreground flow and do not retain shared grants. Recoverable state remains in the app-private payload and ledger. This boundary also avoids complete-file and multi-selection Java heap residency from the previous plugin path.

### Android Release and Device Evidence

- Release retains `com.mnemonas.app`; Debug and Profile use separate package IDs, version suffixes, and display names. Signing configuration and keystore material outside the source checkout are mandatory before a release APK/AAB is generated, with checks for a private-key alias, certificate validity, Android Debug certificates, and the certificate SHA-256 fingerprint.
- The gate rejects `android.injected.signing.*` overrides and identifies release artifact tasks from Gradle's resolved task graph, so task abbreviation or exclusion of the standalone validation task cannot skip validation. A temporary PKCS12 test key covers rejection of missing configuration, blank fields, unreadable files, key material inside the checkout, invalid aliases or credentials, certificate validity failures, Debug certificates, fingerprint mismatches, and both bypass regressions. The generated APK and AAB pass actual signature verification. The script removes the test key and release outputs, and CI runs the same gate.
- This local manual acceptance pass separately exercised connection, sign-in, selection-first back handling, `/e2e-folder-share/subdir → /e2e-folder-share → /` navigation, session recovery after force-stop, and session retention across a same-Debug-signature `versionCode 1 → 2` update on an API 35 emulator and an OPPO PLP120 running Android 16 (API 36). This record does not replace acceptance of a candidate signed with controlled production material.

#### Android Manual Validation Record for This Pass

| Environment | Installation and update | Session and back behavior | Evidence boundary |
| --- | --- | --- | --- |
| Temporary Android API 35 emulator | Same-Debug-signature `versionCode 1 → 2` update passed | Sign-in, selection cancellation, parent-directory back navigation, and session recovery after force-stop passed | The temporary AVD, test applications, and local backend were removed after validation |
| OPPO PLP120, Android 16 (API 36) | Same-Debug-signature `versionCode 1 → 2` update passed | Sign-in, selection cancellation, parent-directory back navigation, and session recovery after force-stop passed | Test applications, ADB reverse forwarding, and the local backend were removed after validation |

This table is a local manual execution summary. Test credentials, screenshots, and UI XML may contain local-environment or session information and are not stored in the repository. No remote device-farm run, controlled Release artifact, or downloadable device log exists yet. Formal candidate acceptance must retain sanitized command records, artifact digests, the signing fingerprint, and the device-matrix result.

### Upload Recovery

- The server uses owner-isolated durable upload sessions with request-ID idempotency, status lookup, sequential chunks, authoritative offsets, chunk SHA-256, complete-payload BLAKE3, conditional commit, cancellation, expiry cleanup, and crash-result reconciliation.
- Session storage uses a bounded two-snapshot state chain, retained-record limits, 20/100 GiB staged-byte limits, and host minimum-free-space admission. Overlapping commits converge on one terminal result, and server startup reconciles interrupted `committing` states.
- The legacy `POST /api/v1/files/{path}` continues to reject `Content-Range`; recoverable clients use only the separate `/api/v1/upload-sessions` protocol.
- The client creates a task-owned private payload and records its SHA-256 before persisting creation-attempt state, the session ID, server offset, and expiry in the ledger. It must persist the attempt before the first creation request. If the response is lost before a session ID is known, it only looks up an existing session by client request ID and does not repeat creation.
- If a session is missing or expired, the client marks the task as unconfirmed rather than creating a session against a new target baseline. Server recovery must also pass the storage recovery gate instead of confirming publication from a visible target that has not been reconciled. For a successful upload, it deletes the private payload only after observing `committed`; startup retries payload cleanup for confirmed completed or cancelled tasks.
- When Android file metadata contains a size, a source above 10 GiB is rejected before copying. If metadata omits the size, the native copy layer still enforces a 10 GiB hard limit and stops writing and removes the partial file when the limit is reached. Local preparation uses a separate visible state so it is not presented as network-upload progress.

## Remaining Work

In current priority order:

1. Replace the foreground JSON generation ledger with a transactional store that provides revision/CAS, durable task commands, and fencing tokens, then connect the Android native background executor, progress notifications, pause and retry actions, and system stop reasons.
2. Tighten Android release cleartext policy; establish an isolated build, read-only production-key custody, rotation, and recovery; implement monotonic release versioning; and complete candidate upgrade acceptance after independently checking the final APK/AAB digest, application identity, and certificate fingerprint. Review the protected signing-task set whenever AGP, flavors, or publication tasks change.
3. Add API 24/33/34 and broader vendor-device coverage for large files, network loss, permission revocation, and foreground/background pause and recovery, including provider-retained empty or partial documents after native-copy failure.
4. Reduce the temporary duplicate storage between Android SAF import and the task-owned private payload, and expose a separate full-payload verification state.
5. Add no-replace atomic primitives, a durable publication journal, and directory synchronization for desktop download destinations, and persist the overwrite decision across restart.
6. Validate builds, file selection, destination publication, path replacement, and process recovery on native Linux and Windows hosts.
7. Complete full-text and photo indexing, cursor-based search pagination, directory archive download, sharing, and the remaining administration flows. Replace absolute-path text input for move, copy, and conflict restore with a browsable destination-directory selector, then complete localization and the remaining interaction-consistency checks.
8. Close the version-restore submit-time race after the server exposes current-identity CAS.

## Record Locations

- This document: refactor scope, weighted progress, completed work, and remaining work.
- [Development-stage change log](release-notes.en.md): behavior changes and validation summaries for the current unreleased branch.
- [Roadmap](roadmap.en.md): later phases, next steps, and acceptance criteria.
- [Client README](../client/README.en.md): current visible capability, limitations, and feedback entry.
- [Client architecture](architecture.en.md): sessions, the transfer ledger, and Android native boundaries.
- [API reference](api-reference.en.md): download-resumption identity and upload contracts.

## Validation Rule

Each refactor slice starts with the narrowest targeted tests and then runs `make verify-changed` and `make client-check`. Android signing changes must also run `make client-android-release-signing-check`; Android behavior changes require Kotlin compilation, a Debug APK, emulator workflows, and physical-device evidence. A Debug APK is a development artifact, not a distributable release.
