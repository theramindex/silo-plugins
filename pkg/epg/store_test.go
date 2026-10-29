package epg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

type program struct {
	ID, Channel, Title string
	Summary            string
	Start, End         int64
	Extra              any `json:",omitempty"`
}

func fields(p program) Fields { return Fields{p.ID, p.Channel, p.Start, p.End} }

func TestGuidePersistsAndQueriesOverlapWindows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private guide.sqlite")
	s, err := Open(path, fields)
	if err != nil {
		t.Fatal(err)
	}
	programs := []program{
		{ID: "same", Channel: "a", Start: 50, End: 101},
		{ID: "ended", Channel: "a", Start: 10, End: 100},
		{ID: "same", Channel: "b", Start: 100, End: 200},
		{ID: "instant", Channel: "a", Start: 100},
		{ID: "next", Channel: "a", Start: 200, End: 300},
	}
	view, err := s.Replace(ctx, []byte(`{"source":"one"}`), programs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := view.Query(ctx, Query{ChannelIDs: []string{"a"}, Start: 100, End: 200})
	if err != nil || len(got) != 2 || got[0].ID != "same" || got[1].ID != "instant" {
		t.Fatalf("window = %+v, %v", got, err)
	}
	count, channels, err := view.Coverage(ctx, 100)
	if err != nil || count != 4 || channels != 2 {
		t.Fatalf("coverage = %d/%d, %v", count, channels, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, fields)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	metadata, view, found, err := s.Load(ctx)
	if err != nil || !found || string(metadata) != `{"source":"one"}` || view.Count != 5 {
		t.Fatalf("reopen = %s, %+v, %v, %v", metadata, view, found, err)
	}
	got, err = view.Query(ctx, Query{Start: math.MinInt64, End: math.MaxInt64})
	if err != nil || len(got) != len(programs) {
		t.Fatalf("export = %d programs, %v", len(got), err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("private database permissions: %v, %v", st, err)
	}
}

func TestFailedImportRetainsGuideAndMetadata(t *testing.T) {
	ctx := context.Background()
	var cancelImport context.CancelFunc
	s, err := Open(filepath.Join(t.TempDir(), "guide.sqlite"), func(p program) Fields {
		if p.ID == "cancel" {
			cancelImport()
		}
		return fields(p)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	good := []program{{ID: "old", Channel: "a", Start: 10, End: 30}}
	view, err := s.Replace(ctx, []byte(`"old"`), good)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Replace(ctx, []byte(`"broken"`), []program{
		{ID: "partial", Channel: "a", Start: 10, End: 20},
		{ID: "invalid", Channel: "a", Extra: make(chan int)},
	})
	if err == nil {
		t.Fatal("invalid guide import succeeded")
	}
	got, err := view.Query(ctx, Query{Start: 0, End: 100})
	if err != nil || !reflect.DeepEqual(got, good) {
		t.Fatalf("old guide after failed import: %+v, %v", got, err)
	}
	metadata, _, _, err := s.Load(ctx)
	if err != nil || string(metadata) != `"old"` {
		t.Fatalf("metadata after rollback: %s, %v", metadata, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Replace(canceled, []byte(`"canceled"`), good); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh: %v", err)
	}
	midway, cancelImport := context.WithCancel(ctx)
	defer cancelImport()
	_, err = s.Replace(midway, []byte(`"partial"`), []program{{ID: "partial", Channel: "a"}, {ID: "cancel", Channel: "a"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-import cancellation: %v", err)
	}
	got, err = view.Query(ctx, Query{Start: 0, End: 100})
	if err != nil || !reflect.DeepEqual(got, good) {
		t.Fatalf("cancellation lost the last guide: %+v %v", got, err)
	}
}

func TestOpenRefusesANewerGuideSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guide.sqlite")
	s, err := Open(path, fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if unexpected, err := Open(path, fields); err == nil {
		_ = unexpected.Close()
		t.Fatal("opened an unknown future schema")
	}
}

func TestRefreshDoesNotBlockReadersOrMixSnapshots(t *testing.T) {
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	s, err := Open(filepath.Join(t.TempDir(), "guide.sqlite"), func(p program) Fields {
		if p.ID == "new" {
			close(entered)
			<-release
		}
		return fields(p)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.Replace(ctx, []byte(`"old"`), []program{{ID: "old", Channel: "a", Start: 1, End: 100}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Replace(ctx, []byte(`"new"`), []program{{ID: "new", Channel: "b", Start: 2, End: 100}})
		done <- err
	}()
	<-entered
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	got, readErr := old.Query(readCtx, Query{Start: 0, End: 100})
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if readErr != nil || len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("read during import: %+v, %v", got, readErr)
	}
	if _, err := old.Query(ctx, Query{Start: 0, End: 100}); !errors.Is(err, ErrChanged) {
		t.Fatalf("old snapshot read should retry: %v", err)
	}
	if err := s.SaveMetadata(ctx, []byte(`"stale"`), old); !errors.Is(err, ErrChanged) {
		t.Fatalf("old metadata overwrote new source: %v", err)
	}
	metadata, current, _, err := s.Load(ctx)
	if err != nil || string(metadata) != `"new"` {
		t.Fatalf("new source metadata: %s, %v", metadata, err)
	}
	got, err = current.Query(ctx, Query{Start: 0, End: 100})
	if err != nil || len(got) != 1 || got[0].Channel != "b" {
		t.Fatalf("new source guide: %+v, %v", got, err)
	}
}

func BenchmarkGuideStorage(b *testing.B) {
	ctx := context.Background()
	programs := make([]program, 250000)
	for i := range programs {
		programs[i] = program{ID: fmt.Sprint(i), Channel: fmt.Sprintf("channel:%d", i%5000), Title: "Evening programme", Summary: "A programme description providing details about the episode, the presenters, and the stories covered in this broadcast.", Start: 1700000000 + int64(i/5000)*1800, End: 1700000000 + int64(i/5000+1)*1800}
	}
	path := filepath.Join(b.TempDir(), "guide.sqlite")
	s, err := Open(path, fields)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	view, err := s.Replace(ctx, []byte(`{}`), programs)
	if err != nil {
		b.Fatal(err)
	}
	legacy, err := json.Marshal(programs)
	if err != nil {
		b.Fatal(err)
	}
	legacyPath := filepath.Join(b.TempDir(), "catalog.json")
	if err := os.WriteFile(legacyPath, legacy, 0600); err != nil {
		b.Fatal(err)
	}
	channelPrograms := []program{}
	for _, p := range programs {
		if p.Channel == "channel:5" {
			channelPrograms = append(channelPrograms, p)
		}
	}
	start, end := int64(1700018000), int64(1700028800)
	b.Run("RestoreJSON", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out []program
			data, err := os.ReadFile(legacyPath)
			if err != nil {
				b.Fatal(err)
			}
			if err := json.Unmarshal(data, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("RestoreSQLite", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			restored, err := Open(path, fields)
			if err != nil {
				b.Fatal(err)
			}
			_, _, _, err = restored.Load(ctx)
			_ = restored.Close()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("MemoryVisibleWindow", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			low := sort.Search(len(channelPrograms), func(i int) bool { return channelPrograms[i].Start >= start-1800 })
			high := sort.Search(len(channelPrograms), func(i int) bool { return channelPrograms[i].Start >= end })
			out := make([]program, 0, high-low)
			for _, p := range channelPrograms[low:high] {
				if p.End > start {
					out = append(out, p)
				}
			}
			if len(out) != 6 {
				b.Fatal(len(out))
			}
		}
	})
	b.Run("SQLiteVisibleWindow", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			out, err := view.Query(ctx, Query{ChannelIDs: []string{"channel:5"}, Start: start, End: end})
			if err != nil || len(out) != 6 {
				b.Fatalf("visible window: %d, %v", len(out), err)
			}
		}
	})
}
