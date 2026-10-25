package dashboard

import (
	"strings"
	"testing"
	"time"

	"syslog-gomonitor/internal/snapshot"
	"syslog-gomonitor/internal/status"
)

func TestPageEscapesInventory(t *testing.T) {
	when := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC)
	active := int64(12)
	snap := snapshot.Snapshot{
		GeneratedAt:           when,
		MonitorOverdueSeconds: 180,
		SizeIntervalSeconds:   3600,
		SizeMeasuredAt:        &when,
		Devices: []snapshot.Device{{
			Name:             `<script>alert('x')</script>`,
			SourceIP:         "192.0.2.10",
			Protocol:         "udp",
			LogPath:          "/var/log/host&log.log",
			LogStatus:        status.LogRecent,
			LogDetail:        "Last write 1s ago.",
			ActiveBytes:      &active,
			Connection:       status.ConnUDP,
			ConnectionDetail: "N/A (UDP has no connection)",
			Status:           status.Healthy,
			SizeKnown:        true,
			StoredBytes:      4096,
			StoredFiles:      2,
			SizeDetail:       "2 files on disk",
		}},
	}
	snap.Summary = snapshot.Summarize(snap.Devices)
	page, err := Page(snap)
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	if strings.Contains(body, "<script>alert") {
		t.Fatal("raw script tag was rendered")
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "host&amp;log.log") {
		t.Fatal("inventory was not escaped")
	}
	if !strings.Contains(body, `data-overdue-after="180"`) {
		t.Fatal("overdue threshold missing")
	}
	if !strings.Contains(body, "4.0 KiB") || !strings.Contains(body, "Receiving") {
		t.Fatal("expected size and reporting label")
	}
	if !strings.Contains(body, "about every 1 hour") {
		t.Fatal("hourly note missing")
	}
	if !strings.Contains(body, `id="device-search"`) || !strings.Contains(body, `data-filter="healthy"`) {
		t.Fatal("search or card filters missing")
	}
	if !strings.Contains(body, `data-status="healthy"`) || !strings.Contains(body, "192.0.2.10") {
		t.Fatal("row search data missing")
	}
	if strings.Contains(body, `data-view="candidates"`) {
		t.Fatal("adoption cards shown without watched directories")
	}

	when = when.Add(time.Second)
	active = 40
	snap.WatchedDirectories = []string{"/var/log/remote"}
	snap.Candidates = []snapshot.Candidate{{
		LogPath:       "/var/log/host&log/<script>.log",
		SourceIP:      "192.0.2.50",
		SuggestedName: `<script>alert("adopt")</script>`,
		LastWrite:     &when,
		ActiveBytes:   active,
		Detail:        "Per-address log is not in the inventory.",
	}}
	snap.Ignored = []snapshot.Ignored{{LogPath: "/var/log/remote/noise&1.log"}}
	page, err = Page(snap)
	if err != nil {
		t.Fatal(err)
	}
	body = string(page)
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "host&log") {
		t.Fatal("candidate text was not escaped")
	}
	if !strings.Contains(body, "Adopt") || !strings.Contains(body, "Ignore") || !strings.Contains(body, "Stop ignoring") {
		t.Fatal("adoption actions missing")
	}
	if !strings.Contains(body, `data-view="candidates"`) || !strings.Contains(body, `data-view="ignored"`) {
		t.Fatal("adoption cards missing")
	}
	if !strings.Contains(body, `data-view-panel="candidates" hidden`) || !strings.Contains(body, `data-view-panel="ignored" hidden`) {
		t.Fatal("adoption panels should stay hidden until a card is selected")
	}
	if !strings.Contains(body, `class="card attention" data-view="candidates"`) {
		t.Fatal("untracked count should use the attention card when files are waiting")
	}
	if !strings.Contains(body, "host&amp;log") || !strings.Contains(body, "noise&amp;1.log") {
		t.Fatal("candidate paths were not escaped")
	}
	event, err := Event(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(event), "\n") {
		t.Fatal("sse payload must be one line")
	}
}
