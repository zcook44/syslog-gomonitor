package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syslog-gomonitor/internal/status"
)

func TestInspectStates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "syslog.log")
	now := time.Unix(1_700_000_000, 0)
	staleAfter := 60 * time.Second

	missing := Inspect(path, now, staleAfter)
	if missing.Status != status.LogMissing {
		t.Fatalf("missing status %s", missing.Status)
	}

	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := Inspect(path, now, staleAfter)
	if empty.Status != status.LogEmpty || empty.ActiveBytes == nil || *empty.ActiveBytes != 0 {
		t.Fatalf("empty observation: %+v", empty)
	}

	writeAged(t, path, []byte("event\n"), now.Add(-60*time.Second))
	fresh := Inspect(path, now, staleAfter)
	if fresh.Status != status.LogRecent || !fresh.AgeKnown {
		t.Fatalf("fresh observation: %+v", fresh)
	}

	writeAged(t, path, []byte("event\n"), now.Add(-61*time.Second))
	stale := Inspect(path, now, staleAfter)
	if stale.Status != status.LogStale {
		t.Fatalf("stale status %s (%s)", stale.Status, stale.Detail)
	}
	if stale.Detail != "No write for 1m (limit 1m)." {
		t.Fatalf("stale detail %q", stale.Detail)
	}

	writeAged(t, path, []byte("future\n"), now.Add(time.Hour))
	future := Inspect(path, now, staleAfter)
	if future.Status != status.LogUnknown {
		t.Fatalf("future status %s", future.Status)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "other.log"), path); err != nil {
		t.Fatal(err)
	}
	link := Inspect(path, now, staleAfter)
	if link.Status != status.LogUnknown {
		t.Fatalf("symlink status %s", link.Status)
	}
	if !strings.Contains(link.Detail, "symlinks") {
		t.Fatalf("symlink detail %q", link.Detail)
	}
}

func TestUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "syslog.log")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory is not a regular file. Permission failures are covered by
	// removing search permission on the parent when the test user can do so.
	obs := Inspect(path, time.Now(), time.Minute)
	if obs.Status != status.LogUnknown {
		t.Fatalf("directory status %s", obs.Status)
	}

	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(blocked, "syslog.log")
	if err := os.WriteFile(hidden, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode 000")
	}
	denied := Inspect(hidden, time.Now(), time.Minute)
	if denied.Status != status.LogUnknown || !strings.Contains(denied.Detail, "permission denied") {
		t.Fatalf("denied observation: %+v", denied)
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
