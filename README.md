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

当前仓库自动将主分支发布为最新构建；安装命令只从本仓库下载脚本和构建产物：

当前构建包内置 Xray-core `v26.7.28`；已安装的面板也可在“Xray 配置”页面内按需切换其他官方版本。

```bash
bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

安装完成后运行 `x-ui` 打开管理菜单。安装程序会生成随机登录凭据，并将结果写入 `/etc/x-ui/install-result.env`。

### Xray 内核版本管理

1. 登录面板后进入“Xray 配置”。
2. 在页面顶部点击“内核版本”。
3. 选择官方 Xray 版本并点击“安装所选版本”。

版本切换会先停止 Xray，下载并校验所选版本，安装后自动重新启动 Xray；切换期间当前连接会暂时中断。版本列表按需加载，不会启动后台定时任务。

### 开发版更新

主分支构建由 [GitHub Actions 发布工作流](https://github.com/gzjacktang/3x-ui/actions/workflows/release.yml) 自动构建并更新。修改后需等待发布完成，再直接使用上方命令安装或升级最新版本；也可从 [Releases](https://github.com/gzjacktang/3x-ui/releases/tag/dev-latest) 查看当前构建版本。发布标签 `dev-latest` 仅为内部滚动标签，无需手动填写；该构建仍属于测试版，不等同于稳定版。

### 已安装面板的升级

已安装面板可以重复执行上方安装命令升级。升级时脚本会暂停 `x-ui` 服务并替换程序文件，升级完成后自动重新启动。默认配置下以下数据会保留：

- `/etc/x-ui/x-ui.db` 中的入站、客户端、出站、路由、用户和面板设置。
- 外部 PostgreSQL 数据库中的所有数据。
- 面板端口、访问路径、登录凭据和已配置的证书。

脚本会删除并重新解压 `/usr/local/x-ui` 程序目录，因此请不要把自定义数据或手工修改的文件放在该目录中。升级前建议先备份 SQLite 数据库：

```bash
cp -a /etc/x-ui/x-ui.db "/etc/x-ui/x-ui.db.bak.$(date +%Y%m%d%H%M%S)"
```

### 无人值守安装

适用于 cloud-init 的非交互式安装：

```bash
XUI_NONINTERACTIVE=1 bash <(curl -Ls https://raw.githubusercontent.com/gzjacktang/3x-ui/main/install.sh)
```

更多部署示例见 [`deploy/`](deploy/)，包括 [Cloud-init 配置](deploy/cloud-init/) 和 [Hetzner 部署说明](deploy/marketplace/hetzner/)。

## Docker

默认使用 SQLite，直接在仓库目录执行：

```bash
docker compose up -d
```

使用捆绑的 PostgreSQL 服务：

```bash
docker compose --profile postgres up -d
```

也可以直接构建本仓库的镜像：

```bash
docker build -t ghcr.io/gzjacktang/3x-ui:local .
```

## 数据库

SQLite 数据库默认位于 `/etc/x-ui/x-ui.db`。使用 PostgreSQL 时设置：

```bash
XUI_DB_TYPE=postgres
XUI_DB_DSN=postgres://xui:password@127.0.0.1:5432/xui?sslmode=disable
```

## 支持的平台

支持 Ubuntu、Debian、Armbian、Fedora、CentOS、RHEL、AlmaLinux、Rocky Linux、Oracle Linux、Amazon Linux、Arch、Manjaro、openSUSE、Alpine 和 Windows。

支持 `amd64`、`386`、`arm64`、`armv7`、`armv6`、`armv5` 和 `s390x`。

## 文档与开发

- [仓库文档](docs/README.md)
- [架构说明](docs/architecture.md)
- [贡献指南](CONTRIBUTING.md)
- [Issues](https://github.com/gzjacktang/3x-ui/issues)
- [Releases](https://github.com/gzjacktang/3x-ui/releases)

前端开发：

```bash
cd frontend
pnpm install
pnpm run build
```

后端测试：

```bash
go test -p 1 ./...
```

## 许可证

本项目使用 GPL-3.0 许可证。Xray-core 和其他第三方组件遵循各自的许可证。
