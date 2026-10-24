// Package human formats durations and byte sizes for the dashboard and reports.
package human

import (
	"fmt"
	"time"
)

// Duration formats a span the way an operator reads a log age: 12s, 4m, 1h 5m, 2d 3h.
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d / time.Second)
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%dd %dh", seconds/86400, (seconds%86400)/3600)
	}
}

// Interval formats a schedule in whole hours or minutes when it divides evenly.
func Interval(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d >= time.Hour && d%time.Hour == 0 {
		hours := int(d / time.Hour)
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if d >= time.Minute && d%time.Minute == 0 {
		minutes := int(d / time.Minute)
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return Duration(d)
}

// Bytes formats a stored or active size in binary units. Zero is "0 B".
func Bytes(n int64) string {
	if n < 0 {
		return "unknown"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(n)
	unit := "B"
	for _, name := range units {
		if value < 1024 {
			break
		}
		value /= 1024
		unit = name
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, unit)
	}
	return fmt.Sprintf("%.1f %s", value, unit)
}
