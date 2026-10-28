// Package cli is the syslog-gomonitor command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"syslog-gomonitor/internal/atomicfile"
	"syslog-gomonitor/internal/dashboard"
	"syslog-gomonitor/internal/demo"
	"syslog-gomonitor/internal/engine"
	"syslog-gomonitor/internal/inventory"
	"syslog-gomonitor/internal/server"
	"syslog-gomonitor/internal/snapshot"
)

const (
	defaultConfig = "/etc/syslog-gomonitor/devices.json"
	defaultState  = "/var/lib/syslog-gomonitor/sizes.json"
)

// Run executes one invocation. args does not include the program name.
func Run(args []string) int {
	if err := dispatch(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(os.Stderr, "syslog-gomonitor: %v\n", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func dispatch(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(os.Stdout)
		if len(args) == 0 {
			return errUsage
		}
		return nil
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "size":
		return measure(args[1:])
	case "check":
		return check(args[1:])
	case "validate":
		return validate(args[1:])
	case "configure":
		return configure(args[1:])
	case "validate-tls":
		return validateTLS(args[1:])
	case "demo":
		return runDemo(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "syslog-gomonitor: unknown command %q\n", args[0])
		usage(os.Stderr)
		return errUsage
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `syslog-gomonitor — realtime status for devices that report to syslog

Usage:
  syslog-gomonitor serve       Run the dashboard and activity checks
  syslog-gomonitor size        Measure stored log size once
  syslog-gomonitor check       Print one JSON status snapshot
  syslog-gomonitor validate    Check the inventory file
  syslog-gomonitor configure   Save the syslog directories to watch
  syslog-gomonitor validate-tls  Check the dashboard TLS certificate chain
  syslog-gomonitor demo        Example devices and a local dashboard

Run syslog-gomonitor <command> -h for flags.
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "JSON device inventory")
	state := fs.String("state", defaultState, "hourly stored-size snapshot")
	listen := fs.String("listen", "127.0.0.1:8080", "dashboard address (host:port)")
	tlsCert := fs.String("tls-cert", "", "PEM certificate chain: server certificate, then intermediates")
	tlsKey := fs.String("tls-key", "", "PEM private key for the server certificate")
	pollStr := fs.String("poll", "5s", "how often to recheck log activity and TCP sessions")
	sizeStr := fs.String("size-interval", "1h", "how often to measure stored log size; 0s leaves that to the size command")
	discoverStr := fs.String("discover-interval", "30s", "how often to look for untracked files in log directories; 0s checks once at startup")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	poll, err := parseDuration("poll", *pollStr, 200*time.Millisecond)
	if err != nil {
		return err
	}
	sizeEvery, err := parseDuration("size-interval", *sizeStr, 0)
	if err != nil {
		return err
	}
	discoverEvery, err := parseDuration("discover-interval", *discoverStr, 0)
	if err != nil {
		return err
	}
	if discoverEvery > 0 && discoverEvery < time.Second {
		return fmt.Errorf("--discover-interval must be 0s or at least 1s")
	}
	if _, _, err := net.SplitHostPort(*listen); err != nil {
		return fmt.Errorf("--listen must be host:port")
	}
	inv, err := inventory.Load(*config)
	if err != nil {
		return err
	}
	eng := engine.New(inv, engine.Options{
		ConfigPath:       *config,
		StatePath:        *state,
		PollInterval:     poll,
		SizeInterval:     sizeEvery,
		DiscoverInterval: discoverEvery,
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	id := server.Identity{CertificateFile: *tlsCert, KeyFile: *tlsKey}
	if (id.CertificateFile == "") != (id.KeyFile == "") {
		return fmt.Errorf("--tls-cert and --tls-key must both be set")
	}
	scheme := "http"
	if id.CertificateFile != "" {
		if _, err := server.LoadLeaf(id); err != nil {
			return err
		}
		scheme = "https"
	}
	eng.Prime(ctx)
	slog.Info("serving", "scheme", scheme, "addr", *listen, "devices", len(inv.Devices), "poll", poll.String(), "size_interval", sizeEvery.String(), "discover_interval", discoverEvery.String())
	go eng.Run(ctx)
	return server.Listen(ctx, *listen, eng, id)
}

func measure(args []string) error {
	fs := flag.NewFlagSet("size", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "JSON device inventory")
	state := fs.String("state", defaultState, "where to write the stored-size snapshot")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	eng, err := open(*config, *state, time.Hour)
	if err != nil {
		return err
	}
	if err := eng.ScanSizes(context.Background()); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, snapshot.FormatText(eng.Latest()))
	return nil
}

func check(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "JSON device inventory")
	state := fs.String("state", defaultState, "hourly stored-size snapshot to read")
	scan := fs.Bool("scan-size", false, "measure stored size now and update the snapshot")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	interval := time.Duration(0)
	if *scan {
		interval = time.Hour
	}
	eng, err := open(*config, *state, interval)
	if err != nil {
		return err
	}
	ctx := context.Background()
	eng.RefreshCandidates()
	if *scan {
		if err := eng.ScanSizes(ctx); err != nil {
			return err
		}
	} else {
		if err := eng.ReloadState(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		eng.Sample(ctx)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(true)
	return enc.Encode(eng.Latest())
}

func validate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "JSON device inventory")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	inv, err := inventory.Load(*config)
	if err != nil {
		return err
	}
	fmt.Printf("Valid inventory: %d devices\n", len(inv.Devices))
	if len(inv.Ignored) > 0 {
		fmt.Printf("Ignored: %d\n", len(inv.Ignored))
		for _, item := range inv.Ignored {
			if item.SourceIP != "" {
				fmt.Printf("  %s  %s\n", item.SourceIP, item.LogPath)
			} else {
				fmt.Printf("  %s\n", item.LogPath)
			}
		}
	}
	if len(inv.LogDirectories) > 0 {
		fmt.Println("Log directories:")
		for _, dir := range inv.LogDirectories {
			fmt.Printf("  %s\n", dir)
		}
	}
	for _, device := range inv.Devices {
		transport := device.Protocol
		if device.Protocol == "tcp" {
			transport = fmt.Sprintf("tcp/%d", device.TCPPort)
		}
		fmt.Printf("%s  %s  %s  stale %ds  %s\n",
			device.Name, transport, device.SourceIP, device.StaleAfterSeconds, device.LogPath)
	}
	return nil
}

func validateTLS(args []string) error {
	fs := flag.NewFlagSet("validate-tls", flag.ContinueOnError)
	cert := fs.String("tls-cert", "", "PEM certificate chain: server certificate, then intermediates")
	key := fs.String("tls-key", "", "PEM private key for the server certificate")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	leaf, err := server.LoadLeaf(server.Identity{CertificateFile: *cert, KeyFile: *key})
	if err != nil {
		return err
	}
	fmt.Printf("Server certificate: %s\n", leaf.Certificate.Subject.String())
	if len(leaf.Certificate.DNSNames) > 0 {
		fmt.Printf("DNS names: %s\n", strings.Join(leaf.Certificate.DNSNames, ", "))
	}
	if len(leaf.Certificate.IPAddresses) > 0 {
		addrs := make([]string, len(leaf.Certificate.IPAddresses))
		for i, ip := range leaf.Certificate.IPAddresses {
			addrs[i] = ip.String()
		}
		fmt.Printf("IP addresses: %s\n", strings.Join(addrs, ", "))
	}
	fmt.Printf("Intermediates: %d\n", leaf.Intermediates)
	fmt.Println("The chain file lists the server certificate first. Browsers must trust the CA that issued it, and the name in the address bar must be one of the certificate names.")
	return nil
}

func configure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "JSON device inventory")
	var dirs stringList
	fs.Var(&dirs, "log-dir", "absolute syslog directory to watch; repeat for each directory")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if len(dirs) == 0 {
		return errors.New("at least one --log-dir is required")
	}
	if err := inventory.ApplyLogDirectories(*config, dirs); err != nil {
		return err
	}
	if len(dirs) == 1 {
		fmt.Printf("Saved 1 syslog directory in %s\n", *config)
	} else {
		fmt.Printf("Saved %d syslog directories in %s\n", len(dirs), *config)
	}
	for _, dir := range dirs {
		fmt.Printf("  %s\n", dir)
	}
	fmt.Println("Edit log_directories in that file to change the watched paths.")
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("empty path")
	}
	*s = append(*s, value)
	return nil
}

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:18082", "dashboard address (host:port)")
	root := fs.String("root", "", "directory for example logs (default: a new temp directory)")
	once := fs.Bool("once", false, "seed the example, write one page, and exit")
	pollStr := fs.String("poll", "2s", "how often to recheck the example logs")
	tcpPort := fs.Int("tcp-port", 0, "loopback syslog port for the example TCP devices (0 picks a free port)")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if *tcpPort < 0 || *tcpPort > 65535 {
		return fmt.Errorf("--tcp-port must be from 0 to 65535")
	}
	poll, err := parseDuration("poll", *pollStr, 200*time.Millisecond)
	if err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(*listen); err != nil {
		return fmt.Errorf("--listen must be host:port")
	}
	dir := *root
	if dir == "" {
		dir, err = os.MkdirTemp("", "syslog-gomonitor-demo-")
		if err != nil {
			return err
		}
	}
	sc, err := demo.Start(dir, *tcpPort)
	if err != nil {
		return err
	}
	defer sc.Close()

	eng := engine.New(sc.Inv, engine.Options{
		ConfigPath:       sc.ConfigPath,
		StatePath:        filepath.Join(sc.Root, "sizes.json"),
		PollInterval:     poll,
		SizeInterval:     time.Hour,
		DiscoverInterval: 30 * time.Second,
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	eng.Prime(ctx)

	if *once {
		web := filepath.Join(sc.Root, "web")
		if err := os.MkdirAll(web, 0o755); err != nil {
			return err
		}
		page, err := dashboard.Page(eng.Latest())
		if err != nil {
			return err
		}
		htmlPath := filepath.Join(web, "index.html")
		if err := atomicfile.Write(htmlPath, page, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Demo data: %s\nWrote %s\n\n", sc.Root, htmlPath)
		fmt.Fprint(os.Stdout, snapshot.FormatText(eng.Latest()))
		return nil
	}

	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sc.Refresh(); err != nil && ctx.Err() == nil {
					slog.Warn("demo refresh failed", "err", err)
				}
			}
		}
	}()
	go eng.Run(ctx)
	fmt.Fprintf(os.Stdout, "Demo data: %s\nOpen http://%s/\n", sc.Root, *listen)
	return server.Listen(ctx, *listen, eng, server.Identity{})
}

func open(config, state string, sizeEvery time.Duration) (*engine.Engine, error) {
	inv, err := inventory.Load(config)
	if err != nil {
		return nil, err
	}
	return engine.New(inv, engine.Options{
		ConfigPath:   config,
		StatePath:    state,
		PollInterval: 5 * time.Second,
		SizeInterval: sizeEvery,
	}), nil
}

func parseArgs(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

func parseDuration(name, value string, min time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("--%s: %w", name, err)
	}
	if d < min {
		if min == 0 {
			return 0, fmt.Errorf("--%s must not be negative", name)
		}
		return 0, fmt.Errorf("--%s must be at least %s", name, min)
	}
	return d, nil
}
