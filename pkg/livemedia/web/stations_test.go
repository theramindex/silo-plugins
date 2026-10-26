package web

import (
	"reflect"
	"testing"
)

func TestStationOrderRequiresExplicitIdentityAndHonorsPriority(t *testing.T) {
	rows := []StationMapping{{ChannelID: "one", StationID: "news", Priority: 20}, {ChannelID: "two", StationID: "news", Priority: 1}, {ChannelID: "regional", StationID: "regional", Priority: 0}, {ChannelID: "two", StationID: "duplicate", Priority: 0}, {ChannelID: "bad", StationID: "../invalid", Priority: 0}}
	if got := StationOrder(rows, "one"); !reflect.DeepEqual(got, []string{"two", "one"}) {
		t.Fatalf("station order %v", got)
	}
	if got := StationOrder(rows, "unmapped"); !reflect.DeepEqual(got, []string{"unmapped"}) {
		t.Fatalf("unmapped station merged: %v", got)
	}
	if len(NormalizeStations(rows)) != 3 {
		t.Fatal("invalid or repeated channel mappings survived")
	}
}
