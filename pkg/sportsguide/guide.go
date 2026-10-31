// Package sportsguide creates explicitly attributed guide overlays from
// independent sports schedules. It never receives EPG titles as evidence.
package sportsguide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Channel struct{ ID, Name, GroupID, GroupName string }
type Event struct {
	ID, Provider, LeagueID, LeagueName, Name, Home, Away, HomeAbbr, AwayAbbr, ImageURL, Description, Status string
	Start, End                                                                                              int64
}
type GroupRule struct {
	GroupID  string `json:"groupId"`
	LeagueID string `json:"leagueId,omitempty"`
	Mode     string `json:"mode,omitempty"` // auto, matchup, session, off
}
type Override struct {
	ChannelID string `json:"channelId"`
	EventID   string `json:"eventId,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
}
type Policy struct {
	Enabled   bool        `json:"enabled"`
	Groups    []GroupRule `json:"groups"`
	Overrides []Override  `json:"overrides"`
}
type Provenance struct {
	Provider    string `json:"provider"`
	EventID     string `json:"eventId"`
	ChannelName string `json:"channelName"`
	Phase       string `json:"phase"`
	Evidence    string `json:"evidence"`
	Estimated   bool   `json:"estimated"`
	Created     int64  `json:"createdAtUnix"`
	Expires     int64  `json:"expiresAtUnix"`
}
type Listing struct {
	ID         string     `json:"id"`
	ChannelID  string     `json:"channelId"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary,omitempty"`
	ImageURL   string     `json:"imageUrl,omitempty"`
	Categories []string   `json:"categories,omitempty"`
	Start      int64      `json:"startUnix"`
	End        int64      `json:"endUnix"`
	Generated  Provenance `json:"generated"`
}
type Span struct {
	ChannelID  string
	Start, End int64
}

func DefaultPolicy() Policy {
	return Policy{Enabled: true, Groups: []GroupRule{}, Overrides: []Override{}}
}

// Normalize bounds administrator input and makes policy fingerprints stable.
func Normalize(p Policy) Policy {
	groups := map[string]GroupRule{}
	for _, g := range p.Groups {
		g.GroupID = strings.TrimSpace(g.GroupID)
		g.LeagueID = strings.ToLower(strings.TrimSpace(g.LeagueID))
		if g.GroupID == "" || len(g.GroupID) > 256 || len(g.LeagueID) > 64 {
			continue
		}
		switch g.Mode {
		case "matchup", "session", "off":
		default:
			g.Mode = "auto"
		}
		groups[g.GroupID] = g
		if len(groups) >= 500 {
			break
		}
	}
	p.Groups = []GroupRule{}
	for _, g := range groups {
		p.Groups = append(p.Groups, g)
	}
	sort.Slice(p.Groups, func(i, j int) bool { return p.Groups[i].GroupID < p.Groups[j].GroupID })
	overrides := map[string]Override{}
	for _, o := range p.Overrides {
		o.ChannelID = strings.TrimSpace(o.ChannelID)
		o.EventID = strings.TrimSpace(o.EventID)
		if o.ChannelID == "" || len(o.ChannelID) > 256 || len(o.EventID) > 512 || (o.EventID == "" && !o.Disabled) {
			continue
		}
		overrides[o.ChannelID] = o
		if len(overrides) >= 1000 {
			break
		}
	}
	p.Overrides = []Override{}
	for _, o := range overrides {
		p.Overrides = append(p.Overrides, o)
	}
	sort.Slice(p.Overrides, func(i, j int) bool { return p.Overrides[i].ChannelID < p.Overrides[j].ChannelID })
	return p
}
func Fingerprint(p Policy) string {
	b, _ := json.Marshal(Normalize(p))
	return digest(string(b))
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:16]) }
func words(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}
func contains(text, term string) bool {
	term = words(term)
	return term != "" && strings.Contains(" "+words(text)+" ", " "+term+" ")
}

func Generate(now time.Time, channels []Channel, events []Event, policy Policy) []Listing {
	policy = Normalize(policy)
	out := []Listing{}
	if !policy.Enabled {
		return out
	}
	rules := map[string]GroupRule{}
	overrides := map[string]Override{}
	for _, r := range policy.Groups {
		rules[r.GroupID] = r
	}
	for _, o := range policy.Overrides {
		overrides[o.ChannelID] = o
	}
	// Restrict candidates before looking at channel labels. Deduplicate the same
	// fixture, but never collapse two differently identified rematches.
	byLeague := map[string][]Event{}
	byID := map[string][]Event{}
	leagueNames := map[string]string{}
	seen := map[string]bool{}
	for _, e := range events {
		status := words(e.Status)
		if contains(status, "canceled") || contains(status, "cancelled") || contains(status, "postponed") || contains(status, "suspended") || contains(status, "tbd") {
			continue
		}
		if e.ID == "" || e.Provider == "" || strings.HasPrefix(strings.ToLower(e.ID), "epg:") || strings.Contains(strings.ToLower(e.Provider), "epg") || e.Start < now.Add(-20*time.Hour).Unix() || e.Start > now.Add(10*24*time.Hour).Unix() {
			continue
		}
		if seen[e.Provider+"|"+e.ID] {
			continue
		}
		seen[e.Provider+"|"+e.ID] = true
		byLeague[strings.ToLower(e.LeagueID)] = append(byLeague[strings.ToLower(e.LeagueID)], e)
		byID[e.ID] = append(byID[e.ID], e)
		leagueNames[strings.ToLower(e.LeagueID)] = e.LeagueName
	}
	for _, ch := range channels {
		rule := rules[ch.GroupID]
		if rule.Mode == "off" {
			continue
		}
		override, manual := overrides[ch.ID]
		if manual && override.Disabled {
			continue
		}
		candidates := []Event{}
		split := matchupSides(ch.Name)
		// Most lineups contain tens of thousands of ordinary TV channels. Do not
		// scan a sports schedule for every one of them, or scan unrelated leagues.
		context := ch.Name + " " + ch.GroupName
		possibleSession := rule.Mode == "session" || contains(context, "golf") || contains(context, "pga") || contains(context, "lpga") || contains(context, "nascar") || contains(context, "formula 1") || contains(context, "f1")
		if !manual && len(split) != 2 && !possibleSession {
			continue
		}
		eligible := []Event{}
		if manual {
			eligible = byID[override.EventID]
		} else if rule.LeagueID != "" {
			eligible = byLeague[rule.LeagueID]
		} else {
			for id, name := range leagueNames {
				if contains(context, id) || contains(context, name) {
					eligible = append(eligible, byLeague[id]...)
				}
			}
		}
		for _, e := range eligible {
			end := eventEnd(e)
			if end+3600 <= now.Unix() {
				continue
			}
			if manual {
				if e.ID == override.EventID {
					candidates = append(candidates, e)
				}
				continue
			}
			if rule.LeagueID != "" && rule.LeagueID != strings.ToLower(e.LeagueID) {
				continue
			}
			if !channelDateMatches(ch.Name, e.Start) {
				continue
			}
			mode := rule.Mode
			if mode == "" || mode == "auto" {
				mode = "matchup"
				if e.Home == "" || e.Away == "" {
					mode = "session"
				}
			}
			context := ch.Name + " " + ch.GroupName
			leagueKnown := rule.LeagueID != "" || contains(context, e.LeagueID) || contains(context, e.LeagueName)
			if !leagueKnown {
				continue
			}
			matched := false
			if mode == "matchup" {
				// A separator and both independently named sides are mandatory. A team
				// channel or group label naming one opponent cannot establish a game.
				if len(split) == 2 {
					side := func(text, name, abbr string) bool {
						return contains(text, name) || (len(words(abbr)) >= 2 && contains(text, abbr))
					}
					matched = (side(split[0], e.Away, e.AwayAbbr) && side(split[1], e.Home, e.HomeAbbr)) || (side(split[0], e.Home, e.HomeAbbr) && side(split[1], e.Away, e.AwayAbbr))
				}
			} else if mode == "session" {
				matched = sessionMatch(ch.Name, e.Name)
			}
			if matched {
				candidates = append(candidates, e)
			}
		}
		if len(candidates) != 1 {
			continue
		}
		e := candidates[0]
		end := eventEnd(e)
		title := strings.TrimSpace(e.Name)
		if title == "" {
			title = e.Away + " at " + e.Home
		}
		evidence := "channel-name"
		if manual {
			evidence = "manual"
		}
		phases := []struct {
			name, title string
			start, end  int64
		}{
			{"pregame", "Pregame: " + title, e.Start - 3600, e.Start},
			{"game", title, e.Start, end},
			{"postgame", "Postgame: " + title, end, end + 3600},
		}
		for _, phase := range phases {
			summary := fmt.Sprintf("Generated from %s schedule. %s", e.Provider, e.Description)
			estimated := true // Schedule windows do not prove what the stream actually carries.
			if estimated {
				summary += " Timing is estimated; the stream may carry different coverage."
			}
			out = append(out, Listing{ID: "sports-guide:" + digest(e.Provider+"|"+e.ID+"|"+ch.ID+"|"+phase.name), ChannelID: ch.ID, Title: phase.title, Summary: strings.TrimSpace(summary), ImageURL: e.ImageURL, Categories: []string{"Sports", e.LeagueName}, Start: phase.start, End: phase.end, Generated: Provenance{Provider: e.Provider, EventID: e.ID, ChannelName: ch.Name, Phase: phase.name, Evidence: evidence, Estimated: estimated, Created: now.Unix(), Expires: now.Add(24 * time.Hour).Unix()}})
		}
	}
	return out
}
func eventEnd(e Event) int64 {
	if e.End > e.Start && e.End-e.Start <= 24*3600 {
		return e.End
	}
	duration := int64(3 * 3600)
	switch strings.ToLower(e.LeagueID) {
	case "soccer", "epl", "mls", "nhl", "nba", "wnba":
		duration = 150 * 60
	case "nfl", "ncaaf", "mlb":
		duration = 4 * 3600
	case "pga", "lpga", "f1", "nascar":
		duration = 6 * 3600
	}
	return e.Start + duration
}
func matchupSides(s string) []string {
	for _, sep := range []string{" @ ", " vs. ", " vs ", " versus ", " v ", " at "} {
		parts := strings.Split(strings.ToLower(s), sep)
		if len(parts) == 2 {
			return parts
		}
	}
	return nil
}
func channelDateMatches(name string, start int64) bool {
	// Only an explicit ISO date can resolve same-team rematches. Unspecified
	// dates leave multiple candidates ambiguous rather than choosing the nearest.
	for _, part := range strings.Fields(name) {
		part = strings.Trim(part, "()[]|,")
		if d, err := time.Parse("2006-01-02", part); err == nil {
			at := time.Unix(start, 0).UTC()
			return d.Format("2006-01-02") == at.Format("2006-01-02")
		}
	}
	return true
}
func sessionMatch(channel, title string) bool {
	// Require the complete feed title (including players/round when supplied).
	// A generic "Featured Groups" channel cannot choose a specific player feed.
	clean := func(s string) string {
		tokens := strings.Fields(words(s))
		out := []string{}
		for _, w := range tokens {
			switch w {
			case "hd", "fhd", "uhd", "1080p", "720p", "4k", "pga", "lpga", "golf":
				continue
			}
			out = append(out, w)
		}
		return strings.Join(out, " ")
	}
	a, b := clean(channel), clean(title)
	return len(strings.Fields(b)) >= 3 && a == b
}

// FillGaps clips generated blocks around provider listings. The originals are
// never changed. Split IDs include the final interval and remain deterministic.
func FillGaps(generated []Listing, provider []Span) []Listing {
	byChannel := map[string][]Span{}
	for _, p := range provider {
		if p.End > p.Start {
			byChannel[p.ChannelID] = append(byChannel[p.ChannelID], p)
		}
	}
	for id := range byChannel {
		sort.Slice(byChannel[id], func(i, j int) bool { return byChannel[id][i].Start < byChannel[id][j].Start })
	}
	out := []Listing{}
	for _, g := range generated {
		cursor := g.Start
		appendPart := func(start, end int64) {
			if end <= start {
				return
			}
			part := g
			part.Start, part.End = start, end
			if start != g.Start || end != g.End {
				part.ID = g.ID + ":" + strconv.FormatInt(start, 10) + ":" + strconv.FormatInt(end, 10)
			}
			out = append(out, part)
		}
		for _, p := range byChannel[g.ChannelID] {
			if p.End <= cursor {
				continue
			}
			if p.Start >= g.End {
				break
			}
			if p.Start > cursor {
				appendPart(cursor, p.Start)
			}
			if p.End > cursor {
				cursor = p.End
			}
			if cursor >= g.End {
				break
			}
		}
		appendPart(cursor, g.End)
	}
	return out
}
