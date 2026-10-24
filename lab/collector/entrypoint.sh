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

edge=/var/log/remote/10.88.0.10
branch=/var/log/remote/10.88.0.18
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

wait_nonempty /var/log/remote/10.88.0.12/syslog.log && touch -d '1 hour ago' /var/log/remote/10.88.0.12/syslog.log
wait_nonempty /var/log/remote/10.88.0.19/syslog.log && touch -d '3 hours ago' /var/log/remote/10.88.0.19/syslog.log
wait_nonempty /var/log/remote/10.88.0.15/syslog.log && touch -d '1 hour ago' /var/log/remote/10.88.0.15/syslog.log
wait_nonempty /var/log/remote/10.88.0.17/syslog.log && touch -d '1 hour' /var/log/remote/10.88.0.17/syslog.log

if wait_nonempty /var/log/firewall/firewall-01.log; then
  cp /var/log/firewall/firewall-01.log /var/log/firewall/firewall-01.log.1
  : > /var/log/firewall/firewall-01.log
fi

if wait_nonempty /var/log/remote/10.88.0.10/syslog.log; then
  mkdir -p /var/log/remote/10.88.0.16
  ln -sfn /var/log/remote/10.88.0.10/syslog.log /var/log/remote/10.88.0.16/syslog.log
fi

echo "lab: example files are prepared"
trap - EXIT
exec /usr/local/bin/syslog-gomonitor serve \
  --config /etc/syslog-gomonitor/devices.json \
  --state /var/lib/syslog-gomonitor/sizes.json \
  --listen 0.0.0.0:8080 \
  --poll 2s \
  --size-interval 1h
