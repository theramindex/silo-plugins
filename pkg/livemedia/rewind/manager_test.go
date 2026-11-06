package rewind

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type rewindRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rewindRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type gatedRewindBody struct {
	started chan context.Context
	ctx     context.Context
	release <-chan struct{}
}

func (b *gatedRewindBody) Read([]byte) (int, error) {
	b.started <- b.ctx
	<-b.release
	return 0, io.EOF
}
func (*gatedRewindBody) Close() error { return nil }

func TestManagerClearCannotDeleteConcurrentAdmission(t *testing.T) {
	t.Parallel()
	stream := bytes.Join([][]byte{testPATPacket(), testPMTPacket(), testVideoPacket(0), testFillerPacket(), testVideoPacket(180000), testFillerPacket(), testVideoPacket(360000)}, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(stream)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	manager := NewManager(t.TempDir())
	defer manager.Close()
	config := Config{Enabled: true, MaxBytes: 32 << 20, MinFreeBytes: 1, MaxBuffers: 2}
	release := make(chan struct{})
	started := make(chan context.Context, 1)
	client := &http.Client{Transport: rewindRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &gatedRewindBody{started: started, ctx: r.Context(), release: release}, Header: http.Header{}}, nil
	})}
	if _, err := manager.Acquire("old", "http://old.invalid", config, client); err != nil {
		t.Fatal(err)
	}
	oldContext := <-started
	cleared := make(chan error, 1)
	go func() { cleared <- manager.Clear() }()
	<-oldContext.Done() // Clear is now waiting for the old ingest to finish.
	type result struct {
		lease Lease
		err   error
	}
	admitted := make(chan result, 1)
	go func() { lease, err := manager.Acquire("fresh", upstream.URL, config); admitted <- result{lease, err} }()
	var fresh result
	select {
	case fresh = <-admitted:
		// Baseline admits while Clear waits. Let that generation write real files.
		deadline := time.Now().Add(3 * time.Second)
		for {
			status, _ := manager.Status(fresh.lease.ID)
			if status.SegmentCount > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
	case <-time.After(100 * time.Millisecond):
		// Serialized admission waits until the previous generation is cleared.
	}
	close(release)
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	if fresh.lease.ID == "" {
		fresh = <-admitted
	}
	if fresh.err != nil {
		t.Fatal(fresh.err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := manager.Segment(fresh.lease.BufferID, fresh.lease.ID, 0); ok {
			break
		}
		status, _ := manager.Status(fresh.lease.ID)
		if status.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("successful admission has no readable segment after Clear: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagerSharesOneChannelBufferAcrossLeases(t *testing.T) {
	t.Parallel()
	stream := bytes.Join([][]byte{
		testPATPacket(), testPMTPacket(), testVideoPacket(0), testFillerPacket(),
		testVideoPacket(180000), testFillerPacket(), testVideoPacket(360000),
	}, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/mp2t")
		_, _ = w.Write(stream)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	manager := NewManager(t.TempDir())
	config := Config{Enabled: true, MaxBytes: 32 << 20, MinFreeBytes: 1, MaxBuffers: 2, Window: 30 * time.Minute}
	first, err := manager.Acquire("channel-1", upstream.URL, config)
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	second, err := manager.Acquire("channel-1", upstream.URL, config)
	if err != nil {
		t.Fatalf("acquire second lease: %v", err)
	}
	if first.BufferID != second.BufferID || first.ID == second.ID {
		t.Fatalf("expected shared buffer with unique leases: first=%+v second=%+v", first, second)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, ok := manager.Status(first.ID)
		if ok && status.SegmentCount >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("buffer did not become ready: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	manifest, ok := manager.Manifest(first.BufferID, first.ID)
	if !ok || !bytes.Contains(manifest, []byte("#EXT-X-MEDIA-SEQUENCE")) {
		t.Fatalf("expected HLS manifest, ok=%v body=%q", ok, manifest)
	}
	expectedSegmentRoute := []byte("segment?buffer_id=" + first.BufferID + "&sequence=0&lease=" + first.ID)
	if !bytes.Contains(manifest, expectedSegmentRoute) {
		t.Fatalf("expected manifest to use the declared segment route %q, body=%q", expectedSegmentRoute, manifest)
	}
	stats := manager.Stats()
	if stats.ActiveBuffers != 1 || stats.ActiveLeases != 2 {
		t.Fatalf("expected one buffer and two leases, got %+v", stats)
	}
	if err := manager.Clear(); err != nil {
		t.Fatalf("clear manager: %v", err)
	}
}

func TestManagerBudgetEvictionOfOwnBufferDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	stream := bytes.Join([][]byte{
		testPATPacket(), testPMTPacket(), testVideoPacket(0), testFillerPacket(),
		testVideoPacket(180000), testFillerPacket(), testVideoPacket(360000),
	}, nil)
	upstreamClosed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/mp2t")
		_, _ = w.Write(stream)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(upstreamClosed)
	}))
	defer upstream.Close()

	root := t.TempDir()
	manager := NewManager(root)
	// One byte of budget: the first segment written makes the ingest goroutine
	// evict its own buffer from inside writeSegment -> enforceBudget.
	config := Config{Enabled: true, MaxBytes: 1, MinFreeBytes: 1, MaxBuffers: 2, Window: 30 * time.Minute}
	lease, err := manager.Acquire("channel-1", upstream.URL, config)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	b := manager.bufferByID(lease.BufferID)
	if b == nil {
		t.Fatal("expected buffer to be registered")
	}

	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ingest goroutine deadlocked evicting its own buffer")
	}
	select {
	case <-upstreamClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream connection leaked after eviction")
	}
	if manager.bufferByID(lease.BufferID) != nil {
		t.Fatal("expected evicted buffer to be removed from the manager")
	}
	if stats := manager.Stats(); stats.Bytes != 0 || stats.ActiveBuffers != 0 {
		t.Fatalf("expected evicted bytes to be released once, got %+v", stats)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(b.dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("evicted buffer directory %s was not removed", b.dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := manager.Clear(); err != nil {
		t.Fatalf("clear manager: %v", err)
	}
}

func TestManagerReplacingBufferDoesNotBlockOrDeleteReplacement(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/mp2t")
		_, _ = w.Write(testPATPacket())
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	manager := NewManager(t.TempDir())
	config := Config{Enabled: true, MaxBytes: 32 << 20, MinFreeBytes: 1, MaxBuffers: 2, Window: 30 * time.Minute}
	first, err := manager.Acquire("channel-1", upstream.URL+"/a", config)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	old := manager.bufferByID(first.BufferID)
	done := make(chan Lease, 1)
	go func() {
		lease, _ := manager.Acquire("channel-1", upstream.URL+"/b", config)
		done <- lease
	}()
	var second Lease
	select {
	case second = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire blocked replacing a buffer")
	}
	select {
	case <-old.done:
	case <-time.After(5 * time.Second):
		t.Fatal("replaced buffer did not stop")
	}
	replacement := manager.bufferByID(second.BufferID)
	if replacement == nil || replacement == old || replacement.dir == old.dir {
		t.Fatalf("expected a replacement buffer with its own directory")
	}
	_ = manager.Clear()
}

func TestManagerRejectsDisabledAndOverCapacityBuffers(t *testing.T) {
	t.Parallel()
	manager := NewManager(t.TempDir())
	if _, err := manager.Acquire("channel", "http://example.invalid", Config{}); err == nil {
		t.Fatal("expected disabled manager to reject acquire")
	}
	config := Config{Enabled: true, MaxBuffers: 1, MaxBytes: 1 << 20, MinFreeBytes: 1}
	if _, err := manager.Acquire("one", "http://127.0.0.1:1", config); err != nil {
		t.Fatalf("first buffer should reserve capacity: %v", err)
	}
	if _, err := manager.Acquire("two", "http://127.0.0.1:1", config); err == nil {
		t.Fatal("expected distinct channel buffer limit")
	}
	_ = manager.Clear()
}
