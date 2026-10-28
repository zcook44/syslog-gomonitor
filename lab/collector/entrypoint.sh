#!/bin/bash
# Start rsyslog, let the example senders create logs, then pin the files that
# must show a non-live state. The monitor starts after those fixtures exist.
set -uo pipefail

mkdir -p /var/log/remote /var/log/firewall /var/lib/rsyslog /var/lib/syslog-gomonitor /run

rsyslogd -n -f /etc/rsyslog.conf &
rsyslog_pid=$!

cleanup() {
  kill "$rsyslog_pid" 2>/dev/null || true
}
trap cleanup EXIT

ready=0
for _ in $(seq 1 50); do
  if ss -H -uln | grep -q ':514'; then
    ready=1
    break
  fi
  sleep 0.2
done
if [[ "$ready" -ne 1 ]]; then
  echo "lab: rsyslog did not open UDP 514" >&2
  exit 1
fi
echo "lab: rsyslog is listening"

edge=/var/log/remote/10.188.0.10
branch=/var/log/remote/10.188.0.18
mkdir -p "$edge" "$branch" /var/log/firewall
printf 'edge-router-01 rotated syslog\n' > "$edge/syslog.log.1"
printf 'edge-router-01 historical counters\n%.0s' {1..80} | gzip -c > "$edge/syslog.log-20261022.gz"
printf 'operator note, not a log\n' > "$edge/notes.txt"
printf 'state\n' > "$edge/syslog.log.offset"
dd if=/dev/zero bs=1024 count=64 status=none | tr '\0' 'x' > "$branch/syslog.log.1"

wait_nonempty() {
  local path="$1"
  local i
  for i in $(seq 1 45); do
    if [[ -s "$path" ]]; then
      return 0
    fi
    sleep 1
  done
  echo "lab: timed out waiting for $path" >&2
  return 1
}

wait_nonempty /var/log/remote/10.188.0.12/syslog.log && touch -d '1 hour ago' /var/log/remote/10.188.0.12/syslog.log
wait_nonempty /var/log/remote/10.188.0.19/syslog.log && touch -d '3 hours ago' /var/log/remote/10.188.0.19/syslog.log
wait_nonempty /var/log/remote/10.188.0.15/syslog.log && touch -d '1 hour ago' /var/log/remote/10.188.0.15/syslog.log
wait_nonempty /var/log/remote/10.188.0.17/syslog.log && touch -d '1 hour' /var/log/remote/10.188.0.17/syslog.log

if wait_nonempty /var/log/firewall/firewall-01.log; then
  cp /var/log/firewall/firewall-01.log /var/log/firewall/firewall-01.log.1
  : > /var/log/firewall/firewall-01.log
fi

if wait_nonempty /var/log/remote/10.188.0.10/syslog.log; then
  mkdir -p /var/log/remote/10.188.0.16
  ln -sfn /var/log/remote/10.188.0.10/syslog.log /var/log/remote/10.188.0.16/syslog.log
fi

# Untracked sources whose active name includes a real calendar date.
# Each series has several days so the dashboard can offer one convention
# and the monitor can follow the newest file after adoption.
seed_dated_series() {
  local dir="$1" host="$2" layout="$3"
  shift 3
  local kind day when path
  mkdir -p "$dir"
  local -a days
  if [[ "$layout" == "dashed" ]]; then
    days=(2026-10-26 2026-10-27 2026-10-28)
  else
    days=(20261026 20261027 20261028)
  fi
  for day in "${days[@]}"; do
    case "$day" in
      *26) when="2026-10-26 08:10:00" ;;
      *27) when="2026-10-27 08:10:00" ;;
      *) when="2026-10-28 16:50:00" ;;
    esac
    for kind in "$@"; do
      path="$dir/${host}-${day}-${kind}.log"
      printf 'lab %s %s %s\n' "$host" "$day" "$kind" > "$path"
      touch -d "$when" "$path"
    done
  done
}

seed_dated_series /var/log/remote/10.188.0.41 access-switch-05 compact syslog
seed_dated_series /var/log/remote/10.188.0.42 dist-switch-03 dashed syslog
seed_dated_series /var/log/remote/10.188.0.43 idf-switch-05 compact syslog messages
seed_dated_series /var/log/remote/10.188.0.44 wan-router-03 compact syslog
printf 'operator note, not a log\n' > /var/log/remote/10.188.0.44/notes.txt
printf 'rotated day\n' > /var/log/remote/10.188.0.44/wan-router-03-20261027-syslog.log.1
seed_dated_series /var/log/remote/10.188.0.45 wireless-02 dashed syslog messages
seed_dated_series /var/log/remote/10.188.0.46 vpn-gw-02 compact syslog
seed_dated_series /var/log/remote/10.188.0.47 ups-02 compact syslog
printf 'lab ups-02 20261025 syslog\n' > /var/log/remote/10.188.0.47/ups-02-20261025-syslog.log
touch -d '2026-10-25 08:10:00' /var/log/remote/10.188.0.47/ups-02-20261025-syslog.log
seed_dated_series /var/log/remote/10.188.0.48 branch-router-02 dashed syslog
seed_dated_series /var/log/remote/10.188.0.49 core-router-03 compact syslog
seed_dated_series /var/log/remote/10.188.0.50 lab-ap-02 compact syslog messages
seed_dated_series /var/log/remote mgmt-switch-01 compact syslog
seed_dated_series /var/log/remote printer-01 dashed syslog
seed_dated_series /var/log/remote badge-reader-01 compact syslog
seed_dated_series /var/log/remote hvac-01 dashed syslog
printf 'lab camera-nvr-01 20261028 syslog\n' > /var/log/remote/camera-nvr-01-20261028-syslog.log
touch -d '2026-10-28 16:50:00' /var/log/remote/camera-nvr-01-20261028-syslog.log
echo "lab: dated adoption fixtures are prepared"

echo "lab: example files are prepared"
trap - EXIT
exec /usr/local/bin/syslog-gomonitor serve \
  --config /etc/syslog-gomonitor/devices.json \
  --state /var/lib/syslog-gomonitor/sizes.json \
  --listen 0.0.0.0:8443 \
  --tls-cert /etc/syslog-gomonitor/tls/fullchain.pem \
  --tls-key /etc/syslog-gomonitor/tls/privkey.pem \
  --poll 2s \
  --size-interval 1h
