# MnemoNAS Agent Guide

## Communication

- Respond to maintainers in Chinese.
- Write code comments in English.
- Keep documentation wording formal and objective. Avoid marketing phrasing and second-person wording in project docs.

## Project Shape

MnemoNAS is a self-hosted NAS with a Go control plane, Rust data plane, React/Vite web UI, and Docker/systemd deployment paths.

- `cmd/nasd/`: Go server entry point.
- `internal/`: Go API, WebDAV, auth, storage, config, backup, and business logic.
- `dataplane/`: Rust CAS, CDC, scrub, and gRPC data plane.
- `proto/`: gRPC contract and generated Go code.
- `web/`: React, Vite, Tailwind, HeroUI, Playwright, and Vitest frontend.
- `scripts/`: deployment, development, E2E, benchmark, and safety-check scripts.
- `docs/`: bilingual documentation. Update Chinese and English docs together when behavior changes.

## Change Rules

- Preserve user-owned work. Do not revert unrelated local changes.
- Keep edits scoped to the requested behavior and nearby tests.
- Follow existing helpers and patterns before adding abstractions.
- When changing proto, API, config, deployment scripts, or public behavior, update generated files, docs, and tests in the same change.
- Do not introduce compatibility layers, migration scripts, or public behavior changes without explicit maintainer confirmation.
- Treat paths, archive extraction, WebDAV boundaries, sharing links, secrets, and public-server exposure as security-sensitive.

## Validation

Use the narrowest command that proves the change first, then broaden as needed.

- Diff-aware checks: `make verify-changed`
- Fast local confidence: `make quick-check`
- Full CI-style path: `make check`
- All tests: `make test`
- Go packages: `make go-packages`
- Frontend unit tests: `cd web && npm run test:run`
- Frontend lint: `cd web && npm run lint`
- Frontend build: `cd web && npm run build`
- Frontend E2E: `cd web && npm run test:e2e`
- Deployment script checks: `make scripts-check`
- Security dependency checks: `make security-check`
- Docker build: `make docker`

## Commit Messages

Use Conventional Commits:

```text
<type>(<scope>): <subject>
```

Use imperative mood, lowercase the subject after the colon, and omit the final period. Add a body only when it explains non-obvious context, migration, risk, or motivation.
