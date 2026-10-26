package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// MaxRestrictedRedirects bounds how many redirects a restricted client follows.
const MaxRestrictedRedirects = 3

// ErrBlockedAddress is returned when a restricted client refuses to connect to
// a loopback, private, link-local, unspecified, or multicast address.
var ErrBlockedAddress = errors.New("destination address is not allowed")

// ErrBlockedScheme is returned for any URL scheme other than http or https.
var ErrBlockedScheme = errors.New("only http and https URLs are allowed")

var thisNetwork = netip.MustParsePrefix("0.0.0.0/8")

// NewRestricted returns a client for URLs that come from untrusted upstream
// content, such as entries in a provider playlist. Connections to non-public
// addresses are rejected at dial time, after DNS resolution, so a hostname
// that rebinds to an internal address is refused too. Hosts in allowedHosts
// (the admin-configured source servers, which are usually on the LAN) skip
// the address check. Redirects are limited and re-checked the same way.
func NewRestricted(allowedHosts ...string) *http.Client {
	allowed := map[string]bool{}
	for _, host := range allowedHosts {
		if normalized := normalizeHost(host); normalized != "" {
			allowed[normalized] = true
		}
	}
	transport := &http.Transport{
		// No environment proxy: dialing a proxy would bypass the address check.
		Proxy:                 nil,
		DialContext:           restrictedDialContext(allowed),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   DefaultTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > MaxRestrictedRedirects {
				return fmt.Errorf("stopped after %d redirects", MaxRestrictedRedirects)
			}
			return CheckScheme(req.URL.Scheme)
		},
	}
}

// NewUntrusted is NewRestricted with no allowed hosts and a catalog-sized
// timeout, for providers a non-admin user configured: every address they
// name must be public.
func NewUntrusted(timeout time.Duration) *http.Client {
	client := NewRestricted()
	client.Timeout = timeout
	return client
}

// CheckScheme reports whether scheme may be fetched by a restricted client.
func CheckScheme(scheme string) error {
	switch strings.ToLower(scheme) {
	case "http", "https":
		return nil
	default:
		return ErrBlockedScheme
	}
}

// PublicAddress reports whether addr is a globally routable unicast address.
func PublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() || thisNetwork.Contains(addr) {
		return false
	}
	return addr != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func restrictedDialContext(allowed map[string]bool) func(context.Context, string, string) (net.Conn, error) {
	open := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: guardDial}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if allowed[normalizeHost(host)] {
			return open.DialContext(ctx, network, address)
		}
		return guarded.DialContext(ctx, network, address)
	}
}

// guardDial runs for every resolved address just before connect.
func guardDial(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !PublicAddress(addr) {
		return ErrBlockedAddress
	}
	return nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String()
	}
	return strings.ToLower(host)
}
