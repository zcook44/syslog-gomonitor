package discover

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syslog-gomonitor/internal/inventory"
)

func TestScanFindsNewSwitchAndSkipsNoiseShapes(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	knownDir := filepath.Join(remote, "192.0.2.10")
	newDir := filepath.Join(remote, "192.0.2.50")
	hostDir := filepath.Join(remote, "weird-host")
	if err := os.MkdirAll(knownDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := "SECRET_LOG_LINE_SHOULD_NOT_LEAK"
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(knownDir, "syslog.log"), secret+"\n")
	write(filepath.Join(knownDir, "syslog.log.1"), "rotated\n")
	write(filepath.Join(knownDir, "notes.txt"), "not a source\n")
	write(filepath.Join(newDir, "syslog.log"), secret+" from the new switch\n")
	write(filepath.Join(newDir, "messages.log"), secret+" messages\n")
	write(filepath.Join(newDir, "notes.txt"), "not a log\n")
	write(filepath.Join(newDir, "syslog.log.1"), "old\n")
	write(filepath.Join(knownDir, "other.log"), "already tracked address\n")
	write(filepath.Join(hostDir, "syslog.log"), "hostname layout\n")
	trackedHost := filepath.Join(remote, "tracked-host")
	if err := os.MkdirAll(trackedHost, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(trackedHost, "syslog.log"), "already chosen\n")
	write(filepath.Join(trackedHost, "messages.log"), "leftover convention\n")
	write(filepath.Join(remote, "noise.log"), "garbage from a bad rule\n")
	write(filepath.Join(remote, "syslog.log.1"), "orphan rotation\n")
	write(filepath.Join(remote, "syslog.log.offset"), "state\n")
	if err := os.Symlink(filepath.Join(newDir, "syslog.log"), filepath.Join(remote, "linked.log")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(filepath.Join(outside, "192.0.2.77"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(outside, "192.0.2.77", "syslog.log"), "outside\n")
	if err := os.Symlink(outside, filepath.Join(remote, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	inv, err := inventory.Parse([]byte(`{
		"log_directories": ["` + remote + `"],
		"log_path_template": "` + remote + `/{source_ip}/syslog.log",
		"devices": [
			{"name": "edge", "source_ip": "192.0.2.10", "protocol": "udp"},
			{"name": "tracked", "source_ip": "192.0.2.60", "protocol": "udp", "log_path": "` + filepath.Join(trackedHost, "syslog.log") + `"}
		],
		"ignored": [{"log_path": "` + filepath.Join(remote, "hidden.log") + `"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(remote, "hidden.log"), "hidden\n")

	result := Scan(context.Background(), inv)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("discovery included log contents")
	}
	got := map[string]Candidate{}
	for _, item := range result.Candidates {
		got[filepath.Base(filepath.Dir(item.LogPath))+"/"+filepath.Base(item.LogPath)] = item
		if filepath.Dir(item.LogPath) == remote {
			got[filepath.Base(item.LogPath)] = item
		}
	}
	if _, ok := got["192.0.2.10/syslog.log"]; ok {
		t.Fatal("known device was offered again")
	}
	if _, ok := got["notes.txt"]; ok || got["syslog.log.1"].LogPath != "" || got["linked.log"].LogPath != "" {
		t.Fatalf("auxiliary or symlink listed: %#v", got)
	}
	sw, ok := got["192.0.2.50/syslog.log"]
	if !ok || sw.SourceIP != "192.0.2.50" || sw.SuggestedName != "192.0.2.50" {
		t.Fatalf("new switch %#v", sw)
	}
	if !strings.Contains(sw.Detail, "Choose which") || sw.ActiveBytes == 0 || len(sw.LogFiles) != 2 {
		t.Fatalf("switch detail %#v", sw)
	}
	if sw.LogFiles[0].Name != "messages.log" || sw.LogFiles[1].Name != "syslog.log" || sw.LogPath != sw.LogFiles[1].Path {
		t.Fatalf("log choices %#v", sw.LogFiles)
	}
	if _, ok := got["192.0.2.50/messages.log"]; ok {
		t.Fatal("messages.log was a separate card")
	}
	if _, ok := got["tracked-host/syslog.log"]; ok || got["tracked-host/messages.log"].LogPath != "" {
		t.Fatal("tracked directory was offered again")
	}
	if _, ok := got["192.0.2.50/notes.txt"]; ok || got["192.0.2.10/other.log"].LogPath != "" {
		t.Fatalf("non-log or already tracked address listed: %#v", got)
	}
	host, ok := got["weird-host/syslog.log"]
	if !ok || host.SourceIP != "" || host.SuggestedName != "weird-host" {
		t.Fatalf("hostname dir %#v", host)
	}
	noise, ok := got["noise.log"]
	if !ok || noise.SourceIP != "" || noise.SuggestedName != "noise" {
		t.Fatalf("noise %#v", noise)
	}
	if _, ok := got["hidden.log"]; ok {
		t.Fatal("ignored file was listed")
	}
	if _, ok := got["192.0.2.77/syslog.log"]; ok {
		t.Fatal("followed a symlink directory")
	}
}

func TestScanGroupsDatedNamesAndHidesTheSeries(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	child := filepath.Join(remote, "10.1.1.1")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yesterday := filepath.Join(remote, "edge-router-01-20261027-syslog.log")
	write(yesterday, "old\n")
	write(filepath.Join(remote, "edge-router-01-20261028-syslog.log"), "new\n")
	write(filepath.Join(remote, "core-switch-01-20261028-syslog.log"), "core\n")
	write(filepath.Join(child, "sw-20261027-syslog.log"), "old switch\n")
	write(filepath.Join(child, "sw-20261028-syslog.log"), "new switch\n")
	write(filepath.Join(child, "sw-20261028-messages.log"), "messages\n")

	inv, err := inventory.Parse([]byte(`{
		"log_directories": ["` + remote + `"],
		"devices": [{"name": "edge", "source_ip": "192.0.2.10", "protocol": "udp", "log_path": "` + yesterday + `"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	result := Scan(context.Background(), inv)
	got := map[string]Candidate{}
	for _, item := range result.Candidates {
		got[filepath.Base(item.LogPath)] = item
	}
	if _, ok := got["edge-router-01-20261028-syslog.log"]; ok || got["edge-router-01-20261027-syslog.log"].LogPath != "" {
		t.Fatalf("adopted series was offered again: %#v", got)
	}
	core, ok := got["core-switch-01-20261028-syslog.log"]
	if !ok || core.SuggestedName != "core-switch-01" || core.LogFiles[0].Name != "core-switch-01-{date}-syslog.log" {
		t.Fatalf("core series %#v", core)
	}
	if !strings.Contains(core.Detail, "newest") {
		t.Fatalf("core detail %q", core.Detail)
	}
	sw, ok := got["sw-20261028-syslog.log"]
	if !ok || sw.SourceIP != "10.1.1.1" || len(sw.LogFiles) != 2 {
		t.Fatalf("switch series %#v", sw)
	}
	if sw.LogFiles[0].Name != "sw-{date}-messages.log" || sw.LogFiles[1].Name != "sw-{date}-syslog.log" {
		t.Fatalf("switch choices %#v", sw.LogFiles)
	}
}

func TestScanCapsTheList(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxShown+5; i++ {
		name := filepath.Join(root, "junk-"+itoa(i)+".log")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := inventory.Parse([]byte(`{
		"log_directories": ["` + root + `"],
		"devices": [{"name": "edge", "source_ip": "192.0.2.10", "log_path": "` + filepath.Join(root, "edge.log") + `"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "edge.log"), []byte("known"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := Scan(context.Background(), inv)
	if len(result.Candidates) != maxShown {
		t.Fatalf("candidates %d", len(result.Candidates))
	}
	if !strings.Contains(result.Note, "Showing") {
		t.Fatalf("note %q", result.Note)
	}
}

func TestScanReportsUnreadableDirectory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	inv, err := inventory.Parse([]byte(`{
		"log_directories": ["` + missing + `"],
		"devices": [{"name": "edge", "source_ip": "192.0.2.10", "log_path": "` + filepath.Join(missing, "192.0.2.10", "syslog.log") + `"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	result := Scan(context.Background(), inv)
	if len(result.Candidates) != 0 || !strings.Contains(result.Note, "Cannot read") {
		t.Fatalf("result %+v", result)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
