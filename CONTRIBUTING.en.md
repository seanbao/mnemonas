# Contributing Guide

English | [简体中文](CONTRIBUTING.md)

This document describes the MnemoNAS contribution flow, quality gates, and safety boundaries. It applies to code, scripts, documentation, deployment templates, and tests.

## Contribution Scope

Preferred contributions include:

- fixes for reproducible defects, data-boundary issues, security-boundary issues, or deployment blockers;
- improvements to existing Web UI, WebDAV, version-history, trash, backup, maintenance, and diagnostic capabilities;
- reliability improvements for systemd, Docker, reverse proxy, public access, and release paths;
- bilingual documentation, configuration examples, test coverage, and validation scripts;
- simplification of existing implementation while keeping public behavior clear.

Large refactors should be split into reviewable commits. Each commit should cover one risk area and include matching tests or documentation.

## Development Setup

Recommended starting points:

- [README](README.en.md)
- [Documentation index](docs/README.en.md)
- [Development guide](docs/development.en.md)
- [Testing strategy](docs/testing-strategy.en.md)
- [Security hardening guide](docs/security.en.md)

Local development requires Go, Rust, Node.js, Docker Engine, and Compose v2. Authoritative versions are recorded in `.go-version`, `dataplane/rust-toolchain.toml`, `.nvmrc`, `web/package.json`, and the [development guide](docs/development.en.md).

## Pre-Commit Checks

Changes should run the narrowest command that proves correctness first, then broaden validation according to risk.

Common entry points:

```bash
make verify-changed
make quick-check
make check
make docs-check
make scripts-check
make security-check
```

Branch-range validation:

```bash
GOTOOLCHAIN=local ./scripts/verify-changed.sh --base master
```

Documentation or script changes usually require at least:

```bash
git diff --check
make docs-check
make scripts-check
./scripts/check-secret-leaks.sh
```

Visible frontend behavior changes should add matching Vitest or Playwright coverage. Deployment, configuration, protocol, or security-boundary changes should update tests, examples, and bilingual documentation together.

## Documentation Requirements

- Chinese and English documentation should be updated together.
- Project documentation should stay formal and objective, without promotional wording or second-person phrasing.
- Configuration, API, deployment path, and public behavior changes should update relevant README, `docs/`, example configuration, and release-notes draft entries.
- Security-sensitive examples must not include real secrets, tokens, cookies, or copyable weak passwords.

## Security Boundaries

The following areas are security-sensitive:

- path handling, archive download, WebDAV, public sharing, trash, and restore flows;
- passwords, tokens, cookies, initial credentials, webhooks, and alert secrets;
- public access, reverse proxies, firewalls, TLS, Docker ports, and systemd service permissions;
- backup, restore, scrub, GC, CAS objects, and version history.

Security vulnerabilities should not be disclosed through public Issues. Reporting instructions are documented in the [security policy](SECURITY.md).

## Commit Messages

Commit messages use Conventional Commits:

```text
<type>(<scope>): <subject>
```

Examples:

```text
fix(api): reject unsafe archive paths
docs(deploy): clarify public access checklist
build(docker): harden smoke test port binding
```

Use imperative mood, lowercase the subject after the colon, and omit the final period. Use the commit body only for non-obvious migration, risk, or design context.

## Pull Request Preparation

PR descriptions should cover:

- goal and scope;
- user-visible behavior changes;
- data, permission, security, or deployment impact;
- validation commands that ran;
- uncovered risks and follow-up work.

Large PRs should be split by risk area. Bilingual documentation changes should remain in the same commit group as the matching behavior change.
