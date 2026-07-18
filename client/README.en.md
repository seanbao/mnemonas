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
- folder creation, upload, download, transfer progress and cancellation, open, rename, move, copy, and deletion confirmation based on the server deletion policy;
- trash listing with per-item expiry, restore to the original or a custom path, and permanent deletion of a frozen exact ID selection; when a mutation result is unconfirmed, the client reloads Trash but does not infer restore or deletion success only because an item disappeared, and later mutations remain paused until an explicit refresh;
- client and server version display, plus a GitHub Issues feedback entry.

This list describes the implementation scope in the current source tree. It does not establish a usable client release.

## Current Gaps

- Full-text and photo indexing are not connected, and filename search does not yet support cursor pagination.
- Version history, sharing, and administrative workflows are not complete in the client.
- Background transfers, resumable transfers, and transfer recovery after process restart are not complete.
- Interface text is currently primarily Simplified Chinese; complete localization is not available.
- Linux and Windows native build and runtime validation have not been completed.
- Physical Android-device acceptance, upgrade validation, independent release signing, and formal release artifacts have not been completed.

The client permits HTTP for loopback or trusted local-network addresses and displays an unencrypted-transport warning. Public addresses require HTTPS.

## Development and Feedback

Build, test, security, signing, and device-validation requirements are documented in the [Development guide](../docs/development.en.md). The Chinese version is available in the [Chinese development guide](../docs/development.md).

Defects, usage problems, and feature suggestions may be submitted through [GitHub Issues](https://github.com/seanbao/mnemonas/issues). Reports should identify the server Git commit, client source commit, client version, device model, and operating-system version.
