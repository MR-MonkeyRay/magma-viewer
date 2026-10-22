# magma-viewer

Magma 画板存档站：定时抓取画板画面，只在内容变化时保存；按时间浏览历史快照，支持单张或打包下载。

[![Release](https://img.shields.io/github/v/release/MR-MonkeyRay/magma-viewer)](https://github.com/MR-MonkeyRay/magma-viewer/releases)
[![License](https://img.shields.io/github/license/MR-MonkeyRay/magma-viewer)](LICENSE)

Go 单二进制 · 前端内嵌 · 无数据库 · Docker Compose / systemd 部署

## 功能

- **自动存档**：按 cron 定时抓取，MD5 与上一张对比，画面变化才保存；重启后继续。
- **时间浏览**：按日分组的网格 / 列表视图，最新画面向前 24 小时的时间带回放，支持跨页切换。回放可选画质：流畅（480px 缩略图）、高清（1600px 压缩图，默认）或原图（原始文件）；帧会提前预加载，下一帧未就绪时短暂等待而不跳过。
- **大图预览**：缩放拖动、键盘与触屏操作，查看尺寸、格式等信息，复制原图链接、Markdown、HTML、BBCode。
- **下载**：单张原图 / 预览图、多选打包；访客最多 10 张，管理员不限并可打包全部。
- **管理**：立即抓取、暂停 / 恢复定时任务、发布 / 撤回公告、在线更新 Cookie（无需重启）、查看最近 50 次抓取记录与磁盘占用、告警 webhook 测试，登录后可用。
- **体验**：中英文界面、明暗主题、快捷键（按 `?` 查看）、响应式布局，尊重系统“减少动态效果”。

## 快速开始

### Docker Compose（推荐）

需要 Docker Engine 与 Compose v2。

```bash
git clone https://github.com/MR-MonkeyRay/magma-viewer.git
cd magma-viewer/deploy/compose
cp config.example.yaml config.yaml
vi config.yaml    # 至少填写 admin.password、magma.url、magma.cookie
docker compose up -d
```

访问 `http://<服务器>:8080`。配置和数据分别在 `config.yaml` 与 `images/`（容器内 `/data/images`）。端口默认映射到所有网卡，公网部署请限制访问或使用反向代理。

镜像默认 `ghcr.io/mr-monkeyray/magma-viewer:latest`；固定版本可在同目录 `.env` 设置 `MAGMA_VIEWER_TAG=<版本>`。GHCR 包为私有时先 `docker login ghcr.io`。

升级执行 `docker compose pull && docker compose up -d`；升级或迁移前备份 `config.yaml` 与 `images/`。

### Linux systemd

适用于使用 systemd 的 Linux x86_64 / aarch64，无需 Docker 或 Go，需要 root。

```bash
curl -fL https://github.com/MR-MonkeyRay/magma-viewer/releases/latest/download/install.sh -o install.sh && sudo bash install.sh
sudo vi /etc/magma-viewer/config.yaml    # 设置 magma.url、magma.cookie，查看管理员密码
sudo systemctl restart magma-viewer.service
```

安装器从 Release 下载固定版本压缩包，校验 `SHA256SUMS` 后才写入；首次安装生成随机管理员密码和会话密钥。默认只监听 `127.0.0.1:8080`，公网访问请配置 HTTPS 反向代理。

| 路径 | 用途 |
| --- | --- |
| `/usr/local/bin/magma-viewer` | 程序 |
| `/etc/magma-viewer/config.yaml` | 配置，升级时保留 |
| `/var/lib/magma-viewer/images` | 图片与缓存，升级时保留 |
| `/etc/systemd/system/magma-viewer.service` | systemd 单元 |

```bash
sudo systemctl status magma-viewer.service
sudo journalctl -u magma-viewer.service -f
sudo /usr/local/bin/magma-viewer -config /etc/magma-viewer/config.yaml healthcheck

# 升级到最新稳定版；指定版本可回退
sudo bash install.sh
sudo bash install.sh --version <版本>

# 首次安装时使用预先准备好的配置，且不启动服务
sudo bash install.sh --config ./config.yaml --no-start
```

已有配置不会被覆盖。服务使用专用用户 `magma-viewer`，持久写入仅限 `/var/lib/magma-viewer`（临时目录为私有 `/tmp`）；自定义单元参数用 `sudo systemctl edit magma-viewer.service`。卸载前执行 `sudo systemctl disable --now magma-viewer.service`，再删除程序和单元并 `sudo systemctl daemon-reload`；配置与图片按需保留或删除。

### HTTPS 反向代理

公网部署建议经 nginx 提供 HTTPS。将应用改为只监听 `127.0.0.1:8080`，由 nginx 负责 TLS 与登录限流，跨站请求防护由应用和 nginx 共同承担；应用依据 nginx 转发的 `X-Real-IP` 识别客户端（可用 `trusted_proxies` 限定来源）；配置与步骤见 [`deploy/nginx`](deploy/nginx/README.md)。

## 配置

配置文件路径通过 `-config` 参数或 `CONFIG` 环境变量指定，容器内默认 `/config/config.yaml`。`TZ`（镜像默认 `Asia/Shanghai`）决定定时执行时间；文件名中的时间在首次运行时按当时的 `TZ` 固定（见「抓取与存储」）。配置中拼错或未知的键会在启动日志中给出 WARNING（不会阻止启动）。可用 `magma-viewer -config <路径> checkconfig` 只校验配置、不启动服务（Docker 中为 `docker compose run --rm magma-viewer checkconfig`）。

完整样例：[`deploy/compose/config.example.yaml`](deploy/compose/config.example.yaml)（systemd 使用 [`deploy/systemd/config.example.yaml`](deploy/systemd/config.example.yaml)）。

| 配置项 | 说明 | 默认值 |
| --- | --- | --- |
| `listen` | HTTP 监听地址 | `:8080` |
| `trusted_proxies` | 可信反向代理列表（IP 或 CIDR）；仅采用这些来源转发的 `X-Real-IP` 作为客户端地址（用于登录限流），设为 `[]` 则不信任任何代理 | 本机与内网 |
| `admin.username` / `admin.password` | 管理员凭证（明文）；保留示例值 `change-me` 会拒绝启动 | 必填 |
| `admin.session_secret` | 会话签名密钥；留空则每次启动随机生成，重启后需重新登录；修改 `admin.password` 后已有登录全部失效 | 空 |
| `admin.session_ttl` | 登录有效期 | `168h` |
| `magma.url` | 画板渲染接口，返回图片或跳转到图片 | 必填 |
| `magma.cookie` | 原样放入 `Cookie` 请求头，可被管理面板中更新的 Cookie 覆盖 | 空 |
| `magma.headers` | 额外请求头（`User-Agent`、`Referer` 等） | 空 |
| `magma.timeout` | 单次下载超时 | `30s` |
| `schedule` | 标准 5 段 crontab，也支持 `@every 30s`、`@hourly` | `* * * * *` |
| `storage.dir` | 图片存储目录 | `/data/images` |
| `storage.temp_dir` | 下载临时目录 | `/tmp` |
| `notify.webhook` | 告警 webhook 地址，留空关闭 | 空 |
| `notify.format` | 告警请求体格式：`json` 或 `text` | `json` |

### Magma 地址与 Cookie

- 地址：`https://magma.com/api/render/<画板ID>/jpg`，`<画板ID>` 取自画板链接；接口会 302 跳转到渲染图，程序自动跟随。
- Cookie：只需 `AUTH_TOKEN`。登录 Magma 后，在开发者工具 → Application → Cookies → `magma.com` 复制其值：

  ```yaml
  cookie: "AUTH_TOKEN=eyJhbGciOi..."
  ```

  缺失或无效时接口通常返回 `403 Access denied`。`AUTH_TOKEN` 是 JWT，有效期由 Magma 决定（通常约 90 天）；启动日志显示到期时间，管理面板显示剩余天数，不足 7 天告警。可在管理面板直接粘贴新的 Cookie（完整 Cookie 头、`AUTH_TOKEN=...` 或只粘贴 token 值均可），立即生效并保存在 `storage.dir/.state/cookie.json`（权限 600）；之后若修改了配置文件中的 `magma.cookie`，以配置文件为准并删除 `cookie.json`。直接改配置文件仍需重启。

## 抓取与存储

每次计划触发（以及启动时；仅在未暂停且不在失败退避期内时执行）：

1. 带 Cookie 请求 `magma.url`（已有保存的图片时，附上次响应的 ETag / Last-Modified 发条件请求；上游返回 304 即视为未变化，跳过下载），流式写入 `storage.temp_dir` 并计算 MD5，单张上限 200 MiB。
2. 按文件头识别 JPEG / PNG / WebP / GIF，非图片（如失效 Cookie 返回的登录页）记为失败。
3. 仅与上一张已保存图片比较 MD5，不同才保存为 `YYYYMMDD-HHMMSS_<md5>.<ext>`。先写入 `.state/tmp` 并落盘（fsync）后再改名，断电不会留下截断的图片。

重启后从最新图片恢复 MD5。去重基准是上一张，因此 A → B → A 会保存三次。

- 暂停状态与最近获取时间保存在 `storage.dir/.state/fetcher.json`，重启后保持；公告在 `.state/notices.json`。
- 手动放入存储目录第一层的图片会被识别（目录变化每分钟检查一次，另外每 30 分钟强制完整对账一次）；时间优先取文件名前缀，否则取文件修改时间。
- 缩略图（长边 480px）与预览图（长边 1600px）缓存在 `.thumbs/`；均不放大，长边不超过 1600 的图片预览直接返回原图，GIF 预览使用原图；无法解码的图片在文件变化前不会重复尝试生成，直接返回原图。缺失缓存会在启动后后台补齐，也可按需生成。
- 首次运行时把当前 `TZ` 记录到 `storage.dir/.state/archive.json`，之后文件名中的时间始终按该时区写入和解析；迁移主机或修改 `TZ` 不会让历史时间整体偏移，启动日志会提示两者不一致。未设置 `TZ` 时按 `/etc/localtime` 指向的时区记录；无法确定时区时只在日志中提示、不记录；`archive.json` 损坏或其中时区无法加载时拒绝启动（修复或删除该文件后会按当前时区重新记录）。
- 连续失败时自第 3 次起暂停定时抓取 1 分钟，此后每次翻倍，最长 30 分钟；成功一次即恢复，手动“立即抓取”不受影响。同一错误持续出现时日志只在首次、每 60 次和恢复时记录。
- 最近 50 次抓取（时间、结果、错误类型、HTTP 状态、字节数、耗时、是否手动）保存在 `storage.dir/.state/history.json`；重启后连续失败次数与退避时间会从中恢复。错误类型：`network`（无法连接）、`timeout`（超时）、`auth`（401/403，Cookie 失效）、`http`（其他状态码）、`not_image`（返回的不是图片）、`too_large`（超过 200 MiB）、`save`（写入磁盘失败）。
- 配置 `notify.webhook` 后，以下事件各 POST 一次告警：Cookie 7 天内过期时每天一次（`cookie_expiring`）、连续 3 次抓取失败时（`fetch_failing`）、此后恢复时（`fetch_recovered`）。`json` 格式请求体为 `{"event","title","message","time"}`，`event` 取上述三者或 `test`；`text` 格式为纯文本“标题换行内容”，适合 ntfy 等直接展示正文的服务。发送失败只记录日志，不重试。
- 启动时清理超过 1 小时的写入残留（`*.part`、`.state/tmp`、下载临时文件）以及原图已不存在的缩略图和预览图。

## 权限

| 操作 | 访客 | 管理员 |
| --- | --- | --- |
| 浏览、预览、下载单张 | ✓ | ✓ |
| 多选打包下载 | 最多 10 张 | 不限 |
| 打包下载全部 | — | ✓ |
| 查看抓取状态（暂停、最近获取与下次抓取、最近结果与错误类型）与公告 | ✓ | ✓ |
| 立即抓取、暂停 / 恢复、管理公告 | — | ✓ |
| 在线更新 Cookie、测试告警、查看抓取记录与磁盘占用 | — | ✓ |

管理员通过右上角头像登录。同一 IP 自首次失败起 5 分钟内累计 10 次登录失败后，暂时拒绝登录。

## HTTP 接口

管理员接口需登录，未登录返回 401。

| 路由 | 说明 |
| --- | --- |
| `GET /api/images?page=1&size=48&sort=desc` | 分页列表；`size` 12–200；`sort=asc` 最早在前；`around=<文件名>` 返回包含该图的页；`date=YYYY-MM-DD&tz=<时区>` 返回该日所在页（按当前排序取该日第一张；无图的日期落到阅读方向上最近的一张）；响应中的 `focus` 为定位到的文件名。含任务状态与最新公告 |
| `GET /api/overview?tz=Asia/Shanghai` | 总数、字节数、首末时间、每日张数、最近 24 小时（以最新存档为终点，最多 400 张） |
| `POST /api/login` / `POST /api/logout` | 登录 / 退出 |
| `GET /api/status` / `POST /api/fetch` | 抓取状态（含最近抓取记录、Cookie 来源、磁盘占用）/ 立即抓取（管理员）；抓取失败仍返回 200，查看响应中的 `fetch.result` |
| `POST /api/schedule` | `{"paused": true}` 暂停或恢复（管理员） |
| `PUT /api/cookie` | `{"cookie": "..."}` 在线更新 Cookie（管理员） |
| `POST /api/notify/test` | 发送测试告警（管理员）；未配置 webhook 返回 409 |
| `GET /api/notices` | 已发布公告 |
| `GET /api/notices/all` | 全部公告，含草稿（管理员） |
| `POST /api/notices` / `PUT /api/notices/{id}` | 新建 / 修改；`publish=false` 存草稿或撤回（管理员） |
| `DELETE /api/notices/{id}` | 删除（管理员） |
| `GET /img/{name}` | 原图 |
| `GET /thumb/{name}` / `GET /preview/{name}` | 480px 缩略图 / 1600px 预览图 |
| `GET /download/{name}` | 下载原图 |
| `GET /download/zip?f=a&f=b` | 打包所选图片；访客最多 10 张；也可用 `POST` 表单提交 `f`，适合大量选择；加 `check=1`（GET 或 POST）只校验并返回 `204`，不发送压缩包，校验失败时返回与正式下载相同的 JSON 错误（`error`、`code`，如 `image_not_found`、`guest_zip_limit`） |
| `GET /download/all` | 打包全部（管理员）；同样支持 `?check=1`，压缩包为空时返回 `no_images` |
| `GET /readyz` | 就绪检查：存储可写时 200，否则 503；同时返回最近获取时间与连续失败次数 |
| `GET /healthz` | 存活检查 |

公告、`/api/schedule` 与 `/api/cookie` 接口要求 `Content-Type: application/json`。

## 本地开发

需要 Go，版本以 [`go.mod`](go.mod) 为准（`GOTOOLCHAIN=auto` 可自动获取）。

```bash
cp deploy/compose/config.example.yaml config.yaml
go test ./...
go run . -config ./config.yaml
```

本地运行时将 `storage.dir` 改为可写目录（如 `./data/images`），并确认 `listen` 端口可用。

目录：

```text
build/      镜像构建与 Release 打包脚本
deploy/     Compose、systemd、nginx 部署文件
internal/   Go 服务（抓取、存储、Web、公告）
web/        前端页面、脚本、样式与字体（编译时内嵌）
```

## 版本与发布

版本由 [`VERSION`](VERSION) 统一维护（无 `v` 前缀的 SemVer），递增并推送后由 [Release 工作流](.github/workflows/release.yml) 测试、打包并创建 `v<版本>` Release；资产为 Linux amd64 / arm64 压缩包、`SHA256SUMS`、`install.sh` 与 `VERSION`。已发布的 Release 不会被覆盖。

[GHCR 工作流](.github/workflows/docker.yml) 在分支推送时构建镜像：`main` 使用 `<版本>` 与 `latest`，其他分支使用 `<分支>-<7 位 commit>`。目标标签均已存在时跳过构建。

本地打包：

```bash
bash build/package.sh
(cd dist && sha256sum --check SHA256SUMS)
```

产物在已忽略的 `dist/`。

## 安全

- `config.yaml` 含真实 Cookie 和管理员密码，不要提交；仓库只保留 `config.example.yaml`，`.gitignore` 已排除本地配置、`.env` 和运行数据。
- API 与 ZIP 响应带 `Cache-Control: no-store`；公网部署按 [`deploy/nginx`](deploy/nginx/README.md) 配置 HTTPS 与登录限流（应用自身已设置安全响应头和跨站防护）。
- 归档图片（文件名含 md5）的原图、缩略图、预览图带一年期 `immutable` 缓存，手动放入的其他文件名缓存 1 天；页面、脚本、样式按内容哈希提供 ETag 并 gzip 压缩，每次访问重新验证（未变化返回 304）；API 的 JSON 响应在客户端支持时 gzip 压缩。
- 应用自身设置 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy` 与 CSP 响应头，并拒绝浏览器发起的跨站 POST / PUT / DELETE 请求，以及跨站触发的“打包下载全部”；在纯 HTTP 部署下以 Origin / Referer 判断来源，无法证明同源时拒绝。
- 停止服务时最多等待 8 秒，让进行中的下载和抓取完成（低于 Docker 默认 10 秒与 systemd 单元 15 秒的停止期限）。
- Release 的 `SHA256SUMS` 用于校验下载完整性，不替代对发布来源的信任。

## 许可证

[GPL-3.0](LICENSE)