package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIgnoredSources(t *testing.T) {
	inv, err := Parse([]byte(`{
		"log_directories": ["/var/log/remote"],
		"log_path_template": "/var/log/remote/{source_ip}/syslog.log",
		"devices": [{"name": "edge", "source_ip": "192.0.2.10"}],
		"ignored": [
			{"log_path": "/var/log/remote/noise.log"},
			{"log_path": "/var/log/remote/192.0.2.99/syslog.log", "source_ip": "192.0.2.99"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Ignored) != 2 || inv.Ignored[1].SourceIP != "192.0.2.99" {
		t.Fatalf("ignored %+v", inv.Ignored)
	}
	if inv.TCPPort != DefaultTCPPort || inv.StaleAfterSeconds != DefaultStaleAfterSeconds {
		t.Fatalf("defaults port %d stale %d", inv.TCPPort, inv.StaleAfterSeconds)
	}
	if inv.LogPathTemplate != "/var/log/remote/{source_ip}/syslog.log" {
		t.Fatalf("template %s", inv.LogPathTemplate)
	}

	cases := []string{
		`{"log_directories":["/var/log/remote"],"devices":[{"name":"edge","source_ip":"192.0.2.10"}],"ignored":[{"log_path":"/var/log/remote/192.0.2.10/syslog.log"}]}`,
		`{"log_directories":["/var/log/remote"],"devices":[{"name":"edge","source_ip":"192.0.2.10"}],"ignored":[{"log_path":"/var/log/remote/other.log","source_ip":"192.0.2.10"}]}`,
		`{"log_directories":["/var/log/remote"],"devices":[{"name":"edge","source_ip":"192.0.2.10"}],"ignored":[{"log_path":"/etc/passwd"}]}`,
		`{"log_directories":["/var/log/remote"],"devices":[{"name":"edge","source_ip":"192.0.2.10"}],"ignored":[{"log_path":"/var/log/remote/a.log"},{"log_path":"/var/log/remote/a.log"}]}`,
	}
	for _, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestAdoptIgnoreAndUnignore(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	firewall := filepath.Join(root, "firewall")
	if err := os.MkdirAll(filepath.Join(remote, "192.0.2.10"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(remote, "192.0.2.50"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(firewall, 0o755); err != nil {
		t.Fatal(err)
	}
	switchLog := filepath.Join(remote, "192.0.2.50", "syslog.log")
	noise := filepath.Join(remote, "noise.log")
	custom := filepath.Join(firewall, "new-fw.log")
	for _, path := range []string{
		filepath.Join(remote, "192.0.2.10", "syslog.log"),
		switchLog,
		noise,
		custom,
	} {
		if err := os.WriteFile(path, []byte("syslog line\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(switchLog, filepath.Join(remote, "linked.log")); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "devices.json")
	raw := `{
	  "stale_after_seconds": 900,
	  "log_directories": ["` + remote + `", "` + firewall + `"],
	  "log_path_template": "` + remote + `/{source_ip}/syslog.log",
	  "devices": [
	    {"name": "edge-router-01", "source_ip": "192.0.2.10", "protocol": "udp", "stale_after_seconds": 600}
	  ]
	}`
	if err := os.WriteFile(config, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	err = Adopt(config, Adoption{Name: "nope", SourceIP: "192.0.2.8", Protocol: "udp", LogPath: "/etc/passwd"})
	if err == nil {
		t.Fatal("adopted a path outside the watched directories")
	}
	err = Adopt(config, Adoption{Name: "linked", SourceIP: "192.0.2.8", Protocol: "udp", LogPath: filepath.Join(remote, "linked.log")})
	if err == nil || !strings.Contains(err.Error(), "Symlink") {
		t.Fatalf("symlink adopt: %v", err)
	}
	afterReject, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterReject) {
		t.Fatal("rejected adopt changed the inventory")
	}

	if err := Adopt(config, Adoption{
		Name: "access-switch-04", SourceIP: "10.0.0.1", Protocol: "udp", LogPath: switchLog,
	}); err != nil {
		t.Fatal(err)
	}
	inv, err := Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Devices) != 2 || inv.Devices[1].Name != "access-switch-04" || inv.Devices[1].SourceIP != "192.0.2.50" {
		t.Fatalf("adopted %+v", inv.Devices)
	}
	if inv.Devices[1].LogPath != switchLog || inv.Devices[0].StaleAfterSeconds != 600 {
		t.Fatalf("paths %+v", inv.Devices)
	}
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"stale_after_seconds": 600`) {
		t.Fatalf("rewritten inventory dropped the existing device override:\n%s", body)
	}
	if strings.Contains(string(body), "10.0.0.1") {
		t.Fatal("client address replaced the address in the path")
	}
	info, err := os.Lstat(config)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}

	if err := Adopt(config, Adoption{
		Name: "firewall-02", SourceIP: "192.0.2.70", Protocol: "tcp", TCPPort: 6514, LogPath: custom,
	}); err != nil {
		t.Fatal(err)
	}
	inv, err = Load(config)
	if err != nil {
		t.Fatal(err)
	}
	fw := inv.Devices[2]
	if fw.SourceIP != "192.0.2.70" || fw.LogPath != custom || fw.Protocol != "tcp" || fw.TCPPort != 6514 {
		t.Fatalf("custom %+v", fw)
	}

	if err := Ignore(config, noise); err != nil {
		t.Fatal(err)
	}
	if err := Ignore(config, noise); err != nil {
		t.Fatal(err)
	}
	inv, err = Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Ignored) != 1 || inv.Ignored[0].LogPath != noise || inv.Ignored[0].SourceIP != "" {
		t.Fatalf("ignored %+v", inv.Ignored)
	}
	if _, err := Recognize(inv, noise); err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Fatalf("recognize ignored: %v", err)
	}
	err = Adopt(config, Adoption{Name: "noise", SourceIP: "192.0.2.71", Protocol: "udp", LogPath: noise})
	if err == nil {
		t.Fatal("adopted an ignored file")
	}
	if err := Unignore(config, noise); err != nil {
		t.Fatal(err)
	}
	inv, err = Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Ignored) != 0 {
		t.Fatalf("still ignored %+v", inv.Ignored)
	}
	if _, err := Recognize(inv, noise); err != nil {
		t.Fatal(err)
	}

	sender := filepath.Join(remote, "192.0.2.90", "syslog.log")
	if err := os.MkdirAll(filepath.Dir(sender), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sender, []byte("unwanted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ignore(config, sender); err != nil {
		t.Fatal(err)
	}
	inv, err = Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Ignored) != 1 || inv.Ignored[0].SourceIP != "192.0.2.90" || inv.Ignored[0].LogPath != sender {
		t.Fatalf("address ignore %+v", inv.Ignored)
	}
}

func TestApplyLogDirectoriesKeepsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	if err := os.WriteFile(path, []byte(`{
		"log_directories": ["/var/log/remote"],
		"log_path_template": "/var/log/remote/{source_ip}/syslog.log",
		"devices": [{"name": "edge", "source_ip": "192.0.2.10", "protocol": "udp"}],
		"ignored": [{"log_path": "/var/log/remote/noise.log"}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLogDirectories(path, []string{"/var/log/remote", "/var/log/firewall"}); err != nil {
		t.Fatal(err)
	}
	inv, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Ignored) != 1 || inv.Ignored[0].LogPath != "/var/log/remote/noise.log" {
		t.Fatalf("ignored %+v", inv.Ignored)
	}
	if len(inv.LogDirectories) != 2 {
		t.Fatalf("directories %#v", inv.LogDirectories)
	}
}
