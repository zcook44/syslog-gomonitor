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
	"syslog-gomonitor/internal/logname"
)

const (
	maxShown    = 100
	maxExamined = 4096
)

// LogFile is one log convention inside a candidate directory. A dated name is
// collapsed to its pattern, and Path is the newest file of that pattern.
type LogFile struct {
	Path        string
	Name        string
	LastWrite   time.Time
	ActiveBytes int64
}

// Candidate is one child directory, or one loose file, the dashboard can adopt
// or ignore. LogFiles lists every *.log convention in that directory.
type Candidate struct {
	LogPath       string
	LogFiles      []LogFile
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

// Scan walks each configured log directory one level down. Each regular
// *.log file inside a child directory is its own candidate. Other regular
// files in the directory itself are listed too, except rotations and state
// files. Known and ignored sources are omitted.
func Scan(ctx context.Context, inv inventory.Inventory) Result {
	if len(inv.LogDirectories) == 0 {
		return Result{}
	}
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
			child := path.Join(dir, entry.Name())
			childEntries, err := os.ReadDir(child)
			if err != nil {
				budget--
				notes = append(notes, "Cannot read "+child+": "+unwrap(err)+".")
				continue
			}
			var logs []LogFile
			var sourceIP string
			for _, childEntry := range childEntries {
				if budget == 0 {
					stopped = true
					break
				}
				budget--
				name := childEntry.Name()
				if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".log") {
					continue
				}
				if childEntry.Type()&os.ModeSymlink != 0 || !childEntry.Type().IsRegular() {
					continue
				}
				logPath := path.Join(child, name)
				file, ip, ok := candidateFile(inv, logPath)
				if !ok {
					continue
				}
				logs = append(logs, file)
				if sourceIP == "" {
					sourceIP = ip
				}
			}
			if len(logs) == 0 || directoryTracked(inv, child) {
				continue
			}
			found = append(found, childCandidate(inv, used, sourceIP, logs))
		}
		var loose []LogFile
		looseIP := map[string]string{}
		for _, entry := range files {
			if budget == 0 {
				stopped = true
				break
			}
			budget--
			logPath := path.Join(dir, entry.Name())
			file, ip, ok := candidateFile(inv, logPath)
			if !ok {
				continue
			}
			loose = append(loose, file)
			looseIP[file.Path] = ip
		}
		found = append(found, looseCandidates(inv, used, loose, looseIP)...)
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
		notes = append(notes, "Showing "+strconv.Itoa(maxShown)+" of "+strconv.Itoa(len(found))+" untracked sources. Ignore the ones listed, or fix the collector writing the rest.")
		found = found[:maxShown]
	}
	return Result{Candidates: found, Note: strings.Join(notes, " ")}
}

func candidateFile(inv inventory.Inventory, logPath string) (LogFile, string, bool) {
	sourceIP, err := inventory.Recognize(inv, logPath)
	if err != nil {
		return LogFile{}, "", false
	}
	info, err := os.Lstat(logPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LogFile{}, "", false
	}
	return LogFile{
		Path:        logPath,
		Name:        path.Base(logPath),
		LastWrite:   info.ModTime(),
		ActiveBytes: info.Size(),
	}, sourceIP, true
}

func childCandidate(inv inventory.Inventory, used map[string]struct{}, sourceIP string, logs []LogFile) Candidate {
	logs = groupConventions(logs)
	chosen := logs[0]
	for _, file := range logs {
		if file.Name == "syslog.log" || strings.HasSuffix(file.Name, "-syslog.log") {
			chosen = file
			break
		}
	}
	detail := childDetail(sourceIP, len(logs))
	if strings.Contains(chosen.Name, "{date}") && len(logs) == 1 {
		detail = "Monitors the newest " + chosen.Name + " in this directory."
	}
	return Candidate{
		LogPath:       chosen.Path,
		LogFiles:      logs,
		SourceIP:      sourceIP,
		SuggestedName: suggest(inv, chosen.Path, sourceIP, false, used),
		LastWrite:     chosen.LastWrite,
		ActiveBytes:   chosen.ActiveBytes,
		Detail:        detail,
	}
}

func looseCandidates(inv inventory.Inventory, used map[string]struct{}, logs []LogFile, ips map[string]string) []Candidate {
	if len(logs) == 0 {
		return nil
	}
	var found []Candidate
	for _, file := range groupConventions(logs) {
		detail := "File in a watched directory is not in the inventory."
		suggestPath := file.Path
		if series, ok := logname.Parse(path.Base(file.Path)); ok {
			detail = "Monitors the newest " + file.Name + " in this directory."
			suggestPath = path.Join(path.Dir(file.Path), series.Stem()+".log")
		}
		found = append(found, Candidate{
			LogPath:       file.Path,
			LogFiles:      []LogFile{file},
			SourceIP:      ips[file.Path],
			SuggestedName: suggest(inv, suggestPath, ips[file.Path], true, used),
			LastWrite:     file.LastWrite,
			ActiveBytes:   file.ActiveBytes,
			Detail:        detail,
		})
	}
	return found
}

func groupConventions(logs []LogFile) []LogFile {
	type bucket struct {
		best LogFile
		when time.Time
		set  bool
	}
	order := make([]string, 0)
	groups := map[string]*bucket{}
	for _, file := range logs {
		key := "name\x00" + file.Name
		label := file.Name
		var when time.Time
		if series, ok := logname.Parse(file.Name); ok {
			key = series.Layout + "\x00" + series.Prefix + "\x00" + series.Suffix
			label = series.Label()
			when, _ = series.Match(file.Name)
		}
		b := groups[key]
		if b == nil {
			b = &bucket{}
			groups[key] = b
			order = append(order, key)
		}
		if !b.set || when.After(b.when) || (when.Equal(b.when) && file.LastWrite.After(b.best.LastWrite)) {
			file.Name = label
			b.best = file
			b.when = when
			b.set = true
		}
	}
	out := make([]LogFile, 0, len(order))
	for _, key := range order {
		out = append(out, groups[key].best)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func directoryTracked(inv inventory.Inventory, child string) bool {
	for _, device := range inv.Devices {
		if path.Dir(device.LogPath) == child {
			return true
		}
	}
	return false
}

func childDetail(sourceIP string, files int) string {
	switch {
	case files > 1 && sourceIP != "":
		return "Per-address directory has more than one log file. Choose which one to monitor."
	case files > 1:
		return "This directory has more than one log file. Choose which one to monitor."
	case sourceIP != "":
		return "Per-address log is not in the inventory."
	default:
		return "Directory in a watched tree is not in the inventory."
	}
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
