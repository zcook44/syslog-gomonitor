package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syslog-gomonitor/internal/inventory"
	"syslog-gomonitor/internal/session"
	"syslog-gomonitor/internal/status"
)

type fakeLister struct {
	sessions map[session.Endpoint]struct{}
	err      error
}

func (f fakeLister) Established(context.Context) (map[session.Endpoint]struct{}, error) {
	return f.sessions, f.err
}

func TestSampleCombinesSignalsAndHidesLogText(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0).UTC()
	secret := "SECRET_LOG_LINE_SHOULD_NOT_LEAK"
	udpPath := filepath.Join(dir, "udp.log")
	tcpPath := filepath.Join(dir, "tcp.log")
	stalePath := filepath.Join(dir, "stale.log")
	writeAged(t, udpPath, []byte(secret+"\n"), now)
	writeAged(t, tcpPath, []byte(secret+"\n"), now)
	writeAged(t, stalePath, []byte("old\n"), now.Add(-2*time.Hour))

	inv := inventory.Inventory{
		MonitorOverdueSeconds: 180,
		Devices: []inventory.Device{
			{Name: "udp", SourceIP: "192.0.2.10", LogPath: udpPath, Protocol: "udp", StaleAfterSeconds: 60},
			{Name: "tcp-up", SourceIP: "192.0.2.11", LogPath: tcpPath, Protocol: "tcp", TCPPort: 514, StaleAfterSeconds: 60},
			{Name: "tcp-old", SourceIP: "192.0.2.12", LogPath: stalePath, Protocol: "tcp", TCPPort: 514, StaleAfterSeconds: 60},
		},
	}
	eng := New(inv, Options{
		Now:    func() time.Time { return now },
		Lister: fakeLister{sessions: map[session.Endpoint]struct{}{{IP: "192.0.2.11", Port: 514}: {}}},
	})
	snap := eng.Sample(context.Background())
	byName := map[string]string{}
	for _, device := range snap.Devices {
		byName[device.Name] = device.Status + "/" + device.LogStatus + "/" + device.Connection
	}
	if byName["udp"] != status.Healthy+"/"+status.LogRecent+"/"+status.ConnUDP {
		t.Fatalf("udp %s", byName["udp"])
	}
	if byName["tcp-up"] != status.Healthy+"/"+status.LogRecent+"/"+status.ConnConnected {
		t.Fatalf("tcp-up %s", byName["tcp-up"])
	}
	if byName["tcp-old"] != status.Attention+"/"+status.LogStale+"/"+status.ConnDisconnected {
		t.Fatalf("tcp-old %s", byName["tcp-old"])
	}

	eng.lister = fakeLister{err: os.ErrNotExist}
	unknown := eng.Sample(context.Background())
	for _, device := range unknown.Devices {
		if device.Name == "tcp-up" && device.Status != status.Unknown {
			t.Fatalf("ss failure became %s (%s)", device.Status, device.ConnectionDetail)
		}
		if device.Name == "tcp-old" && device.Status != status.Attention {
			t.Fatalf("stale log with ss failure became %s", device.Status)
		}
	}

	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("snapshot included log contents")
	}
}

func TestScanSizesPersistsForALaterProcess(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "remote", "syslog.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("event\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath+".1", make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := inventory.Inventory{
		MonitorOverdueSeconds: 30,
		Devices: []inventory.Device{{
			Name: "router", SourceIP: "192.0.2.10", LogPath: logPath,
			Protocol: "udp", StaleAfterSeconds: 900,
		}},
	}
	statePath := filepath.Join(dir, "sizes.json")
	now := time.Now().UTC()
	eng := New(inv, Options{
		StatePath:    statePath,
		SizeInterval: time.Hour,
		Now:          func() time.Time { return now },
	})
	if err := eng.ScanSizes(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := eng.Latest().Devices[0]
	if !first.SizeKnown || first.StoredBytes != int64(len("event\n")+100) || first.StoredFiles != 2 {
		t.Fatalf("first measurement %+v", first)
	}

	later := New(inv, Options{StatePath: statePath, SizeInterval: time.Hour, Now: func() time.Time { return now }})
	if err := later.ReloadState(); err != nil {
		t.Fatal(err)
	}
	snap := later.Sample(context.Background())
	if snap.Devices[0].StoredBytes != first.StoredBytes || snap.SizeMeasuredAt == nil {
		t.Fatalf("reloaded %+v", snap.Devices[0])
	}
	if later.needsInitialScan() {
		t.Fatal("fresh snapshot should not be scanned again")
	}
}

func writeAged(t *testing.T, path string, body []byte, when time.Time) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}
