package storage

import (
	"os"
	"path/filepath"
	"testing"

	"syslog-gomonitor/internal/inventory"
)

func TestIsStoredLog(t *testing.T) {
	base := "syslog.log"
	yes := []string{
		"syslog.log",
		"syslog.log.1",
		"syslog.log.2.gz",
		"syslog.log-20261022",
		"syslog.log-20261022.gz",
	}
	no := []string{
		"notes.txt",
		"syslog.logbak",
		"syslog.log.offset",
		"syslog.log.state",
		"other.log",
		"Syslog.log",
	}
	for _, name := range yes {
		if !IsStoredLog(base, name) {
			t.Errorf("expected %s to count", name)
		}
	}
	for _, name := range no {
		if IsStoredLog(base, name) {
			t.Errorf("expected %s to be ignored", name)
		}
	}
	if !IsStoredLog("firewall-01.log", "firewall-01.log.1") {
		t.Fatal("custom base should match its rotation")
	}
}

func TestScanSumsRotationsAndSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	edgeDir := filepath.Join(root, "remote", "192.0.2.10")
	fwDir := filepath.Join(root, "firewall")
	if err := os.MkdirAll(edgeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fwDir, 0o755); err != nil {
		t.Fatal(err)
	}
	edge := filepath.Join(edgeDir, "syslog.log")
	mustWrite(t, edge, 100)
	mustWrite(t, edge+".1", 4096)
	mustWrite(t, filepath.Join(edgeDir, "syslog.log-20261022.gz"), 300)
	mustWrite(t, filepath.Join(edgeDir, "notes.txt"), 64)
	mustWrite(t, filepath.Join(edgeDir, "syslog.log.offset"), 32)
	if err := os.Symlink(edge+".1", filepath.Join(edgeDir, "syslog.log.3")); err != nil {
		t.Fatal(err)
	}

	fw := filepath.Join(fwDir, "firewall-01.log")
	mustWrite(t, fw, 0)
	mustWrite(t, fw+".1", 8192)
	mustWrite(t, filepath.Join(fwDir, "other.log"), 777)

	missingDir := filepath.Join(root, "remote", "192.0.2.22")
	linkDir := filepath.Join(root, "remote", "192.0.2.40")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkLog := filepath.Join(linkDir, "syslog.log")
	if err := os.Symlink(edge, linkLog); err != nil {
		t.Fatal(err)
	}

	devices := []inventory.Device{
		{Name: "edge", SourceIP: "192.0.2.10", LogPath: edge},
		{Name: "fw", SourceIP: "192.0.2.30", LogPath: fw},
		{Name: "missing", SourceIP: "192.0.2.22", LogPath: filepath.Join(missingDir, "syslog.log")},
		{Name: "link", SourceIP: "192.0.2.40", LogPath: linkLog},
	}
	got := Scan(devices)

	edgeM := got["192.0.2.10"]
	if edgeM.Bytes != 100+4096+300 || edgeM.Files != 3 || edgeM.SkippedSymlinks != 1 {
		t.Fatalf("edge measurement %+v", edgeM)
	}
	if edgeM.Err != "" {
		t.Fatalf("edge error %s", edgeM.Err)
	}

	fwM := got["192.0.2.30"]
	if fwM.Bytes != 8192 || fwM.Files != 2 {
		t.Fatalf("firewall measurement %+v", fwM)
	}

	missing := got["192.0.2.22"]
	if missing.Bytes != 0 || missing.Files != 0 || missing.Detail != "Log directory is missing" {
		t.Fatalf("missing measurement %+v", missing)
	}

	link := got["192.0.2.40"]
	if link.Bytes != 0 || link.Files != 0 || link.SkippedSymlinks != 1 {
		t.Fatalf("symlink measurement %+v", link)
	}
}

func TestScanRefusesSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(real, "syslog.log"), 50)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	got := Scan([]inventory.Device{{
		Name: "a", SourceIP: "192.0.2.1", LogPath: filepath.Join(alias, "syslog.log"),
	}})
	if got["192.0.2.1"].Bytes != 0 || got["192.0.2.1"].Err == "" {
		t.Fatalf("symlinked dir was followed: %+v", got["192.0.2.1"])
	}
}

func TestScanSumsEveryDayOfADatedName(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "edge-router-01-20261027-syslog.log"), 100)
	mustWrite(t, filepath.Join(dir, "edge-router-01-20261028-syslog.log"), 50)
	mustWrite(t, filepath.Join(dir, "edge-router-01-20261027-syslog.log.1"), 10)
	mustWrite(t, filepath.Join(dir, "edge-router-01-20261028-syslog.log.gz"), 5)
	mustWrite(t, filepath.Join(dir, "edge-router-01-20261028-syslog.log.offset"), 32)
	mustWrite(t, filepath.Join(dir, "core-switch-01-20261028-syslog.log"), 1000)
	got := Scan([]inventory.Device{{
		Name: "edge", SourceIP: "192.0.2.10",
		LogPath: filepath.Join(dir, "edge-router-01-20261027-syslog.log"),
	}})
	edge := got["192.0.2.10"]
	if edge.Bytes != 165 || edge.Files != 4 {
		t.Fatalf("dated measurement %+v", edge)
	}
}

func mustWrite(t *testing.T, path string, n int) {
	t.Helper()
	body := make([]byte, n)
	for i := range body {
		body[i] = 'x'
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
