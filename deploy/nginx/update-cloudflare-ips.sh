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

# 每份列表单独下载、单独校验：任一份失败、为空或格式不对都保留旧文件，
# 以免一份正常的列表掩盖另一份的问题（sh 的管道不会因 curl 失败而失败）。
# check 文件 格式 最少行数
check() {
  if [ ! -s "$1" ] || grep -Evq "$2" "$1" || [ "$(wc -l <"$1")" -lt "$3" ]; then
    echo "update-cloudflare-ips: unexpected $(basename "$1") list from cloudflare.com, keeping current list" >&2
    exit 1
  fi
}
for v in 4 6; do
  if ! curl -fsS --max-time 20 --retry 3 -o "$tmp/raw$v" "https://www.cloudflare.com/ips-v$v"; then
    echo "update-cloudflare-ips: download of ips-v$v failed, keeping current list" >&2
    exit 1
  fi
  tr -d '\r' <"$tmp/raw$v" | awk 'NF' >"$tmp/v$v"
done
octet='(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])'
check "$tmp/v4" "^($octet\\.){3}$octet/([0-9]|[12][0-9]|3[0-2])\$" 5
check "$tmp/v6" '^[0-9A-Fa-f]{1,4}(:[0-9A-Fa-f]{0,4})+/([0-9]|[1-9][0-9]|1[01][0-9]|12[0-8])$' 3
# 正则只限定字符和前缀长度；这里再检查组数、"::" 至多一次且不出现 ":::"，以及首尾没有单个冒号。
if ! awk -F/ '{
  a = $1; d = gsub(/::/, "&", a)
  if (d > 1 || a ~ /:::/ || a ~ /^:[^:]/ || a ~ /[^:]:$/) exit 1
  n = 0; k = split(a, g, ":"); for (i = 1; i <= k; i++) if (g[i] != "") n++
  if (d ? n > 7 : n != 8) exit 1
}' "$tmp/v6"; then
  echo "update-cloudflare-ips: unexpected v6 list from cloudflare.com, keeping current list" >&2
  exit 1
fi
cat "$tmp/v4" "$tmp/v6" >"$tmp/all"

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
