package transcode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sharedhttp "github.com/theramindex/silo-plugins/pkg/livemedia/httpclient"
)

func fixtureManager(t *testing.T, ttl time.Duration) *Manager {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "encoder")
	// A real child owns the output lifecycle; these tests do not assert FFmpeg's
	// flags or pretend this stub proves codec compatibility (the GPU test does).
	script := "#!/bin/sh\nfor arg; do out=\"$arg\"; done\ndir=$(dirname \"$out\")\nprintf segment > \"$dir/segment-000000000.ts\"\nprintf '#EXTM3U\\n#EXT-X-TARGETDURATION:2\\n#EXTINF:2,\\nsegment-000000000.ts\\n' > \"$out\"\nexec sleep 120\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m := New(Config{GPUQuotaRoot: filepath.Join(dir, "quota"), FFmpegPath: binary, Device: binary, Root: dir, LeaseTTL: ttl, StartupTimeout: time.Second})
	t.Cleanup(m.Close)
	return m
}

func waitReady(t *testing.T, m *Manager, id, owner string) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		s, err := m.Status(id, owner)
		if err != nil {
			t.Fatal(err)
		}
		if s.State == "ready" {
			return
		}
		if s.State == "failed" {
			t.Fatalf("session failed: %+v", s)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session did not become ready")
}

func TestSessionOwnershipCapacityAndCleanup(t *testing.T) {
	m := fixtureManager(t, time.Minute)
	start := func(owner, channel string) Status {
		t.Helper()
		s, err := m.Start(owner, channel, "https://example.com/live.ts", http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := start("viewer-a", "one")
	waitReady(t, m, first.ID, "viewer-a")
	if _, err := m.Status(first.ID, "viewer-b"); err != ErrSession {
		t.Fatal("another viewer accessed status", err)
	}
	if _, err := m.Media(first.ID, "viewer-b", "index.m3u8"); err != ErrSession {
		t.Fatal("another viewer read media", err)
	}
	if err := m.Stop(first.ID, "viewer-b"); err != ErrSession {
		t.Fatal("another viewer stopped encoder", err)
	}
	if _, err := m.Media(first.ID, "viewer-a", "../secret"); err != ErrSession {
		t.Fatal("accepted arbitrary file", err)
	}
	same := start("viewer-a", "one")
	if same.ID == first.ID {
		t.Fatal("retry reused a session that a stale start response can stop")
	}
	if err := m.Stop(first.ID, "viewer-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(same.ID, "viewer-a"); err != nil {
		t.Fatal("late stop killed the newer retry", err)
	}
	second := start("viewer-b", "two")
	if _, err := m.Start("viewer-c", "three", "https://example.com/live.ts", http.DefaultClient); err != ErrCapacity {
		t.Fatal("global capacity not enforced", err)
	}
	replacement := start("viewer-a", "other")
	if replacement.ID == first.ID {
		t.Fatal("channel change reused old stream")
	}
	if _, err := m.Status(first.ID, "viewer-a"); err != ErrSession {
		t.Fatal("old channel is still accessible", err)
	}
	if err := m.Stop(second.ID, "viewer-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(second.ID, "viewer-b"); err != ErrSession {
		t.Fatal("stopped stream is still accessible", err)
	}
	m.Close()
	entries, err := os.ReadDir(m.config.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "silo-live-media-") {
			t.Fatal("conversion files leaked after close")
		}
	}
}

func TestAbandonedAndFailedSessionsReleaseResources(t *testing.T) {
	m := fixtureManager(t, 50*time.Millisecond)
	s, err := m.Start("viewer", "one", "https://example.com/live.ts", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	process := m.sessions[s.ID]
	m.mu.Unlock()
	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		t.Fatal("abandoned encoder did not stop")
	}
	if _, err := m.Status(s.ID, "viewer"); err != ErrSession {
		t.Fatal("expired session is still alive")
	}
	// A child that exits before producing a playlist must never be called ready.
	if err := os.WriteFile(m.config.FFmpegPath, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	failed, err := m.Start("viewer", "two", "https://example.com/live.ts", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		status, err := m.Status(failed.ID, "viewer")
		if err == nil && status.State == "failed" && status.Error != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("failed encoder was not reported")
}

func TestSourceGatewayRestrictsNestedPlaylistsAndRedirects(t *testing.T) {
	secret := "test-provider-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nmedia.m3u8?token="+secret+"\n")
		case "/media.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key?token="+secret+"\"\n#EXTINF:2,\n/segment.ts?token="+secret+"\n")
		case "/segment.ts":
			io.WriteString(w, "video")
		case "/key":
			io.WriteString(w, "key")
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.2/private", 302)
		case "/unsafe.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXTINF:2,\nfile:///etc/passwd\n")
		}
	}))
	defer upstream.Close()
	m := fixtureManager(t, time.Minute)
	// Configured source host can be private, but a different loopback destination
	// in a redirect remains blocked by the actual production dial guard.
	host := strings.TrimPrefix(upstream.URL, "http://")
	client := sharedhttp.NewRestricted(strings.Split(host, ":")[0])
	for _, source := range []string{"/index.m3u8", "/redirect", "/unsafe.m3u8"} {
		s, err := m.Start("viewer", source, upstream.URL+source, client)
		if err != nil {
			t.Fatal(err)
		}
		root := "http://" + m.listener.Addr().String() + "/" + s.ID + "/input"
		get := func(url string) (int, string) {
			t.Helper()
			r, err := http.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			return r.StatusCode, string(b)
		}
		code, body := get(root)
		if source != "/index.m3u8" {
			if code != 502 {
				t.Fatalf("unsafe source returned %d", code)
			}
			continue
		}
		if code != 200 || strings.Contains(body, secret) || strings.Contains(body, upstream.URL) {
			t.Fatalf("source credentials escaped playlist: %s", body)
		}
		lines := strings.Split(strings.TrimSpace(body), "\n")
		code, body = get(lines[len(lines)-1])
		if code != 200 || strings.Contains(body, secret) {
			t.Fatalf("nested playlist failed: %d %s", code, body)
		}
		refs := uriAttribute.FindStringSubmatch(body)
		if len(refs) != 2 {
			t.Fatal("key URI was not rewritten")
		}
		code, key := get(refs[1])
		if code != 200 || key != "key" {
			t.Fatal("rewritten key unavailable")
		}
		lines = strings.Split(strings.TrimSpace(body), "\n")
		code, segment := get(lines[len(lines)-1])
		if code != 200 || segment != "video" {
			t.Fatal("rewritten segment unavailable")
		}
	}
}

// Runs on a Linux GPU host; ordinary CI skips it. It exercises actual FFmpeg,
// restricted HTTP input, generated HLS files, and independent ffprobe output.
func TestHardwarePipeline(t *testing.T) {
	ffmpeg := "/usr/lib/jellyfin-ffmpeg/ffmpeg"
	if _, err := os.Stat("/dev/dri/renderD128"); err != nil {
		t.Skip("no VAAPI device")
	}
	if _, err := os.Stat(ffmpeg); err != nil {
		t.Skip("no bundled FFmpeg")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "source.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "12", "-c:v", "mpeg2video", "-threads", "1", "-c:a", "ac3", "-f", "mpegts", fixture}
	if b, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, b)
	}
	// Serve a finite MPEG-TS at live speed so the encoder stays active while read.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.m3u8" {
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:12\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:12,\nfixture.ts\n")
			return
		}
		f, err := os.Open(fixture)
		if err != nil {
			return
		}
		defer f.Close()
		buffer := make([]byte, 188*100)
		for {
			n, err := f.Read(buffer)
			if n > 0 {
				if _, e := w.Write(buffer[:n]); e != nil {
					return
				}
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				return
			}
		}
	}))
	defer source.Close()
	for _, input := range []string{"/live.ts", "/index.m3u8"} {
		t.Run(input, func(t *testing.T) {
			diagnostic := filepath.Join(dir, "encoder.log")
			wrapper := filepath.Join(dir, "ffmpeg")
			if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+ffmpeg+" \"$@\" 2>\""+diagnostic+"\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			m := New(Config{FFmpegPath: wrapper, Root: dir})
			defer m.Close()
			s, err := m.Start("viewer", "channel", source.URL+input, http.DefaultClient)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(25 * time.Second)
			var segment []byte
			for time.Now().Before(deadline) {
				status, err := m.Status(s.ID, "viewer")
				if err != nil {
					t.Fatal(err)
				}
				if status.State == "failed" {
					detail, _ := os.ReadFile(diagnostic)
					t.Fatalf("hardware session failed: %+v; synthetic fixture diagnostic: %s", status, detail)
				}
				if status.State == "ready" {
					body, err := m.Media(s.ID, "viewer", "index.m3u8")
					if err != nil {
						t.Fatal(err)
					}
					for _, line := range strings.Split(string(body), "\n") {
						if strings.HasPrefix(line, "segment-") {
							segment, err = m.Media(s.ID, "viewer", line)
							break
						}
					}
					if err == nil && len(segment) > 0 {
						break
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			if len(segment) == 0 {
				t.Fatal("no playable hardware segment")
			}
			output := filepath.Join(dir, "output.ts")
			if err := os.WriteFile(output, segment, 0600); err != nil {
				t.Fatal(err)
			}
			probe := filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
			b, err := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_name", "-of", "json", output).CombinedOutput()
			if err != nil {
				t.Fatalf("probe: %v %s", err, b)
			}
			var result struct {
				Streams []struct {
					Codec string `json:"codec_name"`
				} `json:"streams"`
			}
			if json.Unmarshal(b, &result) != nil {
				t.Fatalf("invalid probe: %s", b)
			}
			found := map[string]bool{}
			for _, stream := range result.Streams {
				found[stream.Codec] = true
			}
			if !found["h264"] || !found["aac"] {
				t.Fatalf("expected H264/AAC, got %s", b)
			}
		})
	}
}

func TestGPUQuotaSharedAcrossPluginManagers(t *testing.T) {
	quota := t.TempDir()
	a, b, c := fixtureManager(t, time.Minute), fixtureManager(t, time.Minute), fixtureManager(t, time.Minute)
	a.config.GPUQuotaRoot = quota
	b.config.GPUQuotaRoot = quota
	c.config.GPUQuotaRoot = quota
	first, err := a.Start("a", "one", "https://example.com/live.ts", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Start("b", "two", "https://example.com/live.ts", http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Start("c", "three", "https://example.com/live.ts", http.DefaultClient); err != ErrCapacity {
		t.Fatalf("third plugin exceeded shared GPU capacity: %v", err)
	}
	if err = a.Stop(first.ID, "a"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err = c.Start("c", "three", "https://example.com/live.ts", http.DefaultClient); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stopped plugin did not return GPU slot", err)
}

func TestRelayRetainsCodecAndStopsOnViewerDisconnect(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "video/mp2t")
		w.Write([]byte("video-bytes"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer source.Close()
	manager := New(Config{Root: t.TempDir(), MaxSessions: 8})
	defer manager.Close()
	session, err := manager.StartRelay("viewer", "channel", source.URL, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if session.Encoder != "copy" {
		t.Fatal("relay attempted transcoding")
	}
	if _, err = manager.RelayURL(session.ID, "other", "input"); err != ErrSession {
		t.Fatal("other viewer adopted relay")
	}
	target, err := manager.RelayURL(session.ID, "viewer", "input")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 11)
	if _, err = io.ReadFull(response.Body, buffer); err != nil || string(buffer) != "video-bytes" {
		t.Fatal("continuous stream was buffered", err)
	}
	if err = manager.Stop(session.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source never started")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("relay retained upstream after stop")
	}
}

func TestRelayTriesAlternateBeforeDeliveringBytes(t *testing.T) {
	var attempts []string
	var attemptsMu sync.Mutex
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptsMu.Lock()
		attempts = append(attempts, r.URL.Path)
		attemptsMu.Unlock()
		if r.URL.Path == "/busy" {
			http.Error(w, "busy", 503)
			return
		}
		io.WriteString(w, "alternate-video")
	}))
	defer source.Close()
	manager := New(Config{MaxSessions: 8, Root: t.TempDir()})
	defer manager.Close()
	session, err := manager.StartRelay("viewer", "station", source.URL+"/busy", http.DefaultClient, RelayOptions{Fallback: func(context.Context) (string, *http.Client, error) {
		return source.URL + "/available", http.DefaultClient, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.RelayURL(session.ID, "viewer", "input")
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || string(body) != "alternate-video" {
		t.Fatalf("alternate stream: %d %s", r.StatusCode, body)
	}
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	if len(attempts) != 2 || attempts[0] != "/busy" || attempts[1] != "/available" {
		t.Fatalf("unexpected attempts %v", attempts)
	}
}

func TestRelayPreservesByteRanges(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=4-8" {
			t.Error("range header was not forwarded")
		}
		w.Header().Set("Content-Range", "bytes 4-8/20")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(206)
		io.WriteString(w, "video")
	}))
	defer source.Close()
	manager := New(Config{Root: t.TempDir(), MaxSessions: 4})
	defer manager.Close()
	session, err := manager.StartRelay("viewer", "station", source.URL, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.RelayURL(session.ID, "viewer", "input")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", target, nil)
	request.Header.Set("Range", "bytes=4-8")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 206 || response.Header.Get("Content-Range") != "bytes 4-8/20" || string(data) != "video" {
		t.Fatalf("range response lost: %d %s", response.StatusCode, data)
	}
}

func TestRelayRewritesBOMPlaylistsAndRejectsUnquotedSecretURIs(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		if r.URL.Path == "/invalid" {
			io.WriteString(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=https://provider.example/password/key\n")
			return
		}
		io.WriteString(w, "\xef\xbb\xbf  \n#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI = \"https://provider.example/password/key\"\n#EXTINF:2,\nhttps://provider.example/password/video.ts\n")
	}))
	defer source.Close()
	manager := New(Config{Root: t.TempDir(), MaxSessions: 4})
	defer manager.Close()
	for _, path := range []string{"/valid", "/invalid"} {
		session, err := manager.StartRelay("viewer", path, source.URL+path, http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		target, _ := manager.RelayURL(session.ID, "viewer", "input")
		r, err := http.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if strings.Contains(string(body), "password") {
			t.Fatal("provider URL escaped rewritten playlist")
		}
		if path == "/valid" && (r.StatusCode != 200 || !strings.Contains(string(body), "media?session_id=")) {
			t.Fatalf("BOM playlist failed: %d %s", r.StatusCode, body)
		}
		if path == "/invalid" && r.StatusCode != 502 {
			t.Fatalf("unquoted secret URI accepted: %d", r.StatusCode)
		}
	}
}

func TestRestartCleansDeadProcessCacheAndKeepsLiveProcessCache(t *testing.T) {
	root := t.TempDir()
	dead := filepath.Join(root, "silo-live-media-1073741824-dead")
	alive := filepath.Join(root, "silo-live-media-"+strconv.Itoa(os.Getpid())+"-alive")
	for _, dir := range []string{dead, alive} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "segment.ts"), []byte("video"), 0600)
	}
	manager := New(Config{Root: root})
	defer manager.Close()
	if _, err := manager.StartRelay("viewer", "channel", "https://example.com/video.ts", http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("dead encoder cache survived restart")
	}
	if _, err := os.Stat(alive); err != nil {
		t.Fatal("live process cache was removed", err)
	}
}

func TestRewindHonorsConfiguredFreeDiskReserve(t *testing.T) {
	m := fixtureManager(t, time.Minute)
	if _, err := m.StartWithOptions("viewer", "channel", "https://example.com/live.ts", http.DefaultClient, Options{Rewind: true, MinFreeBytes: 1 << 62}); err != ErrUnavailable {
		t.Fatalf("encoder started below configured reserve: %v", err)
	}
}
