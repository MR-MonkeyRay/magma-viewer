# nginx + Cloudflare 部署

访客 → Cloudflare（CDN / WAF）→ nginx（TLS、来源校验、限流）→ magma-viewer（会话校验、登录失败限制）

| 文件 | 安装位置 | 说明 |
|---|---|---|
| `magma-viewer.conf` | `/etc/nginx/conf.d/magma-viewer.conf` | 站点配置，含 http 级的 map / geo / 限流区 |
| `cloudflare-realip.conf` | `/etc/nginx/magma-viewer/` | Cloudflare 回源网段 → 采用 `CF-Connecting-IP` 作为访客 IP |
| `cloudflare-geo.conf` | `/etc/nginx/magma-viewer/` | 同一份网段，用于拒绝不经过 Cloudflare 的直连 |
| `update-cloudflare-ips.sh` | `/etc/nginx/magma-viewer/` | 从 cloudflare.com 重新生成上面两个文件，校验通过后重载 nginx |

两个 IP 列表是生成时的快照，部署时请先执行一次更新脚本，并加入定时任务。

## 1. 应用配置

在 `config.yaml` 中：

```yaml
listen: "127.0.0.1:8080"        # 只监听本机，公网只能经 nginx 访问
admin:
  session_secret: "<随机长字符串>" # 固定密钥，重启后不必重新登录
```

使用 docker compose 时，端口改为只绑定本机：`ports: ["127.0.0.1:8080:8080"]`，否则 8080 会绕过 nginx 直接暴露在公网。

请求来自本机或内网地址（nginx、docker 网桥）时，应用按 `X-Real-IP` 识别访客 IP，登录失败限制按访客分别计算；nginx 总会用 Cloudflare 给出的访客 IP 覆盖该头，客户端无法伪造。应用不要直接暴露给内网中不可信的机器。

## 2. 安装 nginx 配置

```bash
sudo mkdir -p /etc/nginx/magma-viewer
sudo cp update-cloudflare-ips.sh /etc/nginx/magma-viewer/
sudo cp magma-viewer.conf /etc/nginx/conf.d/
sudo vi /etc/nginx/conf.d/magma-viewer.conf          # 替换 viewer.example.com 等占位

# Cloudflare 面板 → SSL/TLS → 源服务器 → 创建证书，保存为下面两个文件
sudo vi /etc/nginx/magma-viewer/origin.pem
sudo vi /etc/nginx/magma-viewer/origin.key && sudo chmod 600 /etc/nginx/magma-viewer/origin.key

sudo /etc/nginx/magma-viewer/update-cloudflare-ips.sh   # 生成 IP 列表并重载 nginx
echo '0 4 * * 1 root /etc/nginx/magma-viewer/update-cloudflare-ips.sh' | sudo tee /etc/cron.d/magma-viewer-cf-ips
```

需要 nginx ≥ 1.25.1（`http2 on`），更早的版本按配置中的注释改用 `listen 443 ssl http2`。本机已有其他 `default_server` 时，删除配置里拒绝未知域名的两个 server 块。

## 3. Cloudflare 设置

**DNS**：站点记录开启代理（橙色云朵）。

**SSL/TLS**

- 加密模式选 **完全（严格）**，源站使用上一步的 Origin CA 证书。不要用「灵活」：Cloudflare 到源站会变成明文。
- 开启 **始终使用 HTTPS**，最低 TLS 版本 1.2。
- HSTS 已在 nginx 中设置。如改在 Cloudflare 开启，删除 nginx 中的 `Strict-Transport-Security`。
- 可选：**Authenticated Origin Pulls**。开启后取消配置中 `ssl_verify_client` 两行的注释，源站只接受 Cloudflare 的 TLS 连接。

**缓存规则**（缓存 → Cache Rules，按顺序创建）

1. 图片长缓存
   - 条件：`(starts_with(http.request.uri.path, "/thumb/") or starts_with(http.request.uri.path, "/preview/") or starts_with(http.request.uri.path, "/img/") or starts_with(http.request.uri.path, "/download/")) and not http.request.uri.path in {"/download/all" "/download/zip"}`
   - 操作：符合缓存条件；边缘 TTL 忽略源站，30 天；浏览器 TTL 遵循源站（1 天）。
   - 文件名包含 md5，内容不会变化。从存储目录删除图片后，需要在 Cloudflare 按 URL 清除缓存。
2. 接口与打包下载不缓存
   - 条件：`starts_with(http.request.uri.path, "/api/") or http.request.uri.path in {"/download/all" "/download/zip"}`
   - 操作：绕过缓存。
   - 应用已对这些响应返回 `Cache-Control: no-store`，这条规则是双重保险。`/api/images` 的内容随登录状态不同，绝不能被缓存。

不要开启全站「缓存所有内容」。页面、`app.js`、`app.css` 由应用返回 `no-cache`，Cloudflare 每次回源校验，升级后无需清缓存。

**速度 / 优化**：关闭 **Rocket Loader**。它会改写脚本标签，被 CSP 拦截。开启 Web Analytics 自动注入时，按 nginx 配置中 CSP 的注释放行它的域名。

**安全 → WAF**

- 速率限制规则：`http.request.uri.path eq "/api/login" and http.request.method eq "POST"`，按 IP 每 10 秒 5 次，阻止 10 秒（免费版可建 1 条）。
- 可选自定义规则：管理入口只允许特定国家或 IP，例如 `http.request.uri.path in {"/api/login" "/api/status" "/api/fetch" "/api/schedule" "/download/all"} and not ip.src in {203.0.113.10}` → 阻止。
  - 这些路径由页面内的 fetch 调用，无法完成交互式质询，所以操作应选「阻止」，不要选「质询」。
- 「我受到攻击」模式和 Bot Fight Mode 会质询请求。如果访客反馈列表加载失败或不刷新，先检查这两项。

**超时**：Cloudflare 等待源站响应最长 100 秒。「立即抓取」最长约 `magma.timeout + 10s`，`magma.timeout` 不要超过 80 秒。打包下载边打包边传输，不受影响。

## 4. 防护一览

| 威胁 | Cloudflare | nginx | 应用 |
|---|---|---|---|
| 绕过 CDN 直连源站 | 仅代理流量 | 非 Cloudflare 来源、未知域名、按 IP 访问直接断开（444） | 只监听本机 |
| 伪造访客 IP | 生成 `CF-Connecting-IP` | 仅信任 Cloudflare 网段的该头，覆盖客户端自带的 `X-Real-IP` / `X-Forwarded-For` | 仅对本机 / 内网来源采用 `X-Real-IP` |
| 暴力破解登录 | WAF 速率限制 | `/api/login` 每 IP 6 次/分钟（突发 5），超出返回 429 | 每 IP 5 分钟内失败 10 次即拒绝 |
| 管理接口滥用 | 可选 IP / 国家规则 | 限流；可选 IP 白名单（`$mv_admin_allowed`） | 会话 cookie（HMAC，HttpOnly、Secure、SameSite=Lax） |
| 跨站请求伪造 | — | POST 要求 Origin 与 Host 一致，拒绝 `Sec-Fetch-Site: cross-site / same-site` | `/api/schedule` 要求 JSON |
| 打包下载耗尽资源 | — | 每 IP 频率与并发限制 | 访客最多 10 张 |
| 缓存泄露登录状态 | 绕过缓存规则 | — | API 与打包响应 `no-store` |
| 点击劫持、XSS 放大 | — | CSP（`script-src 'self'`）、`X-Frame-Options`、`nosniff` | 输出转义 |

## 5. 验证

```bash
D=viewer.example.com
curl -sI https://$D/ | grep -iE 'strict-transport|content-security|cf-cache-status'
curl -sI https://$D/api/images | grep -i cache-control                               # no-store
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Origin: https://evil.example' https://$D/api/logout   # 403
for i in $(seq 8); do curl -s -o /dev/null -w '%{http_code} ' -X POST https://$D/api/login; done; echo    # 后几次 429
curl -sk -o /dev/null -w '%{http_code}\n' --resolve $D:443:<源站IP> https://$D/            # 000：直连被拒绝
```

缩略图第二次请求的响应头中，`cf-cache-status` 应为 `HIT`。
