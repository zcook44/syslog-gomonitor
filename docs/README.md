# syslog-gomonitor

Version 1.1.0.

`syslog-gomonitor` reports whether the devices that should be sending syslog
to **this server** are still doing so. It reads a JSON inventory, stats each
device's own log file, and serves a status page on loopback. A separate hourly
pass totals the bytes stored for each device, including rotated logs.

The program does not receive syslog, does not change rsyslog, and does not
read log contents. rsyslog (or another collector) must already write one
regular file per source. A file that appears under a watched directory can be
adopted into the inventory from the status page, or ignored so it is not
offered again.

## Documentation

| Document | What it covers |
| --- | --- |
| [Architecture and data flow](architecture.md) | How a message, a status check, discovery of untracked files, and a size measurement move through the system |
| [Inventory and log files](inventory-and-logs.md) | The JSON schema, including ignored sources, path rules, and which files count toward stored size |
| [Install](install.md) | Building the offline package and installing it on a production syslog server |
| [Operations](operations.md) | The dashboard, commands, status meanings, and troubleshooting |

The development tree also has a short [project README](../README.md). The
[lab](../lab/README.md) is the container test bed for later releases: rsyslog,
example senders, and the dashboard on `http://127.0.0.1:18080/`.
`go run ./cmd/syslog-gomonitor demo` is a smaller preview without rsyslog.
Production uses the package described in the install document.
