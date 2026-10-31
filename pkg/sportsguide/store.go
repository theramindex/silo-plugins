package sportsguide

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/theramindex/silo-plugins/pkg/epg"
)

// Store persists only generated overlays; provider rows never enter this store.
// Empty paths use memory for callers without persistent plugin storage.
type Store struct {
	db       *epg.Store[Listing]
	mu       sync.RWMutex
	scope    string
	listings []Listing
}

func Open(path string) (*Store, error) {
	s := &Store{}
	if path != "" {
		var err error
		s.db, err = epg.Open(path, func(p Listing) epg.Fields {
			return epg.Fields{ID: p.ID, ChannelID: p.ChannelID, Start: p.Start, End: p.End}
		})
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
func (s *Store) Replace(ctx context.Context, scope string, listings []Listing) error {
	if s.db != nil {
		_, err := s.db.Replace(ctx, []byte(scope), listings)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scope = scope
	s.listings = cloneListings(listings)
	return nil
}
func cloneListings(listings []Listing) []Listing {
	out := append([]Listing(nil), listings...)
	for i := range out {
		out[i].Categories = append([]string(nil), out[i].Categories...)
	}
	return out
}
func (s *Store) Query(ctx context.Context, scope string, channels []Channel, start, end int64, now time.Time) ([]Listing, error) {
	ids := []string{}
	names := map[string]string{}
	for _, ch := range channels {
		ids = append(ids, ch.ID)
		names[ch.ID] = ch.Name
	}
	if len(ids) == 0 {
		return []Listing{}, nil
	}
	var listings []Listing
	if s.db != nil {
		for attempt := 0; attempt < 2; attempt++ {
			metadata, view, found, err := s.db.Load(ctx)
			if err != nil {
				return nil, err
			}
			if !found || string(metadata) != scope {
				return []Listing{}, nil
			}
			listings, err = view.Query(ctx, epg.Query{ChannelIDs: ids, Start: start, End: end})
			if errors.Is(err, epg.ErrChanged) {
				if attempt == 1 {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			break
		}
	} else {
		s.mu.RLock()
		if s.scope == scope {
			listings = cloneListings(s.listings)
		}
		s.mu.RUnlock()
	}
	out := []Listing{}
	for _, p := range listings {
		if name, ok := names[p.ChannelID]; !ok || name != p.Generated.ChannelName || p.Generated.Expires <= now.Unix() || p.Start >= end || p.End <= start {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// DecodePolicy accepts older settings that have no sports-guide policy yet.
func DecodePolicy(value any) Policy {
	p := DefaultPolicy()
	if value != nil {
		if data, err := json.Marshal(value); err == nil {
			_ = json.Unmarshal(data, &p)
		}
	}
	return Normalize(p)
}
