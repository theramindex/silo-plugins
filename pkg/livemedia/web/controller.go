// Package web adapts shared media sessions to the Silo plugin route contract.
package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/theramindex/silo-plugins/pkg/livemedia/rewind"
	"github.com/theramindex/silo-plugins/pkg/livemedia/transcode"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed media.js
var ClientScript []byte

type Input struct {
	Container string
	URL       string
	Client    *http.Client
	Release   func()
	Heartbeat func() error
	Fallback  func(context.Context) (string, *http.Client, error)
}
type Resolver func(context.Context, string, string, string) (Input, error)
type Config struct {
	Prefix, Root  string
	Resolve       Resolver
	RewindAllowed func() bool
	RewindOptions func() rewind.Config
	RewindManager *rewind.ViewerManager
}
type record struct {
	rewinding               bool
	owner, mode, id, buffer string
	reservation             string
	input                   Input
	seen                    time.Time
}
type Controller struct {
	config     Config
	gpu, relay *transcode.Manager
	rewind     *rewind.ViewerManager
	mu         sync.Mutex
	records    map[string]*record
	stop       chan struct{}
	once       sync.Once
}

func New(config Config) *Controller {
	buffers := config.RewindManager
	if buffers == nil {
		buffers = rewind.NewViewerManager(filepath.Join(config.Root, "rewind"))
	}
	c := &Controller{config: config, gpu: transcode.New(transcode.Config{Root: filepath.Join(config.Root, "converted")}), relay: transcode.New(transcode.Config{MaxSessions: 64, MaxPerViewer: 4}), rewind: buffers, records: map[string]*record{}, stop: make(chan struct{})}

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				if time.Now().Unix()%5 == 0 {
					c.enforceCache()
				}
				c.mu.Lock()
				var stale []*record
				for id, r := range c.records {
					if time.Since(r.seen) > 75*time.Second {
						stale = append(stale, r)
						delete(c.records, id)
					}
				}
				c.mu.Unlock()
				for _, r := range stale {
					c.release(r)
				}
			}
		}
	}()
	return c
}
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stop)
		c.mu.Lock()
		all := c.records
		c.records = map[string]*record{}
		c.mu.Unlock()
		for _, r := range all {
			c.release(r)
		}
		c.gpu.Close()
		c.relay.Close()
		c.rewind.Close()
	})
}
func Owner(r *pluginv1.HandleHTTPRequest) string {
	uid, profile := "", ""
	for k, v := range r.GetHeaders() {
		if strings.EqualFold(k, "X-Silo-User-Id") {
			uid = v
		}
		if strings.EqualFold(k, "X-Silo-Profile-Id") {
			profile = v
		}
	}
	id, err := strconv.Atoi(uid)
	if err != nil || id <= 0 {
		return ""
	}
	return strconv.Itoa(id) + ":" + profile
}
func query(r *pluginv1.HandleHTTPRequest, key string) string {
	v := r.GetQuery().AsMap()[key]
	if a, ok := v.([]any); ok && len(a) > 0 {
		v = a[0]
	}
	s, _ := v.(string)
	return s
}
func jsonResponse(code int, value any) *pluginv1.HandleHTTPResponse {
	data, _ := json.Marshal(value)
	return &pluginv1.HandleHTTPResponse{StatusCode: int32(code), Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: data}
}
func failure(code int, message string) *pluginv1.HandleHTTPResponse {
	return jsonResponse(code, map[string]string{"error": message})
}
func Handles(prefix, path string) bool {
	return strings.HasPrefix(path, prefix+"/api/media/") || strings.HasPrefix(path, prefix+"/relay/") || strings.HasPrefix(path, prefix+"/converted/") || strings.HasPrefix(path, prefix+"/rewind/") || path == prefix+"/assets/media.js"
}
func (c *Controller) Handles(path string) bool { return Handles(c.config.Prefix, path) }

func (c *Controller) find(id, owner string) *record {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.records[id]
	if r == nil || r.owner != owner || time.Since(r.seen) > 75*time.Second {
		return nil
	}
	r.seen = time.Now()
	return r
}
func (c *Controller) release(r *record) {
	switch r.mode {
	case "direct":
		c.relay.Stop(r.id, r.owner)
	case "rewind":
		c.rewind.Release(r.id, r.owner)
	default:
		c.gpu.Stop(r.id, r.owner)
	}
	if r.input.Release != nil {
		r.input.Release()
	}
}
func (c *Controller) Handle(ctx context.Context, r *pluginv1.HandleHTTPRequest) *pluginv1.HandleHTTPResponse {
	owner := Owner(r)
	if owner == "" {
		return failure(401, "Sign in to Silo to play this channel.")
	}
	path := strings.TrimPrefix(r.GetPath(), c.config.Prefix)
	if path == "/assets/media.js" && r.GetMethod() == "GET" {
		return &pluginv1.HandleHTTPResponse{StatusCode: 200, Headers: map[string]string{"Content-Type": "application/javascript", "Cache-Control": "no-cache"}, Body: ClientScript}
	}
	if path == "/api/media/start" {
		if r.GetMethod() != "POST" {
			return failure(405, "Method not allowed")
		}
		var p struct {
			Channel     string `json:"channelId"`
			Mode        string `json:"mode"`
			Rewind      bool   `json:"rewind"`
			Reservation string `json:"reservationId"`
		}
		if len(r.GetBody()) > 4096 || json.Unmarshal(r.GetBody(), &p) != nil || p.Channel == "" {
			return failure(400, "Choose a channel.")
		}
		if p.Mode == "" {
			p.Mode = "direct"
		}
		if p.Mode != "direct" && p.Mode != "compatible" && p.Mode != "rewind" {
			return failure(400, "Unknown playback mode.")
		}
		if (p.Mode == "rewind" || p.Rewind) && (!c.rewindConfig().Enabled || c.config.RewindAllowed != nil && !c.config.RewindAllowed()) {
			return failure(409, "Live Rewind is unavailable for this source.")
		}
		token := make([]byte, 18)
		if _, err := rand.Read(token); err != nil {
			return failure(503, "Playback is unavailable.")
		}
		inputID := "media-" + hex.EncodeToString(token)
		input, err := c.config.Resolve(context.WithValue(ctx, reservationKey{}, p.Reservation), p.Channel, owner, inputID)
		if err != nil {
			return failure(409, "No available source could start this channel.")
		}
		var cleanup sync.Once
		release := input.Release
		input.Release = func() {
			cleanup.Do(func() {
				if release != nil {
					release()
				}
			})
		}
		rec := &record{owner: owner, mode: p.Mode, reservation: p.Reservation, rewinding: p.Mode == "rewind" || p.Rewind, input: input, seen: time.Now()}
		state, encoder := "starting", "copy"
		media := ""
		switch p.Mode {
		case "direct":
			var session transcode.Status
			session, err = c.relay.StartRelay(owner, p.Channel, input.URL, input.Client, transcode.RelayOptions{OnStop: input.Release, Fallback: input.Fallback})
			rec.id = session.ID
			state = session.State
			media = c.config.Prefix + "/relay/media?session_id=" + rec.id + "&resource=input"
		case "compatible":
			var session transcode.Status
			session, err = c.gpu.StartWithOptions(owner, p.Channel, input.URL, input.Client, transcode.Options{Rewind: p.Rewind, RewindSegments: c.rewindSegments(), MinFreeBytes: c.rewindConfig().MinFreeBytes, OnStop: input.Release, Fallback: input.Fallback})
			rec.id = session.ID
			state = session.State
			encoder = session.Encoder
			media = c.config.Prefix + "/converted/media?session_id=" + rec.id + "&name=index.m3u8"
		case "rewind":
			var session rewind.Lease
			session, err = c.rewind.Acquire(p.Channel+":"+inputID, input.URL, owner, c.copyRewindConfig(), input.Client, input.Release)
			rec.id = session.ID
			rec.buffer = session.BufferID
			media = c.config.Prefix + "/rewind/manifest?session_id=" + rec.id + "&buffer_id=" + rec.buffer
		}
		if err != nil {
			input.Release()
			code := 503
			if errors.Is(err, transcode.ErrCapacity) {
				code = 429
			}
			return failure(code, "Playback resources are busy or unavailable. Try again shortly.")
		}
		c.mu.Lock()
		c.records[rec.id] = rec
		c.mu.Unlock()
		return jsonResponse(202, map[string]any{"sessionId": rec.id, "state": state, "path": media, "encoder": encoder, "mode": p.Mode, "container": input.Container})
	}
	if path == "/api/media/admin-status" || path == "/api/media/clear" {
		admin := false
		for key, value := range r.GetHeaders() {
			if strings.EqualFold(key, "X-Silo-User-Role") && value == "admin" {
				admin = true
			}
		}
		if !admin {
			return failure(403, "Administrator access is required.")
		}
		if path == "/api/media/clear" {
			if r.GetMethod() != "POST" {
				return failure(405, "Method not allowed")
			}
			c.mu.Lock()
			var all []*record
			for id, record := range c.records {
				if record.mode != "direct" {
					all = append(all, record)
					delete(c.records, id)
				}
			}
			c.mu.Unlock()
			for _, record := range all {
				c.release(record)
			}
			c.rewind.Clear()
			return jsonResponse(200, map[string]bool{"ok": true})
		}
		if r.GetMethod() != "GET" {
			return failure(405, "Method not allowed")
		}
		stats := c.rewind.Stats()
		gpu, bytes := c.gpu.Stats()
		return jsonResponse(200, map[string]any{"bytes": stats.Bytes + bytes, "maxBytes": c.rewindConfig().MaxBytes, "activeBuffers": stats.ActiveBuffers + gpu, "activeLeases": stats.ActiveLeases + gpu, "gpuSlots": 2})
	}
	id := query(r, "session_id")
	if path == "/api/media/stop" && r.GetMethod() == "POST" {
		var p struct {
			ID string `json:"sessionId"`
		}
		if json.Unmarshal(r.GetBody(), &p) == nil && p.ID != "" {
			id = p.ID
		}
		rec := c.find(id, owner)
		if rec != nil {
			c.mu.Lock()
			delete(c.records, id)
			c.mu.Unlock()
			c.release(rec)
		}
		return jsonResponse(200, map[string]bool{"ok": true})
	}
	rec := c.find(id, owner)
	if rec == nil {
		return failure(404, "Playback session not found.")
	}
	if path == "/api/media/status" && r.GetMethod() == "GET" {
		if rec.input.Heartbeat != nil {
			if err := rec.input.Heartbeat(); err != nil {
				c.release(rec)
				return failure(409, "The source reservation expired.")
			}
		}
		if rec.mode == "rewind" {
			c.rewind.Heartbeat(id, owner)
			status, ok := c.rewind.Status(id, owner)
			if !ok {
				return failure(404, "Rewind session not found.")
			}
			if status.SegmentCount > 0 && status.State == "buffering" {
				status.State = "ready"
			}
			return jsonResponse(200, status)
		}
		manager := c.gpu
		if rec.mode == "direct" {
			manager = c.relay
		}
		status, err := manager.Status(id, owner)
		if err != nil {
			return failure(404, "Playback session not found.")
		}
		return jsonResponse(200, status)
	}
	if r.GetMethod() != "GET" {
		return failure(405, "Method not allowed")
	}
	if path == "/relay/media" && rec.mode == "direct" {
		target, err := c.relay.RelayURL(id, owner, query(r, "resource"))
		if err != nil {
			return failure(404, "Media not found.")
		}
		return &pluginv1.HandleHTTPResponse{StatusCode: 200, Headers: map[string]string{"X-Silo-Relay-URL": target}}
	}
	if path == "/converted/media" && rec.mode == "compatible" {
		name := query(r, "name")
		data, err := c.gpu.Media(id, owner, name)
		if err != nil {
			return failure(404, "Media not found.")
		}
		if name == "index.m3u8" {
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if line != "" && !strings.HasPrefix(line, "#") {
					lines[i] = "media?session_id=" + id + "&name=" + url.QueryEscape(line)
				}
			}
			return mediaResponse([]byte(strings.Join(lines, "\n")), true)
		}
		return mediaResponse(data, false)
	}
	if rec.mode == "rewind" {
		if path == "/rewind/manifest" {
			data, ok := c.rewind.Manifest(rec.buffer, id, owner)
			if !ok {
				return failure(425, "Rewind is preparing.")
			}
			data = []byte(strings.ReplaceAll(string(data), "&lease="+url.QueryEscape(id), "&session_id="+url.QueryEscape(id)))
			return mediaResponse(data, true)
		}
		if path == "/rewind/segment" {
			seq, err := strconv.ParseInt(query(r, "sequence"), 10, 64)
			if err != nil {
				return failure(400, "Invalid segment.")
			}
			data, ok := c.rewind.Segment(rec.buffer, id, owner, seq)
			if !ok {
				return failure(404, "Segment not found.")
			}
			return mediaResponse(data, false)
		}
	}
	return failure(404, "Media not found.")
}
func mediaResponse(data []byte, playlist bool) *pluginv1.HandleHTTPResponse {
	kind := "video/mp2t"
	if playlist {
		kind = "application/vnd.apple.mpegurl"
	}
	return &pluginv1.HandleHTTPResponse{StatusCode: 200, Headers: map[string]string{"Content-Type": kind, "Cache-Control": "no-store"}, Body: data}
}

type reservationKey struct{}

func Reservation(ctx context.Context) string {
	value, _ := ctx.Value(reservationKey{}).(string)
	return value
}
func (c *Controller) StopReservation(owner, id string) {
	if owner == "" || id == "" {
		return
	}
	c.mu.Lock()
	var all []*record
	for key, r := range c.records {
		if r.owner == owner && r.reservation == id {
			all = append(all, r)
			delete(c.records, key)
		}
	}
	c.mu.Unlock()
	for _, r := range all {
		c.release(r)
	}
}

func (c *Controller) rewindConfig() rewind.Config {
	cfg := rewind.Config{Enabled: true}
	if c.config.RewindOptions != nil {
		cfg = c.config.RewindOptions()
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 5 << 30
	}
	if cfg.MinFreeBytes <= 0 {
		cfg.MinFreeBytes = 2 << 30
	}
	if cfg.MaxBuffers <= 0 {
		cfg.MaxBuffers = 20
	}
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Minute
	}
	return cfg
}
func (c *Controller) copyRewindConfig() rewind.Config {
	cfg := c.rewindConfig()
	_, bytes := c.gpu.Stats()
	cfg.MaxBytes -= bytes
	if cfg.MaxBytes < 3<<20 {
		cfg.Enabled = false
		cfg.MaxBytes = 3 << 20
	}
	return cfg
}
func (c *Controller) rewindSegments() int {
	cfg := c.rewindConfig()
	n := int(cfg.Window / (2 * time.Second))
	budget := int((cfg.MaxBytes - (16 << 20)) / (2 * 2 * 900000))
	if n > budget {
		n = budget
	}
	if n < 6 {
		n = 6
	}
	if n > 900 {
		n = 900
	}
	return n
}
func (c *Controller) enforceCache() {
	cfg := c.copyRewindConfig()
	c.rewind.SetConfig(cfg)
	if !c.rewindConfig().Enabled {
		c.mu.Lock()
		var all []*record
		for id, r := range c.records {
			if r.rewinding {
				all = append(all, r)
				delete(c.records, id)
			}
		}
		c.mu.Unlock()
		for _, r := range all {
			c.release(r)
		}
	}
}
