# magma-viewer

Magma 画板存档站：定时抓取画板图片，内容有变化才保存；按时间分页浏览，支持单张或打包下载。

## 特性

- Go 单二进制，前端内嵌，无数据库；`scratch` 镜像
- 支持 jpg / jpeg / png / webp / gif，按 md5 去重
- 自动生成 480px 缩略图与 1600px 预览图（缓存在存储目录的 `.thumbs/`），下载始终为原图
- 网格 / 瀑布流 / 列表视图，按日分组，明暗主题，大图预览（← → 切换、Esc 关闭、手机滑动）
- 访客最多打包 10 张；管理员可下载全部、查看抓取状态、立即抓取

## 目录

```text
build/docker/       Dockerfile 与构建上下文忽略规则
build/package.sh    Linux amd64 / arm64 Release 打包脚本
deploy/compose/     使用 GHCR 镜像部署（配置与数据在本目录）
deploy/systemd/     Linux 原生服务、配置样例与 Release 安装脚本
.github/workflows/  GHCR 镜像与 Linux GitHub Release 发布工作流
```

本地配置、`.env` 与运行数据由 `.gitignore` 排除。

## 部署

### docker compose（使用 GHCR 镜像）

```bash
cd deploy/compose
cp config.example.yaml config.yaml   # 已有配置可跳过
vi config.yaml                       # 修改密码、magma 地址和 cookie
docker compose up -d
```

在 `deploy/compose` 下执行，配置为同目录的 `config.yaml`，图片保存在同目录的 `data/images`。镜像默认 `ghcr.io/mr-monkeyray/magma-viewer:latest`；需要固定版本时设置 `MAGMA_VIEWER_TAG=<版本>`（或写入同目录 `.env`，将 `<版本>` 替换为所需的已发布版本）。访问 `http://<服务器>:8080`。私有包需先 `docker login ghcr.io`。

### Linux systemd（从 GitHub Release 一键安装）

适用于使用 systemd 的 Linux x86_64 / aarch64 主机，无需 Docker 或 Go。需安装 `bash`、`curl`、CA 证书、`tar`、coreutils 与提供 `useradd` 的账户管理工具；安装操作需要 root。

```bash
# 下载最新稳定版安装器并执行；&& 保证下载失败时不会执行旧脚本
curl -fL https://github.com/MR-MonkeyRay/magma-viewer/releases/latest/download/install.sh -o install.sh && sudo bash install.sh
sudo vi /etc/magma-viewer/config.yaml       # 设置 Magma 地址、cookie，查看/修改管理员密码
sudo systemctl restart magma-viewer.service
```

安装器解析 Release 的 `VERSION`，按架构下载固定 `v<版本>` 的压缩包，校验 `SHA256SUMS` 后才修改安装目录；不在服务器编译。首次生成随机管理员密码和会话密钥，配置权限为 `root:magma-viewer 0640`，不会把凭证打印到终端。默认仅监听 `127.0.0.1:8080`；远程访问推荐配置 HTTPS 反向代理，也可自行修改 `listen` 并设置防火墙。Magma 占位地址和 cookie 必须编辑后才能成功抓取。

| 路径 | 用途 |
|---|---|
| `/usr/local/bin/magma-viewer` | 程序 |
| `/etc/magma-viewer/config.yaml` | 运行配置，升级时保留 |
| `/etc/magma-viewer/config.example.yaml` | 随版本更新的配置样例 |
| `/var/lib/magma-viewer/images` | 图片、缩略图与预览缓存，升级时保留 |
| `/etc/systemd/system/magma-viewer.service` | systemd 单元，升级时更新 |

服务使用专用非登录用户 `magma-viewer`，启动后自动运行并设为开机启动，失败时自动重启；日志写入 journal。时区默认 `Asia/Shanghai`。配置中的持久存储路径应位于 `/var/lib/magma-viewer` 内，服务沙箱禁止写入其他持久目录，临时文件可使用私有 `/tmp`。

```bash
sudo systemctl status magma-viewer.service
sudo journalctl -u magma-viewer.service -f
sudo /usr/local/bin/magma-viewer -config /etc/magma-viewer/config.yaml healthcheck

# 再次执行安装器即升级；固定版本或回退时，将 <版本> 替换为所需的已发布版本（可带 v 前缀）
sudo bash install.sh --version '<版本>'

# 首次安装时使用事先编辑好的配置，先安装不启动
sudo bash install.sh --config ./config.yaml --no-start
sudo systemctl enable --now magma-viewer.service
```

已有配置永不覆盖，`--config` 仅在首次创建配置时生效。`--no-start` 不启用、启动或重启服务，已有运行进程保持原状；需要时手动重启加载新程序。`--repo OWNER/REPO` 可选择遵循相同资产格式的公开 fork。升级会替换单元文件，自定义服务参数请用 `sudo systemctl edit magma-viewer.service` 创建 drop-in；仓库根目录的本地配置和数据不会被迁移或修改。

卸载时先 `sudo systemctl disable --now magma-viewer.service`，再删除程序及单元并执行 `sudo systemctl daemon-reload`。配置、图片和服务账户默认保留，按需备份后自行删除。

## 配置

完整说明见 [`deploy/compose/config.example.yaml`](deploy/compose/config.example.yaml)。配置路径通过 `-config` 参数或 `CONFIG` 环境变量指定，容器内默认 `/config/config.yaml`。`TZ` 环境变量（镜像默认 `Asia/Shanghai`）决定计划执行时间和文件名中的时间。

| 配置项 | 说明 | 默认值 |
|---|---|---|
| `listen` | HTTP 监听地址 | `:8080` |
| `admin.username` / `admin.password` | 管理员凭证（明文） | 必填 |
| `admin.session_secret` | 会话签名密钥；留空则每次启动随机生成，重启后需重新登录 | 空 |
| `admin.session_ttl` | 登录有效期 | `168h` |
| `magma.url` | 画板渲染接口，需直接返回图片 | 必填 |
| `magma.cookie` | 原样放入 `Cookie` 请求头 | 空 |
| `magma.headers` | 额外请求头（`User-Agent`、`Referer` 等） | 空 |
| `magma.timeout` | 单次下载超时 | `30s` |
| `schedule` | 标准 5 段 crontab，也支持 `@every 30s`、`@hourly` | `* * * * *` |
| `storage.dir` | 图片存储目录 | `/data/images` |
| `storage.temp_dir` | 下载临时目录 | `/tmp` |

### magma 地址与 cookie

- 地址：`https://magma.com/api/render/<画板ID>/jpg`，`<画板ID>` 取自画板链接。接口会 302 跳转到渲染图片，程序自动跟随。
- cookie：只需 `AUTH_TOKEN`。浏览器登录 magma 后，在开发者工具 → 应用 → Cookies → `magma.com` 复制其值：

  ```yaml
  cookie: "AUTH_TOKEN=eyJhbGciOi..."
  ```

  缺少或无效 cookie 时接口返回 `403 Access denied`。`AUTH_TOKEN` 是有效期约 90 天的 JWT；启动日志与管理面板会显示剩余天数，不足 7 天时告警。更新配置后重启容器生效。

## 抓取与去重

每次计划触发（启动时立即执行一次）：

1. 带 cookie 请求 `magma.url`，流式写入 `temp_dir` 下的临时文件并计算 md5；
2. 按文件头判断图片类型，非图片（如 cookie 失效返回的登录页）则放弃并记录错误；
3. 与上一张已保存图片的 md5 比较：相同则丢弃，不同则保存为 `YYYYMMDD-HHMMSS_<md5>.<ext>`。

重启后从最新图片恢复 md5，不会重复保存。手动放入存储目录的图片会被识别（每分钟检查目录变化），时间取文件修改时间。新图保存后及启动时为缺失的图片补齐缩略图。

## 权限

| 操作 | 访客 | 管理员 |
|---|---|---|
| 浏览、预览、下载单张 | ✓ | ✓ |
| 多选打包下载 | 最多 10 张 | 不限 |
| 打包下载全部 | | ✓ |
| 查看抓取状态、立即抓取 | | ✓ |

管理员通过页面右上角登录；同一 IP 连续失败 10 次后需等待 5 分钟。

## 接口

| 路由 | 说明 |
|---|---|
| `GET /api/images?page=1&size=48&sort=desc` | 分页列表，`size` 范围 12–200，`sort=asc` 为最早在前 |
| `GET /img/{name}` | 原图 |
| `GET /thumb/{name}` / `GET /preview/{name}` | 480px 缩略图 / 1600px 预览图 |
| `GET /download/{name}` | 下载单张 |
| `GET /download/zip?f=a&f=b` | 打包下载所选图片 |
| `GET /download/all` | 打包下载全部（管理员） |
| `POST /api/login` / `POST /api/logout` | 登录 / 退出 |
| `GET /api/status` / `POST /api/fetch` | 抓取状态 / 立即抓取（管理员） |
| `GET /healthz` | 健康检查 |

## 本地开发

```bash
cp deploy/compose/config.example.yaml config.yaml   # 已有配置可跳过
go test ./...
go run . -config ./config.yaml
```

## 版本、镜像与 Release

版本由 [`VERSION`](VERSION) 统一维护，发布新版本时递增该文件并推送；本文中的 `<版本>` 均为占位符，使用时替换为所需的已发布版本。

### Linux Release

[Release 工作流](.github/workflows/release.yml) 在 `main` 推送时自动检查 `VERSION`，也可在 Actions 中针对 `main` 手动执行。未发布的版本先运行 `go test ./...`，再交叉构建静态 Linux 二进制、打包并创建 `v<版本>` 标签及 GitHub Release，发布说明由 GitHub 自动生成。无需额外 PAT；只有发布 job 获得 `contents: write`，仓库策略须允许该权限。

Release 资产：

- `magma-viewer_<版本>_linux_amd64.tar.gz`
- `magma-viewer_<版本>_linux_arm64.tar.gz`
- `SHA256SUMS`、`install.sh`、`VERSION`

每个压缩包包含二进制、`config.example.yaml`、`magma-viewer.service`、安装脚本、README、LICENSE 与 VERSION。已发布版本直接跳过，不覆盖已有资产；失败上传保留草稿，可重跑原工作流完成发布。已有版本标签必须指向该次工作流的源码，否则拒绝发布；若 `main` 已前进，请重跑原提交的工作流或递增版本。带预发布后缀的版本发布为 prerelease，不作为默认安装的最新稳定版。

本地构建（Go 工具链版本以 [`go.mod`](go.mod) 为准，需支持自动获取模块工具链）：

```bash
bash build/package.sh
(cd dist && sha256sum --check SHA256SUMS)
```

产物在已忽略的 `dist/`，不会包含本地 `config.yaml`、cookie 或运行数据。首次使用安装命令前，需先将以上工作流推送到 `main` 并成功发布至少一个稳定 Release；没有 Release 时下载地址会返回 404。

### GHCR 镜像

[Docker 工作流](.github/workflows/docker.yml) 在任意分支推送或手动触发时运行，使用 `GITHUB_TOKEN` 推送到 `ghcr.io/<owner>/<repo>`：

| 分支 | 标签 | 示例 |
|---|---|---|
| `main` | `VERSION` 原值与 `latest` | `ghcr.io/mr-monkeyray/magma-viewer:<版本>`、`ghcr.io/mr-monkeyray/magma-viewer:latest` |
| 其他分支 | `<分支名>-<7 位 commit>` | `ghcr.io/mr-monkeyray/magma-viewer:feature-login-abcdef0` |

分支名会转为合法的 Docker 标签；目标标签全部已存在时跳过构建，不覆盖已有镜像。`main` 推送会同时更新 `VERSION` 标签和 `latest`，因此再次发布需先递增 `VERSION`。

GHCR 包默认私有，匿名拉取需在包设置中改为 Public；私有包需先 `docker login ghcr.io`。

## 安全

`config.yaml` 可能包含真实 cookie 和管理员密码，不要提交。仓库只保留 `config.example.yaml` 占位配置；`.gitignore` 已排除本地配置、`.env`、运行数据、缩略图、构建与测试产物。安装包的 SHA-256 校验用于发现下载损坏，不替代对 GitHub 仓库和 Release 发布者的信任。