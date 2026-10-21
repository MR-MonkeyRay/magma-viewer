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
deploy/compose/     从源码构建并部署（构建上下文为项目根目录）
deploy/minimal/     使用已有镜像的最小部署
.github/workflows/  GHCR 镜像发布工作流
```

本地配置 `config.yaml` 和图片数据 `data/` 由 `.gitignore` 排除。

## 部署

### docker compose（从源码构建）

```bash
cp deploy/compose/config.example.yaml config.yaml   # 已有配置可跳过
vi config.yaml                                      # 修改密码、magma 地址和 cookie
docker compose -f deploy/compose/docker-compose.yml up -d --build
```

在项目根目录执行，访问 `http://<服务器>:8080`。后续操作同样指定 `-f deploy/compose/docker-compose.yml`。国内网络可在该文件中打开 `GOPROXY` 构建参数。

### 最小部署（使用已有镜像）

```bash
docker build -f build/docker/Dockerfile -t magma-viewer:latest .   # 在项目根目录构建一次
cp -r deploy/minimal /opt/magma-viewer && cd /opt/magma-viewer
cp config.example.yaml config.yaml && vi config.yaml
docker compose up -d
```

图片保存在当前目录的 `./images`；源码部署保存在项目根目录的 `data/images`。

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

## 版本与镜像

版本由 [`VERSION`](VERSION) 统一维护，当前为 `0.1.1`。发布新版本时递增该文件并推送。

[Docker 工作流](.github/workflows/docker.yml) 在任意分支推送或手动触发时运行，使用 `GITHUB_TOKEN` 推送到 `ghcr.io/<owner>/<repo>`：

| 分支 | 标签 | 示例 |
|---|---|---|
| `main` | `VERSION` 原值 | `ghcr.io/mr-monkeyray/magma-viewer:0.1.1` |
| 其他分支 | `<分支名>-<7 位 commit>` | `ghcr.io/mr-monkeyray/magma-viewer:feature-login-abcdef0` |

分支名会转为合法的 Docker 标签；目标标签已存在时跳过构建，不覆盖已有镜像，也不推送 `latest`。因此 `main` 上再次发布需先递增 `VERSION`。

使用预构建镜像时，把 `deploy/minimal/docker-compose.yml` 的 `image` 改为上述完整地址即可。GHCR 包默认私有，匿名拉取需在包设置中改为 Public；私有包需先 `docker login ghcr.io`。

## 安全

`config.yaml` 可能包含真实 cookie 和管理员密码，不要提交。仓库只保留两个 `config.example.yaml` 占位配置；`.gitignore` 已排除本地配置、`.env`、运行数据、缩略图、构建与测试产物。