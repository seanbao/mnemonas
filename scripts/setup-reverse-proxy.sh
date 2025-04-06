#!/bin/bash
# MnemoNAS 反向代理自动配置脚本 (Ubuntu)
# 用法: sudo ./setup-reverse-proxy.sh <域名> [邮箱]
# 示例: sudo ./setup-reverse-proxy.sh nas.example.com admin@example.com

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 检查参数
DOMAIN="${1:-}"
EMAIL="${2:-}"

if [[ -z "$DOMAIN" ]]; then
    echo "用法: sudo $0 <域名> [邮箱]"
    echo "示例: sudo $0 nas.example.com admin@example.com"
    exit 1
fi

if [[ -z "$EMAIL" ]]; then
    EMAIL="admin@${DOMAIN}"
    log_warn "未指定邮箱，使用默认: $EMAIL"
fi

# 检查 root 权限
if [[ $EUID -ne 0 ]]; then
    log_error "请使用 sudo 运行此脚本"
    exit 1
fi

# 检查 Ubuntu 版本
if ! grep -q "Ubuntu" /etc/os-release 2>/dev/null; then
    log_warn "非 Ubuntu 系统，脚本可能不兼容"
fi

log_info "配置域名: $DOMAIN"
log_info "证书邮箱: $EMAIL"

# 选择方案
echo ""
echo "请选择反向代理方案:"
echo "  1) Caddy (推荐，自动 HTTPS)"
echo "  2) Nginx + Certbot"
echo ""
read -p "选择 [1/2]: " CHOICE

case "$CHOICE" in
    1|"")
        PROXY_TYPE="caddy"
        ;;
    2)
        PROXY_TYPE="nginx"
        ;;
    *)
        log_error "无效选择"
        exit 1
        ;;
esac

log_info "选择方案: $PROXY_TYPE"

#######################################
# Caddy 安装配置
#######################################
install_caddy() {
    log_info "安装 Caddy..."
    
    # 安装依赖
    apt-get update
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl
    
    # 添加 Caddy 仓库
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | \
        gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | \
        tee /etc/apt/sources.list.d/caddy-stable.list
    
    apt-get update
    apt-get install -y caddy
    
    log_info "配置 Caddyfile..."
    
    # 备份原配置
    if [[ -f /etc/caddy/Caddyfile ]]; then
        cp /etc/caddy/Caddyfile /etc/caddy/Caddyfile.bak.$(date +%Y%m%d%H%M%S)
    fi
    
    # 写入新配置
    cat > /etc/caddy/Caddyfile << EOF
# MnemoNAS 反向代理配置
# 生成时间: $(date)

$DOMAIN {
    # 自动 HTTPS (Let's Encrypt)
    tls $EMAIL
    
    # 反向代理到 MnemoNAS
    reverse_proxy localhost:8080 {
        header_up Host {host}
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
    
    # 大文件上传支持 (10GB)
    request_body {
        max_size 10GB
    }
    
    # 访问日志
    log {
        output file /var/log/caddy/access.log {
            roll_size 100mb
            roll_keep 5
        }
        format json
    }
}
EOF

    # 创建日志目录
    mkdir -p /var/log/caddy
    chown caddy:caddy /var/log/caddy
    
    # 验证配置
    log_info "验证 Caddy 配置..."
    caddy validate --config /etc/caddy/Caddyfile
    
    # 启动服务
    systemctl enable caddy
    systemctl restart caddy
    
    log_info "Caddy 配置完成!"
}

#######################################
# Nginx + Certbot 安装配置
#######################################
install_nginx() {
    log_info "安装 Nginx 和 Certbot..."
    
    apt-get update
    apt-get install -y nginx certbot python3-certbot-nginx
    
    log_info "配置 Nginx..."
    
    # 写入 Nginx 配置
    cat > /etc/nginx/sites-available/$DOMAIN << 'EOF'
# MnemoNAS 反向代理配置
# 生成时间: GENERATED_TIME

server {
    listen 80;
    server_name DOMAIN_PLACEHOLDER;
    
    # Certbot 验证
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }
    
    # HTTP 跳转 HTTPS
    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    server_name DOMAIN_PLACEHOLDER;
    
    # SSL 证书 (certbot 会自动配置)
    ssl_certificate /etc/letsencrypt/live/DOMAIN_PLACEHOLDER/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/DOMAIN_PLACEHOLDER/privkey.pem;
    
    # SSL 安全配置
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
    
    # HSTS
    add_header Strict-Transport-Security "max-age=63072000" always;
    
    # 大文件上传 (10GB)
    client_max_body_size 10G;
    client_body_timeout 3600s;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    
    # 禁用缓冲 (流式传输)
    proxy_buffering off;
    proxy_request_buffering off;
    
    # 反向代理
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # HTTP/1.1 支持
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
    
    # 访问日志
    access_log /var/log/nginx/DOMAIN_PLACEHOLDER.access.log;
    error_log /var/log/nginx/DOMAIN_PLACEHOLDER.error.log;
}
EOF

    # 替换占位符
    sed -i "s/DOMAIN_PLACEHOLDER/$DOMAIN/g" /etc/nginx/sites-available/$DOMAIN
    sed -i "s/GENERATED_TIME/$(date)/g" /etc/nginx/sites-available/$DOMAIN
    
    # 创建 certbot 目录
    mkdir -p /var/www/certbot
    
    # 启用站点
    ln -sf /etc/nginx/sites-available/$DOMAIN /etc/nginx/sites-enabled/
    
    # 删除默认站点
    rm -f /etc/nginx/sites-enabled/default
    
    # 创建临时配置用于申请证书
    cat > /etc/nginx/sites-available/$DOMAIN.temp << EOF
server {
    listen 80;
    server_name $DOMAIN;
    
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }
    
    location / {
        return 200 'MnemoNAS setup in progress';
        add_header Content-Type text/plain;
    }
}
EOF
    
    # 先用临时配置启动
    ln -sf /etc/nginx/sites-available/$DOMAIN.temp /etc/nginx/sites-enabled/$DOMAIN
    
    # 测试并重载
    nginx -t
    systemctl enable nginx
    systemctl restart nginx
    
    log_info "申请 Let's Encrypt 证书..."
    certbot certonly --webroot -w /var/www/certbot \
        -d $DOMAIN \
        --email $EMAIL \
        --agree-tos \
        --non-interactive
    
    # 切换到正式配置
    ln -sf /etc/nginx/sites-available/$DOMAIN /etc/nginx/sites-enabled/$DOMAIN
    rm -f /etc/nginx/sites-available/$DOMAIN.temp
    
    # 重载配置
    nginx -t
    systemctl reload nginx
    
    # 设置自动续期
    log_info "配置证书自动续期..."
    (crontab -l 2>/dev/null; echo "0 3 * * * certbot renew --quiet --post-hook 'systemctl reload nginx'") | crontab -
    
    log_info "Nginx + Certbot 配置完成!"
}

#######################################
# 防火墙配置
#######################################
configure_firewall() {
    log_info "配置防火墙..."
    
    # 检查 ufw 是否已安装
    if command -v ufw &> /dev/null; then
        ufw allow 80/tcp
        ufw allow 443/tcp
        log_info "已开放 80/443 端口"
    else
        log_warn "未检测到 ufw，请手动配置防火墙"
    fi
}

#######################################
# 主流程
#######################################
main() {
    echo ""
    log_info "=========================================="
    log_info "MnemoNAS 反向代理自动配置"
    log_info "=========================================="
    echo ""
    
    # 配置防火墙
    configure_firewall
    
    # 安装反向代理
    if [[ "$PROXY_TYPE" == "caddy" ]]; then
        install_caddy
    else
        install_nginx
    fi
    
    echo ""
    log_info "=========================================="
    log_info "配置完成!"
    log_info "=========================================="
    echo ""
    echo "访问地址: https://$DOMAIN"
    echo "WebDAV:   https://$DOMAIN/dav"
    echo ""
    echo "验证命令:"
    echo "  curl -I https://$DOMAIN/health"
    echo "  WEBDAV_USER=<webdav-username>"
    echo "  WEBDAV_PASS=<webdav-password>"
    echo "  curl -u \"\$WEBDAV_USER:\$WEBDAV_PASS\" -X PROPFIND https://$DOMAIN/dav/ -H 'Depth: 0'"
    echo ""
    
    if [[ "$PROXY_TYPE" == "caddy" ]]; then
        echo "管理命令:"
        echo "  systemctl status caddy    # 查看状态"
        echo "  journalctl -u caddy -f    # 查看日志"
        echo "  caddy reload              # 重载配置"
    else
        echo "管理命令:"
        echo "  systemctl status nginx    # 查看状态"
        echo "  nginx -t                  # 测试配置"
        echo "  systemctl reload nginx    # 重载配置"
        echo "  certbot certificates      # 查看证书"
    fi
    echo ""
}

main
