# Lab

This is the integration lab for syslog-gomonitor. It stands up a Rocky Linux
collector running rsyslog and the monitor from the current source tree, plus
example containers that send syslog at that collector. Use it to check the
dashboard while changing the monitor.

The page is published only on the host loopback:

<http://127.0.0.1:18080/>

## Start and stop

From the repository root, with permission to use Docker:

```bash
docker compose -f lab/compose.yaml up -d --build
```

If Docker says the socket at `/var/run/docker.sock` is not accessible, run that
command with sudo, or use an account that is in the `docker` group. Do not
change the socket permissions. Membership in that group is root-level access.

The collector waits for the one-shot senders before it pins stale, empty, and
future files, then starts the monitor. The page can take about a minute after
`up` returns.

```bash
docker compose -f lab/compose.yaml logs -f collector
docker compose -f lab/compose.yaml down
```

After a monitor code change, rebuild the collector only:

```bash
docker compose -f lab/compose.yaml up -d --build collector
```

Sender images do not contain the monitor. Rebuild them only when `lab/sender`
changes.

## What each row is

Twelve containers send syslog. Two inventory rows have no sender; the collector
prepares those files itself so the dashboard can show states a live sender
cannot hold.

| Device | Address | How it reports | Expected row |
| --- | --- | --- | --- |
| `edge-router-01` | 10.88.0.10 | UDP every 5s | Healthy. Rotations and a note file sit beside the live log; the note is not counted. |
| `core-switch-01` | 10.88.0.11 | TCP session held open, a message every 5s | Healthy. |
| `vpn-gw-01` | 10.88.0.20 | TCP session held open, a message every 5s | Healthy. |
| `branch-router-01` | 10.88.0.18 | UDP every 5s | Healthy, with a large rotated file already on disk. |
| `wireless-01` | 10.88.0.21 | UDP every 5s | Healthy. |
| `ups-01` | 10.88.0.22 | UDP every 30s | Healthy. |
| `dist-switch-02` | 10.88.0.12 | One UDP burst, then the collector ages the file | Stale. Needs attention. |
| `idf-switch-04` | 10.88.0.19 | One UDP burst, then the file is aged further | Stale. Needs attention. |
| `firewall-01` | 10.88.0.30 | One UDP burst into `/var/log/firewall`, then the collector empties the active file and keeps an archive | Empty active file, stored size still includes the archive. |
| `wan-router-02` | 10.88.0.14 | TCP connect, send, close, every 20s | Recent log, no lasting session. Needs attention. It can show established for a moment while it sends. |
| `core-router-02` | 10.88.0.15 | TCP session held open after one message; the collector ages that file | Connected and stale. Needs attention. |
| `dmz-firewall-01` | 10.88.0.17 | One UDP burst, then the collector sets a future modification time | Unknown. |
| `access-switch-03` | 10.88.0.13 | No sender and no file | Missing. Needs attention. |
| `lab-switch-01` | 10.88.0.16 | No sender. The collector symlinks this path at the edge router's log | Unknown. The target's bytes are not counted again. |

Search and the summary cards work on this page the same way they do in
production. The collector listens on the lab bridge only; host port 514 is not
published.

The lab inventory sets `log_directories`, so the summary includes **Not in
inventory** and **Ignored**. The example files are already devices, or they
are rotations and notes beside a device log, so both counts stay at zero
unless a sender writes a new address directory. Adopt and ignore on that page
rewrite the lab inventory inside the collector container.
