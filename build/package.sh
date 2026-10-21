#!/usr/bin/env bash
set -euo pipefail

# Resolve from this file so callers can invoke it outside the repository root.
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
version=$(<VERSION)
number='(0|[1-9][0-9]*)'
prerelease='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
identifier='[0-9A-Za-z-]+'
if [[ ! "$version" =~ ^$number\.$number\.$number(-$prerelease(\.$prerelease)*)?(\+$identifier(\.$identifier)*)?$ ]]; then
  printf 'VERSION must contain a semantic version without a v prefix: %q\n' "$version" >&2
  exit 1
fi

# Fail before building if a release would lack its real deployment files.
for source in deploy/systemd/install.sh deploy/systemd/magma-viewer.service \
  deploy/systemd/config.example.yaml README.md LICENSE VERSION; do
  if [[ ! -s "$source" ]]; then
    printf 'Required package input is missing or empty: %s\n' "$source" >&2
    exit 1
  fi
done

mkdir -p dist
work=$(mktemp -d "$root/dist/.package.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/package"
install -m 0755 deploy/systemd/install.sh "$work/package/install.sh"
install -m 0644 deploy/systemd/magma-viewer.service deploy/systemd/config.example.yaml \
  README.md LICENSE VERSION "$work/package/"

# The go directive in go.mod selects the required toolchain, including when a
# developer invokes this with an older Go installation supporting toolchains.
export GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux
archives=()
for arch in amd64 arm64; do
  archive="magma-viewer_${version}_linux_${arch}.tar.gz"
  GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$work/package/magma-viewer" .
  chmod 0755 "$work/package/magma-viewer"
  tar -czf "$work/$archive" -C "$work/package" \
    magma-viewer config.example.yaml magma-viewer.service install.sh README.md LICENSE VERSION
  archives+=("$archive")
done

(cd "$work" && sha256sum "${archives[@]}" > SHA256SUMS)
# Only expose final outputs once both architectures built successfully.
for archive in "${archives[@]}"; do
  install -m 0644 "$work/$archive" "$root/dist/$archive"
done
install -m 0644 "$work/SHA256SUMS" "$root/dist/SHA256SUMS"
install -m 0755 "$work/package/install.sh" "$root/dist/install.sh"
install -m 0644 "$work/package/VERSION" "$root/dist/VERSION"
printf 'Packaged version %s in %s/dist\n' "$version" "$root"
