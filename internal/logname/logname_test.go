package logname

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDatedNames(t *testing.T) {
	series, ok := Parse("edge-router-01-20261028-syslog.log")
	if !ok || series.Label() != "edge-router-01-{date}-syslog.log" || series.Stem() != "edge-router-01" {
		t.Fatalf("compact %#v %v", series, ok)
	}
	when, ok := series.Match("edge-router-01-20261029-syslog.log")
	if !ok || when.Format("20060102") != "20261029" {
		t.Fatalf("match %v %v", when, ok)
	}
	if _, ok := series.Match("edge-router-01-20261029-syslog.log.1"); ok {
		t.Fatal("rotation matched as the active file")
	}
	if !series.Owns("edge-router-01-20261027-syslog.log.gz") || !series.Owns("edge-router-01-20261028-syslog.log.1") {
		t.Fatal("rotations of other days should count")
	}
	if series.Owns("edge-router-01-20261028-syslog.log.offset") || series.Owns("other-20261028-syslog.log") {
		t.Fatal("state file or other device was owned")
	}

	dashed, ok := Parse("edge-router-01-2026-10-28-syslog.log")
	if !ok || dashed.Layout != "2006-01-02" || dashed.Label() != "edge-router-01-{date}-syslog.log" {
		t.Fatalf("dashed %#v %v", dashed, ok)
	}
	if _, ok := Parse("syslog.log"); ok {
		t.Fatal("stable name was treated as dated")
	}
	if _, ok := Parse("edge-20260231-syslog.log"); ok {
		t.Fatal("impossible date was accepted")
	}
}

func TestActivePicksNewestDate(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "edge-router-01-20261027-syslog.log")
	newer := filepath.Join(dir, "edge-router-01-20261028-syslog.log")
	other := filepath.Join(dir, "core-switch-01-20261029-syslog.log")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(older, "old")
	write(newer, "new")
	write(newer+".1", "rotated")
	write(other, "someone else")
	if err := os.Symlink(newer, filepath.Join(dir, "edge-router-01-20261030-syslog.log")); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if got := Active(older); got != newer {
		t.Fatalf("active %s", got)
	}
	stable := filepath.Join(dir, "syslog.log")
	write(stable, "stable")
	if got := Active(stable); got != stable {
		t.Fatalf("stable %s", got)
	}
}
