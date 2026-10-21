#!/bin/sh
# 从 Cloudflare 官方地址重新生成 magma-viewer.conf 引用的两个 IP 列表，校验通过后重载 nginx。
#
#   用法：update-cloudflare-ips.sh [输出目录]          默认 /etc/nginx/magma-viewer
#   定时：0 4 * * 1 root /etc/nginx/magma-viewer/update-cloudflare-ips.sh
#   RELOAD=0 只生成文件，不检查、不重载 nginx。
#
# cloudflare-realip.conf  set_real_ip_from 列表：只有来自这些地址的 CF-Connecting-IP 才被当作访客 IP
# cloudflare-geo.conf     geo 列表：判断连接是否来自 Cloudflare，用于拒绝绕过 CDN 的直连
set -eu

dir=${1:-/etc/nginx/magma-viewer}
reload=${RELOAD:-1}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for v in 4 6; do
  curl -fsS --max-time 20 --retry 3 "https://www.cloudflare.com/ips-v$v" | tr -d '\r' | awk 'NF' >"$tmp/v$v"
done
cat "$tmp/v4" "$tmp/v6" >"$tmp/all"

# 空列表或异常内容会让所有访客被拒绝或 IP 识别错误，宁可保留旧文件。
if grep -Evq '^[0-9A-Fa-f:.]+/[0-9]{1,3}$' "$tmp/all" || [ "$(wc -l <"$tmp/all")" -lt 10 ]; then
  echo "update-cloudflare-ips: unexpected response from cloudflare.com, keeping current lists" >&2
  exit 1
fi

stamp="# 由 update-cloudflare-ips.sh 生成于 $(date -u '+%Y-%m-%d %H:%M UTC')，请勿手动修改。"
{
  echo "$stamp"
  awk '{ print "set_real_ip_from " $0 ";" }' "$tmp/all"
  echo "real_ip_header CF-Connecting-IP;"
} >"$tmp/cloudflare-realip.conf"
{
  echo "$stamp"
  awk '{ print $0 " 1;" }' "$tmp/all"
} >"$tmp/cloudflare-geo.conf"

mkdir -p "$dir"
for f in cloudflare-realip.conf cloudflare-geo.conf; do
  [ -f "$dir/$f" ] && cp "$dir/$f" "$tmp/$f.bak"
  cp "$tmp/$f" "$dir/.$f.new" && mv "$dir/.$f.new" "$dir/$f"
done

[ "$reload" = 0 ] && exit 0
if nginx -t -q; then
  nginx -s reload
else
  echo "update-cloudflare-ips: nginx -t failed, restoring previous lists" >&2
  for f in cloudflare-realip.conf cloudflare-geo.conf; do
    [ -f "$tmp/$f.bak" ] && cp "$tmp/$f.bak" "$dir/$f"
  done
  exit 1
fi
