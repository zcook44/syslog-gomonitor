// Package discover lists syslog files that are in a watched directory and are
// not in the inventory. It does not read log contents or follow symlinks.
package discover

import (
	"context"
	"errors"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"syslog-gomonitor/internal/inventory"
)

const (
	maxShown    = 100
	maxExamined = 4096
)

// Candidate is one regular file the dashboard can adopt or ignore.
type Candidate struct {
	LogPath       string
	SourceIP      string
	SuggestedName string
	LastWrite     time.Time
	ActiveBytes   int64
	Detail        string
}

// Result is a discovery pass. Note is empty when nothing was skipped.
type Result struct {
	Candidates []Candidate
	Note       string
}

// Scan walks each configured log directory one level down. Address directories
// contribute their active log. Other regular files in the directory itself are
// listed too, except rotations and state files. Known and ignored sources are
// omitted.
func Scan(ctx context.Context, inv inventory.Inventory) Result {
	if len(inv.LogDirectories) == 0 {
		return Result{}
	}
	active := inventory.AddressFile(inv.LogPathTemplate)
	used := map[string]struct{}{}
	for _, device := range inv.Devices {
		used[strings.ToLower(device.Name)] = struct{}{}
	}
	var found []Candidate
	var notes []string
	for _, dir := range inv.LogDirectories {
		if err := ctx.Err(); err != nil {
			notes = append(notes, "Discovery stopped early.")
			break
		}
		info, err := os.Lstat(dir)
		if err != nil {
			notes = append(notes, "Cannot read "+dir+": "+unwrap(err)+".")
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			notes = append(notes, dir+" is not a real directory and was not searched.")
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			notes = append(notes, "Cannot read "+dir+": "+unwrap(err)+".")
			continue
		}
		dirs, files := splitEntries(entries)
		sort.SliceStable(dirs, func(i, j int) bool {
			return ipName(dirs[i].Name()) && !ipName(dirs[j].Name())
		})
		budget := maxExamined
		stopped := false
		for _, entry := range dirs {
			if budget == 0 {
				stopped = true
				break
			}
			budget--
			logPath := path.Join(dir, entry.Name(), active)
			found = appendCandidate(found, inv, used, logPath, false)
		}
		for _, entry := range files {
			if budget == 0 {
				stopped = true
				break
			}
			budget--
			logPath := path.Join(dir, entry.Name())
			found = appendCandidate(found, inv, used, logPath, true)
		}
		if stopped {
			notes = append(notes, "Stopped reading "+dir+" after "+strconv.Itoa(maxExamined)+" entries.")
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if (found[i].SourceIP == "") != (found[j].SourceIP == "") {
			return found[i].SourceIP != ""
		}
		return found[i].LogPath < found[j].LogPath
	})
	if len(found) > maxShown {
		notes = append(notes, "Showing "+strconv.Itoa(maxShown)+" of "+strconv.Itoa(len(found))+" untracked files. Ignore the ones listed, or fix the collector writing the rest.")
		found = found[:maxShown]
	}
	return Result{Candidates: found, Note: strings.Join(notes, " ")}
}

func appendCandidate(found []Candidate, inv inventory.Inventory, used map[string]struct{}, logPath string, inRoot bool) []Candidate {
	sourceIP, err := inventory.Recognize(inv, logPath)
	if err != nil {
		return found
	}
	info, err := os.Lstat(logPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return found
	}
	detail := "File in a watched directory is not in the inventory."
	if !inRoot {
		if sourceIP != "" {
			detail = "Per-address log is not in the inventory."
		} else {
			detail = "Directory in a watched tree is not in the inventory."
		}
	}
	return append(found, Candidate{
		LogPath:       logPath,
		SourceIP:      sourceIP,
		SuggestedName: suggest(inv, logPath, sourceIP, inRoot, used),
		LastWrite:     info.ModTime(),
		ActiveBytes:   info.Size(),
		Detail:        detail,
	})
}

func splitEntries(entries []os.DirEntry) (dirs, files []os.DirEntry) {
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			dirs = append(dirs, entry)
			continue
		}
		if entry.Type().IsRegular() {
			files = append(files, entry)
		}
	}
	return dirs, files
}

func suggest(inv inventory.Inventory, logPath, sourceIP string, inRoot bool, used map[string]struct{}) string {
	raw := sourceIP
	if raw == "" {
		if inRoot {
			raw = looseStem(path.Base(logPath))
		} else {
			raw = path.Base(path.Dir(logPath))
		}
	}
	raw = cleanSuggestion(raw)
	name := raw
	for n := 2; n < 1000; n++ {
		if _, taken := used[strings.ToLower(name)]; !taken && validSuggestion(name) {
			used[strings.ToLower(name)] = struct{}{}
			return name
		}
		name = raw + "-" + strconv.Itoa(n)
	}
	used["untracked"] = struct{}{}
	return "untracked"
}

func looseStem(name string) string {
	ext := path.Ext(name)
	if len(ext) > 1 && isLetters(ext[1:]) {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

func isLetters(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func cleanSuggestion(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	for _, r := range raw {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 64 {
			break
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return "untracked"
	}
	return cleaned
}

func validSuggestion(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len(name) <= 64
}

func ipName(name string) bool {
	if name == "" || strings.Contains(name, "%") {
		return false
	}
	// Address directories are listed first so a flood of other names does not
	// crowd a real device out of a capped scan.
	return net.ParseIP(name) != nil
}

func unwrap(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return pathErr.Err.Error()
	}
	return err.Error()
}
