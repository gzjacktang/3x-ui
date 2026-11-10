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

## 快速开始

当前仓库自动将主分支发布为最新 Linux amd64 构建，安装包附带 `.sha256` 校验文件；安装/更新在校验缺失或不匹配时中止。安装命令只从本仓库下载脚本和构建产物：

当前构建包内置 Xray-core `v26.10.31`；已安装的面板也可在“Xray 配置”页面内按需切换其他官方版本。

面板自身的 TCP 监听已显式禁用 MPTCP，构建默认值及面板启动的 Xray/MTProto 子进程也默认禁用 MPTCP，以缓解 CVE-2026-98432 已公开的利用路径。用户通过高级配置显式启用的 Xray `tcpMptcp` 仍会覆盖默认值，请保持关闭。此措施不修复 Linux 内核中的底层漏洞，仍需安装发行版的内核安全更新并按要求重启。

```bash
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

安装完成后运行 `x-ui` 打开管理菜单。安装程序会生成随机登录凭据，并将结果写入 `/etc/x-ui/install-result.env`。

### Xray 内核版本管理

1. 登录面板后进入“Xray 配置”。
2. 在页面顶部点击“内核版本”。
3. 选择官方 Xray 版本并点击“安装所选版本”。

版本切换会先下载并校验所选版本，用候选内核测试当前配置；检查通过后才替换程序并重新启动 Xray。下载或配置检查失败时，旧内核继续运行；实际重启时连接会短暂中断。版本列表按需加载，不会启动后台定时任务。

### 开发版更新

主分支构建由 [GitHub Actions 发布工作流](https://github.com/gzjacktang/3x-ui/actions/workflows/release.yml) 自动构建并更新。修改后需等待发布完成，再直接使用上方命令安装或升级最新版本；也可从 [Releases](https://github.com/gzjacktang/3x-ui/releases/tag/dev-latest) 查看当前构建版本。发布标签 `dev-latest` 仅为内部滚动标签，无需手动填写；该构建仍属于测试版，不等同于稳定版。

### 已安装面板的升级

已安装面板可在“面板设置 → 常规 → 面板更新”中检查并安装本仓库的最新构建；确认更新后，页面会显示执行状态并在成功后自动刷新。也可以重复执行上方安装命令升级。升级会先下载并校验 SHA-256、解包和检查资源，随后暂停 `x-ui` 服务并替换程序文件；数据库迁移成功后才重新启动。更新不申请或改动证书，不重建已有的短访问路径。默认配置下以下数据会保留：

- `/etc/x-ui/x-ui.db` 中的入站、客户端、出站、路由、用户和面板设置。
- 外部 PostgreSQL 数据库中的所有数据。
- 面板端口、访问路径、登录凭据和已配置的证书。

已有安装通过暂存更新替换程序与内置资源，数据库、证书路径和自定义 `bin/` 文件会保留；同名的内置程序和数据库文件仍会随版本更新。升级前建议先备份 SQLite 数据库：

```bash
cp -a /etc/x-ui/x-ui.db "/etc/x-ui/x-ui.db.bak.$(date +%Y%m%d%H%M%S)"
```

升级包含一次旧会话密钥轮换，已有账号和密码不变，但需要重新登录。Freedom、DNS、WireGuard/WARP、XDNS 旧配置会按当前内核语义迁移。数据库迁移失败时更新会中止并保持服务停止，不会自动回滚数据库；请保留升级前备份。

### 本轮选择性升级

- Go 工具链、安全依赖和面板使用的 Xray 配置库与内置 `26.10.31` 对齐。
- 登录并发限流、登录会话轮换、反代 IP 和 WebSocket 来源校验；增加 Web 连接上限和共享静态压缩缓存。
- SQLite 文件权限收紧；直接启动空库时也生成随机初始凭据，已有登录账号不重置。
- ENC 与回落冲突在保存及配置生成前拒绝；补充出站密钥、证书文件和高开销配置校验。
- 修复客户端续期、流量重置、手动停用保护、部分字段更新和 IP 临时限制恢复；完整保留核心崩溃原因。
- 发布仅保留 Linux amd64，安装与升级严格核对 `.sha256`，所有面板更新入口继续使用本仓库。

未恢复系统监控、节点、主机、分组、订阅、通知服务或额外语言。普通 `geoip.dat` / `geosite.dat` 继续随构建更新，不下载 IR/RU 数据库。

详细范围、验证记录和升级注意事项见 [升级记录](UPGRADE_PROGRESS.md)。

### 无人值守安装

适用于 cloud-init 的非交互式安装：

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
