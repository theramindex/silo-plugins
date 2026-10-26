package httpclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicAddressRejectsInternalRanges(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"8.8.8.8":            true,
		"2606:4700::1111":    true,
		"127.0.0.1":          false,
		"10.1.2.3":           false,
		"172.16.0.1":         false,
		"192.168.1.10":       false,
		"169.254.169.254":    false,
		"0.0.0.0":            false,
		"0.1.2.3":            false,
		"224.0.0.1":          false,
		"255.255.255.255":    false,
		"::1":                false,
		"::":                 false,
		"fc00::1":            false,
		"fd12:3456::1":       false,
		"fe80::1":            false,
		"ff02::1":            false,
		"::ffff:127.0.0.1":   false,
		"::ffff:192.168.0.1": false,
	}
	for raw, want := range tests {
		if got := PublicAddress(netip.MustParseAddr(raw)); got != want {
			t.Errorf("PublicAddress(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestRestrictedClientAllowsConfiguredPrivateHost(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)

	response, err := NewRestricted(parsed.Hostname()).Get(server.URL)
	if err != nil {
		t.Fatalf("expected configured loopback host to be allowed: %v", err)
	}
	_ = response.Body.Close()
}

func TestRestrictedClientRejectsUnconfiguredPrivateHost(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)

	_, err := NewRestricted("provider.example.com").Get(server.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected blocked address error, got %v", err)
	}
	// localhost resolves to loopback at dial time, which must be refused too.
	_, err = NewRestricted().Get(strings.Replace(server.URL, "127.0.0.1", "localhost", 1))
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected resolved loopback to be blocked, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("blocked requests reached the server %d times", hits.Load())
	}
}

func TestRestrictedClientChecksRedirects(t *testing.T) {
	t.Parallel()
	loops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	t.Cleanup(loops.Close)
	parsed, _ := url.Parse(loops.URL)
	if _, err := NewRestricted(parsed.Hostname()).Get(loops.URL); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("expected redirect limit error, got %v", err)
	}

	toFile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(toFile.Close)
	parsed, _ = url.Parse(toFile.URL)
	if _, err := NewRestricted(parsed.Hostname()).Get(toFile.URL); !errors.Is(err, ErrBlockedScheme) {
		t.Fatalf("expected scheme error on redirect, got %v", err)
	}

	// An allowed host redirecting to a non-allowed internal address is refused.
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(internal.Close)
	bounce := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(internal.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	t.Cleanup(bounce.Close)
	parsed, _ = url.Parse(bounce.URL)
	if _, err := NewRestricted(parsed.Hostname()).Get(bounce.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected redirect target to be blocked, got %v", err)
	}
}
