package server

import (
	"bufio"
	"context"
	"encoding/json"
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

func TestAdoptFromTheDashboard(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	if err := os.MkdirAll(filepath.Join(remote, "192.0.2.50"), 0o755); err != nil {
		t.Fatal(err)
	}
	switchLog := filepath.Join(remote, "192.0.2.50", "syslog.log")
	secret := "SECRET_LOG_LINE_SHOULD_NOT_LEAK"
	if err := os.WriteFile(switchLog, []byte(secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "devices.json")
	raw := []byte(`{
		"log_directories": ["` + remote + `"],
		"log_path_template": "` + remote + `/{source_ip}/syslog.log",
		"devices": [{"name": "edge", "source_ip": "192.0.2.10", "protocol": "udp"}]
	}`)
	if err := os.WriteFile(config, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	inv, err := inventory.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(inv, engine.Options{ConfigPath: config})
	eng.RefreshCandidates()
	eng.Sample(context.Background())
	srv := httptest.NewServer(Handler(eng))
	t.Cleanup(srv.Close)

	page := get(t, srv.URL+"/")
	if !strings.Contains(page, "192.0.2.50") || !strings.Contains(page, "Adopt") {
		t.Fatal("page did not offer the new switch")
	}
	if strings.Contains(page, secret) {
		t.Fatal("page leaked log text")
	}

	cross, err := http.NewRequest(http.MethodPost, srv.URL+"/api/adopt", strings.NewReader(`{"name":"x","log_path":"`+switchLog+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	cross.Header.Set("Content-Type", "application/json")
	cross.Header.Set("X-Requested-With", "syslog-gomonitor")
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	refused, err := http.DefaultClient.Do(cross)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site status %d", refused.StatusCode)
	}
	same, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(same) != string(raw) {
		t.Fatal("cross-site adopt changed the inventory")
	}

	body := `{"name":"access-switch-04","protocol":"udp","source_ip":"10.1.1.1","log_path":` + jsonString(switchLog) + `}`
	res := postJSON(t, srv.URL+"/api/adopt", body)
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(res.Body)
		t.Fatalf("adopt status %d %s", res.StatusCode, msg)
	}
	res.Body.Close()
	status := get(t, srv.URL+"/api/status")
	if !strings.Contains(status, "access-switch-04") || strings.Contains(status, secret) {
		t.Fatalf("status %s", status)
	}
	if strings.Contains(status, switchLog) && strings.Contains(status, `"candidates"`) {
		var snap struct {
			Candidates []struct {
				LogPath string `json:"log_path"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(status), &snap); err != nil {
			t.Fatal(err)
		}
		for _, item := range snap.Candidates {
			if item.LogPath == switchLog {
				t.Fatal("adopted switch remains a candidate")
			}
		}
	}
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "syslog-gomonitor")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
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
