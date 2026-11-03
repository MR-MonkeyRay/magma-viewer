<h1 align="center">Magma Viewer</h1>

<p align="center">自动存档 Magma 画板，浏览和回放画面变化。</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-GPL%20v3-22C55E?style=flat-square" alt="License: GPL v3"></a>
</p>

## 功能

- 按计划抓取画板图片，内容未变化时不重复存档。
- 在网页中浏览历史画面、回放最近 24 小时，并对比两张图片。
- 下载原图或打包下载；管理员可下载全部存档并导出延时视频。
- 管理员可手动抓取、暂停定时任务、更新 Cookie 和管理公告。
- 可通过 webhook 接收 Cookie 临期、连续抓取失败及恢复通知。

## 架构

```text
┌───────┐    ┌─────────────────────────────────────────────────┐    ┌─────────┐
│ Magma │───▶│ Fetcher ───▶ Storage ◀─── Web service           │◀───│ Browser │
└───────┘    └─────────────────────────────────────────────────┘    └─────────┘
```

Magma Viewer 启动时及按计划抓取画面，图片保存在本地；浏览器通过 Web 服务查看存档。

## 快速开始

需要 Git、Docker 和 Docker Compose，以及可访问目标画板的 Magma 账号。默认发布镜像支持 Linux amd64 和 arm64。

1. 获取部署文件：

   ```bash
   git clone https://github.com/MR-MonkeyRay/magma-viewer.git
   cd magma-viewer/deploy/compose
   cp config.example.yaml config.yaml
   ```

2. 编辑 `config.yaml`，至少设置：

   - `admin.password`：改为自己的管理员密码，示例密码不能用于启动。
   - `magma.url`：将地址中的画板 ID 替换为目标画板 ID。
   - `magma.cookie`：填写 Magma 登录会话中的 `AUTH_TOKEN`。

   建议同时设置固定的 `admin.session_secret`；留空时服务重启会使管理员重新登录。配置包含敏感凭据，请限制文件访问权限，不要提交到仓库。

3. 启动服务：

   ```bash
   docker compose up -d
   ```

4. 打开 [http://localhost:8080](http://localhost:8080)。服务启动后会尝试抓取画面，之后按计划继续抓取；可在页面登录管理员账户查看运行状态。

容器配置文件为 `deploy/compose/config.yaml`，存档目录为 `deploy/compose/images/`。部署到远程主机时，使用主机地址访问；如需更改宿主机端口，修改 Compose 文件中的端口映射。

## 配置与维护

完整选项见仓库中的 `deploy/compose/config.example.yaml`；发布包中也包含配置样例。

| 配置项 | 用途 |
|---|---|
| `listen` | 服务监听地址，容器样例默认为 `:8080` |
| `schedule` | 抓取计划；样例为每分钟一次，支持标准五段 cron 和 `@every` |
| `storage.dir` | 图片和运行状态目录；Compose 将其映射到 `images/` |
| `notify.webhook` | 可选通知地址；`notify.format` 支持 `json` 或 `text` |
| `trusted_proxies` | 指定可信反向代理的 IP 或 CIDR |

管理员可在网页中更新 Cookie，无需重启；网页更新的 Cookie 保存在存档目录的 `.state/` 中。备份或迁移时请保护凭据，并保留完整存档目录，包括隐藏的 `.state/`。

更新容器时，在 `deploy/compose` 目录执行：

```bash
docker compose pull
docker compose up -d
```

查看日志：

```bash
docker compose logs -f magma-viewer
```

**访问控制：**存档浏览和图片下载对访客开放，管理员登录仅保护管理操作。如果画板内容不应公开，请通过网络访问限制或带认证的反向代理保护整个服务。公网部署建议启用 HTTPS，并按实际代理配置 `trusted_proxies`；示例见 [Nginx 部署说明](https://github.com/MR-MonkeyRay/magma-viewer/blob/main/deploy/nginx/README.md)。

## Linux systemd 部署

适用于运行 systemd 的 Linux amd64 或 arm64 主机。安装脚本需要 root、Bash、curl、tar、sha256sum 和 useradd 等工具。

```bash
curl -fL https://github.com/MR-MonkeyRay/magma-viewer/releases/latest/download/install.sh -o install.sh &&
sudo bash install.sh --no-start
```

编辑 `/etc/magma-viewer/config.yaml`，填写画板地址和 Cookie；首次安装时脚本会生成管理员密码及会话密钥。配置完成后启动：

```bash
sudo systemctl enable --now magma-viewer.service
sudo journalctl -u magma-viewer.service -f
```

默认只监听 `127.0.0.1:8080`，图片保存在 `/var/lib/magma-viewer/images`。安装脚本升级时保留已有配置和图片；服务默认只允许在 `/var/lib/magma-viewer` 和私有临时目录写入。

## 许可证

[GNU General Public License v3](./LICENSE)。
