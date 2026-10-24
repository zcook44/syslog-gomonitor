// Package inventory loads the JSON list of syslog sources this server expects.
// Paths are resolved and checked before any measurement runs, so a bad edit
// cannot be mistaken for a device outage.
package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
)

const (
	DefaultStaleAfterSeconds = 900
	DefaultOverdueSeconds    = 180
	DefaultTCPPort           = 514
	maxInventoryBytes        = 4 << 20
)

// Device is one expected syslog source after path and address resolution.
type Device struct {
	Name              string
	SourceIP          string
	LogPath           string
	Protocol          string
	TCPPort           int
	StaleAfterSeconds int
}

// Inventory is the validated device list for this server.
type Inventory struct {
	Devices               []Device
	LogDirectories        []string
	MonitorOverdueSeconds int
}

// NeedsTCP reports whether any device asks for an established-session check.
func (inv Inventory) NeedsTCP() bool {
	for _, device := range inv.Devices {
		if device.Protocol == "tcp" {
			return true
		}
	}
	return false
}

type document struct {
	StaleAfterSeconds     *int        `json:"stale_after_seconds"`
	MonitorOverdueSeconds *int        `json:"monitor_overdue_seconds"`
	LogDirectories        []string    `json:"log_directories"`
	LogPathTemplate       *string     `json:"log_path_template"`
	TCPPort               *int        `json:"tcp_port"`
	Devices               []deviceDoc `json:"devices"`
}

type deviceDoc struct {
	Name              string  `json:"name"`
	SourceIP          string  `json:"source_ip"`
	LogDirectory      *string `json:"log_directory"`
	LogPath           *string `json:"log_path"`
	Protocol          *string `json:"protocol"`
	TCPPort           *int    `json:"tcp_port"`
	StaleAfterSeconds *int    `json:"stale_after_seconds"`
}

// Load reads and validates an inventory file.
func Load(configPath string) (Inventory, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Inventory{}, fmt.Errorf("cannot read inventory %s: %w", configPath, err)
	}
	inv, err := Parse(data)
	if err != nil {
		return Inventory{}, err
	}
	return inv, nil
}

// Parse validates inventory JSON.
func Parse(data []byte) (Inventory, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Inventory{}, errors.New("inventory is empty")
	}
	if len(data) > maxInventoryBytes {
		return Inventory{}, errors.New("inventory is larger than 4MiB")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Inventory{}, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return Inventory{}, fmt.Errorf("inventory JSON: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return Inventory{}, errors.New("inventory must be a single JSON document")
	}
	return doc.resolve()
}

func (doc document) resolve() (Inventory, error) {
	staleAfter := DefaultStaleAfterSeconds
	if doc.StaleAfterSeconds != nil {
		staleAfter = *doc.StaleAfterSeconds
	}
	if err := positive(staleAfter, "stale_after_seconds"); err != nil {
		return Inventory{}, err
	}

	overdue := DefaultOverdueSeconds
	if doc.MonitorOverdueSeconds != nil {
		overdue = *doc.MonitorOverdueSeconds
	}
	if err := positive(overdue, "monitor_overdue_seconds"); err != nil {
		return Inventory{}, err
	}

	defaultPort := DefaultTCPPort
	if doc.TCPPort != nil {
		defaultPort = *doc.TCPPort
	}
	if err := tcpPort(defaultPort, "tcp_port"); err != nil {
		return Inventory{}, err
	}

	directories, err := cleanDirectories(doc.LogDirectories)
	if err != nil {
		return Inventory{}, err
	}

	var template string
	if doc.LogPathTemplate != nil {
		template, err = cleanFilePath(*doc.LogPathTemplate, "log_path_template")
		if err != nil {
			return Inventory{}, err
		}
		if err := validateTemplate(template); err != nil {
			return Inventory{}, err
		}
	}

	if len(doc.Devices) == 0 {
		return Inventory{}, errors.New("devices must be a nonempty list")
	}

	names := map[string]struct{}{}
	addresses := map[string]struct{}{}
	paths := map[string]struct{}{}
	resolved := make([]Device, 0, len(doc.Devices))

	for i, raw := range doc.Devices {
		label := fmt.Sprintf("devices[%d]", i+1)
		name := raw.Name
		if err := validName(name); err != nil {
			return Inventory{}, fmt.Errorf("%s.name %s", label, err.Error())
		}
		folded := strings.ToLower(name)
		if _, dup := names[folded]; dup {
			return Inventory{}, fmt.Errorf("duplicate device name: %s", name)
		}
		names[folded] = struct{}{}

		sourceIP, err := canonicalIP(raw.SourceIP)
		if err != nil {
			return Inventory{}, fmt.Errorf("%s.source_ip %s", label, err.Error())
		}
		if _, dup := addresses[sourceIP]; dup {
			return Inventory{}, fmt.Errorf("duplicate source IP: %s", sourceIP)
		}
		addresses[sourceIP] = struct{}{}

		protocol := "udp"
		if raw.Protocol != nil {
			protocol = *raw.Protocol
		}
		if protocol != "udp" && protocol != "tcp" {
			return Inventory{}, fmt.Errorf("%s.protocol must be udp or tcp", label)
		}
		if protocol == "udp" && raw.TCPPort != nil {
			return Inventory{}, fmt.Errorf("%s.tcp_port is only meaningful for TCP", label)
		}

		port := 0
		if protocol == "tcp" {
			port = defaultPort
			if raw.TCPPort != nil {
				port = *raw.TCPPort
			}
			if err := tcpPort(port, label+".tcp_port"); err != nil {
				return Inventory{}, err
			}
		}

		threshold := staleAfter
		if raw.StaleAfterSeconds != nil {
			threshold = *raw.StaleAfterSeconds
		}
		if err := positive(threshold, label+".stale_after_seconds"); err != nil {
			return Inventory{}, err
		}

		var logPath string
		switch {
		case raw.LogPath != nil && raw.LogDirectory != nil:
			return Inventory{}, fmt.Errorf("%s cannot set both log_path and log_directory", label)
		case raw.LogPath != nil:
			logPath, err = cleanFilePath(*raw.LogPath, label+".log_path")
			if err != nil {
				return Inventory{}, err
			}
		case raw.LogDirectory != nil:
			dir, err := directoryChoice(*raw.LogDirectory, directories, label+".log_directory")
			if err != nil {
				return Inventory{}, err
			}
			logPath, err = cleanFilePath(dir+"/"+sourceIP+"/syslog.log", label+".log_path")
			if err != nil {
				return Inventory{}, err
			}
		case template != "":
			logPath, err = cleanFilePath(strings.ReplaceAll(template, "{source_ip}", sourceIP), label+".log_path")
			if err != nil {
				return Inventory{}, err
			}
		case len(directories) > 0:
			logPath, err = cleanFilePath(directories[0]+"/"+sourceIP+"/syslog.log", label+".log_path")
			if err != nil {
				return Inventory{}, err
			}
		default:
			return Inventory{}, fmt.Errorf("%s needs log_path, log_directory, or a log_path_template", label)
		}
		if len(directories) > 0 && !insideDirectory(directories, logPath) {
			return Inventory{}, fmt.Errorf("%s log path %s is outside the configured log directories", label, logPath)
		}
		if _, dup := paths[logPath]; dup {
			return Inventory{}, fmt.Errorf("log paths must be per-device; duplicate: %s", logPath)
		}
		paths[logPath] = struct{}{}

		resolved = append(resolved, Device{
			Name:              name,
			SourceIP:          sourceIP,
			LogPath:           logPath,
			Protocol:          protocol,
			TCPPort:           port,
			StaleAfterSeconds: threshold,
		})
	}

	return Inventory{
		Devices:               resolved,
		LogDirectories:        directories,
		MonitorOverdueSeconds: overdue,
	}, nil
}

func validateTemplate(template string) error {
	if strings.Count(template, "{source_ip}") != 1 {
		return errors.New("log_path_template must contain only one {source_ip} placeholder")
	}
	stripped := strings.ReplaceAll(template, "{source_ip}", "")
	if strings.ContainsAny(stripped, "{}") {
		return errors.New("log_path_template must contain only one {source_ip} placeholder")
	}
	return nil
}

func positive(value int, setting string) error {
	if value < 1 {
		return fmt.Errorf("%s must be a positive integer", setting)
	}
	return nil
}

func tcpPort(value int, setting string) error {
	if value < 1 || value > 65535 {
		return fmt.Errorf("%s must be a TCP port from 1 to 65535", setting)
	}
	return nil
}

func validName(name string) error {
	if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
		return errors.New("must be nonempty and contain no surrounding space")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return errors.New("must not contain control characters")
		}
	}
	return nil
}

func canonicalIP(value string) (string, error) {
	if value == "" || strings.Contains(value, "%") {
		return "", errors.New("must be a literal IPv4 or IPv6 address")
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return "", errors.New("must be a literal IPv4 or IPv6 address")
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String(), nil
	}
	return ip.String(), nil
}

func cleanDirectories(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 {
		return nil, errors.New("log_directories must list at least one directory")
	}
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(values))
	for i, value := range values {
		dir, err := cleanDirPath(value, fmt.Sprintf("log_directories[%d]", i+1))
		if err != nil {
			return nil, err
		}
		if _, dup := seen[dir]; dup {
			return nil, fmt.Errorf("duplicate log directory: %s", dir)
		}
		seen[dir] = struct{}{}
		cleaned = append(cleaned, dir)
	}
	return cleaned, nil
}

func cleanDirPath(value, setting string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%s must be an absolute directory", setting)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("%s contains a control character", setting)
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s must not contain '..' components", setting)
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%s must be a specific directory", setting)
	}
	return cleaned, nil
}

func directoryChoice(value string, directories []string, setting string) (string, error) {
	dir, err := cleanDirPath(value, setting)
	if err != nil {
		return "", err
	}
	for _, allowed := range directories {
		if dir == allowed {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%s must be one of the configured log directories", setting)
}

func insideDirectory(directories []string, file string) bool {
	for _, dir := range directories {
		if file == dir || strings.HasPrefix(file, dir+"/") {
			return file != dir
		}
	}
	return false
}

func cleanFilePath(value, setting string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%s must be an absolute file path", setting)
	}
	if strings.HasSuffix(value, "/") {
		return "", fmt.Errorf("%s must name a file", setting)
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("%s contains a control character", setting)
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s must name a file without '..' components", setting)
		}
	}
	cleaned := path.Clean(value)
	if !strings.HasPrefix(cleaned, "/") || path.Base(cleaned) == "." || path.Base(cleaned) == "/" {
		return "", fmt.Errorf("%s must name a file", setting)
	}
	return cleaned, nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSON(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("inventory must be a single JSON document")
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("inventory JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("inventory JSON: %w", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("inventory JSON: object key must be a string")
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("duplicate JSON key: %s", key)
			}
			seen[key] = struct{}{}
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return fmt.Errorf("inventory JSON: %w", err)
		}
		if end != json.Delim('}') {
			return errors.New("inventory JSON: object was not closed")
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return fmt.Errorf("inventory JSON: %w", err)
		}
		if end != json.Delim(']') {
			return errors.New("inventory JSON: array was not closed")
		}
	default:
		return errors.New("inventory JSON: unexpected delimiter")
	}
	return nil
}
