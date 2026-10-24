package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCheckSizeAndDemoOnce(t *testing.T) {
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	if code := Run([]string{"demo", "--once", "--root", dir, "--tcp-port", "0"}); code != 0 {
		t.Fatalf("demo --once exited %d", code)
	}
	page, err := os.ReadFile(filepath.Join(dir, "web", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("edge-router-01")) || !bytes.Contains(page, []byte("Needs attention")) {
		t.Fatal("demo page is missing expected devices")
	}
	if bytes.Contains(page, []byte("changed state to up")) {
		t.Fatal("demo page included log contents")
	}
	if _, err := os.Stat(filepath.Join(dir, "sizes.json")); err != nil {
		t.Fatal(err)
	}

	config := filepath.Join(dir, "devices.json")
	if code := Run([]string{"validate", "--config", config}); code != 0 {
		t.Fatalf("validate exited %d", code)
	}
	if code := Run([]string{"size", "--config", config, "--state", filepath.Join(dir, "sizes.json")}); code != 0 {
		t.Fatalf("size exited %d", code)
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := Run([]string{"check", "--config", config, "--state", filepath.Join(dir, "sizes.json")})
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if code != 0 {
		t.Fatalf("check exited %d\n%s", code, buf.String())
	}
	var snap map[string]any
	if err := json.Unmarshal(buf.Bytes(), &snap); err != nil {
		t.Fatalf("check output: %v\n%s", err, buf.String())
	}
	if snap["summary"] == nil {
		t.Fatalf("check JSON %#v", snap)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"devices":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"check", "--config", bad, "--state", filepath.Join(dir, "unused.json")}); code != 1 {
		t.Fatalf("bad inventory exited %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "unused.json")); !os.IsNotExist(err) {
		t.Fatal("bad inventory wrote a size snapshot")
	}
	if code := Run([]string{"nope"}); code != 2 {
		t.Fatalf("unknown command exited %d", code)
	}
	if !strings.Contains(string(page), "firewall-01") {
		t.Fatal("custom log path device missing")
	}
}
