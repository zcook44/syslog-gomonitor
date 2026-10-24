package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sizes.json")
	if err := Write(path, []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("two"), 0o640); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "two" {
		t.Fatalf("body %q", body)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "sizes.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("changed"), 0o640); err == nil {
		t.Fatal("expected symlink refusal")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "keep" {
		t.Fatalf("target changed to %q", body)
	}
}
