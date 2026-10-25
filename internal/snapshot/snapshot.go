// Package snapshot is the point-in-time status of every expected device.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"syslog-gomonitor/internal/atomicfile"
	"syslog-gomonitor/internal/human"
	"syslog-gomonitor/internal/status"
)

// Snapshot is safe to publish. It contains metadata, not log contents.
type Snapshot struct {
	GeneratedAt           time.Time   `json:"generated_at"`
	MonitorOverdueSeconds int         `json:"monitor_overdue_seconds"`
	PollIntervalSeconds   int         `json:"poll_interval_seconds"`
	SizeMeasuredAt        *time.Time  `json:"size_measured_at"`
	SizeIntervalSeconds   int         `json:"size_interval_seconds"`
	Devices               []Device    `json:"devices"`
	Summary               Summary     `json:"summary"`
	WatchedDirectories    []string    `json:"watched_directories,omitempty"`
	DiscoverNote          string      `json:"discover_note,omitempty"`
	Candidates            []Candidate `json:"candidates,omitempty"`
	Ignored               []Ignored   `json:"ignored,omitempty"`
}

// Candidate is a file in a watched directory that is not an inventory device
// and has not been ignored. It is metadata only.
type Candidate struct {
	LogPath       string     `json:"log_path"`
	SourceIP      string     `json:"source_ip,omitempty"`
	SuggestedName string     `json:"suggested_name"`
	LastWrite     *time.Time `json:"last_write,omitempty"`
	ActiveBytes   int64      `json:"active_bytes"`
	Detail        string     `json:"detail"`
}

// Ignored is a source the operator chose not to track.
type Ignored struct {
	LogPath  string `json:"log_path"`
	SourceIP string `json:"source_ip,omitempty"`
}

// Device is one inventory row plus the latest activity and size measurement.
type Device struct {
	Name              string     `json:"name"`
	SourceIP          string     `json:"source_ip"`
	Protocol          string     `json:"protocol"`
	TCPPort           int        `json:"tcp_port,omitempty"`
	LogPath           string     `json:"log_path"`
	StaleAfterSeconds int        `json:"stale_after_seconds"`
	LogStatus         string     `json:"log_status"`
	LogDetail         string     `json:"log_detail"`
	LastWrite         *time.Time `json:"last_write"`
	AgeSeconds        *int       `json:"age_seconds,omitempty"`
	ActiveBytes       *int64     `json:"active_bytes"`
	Connection        string     `json:"connection"`
	ConnectionDetail  string     `json:"connection_detail"`
	Status            string     `json:"status"`
	SizeKnown         bool       `json:"size_known"`
	StoredBytes       int64      `json:"stored_bytes"`
	StoredFiles       int        `json:"stored_files"`
	StoredError       string     `json:"stored_error,omitempty"`
	SizeDetail        string     `json:"size_detail"`
}

// Summary counts outcomes. StoredBytes includes only devices whose size
// measurement succeeded.
type Summary struct {
	Total       int   `json:"total"`
	Healthy     int   `json:"healthy"`
	Attention   int   `json:"attention"`
	Unknown     int   `json:"unknown"`
	StoredBytes int64 `json:"stored_bytes"`
	SizeKnown   int   `json:"size_known"`
	SizeErrors  int   `json:"size_errors"`
}

// SizeState is the hourly measurement persisted between processes.
type SizeState struct {
	Version    int                   `json:"version"`
	MeasuredAt time.Time             `json:"measured_at"`
	Devices    map[string]StoredSize `json:"devices"`
}

// StoredSize is one device's on-disk total inside SizeState.
type StoredSize struct {
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	Detail string `json:"detail"`
	Error  string `json:"error,omitempty"`
}

const sizeStateVersion = 1

// Sort puts devices that need a person first, then unknown, then healthy.
func Sort(devices []Device) {
	sort.SliceStable(devices, func(i, j int) bool {
		ri, rj := rank(devices[i].Status), rank(devices[j].Status)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(devices[i].Name) < strings.ToLower(devices[j].Name)
	})
}

func rank(s string) int {
	switch s {
	case status.Attention:
		return 0
	case status.Unknown:
		return 1
	default:
		return 2
	}
}

// Summarize counts the rows currently in the snapshot.
func Summarize(devices []Device) Summary {
	var sum Summary
	sum.Total = len(devices)
	for _, device := range devices {
		switch device.Status {
		case status.Healthy:
			sum.Healthy++
		case status.Attention:
			sum.Attention++
		case status.Unknown:
			sum.Unknown++
		}
		if device.StoredError != "" {
			sum.SizeErrors++
			continue
		}
		if device.SizeKnown {
			sum.SizeKnown++
			sum.StoredBytes += device.StoredBytes
		}
	}
	return sum
}

// SaveSizeState writes the hourly measurement atomically.
func SaveSizeState(path string, state SizeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	state.Version = sizeStateVersion
	state.MeasuredAt = state.MeasuredAt.UTC()
	if state.Devices == nil {
		state.Devices = map[string]StoredSize{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0o640)
}

// LoadSizeState reads a snapshot written by SaveSizeState.
func LoadSizeState(path string) (SizeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SizeState{}, err
	}
	var state SizeState
	if err := json.Unmarshal(data, &state); err != nil {
		return SizeState{}, fmt.Errorf("size snapshot %s: %w", path, err)
	}
	if state.Version != sizeStateVersion {
		return SizeState{}, fmt.Errorf("size snapshot %s: unsupported version %d", path, state.Version)
	}
	if state.MeasuredAt.IsZero() {
		return SizeState{}, fmt.Errorf("size snapshot %s: missing measured_at", path)
	}
	if state.Devices == nil {
		return SizeState{}, errors.New("size snapshot has no devices object")
	}
	return state, nil
}

// FormatText renders a fixed-column report for the terminal and the journal.
func FormatText(snap Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Checked %s\n", snap.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	if snap.SizeMeasuredAt != nil {
		fmt.Fprintf(&b, "Stored size measured %s\n", snap.SizeMeasuredAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	} else {
		b.WriteString("Stored size not measured\n")
	}
	fmt.Fprintf(&b, "Devices %d   healthy %d   attention %d   unknown %d   stored %s\n\n",
		snap.Summary.Total, snap.Summary.Healthy, snap.Summary.Attention, snap.Summary.Unknown,
		storedSummary(snap.Summary))
	fmt.Fprintf(&b, "%-28s %-12s %-12s %-14s %12s %12s\n",
		"DEVICE", "OVERALL", "REPORTING", "CONNECTION", "ACTIVE", "STORED")
	for _, device := range snap.Devices {
		fmt.Fprintf(&b, "%-28s %-12s %-12s %-14s %12s %12s\n",
			trim(device.Name, 28),
			device.Status,
			device.LogStatus,
			device.Connection,
			activeText(device.ActiveBytes),
			storedText(device),
		)
	}
	return b.String()
}

func storedSummary(sum Summary) string {
	if sum.SizeKnown == 0 && sum.SizeErrors > 0 {
		return "unavailable"
	}
	if sum.SizeKnown == 0 {
		return "pending"
	}
	return human.Bytes(sum.StoredBytes)
}

func activeText(n *int64) string {
	if n == nil {
		return "—"
	}
	return human.Bytes(*n)
}

func storedText(device Device) string {
	if !device.SizeKnown {
		return "pending"
	}
	if device.StoredError != "" {
		return "unavailable"
	}
	return human.Bytes(device.StoredBytes)
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
