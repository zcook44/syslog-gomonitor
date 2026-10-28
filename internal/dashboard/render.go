// Package dashboard renders the status page. Inventory text is escaped.
// Log contents are not part of the page.
package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"strings"
	"time"

	"syslog-gomonitor/internal/human"
	"syslog-gomonitor/internal/snapshot"
)

//go:embed page.tmpl board.tmpl
var files embed.FS

var (
	pageTmpl  = template.Must(template.ParseFS(files, "page.tmpl"))
	boardTmpl = template.Must(template.ParseFS(files, "board.tmpl"))
)

var logLabels = map[string]string{
	"recent":  "Receiving",
	"stale":   "Stale",
	"missing": "Missing",
	"empty":   "Empty",
	"unknown": "Unknown",
}

var statusLabels = map[string]string{
	"healthy":   "Healthy",
	"attention": "Needs attention",
	"unknown":   "Unknown",
}

type pageData struct {
	OverdueAfter int
	GeneratedAt  string
	Board        template.HTML
}

type boardModel struct {
	Checked        string
	Total          int
	Healthy        int
	Attention      int
	Unknown        int
	Stored         string
	SizeNote       string
	Rows           []rowView
	ShowAdoption   bool
	CandidateAlert bool
	DiscoverNote   string
	Revision       string
	CandidateCount int
	IgnoredCount   int
	Candidates     []candidateView
	Ignored        []ignoredView
}

type candidateView struct {
	Path        string
	Dir         string
	Source      string
	Name        string
	Detail      string
	LastTime    string
	LastDay     string
	Active      string
	ActiveTitle string
	FileMeta    string
	IPLocked    bool
	Choose      bool
	Files       []logFileView
}

type logFileView struct {
	Path     string
	Name     string
	Meta     string
	Selected bool
}

type ignoredView struct {
	Path   string
	Source string
}

type rowView struct {
	Name         string
	Path         string
	Source       string
	Transport    string
	LogLabel     string
	LogDetail    string
	ConnDetail   string
	LastTime     string
	LastDay      string
	Active       string
	ActiveTitle  string
	Stored       string
	StoredDetail string
	StoredTitle  string
	StatusClass  string
	StatusLabel  string
	Search       string
}

// Page renders a complete HTML document for snap.
func Page(snap snapshot.Snapshot) ([]byte, error) {
	board, err := Board(snap)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = pageTmpl.Execute(&buf, pageData{
		OverdueAfter: snap.MonitorOverdueSeconds,
		GeneratedAt:  stamp(snap.GeneratedAt),
		Board:        template.HTML(board),
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Board renders the summary and table.
func Board(snap snapshot.Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	if err := boardTmpl.Execute(&buf, model(snap)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Event is one Server-Sent Event payload: the board HTML plus the check time.
func Event(snap snapshot.Snapshot) ([]byte, error) {
	board, err := Board(snap)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(struct {
		GeneratedAt time.Time `json:"generated_at"`
		HTML        string    `json:"html"`
	}{
		GeneratedAt: snap.GeneratedAt.UTC(),
		HTML:        string(board),
	}); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func model(snap snapshot.Snapshot) boardModel {
	rows := make([]rowView, 0, len(snap.Devices))
	for _, device := range snap.Devices {
		active, activeTitle := activeView(device.ActiveBytes)
		stored, storedDetail, storedTitle := storedView(device)
		lastTime, lastDay := clockParts(device.LastWrite)
		rows = append(rows, rowView{
			Name:         device.Name,
			Path:         device.LogPath,
			Source:       device.SourceIP,
			Transport:    transport(device),
			LogLabel:     label(logLabels, device.LogStatus),
			LogDetail:    device.LogDetail,
			ConnDetail:   device.ConnectionDetail,
			LastTime:     lastTime,
			LastDay:      lastDay,
			Active:       active,
			ActiveTitle:  activeTitle,
			Stored:       stored,
			StoredDetail: storedDetail,
			StoredTitle:  storedTitle,
			StatusClass:  device.Status,
			StatusLabel:  label(statusLabels, device.Status),
			Search: strings.ToLower(strings.Join([]string{
				device.Name,
				device.SourceIP,
				device.LogPath,
				transport(device),
				label(logLabels, device.LogStatus),
				label(statusLabels, device.Status),
			}, " ")),
		})
	}
	candidates := make([]candidateView, 0, len(snap.Candidates))
	var revision strings.Builder
	for _, item := range snap.Candidates {
		active, activeTitle := activeView(&item.ActiveBytes)
		lastTime, lastDay := clockParts(item.LastWrite)
		files := logFileViews(item)
		candidates = append(candidates, candidateView{
			Path:        item.LogPath,
			Dir:         path.Dir(item.LogPath),
			Source:      item.SourceIP,
			Name:        item.SuggestedName,
			Detail:      item.Detail,
			LastTime:    lastTime,
			LastDay:     lastDay,
			Active:      active,
			ActiveTitle: activeTitle,
			FileMeta:    fileMeta(lastTime, lastDay, active),
			IPLocked:    item.SourceIP != "",
			Choose:      len(files) > 1,
			Files:       files,
		})
		for _, file := range files {
			revision.WriteString(file.Path)
			revision.WriteByte('\n')
		}
	}
	ignored := make([]ignoredView, 0, len(snap.Ignored))
	for _, item := range snap.Ignored {
		ignored = append(ignored, ignoredView{Path: item.LogPath, Source: item.SourceIP})
		revision.WriteString("ignored ")
		revision.WriteString(item.LogPath)
		revision.WriteByte('\n')
	}
	watching := len(snap.WatchedDirectories) > 0
	return boardModel{
		Checked:        "Last checked " + clock(&snap.GeneratedAt),
		Total:          snap.Summary.Total,
		Healthy:        snap.Summary.Healthy,
		Attention:      snap.Summary.Attention,
		Unknown:        snap.Summary.Unknown,
		Stored:         storedCard(snap.Summary),
		SizeNote:       sizeNote(snap),
		Rows:           rows,
		ShowAdoption:   watching || len(candidates) > 0 || len(ignored) > 0 || snap.DiscoverNote != "",
		CandidateAlert: len(candidates) > 0 || snap.DiscoverNote != "",
		DiscoverNote:   snap.DiscoverNote,
		Revision:       revision.String(),
		CandidateCount: len(candidates),
		IgnoredCount:   len(ignored),
		Candidates:     candidates,
		Ignored:        ignored,
	}
}

func transport(device snapshot.Device) string {
	if device.Protocol == "tcp" {
		return fmt.Sprintf("TCP/%d", device.TCPPort)
	}
	return "UDP"
}

func label(table map[string]string, key string) string {
	if text, ok := table[key]; ok {
		return text
	}
	if key == "" {
		return "Unknown"
	}
	return key
}

func logFileViews(item snapshot.Candidate) []logFileView {
	files := item.LogFiles
	if len(files) == 0 && item.LogPath != "" {
		files = []snapshot.CandidateFile{{
			Path:        item.LogPath,
			Name:        path.Base(item.LogPath),
			LastWrite:   item.LastWrite,
			ActiveBytes: item.ActiveBytes,
		}}
	}
	views := make([]logFileView, len(files))
	for i, file := range files {
		active, _ := activeView(&file.ActiveBytes)
		lastTime, lastDay := clockParts(file.LastWrite)
		name := file.Name
		if name == "" {
			name = path.Base(file.Path)
		}
		views[i] = logFileView{
			Path:     file.Path,
			Name:     name,
			Meta:     fileMeta(lastTime, lastDay, active),
			Selected: file.Path == item.LogPath,
		}
	}
	return views
}

func fileMeta(lastTime, lastDay, active string) string {
	text := "Last write " + lastTime
	if lastDay != "" {
		text += " " + lastDay
	}
	return text + " · " + active
}

func activeView(n *int64) (string, string) {
	if n == nil {
		return "—", "Active file size is unknown"
	}
	return human.Bytes(*n), fmt.Sprintf("%d bytes", *n)
}

func storedView(device snapshot.Device) (string, string, string) {
	if !device.SizeKnown {
		detail := device.SizeDetail
		if detail == "" {
			detail = "Measured once an hour"
		}
		return "Pending", detail, ""
	}
	if device.StoredError != "" {
		detail := device.SizeDetail
		if detail == "" {
			detail = device.StoredError
		}
		return "Unavailable", detail, ""
	}
	return human.Bytes(device.StoredBytes), device.SizeDetail, fmt.Sprintf("%d bytes", device.StoredBytes)
}

func storedCard(sum snapshot.Summary) string {
	if sum.SizeKnown == 0 && sum.SizeErrors > 0 {
		return "Unavailable"
	}
	if sum.SizeKnown == 0 {
		return "—"
	}
	return human.Bytes(sum.StoredBytes)
}

func sizeNote(snap snapshot.Snapshot) string {
	interval := time.Duration(snap.SizeIntervalSeconds) * time.Second
	if snap.SizeMeasuredAt == nil || snap.SizeMeasuredAt.IsZero() {
		if interval <= 0 {
			return "Stored size has not been measured yet. Run the size command to record it."
		}
		return "Stored size has not been measured yet."
	}
	when := snap.SizeMeasuredAt.UTC().Format("2006-01-02 15:04:05 UTC")
	if interval <= 0 {
		return fmt.Sprintf("Stored size measured %s. In-process measurement is off; the size command refreshes this value.", when)
	}
	return fmt.Sprintf("Stored size measured %s and is scheduled about every %s. Active file size updates on each status check.", when, human.Interval(interval))
}

func clock(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "Not available"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func clockParts(t *time.Time) (string, string) {
	if t == nil || t.IsZero() {
		return "Not available", ""
	}
	utc := t.UTC()
	return utc.Format("15:04:05 UTC"), utc.Format("2006-01-02")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
