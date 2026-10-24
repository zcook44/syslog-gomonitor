// Package engine schedules the two kinds of work this monitor does:
// a short loop that stats each device log and checks TCP sessions, and a
// separate pass that sums stored log files. The size pass stays off the
// fast loop so a large rotation directory is not walked on every refresh.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"syslog-gomonitor/internal/activity"
	"syslog-gomonitor/internal/inventory"
	"syslog-gomonitor/internal/session"
	"syslog-gomonitor/internal/snapshot"
	"syslog-gomonitor/internal/status"
	"syslog-gomonitor/internal/storage"
)

// Options configures one engine. Zero poll and size intervals are filled in
// by the caller; a non-positive size interval disables the in-process scan.
type Options struct {
	ConfigPath   string
	StatePath    string
	PollInterval time.Duration
	SizeInterval time.Duration
	Now          func() time.Time
	Lister       session.Lister
}

// Engine holds the latest status and the cached hourly sizes.
type Engine struct {
	mu         sync.RWMutex
	inv        inventory.Inventory
	configPath string
	configMod  time.Time
	statePath  string
	poll       time.Duration
	sizeEvery  time.Duration
	now        func() time.Time
	lister     session.Lister
	measuredAt time.Time
	sizes      map[string]storage.Measurement
	latest     snapshot.Snapshot
	subs       map[chan snapshot.Snapshot]struct{}
}

// New prepares an engine. The inventory is already validated.
func New(inv inventory.Inventory, opt Options) *Engine {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	lister := opt.Lister
	if lister == nil {
		lister = session.Command{}
	}
	eng := &Engine{
		inv:        inv,
		configPath: opt.ConfigPath,
		statePath:  opt.StatePath,
		poll:       opt.PollInterval,
		sizeEvery:  opt.SizeInterval,
		now:        now,
		lister:     lister,
		sizes:      map[string]storage.Measurement{},
		subs:       map[chan snapshot.Snapshot]struct{}{},
	}
	if opt.ConfigPath != "" {
		if info, err := os.Stat(opt.ConfigPath); err == nil {
			eng.configMod = info.ModTime()
		}
	}
	return eng
}

// Prime loads a previous size snapshot and measures sizes when the saved
// result is missing or older than the interval. It always publishes one
// activity sample before the loops start.
func (e *Engine) Prime(ctx context.Context) {
	if err := e.ReloadState(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("ignoring unreadable size snapshot", "path", e.statePath, "err", err)
	}
	if e.needsInitialScan() {
		if err := e.ScanSizes(ctx); err != nil {
			slog.Warn("stored-size measurement failed", "err", err)
		}
		return
	}
	e.Sample(ctx)
}

// Run polls activity until ctx is cancelled. Size measurement runs on its
// own timer. Call Prime before Run.
func (e *Engine) Run(ctx context.Context) {
	if e.poll <= 0 {
		<-ctx.Done()
		return
	}
	poll := time.NewTicker(e.poll)
	defer poll.Stop()

	var sizeC <-chan time.Time
	var sizeTimer *time.Timer
	if e.sizeEvery > 0 {
		sizeTimer = time.NewTimer(e.remainingSizeWait())
		sizeC = sizeTimer.C
		defer sizeTimer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			e.reload(ctx)
			e.Sample(ctx)
		case <-sizeC:
			if err := e.ScanSizes(ctx); err != nil {
				slog.Warn("stored-size measurement failed", "err", err)
			}
			sizeTimer.Reset(e.sizeEvery)
		}
	}
}

// Sample stats every device and merges the cached size measurement.
func (e *Engine) Sample(ctx context.Context) snapshot.Snapshot {
	inv := e.inventory()
	var sessions map[session.Endpoint]struct{}
	var tcpErr error
	if inv.NeedsTCP() {
		sessions, tcpErr = e.lister.Established(ctx)
	}
	measuredAt, sizes := e.sizeView()
	now := e.now().UTC()
	rows := make([]snapshot.Device, 0, len(inv.Devices))
	for _, device := range inv.Devices {
		rows = append(rows, e.row(device, now, sessions, tcpErr, measuredAt, sizes))
	}
	snapshot.Sort(rows)
	var measured *time.Time
	if !measuredAt.IsZero() {
		utc := measuredAt.UTC()
		measured = &utc
	}
	snap := snapshot.Snapshot{
		GeneratedAt:           now,
		MonitorOverdueSeconds: inv.MonitorOverdueSeconds,
		PollIntervalSeconds:   seconds(e.poll),
		SizeMeasuredAt:        measured,
		SizeIntervalSeconds:   seconds(e.sizeEvery),
		Devices:               rows,
		Summary:               snapshot.Summarize(rows),
	}
	e.mu.Lock()
	e.latest = snap
	e.mu.Unlock()
	e.notify(snap)
	return snap
}

// ScanSizes walks log directories, saves the snapshot, and republishes status.
func (e *Engine) ScanSizes(ctx context.Context) error {
	measured := storage.Scan(e.inventory().Devices)
	now := e.now().UTC()
	e.mu.Lock()
	e.measuredAt = now
	e.sizes = measured
	e.mu.Unlock()

	var err error
	if e.statePath != "" {
		err = snapshot.SaveSizeState(e.statePath, sizeState(now, measured))
	}
	e.Sample(ctx)
	if err != nil {
		return err
	}
	slog.Info("stored-size measurement finished", "devices", len(measured), "at", now.Format(time.RFC3339))
	return nil
}

// ReloadState loads the hourly file when one exists.
func (e *Engine) ReloadState() error {
	if e.statePath == "" {
		return nil
	}
	state, err := snapshot.LoadSizeState(e.statePath)
	if err != nil {
		return err
	}
	sizes := make(map[string]storage.Measurement, len(state.Devices))
	for ip, item := range state.Devices {
		sizes[ip] = storage.Measurement{
			Bytes:  item.Bytes,
			Files:  item.Files,
			Detail: item.Detail,
			Err:    item.Error,
		}
	}
	e.mu.Lock()
	e.measuredAt = state.MeasuredAt
	e.sizes = sizes
	e.mu.Unlock()
	return nil
}

// Latest returns the most recently published snapshot.
func (e *Engine) Latest() snapshot.Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.latest
}

// Subscribe receives later snapshots. The channel is buffered and keeps only
// the newest unread sample so a slow client cannot stall the poll loop.
func (e *Engine) Subscribe() chan snapshot.Snapshot {
	ch := make(chan snapshot.Snapshot, 1)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber created by Subscribe.
func (e *Engine) Unsubscribe(ch chan snapshot.Snapshot) {
	e.mu.Lock()
	delete(e.subs, ch)
	e.mu.Unlock()
}

func (e *Engine) reload(ctx context.Context) {
	if e.configPath == "" {
		return
	}
	info, err := os.Stat(e.configPath)
	if err != nil {
		return
	}
	e.mu.RLock()
	prev := e.configMod
	e.mu.RUnlock()
	if !info.ModTime().After(prev) {
		return
	}
	inv, err := inventory.Load(e.configPath)
	if err != nil {
		slog.Error("inventory reload failed; keeping the previous inventory", "err", err)
		e.mu.Lock()
		e.configMod = info.ModTime()
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	e.inv = inv
	e.configMod = info.ModTime()
	e.mu.Unlock()
	slog.Info("reloaded inventory", "devices", len(inv.Devices))
	if e.sizeEvery > 0 {
		if err := e.ScanSizes(ctx); err != nil {
			slog.Warn("stored-size measurement failed", "err", err)
		}
	}
}

func (e *Engine) needsInitialScan() bool {
	if e.sizeEvery <= 0 {
		return false
	}
	e.mu.RLock()
	at := e.measuredAt
	e.mu.RUnlock()
	if at.IsZero() {
		return true
	}
	return e.now().Sub(at) >= e.sizeEvery
}

func (e *Engine) remainingSizeWait() time.Duration {
	e.mu.RLock()
	at := e.measuredAt
	e.mu.RUnlock()
	if at.IsZero() {
		return e.sizeEvery
	}
	wait := at.Add(e.sizeEvery).Sub(e.now())
	if wait < 0 {
		return 0
	}
	return wait
}

func (e *Engine) inventory() inventory.Inventory {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inv
}

func (e *Engine) sizeView() (time.Time, map[string]storage.Measurement) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]storage.Measurement, len(e.sizes))
	for ip, item := range e.sizes {
		out[ip] = item
	}
	return e.measuredAt, out
}

func (e *Engine) notify(snap snapshot.Snapshot) {
	e.mu.RLock()
	subs := make([]chan snapshot.Snapshot, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
	}
	e.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- snap:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
}

func (e *Engine) row(device inventory.Device, now time.Time, sessions map[session.Endpoint]struct{}, tcpErr error, measuredAt time.Time, sizes map[string]storage.Measurement) snapshot.Device {
	obs := activity.Inspect(device.LogPath, now, time.Duration(device.StaleAfterSeconds)*time.Second)
	conn, connDetail := connectionFor(device, sessions, tcpErr)
	row := snapshot.Device{
		Name:              device.Name,
		SourceIP:          device.SourceIP,
		Protocol:          device.Protocol,
		TCPPort:           device.TCPPort,
		LogPath:           device.LogPath,
		StaleAfterSeconds: device.StaleAfterSeconds,
		LogStatus:         obs.Status,
		LogDetail:         obs.Detail,
		ActiveBytes:       obs.ActiveBytes,
		Connection:        conn,
		ConnectionDetail:  connDetail,
		Status:            status.Combine(obs.Status, conn),
		SizeDetail:        "Not measured yet",
	}
	if !obs.LastWrite.IsZero() {
		utc := obs.LastWrite.UTC()
		row.LastWrite = &utc
	}
	if obs.AgeKnown {
		seconds := int(obs.Age / time.Second)
		row.AgeSeconds = &seconds
	}
	if !measuredAt.IsZero() {
		if item, ok := sizes[device.SourceIP]; ok {
			row.SizeKnown = true
			row.StoredBytes = item.Bytes
			row.StoredFiles = item.Files
			row.StoredError = item.Err
			if item.Detail != "" {
				row.SizeDetail = item.Detail
			}
		}
	}
	return row
}

func connectionFor(device inventory.Device, sessions map[session.Endpoint]struct{}, tcpErr error) (string, string) {
	if device.Protocol != "tcp" {
		return status.ConnUDP, "Connectionless"
	}
	if tcpErr != nil {
		return status.ConnUnknown, tcpErr.Error()
	}
	if _, ok := sessions[session.Endpoint{IP: device.SourceIP, Port: device.TCPPort}]; ok {
		return status.ConnConnected, fmt.Sprintf("Established TCP/%d", device.TCPPort)
	}
	return status.ConnDisconnected, fmt.Sprintf("No TCP/%d session", device.TCPPort)
}

func sizeState(when time.Time, measured map[string]storage.Measurement) snapshot.SizeState {
	devices := make(map[string]snapshot.StoredSize, len(measured))
	for ip, item := range measured {
		devices[ip] = snapshot.StoredSize{
			Bytes:  item.Bytes,
			Files:  item.Files,
			Detail: item.Detail,
			Error:  item.Err,
		}
	}
	return snapshot.SizeState{MeasuredAt: when, Devices: devices}
}

func seconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	sec := int(d / time.Second)
	if sec == 0 {
		return 1
	}
	return sec
}
