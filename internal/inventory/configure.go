package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"

	"syslog-gomonitor/internal/atomicfile"
)

// ApplyLogDirectories saves the directories this server watches.
// A missing inventory is created with example devices under the first directory.
// An existing inventory keeps its devices. A standard log_path_template is
// pointed at the first directory. The result is validated before it is written.
func ApplyLogDirectories(configPath string, dirs []string) error {
	cleaned, err := cleanDirectories(dirs)
	if err != nil {
		return err
	}
	template := standardTemplate(cleaned[0])

	var doc map[string]any
	existing := false
	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		existing = true
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return fmt.Errorf("inventory JSON: %w", err)
		}
		if current, ok := doc["log_path_template"].(string); ok && !isStandardTemplate(current) {
			template = current
		}
	} else {
		doc = map[string]any{
			"stale_after_seconds":     DefaultStaleAfterSeconds,
			"monitor_overdue_seconds": DefaultOverdueSeconds,
			"devices":                 starterDevices(),
		}
	}
	doc["log_directories"] = cleaned
	doc["log_path_template"] = template

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if _, err := Parse(out); err != nil {
		return err
	}

	mode := os.FileMode(0o600)
	if existing {
		info, err := os.Lstat(configPath)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			mode = info.Mode().Perm()
		}
	} else if err := os.MkdirAll(pathDir(configPath), 0o750); err != nil {
		return err
	}
	return atomicfile.Write(configPath, out, mode)
}

func standardTemplate(dir string) string {
	return dir + "/{source_ip}/syslog.log"
}

func isStandardTemplate(value string) bool {
	cleaned := path.Clean(value)
	return stringsCountSource(cleaned) && hasStandardSuffix(cleaned)
}

func stringsCountSource(value string) bool {
	return bytes.Count([]byte(value), []byte("{source_ip}")) == 1 &&
		bytes.Count(bytes.ReplaceAll([]byte(value), []byte("{source_ip}"), nil), []byte("{")) == 0 &&
		bytes.Count(bytes.ReplaceAll([]byte(value), []byte("{source_ip}"), nil), []byte("}")) == 0
}

func hasStandardSuffix(value string) bool {
	const suffix = "/{source_ip}/syslog.log"
	return len(value) > len(suffix) && value[len(value)-len(suffix):] == suffix
}

func starterDevices() []any {
	stale := 600
	return []any{
		map[string]any{"name": "edge-router-01", "source_ip": "192.0.2.10", "protocol": "udp"},
		map[string]any{
			"name": "core-switch-01", "source_ip": "192.0.2.11", "protocol": "tcp",
			"tcp_port": 514, "stale_after_seconds": stale,
		},
		map[string]any{"name": "firewall-01", "source_ip": "192.0.2.12", "protocol": "udp"},
	}
}

func pathDir(value string) string {
	dir := path.Dir(value)
	if dir == "." || dir == "" {
		return "."
	}
	return dir
}
