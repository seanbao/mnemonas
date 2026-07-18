<!-- markdownlint-disable MD032 MD060 -->

# WebDAV 客户端兼容性

[English](webdav-compatibility.en.md) | 简体中文

本文档记录 MnemoNAS WebDAV 协议覆盖范围和预期客户端兼容性。客户端版本、操作系统策略和网络配置都会影响行为，因此 release 前后仍应持续执行真实客户端回归验证。

REST API 资源复制接口位于 `/api/v1/files-copy`；WebDAV `Overwrite: T/F` 行为仅适用于 WebDAV `COPY` 方法。

部分写请求可能在可见变更已经提交后，因后续持久化或清理步骤失败而返回成功状态码。此时 MnemoNAS 会发送 HTTP `Warning` 响应头，而不会把已经提交的变更改写为整体失败。当前覆盖的 warning 值包括 `199 MnemoNAS "workspace mutation persistence incomplete"`、`199 MnemoNAS "delete cleanup incomplete"` 和 `199 MnemoNAS "trash delete cleanup incomplete"`。对于 `DELETE`，`delete cleanup incomplete` 仅表示永久删除模式的隔离区清理未完成，`trash delete cleanup incomplete` 表示实时回收站转移完成后的容量清理未完成。若实时回收站转移的可见变更已经完成，但终态日志清理的持久化状态无法确认，响应仍使用 `workspace mutation persistence incomplete`，同时启用恢复门禁；在恢复成功前，后续写请求可能失败。实时转移的日志、参与者、回执、发件箱、源端或目标端发生其他硬失败时返回 `500 Internal Server Error`，同样会保留恢复证据并阻止后续存储变更。

同源 URI 处理：

- `COPY` / `MOVE` 的 `Destination` 头，以及锁相关 `If` 头中的带标签 URI，必须指向当前 WebDAV 主机。
- 带 WebDAV 前缀的绝对路径引用也可接受，例如 `/dav/path`。
- 裸相对引用会被拒绝。即使引用看起来包含 WebDAV 前缀，例如 `dav/path`，也必须写成 `/dav/path` 或同源绝对 URI。
- 默认端口（HTTP 80、HTTPS 443）可以省略，也可以显式写出。
- scheme-relative URI（例如 `//host/dav/path`）仅在主机匹配且两边都省略端口，或两边使用相同显式端口时接受。
- 主机名的单个 FQDN 尾点会视为同一主机，重复尾点会被拒绝。
- URI 路径会解码一次；控制字符和 `.` / `..` 路径段会被拒绝；反斜杠会先归一化为路径分隔符，再执行前缀和权限边界检查。

认证：

- `auth_type = "users"` 接受 MnemoNAS 用户凭据。
- 普通用户的挂载根目录映射到自己的 `home_dir`。
- 已授权的共享目录会作为挂载根目录下的顶层导航入口出现。
- 共享路径按命中的目录授权规则放行。guest 账号只读。
- 写入 `home_dir` 的 PUT/COPY/MOVE 会执行用户配额限制；共享路径容量限制由目录配额处理。
- 为嵌套授权合成的祖先入口仅用于只读导航。写操作仍需命中写授权。
- `auth_type = "basic"` 保留为全局服务凭据兼容模式。

响应安全头：

- 文件响应、目录 HTML 列表、`PROPFIND` / `PROPPATCH` / `LOCK` XML 响应均设置 `X-Content-Type-Options: nosniff`。
- 这些包含用户文件名或路径的响应还会设置 sandbox 形式的 `Content-Security-Policy`，以限制浏览器直接打开 WebDAV URL 时的脚本、对象和框架能力。标准 WebDAV 客户端通常会忽略这些浏览器安全头。

## 协议状态

### 已实现核心方法

| 方法 | 状态 | 说明 |
| --- | --- | --- |
| `OPTIONS` | 支持 | 返回 `DAV: 1, 2`、`MS-Author-Via: DAV` 和 `Allow` 方法列表；只读挂载和只读用户仅列出读方法 |
| `PROPFIND` | 支持 | 支持 `Depth: 0`、`1` 和 `infinity` |
| `GET` | 支持 | 支持 Range、ETag 和条件请求 |
| `HEAD` | 支持 | 返回文件元数据 |
| `PUT` | 支持 | 完整覆盖写入；直接父目录必须存在，不隐式创建中间目录；支持条件 `If-Match`、`If-None-Match` 和 `If-Unmodified-Since`；partial `Content-Range` PUT 返回 `400` |
| `DELETE` | 支持 | 遵循当前删除策略，可移入回收站或永久删除；集合资源要求或隐含 `Depth: infinity` |
| `MKCOL` | 支持 | 创建目录；直接父目录不存在时返回 `409 Conflict`，目标已存在时返回带 `Allow` 的 `405 Method Not Allowed`，且不会创建中间目录 |
| `MOVE` | 支持 | 移动/重命名，支持 `Overwrite: T/F`；集合资源要求或隐含 `Depth: infinity`；覆盖提交后若 backup cleanup 失败，返回 `204` 并附带 `Warning` |
| `COPY` | 支持 | 复制文件和目录；支持 `Overwrite: T/F`；集合资源支持 `Depth: 0` 和 `Depth: infinity`；递归目录复制在仅 post-create 持久化失败时返回成功并附带 `Warning` |
| `PROPPATCH` | 简化 | 解析请求并返回 `207 Multi-Status`，属性修改返回 `403 Forbidden` |
| `LOCK` | 简化 | 返回虚拟锁 token；支持 `Depth: 0` 和 `Depth: infinity`；一小时过期 |
| `UNLOCK` | 简化 | 需要匹配 `Lock-Token`；过期锁会自动清理 |

### 不支持的方法

不支持的方法返回 `405 Method Not Allowed`，并在 `Allow` 响应头中列出当前作用域可用的方法。只读挂载和只读用户仅列出 `OPTIONS`、`GET`、`HEAD` 和 `PROPFIND`。

| 方法 | 状态 | 说明 |
| --- | --- | --- |
| `ACL` | 不支持 | RFC 3744 扩展 |
| `SEARCH` | 不支持 | RFC 5323 扩展 |

## 兼容性矩阵

状态说明：

- 已验证：已有自动化或真实客户端测试覆盖。
- 预期可用：根据标准 WebDAV 行为应可工作，但仍需要真实客户端确认。
- 需要配置：需要操作系统设置，或验证数据仍有限。

当前自动化覆盖：

- `OPTIONS`、`MKCOL`、`PUT`、`PROPFIND`、`COPY` 和 `MOVE`；
- 条件请求、Range/ETag 和 LOCK/UNLOCK 行为；
- 同源 `Destination` 解析和锁 `If` URI 解析；
- `scripts/webdav-client-smoke.sh` 可对已运行服务执行独立 curl 协议 smoke，覆盖 `OPTIONS`、`MKCOL`、`PUT`、`PROPFIND`、`GET`、`HEAD`、`COPY`、`MOVE` 和 `DELETE`，并验证 COPY/MOVE 后内容一致性和 URL 编码空格路径的读写删除；
- 设置 `RUN_RCLONE_WEBDAV=1` 后，低层 E2E 会在已安装 `rclone` 的环境中执行 WebDAV 客户端上传、下载、移动/重命名、列出和清理 smoke。

下表仍用于跟踪桌面、移动和媒体客户端的剩余真实客户端验证工作。

### Linux

| 客户端 | 版本 | 状态 | 说明 |
| --- | --- | --- | --- |
| Nautilus / GNOME Files | 45+ | 预期可用 | 使用 GVfs DAV 支持 |
| Dolphin | 23+ | 预期可用 | 内置 WebDAV 支持 |
| davfs2 | 1.6+ | 预期可用 | 挂载为本地目录 |
| rclone | 1.60+ | 已验证 | 可选 `RUN_RCLONE_WEBDAV=1` E2E 覆盖上传、下载、移动/重命名、列出和清理 |

### macOS

| 客户端 | 版本 | 状态 | 说明 |
| --- | --- | --- | --- |
| Finder | macOS 12+ | 预期可用 | 使用 **前往** -> **连接服务器** |
| Transmit | 5+ | 预期可用 | 适合大批量传输 |
| Cyberduck | 8+ | 预期可用 | 开源文件浏览器 |
| rclone | 1.60+ | 预期可用 | 支持 CLI 和 mount |

### Windows

| 客户端 | 版本 | 状态 | 说明 |
| --- | --- | --- | --- |
| File Explorer | Windows 10/11 | 需要配置 | 需要 WebClient 服务；HTTP Basic Auth 需要注册表设置 |
| WinSCP | 6+ | 预期可用 | 推荐的 Windows 客户端 |
| Cyberduck | 8+ | 预期可用 | 开源文件浏览器 |
| rclone | 1.60+ | 预期可用 | 可挂载为驱动器 |
| NetDrive | 3+ | 需要验证 | 商业客户端；不同行为可能随版本变化 |

Windows File Explorer 已知注意事项：

- 强烈建议使用 HTTPS。
- 大文件传输可能超时。
- 第三方客户端通常提供更好的体验。

### iOS / iPadOS

| 客户端 | 版本 | 状态 | 说明 |
| --- | --- | --- | --- |
| Files | iOS 15+ | 预期可用 | 原生 WebDAV 支持 |
| Documents by Readdle | 8+ | 预期可用 | 功能较完整的文件管理器 |
| FileBrowser | 14+ | 需要验证 | 专业文件管理器 |

### Android

| 客户端 | 版本 | 状态 | 说明 |
| --- | --- | --- | --- |
| Solid Explorer | 2.8+ | 预期可用 | 推荐的 Android 客户端 |
| Total Commander + WebDAV plugin | - | 需要验证 | 长期维护的文件管理器 |
| FolderSync | 5+ | 需要验证 | 同步客户端 |
| rclone | - | 预期可用 | 可在 Termux 中运行 |

### 媒体播放器

| 客户端 | 平台 | 状态 | 说明 |
| --- | --- | --- | --- |
| Infuse | iOS/tvOS/macOS | 需要验证 | 支持 WebDAV 源 |
| nPlayer | iOS/Android | 需要验证 | 需要验证拖动播放和字幕行为 |
| VLC | 跨平台 | 预期可用 | 需要验证 Range 请求和拖动播放 |
| Kodi | 跨平台 | 需要验证 | 需要配置 WebDAV 源 |

## 真实客户端验证标准

将矩阵中的客户端状态从“预期可用”或“需要验证”调整为“已验证”前，应保留可复核的验证记录。记录可以来自本地测试日志、Issue、发布验证记录或维护者测试笔记。

最低验证流程：

1. 先运行 curl 协议 smoke：`scripts/webdav-client-smoke.sh`。可安装 `rclone` 的环境还应设置 `RUN_RCLONE_WEBDAV=1` 执行可选 E2E。
2. 记录客户端名称和版本、操作系统和版本、WebDAV 认证方式、URL 前缀、反向代理、TLS 和网络位置。
3. 验证连接或挂载、浏览目录、上传、下载、重命名、删除和重新连接后的持久可见性。
4. 对大文件传输、媒体拖动、离线同步或后台同步类客户端，记录对应操作是否通过或受限。
5. 若结果来自外部用户，优先要求使用 WebDAV 兼容性报告表单，并附上可复现步骤和诊断包。

## 已知限制

### 虚拟锁

MnemoNAS 会返回 WebDAV lock token 以兼容客户端，但它不是完整的协作锁系统。

- 锁支持 `Depth: 0` 和 `Depth: infinity`。
- 缺失 `Depth` 时按 `infinity` 处理。
- 锁定不存在的资源返回 `404 Not Found`。
- 刷新请求要求空请求体和匹配的 lock token。
- `UNLOCK` 要求提供 `Lock-Token` 请求头。
- 过期时间当前为一小时。
- WebDAV 运行时配置重建会保留未过期 DAV 锁的 token、存储路径、深度和到期时间。替代 handler 会立即发布，后续请求直接使用更新后的前缀、认证、访问规则、配额和只读设置；在途请求继续由旧代 handler 完成，旧代活动引用归零后再异步关闭。关闭后的 handler 实例不会再次发布；生产 handler 仅以轻量、进程唯一的生命周期 ID 记录该约束，不保留旧代的密码、访问规则或目录属性缓存。所有存活代际共用路径锁表、DAV 锁表、锁过期清理循环和配额协调器。外部重命名或删除会使所有存活代际的目录属性缓存失效，但共享 DAV 锁表只变更一次；删除失败时，各独立运行态返回的锁回滚按相反顺序执行。
- 锁不会跨进程持久化。

Office 类应用在多个客户端编辑同一文件时，仍可能报告冲突。

### 大文件上传

- WebDAV PUT 在每次读取请求体前按 `server.read_timeout` 刷新连接读取截止期；WebDAV 响应在每次写入前按 `server.write_timeout` 刷新连接写入截止期。
- PUT 在路径锁内完成初始权限、父目录、DAV 锁、条件头和目标身份检查，然后在网络请求体读取期间释放路径锁。请求体到达 EOF（包括最后一次读取同时返回数据和 EOF）后，服务端重新取得路径锁，并在提交前重新认证用户；服务端根据该请求准入时的访问规则快照，复核当前身份、角色和主目录对应的写权限，同时复核父目录和当前 DAV 锁。复核失败会丢弃暂存内容。该边界允许慢上传期间的其他路径操作继续执行，同时防止在最终提交窗口绕过锁状态变化。
- Web/API 和 WebDAV 共用进程级配额预留及写入提交门。已知长度 PUT 按覆盖差量预留；未知长度 PUT 最多按 64 MiB 分批增加预留，不会预先独占该配额范围的全部剩余容量。每批增长重新计算当前用量和其他未完成预留；覆盖 MOVE 还会预留提交期间仍位于逻辑目录内的旧目标备份空间。PUT 在请求体到达 EOF 后取得提交门，并持有到存储事务返回。成功、失败、条件冲突或取消后均先释放预留，再释放提交门。准入时的配额规则快照用于该在途请求，热更新应用于后续请求。
- PUT 的直接父目录不存在或不是目录时返回 `409 Conflict`，且在读取请求体前停止；客户端应先使用 `MKCOL` 创建目录。并发写入暂存槽占满时返回 `503 Service Unavailable` 和 `Retry-After: 1`；主机可用空间不足以安全暂存事务时返回 `507 Insufficient Storage`，且不返回 `Retry-After`。目标位于 `files/` 下的嵌套挂载点、挂载表无法验证，或跨根重命名不受支持时，PUT 返回 `503 Service Unavailable` 和明确的原子写入布局错误；在请求体读取前识别的布局错误不会读取内容或修改目标。服务端在读取请求体前求值写入条件，并把当时的目标删除身份绑定到最终发布。发布阶段对已有目标执行原子交换，对新目标执行禁止覆盖发布；读取期间目标被修改或新目标在发布前出现时，带 `If-Match`、`If-None-Match` 或 `If-Unmodified-Since` 的请求返回 `412 Precondition Failed`，无条件请求返回 `409 Conflict`，不会覆盖或移除较新的目标。服务端在可见发布前持久化 `prepared` 决策，只有 `committed` 决策执行前滚；其他决策回滚。无法确认日志或事务参与者终态时会保留跨重启恢复证据、启用恢复门禁并返回 `503 Service Unavailable`。
- 大于 10GB 的文件更适合使用 rclone 或其他稳健客户端处理。
- 反向代理必须允许大请求体和较长上传超时。

### 深层目录

`PROPFIND Depth: infinity` 在非常大的目录树上可能较慢。客户端应优先使用 `Depth: 1` 逐级浏览。

## 性能说明

- `PROPFIND` 响应可能会短时间缓存。
- Range 请求支持断点续传和媒体拖动。
- ETag 支持可帮助客户端避免重复下载。
- 去重内容可以复用已有 CAS 对象，但客户端仍需要发送上传请求。

## 配置示例

### rclone 示例

```ini
[mnemonas]
type = webdav
url = http://localhost:8080/dav
vendor = other
user = <mnemonas-or-webdav-username>
pass = <obscured-mnemonas-or-webdav-password>
```

使用以下命令生成 `pass`：

```bash
rclone obscure <mnemonas-or-webdav-password>
```

### curl 协议 smoke

```bash
WEBDAV_URL=http://localhost:8080/dav \
MNEMONAS_WEBDAV_USERNAME="<mnemonas-or-webdav-username>" \
MNEMONAS_WEBDAV_PASSWORD="<mnemonas-or-webdav-password>" \
./scripts/webdav-client-smoke.sh
```

该脚本会创建临时集合，验证基础读写、URL 编码空格路径、复制、移动、移动后内容一致性和删除操作，然后清理临时数据。`WEBDAV_URL` 必须是不包含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠或编码反斜杠，也不包含 `.`/`..` 路径段的 HTTP(S) WebDAV 根 URL；凭据应通过环境变量传入。每次 curl 请求默认使用 `CURL_CONNECT_TIMEOUT=10` 和 `CURL_MAX_TIME=30`，高延迟网络可通过环境变量调大。它用于协议级回归检查，不替代 Finder、Windows File Explorer、移动端文件管理器或媒体播放器的真实客户端验证。

### davfs2 示例

```text
# /etc/davfs2/secrets
http://localhost:8080/dav <mnemonas-or-webdav-username> <mnemonas-or-webdav-password>
```

```bash
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
```

## 报告兼容性问题

使用 [WebDAV 兼容性报告表单](../.github/ISSUE_TEMPLATE/webdav_compatibility.yml) 提交客户端兼容性结果。报告应包含：

- 客户端名称和版本。
- 操作系统和版本。
- WebDAV 认证方式、访问路径、反向代理和 TLS 背景。
- 已测试的操作，例如连接、浏览、上传、下载、重命名、删除、大文件传输、媒体拖动或离线同步。
- 复现步骤。
- 可行时附上从 Web UI 导出的诊断包。
