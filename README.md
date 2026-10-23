# magma-viewer

定时存档 Magma 画板，按时间浏览、对比和下载历史快照。

[![Release](https://img.shields.io/github/v/release/MR-MonkeyRay/magma-viewer)](https://github.com/MR-MonkeyRay/magma-viewer/releases)
[![License](https://img.shields.io/github/license/MR-MonkeyRay/magma-viewer)](LICENSE)

Go 单二进制，前端内嵌，无需数据库。支持 Docker Compose 和 Linux systemd 部署。

## 功能

- **自动存档**：按 cron 定时抓取，与上一张文件比较 MD5，不同才保存；重启后继续去重。
- **历史浏览**：按日分组、日期跳转、网格 / 列表视图、时间带回放，支持图片定位链接。
- **预览与对比**：缩放拖动、滑块对比、近似差异高亮，支持复制链接和嵌入代码。
- **下载与导出**：单张下载、多选 ZIP、管理员打包全部及浏览器延时视频导出。
- **在线管理**：立即抓取、暂停 / 恢复、更新 Cookie、发布公告、查看抓取记录与磁盘占用；支持 webhook 告警。
- **多端使用**：中英文、明暗主题、键盘快捷键与触屏操作。

**图片默认公开。** 管理员登录不限制图片访问；私密站点需在入口另加认证或访问限制。

## 快速部署

### Docker Compose（推荐）

需要 Docker Engine 与 Compose v2。

```bash
git clone https://github.com/MR-MonkeyRay/magma-viewer.git
cd magma-viewer/deploy/compose
cp config.example.yaml config.yaml
vi config.yaml  # 修改 admin.password，填写 magma.url、magma.cookie

docker compose up -d
docker compose logs -f
```

画板地址与 Cookie 的获取方法见[下文](#获取画板地址与-cookie)。启动后访问 `http://<服务器地址>:8080`，右上角头像可登录管理，示例用户名为 `admin`。

- **配置**：当前目录 `config.yaml`，只读挂载到容器。
- **数据**：当前目录 `images/`，对应容器内 `/data/images`。修改容器内存储路径时需同步调整挂载。
- **镜像**：`ghcr.io/mr-monkeyray/magma-viewer:latest`，支持 amd64 / arm64。`latest` 随 `main` 非预发布版本提交更新，不等同于固定 Release；可在同目录 `.env` 设置 `MAGMA_VIEWER_TAG=<版本号>` 固定版本，普通版本号不带 `v`。

默认端口映射到所有网卡。公网部署使用 HTTPS 反向代理，配置见[下文](#公网访问)。

在 Compose 目录升级；固定版本时先修改 `.env` 中的标签：

```bash
docker compose pull && docker compose up -d
```

### Linux systemd

适用于运行 systemd 的 Linux amd64 / arm64，需要 root、curl、tar、sha256sum 等系统工具，无需 Docker 或 Go。

```bash
curl -fL https://github.com/MR-MonkeyRay/magma-viewer/releases/latest/download/install.sh -o install.sh
sudo bash install.sh
sudo vi /etc/magma-viewer/config.yaml  # 填写 magma.url、magma.cookie，查看管理员密码
sudo systemctl restart magma-viewer.service
sudo journalctl -u magma-viewer.service -f
```

安装器校验 SHA-256 后安装，以专用用户 `magma-viewer` 运行。首次安装自动生成管理员密码和会话密钥，默认仅监听 `127.0.0.1:8080`。

| 路径 | 用途 |
| --- | --- |
| `/usr/local/bin/magma-viewer` | 程序 |
| `/etc/magma-viewer/config.yaml` | 配置 |
| `/var/lib/magma-viewer/images` | 图片与运行状态 |

重新下载并运行安装器可升级到最新稳定版，`--version <版本号>` 可指定版本。已有配置和数据不会覆盖；默认升级会做存活检查，失败时尝试回滚上一版程序。更多参数见 `bash install.sh --help`。

服务默认仅允许持久写入 `/var/lib/magma-viewer`。自定义数据路径需同时通过 `sudo systemctl edit magma-viewer.service` 调整写入权限。

### 公网访问

使用 [nginx 部署指南](deploy/nginx/README.md)配置 HTTPS、登录限流和可信代理：

- **systemd**：保持应用监听 `127.0.0.1:8080`。
- **Compose**：将宿主机端口映射改为 `127.0.0.1:8080:8080`；容器内仍监听 `:8080`。

以上假设 nginx 运行在同一宿主机。

## 配置

完整配置见 [Compose 样例](deploy/compose/config.example.yaml)或 [systemd 样例](deploy/systemd/config.example.yaml)。以下为主要配置项：

| 配置项 | 用途 |
| --- | --- |
| `admin.username` / `admin.password` | 管理员凭证，均不能为空；密码为 `change-me` 会拒绝启动 |
| `admin.session_secret` | 固定会话签名密钥；留空则每次重启需重新登录 |
| `magma.url` / `magma.cookie` | 画板渲染接口与登录凭证，获取方法见下节 |
| `schedule` | 默认每分钟抓取；支持 5 段 cron、`@every 30s`、`@hourly` |
| `storage.dir` / `storage.temp_dir` | 图片与下载临时目录，均需可写 |
| `notify.webhook` / `notify.format` | 可选告警，格式为 `json`（默认）或 `text`；通知 Cookie 即将过期、连续抓取失败和恢复 |

配置路径优先使用 `-config`，其次为 `CONFIG` 环境变量，默认 `/config/config.yaml`。修改配置文件后需重启；定时任务的执行时区由 `TZ` 决定，部署样例使用 `Asia/Shanghai`。

可在 Compose 目录校验配置；此命令不验证 Cookie 或上游可用性：

```bash
docker compose run --rm magma-viewer checkconfig
```

### 获取画板地址与 Cookie

1. 从 Magma 画板链接取得画板 ID，填写渲染地址：
   ```yaml
   magma:
     url: "https://magma.com/api/render/<画板ID>/jpg"
     cookie: "AUTH_TOKEN=<你的 token>"
   ```
2. 登录 Magma，在浏览器开发者工具 → Application（应用）→ Cookies → `magma.com` 中复制 `AUTH_TOKEN` 的值。
3. 启动后登录管理面板，点击“立即抓取”，确认图片成功保存。

Cookie 有效期由 Magma 决定，可识别 JWT 时会显示到期时间。管理面板支持粘贴完整 Cookie、`AUTH_TOKEN=...` 或 token 值，保存后无需重启；再点击“立即抓取”验证凭证。

在线更新保存在数据目录的 `.state/cookie.json`，**不会写回 YAML**。之后若修改 YAML 中的 `magma.cookie`，下次启动以新配置为准，清除在线覆盖值。

## 使用与限制

- **权限**：访客每次最多打包 10 张；管理员不限张数，可打包全部，并管理抓取、Cookie、公告和告警。
- **回放**：范围为最新存档向前 24 小时，最多 400 张；可选等间隔或按真实间隔比例回放。
- **对比**：大图可与上一张对比，多选恰好两张可指定对比；差异高亮基于缩小后的预览图，不是原图级精确检测。
- **延时视频**：管理员可导出上述回放范围或所选图片，由浏览器本地录制，格式取决于浏览器支持。需保持页面前台，耗时约等于视频时长，图片加载慢时更久。
- **去重**：只与上一张已保存文件比较，A → B → A 会保存三张；重新编码后的相同画面也可能再次保存。
- **快捷操作**：按 `?` 查看快捷键；触屏长按缩略图进入多选。

## 运维与排障

**升级或迁移前，备份 `config.yaml` 和整个数据目录，包含隐藏的 `.state/`。** 其中保存在线 Cookie、归档时区、暂停状态、抓取记录和公告；`.thumbs/` 仅为可重建缓存。配置和备份含凭证，不要提交或公开。

首次运行前确定 `TZ`。归档时区记录在 `.state/archive.json`，迁移时应保留；之后修改 `TZ` 不会改变已有文件名的时间基准。页面时间按浏览器时区显示。

| 现象 | 检查与处理 |
| --- | --- |
| 抓取返回 401 / 403 | 更新 Magma Cookie，再手动抓取验证 |
| 服务运行但没有新图片 | 先看管理面板抓取记录；文件未变化不会保存，暂停状态重启后仍保留 |
| 连续失败后抓取变慢 | 第 3 次失败起退避 1 分钟，逐次翻倍至 30 分钟；成功后恢复，手动抓取不受影响 |
| 图片保存失败 | 检查磁盘空间、数据目录属主和写入权限，以及容器挂载 / systemd 写入限制 |
| 重启后需要重新登录 | 设置固定 `admin.session_secret`；修改管理员密码也会使旧会话失效 |

`/healthz` 仅检查服务存活，`/readyz` 检查存储可写；返回成功不代表上游抓取成功。抓取故障以管理面板、日志和 webhook 告警为准。

## 本地开发与发布

Go 版本以 [`go.mod`](go.mod) 为准。前端编译时内嵌，无需 npm 安装或前端构建。

在仓库根目录执行：

```bash
cp deploy/compose/config.example.yaml config.yaml
vi config.yaml  # 修改密码、画板地址和 Cookie；storage.dir 改为 ./data/images，确认端口可用
go run . -config ./config.yaml
```

测试与本地打包：

```bash
go test ./...
bash build/package.sh
```

打包输出到 `dist/`，包含 Linux amd64 / arm64 压缩包和校验文件；完整检查见 [CI 工作流](.github/workflows/ci.yml)。

发布时递增 [`VERSION`](VERSION)（无 `v` 前缀的 SemVer）并推送到 `main`。[Release 工作流](.github/workflows/release.yml)在 CI 通过后创建 `v<版本号>` Release，已发布版本不覆盖；[镜像工作流](.github/workflows/docker.yml)负责发布 GHCR 镜像。

## 反馈与许可证

问题和建议请提交 [GitHub Issues](https://github.com/MR-MonkeyRay/magma-viewer/issues)，附部署方式、版本与相关日志，移除 Cookie、密码等敏感信息。

[GPL-3.0](LICENSE)
