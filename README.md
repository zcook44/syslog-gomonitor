# Syslog source monitor (Go)

Version 1.0.0.

`syslog-gomonitor` watches the devices that are supposed to be reporting to
**this** syslog server. It reads a JSON inventory, checks each device's own log
file, and serves a live status page. It does not receive syslog and it does
not read log contents.

Activity (freshness, and TCP session state when a device uses TCP) is checked
every few seconds. The total size of each device's stored logs, including
rotated files, is measured on a separate hourly pass so that walk does not run
on the fast loop.

The page listens on `127.0.0.1:8080` unless you choose another address. For a
disposable fleet that shows every status, use the demo below.

Operator documentation, including data flow and dark-site install, is in
[docs/](docs/README.md). Build the offline package with `./release/build.sh`.

## What the page means

| Column | Meaning |
| --- | --- |
| Reporting | The device's current log is a regular file and was written within `stale_after_seconds` (default 900). Missing, empty, and older files need attention. |
| Connection | TCP devices need an established session from the configured source IP to the local `tcp_port` (default 514), as reported by `ss`. UDP has no connection. If `ss` fails, the connection is **Unknown**, not disconnected. |
| Active file | Size of the current log, taken from the same stat as the freshness check. |
| Stored logs | Sum of that log plus rotated siblings in the same directory (`syslog.log.1`, `syslog.log-20261022.gz`, and similar). Measured about once an hour. Symlinks are not followed, and `*.offset` / `*.state` files are not counted. |
| Overall | **Healthy** only when the log is recent. TCP also requires an established session. A definite problem outranks an unknown signal. |

The page has a search box and the summary cards filter the table. Clicking the
card that is already selected clears that filter. Search and cards apply
together and stay in place when the page updates. When `log_directories` is
set, **Not in inventory** and **Ignored** are extra cards. Either one replaces
the device table until you select it again or return to a device card.

A quiet device can be fine and still look stale. Give it a longer silence limit
than the gap between its messages, or send a periodic heartbeat. A new empty
file after rotation stays **Empty** until the next message. A modification time
more than a minute in the future is **Unknown**. The check is an activity
signal, not a packet capture: anything that updates the file's mtime, including
`touch`, looks like a write.

Stored size is the bytes currently on disk (`st_size`), not a lifetime counter
and not the uncompressed size of a `.gz` rotation. Deleting old rotations
shows up on the next hourly measurement. The active-file column keeps moving
between those measurements.

## Inventory

Copy [`devices.example.json`](devices.example.json). The `192.0.2.x` addresses
are examples. `source_ip` is the address this server sees, which is the
post-NAT address when a device is translated. Names, source IPs, and log paths
must be unique. Devices that share one relay address cannot be told apart, so
duplicate source IPs are rejected.

```json
{
  "stale_after_seconds": 900,
  "monitor_overdue_seconds": 180,
  "log_directories": ["/var/log/remote", "/var/log/firewall"],
  "log_path_template": "/var/log/remote/{source_ip}/syslog.log",
  "devices": [
    {"name": "edge-router-01", "source_ip": "192.0.2.10", "protocol": "udp"},
    {"name": "core-switch-01", "source_ip": "192.0.2.11", "protocol": "tcp",
     "tcp_port": 514, "stale_after_seconds": 600},
    {"name": "firewall-01", "source_ip": "192.0.2.12", "protocol": "udp",
     "log_path": "/var/log/firewall/firewall-01.log"}
  ]
}
```

- `log_directories` lists the syslog trees to watch. The install script asks for these paths and stores them in the inventory. Every device log must stay inside one of them. Edit the list in `devices.json` when a tree moves.
- `log_path_template` may contain `{source_ip}` once. Omit it and set `log_path` or `log_directory` on every device if the layout is not one directory per address.
- `protocol` defaults to `udp`. For syslog over TLS, use `"tcp"` and the TLS port (often 6514). The monitor sees the socket, not the handshake.
- `monitor_overdue_seconds` is how long the page waits before it calls itself stale. It does not change device health.
- An invalid inventory is refused. A running server keeps the previous device list until the file is valid again.

Logs have to already be split per source. A shared file's mtime cannot say
which device wrote it. [`examples/rsyslog-per-device.conf`](examples/rsyslog-per-device.conf)
is an optional extra copy for an existing imudp/imtcp ruleset. Do not enable it
until you have checked rule order, disk, SELinux, and rotation. Run
`rsyslogd -N1` before restarting rsyslog.

## Adopting a device

A file that shows up under a watched directory is not a dashboard row until it
is in the inventory. About every 30 seconds the service lists `log_directories`
and offers regular files that are not already devices. The usual new switch is
`{directory}/{source_ip}/syslog.log`.

**Not in inventory** and **Ignored** are summary cards. The device table stays
up until you select one. Selecting it again returns to the devices.

On an untracked file, **Adopt** writes a device into `devices.json`. The name is
required. The source address is taken from the path when the directory or file
name is an address; otherwise you enter it. Protocol defaults to UDP. Choose
TCP, and a port if it is not the inventory default, when the device keeps a
session. The page reloads the inventory and starts tracking that file.

**Ignore** records the path in the inventory's `ignored` list. The file stays
out of the adoption list and is not monitored. Ignoring a per-address log also
ignores that source address, so a recreated file for the same address stays
hidden. **Stop ignoring** removes the entry. Use ignore when a bad syslog rule
is dropping extra files into a watched directory.

Rotated files, rsyslog `*.offset` and `*.state` files, and symlinks are not
offered. Discovery does not read log contents and does not follow symlinks.
`--discover-interval 0s` checks once at startup. Discovery only runs when
`log_directories` is set.

## Lab

The container lab is the place to try monitor changes, starting from this
1.0.0 tree and continuing through later releases. It runs rsyslog plus this
source tree, and example containers send syslog at that collector. Open
<http://127.0.0.1:18080/>. Directions and the fourteen example rows are in
[lab/README.md](lab/README.md).

```bash
docker compose -f lab/compose.yaml up -d --build
```

## Demo

The demo writes example logs and opens two loopback TCP sessions. It does not
touch a real syslog daemon or the system web root. It is a single-process
preview when Docker is not what you want to run. From this directory:

```bash
go run ./cmd/syslog-gomonitor demo
```

Open <http://127.0.0.1:18082/>. Stop it with Ctrl+C. The example files stay in
the temp directory printed at startup.

| Device | What you should see |
| --- | --- |
| `edge-router-01` | UDP, receiving, healthy. Active file plus plain and gzip rotations. A note and a symlink in that directory are not counted. |
| `core-switch-01` | TCP session established and still receiving. Healthy. |
| `dist-switch-02` | Last write is an hour old, with an older rotation still on disk. Needs attention. |
| `access-switch-03` | No log file and nothing stored. Needs attention. |
| `firewall-01` | Current file is empty after rotation; the archive is still part of the stored size. Uses a `log_path` override. |
| `wan-router-02` | Recent log, no TCP session. Needs attention. |
| `core-router-02` | TCP session still open, log silent. Needs attention. |
| `lab-switch-01` | Log path is a symlink. Unknown, and the target's bytes are not added again. |
| `dmz-firewall-01` | Modification time is an hour ahead. Unknown. The file is still counted as stored. |

Write one page and exit, without serving:

```bash
go run ./cmd/syslog-gomonitor demo --once
```

Optional container, published only on host loopback:

```bash
docker compose -f demo/compose.yaml up -d --build
```

Open <http://127.0.0.1:18082/>. Stop it with
`docker compose -f demo/compose.yaml down`.

## Run

```bash
go run ./cmd/syslog-gomonitor validate --config ./devices.json
go run ./cmd/syslog-gomonitor serve --config ./devices.json --state ./sizes.json --listen 127.0.0.1:8080
```

Other commands:

```bash
go run ./cmd/syslog-gomonitor check --config ./devices.json --state ./sizes.json
go run ./cmd/syslog-gomonitor size --config ./devices.json --state ./sizes.json
go run ./cmd/syslog-gomonitor configure --config ./devices.json --log-dir /var/log/remote
```

`serve` checks activity on `--poll` (default `5s`) and measures stored size on
`--size-interval` (default `1h`). The first size pass runs at startup when the
saved snapshot is missing or older than that interval. `--size-interval 0s`
skips the in-process walk and only displays a snapshot written by `size`.

`check` prints the current activity and the last saved sizes. Add `--scan-size`
to measure immediately. `size` is the hourly job: it walks the directories,
writes the snapshot, and prints a short report.

Viewing from another machine, without opening the port:

```bash
ssh -L 8080:127.0.0.1:8080 your-user@your-syslog-server
# Browse http://127.0.0.1:8080/
```

The page shows device names, addresses, and log paths. Keep the listener on
loopback unless the host has its own access control.

## Requirements

The 1.0.0 production package is a static `linux/amd64` binary. The syslog
server does not need Go, a C library, Python, or a web server. The install
script does not download packages.

### Build machine

Needed only to create the tarball. The syslog server does not need any of these.

| Requirement | Package on RHEL | Why |
| --- | --- | --- |
| Go 1.22 or newer | `golang` | `release/build.sh` compiles the binary. CGO is disabled, so a C compiler is not required. |
| `file` | `file` | The build refuses a binary that is not statically linked. |
| `sha256sum` | `coreutils` | Checksum of the tarball and of the binary inside it. |
| `tar` and `gzip` | `tar`, `gzip` | Writes `release/syslog-gomonitor-1.0.0-linux-amd64.tar.gz`. |
| Bash | `bash` | `release/build.sh`. |

### Syslog server packages

Install these from local media before running `install.sh`.

| Requirement | Package on RHEL | Required for |
| --- | --- | --- |
| systemd | `systemd` | Installing and running `syslog-gomonitor.service`. `systemctl` and `journalctl` come from this package. |
| Bash 4.4 or newer | `bash` | `install.sh`, including the directory prompt and the `/dev/tcp` health check. |
| GNU coreutils | `coreutils` | `install`, `sha256sum`, `timeout`, and `id` during install. |
| `grep` | `grep` | The install health check looks for `ok` in the HTTP response. |
| `tar` and `gzip` | `tar`, `gzip` | Unpacking the release tarball. |
| `ss` | `iproute` | Classifying TCP devices. The process runs `ss -H -t -n state established`. Without `ss`, UDP rows still update and every TCP connection is **Unknown**, not disconnected. |

`curl` is not required. It is a convenient way to read `/healthz` after install.

### Runtime requirements

These are not extra packages. The service will not report devices correctly without them.

- An existing syslog collector on this host, usually `rsyslog`, already receiving the devices. This package does not install a listener, open UDP or TCP 514, or change collector rules.
- One regular log file per source, inside a directory listed in `log_directories`. A shared log file cannot show which device wrote last. The optional example is [`examples/rsyslog-per-device.conf`](examples/rsyslog-per-device.conf).
- Read permission on those files and on their parent directories. The packaged unit runs as root and keeps only `CAP_DAC_READ_SEARCH`, which is what lets it stat root-owned logs.
- Write permission on `/var/lib/syslog-gomonitor` and on `/etc/syslog-gomonitor`. The size snapshot lives in the first. Adopt and ignore replace `devices.json` in the second. The rest of the filesystem is read-only for the service.
- `127.0.0.1:8080` free. The unit binds only that address. View it from another machine with an SSH tunnel rather than by publishing the port.
- A JSON inventory at `/etc/syslog-gomonitor/devices.json`. `install.sh` creates one from the directories you enter. Device names, source IPs, and log paths must be unique.
- Disk for the per-source files and their rotations. Stored size is the current on-disk total, measured about once an hour.
- A clock within about a minute of true time. A modification time more than 60 seconds in the future is reported as **Unknown**.
- On an SELinux-enforcing host, normal labels on the installed paths. `restorecon` from `policycoreutils` can apply them. The install script does not run it.

The dashboard's first view is plain HTML. Live refresh, search, the summary-card filters, and adopt, ignore, and stop ignoring use JavaScript in the browser. Reloading the page shows the latest device table when JavaScript is disabled. Adopt, ignore, and stop ignoring run from that page script.

The container lab and the single-process demo are not part of the production install. The lab needs Docker Engine with Compose v2. The demo needs Go 1.22 or newer on a workstation.

## Install on the syslog server

Version 1.0.0 is the offline tarball built by `./release/build.sh`. Full steps,
including the directory prompt and upgrades, are in
[docs/install.md](docs/install.md).

```bash
./release/build.sh
# copy release/syslog-gomonitor-1.0.0-linux-amd64.tar.gz and the .sha256 file
sha256sum -c syslog-gomonitor-1.0.0-linux-amd64.tar.gz.sha256
tar -xzf syslog-gomonitor-1.0.0-linux-amd64.tar.gz
cd syslog-gomonitor-1.0.0-linux-amd64
sudo ./install.sh --log-dir /var/log/remote
```

On a terminal, `sudo ./install.sh` asks for each directory instead of
requiring `--log-dir`. The answers are stored in
`/etc/syslog-gomonitor/devices.json` as `log_directories`.

The service runs as root so it can stat root-owned logs, binds only to
loopback, and can write its size snapshot under `/var/lib/syslog-gomonitor`.
`journalctl -u syslog-gomonitor.service` shows startup and each hourly
measurement. Missing logs stay on the page; they do not stop the service.

Leave `syslog-gomonitor-size.timer` disabled while `serve` uses the default
one-hour interval. Enable that timer only after changing the service to
`--size-interval 0s`. Running both measures twice.

## Tests

```bash
go test ./...
```

The tests use temporary files and loopback sockets. They do not contact
devices or change a syslog service.

## Layout

| Package | Role |
| --- | --- |
| `internal/inventory` | Parse and validate the JSON device list, and append adopted or ignored sources |
| `internal/discover` | List untracked files in the watched directories |
| `internal/activity` | Stat the current log, without reading it or following symlinks |
| `internal/session` | Established TCP sessions from `ss` |
| `internal/status` | Combine freshness and connection into one outcome |
| `internal/storage` | Hourly total of the active log and its rotations |
| `internal/engine` | Fast poll, hourly size pass, inventory reload |
| `internal/dashboard` | HTML status page |
| `internal/server` | Loopback HTTP server and live updates |
| `internal/demo` | Example fleet used by `syslog-gomonitor demo` |
