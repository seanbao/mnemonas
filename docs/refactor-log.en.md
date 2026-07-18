# Client Refactor Log

English | [简体中文](refactor-log.md)

This document records Flutter-client refactor scope, verification evidence, and remaining work. It describes the current source tree and does not represent a usable release.

## Progress Baseline

As of 2026-07-18, engineering readiness for the complete Android, Linux, and Windows client objective is estimated at **60%**. The value uses the fixed weights below to compare later changes; it is not a release-completion percentage.

| Workstream | Weight | Current score | Current boundary |
| --- | ---: | ---: | --- |
| Cross-platform project and client structure | 12 | 10 | Flutter project, design system, and Android/Linux/Windows runners exist; native desktop validation is incomplete |
| Authentication, sessions, and context isolation | 14 | 12 | Revision/CAS, secure storage, single-use refresh-token rotation, and server/account isolation are implemented; a background credential broker is not |
| Files, search, Trash, and account workflows | 20 | 15 | Core file operations, bounded search, safe Trash workflows, and account flows are connected; version, sharing, and administration flows remain incomplete |
| Transfer integrity and recovery | 20 | 11 | Server identity conditions, the client ledger, foreground pause and resume, restart recovery, and deferred Android publication from a private payload are implemented; upload has no session, offset, idempotent commit, or result lookup |
| Android native capability and lifecycle | 16 | 8 | SAF import and export provide bounded streaming, progress, cancellation, and Activity-destruction cleanup; background execution, notification controls, lifecycle transitions, and the physical-device matrix are incomplete |
| Release engineering and platform security | 10 | 3 | Debug APK and baseline policy checks are available; independent signing, release versioning, release HTTPS policy, and upgrade validation are incomplete |
| Linux and Windows validation | 8 | 1 | Runners and shared-code boundaries remain; native build, runtime, and distribution evidence is absent |
| **Total** | **100** | **60** | **Still under development, with no usable version** |

## Completed Refactors

### Client Foundation and Interaction

- Established one Flutter project, theme, and component boundary for Android, Linux, and Windows.
- Connected server setup, sign-in, device overview, file browsing, filename search, Trash, and account workflows.
- Documentation retains only a GitHub Issues feedback entry. README and development records state that no usable version exists.

### Session and Destructive-Operation Safety

- Session records use revision/CAS and atomic clearing to prevent mixed refresh-token generations.
- Server switching, account switching, sign-out, and late responses are isolated through a context epoch.
- Delete and Trash flows freeze policy, target identity, and exact IDs; an unconfirmed request outcome is not inferred as success.

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

### Upload Failure Semantics

- The legacy `POST /api/v1/files/{path}` endpoint explicitly rejects `Content-Range`, preventing callers from treating it as a resumable protocol.
- Once an upload enters the send phase, connection loss, timeout, or cancellation conservatively produces an unconfirmed-result state. The client does not automatically replay a request that may already have committed.

## Remaining Work

In current priority order:

1. Implement durable server upload sessions: create, status lookup, sequential chunks, durable offset, chunk idempotency, atomic commit, cancellation, expiry cleanup, and terminal-state reconciliation.
2. Connect upload to the same durable task state machine and retain either the Android SAF source URI or a safe app-private payload.
3. Implement the Android native background executor, progress notifications, cancel and retry actions, system stop-reason handling, and cross-process task leases.
4. Tighten Android release cleartext policy and add independent signing, monotonic versioning, signature verification, upgrade installation, and API 24/33/34/36 validation.
5. Complete physical Android-device acceptance for large files, network loss, permission revocation, process termination, foreground/background transitions, and upgrades, including provider-retained empty or partial documents after native-copy failure.
6. Add no-replace atomic primitives, a durable publication journal, and directory synchronization for desktop download destinations, and persist the overwrite decision across restart.
7. Validate builds, file selection, destination publication, path replacement, and process recovery on native Linux and Windows hosts.
8. Complete version history, sharing, administration, localization, and remaining interaction-consistency checks.

## Record Locations

- This document: refactor scope, weighted progress, completed work, and remaining work.
- [Development-stage change log](release-notes.en.md): behavior changes and validation summaries for the current unreleased branch.
- [Roadmap](roadmap.en.md): later phases, next steps, and acceptance criteria.
- [Client README](../client/README.en.md): current visible capability, limitations, and feedback entry.
- [Client architecture](architecture.en.md): sessions, the transfer ledger, and Android native boundaries.
- [API reference](api-reference.en.md): download-resumption identity and upload contracts.

## Validation Rule

Each refactor slice starts with the narrowest targeted tests and then runs `make verify-changed` and `make client-check`. Android changes additionally require Kotlin compilation, a debug APK, emulator workflows, and physical-device evidence. A debug APK is a development artifact, not a distributable release.
