#!/bin/bash
# MnemoNAS 开发启动脚本
# 用法: ./scripts/dev.sh [选项]
#   无参数: 启动所有组件
#   -b, --backend: 仅启动后端 (nasd + dataplane)
#   -f, --frontend: 仅启动前端
#   -k, --kill: 停止所有组件
#   -c, --creds: 显示 WebDAV 登录凭据

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 项目根目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
FRONTEND_NODE_VERSION_FILE="$PROJECT_ROOT/.nvmrc"

# 日志目录
LOG_DIR="$PROJECT_ROOT/logs"
mkdir -p "$LOG_DIR"

# PID 文件目录
PID_DIR="$PROJECT_ROOT/.pids"
mkdir -p "$PID_DIR"

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_section() {
    echo -e "\n${BLUE}━━━ $1 ━━━${NC}"
}

require_frontend_node() {
    local required_version=""

    if [ ! -f "$FRONTEND_NODE_VERSION_FILE" ]; then
        log_error "缺少 Node.js 版本文件: $FRONTEND_NODE_VERSION_FILE"
        log_info "请在项目根目录添加 .nvmrc 后重试"
        return 1
    fi

    required_version="$(tr -d '[:space:]' < "$FRONTEND_NODE_VERSION_FILE")"
    if [ -z "$required_version" ]; then
        log_error ".nvmrc 为空，无法确定前端 Node.js 版本"
        return 1
    fi

    if [ ! -f "$HOME/.nvm/nvm.sh" ]; then
        log_error "未找到 nvm，请先安装并加载 nvm"
        log_info "参考命令: source \"$HOME/.nvm/nvm.sh\" && nvm install $required_version && nvm use $required_version"
        return 1
    fi

    # shellcheck source=/dev/null
    source "$HOME/.nvm/nvm.sh"

    if ! nvm install "$required_version" >/dev/null; then
        log_error "无法安装 Node.js $required_version"
        return 1
    fi

    if ! nvm use "$required_version" >/dev/null; then
        log_error "无法切换到 Node.js $required_version"
        return 1
    fi

    if ! node ./scripts/check-node.cjs >/dev/null; then
        log_error "当前 Node.js 版本不满足前端要求: $(node --version 2>/dev/null || echo unknown)"
        log_info "请执行: source \"$HOME/.nvm/nvm.sh\" && nvm use"
        return 1
    fi

    log_info "前端 Node.js 版本: $(node --version)"
    return 0
}

# 检查端口是否被占用
check_port() {
    local port=$1
    if lsof -i :$port >/dev/null 2>&1; then
        return 0  # 端口被占用
    else
        return 1  # 端口空闲
    fi
}

# 等待服务启动
wait_for_service() {
    local name=$1
    local url=$2
    local max_attempts=30
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        if curl -s "$url" >/dev/null 2>&1; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 0.5
    done
    return 1
}

# 停止所有组件
kill_all() {
    log_section "停止所有组件"
    
    # 停止 nasd
    if [ -f "$PID_DIR/nasd.pid" ]; then
        local pid=$(cat "$PID_DIR/nasd.pid")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            log_info "已停止 nasd (PID: $pid)"
        fi
        rm -f "$PID_DIR/nasd.pid"
    fi
    
    # 停止 dataplane
    if [ -f "$PID_DIR/dataplane.pid" ]; then
        local pid=$(cat "$PID_DIR/dataplane.pid")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            log_info "已停止 dataplane (PID: $pid)"
        fi
        rm -f "$PID_DIR/dataplane.pid"
    fi
    
    # 停止前端开发服务器
    if [ -f "$PID_DIR/frontend.pid" ]; then
        local pid=$(cat "$PID_DIR/frontend.pid")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            log_info "已停止前端开发服务器 (PID: $pid)"
        fi
        rm -f "$PID_DIR/frontend.pid"
    fi
    
    # 兜底: 按端口杀进程 (9090=gRPC, 9091=HTTP)
    for port in 8080 9090 9091 5173; do
        if check_port $port; then
            local pid=$(lsof -t -i :$port 2>/dev/null || true)
            if [ -n "$pid" ]; then
                kill $pid 2>/dev/null || true
                log_warn "已清理端口 $port 上的进程 (PID: $pid)"
            fi
        fi
    done
    
    log_info "所有组件已停止"
}

# 构建项目
build_project() {
    log_section "构建项目"
    
    cd "$PROJECT_ROOT"
    
    # 构建 Go 控制面 (需要 CGO 支持 SQLite)
    log_info "构建 nasd..."
    CGO_ENABLED=1 go build -o bin/nasd ./cmd/nasd
    
    # 构建 Rust 数据面
    log_info "构建 dataplane..."
    cd dataplane && cargo build --release 2>&1 | tail -3
    cp target/release/dataplane ../bin/dataplane
    cd "$PROJECT_ROOT"
    
    log_info "构建完成"
}

# 启动 Rust 数据面
start_dataplane() {
    log_section "启动 Rust 数据面"
    
    if check_port 9090; then
        log_warn "端口 9090 已被占用，跳过启动 dataplane"
        return 0
    fi
    
    cd "$PROJECT_ROOT"
    
    if [ ! -f "bin/dataplane" ]; then
        log_error "bin/dataplane 不存在，请先运行构建"
        return 1
    fi
    
    # 启动 dataplane
    ./bin/dataplane > "$LOG_DIR/dataplane.log" 2>&1 &
    local pid=$!
    echo $pid > "$PID_DIR/dataplane.pid"
    
    # 等待服务就绪 (HTTP 端口 9091, gRPC 端口 9090)
    if wait_for_service "dataplane" "http://127.0.0.1:9091/health"; then
        log_info "dataplane 已启动 (PID: $pid, HTTP: 9091, gRPC: 9090)"
        log_info "  健康检查: curl http://127.0.0.1:9091/health"
        log_info "  统计信息: curl http://127.0.0.1:9091/stats"
    else
        log_error "dataplane 启动超时，请检查日志: $LOG_DIR/dataplane.log"
        return 1
    fi
}

# 启动 Go 控制面
start_nasd() {
    log_section "启动 Go 控制面"
    
    if check_port 8080; then
        log_warn "端口 8080 已被占用，跳过启动 nasd"
        return 0
    fi
    
    cd "$PROJECT_ROOT"
    
    if [ ! -f "bin/nasd" ]; then
        log_error "bin/nasd 不存在，请先运行构建"
        return 1
    fi
    
    # 启动 nasd
    ./bin/nasd > "$LOG_DIR/nasd.log" 2>&1 &
    local pid=$!
    echo $pid > "$PID_DIR/nasd.pid"
    
    # 等待服务就绪
    if wait_for_service "nasd" "http://127.0.0.1:8080/health"; then
        log_info "nasd 已启动 (PID: $pid, 端口: 8080)"
        log_info "  健康检查: curl http://127.0.0.1:8080/health"
        log_info "  WebDAV:   http://127.0.0.1:8080/dav/"
        log_info "  API:      http://127.0.0.1:8080/api/v1/"
        
        # 检查并显示自动生成的 WebDAV 密码
        if grep -q "WebDAV credentials (auto-generated" "$LOG_DIR/nasd.log"; then
            echo ""
            echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
            echo -e "${YELLOW}🔐 WebDAV 凭据 (自动生成，请保存!):${NC}"
            local username=$(grep "Username username=" "$LOG_DIR/nasd.log" | tail -1 | sed 's/.*username=//')
            local password=$(grep "Password password=" "$LOG_DIR/nasd.log" | tail -1 | sed 's/.*password=//')
            echo -e "   用户名: ${GREEN}${username}${NC}"
            echo -e "   密码:   ${GREEN}${password}${NC}"
            echo -e "   存储于: ~/.mnemonas/secrets.json"
            echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
            echo ""
        fi
    else
        log_error "nasd 启动超时，请检查日志: $LOG_DIR/nasd.log"
        return 1
    fi
}

# 启动前端开发服务器
start_frontend() {
    log_section "启动前端开发服务器"
    
    if check_port 5173; then
        log_warn "端口 5173 已被占用，跳过启动前端"
        return 0
    fi
    
    cd "$PROJECT_ROOT/web"

    if ! require_frontend_node; then
        return 1
    fi
    
    # 检查依赖
    if [ ! -d "node_modules" ]; then
        log_info "安装前端依赖..."
        npm install
    fi
    
    # 启动开发服务器
    npm run dev > "$LOG_DIR/frontend.log" 2>&1 &
    local pid=$!
    echo $pid > "$PID_DIR/frontend.pid"
    
    # 等待服务就绪
    sleep 2
    if wait_for_service "frontend" "http://127.0.0.1:5173"; then
        log_info "前端开发服务器已启动 (PID: $pid, 端口: 5173)"
        log_info "  访问地址: http://127.0.0.1:5173"
    else
        log_warn "前端开发服务器启动中... 请检查日志: $LOG_DIR/frontend.log"
    fi
}

# 显示 WebDAV 凭据
show_credentials() {
    local secrets_file="$HOME/.mnemonas/secrets.json"
    local config_file="$HOME/.mnemonas/config.toml"
    
    if [ ! -f "$secrets_file" ]; then
        log_error "凭据文件不存在: $secrets_file"
        log_info "请先启动服务以生成凭据: ./scripts/dev.sh"
        return 1
    fi

    local username="admin"
    if [ -f "$config_file" ]; then
        local configured_username
        configured_username=$(awk '
            /^\[webdav\]$/ { in_webdav = 1; next }
            /^\[/ { in_webdav = 0 }
            in_webdav && /^[[:space:]]*username[[:space:]]*=/ {
                line = $0
                sub(/^[^=]*=[[:space:]]*/, "", line)
                sub(/[[:space:]]*#.*$/, "", line)
                gsub(/^"/, "", line)
                gsub(/"$/, "", line)
                print line
                exit
            }
        ' "$config_file")
        if [ -n "$configured_username" ]; then
            username="$configured_username"
        fi
    fi
    
    echo ""
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${YELLOW}🔐 WebDAV 凭据:${NC}"
    local password=$(cat "$secrets_file" | grep -o '"webdav_password"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*: *"//' | sed 's/"$//')
    echo -e "   用户名: ${GREEN}${username}${NC}"
    echo -e "   密码:   ${GREEN}${password}${NC}"
    echo -e "   存储于: $secrets_file"
    echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
}

# 显示状态
show_status() {
    log_section "服务状态"
    
    echo ""
    echo "┌─────────────┬────────┬──────────────────────────────────┐"
    echo "│ 组件        │ 状态   │ 地址                             │"
    echo "├─────────────┼────────┼──────────────────────────────────┤"
    
    # dataplane 状态 (HTTP: 9091, gRPC: 9090)
    if check_port 9091; then
        echo "│ dataplane   │ ✅ 运行 │ HTTP:9091 gRPC:9090              │"
    else
        echo "│ dataplane   │ ❌ 停止 │ -                                │"
    fi
    
    # nasd 状态
    if check_port 8080; then
        echo "│ nasd        │ ✅ 运行 │ http://127.0.0.1:8080            │"
    else
        echo "│ nasd        │ ❌ 停止 │ -                                │"
    fi
    
    # 前端状态
    if check_port 5173; then
        echo "│ frontend    │ ✅ 运行 │ http://127.0.0.1:5173            │"
    else
        echo "│ frontend    │ ❌ 停止 │ -                                │"
    fi
    
    echo "└─────────────┴────────┴──────────────────────────────────┘"
    echo ""
    echo "日志目录: $LOG_DIR"
}

# 主函数
main() {
    case "${1:-all}" in
        -k|--kill|kill|stop)
            kill_all
            ;;
        -c|--creds|creds|credentials)
            show_credentials
            ;;
        -b|--backend|backend)
            kill_all
            build_project
            start_dataplane
            start_nasd
            show_status
            ;;
        -f|--frontend|frontend)
            start_frontend
            show_status
            ;;
        -s|--status|status)
            show_status
            ;;
        all|start|"")
            kill_all
            build_project
            start_dataplane
            start_nasd
            start_frontend
            show_status
            echo ""
            log_info "所有组件已启动！按 Ctrl+C 停止，或运行: $0 --kill"
            ;;
        -h|--help|help)
            echo "MnemoNAS 开发启动脚本"
            echo ""
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  (无)        启动所有组件 (默认)"
            echo "  -b, --backend   仅启动后端 (nasd + dataplane)"
            echo "  -f, --frontend  仅启动前端开发服务器"
            echo "  -s, --status    查看服务状态"
            echo "  -k, --kill      停止所有组件"
            echo "  -h, --help      显示帮助"
            ;;
        *)
            log_error "未知选项: $1"
            echo "运行 '$0 --help' 查看帮助"
            exit 1
            ;;
    esac
}

main "$@"
