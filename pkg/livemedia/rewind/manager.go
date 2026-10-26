package rewind

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	sharedhttp "github.com/theramindex/silo-plugins/pkg/livemedia/httpclient"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const DefaultRootDir = "/var/lib/continuum/plugins/silo.ramindex.dispatcharr/timeshift"

var errBufferStopped = errors.New("live rewind buffer stopped")

type Config struct {
	Enabled      bool
	MaxBytes     int64
	MinFreeBytes int64
	MaxBuffers   int
	Window       time.Duration
	IdleTTL      time.Duration
	LeaseTTL     time.Duration
}

func (c Config) normalized() Config {
	if c.MaxBytes <= 0 {
		c.MaxBytes = 5 << 30
	}
	if c.MinFreeBytes <= 0 {
		c.MinFreeBytes = 2 << 30
	}
	if c.MaxBuffers <= 0 {
		c.MaxBuffers = 20
	}
	if c.Window < time.Minute {
		c.Window = 30 * time.Minute
	}
	if c.IdleTTL <= 0 {
		c.IdleTTL = 2 * time.Minute
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 75 * time.Second
	}
	return c
}

type Lease struct {
	ID       string `json:"leaseId"`
	BufferID string `json:"bufferId"`
}

type BufferStatus struct {
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	SegmentCount  int    `json:"segmentCount"`
	WindowSeconds int64  `json:"windowSeconds"`
	Bytes         int64  `json:"bytes"`
}

type Stats struct {
	Enabled       bool  `json:"enabled"`
	Bytes         int64 `json:"bytes"`
	MaxBytes      int64 `json:"maxBytes"`
	MinFreeBytes  int64 `json:"minFreeBytes"`
	MaxBuffers    int   `json:"maxBuffers"`
	FreeBytes     int64 `json:"freeBytes"`
	ActiveBuffers int   `json:"activeBuffers"`
	ActiveLeases  int   `json:"activeLeases"`
}

type Segment struct {
	Seq      int64
	Path     string
	Duration time.Duration
	Size     int64
	Created  time.Time
}

type buffer struct {
	client    *http.Client
	manager   *Manager
	id        string
	channelID string
	streamURL string
	dir       string
	cancel    context.CancelFunc
	done      chan struct{}
	mu        sync.RWMutex
	state     string
	err       string
	segments  []Segment
	nextSeq   int64
	leases    map[string]time.Time
	lastLease time.Time
	window    time.Duration
}

type Manager struct {
	stop      chan struct{}
	closeOnce sync.Once
	root      string
	client    *http.Client
	mu        sync.RWMutex
	initOnce  sync.Once
	config    Config
	buffers   map[string]*buffer
}

func NewManager(root string) *Manager {
	if strings.TrimSpace(root) == "" {
		root = DefaultRootDir
	}
	m := &Manager{
		root: root,
		stop: make(chan struct{}),
		client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          128,
			MaxIdleConnsPerHost:   128,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		}},
		config:  Config{}.normalized(),
		buffers: map[string]*buffer{},
	}
	go m.reaper()
	return m
}

func (m *Manager) SetConfig(config Config) {
	config = config.normalized()
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()
	if !config.Enabled {
		_ = m.Clear()
		return
	}
	m.enforceBudget()
}

func (m *Manager) Acquire(channelID, streamURL string, config Config, clients ...*http.Client) (Lease, error) {
	config = config.normalized()
	if !config.Enabled {
		return Lease{}, fmt.Errorf("live rewind is disabled")
	}
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(streamURL) == "" {
		return Lease{}, fmt.Errorf("missing channel stream")
	}
	if err := m.ensureRoot(); err != nil {
		return Lease{}, fmt.Errorf("prepare rewind cache: %w", err)
	}
	m.SetConfig(config)
	bufferID := stableBufferID(channelID)

	m.mu.Lock()
	b := m.buffers[bufferID]
	if b == nil || b.streamURL != streamURL || b.isTerminal() {
		if b == nil && len(m.buffers) >= config.MaxBuffers {
			m.mu.Unlock()
			return Lease{}, fmt.Errorf("live rewind channel limit reached")
		}
		if b != nil {
			b.retire()
		}
		generation, err := randomID(6)
		if err != nil {
			m.mu.Unlock()
			return Lease{}, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		b = &buffer{
			// Each buffer gets its own directory so a retired buffer's
			// asynchronous cleanup never deletes its replacement's segments.
			client: m.client, manager: m, id: bufferID, channelID: channelID, streamURL: streamURL,
			dir: filepath.Join(m.root, bufferID+"-"+generation), cancel: cancel, done: make(chan struct{}), state: "starting",
			leases: map[string]time.Time{}, lastLease: time.Now(), window: config.Window,
		}
		if len(clients) > 0 && clients[0] != nil {
			b.client = clients[0]
		}
		m.buffers[bufferID] = b
		go b.run(ctx)
	}
	m.mu.Unlock()

	leaseID, err := randomID(18)
	if err != nil {
		return Lease{}, err
	}
	b.mu.Lock()
	b.window = config.Window
	b.leases[leaseID] = time.Now().Add(config.LeaseTTL)
	b.lastLease = time.Now()
	b.mu.Unlock()
	return Lease{ID: leaseID, BufferID: bufferID}, nil
}

func (m *Manager) Heartbeat(leaseID string) bool {
	b := m.bufferForLease(leaseID)
	if b == nil {
		return false
	}
	m.mu.RLock()
	ttl := m.config.LeaseTTL
	m.mu.RUnlock()
	b.mu.Lock()
	if _, ok := b.leases[leaseID]; ok {
		b.leases[leaseID] = time.Now().Add(ttl)
		b.lastLease = time.Now()
		b.mu.Unlock()
		return true
	}
	b.mu.Unlock()
	return false
}

func (m *Manager) Release(leaseID string) {
	b := m.bufferForLease(leaseID)
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.leases, leaseID)
	b.lastLease = time.Now()
	removeNow := len(b.leases) == 0 && (b.state == "failed" || b.state == "stopped")
	b.mu.Unlock()
	if removeNow {
		m.mu.Lock()
		if m.buffers[b.id] == b {
			delete(m.buffers, b.id)
		}
		m.mu.Unlock()
		b.retire()
	}
}

func (m *Manager) Status(leaseID string) (BufferStatus, bool) {
	b := m.bufferForLease(leaseID)
	if b == nil {
		return BufferStatus{}, false
	}
	return b.status(), true
}

func (m *Manager) Manifest(bufferID, leaseID string) ([]byte, bool) {
	b := m.bufferByID(bufferID)
	if b == nil || !b.validLease(leaseID) {
		return nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.segments) == 0 {
		return nil, false
	}
	target := 2
	for _, segment := range b.segments {
		seconds := int(segment.Duration.Seconds() + 0.999)
		if seconds > target {
			target = seconds
		}
	}
	var out strings.Builder
	out.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	out.WriteString("#EXT-X-TARGETDURATION:" + strconv.Itoa(target) + "\n")
	out.WriteString("#EXT-X-MEDIA-SEQUENCE:" + strconv.FormatInt(b.segments[0].Seq, 10) + "\n")
	for _, segment := range b.segments {
		out.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", segment.Duration.Seconds()))
		out.WriteString("segment?buffer_id=" + url.QueryEscape(bufferID) +
			"&sequence=" + strconv.FormatInt(segment.Seq, 10) +
			"&lease=" + url.QueryEscape(leaseID) + "\n")
	}
	return []byte(out.String()), true
}

func (m *Manager) Segment(bufferID, leaseID string, seq int64) ([]byte, bool) {
	b := m.bufferByID(bufferID)
	if b == nil || !b.validLease(leaseID) {
		return nil, false
	}
	b.mu.RLock()
	var path string
	for _, segment := range b.segments {
		if segment.Seq == seq {
			path = segment.Path
			break
		}
	}
	b.mu.RUnlock()
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	return data, err == nil
}

func (m *Manager) Stats() Stats {
	_ = m.ensureRoot()
	m.mu.RLock()
	config := m.config
	buffers := make([]*buffer, 0, len(m.buffers))
	for _, b := range m.buffers {
		buffers = append(buffers, b)
	}
	m.mu.RUnlock()
	stats := Stats{Enabled: config.Enabled, MaxBytes: config.MaxBytes, MinFreeBytes: config.MinFreeBytes, MaxBuffers: config.MaxBuffers, FreeBytes: freeBytes(m.root)}
	now := time.Now()
	for _, b := range buffers {
		b.mu.RLock()
		if b.state == "starting" || b.state == "buffering" {
			stats.ActiveBuffers++
		}
		for _, segment := range b.segments {
			stats.Bytes += segment.Size
		}
		for _, expires := range b.leases {
			if expires.After(now) {
				stats.ActiveLeases++
			}
		}
		b.mu.RUnlock()
	}
	return stats
}

func (m *Manager) Clear() error {
	m.mu.Lock()
	buffers := make([]*buffer, 0, len(m.buffers))
	for _, b := range m.buffers {
		buffers = append(buffers, b)
	}
	m.buffers = map[string]*buffer{}
	m.mu.Unlock()
	for _, b := range buffers {
		b.shutdown()
	}
	// Clear is never called from an ingest goroutine, so waiting here cannot
	// self-deadlock; it keeps the root removal below from racing late writes.
	for _, b := range buffers {
		<-b.done
	}
	if err := os.RemoveAll(m.root); err != nil {
		return err
	}
	return os.MkdirAll(m.root, 0o700)
}

func (m *Manager) ensureRoot() error {
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return err
	}
	m.initOnce.Do(m.removeStaleDirectories)
	return nil
}

func (b *buffer) run(ctx context.Context) {
	defer close(b.done)
	if err := os.RemoveAll(b.dir); err != nil {
		b.fail(sharedhttp.RedactErrorURL(err))
		return
	}
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		b.fail(sharedhttp.RedactErrorURL(err))
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.streamURL, nil)
	if err != nil {
		b.fail(sharedhttp.RedactErrorURL(err))
		return
	}
	response, err := b.client.Do(request)
	if err != nil {
		b.fail(sharedhttp.RedactErrorURL(err))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		b.fail(fmt.Errorf("upstream returned %s", response.Status))
		return
	}
	segmenter := newTSSegmenter(2 * time.Second)
	buffer := make([]byte, 64*1024)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			segments, segmentErr := segmenter.feed(buffer[:count])
			if segmentErr != nil {
				b.fail(segmentErr)
				return
			}
			for _, segment := range segments {
				if err := b.writeSegment(segment); err != nil {
					b.fail(err)
					return
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF || ctx.Err() != nil {
				b.fail(fmt.Errorf("upstream stream ended"))
			} else {
				b.fail(readErr)
			}
			return
		}
	}
}

func (b *buffer) writeSegment(closed closedSegment) error {
	b.mu.Lock()
	seq := b.nextSeq
	b.nextSeq++
	b.mu.Unlock()
	path := filepath.Join(b.dir, fmt.Sprintf("seg-%09d.ts", seq))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, closed.data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	segment := Segment{Seq: seq, Path: path, Duration: closed.duration, Size: int64(len(closed.data)), Created: time.Now()}
	b.mu.Lock()
	if b.state == "stopped" {
		// Retired while writing; the segment must not be counted again.
		b.mu.Unlock()
		_ = os.Remove(path)
		return errBufferStopped
	}
	b.segments = append(b.segments, segment)
	b.state = "buffering"
	b.err = ""
	b.evictWindowLocked()
	b.mu.Unlock()
	b.manager.enforceBudget()
	return nil
}

func (b *buffer) evictWindowLocked() {
	var duration time.Duration
	for _, segment := range b.segments {
		duration += segment.Duration
	}
	for duration > b.window && len(b.segments) > 3 {
		oldest := b.segments[0]
		b.segments = b.segments[1:]
		duration -= oldest.Duration
		_ = os.Remove(oldest.Path)
	}
}

func (b *buffer) status() BufferStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	status := BufferStatus{State: b.state, Error: b.err, SegmentCount: len(b.segments)}
	for _, segment := range b.segments {
		status.Bytes += segment.Size
		status.WindowSeconds += int64(segment.Duration.Seconds())
	}
	return status
}

func (b *buffer) validLease(leaseID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	expires, ok := b.leases[leaseID]
	if !ok || expires.Before(time.Now()) {
		delete(b.leases, leaseID)
		return false
	}
	b.lastLease = time.Now()
	return true
}

func (b *buffer) isTerminal() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state == "failed" || b.state == "stopped"
}

func (b *buffer) fail(err error) {
	b.mu.Lock()
	if b.state != "stopped" {
		b.state = "failed"
		b.err = segmenterErrorMessage(err)
	}
	b.mu.Unlock()
}

// shutdown cancels ingest and detaches the buffer's segments without waiting
// for the ingest goroutine, so it is safe to call from that goroutine itself
// (budget eviction of its own buffer) and while holding m.mu.
func (b *buffer) shutdown() {
	b.cancel()
	b.mu.Lock()
	b.state = "stopped"
	b.leases = map[string]time.Time{}
	b.segments = nil
	b.mu.Unlock()
}

// retire shuts the buffer down and removes its directory once ingest exits.
// The wait runs in its own goroutine: never block on done inline, because
// the caller may be the ingest goroutine being waited on.
func (b *buffer) retire() {
	b.shutdown()
	go func() {
		<-b.done
		_ = os.RemoveAll(b.dir)
	}()
}

func (m *Manager) bufferByID(id string) *buffer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buffers[id]
}

func (m *Manager) bufferForLease(leaseID string) *buffer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.buffers {
		b.mu.RLock()
		_, ok := b.leases[leaseID]
		b.mu.RUnlock()
		if ok {
			return b
		}
	}
	return nil
}

func (m *Manager) enforceBudget() {
	m.mu.RLock()
	config := m.config
	buffers := make([]*buffer, 0, len(m.buffers))
	for _, b := range m.buffers {
		buffers = append(buffers, b)
	}
	m.mu.RUnlock()
	type candidate struct {
		buffer  *buffer
		segment Segment
	}
	var candidates []candidate
	var total int64
	for _, b := range buffers {
		b.mu.RLock()
		for index, segment := range b.segments {
			total += segment.Size
			if index < len(b.segments)-3 {
				candidates = append(candidates, candidate{buffer: b, segment: segment})
			}
		}
		b.mu.RUnlock()
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].segment.Created.Before(candidates[j].segment.Created) })
	for _, item := range candidates {
		if total <= config.MaxBytes && freeBytes(m.root) >= config.MinFreeBytes {
			return
		}
		if item.buffer.removeSegment(item.segment.Seq) {
			total -= item.segment.Size
		}
	}
	if total <= config.MaxBytes && freeBytes(m.root) >= config.MinFreeBytes {
		return
	}
	type bufferUsage struct {
		buffer    *buffer
		bytes     int64
		lastLease time.Time
	}
	usage := make([]bufferUsage, 0, len(buffers))
	for _, b := range buffers {
		b.mu.RLock()
		item := bufferUsage{buffer: b, lastLease: b.lastLease}
		for _, segment := range b.segments {
			item.bytes += segment.Size
		}
		b.mu.RUnlock()
		usage = append(usage, item)
	}
	sort.Slice(usage, func(i, j int) bool { return usage[i].lastLease.Before(usage[j].lastLease) })
	for _, item := range usage {
		if total <= config.MaxBytes && freeBytes(m.root) >= config.MinFreeBytes {
			break
		}
		// Only the call that actually detaches a buffer subtracts its bytes, so
		// concurrent enforcement passes never count one eviction twice.
		m.mu.Lock()
		evicted := m.buffers[item.buffer.id] == item.buffer
		if evicted {
			delete(m.buffers, item.buffer.id)
			item.buffer.retire()
		}
		m.mu.Unlock()
		if evicted {
			total -= item.bytes
		}
	}
}

func (b *buffer) removeSegment(seq int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index, segment := range b.segments {
		if segment.Seq != seq || index >= len(b.segments)-3 {
			continue
		}
		b.segments = append(b.segments[:index], b.segments[index+1:]...)
		_ = os.Remove(segment.Path)
		return true
	}
	return false
}

func (m *Manager) reaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.prune()
		}
	}
}

func (m *Manager) prune() {
	m.mu.RLock()
	config := m.config
	m.mu.RUnlock()
	now := time.Now()
	var remove []*buffer
	m.mu.Lock()
	for id, b := range m.buffers {
		b.mu.Lock()
		for lease, expires := range b.leases {
			if expires.Before(now) {
				delete(b.leases, lease)
			}
		}
		idle := len(b.leases) == 0 && now.Sub(b.lastLease) >= config.IdleTTL
		b.mu.Unlock()
		if idle {
			delete(m.buffers, id)
			remove = append(remove, b)
		}
	}
	m.mu.Unlock()
	for _, b := range remove {
		b.retire()
	}
	if !config.Enabled {
		_ = m.Clear()
	}
	if config.Enabled {
		m.enforceBudget()
	}
}

func (m *Manager) removeStaleDirectories() {
	entries, _ := os.ReadDir(m.root)
	for _, entry := range entries {
		if entry.IsDir() {
			_ = os.RemoveAll(filepath.Join(m.root, entry.Name()))
		}
	}
}

func stableBufferID(channelID string) string {
	sum := sha256.Sum256([]byte(channelID))
	return hex.EncodeToString(sum[:12])
}

func randomID(bytesCount int) (string, error) {
	data := make([]byte, bytesCount)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func freeBytes(path string) int64 {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0
	}
	return int64(stats.Bavail) * int64(stats.Bsize)
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stop); m.Clear(); m.client.CloseIdleConnections() })
}
