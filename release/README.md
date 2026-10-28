# Offline release

Version 1.1.0 of syslog-gomonitor. This directory builds a package for a
syslog server that cannot download software. The tarball contains a statically
linked `linux/amd64` binary, the systemd units, the example inventory, and the
install script. It does not contain a Go toolchain or third-party modules.
The program's Go code uses only the standard library.

`install.sh` asks for the syslog directories to watch and stores them in
`devices.json` as `log_directories`. Pass `--log-dir` once per directory when
there is no terminal. The packaged service unit allows the process to replace
that inventory file when an operator adopts or ignores a source from the
dashboard. `./release/build.sh` packs HTTPS on port 8443. `./release/build.sh --http`
packs the loopback HTTP unit (`127.0.0.1:8080`) as the default, and
`install.sh --http` selects that unit from either package. A dated log name
keeps following the newest day, and selecting a device lists the files in its
directory. The rest of the filesystem stays read-only, apart from the size
snapshot under `/var/lib/syslog-gomonitor`.

| File | Role |
| --- | --- |
| `build.sh` | Run on a machine with Go 1.22 or newer. Writes the tarball here. `--http` packs the loopback HTTP unit as the default dashboard. |
| `install.sh` | Copied into the tarball. Run as root on the syslog server. `--http` installs HTTP on `127.0.0.1:8080` instead of HTTPS. |
| `VERSION` | Version embedded in the package name. |

```bash
./release/build.sh
```

Carry `syslog-gomonitor-<version>-linux-amd64.tar.gz` and its `.sha256` file
to the server. Install directions are in [../docs/install.md](../docs/install.md).
