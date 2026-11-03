<h1 align="center">Magma Viewer</h1>

<p align="center">自动存档 Magma 画板，浏览和回放画面变化。</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-GPL%20v3-22C55E?style=flat-square" alt="License: GPL v3"></a>
</p>

## 功能

- 可同时存档多个 Magma 画板；每个画板有独立的目录和抓取记录。
- 按计划抓取画板图片，内容未变化时不重复存档。
- 首页默认显示最近有新画面的画板，顶栏下拉可切换画板；切换后地址带 `?board=<画板ID>`，可直接分享。
- 在网页中浏览历史画面、回放最近 24 小时，并对比两张图片。
- 下载原图或打包下载；管理员可下载全部存档并导出延时视频。
- 管理后台 `/admin`（独立页面）：概览；画板管理（新建、命名、画板地址、抓取频率可沿用全局或单独设置、同步有效期——到设定时间开始/停止定时抓取、是否在首页列表中显示、排序、单独 Cookie、暂停/启用、立即抓取）；抓取记录；共享 Cookie；公告；通知与存储；归档包管理。
- 归档与删除：归档会把画板全部原图打包成 ZIP 存入归档列表，画板随即下线、不可编辑或恢复，数据目录在 ZIP 校验通过后删除，归档包可随时下载或删除；删除会移除画板、数据目录及其全部归档包，需要输入画板名称确认。
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

2. 编辑 `config.yaml`，设置以下选项：

   - `admin.password`：改为自己的管理员密码，示例密码不能用于启动。
   - `magma.url`：可选。首次启动时如已设置，会据此创建「默认画板」；之后在管理后台添加和管理画板，修改此项不会影响已有画板。
   - `magma.cookie`：所有画板共用的默认 Cookie（Magma 登录会话中的 `AUTH_TOKEN`）。

   建议同时设置固定的 `admin.session_secret`；留空时服务重启会使管理员重新登录。配置包含敏感凭据，请限制文件访问权限，不要提交到仓库。

3. 启动服务：

   ```bash
   docker compose up -d
   ```

4. 打开 [http://localhost:8080](http://localhost:8080)。服务启动后会尝试抓取画面，之后按计划继续抓取；可在页面登录管理员账户查看运行状态。管理员可通过头像菜单或直接访问 `/admin` 打开管理后台添加画板；新画板默认暂停，可先配置并用「立即抓取」测试，确认无误后再启用。

容器配置文件为 `deploy/compose/config.yaml`，存档目录为 `deploy/compose/images/`。部署到远程主机时，使用主机地址访问；如需更改宿主机端口，修改 Compose 文件中的端口映射。

## 配置与维护

完整选项见仓库中的 `deploy/compose/config.example.yaml`；发布包中也包含配置样例。

| 配置项 | 用途 |
|---|---|
| `listen` | 服务监听地址，容器样例默认为 `:8080` |
| `schedule` | 全局默认抓取计划（样例为每分钟一次，支持标准五段 cron 和 `@every`）；画板可在后台单独设置 |
| `magma.headers` | 所有画板共用的额外请求头；其中的 `Cookie` 会被忽略，Cookie 请填在 `magma.cookie` |
| `storage.dir` | 图片和运行状态目录；Compose 将其映射到 `images/` |
| `notify.webhook` | 可选通知地址；`notify.format` 支持 `json` 或 `text` |
| `trusted_proxies` | 指定可信反向代理的 IP 或 CIDR |

Cookie 优先级为：画板单独设置的 Cookie > 管理后台更新的共享 Cookie > 配置文件中的 `magma.cookie`。在线更新 Cookie 无需重启。

存档目录结构如下：

```text
<storage.dir>/
  .state/boards.json    画板列表与归档记录
  .state/cookie.json    后台更新的共享 Cookie
  .state/notices.json   公告
  boards/<画板ID>/      各画板的图片、缩略图和运行状态（含单独 Cookie）
  archives/             归档包（ZIP）
```

备份或迁移时请保护凭据，并保留完整存档目录，包括所有隐藏的 `.state/`。

更新容器时，在 `deploy/compose` 目录执行：

```bash
docker compose pull
docker compose up -d
```

查看日志：

```bash
docker compose logs -f magma-viewer
```

**访问控制：**存档浏览和图片下载对访客开放，管理员登录仅保护管理操作。在后台关闭「在首页画板列表中显示」只是不在列表中展示，不是访问控制，知道链接的人仍可查看；已归档或删除的画板链接将无法访问。如果画板内容不应公开，请通过网络访问限制或带认证的反向代理保护整个服务。公网部署建议启用 HTTPS，并按实际代理配置 `trusted_proxies`；示例见 [Nginx 部署说明](https://github.com/MR-MonkeyRay/magma-viewer/blob/main/deploy/nginx/README.md)。

## 从单画板版本升级

- 升级前请先完整备份存档目录（Compose 为 `deploy/compose/images/`，systemd 为 `/var/lib/magma-viewer/images`）。
- 新版本首次启动时会自动把原有图片和运行状态移入 `boards/default/`，成为「默认画板」；不带 `?board=` 的旧图片链接和分享链接仍指向默认画板。
- 迁移后的目录不能直接交给旧版本使用。如需回退：停止服务，用升级前的备份恢复整个存档目录，再安装旧版本。
- systemd 安装脚本在升级后服务未能就绪时，若发现本次升级已完成迁移，不会自动回滚到旧版本，而是保留新版本并提示恢复步骤。

## Linux systemd 部署

适用于运行 systemd 的 Linux amd64 或 arm64 主机。安装脚本需要 root、Bash、curl、tar、sha256sum 和 useradd 等工具。

```bash
curl -fL https://github.com/MR-MonkeyRay/magma-viewer/releases/latest/download/install.sh -o install.sh &&
sudo bash install.sh --no-start
```

编辑 `/etc/magma-viewer/config.yaml`，填写 Cookie，画板地址可选（也可在管理后台添加）；首次安装时脚本会生成管理员密码及会话密钥。配置完成后启动：

```bash
sudo systemctl enable --now magma-viewer.service
sudo journalctl -u magma-viewer.service -f
```

默认只监听 `127.0.0.1:8080`，图片保存在 `/var/lib/magma-viewer/images`。安装脚本升级时保留已有配置和图片；服务默认只允许在 `/var/lib/magma-viewer` 和私有临时目录写入。

## 许可证

[GNU General Public License v3](./LICENSE)。
