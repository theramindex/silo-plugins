package rewind

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

var ErrViewer = errors.New("rewind session not found")

type viewerLease struct {
	owner   string
	expires time.Time
	ttl     time.Duration
	release func()
}

// ViewerManager binds buffer capabilities to the authenticated viewer and
// retires upstream reservations when viewers stop or their heartbeats expire.
type ViewerManager struct {
	buffers *Manager
	mu      sync.Mutex
	leases  map[string]viewerLease
	stop    chan struct{}
	once    sync.Once
}

func NewViewerManager(root string) *ViewerManager {
	m := &ViewerManager{buffers: NewManager(root), leases: map[string]viewerLease{}, stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				m.expire()
			}
		}
	}()
	return m
}
func (m *ViewerManager) SetConfig(config Config) { m.buffers.SetConfig(config) }
func (m *ViewerManager) Stats() Stats            { return m.buffers.Stats() }
func (m *ViewerManager) Acquire(channel, url, owner string, config Config, client *http.Client, release func()) (Lease, error) {
	if owner == "" || client == nil {
		return Lease{}, ErrViewer
	}
	lease, err := m.buffers.Acquire(channel, url, config, client)
	if err != nil {
		return Lease{}, err
	}
	ttl := config.normalized().LeaseTTL
	m.mu.Lock()
	m.leases[lease.ID] = viewerLease{owner: owner, expires: time.Now().Add(ttl), ttl: ttl, release: release}
	m.mu.Unlock()
	return lease, nil
}
func (m *ViewerManager) allowed(id, owner string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	return ok && owner != "" && l.owner == owner && time.Now().Before(l.expires)
}
func (m *ViewerManager) Heartbeat(id, owner string) bool {
	m.mu.Lock()
	l, ok := m.leases[id]
	if ok && l.owner == owner && time.Now().Before(l.expires) {
		l.expires = time.Now().Add(l.ttl)
		m.leases[id] = l
	} else {
		ok = false
	}
	m.mu.Unlock()
	return ok && m.buffers.Heartbeat(id)
}
func (m *ViewerManager) Release(id, owner string) {
	m.mu.Lock()
	l, ok := m.leases[id]
	if !ok || l.owner != owner {
		m.mu.Unlock()
		return
	}
	delete(m.leases, id)
	m.mu.Unlock()
	m.buffers.Release(id)
	if l.release != nil {
		l.release()
	}
}
func (m *ViewerManager) Status(id, owner string) (BufferStatus, bool) {
	if !m.allowed(id, owner) {
		return BufferStatus{}, false
	}
	return m.buffers.Status(id)
}
func (m *ViewerManager) Manifest(buffer, id, owner string) ([]byte, bool) {
	if !m.allowed(id, owner) {
		return nil, false
	}
	return m.buffers.Manifest(buffer, id)
}
func (m *ViewerManager) Segment(buffer, id, owner string, sequence int64) ([]byte, bool) {
	if !m.allowed(id, owner) {
		return nil, false
	}
	return m.buffers.Segment(buffer, id, sequence)
}
func (m *ViewerManager) expire() {
	m.mu.Lock()
	var expired []struct{ id, owner string }
	for id, l := range m.leases {
		if time.Now().After(l.expires) {
			expired = append(expired, struct{ id, owner string }{id, l.owner})
		}
	}
	m.mu.Unlock()
	for _, l := range expired {
		m.Release(l.id, l.owner)
	}
}
func (m *ViewerManager) Clear() error {
	m.mu.Lock()
	all := m.leases
	m.leases = map[string]viewerLease{}
	m.mu.Unlock()
	for id, l := range all {
		m.buffers.Release(id)
		if l.release != nil {
			l.release()
		}
	}
	return m.buffers.Clear()
}
func (m *ViewerManager) Close() { m.once.Do(func() { close(m.stop); m.Clear(); m.buffers.Close() }) }
