package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"syslog-gomonitor/internal/atomicfile"
	"syslog-gomonitor/internal/logname"
)

const (
	maxDeviceName = 128
	maxIgnored    = 500
	maxLogPath    = 4096
)

// UserError is a request the dashboard can show as written.
type UserError struct {
	Message string
	Ignored bool
}

func (e *UserError) Error() string { return e.Message }

// ErrIgnored marks a UserError for a source the operator has already hidden.
var ErrIgnored = errors.New("ignored")

func (e *UserError) Is(target error) bool {
	return e != nil && e.Ignored && target == ErrIgnored
}

func user(message string) error {
	return &UserError{Message: message}
}

func ignoredUser() error {
	return &UserError{
		Message: "That source is ignored. Stop ignoring it before adopting it.",
		Ignored: true,
	}
}

// Adoption is one device to append to the inventory.
type Adoption struct {
	Name     string
	SourceIP string
	Protocol string
	TCPPort  int
	LogPath  string
}

var rotationDot = regexp.MustCompile(`\.\d+$`)

// AuxiliaryLogName reports rotations and rsyslog state files. The active log
// itself is not auxiliary. A name that is only an IP address is not a rotation
// even though it ends in a dot and digits.
func AuxiliaryLogName(name string) bool {
	if name == "" {
		return true
	}
	if _, err := canonicalIP(name); err == nil {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".offset") || strings.HasSuffix(lower, ".state") {
		return true
	}
	return rotationDot.MatchString(name)
}

// AddressFile is the active log name inside a per-address directory.
func AddressFile(template string) string {
	file, _ := templateLayout(template)
	return file
}

func templateLayout(template string) (subdirFile, flatSuffix string) {
	subdirFile = "syslog.log"
	const marker = "{source_ip}"
	i := strings.Index(template, marker)
	if i < 0 {
		return subdirFile, ""
	}
	rest := template[i+len(marker):]
	if strings.HasPrefix(rest, "/") && strings.Count(rest, "/") == 1 {
		base := path.Base(rest)
		if base != "." && base != "/" && base != "" {
			return base, ""
		}
		return subdirFile, ""
	}
	if rest != "" && !strings.Contains(rest, "/") {
		return subdirFile, rest
	}
	return subdirFile, ""
}

// Recognize decides whether logPath is a source the dashboard may adopt or
// ignore. The returned address is set when the path itself names one. It does
// not stat the file.
func Recognize(inv Inventory, logPath string) (string, error) {
	if len(inv.LogDirectories) == 0 {
		return "", user("No log directories are configured, so new files cannot be adopted.")
	}
	if len(logPath) > maxLogPath {
		return "", user("The log path is too long.")
	}
	cleaned, err := cleanFilePath(logPath, "log path")
	if err != nil {
		return "", user("The log path must be an absolute file path.")
	}
	if !insideDirectory(inv.LogDirectories, cleaned) {
		return "", user("That path is outside the watched log directories.")
	}
	base := path.Base(cleaned)
	if strings.HasPrefix(base, ".") {
		return "", user("That file is not an untracked syslog source.")
	}
	if AuxiliaryLogName(base) {
		return "", user("Rotated and state files are not separate sources.")
	}
	parent := path.Dir(cleaned)
	_, flatSuffix := templateLayout(inv.LogPathTemplate)
	inRoot := false
	for _, dir := range inv.LogDirectories {
		if parent == dir {
			inRoot = true
			break
		}
	}
	var sourceIP string
	if inRoot {
		sourceIP = inferFlatIP(base, flatSuffix)
	} else {
		if strings.HasPrefix(path.Base(parent), ".") {
			return "", user("That file is not an untracked syslog source.")
		}
		grand := path.Dir(parent)
		rooted := false
		for _, dir := range inv.LogDirectories {
			if grand == dir {
				rooted = true
				break
			}
		}
		if !rooted || !strings.HasSuffix(base, ".log") {
			return "", user("That file is not an untracked syslog source.")
		}
		if ip, err := canonicalIP(path.Base(parent)); err == nil {
			sourceIP = ip
		}
	}
	for _, device := range inv.Devices {
		if device.LogPath == cleaned || sameDatedSeries(device.LogPath, cleaned) {
			return "", user("That source is already in the inventory.")
		}
		if sourceIP != "" && device.SourceIP == sourceIP {
			return "", user("That source address is already in the inventory.")
		}
	}
	for _, item := range inv.Ignored {
		if item.LogPath == cleaned || sameDatedSeries(item.LogPath, cleaned) || (sourceIP != "" && item.SourceIP != "" && item.SourceIP == sourceIP) {
			return "", ignoredUser()
		}
	}
	return sourceIP, nil
}

func inferFlatIP(name, flatSuffix string) string {
	if flatSuffix != "" && strings.HasSuffix(name, flatSuffix) && len(name) > len(flatSuffix) {
		if ip, err := canonicalIP(name[:len(name)-len(flatSuffix)]); err == nil {
			return ip
		}
	}
	if ip, err := canonicalIP(name); err == nil {
		return ip
	}
	return ""
}

// Adopt appends one device to the inventory file. The file is validated again
// before it is replaced. An ignored path is refused until it is unignored.
func Adopt(configPath string, in Adoption) error {
	return update(configPath, func(doc *fileDocument, inv Inventory) error {
		inferred, err := Recognize(inv, in.LogPath)
		if err != nil {
			return err
		}
		logPath, err := cleanFilePath(in.LogPath, "log path")
		if err != nil {
			return user("The log path must be an absolute file path.")
		}
		if err := requireRegularFile(logPath); err != nil {
			return err
		}
		sourceIP := inferred
		if sourceIP == "" {
			sourceIP, err = canonicalIP(in.SourceIP)
			if err != nil {
				return user("Enter the source IP address for this file.")
			}
			for _, device := range inv.Devices {
				if device.SourceIP == sourceIP {
					return user("That source address is already in the inventory.")
				}
			}
		}
		name := in.Name
		if err := validName(name); err != nil || len(name) > maxDeviceName {
			return user("Enter a device name without surrounding spaces or control characters.")
		}
		for _, device := range inv.Devices {
			if strings.EqualFold(device.Name, name) {
				return user("That device name is already in the inventory.")
			}
		}
		protocol := in.Protocol
		if protocol == "" {
			protocol = "udp"
		}
		if protocol != "udp" && protocol != "tcp" {
			return user("Protocol must be udp or tcp.")
		}
		var port *int
		if protocol == "tcp" {
			chosen := in.TCPPort
			if chosen == 0 {
				chosen = inv.TCPPort
			}
			if err := tcpPort(chosen, "tcp_port"); err != nil {
				return user("TCP port must be from 1 to 65535.")
			}
			if chosen != inv.TCPPort {
				port = &chosen
			}
		} else if in.TCPPort != 0 {
			return user("A TCP port is only used when the protocol is TCP.")
		}
		raw, err := json.Marshal(deviceWrite{
			Name:         name,
			SourceIP:     sourceIP,
			Protocol:     protocol,
			TCPPort:      port,
			LogDirectory: logDirectoryField(inv, sourceIP, logPath),
			LogPath:      logPathField(inv, sourceIP, logPath),
		})
		if err != nil {
			return err
		}
		doc.Devices = append(doc.Devices, raw)
		return nil
	})
}

// Ignore records a watched file so discovery stops offering it. Ignoring the
// same path again is a no-op.
func Ignore(configPath, logPath string) error {
	return update(configPath, func(doc *fileDocument, inv Inventory) error {
		inferred, err := Recognize(inv, logPath)
		if errors.Is(err, ErrIgnored) {
			return errNoChange
		}
		if err != nil {
			return err
		}
		cleaned, err := cleanFilePath(logPath, "log path")
		if err != nil {
			return user("The log path must be an absolute file path.")
		}
		if err := requireRegularFile(cleaned); err != nil {
			return err
		}
		if len(inv.Ignored) >= maxIgnored {
			return user("The ignored list is full. Remove some entries from the inventory first.")
		}
		raw, err := json.Marshal(ignoredWrite{LogPath: cleaned, SourceIP: inferred})
		if err != nil {
			return err
		}
		doc.Ignored = append(doc.Ignored, raw)
		return nil
	})
}

// Unignore removes an ignored path so the file can be adopted. The path is
// the one stored in the inventory.
func Unignore(configPath, logPath string) error {
	return update(configPath, func(doc *fileDocument, inv Inventory) error {
		cleaned, err := cleanFilePath(logPath, "log path")
		if err != nil {
			return user("The log path must be an absolute file path.")
		}
		next := make([]json.RawMessage, 0, len(doc.Ignored))
		removed := false
		for _, raw := range doc.Ignored {
			var item ignoredWrite
			if err := json.Unmarshal(raw, &item); err != nil {
				return fmt.Errorf("ignored entry: %w", err)
			}
			pathClean, err := cleanFilePath(item.LogPath, "ignored log path")
			if err != nil {
				return err
			}
			if pathClean == cleaned {
				removed = true
				continue
			}
			next = append(next, raw)
		}
		if !removed {
			return user("That file is not ignored.")
		}
		if len(next) == 0 {
			doc.Ignored = nil
		} else {
			doc.Ignored = next
		}
		return nil
	})
}

func requireRegularFile(logPath string) error {
	info, err := os.Lstat(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return user("That file is no longer there.")
		}
		return user("That file cannot be read.")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return user("Only a regular log file can be adopted or ignored. Symlinks are left alone.")
	}
	return nil
}

func sameDatedSeries(a, b string) bool {
	key := datedSeriesKey(a)
	return key != "" && key == datedSeriesKey(b)
}

func datedSeriesKey(logPath string) string {
	series, ok := logname.Parse(path.Base(logPath))
	if !ok {
		return ""
	}
	return path.Dir(logPath) + "\x00" + series.Layout + "\x00" + series.Prefix + "\x00" + series.Suffix
}

func logPathField(inv Inventory, sourceIP, logPath string) *string {
	if matchesDefault(inv, sourceIP, logPath) || logDirectoryField(inv, sourceIP, logPath) != nil {
		return nil
	}
	return &logPath
}

func logDirectoryField(inv Inventory, sourceIP, logPath string) *string {
	if matchesDefault(inv, sourceIP, logPath) {
		return nil
	}
	for _, dir := range inv.LogDirectories {
		cleaned, err := cleanFilePath(dir+"/"+sourceIP+"/syslog.log", "log_path")
		if err != nil {
			continue
		}
		if cleaned == logPath {
			matched := dir
			return &matched
		}
	}
	return nil
}

func matchesDefault(inv Inventory, sourceIP, logPath string) bool {
	var raw string
	switch {
	case inv.LogPathTemplate != "":
		raw = strings.ReplaceAll(inv.LogPathTemplate, "{source_ip}", sourceIP)
	case len(inv.LogDirectories) > 0:
		raw = inv.LogDirectories[0] + "/" + sourceIP + "/syslog.log"
	default:
		return false
	}
	cleaned, err := cleanFilePath(raw, "log_path")
	return err == nil && cleaned == logPath
}

type deviceWrite struct {
	Name         string  `json:"name"`
	SourceIP     string  `json:"source_ip"`
	Protocol     string  `json:"protocol"`
	TCPPort      *int    `json:"tcp_port,omitempty"`
	LogDirectory *string `json:"log_directory,omitempty"`
	LogPath      *string `json:"log_path,omitempty"`
}

type ignoredWrite struct {
	LogPath  string `json:"log_path"`
	SourceIP string `json:"source_ip,omitempty"`
}

type fileDocument struct {
	StaleAfterSeconds     *int              `json:"stale_after_seconds,omitempty"`
	MonitorOverdueSeconds *int              `json:"monitor_overdue_seconds,omitempty"`
	LogDirectories        []string          `json:"log_directories,omitempty"`
	LogPathTemplate       *string           `json:"log_path_template,omitempty"`
	TCPPort               *int              `json:"tcp_port,omitempty"`
	Devices               []json.RawMessage `json:"devices"`
	Ignored               []json.RawMessage `json:"ignored,omitempty"`
}

var errNoChange = errors.New("inventory unchanged")

func update(configPath string, mutate func(*fileDocument, Inventory) error) error {
	info, err := os.Lstat(configPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("inventory %s is not a regular file", configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	inv, err := Parse(data)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc fileDocument
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("inventory JSON: %w", err)
	}
	if err := mutate(&doc, inv); err != nil {
		if errors.Is(err, errNoChange) {
			return nil
		}
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if _, err := Parse(out); err != nil {
		return err
	}
	now, err := os.Lstat(configPath)
	if err != nil {
		return err
	}
	if !now.ModTime().Equal(info.ModTime()) || now.Size() != info.Size() {
		return user("The inventory changed while saving. Try again.")
	}
	return atomicfile.Write(configPath, out, info.Mode().Perm())
}

func resolveIgnored(raw []ignoredDoc, directories []string, devices []Device) ([]Ignored, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) > maxIgnored {
		return nil, fmt.Errorf("ignored list is longer than %d", maxIgnored)
	}
	paths := map[string]struct{}{}
	addresses := map[string]struct{}{}
	seriesSeen := map[string]struct{}{}
	devicePaths := map[string]struct{}{}
	deviceIPs := map[string]struct{}{}
	deviceSeries := map[string]struct{}{}
	for _, device := range devices {
		devicePaths[device.LogPath] = struct{}{}
		deviceIPs[device.SourceIP] = struct{}{}
		if key := datedSeriesKey(device.LogPath); key != "" {
			deviceSeries[key] = struct{}{}
		}
	}
	resolved := make([]Ignored, 0, len(raw))
	for i, item := range raw {
		label := fmt.Sprintf("ignored[%d]", i+1)
		logPath, err := cleanFilePath(item.LogPath, label+".log_path")
		if err != nil {
			return nil, err
		}
		if len(directories) > 0 && !insideDirectory(directories, logPath) {
			return nil, fmt.Errorf("%s log path %s is outside the configured log directories", label, logPath)
		}
		if _, dup := paths[logPath]; dup {
			return nil, fmt.Errorf("duplicate ignored path: %s", logPath)
		}
		if _, tracked := devicePaths[logPath]; tracked {
			return nil, fmt.Errorf("%s log path %s is already a device", label, logPath)
		}
		if key := datedSeriesKey(logPath); key != "" {
			if _, dup := seriesSeen[key]; dup {
				return nil, fmt.Errorf("duplicate ignored dated log: %s", logPath)
			}
			if _, tracked := deviceSeries[key]; tracked {
				return nil, fmt.Errorf("%s log path %s is already a device", label, logPath)
			}
			seriesSeen[key] = struct{}{}
		}
		var sourceIP string
		if item.SourceIP != nil {
			sourceIP, err = canonicalIP(*item.SourceIP)
			if err != nil {
				return nil, fmt.Errorf("%s.source_ip %s", label, err.Error())
			}
			if _, dup := addresses[sourceIP]; dup {
				return nil, fmt.Errorf("duplicate ignored source IP: %s", sourceIP)
			}
			if _, tracked := deviceIPs[sourceIP]; tracked {
				return nil, fmt.Errorf("%s source IP %s is already a device", label, sourceIP)
			}
			addresses[sourceIP] = struct{}{}
		}
		paths[logPath] = struct{}{}
		resolved = append(resolved, Ignored{LogPath: logPath, SourceIP: sourceIP})
	}
	return resolved, nil
}
