// Package activity reports whether a device's current log file looks like it
// is still receiving syslog. It stats the path and does not read log contents.
// Symlinks are not followed, so one device's file cannot impersonate another.
package activity

import (
	"errors"
	"os"
	"time"

	"syslog-gomonitor/internal/human"
	"syslog-gomonitor/internal/status"
)

// FutureSkew is how far a modification time may sit ahead of the server clock
// before the check refuses to call the file recent.
const FutureSkew = 60 * time.Second

// Observation is the freshness of one device log.
type Observation struct {
	Status      string
	Detail      string
	LastWrite   time.Time
	ActiveBytes *int64
	Age         time.Duration
	AgeKnown    bool
}

// Inspect stats path at now. staleAfter is that device's silence limit.
func Inspect(path string, now time.Time, staleAfter time.Duration) Observation {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Observation{
				Status: status.LogMissing,
				Detail: "No log file has been created.",
			}
		}
		return Observation{
			Status: status.LogUnknown,
			Detail: "Cannot stat log file: " + statMessage(err),
		}
	}
	if !info.Mode().IsRegular() {
		return Observation{
			Status: status.LogUnknown,
			Detail: "Log path is not a regular file (symlinks are not followed).",
		}
	}

	size := info.Size()
	obs := Observation{
		LastWrite:   info.ModTime(),
		ActiveBytes: &size,
	}
	if size == 0 {
		obs.Status = status.LogEmpty
		obs.Detail = "Log file is empty."
		return obs
	}
	if info.ModTime().After(now.Add(FutureSkew)) {
		obs.Status = status.LogUnknown
		obs.Detail = "File modification time is in the future; check the server clock."
		return obs
	}

	age := now.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	obs.Age = age
	obs.AgeKnown = true
	if age <= staleAfter {
		obs.Status = status.LogRecent
		obs.Detail = "Last write " + human.Duration(age) + " ago."
		return obs
	}
	obs.Status = status.LogStale
	obs.Detail = "No write for " + human.Duration(age) + " (limit " + human.Duration(staleAfter) + ")."
	return obs
}

func statMessage(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return pathErr.Err.Error()
	}
	return err.Error()
}
