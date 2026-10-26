package web

import (
	"github.com/theramindex/silo-plugins/pkg/livemedia/rewind"
	"sync"
	"time"
)

// SourceBackoff temporarily prefers explicit alternatives after startup failure.
type SourceBackoff struct {
	mu     sync.Mutex
	failed map[string]time.Time
}

func (b *SourceBackoff) Reject(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed == nil {
		b.failed = map[string]time.Time{}
	}
	b.failed[id] = time.Now().Add(15 * time.Second)
}
func (b *SourceBackoff) Order(ids []string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for id, until := range b.failed {
		if !now.Before(until) {
			delete(b.failed, id)
		}
	}
	healthy, failed := []string{}, []string{}
	for _, id := range ids {
		if now.Before(b.failed[id]) {
			failed = append(failed, id)
		} else {
			healthy = append(healthy, id)
		}
	}
	return append(healthy, failed...)
}
func RewindSettings(settings map[string]any) rewind.Config {
	number := func(key string, fallback float64) float64 {
		switch n := settings[key].(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
		return fallback
	}
	enabled, _ := settings["liveRewindEnabled"].(bool)
	return rewind.Config{Enabled: enabled, MaxBytes: int64(number("liveRewindCacheGB", 5) * float64(1<<30)), MinFreeBytes: int64(number("liveRewindMinFreeGB", 2) * float64(1<<30)), MaxBuffers: int(number("liveRewindMaxChannels", 20)), Window: time.Duration(number("liveRewindWindowMinutes", 30)) * time.Minute}
}
