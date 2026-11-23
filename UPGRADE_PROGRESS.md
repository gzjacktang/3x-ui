# 精简版选择性升级记录

日期：2026-11-10。升级基线 `90e849bd`，参考上游 `e535df03`（2026-11-06）。采用适用补丁的选择性移植，没有直接合并上游 main。

## 保留范围

保留入站、客户端、手工出站、路由、DNS、Balancer、Xray 配置、必要流量/IP 限制、ENC+Vision、内核版本选择和状态提示。界面仍只有简体中文和 English；面板安装、更新和管理菜单仍使用 `gzjacktang/3x-ui`。

不恢复系统状态主页、主机、节点、分组、订阅、API 文档、Telegram/邮件/通知服务，不新增 TUIC、AmneziaWG、MASQUE、XDRIVE 等协议或相应后台服务。数据库中供历史迁移使用的旧模型不代表重新启用了这些模块。

只构建并发布 Linux amd64（x86_64）。Docker 文件/工作流、Windows 包和其发布分支已移除，386、ARM、s390x 等其他架构不再构建；安装/更新脚本会在不支持的平台提前退出。被移除的仓库文件仍可通过 Git 历史恢复。

## 第一组：依赖、安全和资源边界

- Go 1.27.3，面板链接的 Xray Go 配置库对齐内置 Xray-core 26.10.31，保留 `multipathtcp=0`、显式 TCP 监听及子进程 MPTCP 防护。
- 更新仍在使用的 Go 与前端依赖；额外修复安全扫描发现的 Brotli 解压内存问题，并清除未使用 OpenPGP 包带来的模块级告警。
- 登录增加 16 KiB 请求体及用户名长度限制，采用进行中请求占位和每 IP 汇总限流；登录成功清除匿名会话及旧 CSRF token。
- 反代 IP 从受信链右侧识别，WebSocket 来源比较包含协议和端口。
- 面板连接总量/每来源设上限；静态资源 gzip 结果按文件共享，防止慢客户端下载各自占用压缩器；HTTP 服务异常不再被静默吞掉。
- SQLite 目录新建权限 0700，数据库及 WAL/SHM 文件 0600，SQLite 导出文件 0600。
- 空库直接启动也使用随机初始登录凭据，不生成固定 admin/admin；已有账号不重置。旧会话密钥一次性轮换。

## 第二组：客户端与运行态正确性

- 区分人工停用与配额/到期停用；使用内部 `manual_disabled` 标记保存人工意图。续期、提高额度、单个/批量/全部流量重置不再意外启用已明确手停的客户端。
- 因超额停用的客户端/入站在合适的重置后恢复；仍过期或仍超额的客户端保持关闭；全局流量投影仍纳入判断。
- 管理操作与流量写入使用共享串行边界，后台流量写入不抢占已排队的人工操作；持久化完成后再应用运行态。
- 新入站热添加按 email 过滤停用客户端，不依赖可能已过时的 traffic.inbound_id；SS2022 空用户列表保持锁定，避免退回仅服务器密钥可用的单用户模式。
- IP 临时断开结束后重新核对数据库状态，不恢复已删除、禁用或耗尽的旧快照。
- 客户端部分更新保留省略字段和入站 Flow 覆盖，显式 false/0/空备注/null 对象按请求处理。拒绝新建 `.`、`..` 名称，查询参数入口允许修复或删除历史记录，客户端页面也使用此入口。
- 修复 MTProto 子进程状态查询与退出等待的竞态，未新增后台工作。

## 第三组：配置兼容和升级可靠性

- VLESS ENC 与非空回落互斥，空回落数组自动清理；保存、运行时生成和并发回落写入均受保护。客户端 ENC 密钥及 Vision 不删除。
- 出站校验使用真实 handler 的加密/密钥/DNS 检查并拦截配置加载 panic；限制 XHTTP session、掩码数量、UDP hop 展开和证书文件读取成本。
- Freedom 策略迁至 sockopt；DNS 旧 nonIPQuery/blockTypes 迁至 rules，并保持 qType=0 语义；WireGuard/WARP 策略拆至 sockopt/targetStrategy，迁移 remoteDNS=local；XDNS 旧字符串和中间对象均迁至当前 names/addrs 形态。
- 兼容数据库一次性迁移、旧备份/模板导入、运行时生成和链接导入；不迁回已删主机/订阅模块。
- geodata 保存前检查 .dat 扩展名、最多 100 个资产、合法 cron、至少一分钟的 @every 周期。普通 geoip/geosite 随构建更新，不下载 IR/RU 数据库。
- WireGuard 导出逐行拒绝控制字符；修复 IPv6 端点括号、分享备注重复解码、URL-safe/padded VMess Base64 和移动端 Balancer 标签。
- 2FA 接受相邻时间窗口；Xray 崩溃日志按完整报告记录，面板可看到真正原因而非只有一段 panic 前缀。
- 面板包发布 SHA-256 侧车文件，安装/更新缺失校验或不匹配时中止；全部资源暂存后才停服务，使用包内配套管理脚本，迁移成功后才启动，不修改短访问路径或证书。
- 网页更新向脚本传递已有面板出站代理。Xray 更新先下载、验证官方 digest，并以候选内核 `run -test` 检查现有配置，通过后才原子替换并重启；下载/检查失败时旧内核继续服务。

## 升级前须知

1. 先备份数据库。更新保留现有账号、路径、端口、证书和业务数据，但会迁移旧 Xray 字段。迁移失败会中止更新并保持服务停止，不提供自动数据库回滚。
2. 旧会话轮换后重新登录一次，原密码不变。随机凭据只用于没有账号的新数据库。
3. 历史数据库没有完整记录停用原因，旧停用记录仍需按原有状态兼容推断。必须长期保持关闭的历史客户端，可在升级后再次执行一次“禁用”，记录明确的人工意图；之后编辑备注、续期或重置流量均不会清除此标记。
4. 未连接或修改你的 VPS，也没有宣称测得新的 VPS 内存降幅。连接和压缩改动主要限制并发资源消耗；小机实际 RSS 仍应在部署后观察。

## 验证记录

- 完整 `go test -p 1 ./...` 和 `go build ./...` 已通过。
- 官方 Xray 26.10.31 测试二进制先验证 SHA-256，再执行真实 gRPC 集成测试：热更新、用户增删、SS 账户类型/幂等、新用户流量及路由端口校验全部通过。
- 前端类型检查、ESLint、81 个测试文件/838 个用例（含 Chromium Storybook 测试）、Vite 和 Storybook 静态构建已通过。
- `npm audit --audit-level=low`：0 告警；`govulncheck ./...`：无漏洞。
- 安装/更新隔离测试覆盖校验缺失/错误、平台拒绝、短路径/证书保留、资源下载失败及迁移/启动顺序；脚本语法和 diff 空白检查通过。
- 生成的前端契约已重建核对，内部人工停用字段未暴露到 API，生成文件无差异。
- 修复 MTProto 竞态后，全量 `go test -race -shuffle=on -count=1 -p 1 ./...` 已通过。
- 最终 [Linux CI](https://github.com/gzjacktang/3x-ui/actions/runs/42768182038) 8 项全部通过：Go、race/shuffle、PostgreSQL、前端、生成契约、安全扫描、模糊测试、静态检查。
- PostgreSQL 专用测试在本机按环境门控跳过，已由 GitHub CI 的独立临时 PostgreSQL 服务验证；未接触用户数据库。

## 发布产物核对

[发布构建](https://github.com/gzjacktang/3x-ui/actions/runs/42768181682) 成功，[滚动 Release](https://github.com/gzjacktang/3x-ui/releases/tag/dev-latest) 对应程序提交 `7713275606f3c8523fa2605fd6f6f5f5bcc1d5fc`。后续仅文档提交不改变该程序包。

- Release 资产只有 `x-ui-linux-amd64.tar.gz` 与对应 `.sha256`；旧 Windows/386/ARM/s390x 滚动发布包已清理。
- 下载后 SHA-256 校验通过：`2107df1025eacf8f20e6966e6e38c99408efbaef8f49a74301b4458329590697`。
- 解包核对面板、Xray、MTProto 均为静态链接 Linux x86-64 程序；面板元数据为 Go 1.27.3、提交 `77132756`，并包含 `DefaultGODEBUG=multipathtcp=0`。
- 包内 `x-ui.sh` 与本仓库同提交源码逐字一致；geodata 只有普通 `geoip.dat` 与 `geosite.dat`，没有 IR/RU 数据库。

独立的 GitHub Pages 文档站部署因仓库未启用 Pages 返回 404；文档静态构建本身成功。未擅自开启 Pages 或绑定原作者域名；这不影响 GitHub README、面板 CI 或安装包发布。

## 复核结果

规范轴：本轮改动按现有类型/国际化/格式规则检查，保留自有下载源及精简边界，未改生成契约或跳过失败测试。

需求轴：复核发现的“显式 false 被续期覆盖”和“手停后改备注导致误恢复”已修复并补回归。Storybook 的中文路径测试漏注册、MTProto 的旧进程竞态也在发布前验证中修复；没有通过关闭测试绕过。

## 2026-11-18：v3.6.0 之后的资源精简

基线 `462c085d`。本轮不修改数据库结构，不改变安装/更新来源，也不恢复已移除的模块。

- 流量统计忽略未变化的计数器，保留原采集周期、计量基线和到期/限额检查。两个在线状态任务共用当前 Xray 进程最近 4 秒内的成功快照；查询失败时不用过期数据。Fail2Ban 显式关闭时，IP 检查直接返回。WebSocket 推送复用同一客户端快照里的最后在线时间。
- 移植原版 3x-ui 的 cgroup 内存软预算识别（`efe76852`）和停用客户日志聚合（`5297fd3c`）。内存仍以显式配置优先，cgroup 软预算不等同于整组进程的硬限制。
- 没有本地 MTProto 入站时不再每 10 秒查询数据库。新增或修改 MTProto 入站、更换数据库后会重新检查；禁用入站仍保留配额重置及恢复所需的检查。全局流量和限额继续兼容旧数据，存在性检查只读一行，不再计数整表。
- 正式前端不再打包 sourcemap。需要本地调试时，可以在 `frontend` 下执行 `npm run build -- --sourcemap`。
- 内置 Xray 与 Go SDK 仍为 `26.10.31`。等含 [Mux 并发修复](https://github.com/XTLS/Xray-core/pull/7375) 的正式版，再同步升级内核、SDK 及新旧配置字段。此次未在 VPS 测量 CPU、内存和线路速度。
- `v3.6.1` 面板使用 Go `1.27.4` 构建，修复发布前扫描发现的 [`GO-2026-6859`](https://pkg.go.dev/vuln/GO-2026-6859)。不改变内置 Xray 或面板的 MPTCP 防护。

本机验证：完整 Go 测试和构建、改动相关 Go 竞态测试、前端类型检查、ESLint、838 个前端用例（含 Chromium Storybook）及 Vite 生产构建均通过。生产构建目录中没有 `.map` 文件。

## 2026-11-23：v3.6.2 安全、IP 限制与升级保留修复

基线 `v3.6.1`（`bb973a3e`），对照上游 `5297fd3c`（2026-11-13）与 Xray-core `v26.11.9` / `main` 选择性移植。不修改数据库结构，不改变安装/更新来源，也不恢复已移除的模块。

- 修复 `v3.6.1` 在线状态缓存与客户端 IP 限制执法的误判窗口：展示任务继续复用 4 秒快照并克隆返回切片；启用 IP 限制执法时改用实时 gRPC 查询并核验当前 Xray 进程代际，避免已下线 IP 触发瞬断。
- 移植上游 2FA TOTP 密钥校验与规范化（`7367ff75` / #7113）：接受空格、小写和 `=` 填充形式，拒绝空密钥或非法 Base32，避免登录/关闭 2FA panic 或空密钥绕过。
- 面板手动切换 Xray（升级或降级）时记录 `bin/xray-manual.sha256`，全新安装与内置更新记录 `bin/xray-bundled.sha256`；在已有匹配标记后，`update.sh` 升级面板时自动保留手动选择（含升降级）或自行替换的 `bin/xray-linux-amd64`。无标记文件的旧安装首次升级时仅自动保留高于包内版本的 Xray（若旧实例曾手动降级过，可传 `XUI_KEEP_XRAY=1` 保留并补写标记；传入 `XUI_FORCE_BUNDLED_XRAY=1` 可恢复内置内核）。面板内升降级不受限，`geoip.dat` / `geosite.dat` 保持默认随包更新。
- 移植前端 `log.logLevel` 大小写读写兼容（`bfe826c1`），禁用 `i18next` `$t(...)` 嵌套解析，并修复 `ConfigBlock` Storybook 折叠动画期间 axe 对比度偶发抖动。
- 升级 10 项独立 Go 维护依赖（`gin-contrib/gzip`、`sessions`、`klauspost/compress v1.20.4`、`go-sqlite3 v1.14.53`、`gopsutil/v4 v4.26.10`、`fasthttp v1.78.0`、`x/crypto v0.59.0`、`x/sync`、`x/sys`、`x/text`）；继续保留 Go `1.27.4`、`godebug multipathtcp=0` 以及内置 Xray `26.10.31`（等包含 `#7375`、`#7359`、`#7431` 的下一正式版后统一同步）。
- 给 `ci.yml` 增加 `workflow_dispatch`，清理 6 个未使用的上游 Actions 工作流；补全配置日志测试的具体错误断言并同步 `CONTRIBUTING.md`。

CI 验证：[Linux CI](https://github.com/gzjacktang/3x-ui/actions/runs/45101667822) 8 项全部通过（`go-test`、`race`、`golangci`、`govulncheck`、`postgres-durable-first`、`codegen`、`fuzz-smoke`、`frontend` 含 840 用例与 `npm audit`）。

## 2026-11-23：v3.6.3 IP 限制受控重启竞态与旧安装升级兼容修复

基线 `v3.6.2`（`d2c749f4`）。不修改数据库结构，不改变内置 Xray（`26.10.31`）和 Go（`1.27.4`）基线，也不恢复已移除的模块。

- 将在线状态采样返回的 Xray 进程身份（`OnlineUsersSample.Process`）贯通到 IP 限制的封禁日志、临时断开与恢复操作（`IsCurrentProcess`、`DoForProcess`、`RemoveUserForProcess`、`AddUserForProcess`），修复面板受控重启期间旧采样误作用于新 Xray 进程的竞态。
- 修正 IP 检查与 Xray 重启的锁顺序：移除 SQLite 写事务（`_txlock=immediate`）内对 Xray 重启锁的等待，消除与 `RestartXray -> GetXrayConfig -> AddTraffic` 并发时的锁序互等及 10 秒写锁超时风险，并补齐确定性并发回归测试。
- 将 Fail2Ban `[LIMIT_IP]` 封禁日志写入与进程身份校验合并到事务提交后的同一次短重启锁临界区内（`DoForProcess`），避免检查与写日志之间发生受控重启留下未撤销的旧进程封禁记录。
- 已知边界：换代导致跳过封禁时，已提交的 `inbound_client_ips` 观测记录不会回滚；若 Xray 在检查后自行崩溃退出，也不受面板重启锁控制。本补丁修复的是面板受控重启时的竞态窗口，不构成对所有网络/内核侧断线的兜底结论。
- `update.sh` 新增 `XUI_KEEP_XRAY=1`（优先级低于 `XUI_FORCE_BUNDLED_XRAY=1`），供无标记文件的旧安装首次升级时保留此前手动降级过的 Xray 并补写 `bin/xray-manual.sha256`；同步清理贡献文档中已删除工作流的残留引用，收紧配置日志测试的精确错误断言。

CI 验证：代码提交 `fc593f67` 的 [Linux CI](https://github.com/gzjacktang/3x-ui/actions/runs/45115106344) 8 项全绿；本发版提交将在打 `v3.6.3` 标签前再执行一轮完整 Linux CI。

## 2026-11-23：v3.6.4 默认追踪正式稳定版

基线 `v3.6.3`（`226325f9`）。不修改数据库结构，不改变内置 Xray（`26.10.31`）和 Go（`1.27.4`）基线。

- 面板“面板更新”不再固定检查和安装 `dev-latest`，改为默认对比 GitHub 最新正式稳定版并安装对应标签；运行 `dev+<提交号>` 开发版的面板会提示切换到同版本或更新的稳定版。
- `install.sh` / `update.sh` / `x-ui update` 不传版本号时，通过 GitHub `releases/latest` 自动解析最新稳定标签（仅接受 `vX.Y.Z`）；显式传入 `dev` / `dev-latest` 或使用 “Update to Dev Channel” 仍可安装滚动开发版。
- 早于 `v3.6.4` 的面板网页更新仍固定走 `dev-latest`，需在服务器上执行一次一键升级命令切到稳定版。
- README 一键安装/升级命令改为默认稳定版，无需手动带版本号。

CI 验证：发版提交在打 `v3.6.4` 标签前执行完整 Linux CI。
