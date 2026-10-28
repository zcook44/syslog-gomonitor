#!/bin/sh
# Send example syslog to the lab collector. MODE selects the traffic pattern.
set -u

HOST="${SYSLOG_HOST:-collector}"
PORT="${SYSLOG_PORT:-514}"
DEVICE="${DEVICE:-lab-device}"
MODE="${MODE:-udp-heartbeat}"
INTERVAL="${INTERVAL:-5}"

message() {
  ts=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  case "$DEVICE" in
    edge-router-01) text="%LINK-3-UPDOWN: Interface GigabitEthernet0/0, changed state to up" ;;
    core-switch-01) text="%LINK-3-UPDOWN: Interface GigabitEthernet1/0/24, changed state to up" ;;
    dist-switch-02) text="%SPANTREE-5-TOPOTRAP: Topology change for VLAN 20, then silence" ;;
    firewall-01) text="%FW-4-DENY: inbound tcp 203.0.113.10:443 denied by policy edge" ;;
    wan-router-02) text="%BGP-5-ADJCHANGE: neighbor 10.188.0.50 Up" ;;
    core-router-02) text="%BGP-5-ADJCHANGE: neighbor 10.188.0.51 Down, session remains" ;;
    dmz-firewall-01) text="%FW-4-DENY: clock step will leave this file in the future" ;;
    branch-router-01) text="%SYS-5-CONFIG_I: Configured from console by admin" ;;
    idf-switch-04) text="%LINK-3-UPDOWN: Interface GigabitEthernet1/0/1, changed state to down" ;;
    vpn-gw-01) text="%VPN-5-SESSION: tunnel to 198.51.100.10 established" ;;
    wireless-01) text="%DOT11-6-ASSOC: station 02:00:00:00:00:21 associated" ;;
    ups-01) text="%UPS-6-STATUS: battery normal, input power present" ;;
    *) text="syslog heartbeat" ;;
  esac
  printf '<134>1 %s %s - - - - %s\n' "$ts" "$DEVICE" "$text"
}

wait_for_collector() {
  i=0
  while [ "$i" -lt 60 ]; do
    if nc -z -w 1 "$HOST" "$PORT" 2>/dev/null; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  echo "$DEVICE: collector $HOST:$PORT did not accept TCP" >&2
  return 1
}

send_udp() {
  printf '%s' "$1" | nc -u -w 1 "$HOST" "$PORT"
}

send_tcp_once() {
  printf '%s' "$1" | nc -w 2 "$HOST" "$PORT"
}

wait_for_collector || exit 1

case "$MODE" in
  udp-once)
    n=0
    while [ "$n" -lt 5 ]; do
      send_udp "$(message)" || true
      n=$((n + 1))
      sleep 1
    done
    ;;
  udp-heartbeat|udp-slow)
    while true; do
      send_udp "$(message)" || true
      sleep "$INTERVAL"
    done
    ;;
  tcp-burst)
    while true; do
      send_tcp_once "$(message)" || true
      sleep "$INTERVAL"
    done
    ;;
  tcp-hold)
    {
      message
      # Hold the session open without another write, so the log can be aged.
      sleep 86400
    } | nc "$HOST" "$PORT"
    ;;
  tcp-heartbeat)
    while true; do
      {
        while true; do
          message
          sleep "$INTERVAL"
        done
      } | nc "$HOST" "$PORT" || true
      sleep 1
    done
    ;;
  *)
    echo "unknown MODE=$MODE" >&2
    exit 1
    ;;
esac
