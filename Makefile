.PHONY: all build web-build test clean deps dev proto proto-go proto-rust go-packages fmt lint scripts-check security-check install-audit-tools docker e2e bench coverage check help

# 版本信息
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)
GO_PACKAGES ?=
GO_PACKAGE_PATTERN ?= ./...
GO_PACKAGE_EXCLUDE_PATTERN ?= /web/node_modules/
GO_LINT_PACKAGES ?= ./...
GOVULNCHECK_VERSION ?= v1.3.0
CARGO_AUDIT_VERSION ?= 0.22.1
GO_SECURITY_ENV ?= GOSUMDB=sum.golang.org GOTOOLCHAIN=auto
NPM_AUDIT ?= 0
DEPLOYMENT_SCRIPTS := scripts/install-systemd.sh scripts/uninstall-systemd.sh scripts/mnemonas-doctor.sh scripts/mnemonas-docker-preflight.sh scripts/docker-quickstart.sh scripts/mnemonas-dataplane-start.sh scripts/test-systemd-install.sh scripts/test-systemd-uninstall.sh scripts/test-docker-start.sh scripts/test-docker-preflight.sh scripts/test-docker-quickstart.sh scripts/docker-start.sh scripts/setup-reverse-proxy.sh scripts/dev.sh scripts/benchmark.sh
ACCEPTANCE_SCRIPTS := scripts/e2e-test.sh scripts/fault-injection-test.sh
WEB_SCRIPTS := web/scripts/start-e2e-backend.sh

define RESOLVE_GO_PACKAGES
packages="$(GO_PACKAGES)"; \
go_list_env="$${GO_LIST_ENV:-}"; \
if [ -z "$$packages" ]; then \
	packages="$$(env $$go_list_env go list $(GO_PACKAGE_PATTERN) | grep -v '$(GO_PACKAGE_EXCLUDE_PATTERN)')"; \
fi; \
if [ -z "$$packages" ]; then \
	echo "❌ no Go packages resolved" >&2; \
	exit 1; \
fi
endef

# 默认目标
all: build

# 显示帮助
help:
	@echo "MnemoNAS Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build      - Build Web UI and binaries (proto → Web → Go → Rust)"
	@echo "  dev        - Quick development build (debug mode)"
	@echo "  test       - Run all tests"
	@echo "  coverage   - Run tests with coverage report"
	@echo "  lint       - Run linters (Go + Rust)"
	@echo "  scripts-check - Validate deployment shell scripts"
	@echo "  security-check - Run dependency vulnerability checks"
	@echo "  install-audit-tools - Install pinned security scan tools"
	@echo "  fmt        - Format code (Go + Rust)"
	@echo "  e2e        - Run E2E acceptance tests"
	@echo "  bench      - Run performance benchmarks"
	@echo "  proto      - Generate protobuf code"
	@echo "  go-packages - Print resolved Go package list"
	@echo "  docker     - Build Docker image"
	@echo "  clean      - Remove build artifacts"
	@echo "  deps       - Download dependencies"
	@echo "  help       - Show this help"

# 安装依赖
deps:
	@echo "📦 Installing dependencies..."
	cd dataplane && cargo fetch --locked
	cargo fetch --manifest-path tools/proto-gen/Cargo.toml --locked
	go mod download
	cd web && npm ci

# 生成 protobuf 代码。Rust 生成代码会提交到仓库，避免普通 dataplane/Docker 构建依赖 protoc。
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

# 构建
build: proto web-build
	@echo "🏗️  Building Go control plane..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/nasd ./cmd/nasd
	@echo "🦀 Building Rust data plane..."
	cd dataplane && cargo build --release --locked
	cp dataplane/target/release/dataplane bin/dataplane
	@echo "✅ Build complete: bin/nasd, bin/dataplane, web/dist/"

web-build:
	@echo "🌐 Building Web UI..."
	cd web && npm run build

# 开发模式构建
dev:
	@echo "🔨 Development build..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/nasd ./cmd/nasd
	cd dataplane && cargo build
	cp dataplane/target/debug/dataplane bin/dataplane-debug
	@echo "✅ Dev build complete"

# 运行测试
test:
	@echo "🧪 Running Go tests..."
	@$(RESOLVE_GO_PACKAGES); \
	CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -v -race $$packages
	@echo "🦀 Running Rust tests..."
	cd dataplane && cargo test --locked
	cargo test --manifest-path tools/proto-gen/Cargo.toml --locked
	@echo "🌐 Running frontend tests..."
	cd web && npm run test:run

# 测试覆盖率
coverage:
	@echo "📊 Generating coverage reports..."
	@mkdir -p coverage
	@$(RESOLVE_GO_PACKAGES); \
	CGO_ENABLED=0 bash ./scripts/with-test-dataplane.sh go test -coverprofile=coverage/go.out $$packages
	go tool cover -html=coverage/go.out -o coverage/go.html
	cd web && npm run test:coverage
	@echo "✅ Coverage reports: coverage/go.html, web/coverage/"

# E2E 测试
e2e:
	@echo "🔗 Running E2E tests..."
	@chmod +x scripts/e2e-test.sh
	./scripts/e2e-test.sh

# 性能基准测试
bench:
	@echo "⏱️  Running benchmarks..."
	@chmod +x scripts/benchmark.sh
	./scripts/benchmark.sh

# 运行
run: build
	./bin/nasd

# 清理
clean:
	@echo "🧹 Cleaning..."
	rm -rf bin/ coverage/
	cd dataplane && cargo clean
	cd web && rm -rf dist coverage
	go clean
	@echo "✅ Clean complete"

# 格式化代码
fmt:
	@echo "✨ Formatting code..."
	@$(RESOLVE_GO_PACKAGES); \
	go fmt $$packages
	cd dataplane && cargo fmt
	cargo fmt --manifest-path tools/proto-gen/Cargo.toml
	cd web && npm run lint -- --fix 2>/dev/null || true

# 代码检查
lint:
	@echo "🔍 Linting Go..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(GO_LINT_PACKAGES); \
	else \
		echo "⚠️  golangci-lint not installed, skipping"; \
	fi
	@echo "🔍 Linting Rust..."
	cd dataplane && cargo clippy --all-targets --locked -- -D warnings
	cargo clippy --manifest-path tools/proto-gen/Cargo.toml --locked -- -D warnings
	@echo "🔍 Linting frontend..."
	cd web && npm run lint

# 部署脚本检查
scripts-check:
	@echo "🔍 Checking deployment scripts..."
	bash -n $(DEPLOYMENT_SCRIPTS) $(ACCEPTANCE_SCRIPTS) $(WEB_SCRIPTS)
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck $(DEPLOYMENT_SCRIPTS) $(WEB_SCRIPTS); \
		shellcheck -e SC2155 -e SC2317 $(ACCEPTANCE_SCRIPTS); \
	else \
		echo "⚠️  shellcheck not installed, skipping"; \
	fi
	./scripts/test-systemd-install.sh
	./scripts/test-systemd-uninstall.sh
	./scripts/test-docker-start.sh
	./scripts/test-docker-preflight.sh
	./scripts/test-docker-quickstart.sh

# 安全依赖检查
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
		cd web && npm audit --audit-level=high; \
	else \
		echo "⚠️  Skipping npm audit by default because it sends the dependency tree to the configured npm registry. Run: make security-check NPM_AUDIT=1"; \
	fi

install-audit-tools:
	@echo "🔧 Installing pinned security scan tools..."
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	CARGO_REGISTRIES_CRATES_IO_PROTOCOL=sparse cargo install cargo-audit --version $(CARGO_AUDIT_VERSION) --locked

# Docker构建
docker:
	@echo "🐳 Building Docker image..."
	DOCKER_BUILDKIT=1 docker build --build-arg VERSION=$(VERSION) --build-arg BUILD_TIME=$(BUILD_TIME) -t mnemonas:$(VERSION) -t mnemonas:latest .
	@echo "✅ Docker image: mnemonas:$(VERSION)"

# 运行所有检查 (CI 使用)
check: scripts-check lint test
	@echo "✅ All checks passed"

# 快速检查 (commit 前)
quick-check:
	@echo "🚀 Quick check..."
	@$(RESOLVE_GO_PACKAGES); \
	CGO_ENABLED=0 go build $$packages; \
	CGO_ENABLED=0 bash ./scripts/with-test-dataplane.sh go test -short $$packages
	cd dataplane && cargo check --locked
	cargo check --manifest-path tools/proto-gen/Cargo.toml --locked
	@echo "✅ Quick check passed"
