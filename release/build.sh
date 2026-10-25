#!/bin/bash
# Build a self-contained linux/amd64 package for a server with no internet.
# Run this on a machine that has Go 1.22 or newer. The syslog server does not
# need Go, and the tarball does not download anything when it is installed.
set -euo pipefail

release_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$release_dir/.." && pwd)"
version="$(tr -d '[:space:]' < "$release_dir/VERSION")"
name="syslog-gomonitor-${version}-linux-amd64"
stage="$(mktemp -d)"
pkg="$stage/$name"

cleanup() { rm -rf "$stage"; }
trap cleanup EXIT

mkdir -p "$pkg/bin" "$pkg/config" "$pkg/examples" "$pkg/systemd" "$pkg/doc"

echo "building static $name"
(
  cd "$root"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o "$pkg/bin/syslog-gomonitor" ./cmd/syslog-gomonitor
)

if ! file "$pkg/bin/syslog-gomonitor" | grep -q 'statically linked'; then
  echo "refusing to pack a dynamically linked binary:" >&2
  file "$pkg/bin/syslog-gomonitor" >&2
  exit 1
fi

cp "$release_dir/install.sh" "$pkg/install.sh"
cp "$release_dir/VERSION" "$pkg/VERSION"
cp "$root/devices.example.json" "$pkg/config/devices.example.json"
cp "$root/examples/rsyslog-per-device.conf" "$pkg/examples/rsyslog-per-device.conf"
cp "$root/deploy/syslog-gomonitor.service" "$pkg/systemd/syslog-gomonitor.service"
cp "$root/deploy/syslog-gomonitor-size.service" "$pkg/systemd/syslog-gomonitor-size.service"
cp "$root/deploy/syslog-gomonitor-size.timer" "$pkg/systemd/syslog-gomonitor-size.timer"
cp "$root/docs/"*.md "$pkg/doc/"
chmod 0755 "$pkg/install.sh" "$pkg/bin/syslog-gomonitor"
chmod 0644 "$pkg/VERSION" "$pkg/config/devices.example.json" \
  "$pkg/examples/rsyslog-per-device.conf" "$pkg/systemd/"* "$pkg/doc/"*

(
  cd "$pkg"
  sha256sum bin/syslog-gomonitor > SHA256SUMS
)

rm -rf "$release_dir/$name" "$release_dir/$name.tar.gz" "$release_dir/$name.tar.gz.sha256"
tar -C "$stage" -czf "$release_dir/$name.tar.gz" "$name"
(
  cd "$release_dir"
  sha256sum "$name.tar.gz" > "$name.tar.gz.sha256"
)

echo "package: $release_dir/$name.tar.gz"
cat "$release_dir/$name.tar.gz.sha256"
file "$pkg/bin/syslog-gomonitor"
ls -lh "$pkg/bin/syslog-gomonitor" "$release_dir/$name.tar.gz"
