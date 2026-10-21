# nginx 反向代理

默认配置不依赖 Cloudflare：只需修改域名、证书路径和应用地址。保留登录限流、跨站 POST 防护、安全响应头与 ZIP 流式下载，不再按每个业务接口单独配置代理规则。

## 1. 应用只监听本机

systemd 部署在 `/etc/magma-viewer/config.yaml` 中设置：

```yaml
listen: "127.0.0.1:8080"
admin:
  session_secret: "<随机长字符串>"
```

Docker Compose 部署则将 `deploy/compose/compose.yaml` 的端口改为：

```yaml
ports:
  - "127.0.0.1:8080:8080"
```

容器内的应用仍监听 `:8080`，不要改成容器内的 `127.0.0.1:8080`。以上示例假设 nginx 运行在宿主机。

应用仅对本机或内网来源采用 `X-Real-IP`；nginx 会覆盖客户端传入的代理头。不要将应用端口暴露给公网或内网中不可信的机器。

## 2. 安装配置

在仓库根目录执行：

```bash
sudo mkdir -p /etc/nginx/magma-viewer
sudo cp deploy/nginx/magma-viewer.conf /etc/nginx/conf.d/magma-viewer.conf
sudo vi /etc/nginx/conf.d/magma-viewer.conf
```

修改以下内容：

- `viewer.example.com`：替换为实际域名，包括 HTTP 跳转地址。
- `ssl_certificate` / `ssl_certificate_key`：替换为已有证书路径，或将证书部署到示例中的路径。直接对外提供 HTTPS 时，使用浏览器信任的证书；Cloudflare Origin CA 仅适用于经 Cloudflare 代理的站点。
- `127.0.0.1:8080`：若修改了应用端口，更新两处 `proxy_pass`。

配置包含 `map` 和 `limit_req_zone`，必须在 nginx 的 `http {}` 中引入，不能粘贴到已有 `server {}` 中。发行版通常已在 `http {}` 中引入 `conf.d/*.conf`。本配置不声明 `default_server`，无需修改同机其他站点。

证书就绪后检查并重载：

```bash
sudo nginx -t && sudo systemctl reload nginx
```

## 3. 可选：Cloudflare

不使用 Cloudflare 时跳过本节。使用时：

1. DNS 记录开启代理（橙色云朵）。
2. SSL/TLS 选择 **完全（严格）** 并开启 **始终使用 HTTPS**；不要使用「灵活」模式。
3. 如使用 Origin CA，将证书与私钥放到配置指定路径，私钥权限设为 `600`。
4. 按下方步骤启用真实访客 IP，否则登录限流会按 Cloudflare 出口 IP 共享计数。

```bash
sudo cp deploy/nginx/update-cloudflare-ips.sh /etc/nginx/magma-viewer/
# 先生成列表，再启用 include，避免首次检查时缺少文件。
sudo env RELOAD=0 sh /etc/nginx/magma-viewer/update-cloudflare-ips.sh
sudo vi /etc/nginx/conf.d/magma-viewer.conf
# 取消 include /etc/nginx/magma-viewer/cloudflare-realip.conf; 的注释
sudo nginx -t && sudo systemctl reload nginx
printf '%s\n' '0 4 * * 1 root sh /etc/nginx/magma-viewer/update-cloudflare-ips.sh' | sudo tee /etc/cron.d/magma-viewer-cf-ips
```

`cloudflare-realip.conf` 是网段快照，更新脚本从 Cloudflare 官方下载 IPv4 / IPv6 网段，仅信任这些网段的 `CF-Connecting-IP`。自动更新时会先检查 nginx 配置，失败则恢复已有列表。

**源站保护**：此精简配置不再阻止非 Cloudflare 来源。需要防止绕过 CDN / WAF 时，在主机或云防火墙中仅允许 [Cloudflare 网段](https://www.cloudflare.com/ips/) 访问源站 80/443，并随官方网段更新规则。启用前确认 SSH 等管理入口不受影响。

**缓存**：默认遵循应用响应头，不开启全站「缓存所有内容」。API 与 ZIP 下载为 `no-store`，页面和脚本为 `no-cache`。若自定义缓存规则，必须绕过 `/api/*`、`/download/all` 和 `/download/zip`；`/api/images` 随登录状态变化，不能缓存。

**脚本与 WAF**：关闭 Rocket Loader；若启用 Web Analytics 自动注入，在 CSP 的 `script-src` 中加入 `https://static.cloudflareinsights.com`，在 `connect-src` 中加入 `https://cloudflareinsights.com`。管理接口由页面通过 fetch 调用，WAF 规则应选「阻止」而非交互式「质询」。大批量下载与立即抓取仍受 Cloudflare 套餐的回源超时限制，nginx 的 300 秒读超时不会延长 CDN 上限。

## 4. 从旧配置迁移

替换配置时保留实际域名、证书路径和应用地址；使用 Cloudflare 的站点仍需启用真实 IP 的 include。

- 删除旧配置的 `geo`、管理 IP 白名单、按接口限流区、默认拒绝站点与分路径代理；只保留登录限流和两个代理 location。
- 不再使用 `cloudflare-geo.conf`；确认其他配置未引用后，可删除服务器上的旧文件。新更新脚本只维护 `cloudflare-realip.conf`。
- 若原来依赖 nginx 阻止源站直连或限制管理 IP，切换前将这些策略配置到防火墙 / Cloudflare WAF；精简配置不会自动保留它们。
- HSTS、额外响应头及压缩不再由本示例设置；若此前启用了 HSTS，浏览器会继续执行到期前的策略，必须持续提供有效 HTTPS。

## 5. 验证

将域名替换为实际值：

```bash
curl -I http://viewer.example.com/                   # 301 跳转 HTTPS
curl -i https://viewer.example.com/healthz           # 200，正文 ok
curl -I https://viewer.example.com/api/images        # Cache-Control: no-store
curl -i -X POST -H 'Origin: https://evil.example' \
  https://viewer.example.com/api/logout             # 403
```

最后在浏览器登录、加载图片并打包下载，确认会话与 ZIP 正常。登录接口按 IP 每分钟 6 次、突发 5 次，超出返回 429；应用仍执行自身的登录失败限制和管理会话校验。
