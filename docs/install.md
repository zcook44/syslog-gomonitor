# Install

Production install uses a tarball built ahead of time. The syslog server does
not need Go, and the install script does not contact the network.

The package is `linux/amd64`. The binary is statically linked. There are no
third-party Go modules to vendor. On the server, systemd is required. `ss`
from iproute is required only to classify TCP devices; without it those
devices show connection **Unknown** rather than disconnected. A normal RHEL
syslog host already has both.

## 1. Build the package

On a machine that has Go 1.22 or newer and can see this source tree:

```bash
cd syslog-gomonitor
./release/build.sh
```

That package serves HTTPS on port 8443. To pack the loopback HTTP dashboard
(`127.0.0.1:8080`) as the default unit instead:

```bash
./release/build.sh --http
```

`install.sh` can still choose the other mode. Both unit files are in the
tarball.

The script writes the version 1.1.0 package:

```text
release/syslog-gomonitor-1.1.0-linux-amd64.tar.gz
release/syslog-gomonitor-1.1.0-linux-amd64.tar.gz.sha256
```

The version string comes from [`release/VERSION`](../release/VERSION).

The build refuses to pack the binary unless `file` reports it as statically linked.

Confirm the checksum before the file leaves the build machine:

```bash
cd release
sha256sum -c syslog-gomonitor-1.1.0-linux-amd64.tar.gz.sha256
```

## 2. Move it to the syslog server

Copy the `.tar.gz` and the `.sha256` file by the site's approved offline
method. On the server:

```bash
sha256sum -c syslog-gomonitor-1.1.0-linux-amd64.tar.gz.sha256
tar -xzf syslog-gomonitor-1.1.0-linux-amd64.tar.gz
cd syslog-gomonitor-1.1.0-linux-amd64
sha256sum -c SHA256SUMS
```

`SHA256SUMS` inside the archive covers `bin/syslog-gomonitor`. `install.sh`
checks it again before copying.

## 3. Install

Edit the inventory first if you already know the sources. You can also let
the script install the example and edit it afterwards.

```bash
sudo ./install.sh
```

On a terminal the script asks whether to serve HTTPS. Answer `n` to install
HTTP on `127.0.0.1:8080` instead. The same choice is `--http`:

```bash
sudo ./install.sh --http --log-dir /var/log/remote
```

`--http` cannot be combined with `--tls-cert` or `--tls-key`. A package built
with `./release/build.sh --http` already defaults to that HTTP unit.

What an HTTPS install does:

1. Verifies `bin/syslog-gomonitor` against `SHA256SUMS`.
2. Copies the binary to `/usr/local/bin/syslog-gomonitor`.
3. Copies the three systemd units to `/etc/systemd/system/`.
4. Copies these documents to `/usr/local/share/doc/syslog-gomonitor/`.
5. Copies `devices.example.json` to `/etc/syslog-gomonitor/devices.example.json`.
6. Asks for the syslog directories to watch, or uses each `--log-dir`. A new inventory is created with those directories and example devices. An existing inventory keeps its devices and updates `log_directories`. Without a terminal or `--log-dir`, a new inventory uses `/var/log/remote`.
7. Runs `syslog-gomonitor validate`. A directory list that leaves a device file outside those directories is not written, and the service is left unstarted.
8. Links the host certificate chain and private key under `/etc/syslog-gomonitor/tls/` and checks that the server certificate is first in the chain.
9. Reloads systemd, enables `syslog-gomonitor.service`, and restarts it.
10. Checks that `https://127.0.0.1:8443/healthz` answers over TLS.

`--no-start` stops after the files and `daemon-reload`.

An HTTPS install listens on port 8443 on every interface. An HTTP install listens on `127.0.0.1:8080` only. Either way the service runs as root so it can stat
root-owned logs. It may write its size snapshot under
`/var/lib/syslog-gomonitor`, which systemd creates, and it may replace
`/etc/syslog-gomonitor/devices.json` when an operator adopts or ignores a
source. The unit keeps the rest of the filesystem read-only and grants
`CAP_DAC_READ_SEARCH` so those logs can be stated. It does not open a syslog
port.

If `restorecon` is available on the host, label the installed paths the way
the rest of the system is labeled. The script does not call it, because a
dark site may not want a surprise policy change. A typical follow-up is:

```bash
sudo restorecon -RF \
  /usr/local/bin/syslog-gomonitor \
  /etc/syslog-gomonitor \
  /etc/systemd/system/syslog-gomonitor.service \
  /etc/systemd/system/syslog-gomonitor-size.service \
  /etc/systemd/system/syslog-gomonitor-size.timer \
  /var/lib/syslog-gomonitor \
  /usr/local/share/doc/syslog-gomonitor
```

## 4. Point it at real devices

The watched directories are `log_directories` in the inventory. Change them
there, or run the installer again and answer the prompt. Repeat `--log-dir`
to set the list without a prompt:

```bash
sudo ./install.sh --log-dir /var/log/remote --log-dir /var/log/firewall
```

That keeps the current device entries. A device log that would fall outside
the new list is rejected and the file is left unchanged.

```bash
sudoedit /etc/syslog-gomonitor/devices.json
sudo /usr/local/bin/syslog-gomonitor validate --config /etc/syslog-gomonitor/devices.json
```

The service reloads a valid file on the next poll, within about 5 seconds.
Confirm the page:

```bash
curl --fail --cacert /path/to/root.pem https://127.0.0.1:8443/healthz
curl --fail --cacert /path/to/root.pem https://127.0.0.1:8443/api/status
```

From an admin workstation, open `https://<syslog-server>:8443/`. The name in
that address must be on the server certificate, and the workstation must trust
the CA that issued it. The page has no login, so allow TCP 8443 only from the
admin network. An install that passed `--http` is `http://127.0.0.1:8080/` on
the syslog server itself. The page shows device names, addresses,
and log paths.

A file that arrives under a watched directory can be adopted or ignored from
that page. A child directory is one card, and a date in the filename stays
with that device after the day changes. Selecting a device row lists the
files in its log directory. Both adopt and ignore replace
`/etc/syslog-gomonitor/devices.json`. The unit allows writes to that directory
and nowhere else except the size-snapshot directory. See
[Operations](operations.md) for the buttons and
[Inventory and log files](inventory-and-logs.md) for the `ignored` list and
dated names.

Logs from the service:

```bash
journalctl -u syslog-gomonitor.service
```

Missing device logs are rows on the page. They do not stop the service.

## Hourly size as its own process

By default the service measures stored size itself, once at startup when the
snapshot is stale and then about every hour. Leave
`syslog-gomonitor-size.timer` disabled in that configuration.

To move the walk into a separate oneshot, change the service `ExecStart` line
so it contains `--size-interval 0s`, then:

```bash
sudo systemctl daemon-reload
sudo systemctl restart syslog-gomonitor.service
sudo systemctl enable --now syslog-gomonitor-size.timer
```

The timer is installed but not enabled. It runs about one hour after each
run, with a short delay after boot, and catches up after the host was powered
off (`Persistent=true`). Enabling both the timer and the default in-process
interval measures twice. That is safe and wasteful.

Run one measurement by hand:

```bash
sudo /usr/local/bin/syslog-gomonitor size \
  --config /etc/syslog-gomonitor/devices.json \
  --state /var/lib/syslog-gomonitor/sizes.json
```

## Upgrade

Unpack the new tarball, check both checksums, and run `sudo ./install.sh`
again. Device entries in `devices.json` stay. On a terminal the script asks
before replacing `log_directories`; `--log-dir` replaces that list without a
prompt. The script restarts the service so the new binary replaces the
running process.

## Remove

```bash
sudo ./install.sh --uninstall
```

This stops and disables the service and the optional size timer, and deletes
the binary, the units, and the installed documents. The inventory and
`sizes.json` are kept.

```bash
sudo ./install.sh --uninstall --purge
```

`--purge` also deletes `/etc/syslog-gomonitor` and
`/var/lib/syslog-gomonitor`. It does not delete syslog logs.

## Layout check without root

`install.sh --destdir /tmp/syslog-gomonitor-layout` copies the same tree under
that directory and skips systemd. Use it on a build machine to inspect paths.
It is not a production install.
