#!/usr/bin/env bash
set -euo pipefail
umask 027

usage() {
  cat <<'EOF'
Usage: install.sh [--version VERSION] [--repo OWNER/REPO] [--config FILE] [--no-start]

Download a Linux release, verify SHA-256, and install/upgrade magma-viewer.service.
Requires root, systemd, bash, curl, tar, sha256sum, and useradd (shadow-utils).

  --version VERSION  Install a specific version (with or without v); default: latest
  --repo OWNER/REPO  GitHub repository; default: MR-MonkeyRay/magma-viewer
  --config FILE      Initial config only; never overwrite an existing config
  --no-start         Install without enabling, starting, or restarting the service
  -h, --help         Show this help

Binary: /usr/local/bin/magma-viewer
Config: /etc/magma-viewer/config.yaml (root:magma-viewer, 0640)
Data:   /var/lib/magma-viewer/images
Existing configuration and images are preserved on upgrade. The last binary known to work is
kept as /usr/local/bin/magma-viewer.prev, and an upgrade that does not become ready within 30 seconds
is rolled back to it.
EOF
}

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
version=''
repo='MR-MonkeyRay/magma-viewer'
initial_config=''
start=true
while (($#)); do
  case "$1" in
    --version|--repo|--config)
      if (($# < 2)); then fail "Missing value for $1"; fi
      if [[ -z "$2" || "$2" == --* ]]; then fail "Missing value for $1"; fi
      case "$1" in
        --version) version=${2#v} ;;
        --repo) repo=$2 ;;
        --config) initial_config=$2 ;;
      esac
      shift 2
      ;;
    --no-start) start=false; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "Unknown argument: $1 (see --help)" ;;
  esac
done

[[ $(uname -s) == Linux ]] || fail 'Only Linux is supported'
[[ $(id -u) == 0 ]] || fail 'Run this script as root (sudo bash install.sh)'
[[ -d /run/systemd/system ]] || fail 'A running systemd system is required'
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail 'Invalid GitHub repository'
for tool in curl tar sha256sum install mktemp useradd getent od tr flock; do
  command -v "$tool" >/dev/null || fail "Required command not found: $tool"
done
command -v systemctl >/dev/null || fail 'systemctl is required'
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "Unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
esac
[[ -z "$initial_config" || -f "$initial_config" ]] || fail "Config not found: $initial_config"

# Two runs at once would interleave the binary swap and the kept version, so a second run refuses to start.
exec 9>/run/magma-viewer-install.lock
flock -n 9 || fail 'Another magma-viewer install is already running'

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fetch() {
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 300 --output "$2" "$1"
}
base="https://github.com/$repo/releases"
if [[ -z "$version" ]]; then
  fetch "$base/latest/download/VERSION" "$work/VERSION"
  version=$(<"$work/VERSION")
fi
number='(0|[1-9][0-9]*)'
prerelease='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
identifier='[0-9A-Za-z-]+'
[[ "$version" =~ ^$number\.$number\.$number(-$prerelease(\.$prerelease)*)?(\+$identifier(\.$identifier)*)?$ ]] || fail "Invalid release version: $version"
asset="magma-viewer_${version}_linux_${arch}.tar.gz"
url="$base/download/v$version"
printf 'Downloading %s from %s\n' "$asset" "$repo"
fetch "$url/$asset" "$work/$asset"
fetch "$url/SHA256SUMS" "$work/SHA256SUMS"

# Select only this archive: do not let other manifest paths read local files.
checksum=''
while read -r hash filename extra; do
  if [[ "$filename" == "$asset" ]]; then
    [[ -z "$checksum" && -z "$extra" && "$hash" =~ ^[[:xdigit:]]{64}$ ]] || fail 'Invalid or duplicate checksum entry'
    checksum=$hash
  fi
done < "$work/SHA256SUMS"
[[ -n "$checksum" ]] || fail "No checksum found for $asset"
printf '%s  %s\n' "$checksum" "$asset" > "$work/checksum"
(cd "$work" && sha256sum --check checksum)

# Require the flat package layout before extracting anything as root.
while IFS= read -r member; do
  case "$member" in
    magma-viewer|config.example.yaml|magma-viewer.service|install.sh|README.md|LICENSE|VERSION) ;;
    *) fail "Unexpected archive member: $member" ;;
  esac
done < <(tar -tzf "$work/$asset")
mkdir "$work/package"
tar --extract --gzip --file "$work/$asset" --directory "$work/package" --no-same-owner --no-same-permissions
for name in magma-viewer config.example.yaml magma-viewer.service VERSION; do
  [[ -f "$work/package/$name" && ! -L "$work/package/$name" ]] || fail "Missing or unsafe package file: $name"
done
[[ $(<"$work/package/VERSION") == "$version" ]] || fail 'Package VERSION does not match requested release'

# All download and integrity checks happen before modifying the installation.
if ! getent passwd magma-viewer >/dev/null; then
  useradd --system --user-group --home-dir /var/lib/magma-viewer --no-create-home --shell /usr/sbin/nologin magma-viewer
fi
getent group magma-viewer >/dev/null || fail 'The magma-viewer group is missing'
install -d -m 0755 /usr/local/bin
install -d -m 0750 -o root -g magma-viewer /etc/magma-viewer
install -d -m 0750 -o magma-viewer -g magma-viewer /var/lib/magma-viewer /var/lib/magma-viewer/images
new_config=false
if [[ ! -e /etc/magma-viewer/config.yaml ]]; then
  if [[ -n "$initial_config" ]]; then
    install -m 0640 -o root -g magma-viewer "$initial_config" /etc/magma-viewer/config.yaml
  else
    # Generate credentials without depending on openssl or a YAML tool.
    password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    while IFS= read -r line || [[ -n "$line" ]]; do
      case "$line" in
        '  password: '*) printf '  password: "%s"\n' "$password" ;;
        '  session_secret: '*) printf '  session_secret: "%s"\n' "$secret" ;;
        *) printf '%s\n' "$line" ;;
      esac
    done < "$work/package/config.example.yaml" > "$work/config.yaml"
    install -m 0640 -o root -g magma-viewer "$work/config.yaml" /etc/magma-viewer/config.yaml
    new_config=true
  fi
fi
install -m 0644 -o root -g magma-viewer "$work/package/config.example.yaml" /etc/magma-viewer/config.example.yaml

# Keep the last version known to work beside the new one; an upgrade that never becomes ready is rolled back to it.
# The marker stands for "the installed binary has not passed the readiness check yet" (left by --no-start or a
# failed upgrade), so such a binary never replaces the kept version.
bin=/usr/local/bin/magma-viewer
unit=/etc/systemd/system/magma-viewer.service
unverified=/usr/local/bin/.magma-viewer.unverified
if [[ -f "$bin" && ! -e "$unverified" ]]; then
  install -m 0755 "$bin" "$bin.prev"
  [[ -f "$unit" ]] && install -m 0644 "$unit" "$work/unit.prev"
fi
previous=false
[[ -f "$bin.prev" ]] && previous=true
touch "$unverified"
# Replace via rename so a running executable is never truncated in place.
install -m 0755 "$work/package/magma-viewer" /usr/local/bin/.magma-viewer.new
mv -f /usr/local/bin/.magma-viewer.new "$bin"
install -m 0644 "$work/package/magma-viewer.service" "$unit"
systemctl daemon-reload

# Ready means answering /healthz on the configured listen address, not merely running.
ready() {
  # A deadline rather than a round count: one healthcheck can itself take seconds when the service hangs.
  local deadline=$((SECONDS + 30))
  while ((SECONDS < deadline)); do
    if systemctl is-active --quiet magma-viewer.service \
      && "$bin" -config /etc/magma-viewer/config.yaml healthcheck >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}
if "$start"; then
  systemctl enable magma-viewer.service
  # A failed restart is handled like one that never becomes ready: set -e must not skip the rollback.
  if systemctl restart magma-viewer.service && ready; then
    rm -f "$unverified"
  else
    if ! "$previous"; then
      fail 'Service did not become ready; inspect journalctl -u magma-viewer.service -n 50'
    fi
    printf 'Version %s did not become ready; restoring the previous version.\n' "$version" >&2
    mv -f "$bin.prev" "$bin"
    [[ -f "$work/unit.prev" ]] && install -m 0644 "$work/unit.prev" "$unit"
    systemctl daemon-reload
    if systemctl restart magma-viewer.service && ready; then
      rm -f "$unverified"
      fail "Upgrade to $version rolled back; the previous version is running. Inspect journalctl -u magma-viewer.service -n 50"
    fi
    fail 'Upgrade failed and the previous version did not become ready either; inspect journalctl -u magma-viewer.service -n 50'
  fi
fi
printf '\nInstalled magma-viewer %s (%s).\nConfig: /etc/magma-viewer/config.yaml\nData: /var/lib/magma-viewer/images\n' "$version" "$arch"
if "$new_config"; then
  printf 'Edit config.yaml to set your Magma URL/cookie. The admin password and session secret were generated randomly.\n'
fi
if "$start"; then
  printf 'Service started. Default URL: http://127.0.0.1:8080 (or your configured listen address).\n'
else
  printf 'Service state unchanged. To start: systemctl enable --now magma-viewer.service\n'
fi
printf 'After configuration changes: systemctl restart magma-viewer.service\nLogs: journalctl -u magma-viewer.service -f\n'
