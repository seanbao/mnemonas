# MnemoNAS review progress

Last updated: 2026-04-30

This file records verified review progress so future work can continue from the remaining risks instead of repeating completed checks.

## Verified Areas

- Web stack is modern enough for current needs: React 19, Vite 7, Tailwind 4, HeroUI, React Query 5, React Router 7, Vitest, and Playwright.
- Desktop and mobile navigation are covered by Playwright, including mobile bottom navigation, sidebar closing behavior, and horizontal overflow checks.
- Settings UI has responsive tab and form layout fixes, including mobile-first grids and non-stretching desktop tabs.
- Docker runtime no longer depends on `apt-get` for health checks. The image builds a small `mnemonas-healthcheck` binary and uses it for Docker/Compose health checks.
- Dockerfile uses BuildKit cache mounts for Rust crates, npm packages, Go modules, and compiler caches; the Go builder defaults to `golang:1.25.9-alpine` to reduce first-build downloads.
- Docker Rust build no longer requires system `protoc`; normal dataplane builds use checked-in generated Rust protobuf code.
- `make proto` regenerates both Go protobuf files and checked-in Rust protobuf code through `tools/proto-gen`.
- CI and Makefile checks include the Rust proto generator, and dataplane clippy runs all targets with locked dependencies.
- CI pins `protoc 3.20.1` and fails if `make proto` changes checked-in Go/Rust protobuf generated files.
- Docker and Ubuntu deployment docs cover Compose v2 plugin installation, non-root UID/GID, internal dataplane ports, weak-network Docker build behavior, and Ubuntu laptop storage/network guidance.
- Docker deployment docs now include Buildx package-name differences and an apt foreign-architecture workaround for Ubuntu systems that fail on unsupported `binary-armhf` indexes.
- Docker Compose now has a preflight script that checks Docker daemon access, Compose v2, Buildx, configured host port availability, data-directory permissions, disk space, and existing Docker storage configuration before users run `docker compose up`.
- systemd deployments now install a tested `mnemonas-uninstall-systemd` helper that removes services and binaries while preserving config/data by default, with explicit confirmation required before deleting storage.
- Deployment doctor checks UFW state and warns if dataplane gRPC/HTTP ports are allowed through the firewall.
- Security docs now explicitly call out dataplane 9090/9091 firewall denies and doctor verification.
- GitHub issue templates ask for systemd doctor output, Docker preflight/Compose logs, focused validation commands, and regenerated protobuf files when relevant.
- `make security-check` now scans Go, dataplane Rust, and proto-gen Rust advisories by default while leaving frontend `npm audit` as explicit opt-in (`NPM_AUDIT=1`) because it sends the dependency tree to the configured npm registry.
- Toolchain hints are documented through `.go-version`, existing `.nvmrc`, Go `toolchain`, and Rust `rust-version` without forcing rustup network syncs.
- Web development has a focused `npm run test:e2e:navigation` script for desktop/mobile shell regression checks, documented in both Web README and testing strategy.
- Dependabot covers the main dataplane Cargo project and the `tools/proto-gen` Cargo project separately.
- Browser security headers include CSP, `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options`, and `Permissions-Policy`.
- Go protobuf files were regenerated with the CI-pinned generator versions: `protoc-gen-go v1.36.11` and `protoc-gen-go-grpc v1.6.1`.

## Recent Validation

- `PATH=/tmp/mnemonas-go-bin:$PATH GOTOOLCHAIN=local make build`
- `GOTOOLCHAIN=local make quick-check`
- `GOTOOLCHAIN=local go test ./...` excluding `web/node_modules`
- `PATH=/tmp/mnemonas-golangci:$PATH GOTOOLCHAIN=local golangci-lint run ./cmd/healthcheck ./cmd/nasd ./internal/dataplane ./proto`
- `cargo test --manifest-path dataplane/Cargo.toml --locked`
- `cargo clippy --manifest-path dataplane/Cargo.toml --all-targets -- -D warnings`
- `cargo test --manifest-path tools/proto-gen/Cargo.toml --locked`
- `cargo clippy --manifest-path tools/proto-gen/Cargo.toml -- -D warnings`
- `npm --prefix web run lint`
- `npm --prefix web run build`
- `GOTOOLCHAIN=local npm --prefix web run test:e2e:navigation`
- `GOTOOLCHAIN=local npm --prefix web run test:e2e:navigation -- --list`
- `GOTOOLCHAIN=local node ./scripts/playwright.cjs test navigation.spec.ts`
- `make scripts-check`
- `./scripts/test-systemd-uninstall.sh`
- `./scripts/test-docker-preflight.sh`
- `docker compose version`
- `docker buildx version`
- `./scripts/mnemonas-docker-preflight.sh`
- `docker compose -f docker-compose.yml --env-file .env.example config --quiet`
- `docker compose -f docker-compose.yml --env-file .env.example config` verified Compose resolves `GO_IMAGE: golang:1.25.9-alpine`
- `docker compose -f docker-compose.yml --env-file .env.example build`
- `HOME="$(mktemp -d)" MNEMONAS_HTTP_PORT=18081 docker compose -p mnemonas-smoke -f docker-compose.yml --env-file .env.example up -d --build` followed by `/health` and Web root smoke checks
- `DOCKER_BUILDKIT=1 docker build --progress=plain --build-arg VERSION=codex-check -t mnemonas:codex-check .`
- `docker run -d --name mnemonas-smoke -p 127.0.0.1:18080:8080 mnemonas:codex-check` followed by `/health` and Web root smoke checks
- `DOCKER_BUILDKIT=1 docker build --progress=plain --build-arg VERSION=codex-check-alpine -t mnemonas:codex-check-alpine .`
- `docker run -d --name mnemonas-smoke -p 127.0.0.1:18080:8080 mnemonas:codex-check-alpine` followed by `/health` and Web root smoke checks
- `docker build --build-arg GO_IMAGE=golang:1.25.9-alpine --target go-builder -t mnemonas-go-builder:alpine-check .`
- `docker run --rm --entrypoint sh mnemonas-go-builder:alpine-check -c 'test -s /etc/ssl/certs/ca-certificates.crt && ls -lh /build/nasd /build/mnemonas-healthcheck /etc/ssl/certs/ca-certificates.crt'`
- `GOTOOLCHAIN=local make docker VERSION=codex-make-check BUILD_TIME=2026-04-30T02:04:22Z`
- `docker run --rm -v "$PWD":/src -w /src -v /tmp/mnemonas-go-tools/govulncheck:/usr/local/bin/govulncheck:ro golang:1.25.9-alpine sh -c 'go version && GOTOOLCHAIN=local CGO_ENABLED=0 govulncheck ./...'`
- `GOSUMDB=sum.golang.org go version` verified the local Go toolchain can download and use Go 1.25.9 from `go.mod`'s `toolchain` directive.
- `GOSUMDB=sum.golang.org GOTOOLCHAIN=auto CGO_ENABLED=0 /tmp/mnemonas-go-tools/govulncheck ./...`
- `cargo install cargo-audit --version 0.22.1 --locked --root /tmp/mnemonas-cargo-audit`
- `PATH=/tmp/mnemonas-cargo-audit/bin:$PATH cargo audit` in `dataplane/`
- `PATH=/tmp/mnemonas-cargo-audit/bin:$PATH cargo audit` in `tools/proto-gen/`
- `PATH=/tmp/mnemonas-go-tools:/tmp/mnemonas-cargo-audit/bin:$PATH GOCACHE=/tmp/mnemonas-go-build GOTMPDIR=/tmp make security-check`
- `npm --prefix web outdated --json --registry=https://registry.npmmirror.com`
- `npm --prefix web audit --json`
- `git diff --check`

## Remaining Risks

- Default npm registry access is slow in this environment and `npm outdated` timed out after 180 seconds. A one-off query through `registry.npmmirror.com` succeeded and showed patch/minor updates plus optional major upgrades; `npm audit` is clean, so dependency refresh is a maintenance batch rather than a security blocker.
- This machine has `GOSUMDB=off` in the ambient shell, which blocks Go's toolchain download verification. Use `GOSUMDB=sum.golang.org` for release/security scans, or unset the local override.
