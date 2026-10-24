package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syslog-gomonitor/internal/status"
)

func TestSortAndSummary(t *testing.T) {
	rows := []Device{
		{Name: "zeta", Status: status.Healthy, SizeKnown: true, StoredBytes: 10},
		{Name: "alpha", Status: status.Attention, SizeKnown: true, StoredBytes: 5},
		{Name: "mid", Status: status.Unknown, StoredError: "permission denied"},
	}
	Sort(rows)
	if rows[0].Name != "alpha" || rows[1].Name != "mid" || rows[2].Name != "zeta" {
		t.Fatalf("order %s %s %s", rows[0].Name, rows[1].Name, rows[2].Name)
	}
	sum := Summarize(rows)
	if sum.Healthy != 1 || sum.Attention != 1 || sum.Unknown != 1 || sum.StoredBytes != 15 || sum.SizeErrors != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestSizeStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sizes.json")
	when := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC)
	state := SizeState{
		MeasuredAt: when,
		Devices: map[string]StoredSize{
			"192.0.2.10": {Bytes: 42, Files: 2, Detail: "2 files on disk"},
		},
	}
	if err := SaveSizeState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSizeState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.MeasuredAt.Equal(when) || loaded.Devices["192.0.2.10"].Bytes != 42 {
		t.Fatalf("loaded %+v", loaded)
	}
	if err := os.WriteFile(path, []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSizeState(path); err == nil {
		t.Fatal("expected corrupt snapshot to fail")
	}
}

func TestFormatText(t *testing.T) {
	when := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC)
	size := int64(128)
	snap := Snapshot{
		GeneratedAt:    when,
		SizeMeasuredAt: &when,
		Devices: []Device{{
			Name: "edge-router-01", Status: status.Healthy, LogStatus: status.LogRecent,
			Connection: status.ConnUDP, ActiveBytes: &size, SizeKnown: true, StoredBytes: 4096,
		}},
	}
	snap.Summary = Summarize(snap.Devices)
	text := FormatText(snap)
	if !strings.Contains(text, "edge-router-01") || !strings.Contains(text, "healthy") {
		t.Fatalf("report:\n%s", text)
	}
}
