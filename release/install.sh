#!/bin/bash
# Install syslog-gomonitor from this unpacked release directory.
# The script does not download packages, modules, or images.
set -euo pipefail

package_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
destdir=""
no_start=0
do_uninstall=0
purge=0
log_dirs=()

usage() {
  cat <<'EOF'
Usage: install.sh [--log-dir PATH]... [--no-start] [--uninstall] [--purge] [--destdir DIR]

Install the files in this directory onto a syslog server. Run as root.
The script asks for the syslog directories to watch and saves them in
devices.json. Edit log_directories in that file to change them later.
Device entries already in the inventory are kept.

  --log-dir    Absolute syslog directory to watch. Repeat for each directory.
               Without this flag, an interactive install asks for the paths.
  --no-start   Install files and reload systemd, but do not enable or start
  --uninstall  Stop the service and remove the program and units
  --purge      With --uninstall, also remove the inventory and size snapshot
  --destdir    Install under DIR instead of /. Skips systemd. For layout checks.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --log-dir)
      [[ $# -ge 2 ]] || { echo "install.sh: --log-dir needs a directory" >&2; exit 2; }
      log_dirs+=("$2")
      shift 2
      ;;
    --no-start) no_start=1; shift ;;
    --uninstall) do_uninstall=1; shift ;;
    --purge) purge=1; shift ;;
    --destdir)
      [[ $# -ge 2 ]] || { echo "install.sh: --destdir needs a directory" >&2; exit 2; }
      destdir="${2%/}"
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! -x "$package_dir/bin/syslog-gomonitor" ]]; then
  echo "install.sh: bin/syslog-gomonitor is missing. Unpack the release tarball and run install.sh from inside it." >&2
  exit 1
fi

if [[ -z "$destdir" && "$(id -u)" -ne 0 ]]; then
  echo "install.sh: run as root on the syslog server" >&2
  exit 1
fi

owner=()
if [[ -z "$destdir" ]]; then
  owner=(-o root -g root)
fi

bin_path="$destdir/usr/local/bin/syslog-gomonitor"
unit_dir="$destdir/etc/systemd/system"
config_dir="$destdir/etc/syslog-gomonitor"
doc_dir="$destdir/usr/local/share/doc/syslog-gomonitor"
state_dir="$destdir/var/lib/syslog-gomonitor"
config_path="$config_dir/devices.json"

remove_units() {
  rm -f \
    "$unit_dir/syslog-gomonitor.service" \
    "$unit_dir/syslog-gomonitor-size.service" \
    "$unit_dir/syslog-gomonitor-size.timer"
}

if [[ "$do_uninstall" -eq 1 ]]; then
  if [[ -z "$destdir" ]]; then
    systemctl disable --now syslog-gomonitor.service 2>/dev/null || true
    systemctl disable --now syslog-gomonitor-size.timer 2>/dev/null || true
    systemctl disable --now syslog-gomonitor-size.service 2>/dev/null || true
  fi
  rm -f "$bin_path"
  remove_units
  rm -rf "$doc_dir"
  if [[ "$purge" -eq 1 ]]; then
    rm -rf "$config_dir" "$state_dir"
  fi
  if [[ -z "$destdir" ]]; then
    systemctl daemon-reload
  fi
  echo "syslog-gomonitor removed."
  if [[ "$purge" -eq 0 ]]; then
    echo "Kept $config_dir and $state_dir. Use --purge to delete them."
  fi
  exit 0
fi

if [[ -f "$package_dir/SHA256SUMS" ]]; then
  (
    cd "$package_dir"
    sha256sum -c SHA256SUMS
  )
fi

version="unknown"
if [[ -f "$package_dir/VERSION" ]]; then
  version="$(tr -d '[:space:]' < "$package_dir/VERSION")"
fi

if [[ -z "$destdir" ]] && ! command -v systemctl >/dev/null 2>&1; then
  echo "install.sh: systemctl is required" >&2
  exit 1
fi
if ! command -v ss >/dev/null 2>&1; then
  echo "install.sh: warning: ss was not found. TCP devices will show Unknown until iproute is installed from local media." >&2
fi

install "${owner[@]}" -d -m 0755 "$destdir/usr/local/bin" "$doc_dir" "$doc_dir/examples"
install "${owner[@]}" -d -m 0750 "$config_dir"
install "${owner[@]}" -m 0755 "$package_dir/bin/syslog-gomonitor" "$bin_path"
install "${owner[@]}" -d -m 0755 "$unit_dir"
install "${owner[@]}" -m 0644 "$package_dir/systemd/syslog-gomonitor.service" "$unit_dir/syslog-gomonitor.service"
install "${owner[@]}" -m 0644 "$package_dir/systemd/syslog-gomonitor-size.service" "$unit_dir/syslog-gomonitor-size.service"
install "${owner[@]}" -m 0644 "$package_dir/systemd/syslog-gomonitor-size.timer" "$unit_dir/syslog-gomonitor-size.timer"
install "${owner[@]}" -m 0644 "$package_dir/config/devices.example.json" "$config_dir/devices.example.json"
install "${owner[@]}" -m 0644 "$package_dir/examples/rsyslog-per-device.conf" "$doc_dir/examples/rsyslog-per-device.conf"
install "${owner[@]}" -m 0644 "$package_dir/doc/"*.md "$doc_dir/"

fresh=0
if [[ ! -e "$config_path" ]]; then
  fresh=1
fi

collect_log_dirs() {
  if [[ ${#log_dirs[@]} -gt 0 ]]; then
    return
  fi
  if [[ ! -t 0 ]]; then
    if [[ "$fresh" -eq 1 ]]; then
      log_dirs+=("/var/log/remote")
      echo "install.sh: no --log-dir and no terminal. Using /var/log/remote."
      echo "Edit log_directories in $config_path after installation to change it."
    fi
    return
  fi
  if [[ "$fresh" -eq 0 ]]; then
    local answer=""
    read -r -p "Update syslog directories in the existing inventory? [y/N] " answer || answer=""
    if [[ ! "$answer" =~ ^[Yy]$ ]]; then
      return
    fi
  fi
  echo "Syslog directories to watch."
  echo "These paths are saved as log_directories in $config_path."
  echo "Edit that file after installation to add, remove, or rename them."
  echo "Enter one absolute directory per line. An empty line finishes the list."
  local first=1 line=""
  while true; do
    if [[ "$first" -eq 1 ]]; then
      read -r -p "Directory [/var/log/remote]: " line || line=""
      if [[ -z "$line" ]]; then
        line="/var/log/remote"
      fi
      first=0
    else
      read -r -p "Directory (empty line to finish): " line || break
      if [[ -z "$line" ]]; then
        break
      fi
    fi
    log_dirs+=("$line")
  done
}

collect_log_dirs
if [[ ${#log_dirs[@]} -gt 0 ]]; then
  configure_args=(configure --config "$config_path")
  for dir in "${log_dirs[@]}"; do
    configure_args+=(--log-dir "$dir")
  done
  if ! "$bin_path" "${configure_args[@]}"; then
    echo "install.sh: could not save the syslog directories. The inventory was not changed." >&2
    exit 1
  fi
elif [[ "$fresh" -eq 1 ]]; then
  echo "install.sh: no syslog directories were provided" >&2
  exit 1
fi

if ! "$bin_path" validate --config "$config_path"; then
  echo "install.sh: inventory did not validate. The program is installed, but it was not started." >&2
  exit 1
fi

if [[ -n "$destdir" ]]; then
  echo "Laid out syslog-gomonitor $version under $destdir"
  echo "Inventory: $config_path"
  exit 0
fi

systemctl daemon-reload
if [[ "$no_start" -eq 1 ]]; then
  echo "Installed syslog-gomonitor $version without starting it."
else
  systemctl enable syslog-gomonitor.service
  systemctl restart syslog-gomonitor.service
  sleep 1
  if ! timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/8080 && printf "GET /healthz HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n" >&3 && grep -q "ok" <&3'; then
    echo "install.sh: service did not answer on 127.0.0.1:8080" >&2
    systemctl --no-pager --full status syslog-gomonitor.service >&2 || true
    journalctl -u syslog-gomonitor.service -n 40 --no-pager >&2 || true
    exit 1
  fi
  echo "syslog-gomonitor $version is running."
  echo "Dashboard: http://127.0.0.1:8080/"
fi

echo "Inventory: $config_path"
echo "Docs: $doc_dir"
echo "Journal: journalctl -u syslog-gomonitor.service"
if [[ "$fresh" -eq 1 ]]; then
  echo "A starter inventory was installed. Replace the 192.0.2.x example addresses before treating the page as production."
fi
if grep -q '192\.0\.2\.' "$config_path"; then
  echo "warning: $config_path still contains documentation addresses in 192.0.2.0/24."
fi
echo "The hourly size walk runs inside this service. Enable syslog-gomonitor-size.timer only after changing the service to --size-interval 0s."
