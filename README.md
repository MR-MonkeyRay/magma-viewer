# magma-viewer

一个超轻量的 Magma 画板存档站：后台按 crontab 计划用你的 cookie 抓取 magma 绘画图片，画面有变化才保存；前台按时间倒序分页浏览，访客可下载单张或多选打包，管理员可一键打包下载全部。

- Go 单二进制，前端内嵌，无数据库；Docker 镜像约 9 MB
- 支持 jpg / png / webp（兼容 gif）
- 网格 / 瀑布流 / 列表三种视图，可按日期分组，明暗主题，大图预览（← → 切换、Esc 关闭、手机左右滑动）
- 每张图生成 480px 缩略图和 1600px 预览图（缓存在 `<存储目录>/.thumbs/`），列表和大图预览不必加载 4K 原图；下载始终是原图
- 常驻内存约 50 MB（处理 4K 图片时短暂升高）

## 快速部署（docker compose）

```bash
cp config.example.yaml config.yaml   # 修改管理员密码、magma 地址和 cookie
docker compose up -d --build
# 打开 http://<服务器>:8080
```

国内网络构建可在 `docker-compose.yml` 中打开 `GOPROXY: https://goproxy.cn,direct` 构建参数。

### 最小化部署样例

`deploy/minimal/` 下是只包含必填项的 `config.yaml` 和直接使用镜像的 `docker-compose.yml`：

```bash
docker build -t magma-viewer:latest .        # 在项目根目录构建一次镜像
cp -r deploy/minimal /opt/magma-viewer && cd /opt/magma-viewer
vi config.yaml                               # 填写 url、cookie、密码
docker compose up -d
```

图片保存在宿主机的 `./images` 目录（根目录 compose 为 `./data/images`）。

## 配置

完整说明见 [`config.example.yaml`](config.example.yaml)。

| 配置项 | 说明 | 默认值 |
|---|---|---|
| `listen` | HTTP 监听地址 | `:8080` |
| `admin.username` / `admin.password` | 管理员登录凭证（明文） | 必填 |
| `admin.session_secret` | 登录会话签名密钥；留空则每次启动随机生成，重启后需重新登录 | 空 |
| `admin.session_ttl` | 登录有效期 | `168h` |
| `magma.url` | magma 绘画图片地址，需直接返回图片内容 | 必填 |
| `magma.cookie` | 原样放入 `Cookie` 请求头 | 空 |
| `magma.headers` | 额外请求头，如 `User-Agent`、`Referer` | 空 |
| `magma.timeout` | 单次下载超时 | `30s` |
| `schedule` | 标准 5 段 crontab，也支持 `@every 30s`、`@hourly` | `* * * * *`（每分钟） |
| `storage.dir` | 图片存储目录 | `/data/images` |
| `storage.temp_dir` | 下载临时目录 | `/tmp` |

配置文件路径通过 `-config` 参数或 `CONFIG` 环境变量指定，容器内默认 `/config/config.yaml`。时区由 `TZ` 环境变量决定（镜像默认 `Asia/Shanghai`），影响 crontab 计划和文件名中的时间。

### magma 地址与 cookie

- **图片地址**：`https://magma.com/api/render/<画板ID>/jpg`。接口会经过两次 302 跳转到渲染好的图片（实测为 3840×2160 的 JPEG，约 1–2 MB），程序会自动跟随跳转。
- **cookie**：只需要 `AUTH_TOKEN`。在浏览器登录 magma，打开开发者工具 → 应用（Application）→ Cookies → `magma.com`，复制 `AUTH_TOKEN` 的值，写成：

  ```yaml
  cookie: "AUTH_TOKEN=eyJhbGciOi..."
  ```

  不带 cookie 时接口返回 `403 Access denied`。`AUTH_TOKEN` 是 JWT，有效期约 90 天；程序启动时会解析过期时间并写入日志，剩余不足 7 天时告警，管理面板中也会显示剩余天数。过期后更新配置并重启：

  ```bash
  docker compose restart
  ```

## 抓取与去重

每次计划触发（启动时也会立即执行一次）：

1. 带 cookie 请求 `magma.url`，内容流式写入 `temp_dir` 下的临时文件，同时计算 md5；
2. 按文件头判断类型，不是图片（例如 cookie 失效返回的登录页）则放弃并记录错误；
3. 与**上一张已保存图片**的 md5 比较：相同则丢弃，不同则保存为 `YYYYMMDD-HHMMSS_<md5>.<ext>`。

新图保存后立即在后台生成缩略图；启动时也会为缺少缩略图的图片补齐（单线程，避免占满 CPU）。

重启后会从存储目录中最新的一张恢复“上一张”的 md5，不会重复保存。手动放入存储目录的图片也会被识别（每分钟扫描一次目录变化），时间取文件修改时间。

## 权限

| 操作 | 访客 | 管理员 |
|---|---|---|
| 浏览、预览、下载单张 | ✓ | ✓ |
| 多选打包下载 | 最多 10 张 | 不限 |
| 一键打包下载全部 | | ✓ |
| 查看抓取状态、立即抓取一次 | | ✓ |

管理员通过页面右上角“管理员登录”登录，连续输错 10 次后该 IP 需等待 5 分钟。

## 接口

| 路由 | 说明 |
|---|---|
| `GET /api/images?page=1&size=48&sort=desc` | 分页列表（`sort=asc` 为最早在前） |
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
go test ./...
go run . -config ./config.yaml
```

## 版本与镜像发布

项目版本由根目录的 [`VERSION`](VERSION) 文件统一维护，当前为 `0.1.0`。发布新版本时修改此文件并推送到 `main`。

[Docker 发布工作流](.github/workflows/docker.yml) 在任意分支推送时运行，也支持在 GitHub Actions 中手动触发；使用仓库自带的 `GITHUB_TOKEN` 登录并推送到 `ghcr.io/<owner>/<repo>`（镜像路径自动转为小写），无需另配 Docker Hub 凭证。

| 分支 | 镜像标签 | 示例 |
|---|---|---|
| `main` | `VERSION` 中的版本号，不加 `v` 前缀 | `ghcr.io/mr-monkeyray/magma-viewer:0.1.0` |
| 其他分支 | `<分支名>-<commit 前 7 位 hash>` | `ghcr.io/mr-monkeyray/magma-viewer:feature-login-abcdef0` |

分支名中的 `/` 等 Docker 标签不支持的字符会替换为 `-`，移除开头的 `.` 和 `-`，并截断以保证标签不超过 128 个字符；处理后为空时使用 `branch`。工作流先检查目标标签，已存在则跳过构建和推送，不覆盖已有镜像。因此 `main` 上后续提交若要发布，必须先递增 `VERSION`；不会额外推送 `latest` 标签。同一分支的发布任务串行执行，避免重复构建同一版本。

标签查询通过 GitHub Packages API 分页检查，包括私有包；首次发布、包不存在（404）时构建。权限不足、限流或网络等查询错误会直接使工作流失败，不会把查询失败误判为标签不存在。

拉取已发布版本：

```bash
docker pull ghcr.io/mr-monkeyray/magma-viewer:0.1.0
```

使用预构建镜像时，可将 `deploy/minimal/docker-compose.yml` 中的 `image` 改为上述完整地址，无需先在本地构建。GHCR 首次发布的包默认为私有；若需要匿名拉取，请在 GitHub 包设置中将可见性改为 Public。私有包需先执行 `docker login ghcr.io` 并使用具备 `read:packages` 权限的令牌。仓库的 Actions 也需允许写入 Packages；如组织策略限制该权限，需要先调整策略。

## 版本管理

本地 `config.yaml` 可能包含真实 cookie 和管理员凭证，不应提交。仓库只保留 `config.example.yaml` 和 `deploy/minimal/config.yaml` 中的占位配置；实际部署时先复制到仓库外再填写。

`.gitignore` 已排除本地配置、`.env` 环境文件、运行数据与缩略图、Go 构建和测试产物，以及常见编辑器和系统临时文件。提交前仍应检查待提交内容，避免将真实凭证写入示例配置或源码。
