#!/usr/bin/env bash
# MnemoNAS torture test matrix.
#
# This target is intentionally heavier than normal unit tests. It combines race
# detection, active fuzzing, frontend property tests, and browser runtime scans.
# Isolated fault injection is opt-in because it kills and restarts nasd.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -z "${GOCACHE:-}" ]]; then
    TORTURE_TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/mnemonas-torture.XXXXXX")"
    trap 'rm -rf -- "$TORTURE_TMP_ROOT"' EXIT
    export GOCACHE="$TORTURE_TMP_ROOT/go-build"
    mkdir -p "$GOCACHE"
fi

GO_TOOLCHAIN="${GOTOOLCHAIN:-local}"
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-30m}"
GO_TEST_PACKAGE_PARALLELISM="${GO_TEST_PACKAGE_PARALLELISM:-3}"
GO_FUZZTIME="${GO_FUZZTIME:-10s}"
RUN_GO_RACE="${RUN_GO_RACE:-1}"
RUN_GO_FUZZ="${RUN_GO_FUZZ:-1}"
RUN_FRONTEND_PROPERTY="${RUN_FRONTEND_PROPERTY:-1}"
RUN_E2E_TORTURE="${RUN_E2E_TORTURE:-1}"
RUN_LIVE_FAULTS="${RUN_LIVE_FAULTS:-0}"

DEFAULT_RACE_PACKAGES="./internal/api ./internal/auth ./internal/share ./internal/storage ./internal/versionstore ./internal/dataplane ./internal/workspace"
DEFAULT_FUZZ_TARGETS="./internal/api:FuzzValidatePath ./internal/api:FuzzPathWithinBase ./internal/config:FuzzNormalizeWebDAVPrefix"
DEFAULT_E2E_SPECS="files.spec.ts interaction-integrity.spec.ts layout-integrity.spec.ts runtime-integrity.spec.ts"

read -r -a race_packages <<< "${GO_TORTURE_PACKAGES:-$DEFAULT_RACE_PACKAGES}"
read -r -a fuzz_targets <<< "${GO_FUZZ_TARGETS:-$DEFAULT_FUZZ_TARGETS}"
read -r -a e2e_specs <<< "${WEB_TORTURE_SPECS:-$DEFAULT_E2E_SPECS}"

run() {
    printf '\n==> %s\n' "$*"
    "$@"
}

if [[ "$RUN_GO_RACE" == "1" ]]; then
    run env CGO_ENABLED=1 GOTOOLCHAIN="$GO_TOOLCHAIN" bash ./scripts/with-test-dataplane.sh go test -timeout="$GO_TEST_TIMEOUT" -p="$GO_TEST_PACKAGE_PARALLELISM" -race "${race_packages[@]}"
else
    printf '\n==> skipping Go race tests (RUN_GO_RACE=%s)\n' "$RUN_GO_RACE"
fi

if [[ "$RUN_GO_FUZZ" == "1" ]]; then
    for target in "${fuzz_targets[@]}"; do
        package="${target%%:*}"
        fuzz_name="${target#*:}"
        if [[ -z "$package" || -z "$fuzz_name" || "$package" == "$fuzz_name" ]]; then
            printf 'invalid fuzz target %q, expected package:FuzzName\n' "$target" >&2
            exit 1
        fi
        run env GOTOOLCHAIN="$GO_TOOLCHAIN" go test -run=^$ -fuzz="^${fuzz_name}$" -fuzztime="$GO_FUZZTIME" "$package"
    done
else
    printf '\n==> skipping Go fuzz tests (RUN_GO_FUZZ=%s)\n' "$RUN_GO_FUZZ"
fi

if [[ "$RUN_FRONTEND_PROPERTY" == "1" ]]; then
    run npm --prefix web run test:run -- src/lib/utils.property.test.ts
else
    printf '\n==> skipping frontend property tests (RUN_FRONTEND_PROPERTY=%s)\n' "$RUN_FRONTEND_PROPERTY"
fi

if [[ "$RUN_E2E_TORTURE" == "1" ]]; then
    run npm --prefix web run test:e2e -- "${e2e_specs[@]}"
else
    printf '\n==> skipping browser torture tests (RUN_E2E_TORTURE=%s)\n' "$RUN_E2E_TORTURE"
fi

if [[ "$RUN_LIVE_FAULTS" == "1" ]]; then
    run bash ./scripts/run-fault-injection-isolated.sh
else
    printf '\n==> skipping isolated fault injection; set RUN_LIVE_FAULTS=1 to enable\n'
fi

printf '\nTorture test matrix completed.\n'
