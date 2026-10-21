#!/bin/sh
# 更新可选的 Cloudflare 真实 IP 配置，校验通过后重载 nginx。
#
#   用法：update-cloudflare-ips.sh [输出目录]          默认 /etc/nginx/magma-viewer
#   定时：0 4 * * 1 root /etc/nginx/magma-viewer/update-cloudflare-ips.sh
#   RELOAD=0 只生成文件，不检查、不重载 nginx。
#
# 只有来自 Cloudflare 网段的 CF-Connecting-IP 才被当作访客 IP。
set -eu

dir=${1:-/etc/nginx/magma-viewer}
reload=${RELOAD:-1}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for v in 4 6; do
  curl -fsS --max-time 20 --retry 3 "https://www.cloudflare.com/ips-v$v" | tr -d '\r' | awk 'NF' >"$tmp/v$v"
done
cat "$tmp/v4" "$tmp/v6" >"$tmp/all"

# 空列表或异常内容会导致 IP 识别错误，宁可保留旧文件。
if grep -Evq '^[0-9A-Fa-f:.]+/[0-9]{1,3}$' "$tmp/all" || [ "$(wc -l <"$tmp/all")" -lt 10 ]; then
  echo "update-cloudflare-ips: unexpected response from cloudflare.com, keeping current list" >&2
  exit 1
fi

stamp="# 由 update-cloudflare-ips.sh 生成于 $(date -u '+%Y-%m-%d %H:%M UTC')，请勿手动修改。"
{
  echo "$stamp"
  awk '{ print "set_real_ip_from " $0 ";" }' "$tmp/all"
  echo "real_ip_header CF-Connecting-IP;"
} >"$tmp/cloudflare-realip.conf"

mkdir -p "$dir"
file=cloudflare-realip.conf
[ -f "$dir/$file" ] && cp "$dir/$file" "$tmp/$file.bak"
cp "$tmp/$file" "$dir/.$file.new" && mv "$dir/.$file.new" "$dir/$file"

[ "$reload" = 0 ] && exit 0
if nginx -t -q; then
  nginx -s reload
else
  echo "update-cloudflare-ips: nginx -t failed, restoring previous list" >&2
  [ -f "$tmp/$file.bak" ] && cp "$tmp/$file.bak" "$dir/$file"
  exit 1
fi
