package demo_test

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syslog-gomonitor/internal/dashboard"
	"syslog-gomonitor/internal/demo"
	"syslog-gomonitor/internal/engine"
	"syslog-gomonitor/internal/snapshot"
)

func TestScenarioShowsProductionStatesAndHourlySize(t *testing.T) {
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	sc, err := demo.Start(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sc.Close)

	eng := engine.New(sc.Inv, engine.Options{
		StatePath:    filepath.Join(dir, "sizes.json"),
		SizeInterval: time.Hour,
		PollInterval: time.Second,
	})
	if err := eng.ScanSizes(context.Background()); err != nil {
		t.Fatal(err)
	}

	snap := waitForSessions(t, eng)
	byName := index(snap)
	for _, spec := range demo.Specs {
		got, ok := byName[spec.Name]
		if !ok {
			t.Fatalf("missing device %s", spec.Name)
		}
		if got.LogStatus != spec.WantLog || got.Connection != spec.WantConn || got.Status != spec.WantStatus {
			t.Errorf("%s: got log=%s conn=%s status=%s (%s / %s)",
				spec.Name, got.LogStatus, got.Connection, got.Status, got.LogDetail, got.ConnectionDetail)
		}
	}

	edge := byName["edge-router-01"]
	edgeDir := filepath.Dir(edge.LogPath)
	gzPath := filepath.Join(edgeDir, "syslog.log-20261022.gz")
	wantStored := sizeOf(t, edge.LogPath) + sizeOf(t, edge.LogPath+".1") + sizeOf(t, gzPath)
	if edge.StoredBytes != wantStored || edge.StoredFiles != 3 {
		t.Fatalf("edge stored %d bytes across %d files, want %d across 3", edge.StoredBytes, edge.StoredFiles, wantStored)
	}
	if !strings.Contains(edge.SizeDetail, "symlink") {
		t.Fatalf("edge detail %q", edge.SizeDetail)
	}
	assertGzipIsNotInflated(t, gzPath)

	fw := byName["firewall-01"]
	if fw.ActiveBytes == nil || *fw.ActiveBytes != 0 {
		t.Fatalf("firewall active %+v", fw.ActiveBytes)
	}
	if fw.StoredBytes != sizeOf(t, fw.LogPath+".1") || fw.StoredFiles != 2 {
		t.Fatalf("firewall stored %+v", fw)
	}

	missing := byName["access-switch-03"]
	if missing.StoredBytes != 0 || !strings.Contains(missing.SizeDetail, "missing") {
		t.Fatalf("missing device %+v", missing)
	}
	if _, err := os.Stat(missing.LogPath); !os.IsNotExist(err) {
		t.Fatalf("missing log exists: %v", err)
	}

	lab := byName["lab-switch-01"]
	if lab.StoredBytes != 0 || !strings.Contains(lab.SizeDetail, "symlink") {
		t.Fatalf("symlink device %+v", lab)
	}

	dmz := byName["dmz-firewall-01"]
	if dmz.StoredBytes != sizeOf(t, dmz.LogPath) {
		t.Fatalf("future file was not counted: %+v", dmz)
	}

	stale := byName["dist-switch-02"]
	before, err := os.Stat(stale.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	storedBefore := edge.StoredBytes
	activeBefore := *edge.ActiveBytes
	if err := sc.Refresh(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(stale.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatal("refresh changed the stale device")
	}
	updated := index(eng.Sample(context.Background()))
	edgeNow := updated["edge-router-01"]
	if edgeNow.ActiveBytes == nil || *edgeNow.ActiveBytes <= activeBefore {
		t.Fatal("active file did not grow on the fast loop")
	}
	if edgeNow.StoredBytes != storedBefore {
		t.Fatalf("hourly stored size changed during a status sample: %d -> %d", storedBefore, edgeNow.StoredBytes)
	}

	page, err := dashboard.Page(snap)
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, spec := range demo.Specs {
		if !strings.Contains(body, spec.Name) {
			t.Errorf("page missing %s", spec.Name)
		}
		if !strings.Contains(body, `class="pill `+spec.WantStatus+`"`) {
			t.Errorf("page missing pill for %s", spec.Name)
		}
	}
	if strings.Contains(body, "changed state to up") {
		t.Fatal("dashboard included log contents")
	}
}

func waitForSessions(t *testing.T, eng *engine.Engine) snapshot.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var snap snapshot.Snapshot
	for {
		snap = eng.Sample(context.Background())
		rows := index(snap)
		up := rows["core-switch-01"].Connection == "connected" && rows["core-router-02"].Connection == "connected"
		down := rows["wan-router-02"].Connection == "disconnected"
		if up && down || time.Now().After(deadline) {
			return snap
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func index(snap snapshot.Snapshot) map[string]snapshot.Device {
	out := make(map[string]snapshot.Device, len(snap.Devices))
	for _, device := range snap.Devices {
		out[device.Name] = device
	}
	return out
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func assertGzipIsNotInflated(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(plain)) <= info.Size() {
		t.Fatalf("gzip on disk (%d) is not smaller than its contents (%d)", info.Size(), len(plain))
	}
}
