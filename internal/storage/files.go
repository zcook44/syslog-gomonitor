package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"syslog-gomonitor/internal/logname"
)

const maxDetailFiles = 200

// ListedFile is one name in a device's log directory. Contents are not read.
type ListedFile struct {
	Name    string
	Bytes   int64
	ModTime time.Time
	Kind    string
}

// Listing is the directory that holds a device's current log.
type Listing struct {
	Directory string
	Files     []ListedFile
	Note      string
}

// List reads one directory and classifies the names that belong to logPath.
// Symlinks are not followed. The active file is the one the status check stats.
func List(logPath string) Listing {
	dir := filepath.Dir(logPath)
	out := Listing{Directory: dir}
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			out.Note = "Log directory is missing."
			return out
		}
		out.Note = "Cannot read log directory: " + unwrap(err)
		return out
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		out.Note = "Log directory is not a real directory (symlinks are not followed)."
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		out.Note = "Cannot read log directory: " + unwrap(err)
		return out
	}

	base := filepath.Base(logPath)
	series, dated := logname.Parse(base)
	active := base
	if dated {
		active = newestDated(entries, series, base)
	}

	files := make([]ListedFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || name == "." || name == ".." {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		kind := kindOf(name, active, dated, series, base, fi)
		item := ListedFile{Name: name, Bytes: fi.Size(), ModTime: fi.ModTime(), Kind: kind}
		if kind == "symlink" || kind == "directory" {
			item.Bytes = -1
		}
		files = append(files, item)
	}
	sort.Slice(files, func(i, j int) bool {
		if (files[i].Kind == "active") != (files[j].Kind == "active") {
			return files[i].Kind == "active"
		}
		if !files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ModTime.After(files[j].ModTime)
		}
		return files[i].Name < files[j].Name
	})
	if len(files) > maxDetailFiles {
		out.Note = fmt.Sprintf("Showing %d of %d files.", maxDetailFiles, len(files))
		files = files[:maxDetailFiles]
	}
	out.Files = files
	return out
}

func newestDated(entries []os.DirEntry, series logname.Series, fallback string) string {
	best := ""
	var bestDate, bestWrite time.Time
	for _, entry := range entries {
		when, ok := series.Match(entry.Name())
		if !ok {
			continue
		}
		fi, err := entry.Info()
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			continue
		}
		if best == "" || when.After(bestDate) || (when.Equal(bestDate) && fi.ModTime().After(bestWrite)) {
			best = entry.Name()
			bestDate = when
			bestWrite = fi.ModTime()
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

func kindOf(name, active string, dated bool, series logname.Series, base string, fi os.FileInfo) string {
	if fi.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if fi.IsDir() {
		return "directory"
	}
	if name == active {
		return "active"
	}
	if dated && series.Owns(name) {
		if _, ok := series.Match(name); ok {
			return "series"
		}
		return "rotation"
	}
	if !dated && IsStoredLog(base, name) {
		return "rotation"
	}
	return "other"
}
