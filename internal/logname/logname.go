// Package logname recognizes a calendar date embedded in a log filename.
// A name such as edge-router-01-20261028-syslog.log is one day of the series
// edge-router-01-{date}-syslog.log. The monitor follows the newest day.
package logname

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	layoutDay    = "20060102"
	layoutDashed = "2006-01-02"
)

var (
	dashedDate  = regexp.MustCompile(`(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])`)
	compactDate = regexp.MustCompile(`(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])`)
)

// Series is the fixed part of a dated log name. The date itself is not stored.
type Series struct {
	Prefix string
	Suffix string
	Layout string
}

// Parse reports the first valid calendar date in name. Names without a date
// are not a series. 2026-10-28 is recognized before 20261028.
func Parse(name string) (Series, bool) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return Series{}, false
	}
	if series, ok := find(name, dashedDate, layoutDashed); ok {
		return series, true
	}
	return find(name, compactDate, layoutDay)
}

func find(name string, re *regexp.Regexp, layout string) (Series, bool) {
	for _, span := range re.FindAllStringIndex(name, -1) {
		token := name[span[0]:span[1]]
		if _, err := time.Parse(layout, token); err != nil {
			continue
		}
		return Series{Prefix: name[:span[0]], Suffix: name[span[1]:], Layout: layout}, true
	}
	return Series{}, false
}

// Label is the convention shown when choosing which file to monitor.
func (s Series) Label() string {
	return s.Prefix + "{date}" + s.Suffix
}

// Stem is the device portion of the name, used as a suggested inventory name.
func (s Series) Stem() string {
	stem := strings.TrimRight(s.Prefix, "-_. ")
	if stem == "" {
		return "untracked"
	}
	return stem
}

// Match reports the date when name is one active file in the series.
// Rotations such as name.1 or name.gz do not match.
func (s Series) Match(name string) (time.Time, bool) {
	when, tail, ok := s.split(name)
	if !ok || tail != "" {
		return time.Time{}, false
	}
	return when, true
}

// Owns reports whether name is an active file in the series or a rotation of one.
// rsyslog state files (.offset, .state) are not owned.
func (s Series) Owns(name string) bool {
	_, tail, ok := s.split(name)
	if !ok {
		return false
	}
	if tail == "" {
		return true
	}
	if tail[0] != '.' && tail[0] != '-' {
		return false
	}
	lower := strings.ToLower(name)
	return !strings.HasSuffix(lower, ".offset") && !strings.HasSuffix(lower, ".state")
}

func (s Series) split(name string) (time.Time, string, bool) {
	if s.Layout == "" || !strings.HasPrefix(name, s.Prefix) {
		return time.Time{}, "", false
	}
	rest := name[len(s.Prefix):]
	width := 8
	if s.Layout == layoutDashed {
		width = 10
	}
	if len(rest) < width+len(s.Suffix) || !strings.HasPrefix(rest[width:], s.Suffix) {
		return time.Time{}, "", false
	}
	when, err := time.Parse(s.Layout, rest[:width])
	if err != nil {
		return time.Time{}, "", false
	}
	return when, rest[width+len(s.Suffix):], true
}

// Active returns the newest regular file in the same directory that belongs to
// the same dated series as configured. A name without a date is returned as-is.
// When no matching file is present, configured is returned so the caller can
// report it missing. Symlinks are not followed.
func Active(configured string) string {
	series, ok := Parse(filepath.Base(configured))
	if !ok {
		return configured
	}
	dir := filepath.Dir(configured)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return configured
	}
	best := ""
	var bestDate, bestWrite time.Time
	for _, entry := range entries {
		when, ok := series.Match(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if best == "" || when.After(bestDate) || (when.Equal(bestDate) && info.ModTime().After(bestWrite)) {
			best = filepath.Join(dir, entry.Name())
			bestDate = when
			bestWrite = info.ModTime()
		}
	}
	if best == "" {
		return configured
	}
	return best
}
