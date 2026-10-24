package inventory

import (
	"os"
	"strings"
	"testing"
)

func TestParseResolvesTemplateAndOverrides(t *testing.T) {
	inv, err := Parse([]byte(`{
		"stale_after_seconds": 60,
		"tcp_port": 601,
		"log_path_template": "/var/log/remote/{source_ip}/syslog.log",
		"devices": [
			{"name": "router-01", "source_ip": "192.0.2.10"},
			{
				"name": "switch-02",
				"source_ip": "2001:0db8::2",
				"log_path": "/var/log/custom/switch.log",
				"protocol": "tcp",
				"stale_after_seconds": 300
			}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Devices[0].LogPath != "/var/log/remote/192.0.2.10/syslog.log" {
		t.Fatalf("template path %s", inv.Devices[0].LogPath)
	}
	if inv.Devices[0].Protocol != "udp" || inv.Devices[0].TCPPort != 0 {
		t.Fatalf("udp device %+v", inv.Devices[0])
	}
	sw := inv.Devices[1]
	if sw.SourceIP != "2001:db8::2" || sw.LogPath != "/var/log/custom/switch.log" {
		t.Fatalf("switch identity %+v", sw)
	}
	if sw.TCPPort != 601 || sw.StaleAfterSeconds != 300 || sw.Protocol != "tcp" {
		t.Fatalf("switch overrides %+v", sw)
	}
}

func TestParseCanonicalizesMappedIPv6(t *testing.T) {
	inv, err := Parse([]byte(`{
		"log_path_template": "/var/log/remote/{source_ip}.log",
		"devices": [{"name": "mapped", "source_ip": "::ffff:192.0.2.11"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Devices[0].SourceIP != "192.0.2.11" {
		t.Fatalf("ip %s", inv.Devices[0].SourceIP)
	}
	if inv.MonitorOverdueSeconds != DefaultOverdueSeconds {
		t.Fatalf("overdue %d", inv.MonitorOverdueSeconds)
	}
}

func TestParseRejectsAmbiguousEntries(t *testing.T) {
	base := `{"log_path_template":"/var/log/{source_ip}.log","devices":%s}`
	cases := []struct {
		devices string
		want    string
	}{
		{`[{"name":"a","source_ip":"192.0.2.1"},{"name":"A","source_ip":"192.0.2.2"}]`, "duplicate device name"},
		{`[{"name":"a","source_ip":"192.0.2.1"},{"name":"b","source_ip":"192.0.2.1"}]`, "duplicate source IP"},
		{`[{"name":"a","source_ip":"192.0.2.1"},{"name":"b","source_ip":"192.0.2.2","log_path":"/var/log/192.0.2.1.log"}]`, "duplicate"},
		{`[{"name":"bad","source_ip":"not-an-ip"}]`, "source_ip"},
		{`[{"name":"bad","source_ip":"fe80::1%eth0"}]`, "source_ip"},
		{`[{"name":"bad","source_ip":"192.0.2.1","protocol":"tls"}]`, "protocol"},
		{`[{"name":"bad","source_ip":"192.0.2.1","tcp_port":514}]`, "TCP"},
		{`[{"name":"bad","source_ip":"192.0.2.1","log_path":"/var/log/../secrets"}]`, ".."},
		{`[{"name":" spaced ","source_ip":"192.0.2.1"}]`, "surrounding space"},
		{`[]`, "nonempty"},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(sprintf(base, tc.devices)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("devices %s: got %v, want %q", tc.devices, err, tc.want)
		}
	}
}

func TestParseRejectsBadSettingsAndDuplicateKeys(t *testing.T) {
	cases := []string{
		`{"stale_after_seconds":0,"devices":[{"name":"a","source_ip":"192.0.2.1","log_path":"/var/log/a"}]}`,
		`{"stale_after_seconds":true,"devices":[{"name":"a","source_ip":"192.0.2.1","log_path":"/var/log/a"}]}`,
		`{"monitor_overdue_seconds":-1,"devices":[{"name":"a","source_ip":"192.0.2.1","log_path":"/var/log/a"}]}`,
		`{"tcp_port":65536,"devices":[{"name":"a","source_ip":"192.0.2.1","protocol":"tcp"}]}`,
		`{"log_path_template":"/var/log/{device}/log","devices":[{"name":"a","source_ip":"192.0.2.1"}]}`,
		`{"unexpected_key":1,"devices":[{"name":"a","source_ip":"192.0.2.1","log_path":"/var/log/a"}]}`,
		`{"devices":[],"devices":[]}`,
	}
	for _, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	_, err := Parse([]byte(`{"devices":[{"name":"a","name":"b","source_ip":"192.0.2.1","log_path":"/tmp/a"}]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("nested duplicate: %v", err)
	}
}

func TestLogDirectoriesConstrainDevicePaths(t *testing.T) {
	inv, err := Parse([]byte(`{
		"log_directories": ["/var/log/remote/", "/var/log/firewall"],
		"devices": [
			{"name": "edge", "source_ip": "192.0.2.10"},
			{"name": "fw", "source_ip": "192.0.2.12", "log_directory": "/var/log/firewall"},
			{"name": "custom", "source_ip": "192.0.2.13", "log_path": "/var/log/firewall/custom.log"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Devices[0].LogPath != "/var/log/remote/192.0.2.10/syslog.log" {
		t.Fatalf("default path %s", inv.Devices[0].LogPath)
	}
	if inv.Devices[1].LogPath != "/var/log/firewall/192.0.2.12/syslog.log" {
		t.Fatalf("chosen directory path %s", inv.Devices[1].LogPath)
	}
	if len(inv.LogDirectories) != 2 || inv.LogDirectories[0] != "/var/log/remote" {
		t.Fatalf("directories %#v", inv.LogDirectories)
	}

	_, err = Parse([]byte(`{
		"log_directories": ["/var/log/remote"],
		"devices": [{"name": "fw", "source_ip": "192.0.2.12", "log_path": "/var/log/firewall/fw.log"}]
	}`))
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside path error: %v", err)
	}
	_, err = Parse([]byte(`{"log_directories": ["/var/log/../etc"], "devices": [{"name": "a", "source_ip": "192.0.2.1"}]}`))
	if err == nil || !strings.Contains(err.Error(), "..") {
		t.Fatalf("dotdot error: %v", err)
	}
}

func TestApplyLogDirectoriesCreatesAndUpdates(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/devices.json"
	if err := ApplyLogDirectories(path, []string{"/var/log/remote/", "/var/log/firewall"}); err != nil {
		t.Fatal(err)
	}
	inv, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Devices[0].Name != "edge-router-01" || inv.Devices[0].LogPath != "/var/log/remote/192.0.2.10/syslog.log" {
		t.Fatalf("starter %+v", inv.Devices[0])
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := ApplyLogDirectories(path, []string{"/data/syslog"}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Devices[2].Name != "firewall-01" || updated.Devices[0].LogPath != "/data/syslog/192.0.2.10/syslog.log" {
		t.Fatalf("updated devices %+v", updated.Devices)
	}
	if len(updated.LogDirectories) != 1 || updated.LogDirectories[0] != "/data/syslog" {
		t.Fatalf("updated directories %#v", updated.LogDirectories)
	}

	err = ApplyLogDirectories(path, []string{"/var/log/remote"})
	// The starter devices have no explicit log_path, so this succeeds and moves them.
	if err != nil {
		t.Fatal(err)
	}
	forced := dir + "/forced.json"
	if err := os.WriteFile(forced, []byte(`{
		"log_directories": ["/var/log/firewall"],
		"devices": [{"name": "fw", "source_ip": "192.0.2.12", "log_path": "/var/log/firewall/fw.log"}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(forced)
	err = ApplyLogDirectories(forced, []string{"/var/log/remote"})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected outside error, got %v", err)
	}
	after, _ := os.ReadFile(forced)
	if string(before) != string(after) {
		t.Fatal("rejected directory update changed the inventory")
	}
	_ = original
}

func sprintf(format, arg string) string {
	return strings.Replace(format, "%s", arg, 1)
}
