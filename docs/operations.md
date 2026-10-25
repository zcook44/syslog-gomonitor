# Operations

## Commands

The installed binary is `/usr/local/bin/syslog-gomonitor`.

| Command | What it does |
| --- | --- |
| `serve` | Run the dashboard, the activity poll, discovery of untracked files, and by default the hourly size pass. |
| `size` | Measure stored size once, write the snapshot, and print a text report. |
| `check` | Print one JSON snapshot. Uses the saved sizes unless `--scan-size` is set. |
| `validate` | Check the inventory and print the watched directories, ignored paths, and resolved device paths. Does not stat logs. |
| `configure` | Save `log_directories` into the inventory. Creates a starter inventory when the file is missing. Keeps existing devices. |
| `demo` | Workstation-only synthetic fleet. Not used on the syslog server. |

`serve` flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--config` | `/etc/syslog-gomonitor/devices.json` | Inventory |
| `--state` | `/var/lib/syslog-gomonitor/sizes.json` | Hourly size snapshot |
| `--listen` | `127.0.0.1:8080` | `host:port` |
| `--poll` | `5s` | Activity check interval. Minimum `200ms`. |
| `--size-interval` | `1h` | Stored-size interval. `0s` reads the snapshot and does not scan. |
| `--discover-interval` | `30s` | How often to look for files in `log_directories` that are not in the inventory. `0s` does that once at startup. |

## Dashboard

The page title is "Syslog source monitor". Counts at the top are the whole
inventory, not the rows left after a filter.

| Card | Click |
| --- | --- |
| Devices | Clear a status filter and show the device table, including when **Not in inventory** or **Ignored** is open. |
| Healthy | Show only healthy devices. |
| Needs attention | Show only devices that need attention. |
| Unknown | Show only unknown devices. |
| Stored logs | Not a filter. Total bytes from successful size measurements. |
| Not in inventory | Shown when `log_directories` is set. Hides the device table and lists files that are not devices. The count is not part of the device total. |
| Ignored | Shown when `log_directories` is set. Hides the device table and lists paths recorded in `ignored`. The count is not part of the device total. |

Clicking a status card that is already selected clears that filter and leaves
no card highlighted. **Devices** also clears a status filter. Selecting **Not
in inventory** or **Ignored** again returns to the device table, as does
selecting any device card. The search box matches the device name, source
address, log path, protocol, reporting state, and overall status. Search and a
status card apply together, and only to the device table. "Showing 2 of 9"
counts rows that pass both. A live update keeps the current search and the
open card.

Columns:

| Column | Source |
| --- | --- |
| Reporting | Current log only. Receiving, Stale, Missing, Empty, or Unknown. |
| Connection | `Connectionless` for UDP. Established, no session, or the `ss` error for TCP. |
| Last write | Modification time of the current file, in UTC. |
| Active file | Size of that file from the same stat. Updates on every poll. |
| Stored logs | Active file plus rotations, from the last hourly measurement. |
| Overall | Healthy, Needs attention, or Unknown. |

Rows are ordered with problems first, then unknown, then healthy, and by name
inside each group. The active-file number can move past the stored total
between hourly passes. That is expected: stored size is not recomputed when
the current file grows.

From those two lists:

| Action | Result |
| --- | --- |
| Adopt | Appends the device to `devices.json`. A per-address file keeps the address in its path. A loose file needs a source IP. The new row appears on the next update. |
| Ignore | Adds the path to `ignored`. The file is not monitored. A per-address ignore also hides that source address if the file is created again. |
| Stop ignoring | Removes the `ignored` entry. The file is offered again when it is still there. |

The name field is editable until you adopt. Rotations, `.offset` and `.state`
files, and symlinks are not listed. The list says when a directory could not
be read, or when more than 100 untracked files were found. `validate` prints
ignored paths. `check` includes `candidates` and `ignored` in the JSON snapshot.

## How to read a row

| What you see | Usual cause |
| --- | --- |
| Receiving, connectionless, healthy | UDP device wrote its file inside the silence limit. |
| Receiving, established, healthy | TCP device wrote its file and still has a session to the configured port. |
| Stale | The file is non-empty and older than `stale_after_seconds`. A quiet device needs a longer limit or a heartbeat. |
| Missing | The path does not exist yet. |
| Empty | The file exists and has size 0. Rotation often leaves this until the next message. |
| No session | The log may be fresh, but `ss` shows no established connection from that source to `tcp_port`. |
| Established and stale | The TCP session is up and the log has stopped. |
| Unknown log, "not a regular file" | The path is a symlink, directory, or other non-file. Symlinks are not followed. |
| Unknown log, future mtime | The modification time is more than 60 seconds ahead of the server clock. |
| Unknown connection | `ss` is missing, failed, or returned output that could not be parsed. |
| Stored pending | No `sizes.json` entry yet. |
| Stored unavailable | The log directory could not be listed. |

A TCP session to the wrong port does not count. The match is the device's
source IP and the collector's local port.

## Worked checks

Inventory accepted, paths resolved:

```bash
sudo syslog-gomonitor validate --config /etc/syslog-gomonitor/devices.json
```

One JSON snapshot, including the last stored-size time:

```bash
sudo syslog-gomonitor check \
  --config /etc/syslog-gomonitor/devices.json \
  --state /var/lib/syslog-gomonitor/sizes.json
```

Force a size measurement and keep the service's hourly schedule:

```bash
sudo syslog-gomonitor size \
  --config /etc/syslog-gomonitor/devices.json \
  --state /var/lib/syslog-gomonitor/sizes.json
```

Service health:

```bash
systemctl status syslog-gomonitor.service
journalctl -u syslog-gomonitor.service -n 50 --no-pager
curl --fail http://127.0.0.1:8080/healthz
```

The journal records startup, each hourly measurement, an inventory reload, an
adopt or ignore, and a measurement that could not be saved. It does not
record every 5-second poll or every discovery pass.

## Failure behavior

| Event | Result |
| --- | --- |
| Device log missing | Row needs attention. The service stays up. |
| Inventory edit does not validate | Previous device list keeps running. The error is logged. |
| Adopt or ignore is rejected | The page shows the reason. `devices.json` is unchanged when the new file would not validate. |
| Adopt or ignore cannot replace `devices.json` | The page says the inventory could not be updated. The previous file remains when the replace itself fails. |
| `ss` fails | TCP connections become Unknown. UDP rows are unchanged. |
| Size snapshot cannot be written | The in-memory totals from that pass are still shown. The old file remains. The next pass tries again. |
| Process crash | systemd restarts it after 2 seconds. Activity history is not recovered. `sizes.json` is recovered when it is newer than the interval. |
| Output file `sizes.json` is a symlink | The write is refused. |

## Resources

The poll stats one file per device and, when needed, runs `ss` once. It does
not walk rotation directories. The hourly pass lists each distinct log
directory once. That is the expensive part, which is why it is not on the
5-second loop.

The optional timer unit runs the size command at a lower CPU and I/O priority
than the dashboard process. Use that split when the log directories are large
enough that you do not want the walk inside the process serving the page.

## Lab

The container lab in [../lab/README.md](../lab/README.md) is the test bed for
changes after 1.0.0. A collector container runs rsyslog and a monitor built
from the working tree. More than ten example containers send syslog, and the
collector pins a few files so the page also shows missing, stale, empty,
symlink, and future-mtime rows. The dashboard is at `http://127.0.0.1:18080/`.

```bash
docker compose -f lab/compose.yaml up -d --build
```

Rebuild `collector` after a monitor change. That image is the only one that
contains the program.

## Demo, not production

The single-process demo does not run rsyslog. On a workstation with Go:

```bash
go run ./cmd/syslog-gomonitor demo
```

Open `http://127.0.0.1:18082/`. The process creates example logs and two
loopback TCP sessions under a temp directory it prints at startup. It does
not use `/var/log` or the production unit. `--once` writes a single page and
exits. The nine example devices cover receiving, stale, missing, empty,
TCP up, TCP down, TCP silent, a symlink, and a future modification time.
