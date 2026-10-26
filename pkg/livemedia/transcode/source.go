package transcode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var uriAttribute = regexp.MustCompile(`URI="([^"]*)"`)

func (m *Manager) serveSource(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method != http.MethodGet || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	s := m.sessions[parts[0]]
	var raw string
	var client *http.Client
	if s != nil && s.relay && parts[1] == "input" && s.rootActive {
		m.mu.Unlock()
		http.Error(w, "stream is already active", 409)
		return
	}
	if s != nil {
		client = s.client
		if s.relay && parts[1] == "input" {
			s.rootActive = true
		}
		raw = s.resources[parts[1]]
		s.resourceSeen[parts[1]] = time.Now()
	}
	m.mu.Unlock()
	if s != nil && s.relay && parts[1] == "input" {
		defer func() { m.mu.Lock(); s.rootActive = false; m.mu.Unlock() }()
	}
	if raw == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	initialDeadline := time.AfterFunc(12*time.Second, cancel)
	defer initialDeadline.Stop()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	var response *http.Response
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		req, createErr := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if createErr != nil {
			break
		}
		response, err = client.Do(req)
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			break
		}
		if response != nil {
			response.Body.Close()
			response = nil
		}
		if parts[1] != "input" || s.fallback == nil {
			break
		}
		next, nextClient, failure := s.fallback(ctx)
		if failure != nil {
			break
		}
		m.mu.Lock()
		s.client = nextClient
		client = nextClient
		s.resources["input"] = next
		m.mu.Unlock()
		raw = next
	}
	if response == nil || err != nil {
		code := 502
		if s.fallback != nil {
			code = 503
		}
		http.Error(w, "source unavailable", code)
		return
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	prefix, _ := reader.Peek(7)
	initialDeadline.Stop()
	if strings.TrimSpace(string(prefix)) != "#EXTM3U" {
		if s.relay && parts[1] == "input" {
			defer m.Stop(s.id, s.owner)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(flushWriter{w}, reader)
		return
	}
	body, err := io.ReadAll(io.LimitReader(reader, 2<<20+1))
	if err != nil || len(body) > 2<<20 {
		http.Error(w, "source unavailable", 502)
		return
	}
	base := response.Request.URL
	rewrite := func(ref string) string {
		u, err := url.Parse(strings.TrimSpace(ref))
		if err != nil {
			return ""
		}
		u = base.ResolveReference(u)
		if len(u.String()) > 8192 {
			return ""
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return ""
		}
		raw := u.String()
		sum := sha256.Sum256([]byte(raw))
		key := hex.EncodeToString(sum[:16])
		extension := strings.ToLower(path.Ext(u.Path))
		switch extension {
		case ".m3u8", ".ts", ".mp4", ".m4s", ".aac", ".mp3", ".key":
		default:
			extension = ".ts"
		}
		key += extension
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.sessions[s.id] != s {
			return ""
		}
		if len(s.resources) >= 4096 {
			for id, seen := range s.resourceSeen {
				if id != "input" && time.Since(seen) > 2*time.Minute {
					delete(s.resources, id)
					delete(s.resourceSeen, id)
				}
			}
			if len(s.resources) >= 4096 {
				return ""
			}
		}
		s.resources[key] = raw
		s.resourceSeen[key] = time.Now()
		if s.relay {
			return "media?session_id=" + s.id + "&resource=" + key
		}
		return "http://" + m.listener.Addr().String() + "/" + s.id + "/" + key
	}
	lines := strings.Split(string(body), "\n")
	failed := false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines[i] = rewrite(line)
			if lines[i] == "" {
				failed = true
			}
		} else {
			lines[i] = uriAttribute.ReplaceAllStringFunc(line, func(match string) string {
				ref := uriAttribute.FindStringSubmatch(match)[1]
				value := rewrite(ref)
				if value == "" {
					failed = true
				}
				return `URI="` + value + `"`
			})
		}
	}
	if failed {
		http.Error(w, "source playlist unavailable", 502)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	io.WriteString(w, strings.Join(lines, "\n"))
}

type flushWriter struct{ http.ResponseWriter }

func (w flushWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
