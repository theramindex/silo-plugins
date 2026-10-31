package sportsguide

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateRequiresAnUnambiguousIndependentScheduleMatch(t *testing.T) {
	now := time.Unix(1800000000, 0)
	event := Event{ID: "fixture-1", Provider: "ESPN", LeagueID: "nba", LeagueName: "NBA", Name: "Boston Celtics at New York Knicks", Home: "New York Knicks", Away: "Boston Celtics", Start: now.Add(time.Hour).Unix()}
	channel := Channel{ID: "event-feed", Name: "NBA 04 | Boston Celtics @ New York Knicks", GroupID: "nba-group"}
	policy := DefaultPolicy()
	for _, tc := range []struct {
		name     string
		channels []Channel
		events   []Event
		policy   Policy
		want     int
	}{
		{"both teams", []Channel{channel}, []Event{event}, policy, 3},
		{"one team is insufficient", []Channel{{ID: "team", Name: "New York Knicks", GroupID: "nba-group"}}, []Event{event}, policy, 0},
		{"ambiguous rematch", []Channel{channel}, []Event{event, {ID: "fixture-2", Provider: "ESPN", LeagueID: "nba", LeagueName: "NBA", Home: event.Home, Away: event.Away, Name: event.Name, Start: now.Add(25 * time.Hour).Unix()}}, policy, 0},
		{"disabled", []Channel{channel}, []Event{event}, Policy{}, 0},
		{"EPG derived event", []Channel{channel}, []Event{{ID: "epg:copy", Provider: "EPG fallback", LeagueID: "nba", Home: event.Home, Away: event.Away, Start: event.Start}}, policy, 0},
		{"canceled event", []Channel{channel}, []Event{{ID: "cancel", Provider: "ESPN", LeagueID: "nba", Status: "STATUS_CANCELED", Home: event.Home, Away: event.Away, Start: event.Start}}, policy, 0},
		{"wrong league hint", []Channel{channel}, []Event{event}, Policy{Enabled: true, Groups: []GroupRule{{GroupID: "nba-group", LeagueID: "wnba"}}}, 0},
		{"manual link", []Channel{{ID: "generic", Name: "Event 01"}}, []Event{event}, Policy{Enabled: true, Overrides: []Override{{ChannelID: "generic", EventID: event.ID}}}, 3},
		{"missing manual event never guesses", []Channel{channel}, []Event{event}, Policy{Enabled: true, Overrides: []Override{{ChannelID: channel.ID, EventID: "missing"}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Generate(now, tc.channels, tc.events, tc.policy)
			if len(got) != tc.want {
				t.Fatalf("listings = %+v, want %d", got, tc.want)
			}
			if len(got) > 0 && (!got[1].Generated.Estimated || got[1].Generated.EventID != event.ID || got[1].Generated.Provider != "ESPN" || got[1].Generated.Phase != "game") {
				t.Fatalf("missing provenance: %+v", got[1])
			}
		})
	}
}

func TestSessionMatchingDoesNotInventAPlayerGroupOrRound(t *testing.T) {
	now := time.Unix(1800000000, 0)
	policy := Policy{Enabled: true, Groups: []GroupRule{{GroupID: "golf", LeagueID: "pga", Mode: "session"}}}
	events := []Event{
		{ID: "main", Provider: "Sportarr", LeagueID: "pga", Name: "Wyndham Championship Round 2 Main Feed", Start: now.Add(time.Hour).Unix()},
		{ID: "featured-a", Provider: "Sportarr", LeagueID: "pga", Name: "Wyndham Championship Round 2 Featured Groups Scheffler", Start: now.Add(time.Hour).Unix()},
		{ID: "featured-b", Provider: "Sportarr", LeagueID: "pga", Name: "Wyndham Championship Round 2 Featured Groups McIlroy", Start: now.Add(time.Hour).Unix()},
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"Wyndham Championship Round 2 Main Feed", 3},
		{"Wyndham Championship Round 3 Main Feed", 0},
		{"FedEx Championship Round 2 Main Feed", 0},
		{"Wyndham Championship Round 2 Featured Groups", 0},
	} {
		got := Generate(now, []Channel{{ID: "g", GroupID: "golf", Name: tc.name}}, events, policy)
		if len(got) != tc.want {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
}

func TestOverlayRestoresScopedListingsAndFillsOnlyProviderGaps(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sports.sqlite")
	now := time.Unix(1800000000, 0)
	channel := Channel{ID: "a", Name: "A"}
	generated := []Listing{{ID: "g", ChannelID: "a", Title: "Game", Start: 100, End: 400, Generated: Provenance{EventID: "one", Provider: "ESPN", ChannelName: "A", Expires: now.Add(time.Hour).Unix()}}}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Replace(ctx, "source-a", generated); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Query(ctx, "source-a", []Channel{channel}, 0, 500, now)
	if err != nil || len(got) != 1 {
		t.Fatalf("restart: %+v %v", got, err)
	}
	gaps := FillGaps(got, []Span{{ChannelID: "a", Start: 150, End: 250}})
	if len(gaps) != 2 || gaps[0].Start != 100 || gaps[0].End != 150 || gaps[1].Start != 250 || gaps[1].End != 400 || gaps[0].ID == gaps[1].ID {
		t.Fatalf("provider overlap: %+v", gaps)
	}
	for _, scope := range []string{"source-b", "source-a"} {
		channels := []Channel{channel}
		if scope == "source-a" {
			channels[0].Name = "Another event"
		}
		stale, err := store.Query(ctx, scope, channels, 0, 500, now)
		if err != nil || len(stale) != 0 {
			t.Fatalf("stale source/channel: %+v %v", stale, err)
		}
	}
	expired, err := store.Query(ctx, "source-a", []Channel{channel}, 0, 500, now.Add(2*time.Hour))
	if err != nil || len(expired) != 0 {
		t.Fatalf("expired listings: %+v %v", expired, err)
	}
}
