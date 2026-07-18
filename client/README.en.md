# MnemoNAS Flutter Client

English | [简体中文](README.md)

This directory contains the Flutter client for Android, Linux, and Windows. Android is the first usable-platform target. The Linux and Windows runners currently preserve a shared cross-platform project boundary; builds and release validation on their native hosts have not been completed.

> [!WARNING]
> The client is still under development. No usable client version or distributable installation package has been published. Current source and build artifacts are for development and validation only.

## Current Development Scope

The current source tree implements:

- server-address entry, service validation, and server switching;
- sign-in, secure session storage, access-token refresh, required password changes, voluntary password changes, and sign-out; session updates use revision/CAS, and sign-out, server switching, or a new sign-in invalidates older requests;
- device overview and file-directory browsing;
- filename search across files and directories visible to the current account; each request displays at most 100 results, a new query cancels the older request, and the target directory is reloaded before a result is opened;
- folder creation, upload, download, open, rename, move, copy, and deletion confirmation based on the server deletion policy;
- version-history listing for versioned regular files with concrete-read permission; the version API establishes the current BLAKE3 content identity for the canonical path and returns a strictly validated sequence, the current-version card displays identity and metadata only, historical versions can be previewed or downloaded, and current-file open and download actions remain on the Files page; before an administrator restore, the client rechecks the directory, current content identity, and selected history entry, never automatically replays the restore request, and does not suggest another submission when the result is unconfirmed;
- an app-private durable ledger and stable partial files for foreground downloads, with pause, resume, and client-restart recovery; resumed responses must match the server download identity, `206`, `Content-Range`, and total size;
- foreground uploads in the same durable ledger, using a task-owned private payload and a recoverable server session; the client persists a create-attempt marker before the first session request, and if the response is lost before the session ID is known, it only looks up the original session by client request ID instead of creating another target snapshot; the client writes 8 MiB chunks, resumes from the server-authoritative offset, and queries the idempotent commit result; a missing or expired original session becomes an unconfirmed result;
- a transfer center grouped into active, attention-required, and recent records; paused or attention-required tasks can be resumed, completed private Android downloads can continue to destination selection, paused or failed tasks require confirmation before cancellation removes local recovery progress, and unconfirmed-result records require the target location to be checked first; when the app enters the background, durable transfer-center tasks are persisted as paused or awaiting a newly selected destination, while transient file previews, historical-version previews or downloads, and active SAF upload preparation are cancelled with partial-file cleanup; no task resumes automatically on return;
- Android upload selection through the Storage Access Framework, returning only URI metadata to Dart and streaming one file at a time into app-private storage without complete-file Java heap residency; a known size above 10 GiB is rejected before copying, while native copying enforces the same hard limit and removes the partial file when metadata omits the size; preparation progress is shown separately, and the temporary import can be released after the task payload has been SHA-256 verified and committed to the ledger;
- trash listing with per-item expiry, restore to the original or a custom path, and permanent deletion of a frozen exact ID selection; when a mutation result is unconfirmed, the client reloads Trash but does not infer restore or deletion success only because an item disappeared, and later mutations remain paused until an explicit refresh;
- client and server version display, plus a GitHub Issues feedback entry.

This list describes the implementation scope in the current source tree. It does not establish a usable client release.

## Current Gaps

- Full-text and photo indexing are not connected, and filename search does not yet support cursor pagination.
- Sharing and the remaining administrative workflows are not complete in the client.
- The Android native background-transfer executor, notification controls, and cross-process task lease remain incomplete. Recoverable upload and download execution currently run only in the foreground Dart coordinator and pause durably when the app enters the background.
- Interface text is currently primarily Simplified Chinese; complete localization is not available.
- Linux and Windows native build and runtime validation have not been completed.
- Physical Android-device acceptance, upgrade validation, independent release signing, and formal release artifacts have not been completed.

The client permits HTTP for loopback or trusted local-network addresses and displays an unencrypted-transport warning. Public addresses require HTTPS.

## Development and Feedback

Build, test, security, signing, and device-validation requirements are documented in the [Development guide](../docs/development.en.md). The Chinese version is available in the [Chinese development guide](../docs/development.md).

Defects, usage problems, and feature suggestions may be submitted through [GitHub Issues](https://github.com/seanbao/mnemonas/issues). Reports should identify the server Git commit, client source commit, client version, device model, and operating-system version.
