package web

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

type StationMapping struct {
	ChannelID string `json:"channelId"`
	StationID string `json:"stationId"`
	Priority  int    `json:"priority"`
}

var stationKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func NormalizeStations(value any) []StationMapping {
	data, err := json.Marshal(value)
	if err != nil {
		return []StationMapping{}
	}
	var rows []StationMapping
	if json.Unmarshal(data, &rows) != nil {
		return []StationMapping{}
	}
	out := []StationMapping{}
	seen := map[string]bool{}
	for _, r := range rows {
		r.ChannelID = strings.TrimSpace(r.ChannelID)
		r.StationID = strings.TrimSpace(r.StationID)
		if r.ChannelID == "" || len(r.ChannelID) > 512 || !stationKey.MatchString(r.StationID) || seen[r.ChannelID] {
			continue
		}
		seen[r.ChannelID] = true
		if r.Priority < -1000 {
			r.Priority = -1000
		}
		if r.Priority > 1000 {
			r.Priority = 1000
		}
		out = append(out, r)
		if len(out) >= 20000 {
			break
		}
	}
	return out
}

// StationOrder combines only explicit mappings. Names and numbers never imply
// station identity, so unrelated regional or alternate channels stay distinct.
func StationOrder(value any, channel string) []string {
	rows := NormalizeStations(value)
	station := ""
	for _, r := range rows {
		if r.ChannelID == channel {
			station = r.StationID
			break
		}
	}
	if station == "" {
		return []string{channel}
	}
	matches := []StationMapping{}
	for _, r := range rows {
		if r.StationID == station {
			matches = append(matches, r)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Priority < matches[j].Priority })
	ids := make([]string, 0, len(matches))
	for _, r := range matches {
		ids = append(ids, r.ChannelID)
	}
	return ids
}
