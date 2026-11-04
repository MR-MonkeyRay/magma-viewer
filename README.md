<h1 align="center">Magma Viewer</h1>

<p align="center">定时保存 Magma 画板图片，并通过网页浏览和管理历史记录。</p>

<p align="center"><a href="./LICENSE"><img src="https://img.shields.io/badge/License-GPL%20v3-22C55E?style=flat-square" alt="License: GPL v3"></a></p>

## 核心亮点

- 定时抓取画板图片，内容变化时保存历史版本。
- 在网页中查看画板、浏览历史图片并下载。
- 在管理后台添加画板、调整抓取计划和更新 Cookie。
- 支持多个画板、画板归档和可选的 Webhook 告警。

## 架构

```text
             ┌────── Magma Viewer ──────┐
┌─────────┐  │  ┌─────────┐             │      ┌─────────┐
│  Magma  │──┼─▶│ Fetcher │─────────────┼────▶│ Storage │
└─────────┘  │  └─────────┘             │      └─────────┘
             │                          │          │
             │  ┌─────────┐             │          │
┌─────────┐  │  │   Web   │◀────────────┼──────────┘
│ Browser │──┼─▶│ server  │             │
└─────────┘  │  └─────────┘             │
             └──────────────────────────┘
```

## 快速开始

需要 Docker 和 Docker Compose。以下命令在仓库根目录执行：

```bash
cp deploy/compose/config.example.yaml deploy/compose/config.yaml
```

编辑 `deploy/compose/config.yaml`：将 `admin.password` 改为自己的密码。不要把配置文件或 Cookie 提交到公开仓库。

```bash
docker compose -f deploy/compose/docker-compose.yml up -d
```

1. 打开 `http://localhost:8080/admin`，使用管理员账号登录。
2. 添加画板，填写 Magma 画板地址。
3. 保存 Cookie。
4. 打开 `http://localhost:8080` 浏览图片。

## 配置与维护

- 配置样例：[deploy/compose/config.example.yaml](./deploy/compose/config.example.yaml)。`schedule` 控制默认抓取频率，也可在管理后台为画板单独设置。
- 从旧版单画板升级时，若旧 `config.yaml` 仍含 `magma.url` / `magma.cookie`，新版首次启动会自动迁移：地址成为「默认画板」的地址，Cookie 成为共享 Cookie。迁移后这两项不再使用，可从 `config.yaml` 中删除。
- 数据保存在 `deploy/compose/images/`。备份时保留整个目录，包括其中的状态和归档数据；配置文件也应单独安全备份。
- 镜像默认使用 `latest`；可在 `deploy/compose/.env` 中设置 `MAGMA_VIEWER_TAG` 固定版本。
- Linux systemd 安装脚本和样例位于 [deploy/systemd/](./deploy/systemd/)；反向代理配置说明见 [deploy/nginx/README.md](./deploy/nginx/README.md)。
- 部署到公网时，应配置 HTTPS 反向代理并根据实际网络设置 `trusted_proxies`；妥善保护管理员密码和 Cookie。

## 许可证

[GNU General Public License v3](./LICENSE)
