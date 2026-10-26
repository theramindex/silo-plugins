package web

import (
	"context"
	"encoding/json"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAuthenticatedRelayResourcesStayUnderInstallationAndReleaseSource(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.m3u8" {
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment.ts?password=private\n")
		} else {
			io.WriteString(w, "video")
		}
	}))
	defer source.Close()
	var released atomic.Int32
	c := New(Config{Prefix: "/test", Root: t.TempDir(), Resolve: func(ctx context.Context, ch, owner, id string) (Input, error) {
		if ch != "channel" || owner != "1:profile" || !strings.HasPrefix(id, "media-") {
			t.Fatal("invalid resolver identity")
		}
		return Input{URL: source.URL + "/index.m3u8", Container: "hls", Client: http.DefaultClient, Release: func() { released.Add(1) }}, nil
	}})
	defer c.Close()
	call := func(user, path, method, body string) *pluginv1.HandleHTTPResponse {
		t.Helper()
		u, _ := url.Parse(path)
		values := map[string]any{}
		for key := range u.Query() {
			values[key] = u.Query().Get(key)
		}
		query, _ := structpb.NewStruct(values)
		return c.Handle(context.Background(), &pluginv1.HandleHTTPRequest{Path: u.Path, Method: method, Body: []byte(body), Query: query, Headers: map[string]string{"X-Silo-User-Id": user, "X-Silo-Profile-Id": "profile"}})
	}
	if r := call("", "/test/api/media/start", "POST", `{"channelId":"channel"}`); r.GetStatusCode() != 401 {
		t.Fatal("anonymous start was accepted")
	}
	r := call("1", "/test/api/media/start", "POST", `{"channelId":"channel"}`)
	if r.GetStatusCode() != 202 {
		t.Fatalf("start: %s", r.GetBody())
	}
	var session struct {
		ID        string `json:"sessionId"`
		Path      string `json:"path"`
		Container string `json:"container"`
	}
	json.Unmarshal(r.GetBody(), &session)
	if session.Container != "hls" {
		t.Fatal("source container was lost")
	}
	if r = call("2", session.Path, "GET", ""); r.GetStatusCode() != 404 {
		t.Fatal("other viewer adopted media")
	}
	r = call("1", session.Path, "GET", "")
	response, err := http.Get(r.Headers["X-Silo-Relay-URL"])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(body), "private") || strings.Contains(string(body), source.URL) {
		t.Fatal("source details escaped relay")
	}
	base, _ := url.Parse("https://silo.example/api/v2/plugin-content/plugins/8" + session.Path)
	var asset *url.URL
	for _, line := range strings.Split(string(body), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			ref, _ := url.Parse(line)
			asset = base.ResolveReference(ref)
		}
	}
	if asset == nil || asset.Path != base.Path {
		t.Fatalf("asset escaped installation route: %v", asset)
	}
	relative := strings.TrimPrefix(asset.Path, "/api/v2/plugin-content/plugins/8") + "?" + asset.RawQuery
	r = call("1", relative, "GET", "")
	response, err = http.Get(r.Headers["X-Silo-Relay-URL"])
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "video" {
		t.Fatalf("asset not relayed: %s", body)
	}
	stop := `{"sessionId":"` + session.ID + `"}`
	call("2", "/test/api/media/stop", "POST", stop)
	if released.Load() != 0 {
		t.Fatal("other viewer stopped source")
	}
	call("1", "/test/api/media/stop", "POST", stop)
	call("1", "/test/api/media/stop", "POST", stop)
	if released.Load() != 1 {
		t.Fatalf("source release count: %d", released.Load())
	}
	if r = call("1", session.Path, "GET", ""); r.GetStatusCode() != 404 {
		t.Fatal("stopped media remained accessible")
	}
}

func TestDisabledRewindDoesNotReserveSourceAndCacheRequiresAdmin(t *testing.T) {
	var resolved atomic.Int32
	c := New(Config{Root: t.TempDir(), Prefix: "/test", RewindAllowed: func() bool { return false }, Resolve: func(context.Context, string, string, string) (Input, error) { resolved.Add(1); return Input{}, nil }})
	defer c.Close()
	for _, body := range []string{`{"channelId":"one","mode":"rewind"}`, `{"channelId":"one","mode":"compatible","rewind":true}`} {
		r := c.Handle(context.Background(), &pluginv1.HandleHTTPRequest{Method: "POST", Path: "/test/api/media/start", Body: []byte(body), Headers: map[string]string{"X-Silo-User-Id": "1"}})
		if r.StatusCode != 409 || resolved.Load() != 0 {
			t.Fatalf("disabled rewind reserved a source: %d, %d", r.StatusCode, resolved.Load())
		}
	}
	for _, path := range []string{"admin-status", "clear"} {
		r := c.Handle(context.Background(), &pluginv1.HandleHTTPRequest{Method: "POST", Path: "/test/api/media/" + path, Headers: map[string]string{"X-Silo-User-Id": "1"}})
		if r.StatusCode != 403 {
			t.Fatalf("ordinary viewer used cache administration: %s %d", path, r.StatusCode)
		}
	}
}
