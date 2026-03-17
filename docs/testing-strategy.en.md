# MnemoNAS Testing Strategy

English | [简体中文](testing-strategy.md)

This document defines the MnemoNAS testing strategy across unit, integration, end-to-end, torture, and frontend testing.

## Test Pyramid

```text
                  manual testing
                exploratory and UX
              ----------------------
                    E2E tests
              user journeys, browser
            --------------------------
                 integration tests
            component and config behavior
          ------------------------------
                    unit tests
          functions, boundaries, invariants
```

Targets:

| Layer | Goal | Frequency | Typical Time |
| --- | --- | --- | --- |
| Unit | 80%+ where practical | Every commit | < 30s |
| Integration | Critical paths | Every commit | < 2min |
| E2E | Core scenarios | Daily/release | < 10min |
| Torture matrix | High-risk paths | Manual/scheduled | 10-90min |

## Unit Tests

Use table-driven tests for configuration-heavy behavior:

```go
func TestConfigMatrix_AuthInitialization(t *testing.T) {
    cases := []struct {
        name           string
        authEnabled    bool
        expectPassFile bool
        expectUserFile bool
    }{
        {"auth enabled", true, true, true},
        {"auth disabled", false, false, false},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // use temp directories
            // verify files and behavior
        })
    }
}
```

Boundary tests should cover empty input, maximum lengths, Unicode, path traversal, invalid TOML, invalid durations, and permission failures.

Naming convention:

```text
Test<Module>_<Scenario>_<ExpectedBehavior>
```

Examples:

- `TestUserStore_DuplicateUsername_ReturnsError`
- `TestTokenManager_ExpiredToken_ReturnsErrTokenExpired`
- `TestConfig_MissingFile_UsesDefaults`

## Integration Tests

Integration tests validate multiple modules together:

- Auth store, default admin creation, login, token refresh, and initial password removal.
- Config file loading and validation.
- Workspace writes, version store, trash, and CAS behavior.
- WebDAV handler behavior over real HTTP requests.
- Settings updates that affect runtime WebDAV behavior.

Example:

```go
func TestAuthIntegration_FullLoginFlow(t *testing.T) {
    // create temporary storage root
    // initialize auth
    // read initial password
    // login
    // verify token and password-file cleanup
}
```

## E2E Tests

Default entry:

```bash
make e2e
```

Quick isolated run:

```bash
./scripts/run-e2e-isolated.sh --quick
RUN_RCLONE_WEBDAV=1 ./scripts/run-e2e-isolated.sh --quick
WEBDAV_URL=http://localhost:8080/dav ./scripts/webdav-client-smoke.sh
```

The isolated runner starts a temporary backend, temporary storage, and non-default ports before invoking `scripts/e2e-test.sh`.
The isolated root must be under `/tmp` or the current checkout and must not contain control characters, `..`, or symlink path components.
When `RUN_RCLONE_WEBDAV=1` is set, the isolated runner passes it to the low-level E2E script so an installed `rclone` runs the WebDAV client smoke, covering upload, download, move/rename, list, and cleanup.
`scripts/webdav-client-smoke.sh` targets an already running service and provides an independent curl protocol smoke, including read/write coverage for URL-encoded space paths. `WEBDAV_URL` must be an HTTP(S) WebDAV root URL without whitespace, query strings, fragments, embedded credentials, backslashes, encoded slashes, encoded backslashes, or `.`/`..` path segments. Pass `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD` when authentication is required. Each curl request uses `CURL_CONNECT_TIMEOUT=10` and `CURL_MAX_TIME=30` by default; increase these environment variables on high-latency links.
The default backend port is `18180` and default frontend port is `14173` for Playwright.
Playwright's isolated backend uses a 2-hour access-token lifetime and a 168-hour refresh-token lifetime.
This reduces shared storageState expiration risk during long parallel runs.
The isolated backend also creates public file-share, password-protected share, disabled share, and folder-share fixtures under `MNEMONAS_E2E_ROOT/backend/*-share-id.txt`.
Public-share, public-entry layout, and runtime-integrity tests use those fixtures; default isolated runs should fail when they are missing instead of silently skipping coverage.

Manual tests against an existing service must provide explicit targets:

```bash
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-e2e-target \
CONFIG_FILE=/tmp/mnemonas-e2e-config.toml \
SECRETS_FILE=/tmp/mnemonas-e2e-secrets.json \
INITIAL_PASSWORD_FILE=/tmp/mnemonas-e2e-initial-password.txt \
./scripts/e2e-test.sh
```

Manual `STORAGE_ROOT` values must be absolute paths.
They must not contain control characters, `..`, or symlink path components, and are limited to `/tmp` or the current checkout by default.
When WebDAV uses `auth_type = "users"`, manual runs also require explicit `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD`.

When a manual check needs the initial administrator password, parse the full value after the `Password:` prefix, for example:

```bash
password=$(sed -n 's/^Password:[[:space:]]*//p' "$INITIAL_PASSWORD_FILE" | head -n1)
```

Manual login payloads should be produced with a JSON encoder rather than string interpolation, for example:

```bash
login_payload=$(PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"username": "admin", "password": os.environ["PASSWORD"]}))
PY
)
```

Example first-start scenario:

```bash
test_fresh_install_auth_enabled() {
    TEST_HOME="$(mktemp -d)"
    trap 'rm -rf -- "$TEST_HOME"' EXIT
    export HOME="$TEST_HOME"
    # If [auth].users_file is customized, point INITIAL_PASSWORD_FILE at the sibling initial-password.txt.
    initial_password_file="${INITIAL_PASSWORD_FILE:-$HOME/.mnemonas/.mnemonas/initial-password.txt}"

    mkdir -p ~/.mnemonas
    cat > ~/.mnemonas/config.toml <<'TOML'
[auth]
enabled = true
TOML
    ./bin/nasd &
    sleep 2

    [ -f "$initial_password_file" ] || fail "Password file not created"

    password=$(sed -n 's/^Password:[[:space:]]*//p' "$initial_password_file" | head -n1)
    login_payload=$(PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"username": "admin", "password": os.environ["PASSWORD"]}))
PY
)
    response=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
        -H "Content-Type: application/json" \
        -d "$login_payload")

    echo "$response" | grep -q '"success":true' || fail "Login failed"

    [ ! -f "$initial_password_file" ] || fail "Password file not deleted after login"
}
```

E2E groups:

| Group | Coverage | Mode |
| --- | --- | --- |
| Basic | Health, version API, WebDAV OPTIONS | quick |
| File operations | PUT, GET, DELETE, MKCOL, COPY, MOVE | quick |
| Authentication | Login, refresh, permissions | quick |
| Conditional requests | ETag, If-None-Match, If-Match | quick |
| WebDAV locks | LOCK/UNLOCK virtual lock-token round trip | quick |
| Versions | Version history API | quick |
| Concurrency | Concurrent reads/writes | quick |
| Standalone WebDAV smoke | curl `OPTIONS`, `MKCOL`, `PUT`, `PROPFIND`, `GET`, `HEAD`, `COPY`, `MOVE`, `DELETE`, content validation after COPY/MOVE, URL-encoded space paths, and request timeout settings | manual running service |
| WebDAV client smoke | Optional rclone upload, download, move/rename, list, and cleanup operations | `RUN_RCLONE_WEBDAV=1` |
| Large files | 100MB path | full |
| Crash recovery | interrupted write/restart behavior | full |

## Advanced Tests

### Property-Based Testing

Use property tests for invariants such as path safety, password lifecycle, and frontend formatting utilities.

Go example:

```go
import "pgregory.net/rapid"

func TestProperty_PasswordFileLifecycle(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        authEnabled := rapid.Bool().Draw(t, "authEnabled")
        _ = authEnabled
        // assert lifecycle invariant
    })
}
```

Frontend property tests live in the Vitest suite and should cover pure utility functions and boundary-heavy logic.

### Fuzzing

Go fuzzing is used for path validation, WebDAV prefix normalization, and other input parsers:

```bash
go test -fuzz=FuzzPasswordValidation ./internal/auth/
```

### Contract Testing

API contracts should pin important request/response shapes between frontend and backend.
The current in-repo route contract tests live in `internal/api/routes_contract_test.go`; the following fragment illustrates the contract shape.

```yaml
interactions:
  - description: "Login with valid credentials"
    request:
      method: POST
      path: /api/v1/auth/login
      headers:
        Content-Type: application/json
        X-MnemoNAS-Session-Mode: cookie
    response:
      status: 200
      headers:
        Set-Cookie: !!regexp mnemonas_access=.*HttpOnly
      body:
        success: true
        data:
          user:
            username: "admin"
        absent:
          - data.access_token
          - data.refresh_token
```

### Mutation Testing

Mutation testing is optional and useful for high-risk modules:

```bash
go install "github.com/zimmski/go-mutesting/cmd/go-mutesting@v0.0.0-20210610104036-6d9217011a00"
go-mutesting ./internal/auth/...
```

## Torture Matrix

Run:

```bash
make test-torture
```

Default matrix:

- Go race detector on concurrency-sensitive packages.
- Go fuzz seeds for path safety and WebDAV normalization.
- Frontend property tests.
- Playwright human-interaction flows.
- Runtime error scans and layout integrity checks.

Local quick override:

```bash
GO_FUZZTIME=2s RUN_GO_RACE=0 RUN_E2E_TORTURE=0 make test-torture
```

`.github/workflows/torture.yml` provides manual and scheduled non-destructive deep testing. It keeps `RUN_LIVE_FAULTS=0`.

## Destructive Fault Injection

`scripts/run-fault-injection-isolated.sh` starts an isolated backend and runs `scripts/fault-injection-test.sh` against it.
The destructive runner validates crash recovery, concurrent write conflicts, version restore, object corruption, and metadata corruption.
It can kill `nasd` and modify internal files, so the low-level runner refuses to run without explicit target information.
When WebDAV uses `auth_type = "users"`, the low-level runner also requires explicit `MNEMONAS_WEBDAV_USERNAME` and `MNEMONAS_WEBDAV_PASSWORD`.

Isolated run:

```bash
make fault-injection
./scripts/run-fault-injection-isolated.sh
```

Explicit target run:

```bash
MNEMONAS_LIVE_FAULTS=1 \
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-fault-target \
NASD_BIN="$PWD/bin/nasd" \
FAULT_INJECTION_ASSUME_YES=1 \
RUN_CORRUPTION_TESTS=0 \
./scripts/fault-injection-test.sh
```

Safety gates:

- `scripts/run-fault-injection-isolated.sh` accepts only `/tmp` or checkout-local roots and loopback Web and dataplane addresses.
- `BASE_URL`, `STORAGE_ROOT`, and `NASD_BIN` must be explicit.
- Default allowed storage roots are `/tmp` or the current checkout.
- `STORAGE_ROOT` must be absolute and must not contain control characters, `..`, or symlink path components.
- `$HOME/.mnemonas` is rejected by default.
- Non-interactive runs require `FAULT_INJECTION_ASSUME_YES=1`.
- Real storage paths require `ALLOW_REAL_STORAGE=1`, must still be absolute, and must not point at protected system directories such as `/`, `/tmp`, or `/var`.
- `OBJECTS_DIR`, `INDEX_DB`, and optional `NASD_PID_FILE` paths that may be read or modified by the destructive checks must be under `STORAGE_ROOT`.

These gates are tested by `scripts/test-fault-injection-safety.sh` and included in `make scripts-check`.

## AI-Assisted Testing

AI can help generate:

- Edge-case tables.
- Missing assertions for existing tests.
- Invariant candidates.
- Review checklists for high-risk changes.

AI-generated tests should still be reviewed for false assumptions, fixture quality, and whether they actually fail on broken behavior.

## CI

CI should cover:

- Workflow validation through `make workflows-check`, including YAML syntax, duplicate-key checks, and actionlint validation.
- Script validation through `make scripts-check`, including shell syntax, ShellCheck, and safety-regression fixtures.
- Go protobuf generation, generated-file drift checks, `golangci-lint`, race-enabled tests, the Go coverage threshold, and `govulncheck`.
- Rust dataplane and `tools/proto-gen` formatting, Clippy, tests, dependency audits, and release builds.
- Frontend `npm audit`, lint, typecheck, production build, and coverage tests.
- Playwright E2E with isolated backend and frontend test servers.
- Docker image build plus `/health` and frontend-entry smoke checks.

`.github/workflows/ci.yml` is the authoritative CI definition. It should keep read-only repository permissions and concurrency cancellation by workflow/ref. New jobs or security-boundary changes should update this section and the matching local check target.

Release and security checks should not skip `golangci-lint`.
Go coverage is enforced by `GO_COVERAGE_MIN`, currently 75% in CI and `make coverage`.
Rust dataplane coverage is enforced by `make rust-coverage` through `cargo-llvm-cov` and `RUST_COVERAGE_MIN`, currently 70% line coverage.
Codecov statuses are informational; they are for trend reporting and PR comments, not the only blocking quality gate.

## Local Change-Aware Validation

Run `make verify-changed` before committing local changes. The target invokes `scripts/verify-changed.sh`, selects validation commands from the changed files in the worktree, staged diff, or `--base REF` range, and always runs `git diff --check` against the matching range. Worktree mode also checks untracked text files for trailing whitespace and space-before-tab indentation. The target also runs a high-confidence secret leak scan over tracked and untracked text files, covering private-key blocks and common platform token patterns. Failure output includes only the file, line number, and pattern class, not the matched content.

The selector covers these change classes:

- Go, Rust dataplane, `tools/proto-gen`, protobuf, Web UI, Playwright E2E, Docker, documentation, GitHub Actions workflow, and shell-script changes.
- Go, Rust, and Web dependency manifest or lockfile changes add dependency security checks.
- Toolchain and quality configuration changes, including `.go-version`, `.nvmrc`, `.golangci.yml`/`.golangci.yaml`, `.github/dependabot.yml`/`.github/dependabot.yaml`, `codecov.yml`/`codecov.yaml`, and `mnemonas.example.toml`.
- Docker and public-access template changes, including `.env.example`, Compose templates, and `deploy/public-access/`.

YAML configuration validation rejects syntax errors and duplicate keys within the same mapping so duplicate keys cannot be silently overwritten during local parsing.

Use `./scripts/verify-changed.sh --staged` to inspect only staged content. Use `./scripts/verify-changed.sh --base <ref>` to validate a branch range. Use `--dry-run` to review the selected commands without executing them. Docker image builds and container smoke checks are bounded by `VERIFY_CHANGED_DOCKER_TIMEOUT`, which defaults to `45m`, so external image pulls, build-environment failures, or container health-check failures cannot leave local validation hanging indefinitely. The script automatically uses `timeout` or GNU coreutils `gtimeout`.

Changed-file selection is covered by `scripts/test-verify-changed-safety.sh`.
Secret leak scanning is covered by `scripts/test-secret-leaks.sh`.
Both regression tests are included in `make scripts-check`.

## Frontend Testing

Frontend layers:

| Layer | Tools | Purpose |
| --- | --- | --- |
| Unit | Vitest | Pure utilities, stores, API helpers |
| Component | Vitest + Testing Library | Component behavior and state transitions |
| E2E | Playwright | Real browser flows |
| Visual/regression | Playwright screenshots and layout assertions | Detect layout and interaction regressions |

Commands:

```bash
cd web
npm run check:node
npm run test:run
npm run test:coverage
npm run lint
npm run typecheck
npm run build
npm run test:e2e
npm run test:e2e:ui
```

Playwright should cover desktop and mobile shells, navigation, file-page interactions, runtime console errors, and screenshot/layout checks for important views.

The default Playwright configuration starts isolated backend and frontend test servers.
The default per-test Playwright timeout is 60 seconds, and the default assertion timeout is 10 seconds; use `MNEMONAS_E2E_TEST_TIMEOUT_MS` and `MNEMONAS_E2E_EXPECT_TIMEOUT_MS` for long flows or slow local environments.
In that isolated environment, authentication setup failures are test failures so protected-page regressions are not hidden as skipped tests.

Reused environments are enabled only with `MNEMONAS_E2E_REUSE_EXISTING=1`.
Set `E2E_PASSWORD` or `E2E_PASSWORD_FILE` when the reused service requires authentication.
When `E2E_PASSWORD_FILE` is not set explicitly, the Playwright credential helper tries `~/.mnemonas/.mnemonas/initial-password.txt` and then `~/.mnemonas/initial-password.txt`.
When `E2E_PASSWORD_FILE` is set explicitly, that file is authoritative; missing or empty files do not fall back to the defaults.
Those runs allow protected-page tests to skip when credentials are unavailable by default.
Set `MNEMONAS_E2E_ALLOW_AUTH_SKIP=0` to force failures in reused environments.
Set `MNEMONAS_E2E_ALLOW_AUTH_SKIP=1` only when skipped protected-page checks are intentional.

## Test Checklist

For new features:

- Unit tests for core logic.
- Integration test for module boundaries.
- API contract or handler test when response shapes change.
- Frontend tests for UI state and failure states.
- E2E coverage for user-visible workflows.
- Documentation and config examples updated.

For config changes:

- Defaults tested.
- Invalid values rejected.
- Warnings tested for risky but deployable states.
- Docker/systemd behavior considered.

For bug fixes:

- Reproduction test added first when practical.
- Regression test fails before the fix.
- Related edge cases checked.
- E2E or integration coverage added if user-facing.

Before committing:

- Unit tests pass.
- E2E tests pass when the workflow is user-visible.
- Visual regression has no unexpected changes.
- TypeScript and ESLint checks pass for frontend changes.
- `make verify-changed` passes, or an equivalent broader validation set has run for the changed files.
- `make docs-check` passes so local documentation links, heading anchors, bilingual doc pairs, and documentation-index entries remain valid.

## References

- [Go testing](https://pkg.go.dev/testing)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Vitest](https://vitest.dev/)
- [Playwright](https://playwright.dev/)
