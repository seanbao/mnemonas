.PHONY: all build web-build test test-torture fault-injection fault-injection-live clean deps dev proto proto-go proto-rust go-packages fmt lint workflows-check scripts-check toolchains-check docs-check security-check install-audit-tools docker docker-smoke docker-check e2e bench coverage rust-coverage check verify-changed release-readiness quick-check client-deps client-toolchain-check client-android-policy-check client-android-release-signing-check client-format client-format-check client-analyze client-test client-apk-debug client-check run help

# Version metadata
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)
GO_PACKAGES ?=
GO_PACKAGE_PATTERN ?= ./...
GO_PACKAGE_EXCLUDE_PATTERN ?= /web/node_modules/|/proto$$
GO_CMD_ENV ?= GOTOOLCHAIN=local
GO_LIST_ENV ?= $(GO_CMD_ENV)
GO_LINT_ENV ?= $(GO_CMD_ENV)
GO_LINT_PACKAGES ?=
GO_TEST_TIMEOUT ?= 20m
GO_TEST_PACKAGE_PARALLELISM ?= 3
GO_TEST_FLAGS ?= -timeout=$(GO_TEST_TIMEOUT) -p=$(GO_TEST_PACKAGE_PARALLELISM)
GO_RACE_TEST_FLAGS ?= $(GO_TEST_FLAGS) -race
GOLANGCI_LINT ?= golangci-lint
SKIP_GOLANGCI_LINT ?= 0
GOVULNCHECK_VERSION ?= v1.3.0
CARGO_AUDIT_VERSION ?= 0.22.1
ACTIONLINT_VERSION ?= v1.7.7
ACTIONLINT_CMD ?= actionlint
ACTIONLINT_ENV ?= GOSUMDB=sum.golang.org GOTOOLCHAIN=auto
GO_SECURITY_ENV ?= GOSUMDB=sum.golang.org GOTOOLCHAIN=auto
GO_COVERAGE_ENV ?= GOSUMDB=sum.golang.org GOTOOLCHAIN=auto
FLUTTER ?= flutter
DART ?= dart
JAVA ?= java
JAVAC ?= javac
CLIENT_JAVA_HOME ?= $(shell \
	javac_bin="$$(command -v "$(JAVAC)" 2>/dev/null || true)"; \
	if [ -n "$$javac_bin" ]; then \
		python3 -c 'import os, sys; print(os.path.dirname(os.path.dirname(os.path.realpath(sys.argv[1]))))' "$$javac_bin"; \
	fi)
FLUTTER_VERSION ?= 3.44.4
GO_COVERAGE_MIN ?= 75
RUST_COVERAGE_MIN ?= 70
NPM_AUDIT ?= 0
NPM_AUDIT_LEVEL ?= moderate
GO_FUZZTIME ?= 10s
GO_FUZZ_TARGETS ?= ./internal/api:FuzzValidatePath ./internal/api:FuzzPathWithinBase ./internal/config:FuzzNormalizeWebDAVPrefix
GO_TORTURE_PACKAGES ?= ./internal/api ./internal/auth ./internal/share ./internal/storage ./internal/versionstore ./internal/dataplane ./internal/workspace
WEB_TORTURE_SPECS ?= files.spec.ts interaction-integrity.spec.ts layout-integrity.spec.ts runtime-integrity.spec.ts
DEPLOYMENT_SCRIPTS := scripts/install-systemd.sh scripts/uninstall-systemd.sh scripts/mnemonas-doctor.sh scripts/mnemonas-docker-preflight.sh scripts/docker-quickstart.sh scripts/docker-smoke.sh scripts/mnemonas-dataplane-start.sh scripts/verify-release-artifacts.sh scripts/verify-published-release.sh scripts/release-go-live-check.sh scripts/release-readiness.sh scripts/check-release-tag.sh scripts/release-version.sh scripts/test-systemd-install.sh scripts/test-systemd-uninstall.sh scripts/test-docker-start.sh scripts/test-docker-preflight.sh scripts/test-docker-quickstart.sh scripts/test-docker-smoke.sh scripts/test-fault-injection-safety.sh scripts/test-e2e-safety.sh scripts/test-benchmark-safety.sh scripts/test-dataplane-start.sh scripts/test-dev-safety.sh scripts/test-reverse-proxy-safety.sh scripts/test-public-access-templates.sh scripts/test-release-package.sh scripts/test-release-artifacts.sh scripts/test-published-release.sh scripts/test-release-go-live-check.sh scripts/test-release-readiness.sh scripts/test-release-tag.sh scripts/test-with-test-dataplane-safety.sh scripts/test-webdav-client-smoke.sh scripts/test-backup-restore-drill-smoke.sh scripts/docker-start.sh scripts/setup-reverse-proxy.sh scripts/dev.sh scripts/benchmark.sh
ACCEPTANCE_SCRIPTS := scripts/e2e-test.sh scripts/fault-injection-test.sh scripts/torture-test.sh scripts/run-e2e-isolated.sh scripts/run-benchmark-isolated.sh scripts/run-fault-injection-isolated.sh scripts/public-go-live-smoke.sh scripts/webdav-client-smoke.sh scripts/backup-restore-drill-smoke.sh scripts/with-test-dataplane.sh
DEV_SCRIPTS := scripts/verify-changed.sh scripts/check-commit-message.sh scripts/check-doc-links.sh scripts/check-webdav-compatibility-docs.sh scripts/check-yaml-configs.sh scripts/check-untracked-whitespace.sh scripts/check-toolchain-versions.sh scripts/check-secret-leaks.sh scripts/plan-hardening-commits.sh scripts/test-commit-message.sh scripts/test-doc-links.sh scripts/test-hardening-commit-plan.sh scripts/test-public-go-live-smoke.sh scripts/test-secret-leaks.sh scripts/test-webdav-compatibility-docs.sh scripts/test-web-husky-safety.sh scripts/test-verify-changed-safety.sh
WEB_SCRIPTS := web/scripts/start-e2e-backend.sh
HUSKY_SCRIPTS := web/.husky/pre-commit

export GO_FUZZTIME
export GO_FUZZ_TARGETS
export GO_TORTURE_PACKAGES
export WEB_TORTURE_SPECS

define RESOLVE_GO_PACKAGES
packages="$(GO_PACKAGES)"; \
go_list_env="$${GO_LIST_ENV:-$(GO_LIST_ENV)}"; \
if [ -z "$$packages" ]; then \
	packages="$$(env $$go_list_env go list $(GO_PACKAGE_PATTERN) | grep -Ev '$(GO_PACKAGE_EXCLUDE_PATTERN)')"; \
fi; \
if [ -z "$$packages" ]; then \
	echo "❌ no Go packages resolved" >&2; \
	exit 1; \
fi
endef

# Default target
all: build

# Show help
help:
	@echo "MnemoNAS Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build      - Build Web UI and binaries (proto → Web → Go → Rust)"
	@echo "  dev        - Quick development build (debug mode)"
	@echo "  test       - Run all tests"
	@echo "  test-torture - Run race/fuzz/property/browser torture tests"
	@echo "  coverage   - Run tests with coverage report"
	@echo "  verify-changed - Run checks selected from changed files"
	@echo "  release-readiness - Summarize pre-release readiness"
	@echo "  quick-check - Run fast local Go/Rust checks"
	@echo "  client-check - Run Flutter client format, analyze, test, and debug APK gates"
	@echo "  client-android-policy-check - Validate Android backup, identity, and release policy"
	@echo "  client-android-release-signing-check - Exercise fail-closed signing with temporary keys"
	@echo "  client-format - Format Flutter client Dart sources"
	@echo "  client-apk-debug - Build the Android client debug APK"
	@echo "  lint       - Run linters (Go + Rust)"
	@echo "  scripts-check - Validate deployment shell scripts and Web tool scripts"
	@echo "  toolchains-check - Validate pinned toolchain versions"
	@echo "  docs-check - Validate local documentation links and structured examples"
	@echo "  security-check - Run dependency vulnerability checks"
	@echo "  install-audit-tools - Install pinned security scan tools"
	@echo "  rust-coverage - Run Rust coverage with cargo-llvm-cov"
	@echo "  fmt        - Format code (Go + Rust)"
	@echo "  workflows-check - Validate GitHub Actions workflows"
	@echo "  e2e        - Run isolated E2E acceptance tests"
	@echo "  fault-injection - Run destructive fault-injection tests in an isolated backend"
	@echo "  bench      - Run isolated performance benchmarks"
	@echo "  run        - Build and run the control plane"
	@echo "  proto      - Generate protobuf code"
	@echo "  go-packages - Print resolved Go package list"
	@echo "  docker     - Build Docker image"
	@echo "  docker-smoke - Smoke test a built Docker image"
	@echo "  docker-check - Build and smoke test Docker image"
	@echo "  clean      - Remove build artifacts"
	@echo "  deps       - Download dependencies"
	@echo "  help       - Show this help"

# Install dependencies
deps:
	@echo "📦 Installing dependencies..."
	cd dataplane && cargo fetch --locked
	cargo fetch --manifest-path tools/proto-gen/Cargo.toml --locked
	$(GO_CMD_ENV) go mod download
	cd web && npm ci

# Generate protobuf code. Rust generated code is committed so normal dataplane/Docker builds do not require protoc.
proto: proto-go proto-rust

proto-go:
	@echo "🔧 Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/dataplane.proto

proto-rust:
	@echo "🦀 Generating Rust protobuf code..."
	CARGO_TARGET_DIR=dataplane/target/proto-gen cargo run --manifest-path tools/proto-gen/Cargo.toml --locked

go-packages:
	@$(RESOLVE_GO_PACKAGES); \
	printf '%s\n' $$packages

# Build
build: proto web-build
	@echo "🏗️  Building Go control plane..."
	@mkdir -p bin
	$(GO_CMD_ENV) CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/nasd ./cmd/nasd
	@echo "🦀 Building Rust data plane..."
	cd dataplane && cargo build --release --locked
	cp dataplane/target/release/dataplane bin/dataplane
	@echo "✅ Build complete: bin/nasd, bin/dataplane, web/dist/"

web-build:
	@echo "🌐 Building Web UI..."
	cd web && npm run build

# Development build
dev:
	@echo "🔨 Development build..."
	@mkdir -p bin
	$(GO_CMD_ENV) CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd
	cd dataplane && cargo build
	cp dataplane/target/debug/dataplane bin/dataplane-debug
	@echo "✅ Dev build complete"

# Run tests
test:
	@echo "🧪 Running Go tests..."
	@$(RESOLVE_GO_PACKAGES); \
	$(GO_CMD_ENV) CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test $(GO_RACE_TEST_FLAGS) -v $$packages
	@echo "🦀 Running Rust tests..."
	cd dataplane && cargo test --locked
	cargo test --manifest-path tools/proto-gen/Cargo.toml --locked
	@echo "🌐 Running frontend tests..."
	cd web && npm run test:run

# Torture matrix: race, fuzz, property, and browser runtime integrity scans.
# Isolated fault injection is skipped by default; pass RUN_LIVE_FAULTS=1 explicitly to run it.
test-torture:
	@echo "🔥 Running torture test matrix..."
	@chmod +x scripts/torture-test.sh
	./scripts/torture-test.sh

# Test coverage
coverage:
	@echo "📊 Generating coverage reports..."
	@mkdir -p coverage
	@GO_LIST_ENV="$(GO_COVERAGE_ENV)"; \
	$(RESOLVE_GO_PACKAGES); \
	CGO_ENABLED=0 $(GO_COVERAGE_ENV) bash ./scripts/with-test-dataplane.sh go test $(GO_TEST_FLAGS) -coverprofile=coverage/go.out $$packages
	@coverage="$$($(GO_COVERAGE_ENV) go tool cover -func=coverage/go.out | awk '/^total:/ { gsub("%", "", $$3); print $$3 }')"; \
	awk -v coverage="$$coverage" -v min="$(GO_COVERAGE_MIN)" 'BEGIN { \
		if ((coverage + 0) < (min + 0)) { \
			printf "❌ Go coverage %.1f%% is below %.1f%%\n", coverage, min; \
			exit 1; \
		} \
		printf "✅ Go coverage %.1f%% meets %.1f%%\n", coverage, min; \
	}'
	$(GO_COVERAGE_ENV) go tool cover -html=coverage/go.out -o coverage/go.html
	cd web && npm run test:coverage
	@echo "✅ Coverage reports: coverage/go.html, web/coverage/"

rust-coverage:
	@echo "🦀 Running Rust coverage..."
	@if ! command -v cargo-llvm-cov >/dev/null 2>&1; then \
		echo "❌ cargo-llvm-cov is required. Install with: cargo install cargo-llvm-cov --locked"; \
		exit 1; \
	fi
	cd dataplane && cargo llvm-cov --all-features --locked --summary-only --fail-under-lines $(RUST_COVERAGE_MIN)

# E2E tests
e2e:
	@echo "🔗 Running isolated E2E tests..."
	@chmod +x scripts/e2e-test.sh scripts/run-e2e-isolated.sh
	./scripts/run-e2e-isolated.sh

fault-injection:
	@echo "💥 Running isolated fault-injection tests..."
	@chmod +x scripts/fault-injection-test.sh scripts/run-fault-injection-isolated.sh
	./scripts/run-fault-injection-isolated.sh

fault-injection-live:
	@echo "💥 Running raw live fault-injection tests against an explicit target..."
	@chmod +x scripts/fault-injection-test.sh
	./scripts/fault-injection-test.sh

# Performance benchmarks
bench:
	@echo "⏱️  Running isolated benchmarks..."
	@chmod +x scripts/benchmark.sh scripts/run-benchmark-isolated.sh
	./scripts/run-benchmark-isolated.sh

# Run
run: build
	./bin/nasd

# Clean
clean:
	@echo "🧹 Cleaning..."
	rm -rf bin/ coverage/
	cd dataplane && cargo clean
	cargo clean --manifest-path tools/proto-gen/Cargo.toml
	cd web && rm -rf dist dist-ssr coverage test-results playwright-report .vite .vitest
	$(GO_CMD_ENV) go clean
	@echo "✅ Clean complete"

# Format code
fmt:
	@echo "✨ Formatting code..."
	@$(RESOLVE_GO_PACKAGES); \
	$(GO_CMD_ENV) go fmt $$packages
	cd dataplane && cargo fmt
	cargo fmt --manifest-path tools/proto-gen/Cargo.toml
	cd web && npm run lint -- --fix 2>/dev/null || true

# Lint
lint:
	@echo "🔍 Linting Go..."
	@lint_packages="$(GO_LINT_PACKAGES)"; \
	if [ -z "$$lint_packages" ]; then \
		lint_packages="./..."; \
	fi; \
	if [ "$(SKIP_GOLANGCI_LINT)" = "1" ]; then \
		echo "⚠️  Skipping golangci-lint because SKIP_GOLANGCI_LINT=1"; \
	elif command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1; then \
		env $(GO_LINT_ENV) "$(GOLANGCI_LINT)" run $$lint_packages; \
	else \
		echo "❌ golangci-lint not installed. Install golangci-lint, set GOLANGCI_LINT=/path/to/golangci-lint, or use SKIP_GOLANGCI_LINT=1 for a deliberate local-only skip." >&2; \
		exit 1; \
	fi
	@echo "🔍 Linting Rust..."
	cd dataplane && cargo clippy --all-targets --locked -- -D warnings
	cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings
	@echo "🔍 Linting frontend..."
	cd web && npm run lint

# GitHub Actions workflow checks
workflows-check:
	@echo "🔍 Checking GitHub Actions workflows..."
	./scripts/check-yaml-configs.sh .github/workflows/*.yml .github/workflows/*.yaml
	@if command -v "$(ACTIONLINT_CMD)" >/dev/null 2>&1; then \
		"$(ACTIONLINT_CMD)"; \
	else \
		$(ACTIONLINT_ENV) go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION); \
	fi

# Deployment and tool script checks
scripts-check:
	@echo "🔍 Checking deployment scripts..."
	bash -n $(DEPLOYMENT_SCRIPTS) $(ACCEPTANCE_SCRIPTS) $(DEV_SCRIPTS) $(WEB_SCRIPTS) $(HUSKY_SCRIPTS)
	cd web && npm run check:scripts
	./scripts/check-secret-leaks.sh
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck $(DEPLOYMENT_SCRIPTS) $(DEV_SCRIPTS) $(WEB_SCRIPTS) $(HUSKY_SCRIPTS); \
		shellcheck -e SC2155 -e SC2317 $(ACCEPTANCE_SCRIPTS); \
	else \
		echo "⚠️  shellcheck not installed, skipping"; \
	fi
	./scripts/test-systemd-install.sh
	./scripts/test-systemd-uninstall.sh
	./scripts/test-docker-start.sh
	./scripts/test-docker-preflight.sh
	./scripts/test-docker-quickstart.sh
	./scripts/test-docker-smoke.sh
	./scripts/test-benchmark-safety.sh
	./scripts/test-dataplane-start.sh
	./scripts/test-dev-safety.sh
	./scripts/test-reverse-proxy-safety.sh
	./scripts/test-public-access-templates.sh
	./scripts/test-release-package.sh
	./scripts/test-release-artifacts.sh
	./scripts/test-published-release.sh
	./scripts/test-release-go-live-check.sh
	./scripts/test-release-readiness.sh
	./scripts/test-with-test-dataplane-safety.sh
	./scripts/test-webdav-client-smoke.sh
	./scripts/test-backup-restore-drill-smoke.sh
	./scripts/test-e2e-safety.sh
	./scripts/test-fault-injection-safety.sh
	./scripts/test-commit-message.sh
	./scripts/test-doc-links.sh
	./scripts/test-public-go-live-smoke.sh
	./scripts/test-webdav-compatibility-docs.sh
	./scripts/test-hardening-commit-plan.sh
	./scripts/test-secret-leaks.sh
	./scripts/test-web-husky-safety.sh
	./scripts/test-verify-changed-safety.sh

toolchains-check:
	@echo "🔧 Checking toolchain version consistency..."
	./scripts/check-toolchain-versions.sh

docs-check:
	@echo "📚 Checking documentation links and structured examples..."
	./scripts/check-doc-links.sh
	./scripts/check-webdav-compatibility-docs.sh

# Dependency vulnerability checks
security-check:
	@echo "🔐 Scanning Go dependencies..."
	@GO_LIST_ENV="$(GO_SECURITY_ENV)"; \
	$(RESOLVE_GO_PACKAGES); \
	if command -v govulncheck >/dev/null 2>&1; then \
		$(GO_SECURITY_ENV) govulncheck $$packages; \
	else \
		$(GO_SECURITY_ENV) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $$packages; \
	fi
	@echo "🔐 Scanning Rust dependencies..."
	@if command -v cargo-audit >/dev/null 2>&1; then \
		cd dataplane && cargo audit; \
		cd ../tools/proto-gen && cargo audit; \
	else \
		echo "❌ cargo-audit not installed. Run: make install-audit-tools" >&2; \
		exit 1; \
	fi
	@if [ "$(NPM_AUDIT)" = "1" ]; then \
		echo "🔐 Scanning frontend dependencies..."; \
		cd web && npm audit --audit-level="$(NPM_AUDIT_LEVEL)"; \
	else \
		echo "⚠️  Skipping npm audit by default because it sends the dependency tree to the configured npm registry. Run: make security-check NPM_AUDIT=1 NPM_AUDIT_LEVEL=$(NPM_AUDIT_LEVEL)"; \
	fi

install-audit-tools:
	@echo "🔧 Installing pinned security scan tools..."
	$(GO_SECURITY_ENV) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	CARGO_REGISTRIES_CRATES_IO_PROTOCOL=sparse cargo install cargo-audit --version $(CARGO_AUDIT_VERSION) --locked

# Docker build
docker:
	@echo "🐳 Building Docker image..."
	DOCKER_BUILDKIT=1 docker build --build-arg VERSION=$(VERSION) --build-arg BUILD_TIME=$(BUILD_TIME) -t mnemonas:$(VERSION) -t mnemonas:latest .
	@echo "✅ Docker image: mnemonas:$(VERSION)"

docker-smoke:
	@echo "🚦 Smoke testing Docker image..."
	./scripts/docker-smoke.sh "$${MNEMONAS_DOCKER_SMOKE_IMAGE:-mnemonas:latest}"

docker-check: docker docker-smoke

# Run all checks (used by CI)
check: workflows-check scripts-check toolchains-check docs-check lint test
	@echo "✅ All checks passed"

verify-changed:
	./scripts/verify-changed.sh

release-readiness:
	./scripts/release-readiness.sh

# Fast checks (before commit)
quick-check:
	@echo "🚀 Quick check..."
	@$(RESOLVE_GO_PACKAGES); \
	$(GO_CMD_ENV) CGO_ENABLED=0 go build $$packages; \
	$(GO_CMD_ENV) CGO_ENABLED=0 bash ./scripts/with-test-dataplane.sh go test $(GO_TEST_FLAGS) -short $$packages
	cd dataplane && cargo check --locked
	cargo check --manifest-path tools/proto-gen/Cargo.toml --locked
	@echo "✅ Quick check passed"

# Flutter client checks are separate from the server/Web matrix because Android,
# Linux, and Windows runners require platform-specific SDK dependencies.
client-deps:
	@echo "📦 Installing Flutter client dependencies..."
	cd client && "$(FLUTTER)" pub get

client-toolchain-check:
	@echo "🔧 Checking Flutter client toolchain..."
	@command -v "$(JAVAC)" >/dev/null 2>&1 || { echo "❌ A full JDK 17 is required; javac was not found" >&2; exit 1; }
	@test -n "$(CLIENT_JAVA_HOME)" && test -x "$(CLIENT_JAVA_HOME)/bin/java" && test -x "$(CLIENT_JAVA_HOME)/bin/javac" || { echo "❌ Unable to derive a complete JDK from $(JAVAC)" >&2; exit 1; }
	@javac_version="$$("$(JAVAC)" -version 2>&1 | awk '{print $$2}')"; \
	case "$$javac_version" in \
		17|17.*) ;; \
		*) echo "❌ JDK 17 is required; found javac $$javac_version" >&2; exit 1 ;; \
	esac
	@java_version="$$("$(CLIENT_JAVA_HOME)/bin/java" -version 2>&1 | awk -F '\"' 'NR == 1 { print $$2 }')"; \
	case "$$java_version" in \
		17|17.*) ;; \
		*) echo "❌ JDK 17 is required; derived JAVA_HOME contains java $$java_version" >&2; exit 1 ;; \
	esac
	@actual_version="$$("$(FLUTTER)" --version --machine | python3 -c 'import json, sys; print(json.load(sys.stdin)["frameworkVersion"])')"; \
	if [ "$$actual_version" != "$(FLUTTER_VERSION)" ]; then \
		echo "❌ Flutter $(FLUTTER_VERSION) is required; found $$actual_version" >&2; \
		exit 1; \
	fi

client-android-policy-check:
	@echo "🔐 Checking Android backup and release policy..."
	PYTHONDONTWRITEBYTECODE=1 python3 client/tool/check_android_backup_policy.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s client/tool -p 'test_android_backup_policy.py'
	PYTHONDONTWRITEBYTECODE=1 python3 client/tool/check_android_release_policy.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s client/tool -p 'test_android_release_policy.py'

client-android-release-signing-check: client-toolchain-check client-android-policy-check client-deps
	@echo "🔏 Exercising Android release signing policy..."
	JAVA_HOME="$(CLIENT_JAVA_HOME)" PATH="$(CLIENT_JAVA_HOME)/bin:$$PATH" PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 \
		python3 client/tool/test_android_release_signing.py \
			--client-root client \
			--java-home "$(CLIENT_JAVA_HOME)"

client-format: client-deps
	@echo "✨ Formatting Flutter client..."
	cd client && "$(DART)" format lib test

client-format-check: client-deps
	@echo "🔍 Checking Flutter client formatting..."
	cd client && "$(DART)" format --output=none --set-exit-if-changed lib test

client-analyze: client-deps
	@echo "🔍 Analyzing Flutter client..."
	cd client && "$(FLUTTER)" analyze

client-test: client-deps
	@echo "🧪 Running Flutter client tests..."
	cd client && "$(FLUTTER)" test

client-apk-debug: client-deps
	@echo "🤖 Building Android client debug APK..."
	cd client && JAVA_HOME="$(CLIENT_JAVA_HOME)" PATH="$(CLIENT_JAVA_HOME)/bin:$$PATH" "$(FLUTTER)" build apk --debug

client-check: client-toolchain-check client-android-policy-check client-format-check client-analyze client-test client-apk-debug
	@echo "✅ Flutter client checks passed"
