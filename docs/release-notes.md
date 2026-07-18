# 开发阶段变更记录（未发布）

[English](release-notes.en.md) | 简体中文

本文档记录当前未发布开发分支的变更和验证证据，并保留未来首次公开发布的准备流程。它不是发布公告；MnemoNAS 尚未发布任何可用版本。

## 摘要

当前开发分支重点强化 MnemoNAS 的稳定性、公开访问安全边界、部署可验证性和文档可维护性。硬化变更按风险面拆分；验证结果只适用于下文记录的具体目标与源码树，不表示当前所有未发布变更均已验证，也不表示已经形成可用版本。

## 主要变化

- 新增 `client/` Flutter 跨平台客户端工程，以 Android 作为首个可用平台目标。当前源码已接入服务器连接与校验、Bearer 会话、单次 refresh token 轮换、安全会话存储、首页概览、文件浏览和基本文件操作、版本历史与管理员恢复、可恢复上传下载、Android Storage Access Framework 导入与导出、账户管理及 Issue 反馈入口；客户端门禁和真实 `nasd` API 文件生命周期冒烟已通过。本轮本地人工验收在 Android 16 真机完成基本安装、登录、系统返回层级、进程重启会话恢复和 Debug 同签名升级；该摘要没有对应的远端设备农场记录或可下载设备日志。受控 Release 候选包、完整 Android 设备/故障矩阵、后台传输，以及 Linux 和 Windows 原生构建验证仍未完成，当前没有可用客户端版本。
- Android release 构建增加失败关闭签名门禁。Release 保留 `com.mnemonas.app`，Debug 与 Profile 使用独立包名、版本后缀和显示名称。release APK/AAB 必须从源码 checkout 外读取签名配置和密钥库，并通过私钥别名、证书有效期、Android Debug 证书拒绝及证书 SHA-256 指纹检查。门禁禁止 `android.injected.signing.*` 覆盖，并根据 Gradle 解析后的任务图阻止任务缩写或排除独立校验任务的绕过。临时 PKCS12 测试密钥覆盖缺失或无效配置及两类绕过回归，生成的 APK 与 AAB 均执行实际验签；测试密钥和 release 测试产物会自动清理，CI 运行同一脚本。该门禁不代表已建立正式密钥托管或生成可分发候选包。
- Flutter 文件页补齐 Android 系统返回层级：存在多选时先取消选择，位于子目录时返回规范父目录，根目录才允许退出路由。目录列表响应还必须返回与请求一致的规范路径，异常路径会作为无效响应被拒绝。本轮本地人工验收分别在 API 35 模拟器和 OPPO PLP120 Android 16（API 36）真机执行多选返回、深层目录逐级返回、强制结束进程后的会话恢复，以及 `versionCode 1 → 2` Debug 覆盖升级后的会话保持；临时环境和测试应用已在验证后清理。
- Flutter 客户端补齐目录新鲜度和文件修改单飞边界。初次加载失败、同目录刷新和缓存内容过期使用不同状态；可选统计接口失败不会把文件目录标记为过期。文件修改由控制器租约和界面即时防抖共同限制为单飞；已确认操作不会因目录刷新失败改写为失败，未确认结果会要求先刷新核对，迟到刷新也不会把界面拉回旧目录。应用进入 `hidden`、`paused` 或 `detached` 时，传输中心内的持久任务会保留私有载荷和断点并进入暂停或等待重新选择保存位置状态；临时文件预览、历史版本预览或下载及 SAF 上传准备会取消并清理局部文件。返回前台不会自动继续。
- Flutter 客户端新增版本历史面板。入口面向具备规范化路径、具体读取权限和版本化标记的普通文件，不要求目录条目预先携带内容哈希；版本 API 建立当前 BLAKE3 内容身份，目录条目已携带身份时还会核对一致性。列表校验连续版本号、哈希、大小、时间与有界说明；当前版本卡只展示身份和元数据，历史版本可预览或下载，当前文件操作仍从文件页执行。历史下载要求 strong ETag 与所选哈希精确相等且长度一致，Android 在网络完成后才打开系统保存位置。管理员恢复前重新读取父目录和历史，核对初次版本响应建立的当前身份与所选版本，并复用文件修改单飞租约；恢复 `POST` 不自动重放，连接中断、超时、不可解析响应和结构化 `5xx` 均按“结果待确认”处理。已确认恢复但历史刷新失败时，弹层会停止后续恢复并要求关闭后重新核对。
- Flutter 客户端新增前台持久传输账本。任务按服务端与用户隔离，记录稳定私有载荷、可靠偏移、目标和阶段，但不记录认证令牌。下载恢复必须同时满足服务端对象身份、`206 Partial Content`、精确 `Content-Range` 和总大小。Android 下载先完成私有载荷，再以临时写授权选择保存位置；网络失败不会提前创建空文档。Android 上传选择改用 URI-only `ACTION_OPEN_DOCUMENT`，以临时读取授权按文件流式复制到应用私有目录，避免把完整多选内容载入 Java heap；已知大小超过 10 GiB 时会在复制前拒绝，大小未知时原生复制层仍执行 10 GiB 硬限制，并在超限时停止写入和清理局部文件。准备阶段单独显示进度。任务专属载荷完成 SHA-256 校验和账本持久化后会释放临时导入文件。
- Flutter 客户端将传输记录拆分为独立传输中心，按“进行中”“需要处理”和“最近记录”分组。暂停或等待处理的任务可继续执行，完成私有下载后可继续选择保存位置；暂停或失败任务只有在明确确认后才会取消相关服务端会话（如有）并删除本地可恢复进度，“结果待确认”记录也要求先核对目标位置。任务卡片补充了文件名级操作标签、进度语义和窄屏、大字体及桌面宽度适配。
- 服务端新增所有者隔离的持久上传会话，提供请求 ID 幂等创建、按请求 ID 只读查询、状态查询、8 MiB 顺序分块、服务端可靠偏移、分块 SHA-256、完整载荷 BLAKE3、条件提交、取消、72 小时期限清理及崩溃终态对账。非末分块至少为 1 MiB；可靠暂存默认限制为每个账号 20 GiB、进程内 100 GiB，写入前还会保留配置的主机最小可用空间。客户端在首次创建请求前持久记录尝试状态；响应丢失且尚未取得会话 ID 时，只按客户端请求 ID 查询已有会话，不重发创建请求。客户端在其他响应中断或重启后先查询会话，再从服务端偏移继续；上传成功时只有取得 `committed` 终态后才删除私有载荷，启动时会重试清理已确认完成或取消任务的残留载荷。会话缺失或过期时，任务进入“结果待确认”，不会按新的目标快照自动重建并覆盖较新文件。服务端存储恢复门禁未解除时保持 `committing` 和暂存载荷，不根据尚未对账的可见目标提前确认提交。旧 `POST /api/v1/files/{path}` 仍只接受完整文件请求并拒绝 `Content-Range`。
- Flutter 客户端会话存储改用 revision/CAS 和原子 `takeAndClear`。Refresh token 在发送前先从持久存储中移除；退出登录先清除本机会话，再尽力撤销服务端会话。旧客户端、旧设备地址、旧账号和被后发请求替代的目录响应不能再写入当前状态。控制器会关闭旧 `ApiClient`，按 context epoch 隔离服务器与账号，并对同一上下文的目录请求采用后发请求优先。确定性测试覆盖延迟 refresh 跨越退出、旧 refresh 失败与新登录并发、延迟登录、旧设备目录响应、目录反序完成和控制器释放后的迟到响应。
- Flutter 客户端以“回收站”替换尚无可用索引的“相册”主导航，支持查看逐项到期时间和当前删除策略、恢复到原路径或自定义路径，以及按确认时冻结的精确 ID 集合永久删除。客户端严格校验回收站响应的 ID、路径、时间、数量和删除/保留/跳过分区；请求终态无法确认时会重新读取列表，但不会仅凭项目消失推断恢复或永久删除成功，并在再次明确刷新前暂停后续恢复与永久删除。只读 `guest` 账户不显示修改入口，破坏性控件使用不小于 48 dp 的触控目标。
- Flutter 客户端新增有界文件名搜索。查询按 100 个 Unicode 码点限制，每次最多显示 100 项；新查询、清空、关闭搜索、退出登录和切换设备会取消旧请求，并以请求序列隔离迟到响应。同词刷新失败时保留上次结果并明确标记；打开文件前重新读取父目录，目录结果也在进入前重新读取，搜索快照不直接用于修改操作。真实 `nasd` 冒烟覆盖上传、重命名、移入回收站、恢复和永久清理后的搜索可见性。服务端同时修正多字节查询按字节计数和搜索根目录可能作为普通结果返回的问题。
- 流式上传、WebDAV PUT 和版本恢复现在使用跨命名空间、CAS 与 SQLite 的持久写入决策日志。启动恢复只前滚 `committed` 事务，其余事务回滚；无法确认终态时保留证据并阻断后续写入。写入目标的直接父目录必须先通过目录创建操作建立。
- 加强路径、归档下载、WebDAV、公开分享、工作区、CAS 和备份恢复相关边界检查，覆盖符号链接、路径穿越、百分号编码点段、编码后的查询或片段标记、百分号编码敏感参数名、控制字符和回滚错误情况。
- 升级 `golang.org/x/image` 到 `v0.43.0`，修复缩略图解码路径命中的 TIFF/WebP 依赖安全告警；同步刷新间接 `golang.org/x/text` 版本。
- 将 Rust 数据面与 protobuf 生成工具锁定的 `anyhow` 升级到 `1.0.103`，修复 `RUSTSEC-2026-0190` 所述的 `Error::downcast_mut()` 内存安全问题。
- 完善认证、用户、主目录、目录配额、目录访问规则、分享策略和会话安全默认值的后端与前端覆盖。
- 新增面向全部认证用户的“账户安全”页面，可从用户菜单修改本人密码；管理员设置页提供同一入口。自助改密与强制改密共用校验和错误处理，成功后会退出该账户在所有设备上的登录并要求重新登录；客户端能够观察到请求终态无法确认时，会清除本地认证状态，避免继续使用可能已撤销的会话。`POST /api/v1/auth/password` 请求现在必须提供与认证上下文一致的 `expected_user_id`；旧调用方缺少该字段时会收到 `400 MISSING_EXPECTED_USER_ID`。
- 文件页的单项与批量删除共用同一流程会话，准备、确认和提交阶段相互排斥，避免不同入口并发打开或提交删除。删除目标确认期间会显示可取消的进度提示；取消会立即中止请求、忽略迟到响应、保留当前选择，并把焦点恢复到原操作入口。原入口已移除时，焦点会回退到稳定的文件区域。目录切换或页面卸载只会使旧会话失效，不会把焦点移回旧目录。
- 文件页面会按当前策略明确区分“移入回收站”和“永久删除”，并在策略未知时停用删除而不影响浏览。文件列表为实际条目返回对象身份令牌；删除确认通过 `POST /api/v1/files-delete-intents` 提交所选条目的观察身份，并原子取得完整策略与目标树令牌。同一路径在点选后被新文件或目录替换时，即使类型、大小和修改时间相同，服务端也会拒绝旧身份。目标令牌使用 v3 分层 SHA-256 Merkle 表示，绑定规范化根路径、完整路径层级、对象身份、类型、权限模式、大小、纳秒级修改时间和普通文件内容摘要。删除确认先在存储读锁内采样完整策略与变更纪元，再在锁外扫描并哈希请求中的目标树，最后复核变更纪元；纪元变化时会重试。最终目标令牌仍为 64 字符小写十六进制不透明值；REST 不公开变更纪元。`DELETE /api/v1/files/{path}` 必须同时提交模式、策略令牌和目标令牌。原子捕获前发现策略或目标变化时返回 `409 DELETE_POLICY_CHANGED` 或 `409 DELETE_TARGET_CHANGED`，且不提交业务状态。捕获后，两种删除模式都使用禁止覆盖的源端暂存和已验证内容证据。永久删除使用句柄锚定、权限不宽于 `0700` 的隔离目录；逻辑提交后清理未完成时返回成功并附带 `delete cleanup incomplete`。实时“移入回收站”删除和“从回收站恢复”使用 `.transactions` 下的 `prepared`、`copying`、`ready`、`committed` 和 `completed` 检查点、与业务元数据原子提交的 SQLite `trash_operations` 发件箱行，以及分享与收藏的操作级回执和原删除操作所有权。`copying` 在载荷复制前持久化已同步的操作专属容器身份；权限为 `0600` 的规范化 owner marker 会在此之前绑定 `prepared` 日志、操作角色、精确路径与持久身份，并在完成移除及目录同步后才允许复制。损坏、部分写入或不匹配的 marker 无法授权自动清理，恢复会 fail closed。删除完成后的显式分享或收藏变更会阻止旧恢复覆盖新意图。删除在移除源暂存对象前复核规范回收站副本，恢复在移除回收站源前复核目标。日志、参与者、回执、发件箱、源端或目标端发生硬失败时返回 `500`，保留恢复证据，并阻止后续存储变更；即使持久提交已经完成，也不会降级为清理警告。持久化不完整的失败返回成功并附带 `workspace mutation persistence incomplete`；终态日志清理无法确认时会保留或重建可恢复检查点并启用恢复门禁。达到 `completed` 且终态日志可靠清理后的容量清理失败使用 `trash delete cleanup incomplete`。恢复 `completed` 操作时只重试回执清理、发件箱确认和日志清理，不重放参与者 Apply。在无竞争且成功的单个普通文件删除中，实时回收站路径执行 12 次完整内容哈希加 1 次复制读取：当前文件 2 次、源暂存对象 4 次、回收站目标 6 次。永久删除路径执行 5 次完整内容哈希：当前文件 2 次、源暂存对象 3 次。竞争、失败处理或回滚可能增加读取次数。改名后的暂存路径被替换时，只有完成同步并通过身份与内容复核的恢复副本才会写入 `StagePath`；否则由 `InspectionPaths` 列出待人工检查的位置。存储锁与变更纪元只覆盖平台内操作；外部文件系统写入和并发挂载仍不受该串行边界约束。已位于回收站的项目在永久删除时使用 `.deleting` 下的 `prepared` 和 `committed` 记录；含恢复载荷的项目会在物理变更前只读预检参与者，并在清理后按原删除操作 ID 精确移除所有权，持久化失败会保留 `committed` 证据并启用恢复门禁。启动时先初始化分享与收藏参与者存储及钩子，再依次恢复 `.deleting` 下的永久删除和 `.transactions` 下的实时转移；两类恢复都在工作区暂存清理、后台任务和网络监听器启动前完成。日志、发件箱、参与者、内容或元数据证据无法互证时，恢复以 fail-closed 方式终止可写启动。保留清理任务会同时清理过期版本与到期回收站项目，容量上限可能使回收站项目在持久化到期时间前被清理。
- 清空回收站改为通过 `POST /api/v1/trash/empty` 提交已确认的精确 ID 集合；原 `DELETE /api/v1/trash` 端点已移除。界面在确认对话框打开时冻结项目、ID、数量和大小，超过 1000 项时按冻结集合分批提交。每个有效响应中的删除、保留和跳过结果都会被保留；若后续批次失败或服务端返回部分结果，界面会刷新回收站，并仅将原冻结集合中仍存在的项目保留为重试范围。刷新失败且存在结果未知的请求时，界面会阻止再次删除，直至核对成功；已确认删除或跳过的项目仍会从本地列表精确移除。对话框打开后新增的项目不会进入核对或重试范围。服务端在同一存储写锁内载入当前项目、预检全部现存已选项目的访问规则，再按请求顺序永久删除；未选择、新增或已缺失的项目不会被误删。响应以删除、保留和跳过数组完整划分请求 ID，并将部分结果与提交后的物理清理警告分开表示。
- 本地备份 manifest 升级为 v2，分别绑定普通文件和 `data/` 目录的精确拓扑、空目录与 POSIX `rwx` 权限位。setuid、setgid、sticky bit 等特殊权限位会被拒绝；ACL、扩展属性和所有者身份不在此证据范围内。配置结构目录固定使用 `0700`。本地恢复、恢复演练和 `restore-verify` 均以受信 manifest 为目录真值，不从快照当前目录树推断；安装前的完整树校验拒绝额外或缺失条目、权限漂移和大小写折叠冲突。恢复记录按 manifest 原始字节摘要绑定同一次恢复，证据仅供内部校验，不作为公共 `RestoreResult` 字段返回。显式恢复还会复核同级暂存目录身份；Unix 上要求暂存目录及其祖先不可被非可信账号替换。v1 快照不会用于恢复或自动清理，并从 v2 保留计数中排除；当前快照和恢复历史仍引用的快照会固定保留。v1 manifest 或缺少受信证据时，需要先完成新的本地备份。该变化仅适用于 `local` 任务；`restic` 与 `rclone` 保持各自后端语义。原生 Windows 无法保真保存所需的 POSIX 权限位，因此会拒绝 v2 本地操作。
- 认证下载会先同步短期下载会话并执行单字节有界错误探测；access cookie 过期时会复用现有 refresh 与跨 Tab 协调流程，同一认证代际和取消信号的并发同步会合并。允许 download Cookie 的请求要求同时存在的 access 与 download Cookie 原始值完全一致，避免失效的当前 access Cookie 回退到其他账号或其他会话遗留的有效 download Cookie。公开分享下载通过同源 POST 签发不超过 256 个字符、URL-safe、带签名且与目标和分享状态绑定的短期票据。两类流程最终都交由浏览器原生导航流式下载，不再把完整文件或 ZIP 缓存为前端 Blob。下载提交使用随机命名、带 sandbox 且无 `src` 的同源隐藏导航上下文；实际 URL 仅短暂存在于设置 `referrerpolicy="no-referrer"` 且不带 `noopener` 或 `noreferrer` 的目标链接，避免改变命名目标解析。附件响应使用 `sandbox allow-downloads`、`frame-ancestors 'self'` 和 `X-Frame-Options: SAMEORIGIN`，只允许应用自身嵌入；内联预览继续禁止嵌入。成功提交的导航上下文在页面生命周期结束时清理，非下载错误响应加载完成后立即清理，并以 64 个活动或待提交上下文限制单页资源占用；达到上限后，新提交会在网络预检或公开票据签发前被拒绝并提示刷新页面。浏览器/API 下载不受通用 30 分钟请求上下文总时限约束，并会在响应头、每次写入和刷新前按 `server.write_timeout` 推进连接写入截止期；持续传输可超过该时长，单次停滞写入仍会超时。公开票据请求必须包含由 Web Crypto 生成并持久复用的 32 字节 `client_nonce`；服务端使用独立 HMAC 域，根据分享 ID 与该 nonce 派生稳定的 128 位 binder ID 和 256 位 binder 值，而每张票据仍使用独立随机 ID。票据只保存 binder ID 与 binder 值摘要；同一分享和 nonce 会刷新同一个 `Path=/`、`HttpOnly`、`SameSite=Strict` cookie，不同分享派生不同 cookie。cookie 名以 binder ID 的 32 字符小写十六进制形式结尾，HTTPS 还使用 `Secure` 和 `__Host-` 前缀。下载 GET 同时校验票据查询参数与唯一匹配的 binder。签发端点对结构合法 binder cookie 采用 32 个的请求局部软限制：正确的既有目标 binder 在达到或超过限制后仍可刷新，新增、伪造或重复目标 binder 会返回 `429 Too Many Requests`、`Retry-After: 1` 和 `DOWNLOAD_TICKET_RATE_LIMITED`；容量为 4 的进程级非阻塞签发门限覆盖 ZIP 预检，并限制不同新 nonce 的并发旧快照超出软上限。`max_access` 按成功签发的逻辑下载会话计数；目录浏览、密码验证和同一票据的 Range 或断点续传不重复计数。密码失败限流按分享和客户端地址隔离，同一分桶只允许一次 bcrypt，进程内 bcrypt 并发上限为 8；状态上限为每个分享 128 个、进程内总计 4096 个，活跃失败、锁定和校验中状态不会被驱逐，超过 72 字节的密码不会占用状态。普通文件下载和 ZIP 归档只读取已验证的普通文件快照，拒绝管道、套接字和设备文件；认证 ZIP 与公开 ZIP 还会校验每个快照的实际读取字节数，避免把截断内容表示为完整归档。认证 ZIP 的探测与完整写入另共用容量为 4 的进程级全局并发门限，满载时在访问文件系统前返回 `429`、`Retry-After: 1` 和 `ARCHIVE_DOWNLOAD_RATE_LIMITED`；Web UI 一次批量下载最多提交 20 项且最多包含一个文件夹。公开 ZIP 的签发预检与实际 ZIP 流另共用容量为 4 的进程级全局并发门限；门限占满时使用相同的 `429` 响应。
- 加固邮件告警通知出口，消息头和 SMTP envelope 会清理控制字符，降低内部调用或后续扩展绕过配置校验后的头部注入风险。
- 提升 Web 可见质量，核心页面、公开入口、移动端布局、基础可访问性、运行时错误、失败请求和破碎可见文本已纳入 Playwright 扫描。首页首次部署检查和登录页会基于 setup 状态提示认证关闭、分享启用且认证关闭、WebDAV 匿名访问和 `allow_unsafe_no_auth` 开启的部署风险。
- 加固 systemd、Docker、反向代理、公网访问模板、doctor、公网域名就绪校验、release package 和 release artifact 验证路径；Docker preflight 会在 Compose 检查前拒绝空值、以 `-` 开头、包含空白或控制字符、URL 形态、无效 `sha256` digest 或不兼容 Docker tag 的 `MNEMONAS_IMAGE`，且 URL 形态诊断不回显凭据、query 或 fragment；Docker quickstart、preflight 和容器入口还会拒绝配置中包含父目录段或控制字符的 `auth.users_file` 容器路径，避免将 `/data/../...` 映射为宿主数据目录外的初始密码读取路径；Docker smoke 会在启动容器前拒绝以 `-` 开头或包含空白/控制字符的镜像引用；容器 healthcheck 对无效目标 URL 的诊断日志只输出脱敏后的 URL 形状，不写入嵌入凭据、原始查询字符串或 fragment；反向代理安装脚本对无效 `MNEMONAS_UPSTREAM_HOST` 只输出主机格式约束，不回显原始 host 值或误粘贴 URL 中的凭据、query、fragment；`mnemonas-doctor --public-domain` 对无效 `share.base_url` 诊断只输出脱敏 URL 形状，不回显误配置中的凭据、query 或 fragment；公网 go-live smoke 和 doctor 会拒绝 `localhost`、IP 地址和全数字四段主机名，给手动端口复核命令设置连接和总耗时上限，并拒绝空白的自定义后端目标列表和歧义目标路径，避免跳过端口暴露检查或生成不明确的后端探测 URL；公网 go-live smoke 对无效自定义后端目标和错误 HTTP 跳转只输出脱敏后的目标形状，不回显 query、fragment、userinfo 或控制字符路径内容；Release workflow 会在创建 GitHub Release 前校验归档、checksums、必需目标集合、下载目录未知条目、归档条目类型、重复条目、控制字符路径、空白字符路径、归档成员控制字符路径、归档成员空白字符路径、反斜杠路径、歧义路径、GHCR 仓库名和已推送的容器镜像标签；release artifact verifier 支持通过 `--` 传入以 `-` 开头的本地产物目录，并对下载目录、checksum 清单和归档成员中的控制字符路径使用 shell-safe 诊断表示，避免发布后核验路径被 shell 内建命令按选项解释或把原始控制字符写入验收日志；发布后统一核验入口会把以 `-` 开头的显式 artifact 目录规范化为本地路径，并在下载前拒绝非法仓库名。
- systemd 安装和卸载脚本在拒绝包含控制字符的路径、地址、端口或账号参数时，会使用 shell-safe 诊断表示，避免失败日志写入原始控制字符或形成多行注入。
- 基准测试、E2E、故障注入脚本、反向代理安装向导和双语反向代理文档的 WebDAV PROPFIND 示例均通过临时 curl config 传递 WebDAV Basic Auth 凭据，避免密码出现在 `curl` 命令参数；开发文档和反向代理文档均不再保留直接把 WebDAV 密码放入 `curl -u` 的手动示例，并由脚本测试和文档契约覆盖。
- 公网 go-live smoke 会在 TCP 探测中按 `timeout`、`gtimeout` 顺序自动选择 GNU timeout 兼容命令，并支持用 `TIMEOUT_BIN` 指定兼容替代命令。
- Release tag 会在产物构建前校验，必须使用 `vMAJOR.MINOR.PATCH` 或 `v1.2.3-rc.1` 这类语义化预发布形式，并且去掉 `v` 前缀后的 Docker 镜像 tag 长度不能超过 128 个字符；发布后 artifact verifier 会复用同一版本校验逻辑，对显式或归档名推断出的版本应用同一约束。
- 新增可复跑的 WebDAV curl 协议 smoke，可对已运行服务验证基础读写、URL 编码空格路径、复制、移动和删除操作；脚本会提前拒绝含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠、编码反斜杠或 `.`/`..` 路径段的 `WEBDAV_URL`，并拒绝非 `0/1` 的 `CURL_INSECURE`，相关契约通过脚本门禁覆盖。
- 新增可复跑的备份恢复演练 smoke 入口，可对已运行服务按显式备份任务 ID 执行任务列表读取、单任务读取、立即备份、保留策略检查、恢复演练和恢复报告下载；脚本不创建或删除备份任务，并提前拒绝含空白、query、fragment、内嵌凭据、反斜杠、编码斜杠/反斜杠、空路径段或点段的 API URL。
- 新增发布后上线总核验入口 `scripts/release-go-live-check.sh`，按顺序执行发布就绪摘要、GitHub Release/GHCR 产物核验、公网 `mnemonas-doctor --public-domain`、外部网络 go-live smoke，以及备份恢复演练 smoke；脚本会在任何 helper 启动前校验 release tag、仓库名、公网域名、备份演练 API URL、任务 ID 和可选 cookie 文件，把大写或单个尾点域名规范化后传给公网检查，并拒绝重复尾点域名；备份演练需要显式 API URL 和任务 ID，或显式跳过并在发布记录中保留该事实。
- 新增 WebDAV 兼容性报告表单，用于收集 Finder、Windows File Explorer、移动端文件管理器、媒体播放器和命令行客户端的验证结果或客户端特定失败。
- 维护页恢复完成后可复制恢复切换记录，内容包含恢复目标、只读校验、切换步骤、切换前确认和回滚清单；恢复报告会基于原始恢复目标匹配结果，在最近一次恢复已完成但匹配只读校验缺失、只读校验早于恢复完成、只读校验不属于当前恢复目标或只读校验状态不能作为当前目标证据时给出明确 findings，避免把陈旧、跨目标或不可用校验误读为当前恢复已验证；批量恢复结果会列出跨目录切换候选和冲突处置记录，并在可复制结果记录中写入任务名称、备份目标、保留策略状态、候选目录、只读校验复核结论、校验错误详情、冲突处置建议和配置文件保留要求，便于记录到工单或值班流程。
- 目录配额与目录权限集中到“用户管理 > 目录与访问”。该视图使用独立载荷保存目录策略，并提供用户权限矩阵、未保存规则预览和可复制权限复核记录；记录包含路径、用户读写判定、命中规则和相关分享影响，并保留后端持久化近期复核历史，服务端历史不可用时回退当前浏览器记录。存在未保存草稿时，应用内跳转、浏览器前进后退、登出和页面卸载均执行统一离开确认。
- 分享路径策略可按用户、用户组或角色限制允许创建和维护分享链接的认证调用方；管理员保留修复既有分享的管理权限。
- 分享、版本历史、回收站和维护页的关键处置入口会写入活动复核记录，覆盖分享停用、删除、重新启用、策略更新、版本恢复、回收站恢复和备份恢复执行结果；活动页复核历史在处置后会立即显示符合当前筛选的新记录，便于按记录时间复核误分享、误删和恢复处置结果。
- 收紧发布就绪摘要：记录的完整验证目标之后如出现已提交或未提交的非发布文档变更，`release-readiness` 默认失败，并要求刷新完整验证或显式草稿放行；草稿放行非发布文档变更时会输出 `validation-warning`，避免被误读为正式发布就绪。
- `release-readiness` 会把双语 Docker 部署说明视为完整验证目标后的发布文档，允许最终发布时按实际 tag、Release workflow 结果和产物名称刷新公开部署说明，同时继续拒绝普通文档或代码变更混入。
- `release-readiness` 现在要求四份 hardening 证据文档都存在，并且都记录同一个完整验证目标，避免发布前证据缺失被静默跳过。
- `release-readiness` 还会要求双语 hardening progress 台账在 `make release-readiness` 记录中写入同一个完整验证目标，避免完整验证证据刷新后发布就绪摘要仍停留在旧目标。
- `release-readiness` 还会检查双语 release notes 草稿记录当前完整验证目标，避免发布说明中的验证快照滞后。
- `release-readiness` 会要求双语 release notes 的发布后下载和 artifact verifier 示例使用 `<tag>` 占位，避免首次发布前把固定版本号写入可复制命令。
- `release-readiness` 会要求 `CHANGELOG.md` 和 `CHANGELOG.en.md` 的发布清单包含文档检查、依赖安全检查、Docker 构建烟测、所选发布 tag 校验和发布脚本回归命令，并保留开发阶段、尚无可用版本、不得承载真实数据的边界，避免未来发布核验遗漏关键本地门禁或数据安全限制。
- `release-readiness` 会要求 Dependabot 配置覆盖 Go、Rust 数据面、Rust proto 生成器、Web npm、GitHub Actions 和 Docker 依赖更新入口，避免发布分支丢失依赖维护基线。
- `release-readiness` 会要求 `.github/workflows/ci.yml` 和 `.github/workflows/release.yml` 保留关键 CI、E2E、Docker smoke、release tag 校验、release artifact 上传/下载、checksums 生成与发布、带版本和仓库绑定的 release artifact 校验、发布前镜像校验、release job 依赖和发布权限基线，避免核心自动化路径在发布前失效。
- `release-readiness` 会要求 `Makefile` 保留 `check`、`verify-changed`、`release-readiness`、`quick-check`、`security-check`、`docker-check` 和 `test-torture` 等核心本地门禁目标，避免 CI、发布清单和维护者文档引用的入口在发布前失效。
- `release-readiness` 会要求 `.github/workflows/torture.yml` 保留手动入口、定时入口、只读权限、`RUN_LIVE_FAULTS: '0'` 非破坏性开关和 `make test-torture` 执行入口，避免长期回归工作流在发布前失效。
- `release-readiness` 会要求关闭空白 Issue，并检查缺陷报告、使用问题、功能建议和 WebDAV 兼容性 Issue 表单保留敏感信息脱敏、诊断信息和安全影响提示，避免公开反馈入口绕过安全提示。
- `release-readiness` 会检查安全策略和支持说明保留私密漏洞报告入口、禁止公开漏洞细节、dataplane 端口不外露、依赖安全检查和公网直连限制等关键提示。
- `release-readiness` 会要求发布清单和双语 release notes 保留 `mnemonas-doctor --public-domain`、`scripts/public-go-live-smoke.sh`、`scripts/backup-restore-drill-smoke.sh`、`scripts/release-go-live-check.sh` 和 `cloud-firewall-checklist` 入口，避免公网部署环境复核、发布后上线总核验和恢复演练入口从最终发布流程中遗漏。
- `release-readiness` 会拒绝不是当前 HEAD 祖先的 base ref，避免用旁支范围生成误导性的发布就绪摘要。
- Go 测试入口现在保留 30 分钟包级超时，并将完整 race 测试的包级并发限制为 3；CI 使用相同参数且为 Go job 保留 60 分钟总时限，避免重负载包因资源竞争和详细日志开销误超时。
- 文档检查会拒绝 API 示例中可复制的 `?path=/...` 裸路径查询，要求恢复和收藏检查等 `path` 查询示例使用 `%2F...` 编码形式。
- 文档检查会要求双语 release notes 发布前验证清单中的 Playwright E2E、前端单测数量、Docker image 和 Docker smoke 端口与 hardening 审查摘要中的最新完整验证证据一致，避免验证证据刷新后发布说明局部数据滞后。
- 文档检查会要求双语 Docker 部署指南保留发布后 `verify-published-release.sh` 命令、版本和仓库参数、可选 artifact 目录、镜像 manifest 重试参数、`--skip-image-check`、`--keep-artifacts`、`--keep-published-artifacts`、空目录要求、dash-prefixed artifact 目录和仓库名下载前校验说明，避免发布后核验说明退化。
- 文档检查会要求安全加固指南的公网部署清单保留初始密码、WebDAV 认证、doctor、公网防火墙、匿名 WebDAV、直连后端和 dataplane 暴露等关键复核项。
- 文档检查会要求备份指南保留恢复演练命令、30 天演练提醒、失败分类、保留演练产物、恢复摘要导出和“未恢复过不算验证”的说明，避免恢复可用性文档退化。
- 存储和配置文档明确 FastCDC API 属于 Rust 数据面能力，当前版本历史仍使用整对象 CAS 快照，不按 CDC 分块引用计数；文档检查会拒绝回退为块级版本去重的过度承诺。
- 精简并同步中英文文档，补齐部署、配置、FAQ、路线图、安全、硬化进度和发布前审查入口。

## 发布产物

Release workflow 预期生成以下产物：

- Linux x86_64 / ARM64 二进制归档。
- macOS Intel / Apple Silicon 手动运行归档。
- `checksums.txt`。
- GHCR 容器镜像标签。

归档内应包含顶层目录、`nasd`、`dataplane`、Web UI 静态资源、systemd 安装/卸载脚本、doctor、Docker Compose 模板、`.env.example`、部署模板和中英文文档。归档内 `.env.example` 应预设同一 release tag 的 GHCR 镜像。

## 发布前验证

当前硬化分支已有以下验证证据；最终发布前应以最新 tag、Release workflow 结果和必要的环境验证为准：

最近本地完整验证快照：验证目标 `3f6a01524616`。`GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master` 通过 23 项变更感知门禁，覆盖 diff 与密钥扫描、workflow、脚本、工具链、依赖安全、示例配置、Docker 模板、protobuf 再生成稳定性、27 个 Go 包短测、Rust fmt/test/clippy、前端 lint/typecheck/覆盖率/构建、Playwright、Docker build/smoke 和双语文档。Go 关键包结果为 API 636.884 秒、storage 809.027 秒、WebDAV 27.551 秒；Rust 数据面 59 项测试通过；前端 102 个测试文件共 3652 项测试通过，语句、分支、函数和行覆盖率分别为 91.74%、86.67%、97.43% 和 92.27%；Playwright 409/409 通过，且无 retry、failure 或 skip。Docker image `sha256:1b005ff6838058f94341bfe25dd2ebe3c33f93ff47a4ce958cbe1a8982d5e1e7` 通过 Docker 自动分配的 loopback 地址 `http://127.0.0.1:32769` 的 health 与 frontend smoke。另行执行的隔离 `make fault-injection` 以 9 PASS、0 FAIL、0 SKIP 通过，覆盖崩溃写恢复、并发 ETag 冲突、真实版本恢复、CAS 损坏检测和 SQLite 元数据损坏隔离恢复。

- `GOTOOLCHAIN=local ./scripts/verify-changed.sh`
- `GOTOOLCHAIN=local timeout 90m ./scripts/verify-changed.sh --base master`
- `make scripts-check`
- `make docs-check`
- `make security-check NPM_AUDIT=1`
- `make docker-check`
- `make fault-injection`
- `make release-readiness`
- `sudo mnemonas-doctor --public-domain <domain>`
- `./scripts/public-go-live-smoke.sh <domain>`
- `./scripts/backup-restore-drill-smoke.sh`
- `./scripts/release-go-live-check.sh`
- `docs/cloud-firewall-checklist.md`
- `./scripts/check-release-tag.sh <tag>`
- `./scripts/test-release-tag.sh`
- `./scripts/test-release-package.sh`
- `./scripts/test-release-artifacts.sh`
- Public go-live TCP reachability test：`scripts/test-public-go-live-smoke.sh`
- Backup restore-drill smoke safety test：`scripts/test-backup-restore-drill-smoke.sh`
- Release artifact dash-prefixed directory test：`scripts/test-release-artifacts.sh`
- Docker quickstart safety test：`scripts/test-docker-quickstart.sh`
- Docker preflight safety test：`scripts/test-docker-preflight.sh`
- Docker container startup safety test：`scripts/test-docker-start.sh`
- Docker smoke safety test：`scripts/test-docker-smoke.sh`
- WebDAV curl smoke safety test：`scripts/test-webdav-client-smoke.sh`
- Release workflow 增量验证：`make workflows-check`、`make scripts-check`、`./scripts/check-secret-leaks.sh`、`make toolchains-check`、`git diff --check`
- Playwright E2E：`379 passed`
- 前端单测：`3124 passed`
- Docker build 和 `scripts/docker-smoke.sh`

最终发布前如代码、脚本、配置、文档或 workflow 再次变更，应重跑对应验证。

## 发布后核验

发布 tag 后，应优先运行统一上线核验入口：

```bash
./scripts/release-go-live-check.sh \
  --version <tag> \
  --domain nas.example.com \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check \
  --backup-api-url https://nas.example.com/api/v1 \
  --backup-job-id external-disk \
  --cookie-file cookies.txt
```

如需让统一上线核验入口保留临时下载产物排查失败，应省略 `--artifact-dir` 并传入 `--keep-published-artifacts`；显式 `--artifact-dir` 已由维护者指定并会保留，因此不能与该参数混用。
如本次发布无法执行备份恢复演练，必须显式传入 `--skip-backup-restore-drill`，并在发布记录中标记为未形成完整恢复证据。
只核验 GitHub Release 产物时，也可单独执行：

```bash
mkdir -p dist/release-check
./scripts/verify-published-release.sh \
  --version <tag> \
  --repository seanbao/mnemonas \
  --artifact-dir dist/release-check
```

随后应完成至少一次归档安装 smoke、Docker release 镜像启动 smoke、公开文档链接检查，以及公网部署环境的 `mnemonas-doctor --public-domain`、外部网络 `public-go-live-smoke.sh`、DNS、防火墙、TLS 和云安全组复核。
显式 `--artifact-dir` 可以使用以 `-` 开头的相对路径；仓库名会在下载前校验为 GHCR 兼容的小写 `owner/repo`。
如需保留临时下载产物排查失败，可省略 `--artifact-dir` 并传入 `--keep-artifacts`，脚本会输出保留目录。

## 已知限制

- 当前没有 release candidate，也未发布任何可用版本。本文记录的验证结果只对应指定开发分支和 checkout，不表示生产就绪；当前源码不应承载真实数据或用于生产部署。
- SMB/Samba 可挂载运行时仍未启用；当前仅保留配置、诊断和运行态提示。
- `LOCK` / `UNLOCK` 为 WebDAV 兼容性虚拟实现，多客户端并发编辑同一文件时仍应由客户端或上层流程控制冲突。
- 真实公网部署依赖具体 DNS、防火墙、TLS、反向代理和云厂商安全组配置，模板和 doctor 无法替代环境级复核。
- 如未来版本引入不可逆数据迁移，回退应按对应 release note 或备份恢复流程处理。

## 维护者发布清单

- 确认 `CHANGELOG.md` 和 `CHANGELOG.en.md` 已覆盖本次发布。
- 确认本草稿已按最终 tag、验证结果和产物名称更新。
- 确认 `git status --short --branch` 干净。
- 确认 `./scripts/plan-hardening-commits.sh --fail-on-manual` 没有待分组路径。
- 运行 `make release-readiness`，确认提交标题、临时 `fixup!` / `squash!` 提交、hardening 验证证据、发布文档命令、公网部署复核命令、安全策略、Dependabot 基线、CI/Release workflow 基线、Makefile 核心本地门禁目标基线、torture workflow 基线、开发状态和 Issue 反馈入口均通过检查。
- 创建并推送 tag 后，确认 Release workflow 成功。
- 发布后运行 `./scripts/release-go-live-check.sh`，并记录产物核验、公网 smoke 和备份恢复演练结果。
