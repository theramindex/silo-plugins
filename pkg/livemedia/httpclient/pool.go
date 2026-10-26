package httpclient

import (
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Pool keeps a bounded set of dial-guarded transports warm across HLS segments.
// Different allowlists never share a client or inherit another source's access.
type Pool struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

func (p *Pool) For(hosts ...string) *http.Client {
	set := map[string]bool{}
	for _, h := range hosts {
		if h = normalizeHost(h); h != "" {
			set[h] = true
		}
	}
	keys := make([]string, 0, len(set))
	for h := range set {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	key := strings.Join(keys, "\n")
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.clients[key]; c != nil {
		return c
	}
	if p.clients == nil || len(p.clients) >= 32 {
		for _, c := range p.clients {
			c.CloseIdleConnections()
		}
		p.clients = map[string]*http.Client{}
	}
	c := NewRestricted(keys...)
	p.clients[key] = c
	return c
}
