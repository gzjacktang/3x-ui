# 3X-UI Lite

3X-UI Lite 是一个面向小型 VPS 的低内存 Web 面板，用于管理单台服务器上的 [Xray-core](https://github.com/XTLS/Xray-core)。当前版本保留入站、客户端、手工出站、路由、DNS、Balancer、Xray 设置和必要的流量限制功能，移除了系统监控、主机、节点、分组、API 文档、Telegram、邮件和订阅服务。

界面只保留 English 和简体中文两种语言。

> [!IMPORTANT]
> 本项目仅供个人使用。请勿将其用于非法目的，也不建议直接用于生产环境。

## 功能

- 多协议入站：VLESS、VMess、Trojan、Shadowsocks、WireGuard、Hysteria2、HTTP、SOCKS、Dokodemo-door / Tunnel 和 TUN。
- 现代传输与安全：TCP、mKCP、WebSocket、gRPC、HTTPUpgrade、XHTTP、TLS、XTLS 和 REALITY。
- 回落（Fallback）：在同一端口提供多种协议。
- 客户端管理：流量配额、到期时间、IP 限制、在线状态、分享链接和二维码。
- 出站与路由：手工出站、WARP、NordVPN、自定义路由规则、Balancer 和出站代理链。
- Xray 内核管理：在“Xray 配置”页面查看当前内核版本，按需加载官方版本列表，并选择版本安装。
- 存储：SQLite（默认）或 PostgreSQL。
- Fail2ban：按客户端 IP 限制进行封禁。
- UFW 防火墙：在“面板设置 → 防火墙”查看规则、放行或删除端口、启停防火墙；启用前会保护 SSH 与面板端口，避免把自己锁死。

## 快速开始

当前仓库提供正式稳定版（[Releases](https://github.com/gzjacktang/3x-ui/releases/latest)）与主分支滚动开发版（[`dev-latest`](https://github.com/gzjacktang/3x-ui/releases/tag/dev-latest)）Linux amd64 安装包，均附带 `.sha256` 校验文件；安装/更新在校验缺失或不匹配时中止。安装命令只从本仓库下载脚本和构建产物。

当前构建包使用 Go `1.27.4` 静态构建，内置 Xray-core `v26.10.31`；已安装的面板也可在“Xray 配置”页面内按需切换其他官方版本（升级或降级均不受限）。

面板自身的 TCP 监听已显式禁用 MPTCP，构建默认值及面板启动的 Xray/MTProto 子进程也默认禁用 MPTCP，以缓解 CVE-2026-98432 已公开的利用路径。用户通过高级配置显式启用的 Xray `tcpMptcp` 仍会覆盖默认值，请保持关闭。此措施不修复 Linux 内核中的底层漏洞，仍需安装发行版的内核安全更新并按要求重启。

**一键安装 / 升级（默认自动获取最新正式稳定版）：**

```bash
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

**安装 / 升级到指定版本：**

```bash
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh) v3.6.4
```

**安装 / 升级滚动开发版（`dev-latest`，逐提交测试构建，不推荐日常使用）：**

```bash
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh) dev
```

> 已有安装再次运行上述命令会自动走非交互式暂存升级流程，不会覆盖数据库、证书路径或短访问路径。

安装完成后运行 `x-ui` 打开管理菜单。全新安装会生成随机登录凭据，并将结果写入 `/etc/x-ui/install-result.env`。

### Xray 内核版本管理

1. 登录面板后进入“Xray 配置”。
2. 在页面顶部点击“内核版本”。
3. 选择官方 Xray 版本并点击“安装所选版本”。

版本切换会先下载并校验所选版本，用候选内核测试当前配置；检查通过后才替换程序并重新启动 Xray。下载或配置检查失败时，旧内核继续运行；实际重启时连接会短暂中断。版本列表按需加载，不会启动后台定时任务。

### UFW 防火墙

1. 登录面板后进入“面板设置 → 防火墙”。
2. 可查看当前 UFW 状态与规则，按端口/协议放行，或按规则编号删除。
3. 启用防火墙前会自动放行 SSH 与当前面板监听端口；这两类保护规则不能从面板删除。
4. 仅支持 Debian/Ubuntu 系主机上的 UFW；未安装时可在页面内安装。不做 firewalld / nftables 日常直管，也不会在创建入站时自动开端口。
5. 若检测到系统已有生效的 nftables / iptables / firewalld（且 UFW 未接管），页面会提示一键备份旧规则、停用旧防火墙并切换到 UFW（先放行 SSH 与面板端口）。切换瞬间入站可能短暂开放。

关闭防火墙、删除规则、一键接管均属高风险操作，请确认不会中断 SSH 与面板访问后再执行。

### 开发版更新

主分支构建由 [GitHub Actions 发布工作流](https://github.com/gzjacktang/3x-ui/actions/workflows/release.yml) 自动发布到滚动标签 [`dev-latest`](https://github.com/gzjacktang/3x-ui/releases/tag/dev-latest)。该构建属于测试版，面板中显示为 `dev+<提交号>`，不等同于稳定版；只有显式传入 `dev`（或 `dev-latest`）或在 `x-ui` 菜单中选择 “Update to Dev Channel” 时才会安装。

### 已安装面板的升级

已安装面板可在“面板设置 → 常规 → 面板更新”中检查并安装本仓库的**最新正式稳定版**（`v3.6.4+` 面板起，网页更新默认走稳定版通道；正在运行 `dev+<提交号>` 开发版的面板也会提示切换到同版本或更新的稳定版）。确认更新后，页面会显示执行状态并在成功后自动刷新。也可以在服务器上运行 `x-ui update` 或重复执行上方安装命令升级。升级会先下载并校验 SHA-256、解包和检查资源，随后暂停 `x-ui` 服务并替换程序文件；数据库迁移成功后才重新启动。更新不申请或改动证书，不重建已有的短访问路径。默认配置下以下数据会保留：

- `/etc/x-ui/x-ui.db` 中的入站、客户端、出站、路由、用户和面板设置。
- 外部 PostgreSQL 数据库中的所有数据。
- 面板端口、访问路径、登录凭据和已配置的证书。

已有安装通过暂存更新替换程序与内置资源，数据库、证书路径和自定义 `bin/` 文件会保留；升级到带标记的版本（`v3.6.2+`）后，通过面板“内核版本”手动切换（升级或降级）或自行替换过的 `bin/xray-linux-amd64` 在后续面板升级时会自动保留。对于尚无标记文件的旧安装，升级时默认仅自动保留高于包内版本的 Xray（若旧实例曾手动降级过 Xray，首次升级可传入 `XUI_KEEP_XRAY=1` 保留当前内核并补写标记；如需强制恢复为安装包内置版本，可传入 `XUI_FORCE_BUNDLED_XRAY=1`）。`geoip.dat` / `geosite.dat` 与其他内置程序默认随版本更新。

升级前建议先备份 SQLite 数据库：

```bash
cp -a /etc/x-ui/x-ui.db "/etc/x-ui/x-ui.db.bak.$(date +%Y%m%d%H%M%S)"
```

常用一键升级命令：

```bash
# 升级到最新正式稳定版
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)

# 旧安装曾手动降级过 Xray（无标记文件）：首次升级保留现有内核并补写标记
XUI_KEEP_XRAY=1 bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)

# 升级并强制恢复为安装包内置 Xray（v26.10.31）
XUI_FORCE_BUNDLED_XRAY=1 bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

升级时如果依赖软件包都已安装，脚本会跳过软件源刷新；fail2ban 配置没有变化时也不会重启 fail2ban，以降低小内存 VPS 升级时的瞬时负载。需要强制重新安装依赖时，在命令前加 `XUI_FORCE_DEPS=1`。

> 早于 `v3.6.4` 的面板（包括显示为 `dev+<提交号>` 的开发版），网页“面板更新”仍固定检查 `dev-latest`。请先在服务器上执行一次上方升级命令切到正式稳定版，此后网页更新即默认走稳定版通道。

升级包含一次旧会话密钥轮换，已有账号和密码不变，但需要重新登录。Freedom、DNS、WireGuard/WARP、XDNS 旧配置会按当前内核语义迁移。数据库迁移失败时更新会中止并保持服务停止，不会自动回滚数据库；请保留升级前备份。

### 近期更新与选择性升级（至 `v3.6.5`）

- 升级脚本在依赖齐全时跳过软件源刷新，fail2ban 配置未变时不再重启，降低升级时的 CPU 冲高。
- 面板网页更新、`install.sh` / `update.sh` 与 `x-ui update` 默认改为追踪 GitHub 最新正式稳定版；滚动开发版需显式选择。
- Go `1.27.4` 工具链、维护依赖和面板使用的 Xray 配置库与内置 `26.10.31` 对齐。
- 登录并发限流、登录会话轮换、2FA TOTP 密钥校验与规范化（拒绝空密钥与非法 Base32）、反代 IP 和 WebSocket 来源校验；增加 Web 连接上限和共享静态压缩缓存。
- SQLite 文件权限收紧；直接启动空库时也生成随机初始凭据，已有登录账号不重置。
- ENC 与回落冲突在保存及配置生成前拒绝；补充出站密钥、证书文件和高开销配置校验；兼容前端 `log.logLevel` 大小写读写并禁用 `i18next` 嵌套解析。
- 修复客户端续期、流量重置、手动停用保护、部分字段更新；客户端 IP 限制执法改用实时在线采样并绑定采样 Xray 进程代际，修正面板受控重启时的竞态窗口、SQLite 写事务与重启锁的锁序反转，以及 Fail2Ban 封禁日志原子写入；完整保留核心崩溃原因。
- 发布仅保留 Linux amd64，安装与升级严格核对 `.sha256`；支持自动保留手动切换/替换的 Xray 内核（含 `XUI_KEEP_XRAY=1` / `XUI_FORCE_BUNDLED_XRAY=1` 开关），所有面板更新入口继续使用本仓库。
- 新增 UFW 防火墙管控页：实时放行/删除端口、启停防火墙，并保护 SSH 与面板端口。

未恢复系统监控、节点、主机、分组、订阅、通知服务或额外语言。普通 `geoip.dat` / `geosite.dat` 继续随构建更新，不下载 IR/RU 数据库。

详细范围、验证记录和升级注意事项见 [升级记录](UPGRADE_PROGRESS.md)。

### 无人值守安装

适用于 cloud-init 的非交互式安装（默认最新正式稳定版）：

```bash
XUI_NONINTERACTIVE=1 bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

更多部署示例见 [`deploy/`](deploy/)，包括 [Cloud-init 配置](deploy/cloud-init/) 和 [Hetzner 部署说明](deploy/marketplace/hetzner/)。

## 数据库

SQLite 数据库默认位于 `/etc/x-ui/x-ui.db`。使用 PostgreSQL 时设置：

```bash
XUI_DB_TYPE=postgres
XUI_DB_DSN=postgres://xui:password@127.0.0.1:5432/xui?sslmode=disable
```

## 支持的平台

仅提供 Linux `amd64`（x86_64）原生安装包，适用于 Ubuntu、Debian、Fedora、CentOS、RHEL、AlmaLinux、Rocky Linux、Oracle Linux、Amazon Linux、Arch、Manjaro、openSUSE 和 Alpine。

不再构建或提供 Docker、Windows、386、ARM 和 s390x 安装包。安装器会在不支持的平台上提前退出。

## 文档与开发

- [仓库文档](docs/README.md)
- [架构说明](docs/architecture.md)
- [贡献指南](CONTRIBUTING.md)
- [Issues](https://github.com/gzjacktang/3x-ui/issues)
- [Releases](https://github.com/gzjacktang/3x-ui/releases)

前端开发：

```bash
cd frontend
npm ci
npm run build
```

后端测试：

```bash
go test -p 1 ./...
```

## 许可证

本项目使用 GPL-3.0 许可证。Xray-core 和其他第三方组件遵循各自的许可证。
