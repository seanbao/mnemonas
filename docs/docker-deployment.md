# Docker 部署指南

本文档介绍如何使用 Docker 部署 MnemoNAS，包括基础部署、生产环境配置和常见场景示例。

## 📋 前置要求

- Docker 20.10+ 和 Docker Compose 2.0+
- 至少 1GB 可用内存
- 建议使用 SSD 存储（HDD 也可工作，但性能较低）

检查 Docker 版本：
```bash
docker --version
docker compose version
```

---

## 🚀 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/seanbao/mnemonas.git
cd mnemonas
```

### 2. 创建配置文件

```bash
cp mnemonas.example.toml mnemonas.toml
```

编辑 `mnemonas.toml`，至少修改：
- `password` - WebDAV 认证密码

### 3. 启动服务

```bash
docker compose up -d
```

### 4. 验证服务

```bash
# 健康检查
curl http://localhost:8080/health

# 查看日志
docker compose logs -f
```

---

## 🏠 家庭场景配置

### 场景一：家庭媒体服务器

将 MnemoNAS 用作家庭照片/视频存储，外接大容量硬盘。

**docker-compose.yml**:
```yaml
services:
  mnemonas:
    image: ghcr.io/seanbao/mnemonas:latest
    container_name: mnemonas
    ports:
      - "8080:8080"
    volumes:
      # 数据存储到外接硬盘
      - /mnt/external-disk/mnemonas/data:/var/lib/mnemonas/data
      - /mnt/external-disk/mnemonas/metadata:/var/lib/mnemonas/metadata
      # 配置文件
      - ./mnemonas.toml:/etc/mnemonas/config.toml:ro
    environment:
      - TZ=Asia/Shanghai
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
```

**mnemonas.toml**:
```toml
[server]
host = "0.0.0.0"
port = 8080

[storage]
data_dir = "/var/lib/mnemonas/data"
metadata_dir = "/var/lib/mnemonas/metadata"

[storage.retention]
max_versions = 50        # 照片/视频保留 50 个版本足够
max_age = "17520h"       # 保留 2 年

[webdav]
enabled = true
prefix = "/dav"
auth_type = "basic"
username = "family"
password = "your-secure-password"  # 请修改！

[log]
level = "info"
```

### 场景二：开发者工作站备份

用于备份代码、文档等工作文件，需要更频繁的版本保留。

**docker-compose.yml**:
```yaml
services:
  mnemonas:
    image: ghcr.io/seanbao/mnemonas:latest
    container_name: mnemonas-dev
    ports:
      - "8080:8080"
    volumes:
      - ~/.mnemonas/data:/var/lib/mnemonas/data
      - ~/.mnemonas/metadata:/var/lib/mnemonas/metadata
      - ./mnemonas.toml:/etc/mnemonas/config.toml:ro
    restart: unless-stopped
```

**mnemonas.toml**:
```toml
[server]
host = "127.0.0.1"  # 仅本地访问
port = 8080

[storage.retention]
max_versions = 200       # 代码文件保留更多版本
max_age = "8760h"        # 保留 1 年
gc_interval = "1h"       # 更频繁的 GC

[webdav]
enabled = true
auth_type = "none"       # 本地使用，无需认证

[log]
level = "debug"          # 开发时可用 debug
```

### 场景三：多用户共享 NAS

支持家庭成员各有独立账号（未来功能，当前使用单一账号）。

**docker-compose.yml**:
```yaml
services:
  mnemonas:
    image: ghcr.io/seanbao/mnemonas:latest
    container_name: family-nas
    ports:
      - "8080:8080"
    volumes:
      - /srv/nas/data:/var/lib/mnemonas/data
      - /srv/nas/metadata:/var/lib/mnemonas/metadata
      - ./mnemonas.toml:/etc/mnemonas/config.toml:ro
    environment:
      - TZ=Asia/Shanghai
    restart: always
    # 限制资源使用
    deploy:
      resources:
        limits:
          memory: 2G
        reservations:
          memory: 512M
```

---

## 🔒 生产环境配置

### 使用 HTTPS（Nginx 反向代理）

**docker-compose.yml**:
```yaml
services:
  mnemonas:
    image: ghcr.io/seanbao/mnemonas:latest
    container_name: mnemonas
    # 不暴露端口，通过 nginx 访问
    expose:
      - "8080"
    volumes:
      - mnemonas-data:/var/lib/mnemonas
      - ./mnemonas.toml:/etc/mnemonas/config.toml:ro
    restart: unless-stopped
    networks:
      - internal

  nginx:
    image: nginx:alpine
    container_name: nginx
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./certs:/etc/nginx/certs:ro
    depends_on:
      - mnemonas
    restart: unless-stopped
    networks:
      - internal

networks:
  internal:

volumes:
  mnemonas-data:
```

**nginx.conf**:
```nginx
events {
    worker_connections 1024;
}

http {
    upstream mnemonas {
        server mnemonas:8080;
    }

    server {
        listen 80;
        server_name nas.example.com;
        return 301 https://$server_name$request_uri;
    }

    server {
        listen 443 ssl;
        server_name nas.example.com;

        ssl_certificate /etc/nginx/certs/fullchain.pem;
        ssl_certificate_key /etc/nginx/certs/privkey.pem;
        ssl_protocols TLSv1.2 TLSv1.3;

        client_max_body_size 0;  # 不限制上传大小

        location / {
            proxy_pass http://mnemonas;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            
            # WebDAV 需要这些头
            proxy_pass_request_headers on;
            proxy_set_header Destination $http_destination;
        }
    }
}
```

### 使用 Traefik 反向代理

```yaml
services:
  mnemonas:
    image: ghcr.io/seanbao/mnemonas:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mnemonas.rule=Host(`nas.example.com`)"
      - "traefik.http.routers.mnemonas.tls=true"
      - "traefik.http.routers.mnemonas.tls.certresolver=letsencrypt"
      - "traefik.http.services.mnemonas.loadbalancer.server.port=8080"
    volumes:
      - mnemonas-data:/var/lib/mnemonas
    networks:
      - traefik-network
```

---

## 📊 监控与日志

### 查看日志

```bash
# 实时日志
docker compose logs -f mnemonas

# 最近 100 行
docker compose logs --tail 100 mnemonas

# 输出到文件
docker compose logs mnemonas > mnemonas.log
```

### 健康检查

```bash
# 内置健康检查
docker inspect --format='{{.State.Health.Status}}' mnemonas

# API 健康检查
curl http://localhost:8080/health
```

### 集成 Prometheus

MnemoNAS 暴露 `/api/v1/metrics` 端点，可被 Prometheus 抓取。

---

## 🔄 升级与备份

### 升级服务

```bash
# 拉取最新镜像
docker compose pull

# 重启服务（数据保留）
docker compose up -d
```

### 备份数据

```bash
# 停止服务
docker compose stop

# 备份 Volume
docker run --rm \
  -v mnemonas_mnemonas-data:/data \
  -v $(pwd):/backup \
  alpine tar czf /backup/mnemonas-backup-$(date +%Y%m%d).tar.gz /data

# 重启服务
docker compose start
```

### 恢复数据

```bash
# 停止服务
docker compose down

# 恢复 Volume
docker run --rm \
  -v mnemonas_mnemonas-data:/data \
  -v $(pwd):/backup \
  alpine sh -c "cd / && tar xzf /backup/mnemonas-backup-20240101.tar.gz"

# 启动服务
docker compose up -d
```

---

## 🔧 故障排除

### 容器无法启动

```bash
# 查看详细日志
docker compose logs mnemonas

# 检查配置文件语法
docker run --rm -v $(pwd)/mnemonas.toml:/etc/mnemonas/config.toml:ro \
  ghcr.io/seanbao/mnemonas:latest --config-check
```

### 权限问题

```bash
# 检查挂载目录权限
ls -la /path/to/data

# 修复权限（容器内使用 uid 1000）
sudo chown -R 1000:1000 /path/to/data
```

### 端口冲突

```bash
# 查看端口占用
sudo lsof -i :8080

# 使用其他端口
# 修改 docker-compose.yml: ports: - "8888:8080"
```

---

## 📖 更多资源

- [挂载指南](mounting-guide.md)
- [FAQ](faq.md)
- [配置参考](../mnemonas.example.toml)
