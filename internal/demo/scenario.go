// Package demo seeds an isolated set of device logs and two real loopback
// TCP sessions so the monitor can be viewed without a production syslog server.
package demo

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"syslog-gomonitor/internal/inventory"
)

const (
	KindHealthyUDP   = "healthy-udp"
	KindTCPReporting = "tcp-reporting"
	KindStale        = "stale"
	KindMissing      = "missing"
	KindEmptyArchive = "empty-archive"
	KindTCPDown      = "tcp-down"
	KindTCPSilent    = "tcp-silent"
	KindSymlink      = "symlink"
	KindFuture       = "future"
)

// Spec is one synthetic device and the status it is built to produce.
type Spec struct {
	Name       string
	SourceIP   string
	Protocol   string
	Kind       string
	WantLog    string
	WantConn   string
	WantStatus string
}

// Specs is the example fleet. 192.0.2.0/24 addresses are documentation
// addresses used as file labels. 127.0.0.2 and 127.0.0.4 open real sockets.
var Specs = []Spec{
	{"edge-router-01", "192.0.2.10", "udp", KindHealthyUDP, "recent", "udp", "healthy"},
	{"core-switch-01", "127.0.0.2", "tcp", KindTCPReporting, "recent", "connected", "healthy"},
	{"dist-switch-02", "192.0.2.21", "udp", KindStale, "stale", "udp", "attention"},
	{"access-switch-03", "192.0.2.22", "udp", KindMissing, "missing", "udp", "attention"},
	{"firewall-01", "192.0.2.30", "udp", KindEmptyArchive, "empty", "udp", "attention"},
	{"wan-router-02", "127.0.0.3", "tcp", KindTCPDown, "recent", "disconnected", "attention"},
	{"core-router-02", "127.0.0.4", "tcp", KindTCPSilent, "stale", "connected", "attention"},
	{"lab-switch-01", "192.0.2.40", "udp", KindSymlink, "unknown", "udp", "unknown"},
	{"dmz-firewall-01", "192.0.2.41", "udp", KindFuture, "unknown", "udp", "unknown"},
}

// Scenario owns disposable logs and the loopback sessions behind them.
type Scenario struct {
	Root       string
	ConfigPath string
	Inv        inventory.Inventory
	TCPPort    int

	mu      sync.Mutex
	ln      net.Listener
	clients map[string]net.Conn
	servers map[string]net.Conn
}

// Start creates the inventory, static files, live heartbeats, and TCP sessions.
func Start(dir string, tcpPort int) (*Scenario, error) {
	if dir == "" {
		return nil, fmt.Errorf("demo directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	sc := &Scenario{
		Root:    dir,
		clients: map[string]net.Conn{},
		servers: map[string]net.Conn{},
	}
	if err := sc.open(tcpPort); err != nil {
		sc.Close()
		return nil, err
	}
	return sc, nil
}

// Close releases the demo sockets.
func (s *Scenario) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ip, conn := range s.clients {
		_ = conn.Close()
		delete(s.clients, ip)
	}
	for ip, conn := range s.servers {
		_ = conn.Close()
		delete(s.servers, ip)
	}
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
}

// Refresh appends a new message for devices that are supposed to be reporting.
// Stale, empty, missing, silent, symlink, and future-dated files are left alone.
func (s *Scenario) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, spec := range Specs {
		switch spec.Kind {
		case KindHealthyUDP:
			if err := appendLine(s.path(spec.SourceIP), line(spec.Name, nextMessage(spec.Name))); err != nil {
				return err
			}
		case KindTCPDown:
			if err := appendLine(s.path(spec.SourceIP), line(spec.Name, nextMessage(spec.Name))); err != nil {
				return err
			}
		case KindTCPReporting:
			if err := s.sendTCP(spec.SourceIP, line(spec.Name, nextMessage(spec.Name))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Scenario) open(tcpPort int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tcpPort))
	if err != nil {
		return err
	}
	s.ln = ln
	s.TCPPort = ln.Addr().(*net.TCPAddr).Port
	if tl, ok := ln.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(3 * time.Second))
		defer tl.SetDeadline(time.Time{})
	}

	for _, ip := range []string{"127.0.0.2", "127.0.0.4"} {
		dialer := net.Dialer{
			LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)},
			Timeout:   3 * time.Second,
		}
		client, err := dialer.Dial("tcp", ln.Addr().String())
		if err != nil {
			return fmt.Errorf("demo client %s: %w", ip, err)
		}
		server, err := ln.Accept()
		if err != nil {
			_ = client.Close()
			return err
		}
		s.clients[ip] = client
		s.servers[ip] = server
	}

	if err := s.writeInventory(); err != nil {
		return err
	}
	inv, err := inventory.Load(s.ConfigPath)
	if err != nil {
		return err
	}
	s.Inv = inv
	if err := s.seedStatic(); err != nil {
		return err
	}
	if err := s.Refresh(); err != nil {
		return err
	}
	return s.seedSymlink()
}

func (s *Scenario) writeInventory() error {
	devices := make([]map[string]any, 0, len(Specs))
	for _, spec := range Specs {
		item := map[string]any{
			"name":      spec.Name,
			"source_ip": spec.SourceIP,
			"protocol":  spec.Protocol,
		}
		if spec.Protocol == "tcp" {
			item["tcp_port"] = s.TCPPort
		}
		if spec.Kind == KindEmptyArchive {
			item["log_path"] = filepath.Join(s.Root, "firewall", "firewall-01.log")
		}
		devices = append(devices, item)
	}
	doc := map[string]any{
		"stale_after_seconds":     120,
		"monitor_overdue_seconds": 45,
		"log_path_template":       filepath.Join(s.Root, "remote", "{source_ip}", "syslog.log"),
		"devices":                 devices,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	s.ConfigPath = filepath.Join(s.Root, "devices.json")
	return os.WriteFile(s.ConfigPath, append(data, '\n'), 0o644)
}

func (s *Scenario) seedStatic() error {
	edge := s.path("192.0.2.10")
	if err := writeExact(edge+".1", 4096, "edge-router-01 rotated syslog\n"); err != nil {
		return err
	}
	payload := bytes.Repeat([]byte("edge-router-01 historical interface counters\n"), 200)
	if err := writeGzip(filepath.Join(filepath.Dir(edge), "syslog.log-20261022.gz"), payload); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(edge), "notes.txt"), []byte("operator note, not a log\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(edge), "syslog.log.offset"), []byte("state"), 0o644); err != nil {
		return err
	}
	if err := os.Symlink(edge+".1", filepath.Join(filepath.Dir(edge), "syslog.log.3")); err != nil {
		return err
	}

	stale := s.path("192.0.2.21")
	if err := writeExact(stale, 512, line("dist-switch-02", "last spanning-tree change before the device went quiet")); err != nil {
		return err
	}
	if err := writeExact(stale+".1", 16384, "dist-switch-02 rotated syslog\n"); err != nil {
		return err
	}
	if err := os.Chtimes(stale, past(time.Hour), past(time.Hour)); err != nil {
		return err
	}

	fw := s.path("192.0.2.30")
	if err := writeExact(fw, 0, ""); err != nil {
		return err
	}
	if err := writeExact(fw+".1", 8192, "firewall-01 archived deny log\n"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fw), "other.log"), bytes.Repeat([]byte("x"), 777), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(fw+".offset", []byte("ignore"), 0o644); err != nil {
		return err
	}

	silent := s.path("127.0.0.4")
	if err := appendLine(silent, line("core-router-02", "BGP hold timer expired; session remains, logs stopped")); err != nil {
		return err
	}
	if err := os.Chtimes(silent, past(time.Hour), past(time.Hour)); err != nil {
		return err
	}

	future := s.path("192.0.2.41")
	if err := writeExact(future, 1024, line("dmz-firewall-01", "clock step left this file in the future")); err != nil {
		return err
	}
	when := time.Now().Add(time.Hour)
	return os.Chtimes(future, when, when)
}

func (s *Scenario) seedSymlink() error {
	target := s.path("192.0.2.10")
	link := s.path("192.0.2.40")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}

func (s *Scenario) sendTCP(ip, payload string) error {
	client := s.clients[ip]
	server := s.servers[ip]
	if client == nil || server == nil {
		return fmt.Errorf("demo TCP session %s is not open", ip)
	}
	deadline := time.Now().Add(2 * time.Second)
	_ = client.SetDeadline(deadline)
	if err := writeFull(client, []byte(payload)); err != nil {
		return err
	}
	_ = server.SetDeadline(deadline)
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(server, buf); err != nil {
		return err
	}
	return appendLine(s.path(ip), string(buf))
}

func (s *Scenario) path(ip string) string {
	for _, device := range s.Inv.Devices {
		if device.SourceIP == ip {
			return device.LogPath
		}
	}
	return ""
}

func line(host, msg string) string {
	ts := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf("<134>1 %s %s - - - - %s\n", ts, host, msg)
}

func nextMessage(name string) string {
	choices := messages[name]
	if len(choices) == 0 {
		return "syslog heartbeat"
	}
	n := int(seq.Add(1))
	return choices[n%len(choices)]
}

var seq counter

type counter struct {
	mu sync.Mutex
	n  uint64
}

func (c *counter) Add(n uint64) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += n
	return c.n
}

var messages = map[string][]string{
	"edge-router-01": {
		"%LINK-3-UPDOWN: Interface GigabitEthernet0/0, changed state to up",
		"%LINEPROTO-5-UPDOWN: Line protocol on Interface GigabitEthernet0/1, changed state to up",
		"%SYS-5-CONFIG_I: Configured from console by admin",
	},
	"core-switch-01": {
		"%LINK-3-UPDOWN: Interface GigabitEthernet1/0/24, changed state to up",
		"%SPANTREE-5-TOPOTRAP: Topology Change Trap for VLAN 20",
	},
	"wan-router-02": {
		"%BGP-5-ADJCHANGE: neighbor 192.0.2.50 Up",
		"%SYS-6-LOGGINGHOST_STARTSTOP: Logging to host started",
	},
}

func past(age time.Duration) time.Time {
	return time.Now().Add(-age)
}

func appendLine(path, text string) error {
	if path == "" {
		return fmt.Errorf("missing demo log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func writeExact(path string, n int, header string) error {
	if path == "" {
		return fmt.Errorf("missing demo log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := make([]byte, n)
	if len(header) > n {
		header = header[:n]
	}
	copy(body, header)
	for i := len(header); i < n; i++ {
		if i%80 == 79 {
			body[i] = '\n'
		} else {
			body[i] = 'x'
		}
	}
	return os.WriteFile(path, body, 0o644)
}

func writeGzip(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	_, werr := zw.Write(payload)
	cerr := zw.Close()
	ferr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	return ferr
}

func writeFull(conn net.Conn, payload []byte) error {
	for len(payload) > 0 {
		n, err := conn.Write(payload)
		payload = payload[n:]
		if err != nil {
			return err
		}
	}
	return nil
}
