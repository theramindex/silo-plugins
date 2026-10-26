package rewind

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

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
