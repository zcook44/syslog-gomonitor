# Inventory and log files

The inventory is the list of sources this server expects. It is not an
Ansible inventory, and the monitor does not add a device because a packet
arrived. When `log_directories` is set, the status page can append a device
the operator adopts from a file that is already in one of those directories,
or record a file under `ignored` so it is not offered again. `source_ip` is
the address the collector sees. When a device is translated by NAT, use the
address after translation.

Start from [`devices.example.json`](../devices.example.json). The `192.0.2.x`
addresses in that file are documentation addresses. Replace them.

```json
{
  "stale_after_seconds": 900,
  "monitor_overdue_seconds": 180,
  "log_directories": [
    "/var/log/remote",
    "/var/log/firewall"
  ],
  "log_path_template": "/var/log/remote/{source_ip}/syslog.log",
  "tcp_port": 514,
  "devices": [
    {"name": "edge-router-01", "source_ip": "192.0.2.10", "protocol": "udp"},
    {
      "name": "core-switch-01",
      "source_ip": "192.0.2.11",
      "protocol": "tcp",
      "tcp_port": 514,
      "stale_after_seconds": 600
    },
    {
      "name": "firewall-01",
      "source_ip": "192.0.2.12",
      "protocol": "udp",
      "log_path": "/var/log/firewall/firewall-01.log"
    }
  ]
}
```

## Settings

| Field | Where | Default | Rule |
| --- | --- | --- | --- |
| `stale_after_seconds` | File, or one device | 900 | Positive integer. Silence longer than this is stale. |
| `monitor_overdue_seconds` | File | 180 | Positive integer. Age at which the page calls itself out of date. |
| `tcp_port` | File, or one TCP device | 514 | 1 through 65535. The collector's listening port. |
| `log_directories` | File | none | Absolute directories this server watches. When set, every device log must be inside one of them. |
| `log_path_template` | File | `{first directory}/{source_ip}/syslog.log` when directories are set | Absolute path containing `{source_ip}` once and no other braces. |
| `log_directory` | Device | first `log_directories` entry | One of `log_directories`. The device file is `{log_directory}/{source_ip}/syslog.log`. |
| `devices` | File | required | Non-empty list. |
| `ignored` | File | none | Files in a watched directory that the dashboard must not offer for adoption and must not monitor. |
| `name` | Device | required | Unique, ignoring case. No surrounding space or control characters. |
| `source_ip` | Device | required | Literal IPv4 or IPv6. No zone index. IPv4-mapped IPv6 is stored as IPv4. |
| `protocol` | Device | `udp` | `udp` or `tcp`. |
| `log_path` | Device | from the template or `log_directory` | Absolute file path. Cannot be combined with `log_directory`. Must stay inside `log_directories` when that list is set. |

Names, source addresses, and resolved log paths must be unique. Two devices
behind one relay address cannot be told apart, so a duplicate source IP is
rejected rather than treating one session as proof for both.

`tcp_port` on a UDP device is rejected. For syslog over TLS, set `protocol` to
`tcp` and use the TLS port, often 6514. The monitor sees the TCP session, not
the handshake.

## Ignored sources

`ignored` is optional. Each entry is a file that appeared under
`log_directories` and should stay out of the inventory.

```json
"ignored": [
  {"log_path": "/var/log/remote/noise.log"},
  {
    "log_path": "/var/log/remote/192.0.2.99/syslog.log",
    "source_ip": "192.0.2.99"
  }
]
```

| Field | Rule |
| --- | --- |
| `log_path` | Required. Absolute file path inside `log_directories` when that list is set. Unique. Not a device path. |
| `source_ip` | Optional. Literal address. Unique in this list, and not a device address. |

The dashboard writes these entries from **Ignore**. A per-address file stores
`source_ip`, and discovery then hides every untracked file for that address.
Ignoring one day of a dated name also hides later days of that same name.
**Stop ignoring** removes the entry. An entry that names a device path or a
device address is rejected, so a source is not both tracked and ignored.
`validate` prints the list. Removing `log_directories` does not keep an
ignored path that would fall outside the new list.

`log_directories` is the list of syslog trees to watch. The install script
asks for these paths and writes them here. Change the list in this file when
a tree moves; do not edit the systemd unit. A device with no `log_path` and
no `log_directory` uses `log_path_template`, or `{first directory}/{source_ip}/syslog.log`
when only the directory list is set. A device that keeps logs in another
watched tree sets `log_directory` to that path. A resolved file outside every
configured directory is rejected, so a directory removed from the list cannot
silently keep watching a tree that is no longer listed.

Paths must be absolute and must not contain `..` or control characters. File
paths must name a file. The template is checked before `{source_ip}` is
substituted, and the resolved path is checked again. Duplicate JSON keys are
rejected. Unknown fields are rejected. A file that fails these checks does
not replace a running inventory or a page that was already being served.

Check a file without publishing:

```bash
syslog-gomonitor validate --config /etc/syslog-gomonitor/devices.json
```

After a successful edit, the running service notices the new modification time
on its next poll and reloads. A bad edit is logged and ignored until the file
is valid again.

## Why the logs have to be split

The activity check uses the modification time of one file. A shared log cannot
say which device wrote last, so every device needs its own path. The usual
layout is a directory per source and a stable active name:

```text
/var/log/remote/192.0.2.10/syslog.log
/var/log/remote/192.0.2.10/syslog.log.1
/var/log/remote/192.0.2.10/syslog.log-20261022.gz
```

A collector that puts the date in the active name is tracked as a series.
`edge-router-01-20261028-syslog.log` and `edge-router-01-2026-10-28-syslog.log`
are the two recognized shapes. Adoption stores the newest file of that
convention. Each poll lists the same directory and stats the file with the
latest date, so the next day stays on the same device. The hourly total
includes every day of that series plus rotations of those files. A stable
name such as `syslog.log` is unchanged: the monitor keeps stating that path.

The optional rsyslog snippet
[`examples/rsyslog-per-device.conf`](../examples/rsyslog-per-device.conf) adds
an extra copy for imudp and imtcp messages in the default ruleset. It does not
open a listener. Do not install it until rule order, disk, SELinux labels, and
rotation have been reviewed. An earlier `stop` keeps the action from running.
Validate with `rsyslogd -N1` before restarting rsyslog. The install script
copies that example into the documentation directory and does not enable it.

## What the hourly pass counts

For each device the scan lists the directory containing `log_path` once.
Devices that share a directory share that listing. A name counts when it is
the active file or a rotation of it:

| Name | Counted |
| --- | --- |
| `syslog.log` | Yes, the active file |
| `syslog.log.1`, `syslog.log.2.gz` | Yes |
| `syslog.log-20261022`, `syslog.log-20261022.gz` | Yes |
| `firewall-01.log.1` when the active file is `firewall-01.log` | Yes |
| `edge-router-01-20261027-syslog.log` and `edge-router-01-20261028-syslog.log` when the device was adopted from either day | Yes, both days |
| `edge-router-01-20261028-syslog.log.1` and `edge-router-01-20261028-syslog.log.gz` | Yes |
| `core-switch-01-20261028-syslog.log` beside an `edge-router-01-…` device | No |
| `notes.txt`, `other.log` | No |
| `syslog.log.offset`, `syslog.log.state` | No |
| A symlink with a matching name | No. The link is not followed. |
| Files in a subdirectory, including an `olddir` | No |

The byte count is `st_size` of those regular files. A `.gz` rotation
contributes its compressed size on disk, not the size it would expand to.
An empty active file contributes a file to the count and zero bytes, so an
archive beside it still shows up after rotation.

The scan refuses a log directory that is itself a symlink. A missing directory
is a measured zero with the detail "Log directory is missing", not a failed
measurement. A directory that cannot be read is an error for every device in
that directory; those devices show stored size as unavailable and are left out
of the stored-byte total.

Stored size is the amount currently on disk. Deleting a rotation lowers the
next hourly total. It is not a counter of bytes ever received.
