package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syslog-gomonitor/internal/engine"
	"syslog-gomonitor/internal/inventory"
)

func TestDashboardAndStream(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "syslog.log")
	secret := "SECRET_LOG_LINE_SHOULD_NOT_LEAK"
	if err := os.WriteFile(logPath, []byte(secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := inventory.Inventory{
		MonitorOverdueSeconds: 90,
		Devices: []inventory.Device{{
			Name: "<b>router</b>", SourceIP: "192.0.2.10", LogPath: logPath,
			Protocol: "udp", StaleAfterSeconds: 900,
		}},
	}
	eng := engine.New(inv, engine.Options{PollInterval: time.Second})
	eng.Sample(context.Background())
	srv := httptest.NewServer(Handler(eng))
	t.Cleanup(srv.Close)

	page := get(t, srv.URL+"/")
	if strings.Contains(page, secret) || strings.Contains(page, "<b>router</b>") {
		t.Fatal("page leaked log text or unescaped name")
	}
	if !strings.Contains(page, "&lt;b&gt;router&lt;/b&gt;") {
		t.Fatal("name was not escaped")
	}
	if !strings.Contains(page, "Receiving") {
		t.Fatal(page)
	}

	status := get(t, srv.URL+"/api/status")
	if strings.Contains(status, secret) || !strings.Contains(status, "192.0.2.10") {
		t.Fatal(status)
	}
	if get(t, srv.URL+"/healthz") != "ok\n" {
		t.Fatal("health")
	}

	res, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "data: ") || strings.Contains(line, secret) {
		t.Fatalf("event %q", line)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("post status %d", post.StatusCode)
	}
	missing, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status %d", missing.StatusCode)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s status %d", url, res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
