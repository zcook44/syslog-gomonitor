// Package storage measures how much log data is currently on disk for each
// device. It sums the active file and rotated siblings in the same directory.
// The walk is meant to run hourly, not on the realtime status loop.
// File contents are never read. Symlinks are not followed.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"syslog-gomonitor/internal/inventory"
	"syslog-gomonitor/internal/logname"
)

// Measurement is the stored size of one device at a point in time.
type Measurement struct {
	Bytes           int64
	Files           int
	SkippedSymlinks int
	SkippedOther    int
	Detail          string
	Err             string
}

// IsStoredLog reports whether name belongs to the active log named base.
// Rotations use a dot or hyphen suffix (syslog.log.1, syslog.log-20261022.gz).
// rsyslog state files such as *.offset and *.state are not stored logs.
func IsStoredLog(base, name string) bool {
	if name == base {
		return true
	}
	var rest string
	switch {
	case strings.HasPrefix(name, base+"."):
		rest = name[len(base)+1:]
	case strings.HasPrefix(name, base+"-"):
		rest = name[len(base)+1:]
	default:
		return false
	}
	if rest == "" || strings.Contains(rest, "/") {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".offset") || strings.HasSuffix(lower, ".state") {
		return false
	}
	return true
}

// Scan groups devices by directory so each directory is listed once.
// Results are keyed by source IP.
func Scan(devices []inventory.Device) map[string]Measurement {
	groups := map[string][]inventory.Device{}
	order := make([]string, 0)
	for _, device := range devices {
		dir := filepath.Dir(device.LogPath)
		if _, ok := groups[dir]; !ok {
			order = append(order, dir)
		}
		groups[dir] = append(groups[dir], device)
	}

	out := make(map[string]Measurement, len(devices))
	for _, dir := range order {
		devs := groups[dir]
		info, err := os.Lstat(dir)
		if err != nil {
			detail := "Log directory is missing"
			errText := ""
			if !errors.Is(err, os.ErrNotExist) {
				errText = err.Error()
				detail = "Cannot read log directory: " + unwrap(err)
			}
			for _, device := range devs {
				out[device.SourceIP] = Measurement{Detail: detail, Err: errText}
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			for _, device := range devs {
				out[device.SourceIP] = Measurement{
					Err:    "log directory is a symlink or not a directory",
					Detail: "Log directory is not a real directory (symlinks are not followed).",
				}
			}
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			for _, device := range devs {
				out[device.SourceIP] = Measurement{
					Err:    err.Error(),
					Detail: "Cannot read log directory: " + unwrap(err),
				}
			}
			continue
		}

		infos := make(map[string]os.FileInfo, len(entries))
		infoErr := map[string]error{}
		for _, entry := range entries {
			fi, err := entry.Info()
			if err != nil {
				infoErr[entry.Name()] = err
				continue
			}
			infos[entry.Name()] = fi
		}

		for _, device := range devs {
			base := filepath.Base(device.LogPath)
			series, dated := logname.Parse(base)
			var measured Measurement
			for name, fi := range infos {
				if !storedName(dated, series, base, name) {
					continue
				}
				if fi.Mode()&os.ModeSymlink != 0 {
					measured.SkippedSymlinks++
					continue
				}
				if !fi.Mode().IsRegular() {
					measured.SkippedOther++
					continue
				}
				measured.Bytes += fi.Size()
				measured.Files++
			}
			for name, err := range infoErr {
				if storedName(dated, series, base, name) {
					measured.Err = unwrap(err)
				}
			}
			measured.Detail = describe(measured)
			out[device.SourceIP] = measured
		}
	}
	return out
}

func storedName(dated bool, series logname.Series, base, name string) bool {
	if dated {
		return series.Owns(name)
	}
	return IsStoredLog(base, name)
}

func describe(m Measurement) string {
	var parts []string
	switch m.Files {
	case 0:
		parts = append(parts, "No stored logs")
	case 1:
		parts = append(parts, "1 file on disk")
	default:
		parts = append(parts, fmt.Sprintf("%d files on disk", m.Files))
	}
	if m.SkippedSymlinks > 0 {
		if m.SkippedSymlinks == 1 {
			parts = append(parts, "skipped 1 symlink")
		} else {
			parts = append(parts, fmt.Sprintf("skipped %d symlinks", m.SkippedSymlinks))
		}
	}
	if m.SkippedOther > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d non-regular file(s)", m.SkippedOther))
	}
	if m.Err != "" {
		parts = append(parts, m.Err)
	}
	return strings.Join(parts, "; ")
}

func unwrap(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return pathErr.Err.Error()
	}
	return err.Error()
}
