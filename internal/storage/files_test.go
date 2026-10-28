package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListClassifiesTheLogDirectory(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "edge-router-01-20261028-syslog.log")
	newer := filepath.Join(dir, "edge-router-01-20261029-syslog.log")
	mustWrite(t, older, 20)
	mustWrite(t, newer, 40)
	mustWrite(t, older+".1", 8)
	mustWrite(t, filepath.Join(dir, "notes.txt"), 4)
	if err := os.Mkdir(filepath.Join(dir, "olddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newer, filepath.Join(dir, "edge-router-01-20261030-syslog.log")); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	secret := "SECRET_LOG_LINE_SHOULD_NOT_LEAK"
	if err := os.WriteFile(newer, []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	got := List(older)
	if got.Note != "" || got.Directory != dir {
		t.Fatalf("listing %+v", got)
	}
	kinds := map[string]string{}
	for _, file := range got.Files {
		kinds[file.Name] = file.Kind
		if file.Name == "edge-router-01-20261029-syslog.log" && file.Bytes == int64(len(secret)) && file.Kind != "active" {
			t.Fatalf("active file %+v", file)
		}
	}
	if kinds["edge-router-01-20261029-syslog.log"] != "active" {
		t.Fatalf("kinds %#v", kinds)
	}
	if kinds["edge-router-01-20261028-syslog.log"] != "series" || kinds["edge-router-01-20261028-syslog.log.1"] != "rotation" {
		t.Fatalf("kinds %#v", kinds)
	}
	if kinds["notes.txt"] != "other" || kinds["olddir"] != "directory" || kinds["edge-router-01-20261030-syslog.log"] != "symlink" {
		t.Fatalf("kinds %#v", kinds)
	}
	if got.Files[0].Kind != "active" {
		t.Fatalf("first file %+v", got.Files[0])
	}
}

func TestListReportsAMissingDirectory(t *testing.T) {
	got := List(filepath.Join(t.TempDir(), "missing", "syslog.log"))
	if got.Note != "Log directory is missing." || len(got.Files) != 0 {
		t.Fatalf("listing %+v", got)
	}
}
