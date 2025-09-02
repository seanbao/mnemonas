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
```

The isolated runner starts a temporary backend, temporary storage, and non-default ports before invoking `scripts/e2e-test.sh`.

Manual tests against an existing service must provide explicit targets:

```bash
BASE_URL=http://127.0.0.1:18080 \
STORAGE_ROOT=/tmp/mnemonas-e2e-target \
CONFIG_FILE=/tmp/mnemonas-e2e-config.toml \
SECRETS_FILE=/tmp/mnemonas-e2e-secrets.json \
INITIAL_PASSWORD_FILE=/tmp/mnemonas-e2e-initial-password.txt \
./scripts/e2e-test.sh
```

E2E groups:

| Group | Coverage | Mode |
| --- | --- | --- |
| Basic | Health, version API, WebDAV OPTIONS | quick |
| File operations | PUT, GET, DELETE, MKCOL, COPY, MOVE | quick |
| Authentication | Login, refresh, permissions | quick |
| Conditional requests | ETag, If-None-Match, If-Match | quick |
| Versions | Version history API | quick |
| Concurrency | Concurrent reads/writes | quick |
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

API contracts should pin important request/response shapes between frontend and backend:

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

`scripts/fault-injection-test.sh` validates crash recovery, concurrent write conflicts, version restore, object corruption, and metadata corruption. It can kill `nasd` and modify internal files, so it refuses to run by default.

Isolated run:

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

- `BASE_URL`, `STORAGE_ROOT`, and `NASD_BIN` must be explicit.
- Default allowed storage roots are `/tmp` or the current checkout.
- `$HOME/.mnemonas` is rejected by default.
- Non-interactive runs require `FAULT_INJECTION_ASSUME_YES=1`.
- Real storage paths require `ALLOW_REAL_STORAGE=1`.

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

- Go unit and integration tests.
- Rust tests and formatting.
- Frontend unit tests, lint, typecheck, and build.
- Playwright E2E.
- Script validation.
- Workflow validation.
- Release-build checks.

Release and security checks should not skip `golangci-lint`.

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
npm run build
npm run test:e2e
npm run test:e2e:ui
```

Playwright should cover desktop and mobile shells, navigation, file-page interactions, runtime console errors, and screenshot/layout checks for important views.

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

## References

- [Go testing](https://pkg.go.dev/testing)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Vitest](https://vitest.dev/)
- [Playwright](https://playwright.dev/)
