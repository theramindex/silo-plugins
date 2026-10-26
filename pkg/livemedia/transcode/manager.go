// Package transcode provides bounded GPU-only compatibility sessions for live TV.
// Inputs stay behind a restricted loopback gateway; FFmpeg never receives provider
// credentials or permission to open arbitrary upstream URLs.
package transcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnavailable = errors.New("hardware playback conversion is unavailable on this server")
	ErrCapacity    = errors.New("hardware playback conversion is busy; try again shortly")
	ErrSession     = errors.New("playback conversion session not found")
	mediaName      = regexp.MustCompile(`^(index\.m3u8|segment-[0-9]{9}\.ts)$`)
)

type Config struct {
	GPUQuotaRoot   string
	FFmpegPath     string
	Device         string
	Root           string
	MaxSessions    int
	MaxPerViewer   int
	LeaseTTL       time.Duration
	StartupTimeout time.Duration
}

type Status struct {
	ID      string `json:"sessionId"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	Encoder string `json:"encoder"`
}

type session struct {
	rootActive              bool
	release                 func()
	releaseOnce             sync.Once
	fallback                func(context.Context) (string, *http.Client, error)
	relay                   bool
	encoder                 string
	id, owner, channel, dir string
	client                  *http.Client
	ctx                     context.Context
	cancel                  context.CancelFunc
	lastSeen, started       time.Time
	state, failure          string
	resources               map[string]string
	resourceSeen            map[string]time.Time
	done                    chan struct{}
}

type Manager struct {
	startMu  sync.Mutex
	mu       sync.Mutex
	config   Config
	sessions map[string]*session
	listener net.Listener
	server   *http.Server
	root     string
	closed   bool
	stop     chan struct{}
}

func New(config Config) *Manager {
	if config.MaxSessions <= 0 {
		config.MaxSessions = 2
	}
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 75 * time.Second
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 30 * time.Second
	}
	return &Manager{config: config, sessions: map[string]*session{}, stop: make(chan struct{})}
}

func (m *Manager) initializeLocked(gpu bool) error {
	if m.closed {
		return ErrUnavailable
	}
	if gpu {
		if m.config.FFmpegPath == "" {
			for _, p := range []string{"/usr/lib/jellyfin-ffmpeg/ffmpeg", "/usr/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
				if st, err := os.Stat(p); err == nil && st.Mode()&0111 != 0 {
					m.config.FFmpegPath = p
					break
				}
			}
		}
		if m.config.Device == "" {
			m.config.Device = "/dev/dri/renderD128"
		}
		if _, err := os.Stat(m.config.Device); err != nil || m.config.FFmpegPath == "" {
			return ErrUnavailable
		}
	}
	if m.listener != nil {
		return nil
	}
	root, err := os.MkdirTemp(m.config.Root, "silo-live-transcode-")
	if err != nil {
		return ErrUnavailable
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(root)
		return ErrUnavailable
	}
	m.root, m.listener = root, listener
	m.server = &http.Server{Handler: http.HandlerFunc(m.serveSource), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go m.server.Serve(listener)
	go m.reap()
	return nil
}

// Start accepts only a resolved catalog URL and the caller's restricted client.
// A viewer can have one conversion at a time. Every start gets a new ID so
// a stale response cannot stop a newer attempt, even on the same channel.
type Options struct {
	Rewind bool
	OnStop func()
}

func (m *Manager) Start(owner, channel, source string, client *http.Client) (Status, error) {
	return m.StartWithOptions(owner, channel, source, client, Options{})
}
func (m *Manager) StartWithOptions(owner, channel, source string, client *http.Client, options Options) (Status, error) {
	if owner == "" || channel == "" || client == nil {
		return Status{}, ErrSession
	}
	m.startMu.Lock()
	defer m.startMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var previous []*session
	if err := m.initializeLocked(true); err != nil {
		return Status{}, err
	}
	for id, s := range m.sessions {
		if time.Since(s.lastSeen) > m.config.LeaseTTL {
			m.removeLocked(id)
			continue
		}
		if s.owner == owner {
			previous = append(previous, s)
			m.removeLocked(id)
		}
	}
	if len(previous) > 0 {
		m.mu.Unlock()
		for _, old := range previous {
			<-old.done
		}
		m.mu.Lock()
		if m.closed {
			return Status{}, ErrUnavailable
		}
	}
	if len(m.sessions) >= m.config.MaxSessions {
		return Status{}, ErrCapacity
	}
	gpu, err := acquireGPU(m.config.GPUQuotaRoot)
	if err != nil {
		return Status{}, err
	}
	started := false
	defer func() {
		if !started {
			gpu.Close()
		}
	}()
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return Status{}, ErrUnavailable
	}
	id := hex.EncodeToString(token)
	dir := filepath.Join(m.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Status{}, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{id: id, owner: owner, channel: channel, dir: dir, client: client, ctx: ctx, cancel: cancel, lastSeen: time.Now(), started: time.Now(), state: "starting", encoder: "h264_vaapi", release: options.OnStop, resources: map[string]string{"input": source}, resourceSeen: map[string]time.Time{}, done: make(chan struct{})}
	m.sessions[id] = s
	input := "http://" + m.listener.Addr().String() + "/" + id + "/input"
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-threads", "2", "-filter_threads", "2", "-vaapi_device", m.config.Device,
		"-protocol_whitelist", "http,tcp,crypto", "-format_whitelist", "mpegts,hls,mov,aac,mp3", "-rw_timeout", "15000000", "-i", input,
		"-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-vf", "scale=w='min(1920,iw)':h='min(1080,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,format=nv12,hwupload",
		"-c:v", "h264_vaapi", "-profile:v", "high", "-b:v", "5000k", "-maxrate", "6000k", "-bufsize", "12000k", "-r", "30", "-g", "60", "-force_key_frames", "expr:gte(t,n_forced*2)",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2", "-ar", "48000", "-f", "hls", "-hls_time", "2", "-hls_list_size", strconv.Itoa(hlsSegments(options)), "-hls_delete_threshold", "2",
		"-hls_flags", "delete_segments+independent_segments+temp_file", "-hls_segment_filename", filepath.Join(dir, "segment-%09d.ts"), filepath.Join(dir, "index.m3u8")}
	cmd := exec.CommandContext(ctx, m.config.FFmpegPath, args...)
	setParentDeathSignal(cmd)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard // FFmpeg errors can contain provider secrets.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		delete(m.sessions, id)
		cancel()
		os.RemoveAll(dir)
		return Status{}, ErrUnavailable
	}
	started = true
	go func() {
		_ = cmd.Wait()
		gpu.Close()
		s.releaseOnce.Do(func() {
			if s.release != nil {
				s.release()
			}
		})
		m.mu.Lock()
		if m.sessions[id] == s {
			s.state = "failed"
			s.failure = "The channel could not be converted for playback."
		}
		m.mu.Unlock()
		close(s.done)
		os.RemoveAll(dir)
	}()
	return status(s), nil
}

func status(s *session) Status {
	return Status{ID: s.id, State: s.state, Error: s.failure, Encoder: s.encoder}
}

func (m *Manager) Status(id, owner string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.owner != owner {
		return Status{}, ErrSession
	}
	if time.Since(s.lastSeen) > m.config.LeaseTTL {
		m.removeLocked(id)
		return Status{}, ErrSession
	}
	s.lastSeen = time.Now()
	m.checkLocked(s)
	return status(s), nil
}

func (m *Manager) checkLocked(s *session) {
	if s.state != "starting" {
		return
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "index.m3u8"))
	if err == nil && strings.Contains(string(data), "#EXTINF:") {
		s.state = "ready"
		return
	}
	if time.Since(s.started) > m.config.StartupTimeout {
		s.state = "failed"
		s.failure = "The channel took too long to prepare for playback."
		s.cancel()
	}
}

func (m *Manager) Stop(id, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		if s.owner != owner {
			return ErrSession
		}
		m.removeLocked(id)
	}
	return nil
}

func (m *Manager) removeLocked(id string) {
	if s := m.sessions[id]; s != nil {
		delete(m.sessions, id)
		s.cancel()
		s.releaseOnce.Do(func() {
			if s.release != nil {
				s.release()
			}
		})
		s.client.CloseIdleConnections()
	}
}

// Media reads only generated filenames, never a caller-selected filesystem path.
func (m *Manager) Media(id, owner, name string) ([]byte, error) {
	if !mediaName.MatchString(name) {
		return nil, ErrSession
	}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil || s.owner != owner || time.Since(s.lastSeen) > m.config.LeaseTTL {
		m.mu.Unlock()
		return nil, ErrSession
	}
	s.lastSeen = time.Now()
	file := filepath.Join(s.dir, name)
	m.mu.Unlock()
	f, err := os.Open(file)
	if err != nil {
		return nil, ErrSession
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 3<<20+1))
	if err != nil || len(data) > 3<<20 {
		return nil, ErrSession
	}
	return data, nil
}

func (m *Manager) reap() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				m.checkLocked(s)
				if time.Since(s.lastSeen) > m.config.LeaseTTL {
					m.removeLocked(id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	sessions := make([]*session, 0, len(m.sessions))
	for id, s := range m.sessions {
		sessions = append(sessions, s)
		m.removeLocked(id)
	}
	server, root := m.server, m.root
	m.mu.Unlock()
	if server != nil {
		server.Close()
	}
	for _, s := range sessions {
		<-s.done
	}
	if root != "" {
		os.RemoveAll(root)
	}
}

func hlsSegments(options Options) int {
	if options.Rewind {
		return 900
	}
	return 6
}

// StartRelay retains the source codec and uses no GPU slot. The caller supplies
// an already resolved URL and guarded HTTP client; only local ticket URLs leave
// the manager. Separate viewer sessions keep independent cancellation.
type RelayOptions struct {
	OnStop   func()
	Fallback func(context.Context) (string, *http.Client, error)
}

func (m *Manager) StartRelay(owner, channel, source string, client *http.Client, options ...RelayOptions) (Status, error) {
	if owner == "" || channel == "" || client == nil {
		return Status{}, ErrSession
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.initializeLocked(false); err != nil {
		return Status{}, err
	}
	count := 0
	for id, s := range m.sessions {
		if time.Since(s.lastSeen) > m.config.LeaseTTL {
			m.removeLocked(id)
			continue
		}
		if s.owner == owner {
			count++
		}
	}
	limit := m.config.MaxPerViewer
	if limit <= 0 {
		limit = 4
	}
	if len(m.sessions) >= m.config.MaxSessions || count >= limit {
		return Status{}, ErrCapacity
	}
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return Status{}, ErrUnavailable
	}
	id := hex.EncodeToString(token)
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{id: id, owner: owner, channel: channel, client: client, ctx: ctx, cancel: cancel, lastSeen: time.Now(), started: time.Now(), state: "ready", relay: true, encoder: "copy", resources: map[string]string{"input": source}, resourceSeen: map[string]time.Time{}, done: make(chan struct{})}
	if len(options) > 0 {
		s.release = options[0].OnStop
		s.fallback = options[0].Fallback
	}
	m.sessions[id] = s
	go func() { <-ctx.Done(); close(s.done) }()
	return status(s), nil
}

func (m *Manager) RelayURL(id, owner, resource string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.owner != owner || !s.relay || time.Since(s.lastSeen) > m.config.LeaseTTL {
		return "", ErrSession
	}
	if _, ok := s.resources[resource]; !ok {
		return "", ErrSession
	}
	s.lastSeen = time.Now()
	return "http://" + m.listener.Addr().String() + "/" + s.id + "/" + resource, nil
}
