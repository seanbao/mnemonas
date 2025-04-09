.PHONY: all build test clean deps dev proto fmt lint docker e2e bench coverage help

# 版本信息
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

# 默认目标
all: build

# 显示帮助
help:
	@echo "MnemoNAS Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build      - Build all binaries (proto → Go → Rust)"
	@echo "  dev        - Quick development build (debug mode)"
	@echo "  test       - Run all tests"
	@echo "  coverage   - Run tests with coverage report"
	@echo "  lint       - Run linters (Go + Rust)"
	@echo "  fmt        - Format code (Go + Rust)"
	@echo "  e2e        - Run E2E acceptance tests"
	@echo "  bench      - Run performance benchmarks"
	@echo "  proto      - Generate protobuf code"
	@echo "  docker     - Build Docker image"
	@echo "  clean      - Remove build artifacts"
	@echo "  deps       - Download dependencies"
	@echo "  help       - Show this help"

# 安装依赖
deps:
	@echo "📦 Installing dependencies..."
	cd dataplane && cargo fetch
	go mod download
	cd web && npm ci

# 生成protobuf代码
proto:
	@echo "🔧 Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/dataplane.proto
	cd dataplane && cargo build --release

# 构建
build: proto
	@echo "🏗️  Building Go control plane..."
	CGO_ENABLED=1 go build -ldflags="$(LDFLAGS)" -o bin/nasd ./cmd/nasd
	@echo "🦀 Building Rust data plane..."
	cd dataplane && cargo build --release
	cp dataplane/target/release/dataplane bin/dataplane
	@echo "✅ Build complete: bin/nasd, bin/dataplane"

# 开发模式构建
dev:
	@echo "🔨 Development build..."
	CGO_ENABLED=1 go build -o bin/nasd ./cmd/nasd
	cd dataplane && cargo build
	cp dataplane/target/debug/dataplane bin/dataplane-debug
	@echo "✅ Dev build complete"

# 运行测试
test:
	@echo "🧪 Running Go tests..."
	CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -v -race ./...
	@echo "🦀 Running Rust tests..."
	cd dataplane && cargo test
	@echo "🌐 Running frontend tests..."
	cd web && npm run test:run

# 测试覆盖率
coverage:
	@echo "📊 Generating coverage reports..."
	@mkdir -p coverage
	CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -coverprofile=coverage/go.out ./...
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
	go clean
	@echo "✅ Clean complete"

# 格式化代码
fmt:
	@echo "✨ Formatting code..."
	go fmt ./...
	cd dataplane && cargo fmt
	cd web && npm run lint -- --fix 2>/dev/null || true

# 代码检查
lint:
	@echo "🔍 Linting Go..."
	golangci-lint run || echo "⚠️  golangci-lint not installed, skipping"
	@echo "🔍 Linting Rust..."
	cd dataplane && cargo clippy -- -D warnings
	@echo "🔍 Linting frontend..."
	cd web && npm run lint

# Docker构建
docker:
	@echo "🐳 Building Docker image..."
	docker build -t mnemonas:$(VERSION) -t mnemonas:latest .
	@echo "✅ Docker image: mnemonas:$(VERSION)"

# 运行所有检查 (CI 使用)
check: lint test
	@echo "✅ All checks passed"

# 快速检查 (commit 前)
quick-check:
	@echo "🚀 Quick check..."
	CGO_ENABLED=1 go build ./...
	CGO_ENABLED=1 bash ./scripts/with-test-dataplane.sh go test -short ./...
	cd dataplane && cargo check
	@echo "✅ Quick check passed"
