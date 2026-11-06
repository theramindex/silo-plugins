package rewind

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestViewerClearSerializesAdmissionAndReleasesReservations(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer source.Close()
	manager := NewViewerManager(t.TempDir())
	defer manager.Close()
	config := Config{Enabled: true, MinFreeBytes: 1}
	releasing, proceed := make(chan struct{}), make(chan struct{})
	var oldReleases, freshReleases atomic.Int32
	_, err := manager.Acquire("old", source.URL, "viewer", config, http.DefaultClient, func() {
		oldReleases.Add(1)
		close(releasing)
		<-proceed
	})
	if err != nil {
		t.Fatal(err)
	}
	cleared := make(chan error, 1)
	go func() { cleared <- manager.Clear() }()
	<-releasing
	type result struct {
		lease Lease
		err   error
	}
	admitted := make(chan result, 1)
	go func() {
		lease, err := manager.Acquire("fresh", source.URL, "viewer", config, http.DefaultClient, func() { freshReleases.Add(1) })
		admitted <- result{lease, err}
	}()
	var fresh result
	select {
	case fresh = <-admitted:
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	if fresh.lease.ID == "" {
		fresh = <-admitted
	}
	if fresh.err != nil {
		t.Fatal(fresh.err)
	}
	if !manager.Heartbeat(fresh.lease.ID, "viewer") {
		t.Fatal("Clear orphaned a successful concurrent viewer admission")
	}
	manager.Release(fresh.lease.ID, "viewer")
	manager.Release(fresh.lease.ID, "viewer")
	if oldReleases.Load() != 1 || freshReleases.Load() != 1 {
		t.Fatalf("reservation releases: old=%d fresh=%d", oldReleases.Load(), freshReleases.Load())
	}
}

func TestViewerLeaseCannotBeAdoptedAndReleasesReservation(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer source.Close()
	manager := NewViewerManager(t.TempDir())
	defer manager.Close()
	var releases atomic.Int32
	lease, err := manager.Acquire("one", source.URL, "viewer", Config{Enabled: true, MinFreeBytes: 1}, http.DefaultClient, func() { releases.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if manager.Heartbeat(lease.ID, "other") {
		t.Fatal("other viewer extended lease")
	}
	if _, ok := manager.Status(lease.ID, "other"); ok {
		t.Fatal("other viewer read status")
	}
	if _, ok := manager.Manifest(lease.BufferID, lease.ID, "other"); ok {
		t.Fatal("other viewer read manifest")
	}
	manager.Release(lease.ID, "other")
	if releases.Load() != 0 {
		t.Fatal("other viewer released reservation")
	}
	if !manager.Heartbeat(lease.ID, "viewer") {
		t.Fatal("owner heartbeat failed")
	}
	manager.Release(lease.ID, "viewer")
	manager.Release(lease.ID, "viewer")
	if releases.Load() != 1 {
		t.Fatal("reservation cleanup was not exactly once")
	}
	if _, ok := manager.Status(lease.ID, "viewer"); ok {
		t.Fatal("stopped lease survived")
	}
}
