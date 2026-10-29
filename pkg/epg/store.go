// Package epg stores a plugin's guide on local disk. Refreshes replace the
// schedule and its metadata in one transaction; readers keep seeing the last
// committed guide while an import is running.
package epg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrChanged = errors.New("guide changed; retry with the current snapshot")

type Fields struct {
	ID, ChannelID string
	Start, End    int64
}

type Query struct {
	ChannelIDs []string
	Start, End int64 // Half-open overlap window; use MinInt64/MaxInt64 for exports.
	Limit      int
}

type Store[T any] struct {
	db     *sql.DB
	fields func(T) Fields
	mu     sync.Mutex // Serializes imports without blocking readers.
}

// View identifies one committed guide. A caller racing a later refresh gets
// ErrChanged, rather than programs belonging to another catalog/source.
type View[T any] struct {
	store       *Store[T]
	Revision    int64
	Count       int
	Channels    []string
	maxDuration int64
}

func Open[T any](path string, fields func(T) Fields) (*Store[T], error) {
	if fields == nil {
		return nil, errors.New("guide fields are required")
	}
	dsn := path
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			return nil, err
		}
		st, err := os.Lstat(absolute)
		if err == nil && !st.Mode().IsRegular() {
			return nil, errors.New("guide database must be a regular file")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if errors.Is(err, os.ErrNotExist) {
			f, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				return nil, err
			}
			if err := f.Close(); err != nil {
				return nil, err
			}
		} else if err := os.Chmod(absolute, 0600); err != nil {
			return nil, err
		}
		dsn = (&url.URL{Scheme: "file", Path: absolute}).String()
	}
	dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=cache_size(-2048)&_pragma=journal_size_limit(16777216)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(time.Minute)
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetConnMaxIdleTime(0)
	}
	var schemaVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		_ = db.Close()
		return nil, err
	}
	if schemaVersion > 1 {
		_ = db.Close()
		return nil, fmt.Errorf("unsupported guide database version %d", schemaVersion)
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS guide_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), revision INTEGER NOT NULL,
 metadata BLOB NOT NULL, count INTEGER NOT NULL, channels BLOB NOT NULL,
 max_duration INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS guide_programs (
 seq INTEGER PRIMARY KEY, id TEXT NOT NULL, channel_id TEXT NOT NULL,
 starts INTEGER NOT NULL, ends INTEGER NOT NULL, payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS guide_channel_time ON guide_programs(channel_id,starts,id);
CREATE INDEX IF NOT EXISTS guide_time ON guide_programs(starts,id);
PRAGMA user_version=1;
`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store[T]{db: db, fields: fields}, nil
}

func (s *Store[T]) Close() error { return s.db.Close() }

func (s *Store[T]) Load(ctx context.Context) ([]byte, *View[T], bool, error) {
	v := &View[T]{store: s}
	var metadata, channels []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,metadata,count,channels,max_duration FROM guide_state WHERE singleton=1`).Scan(&v.Revision, &metadata, &v.Count, &channels, &v.maxDuration)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	if err := json.Unmarshal(channels, &v.Channels); err != nil {
		return nil, nil, false, err
	}
	return metadata, v, true, nil
}

// Replace imports records incrementally, without building a second serialized
// copy of the guide. An encoding, disk, or cancellation error rolls everything
// back, including metadata and the revision.
func (s *Store[T]) Replace(ctx context.Context, metadata []byte, programs []T) (*View[T], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO guide_state VALUES(1,1,X'',0,'[]',0) ON CONFLICT(singleton) DO UPDATE SET revision=revision+1 RETURNING revision`).Scan(&revision); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM guide_programs`); err != nil {
		return nil, err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO guide_programs(seq,id,channel_id,starts,ends,payload) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer statement.Close()
	v := &View[T]{store: s, Revision: revision, Count: len(programs), Channels: []string{}}
	seen := map[string]bool{}
	for i, program := range programs {
		fields := s.fields(program)
		payload, err := json.Marshal(program)
		if err != nil {
			return nil, fmt.Errorf("encode guide program: %w", err)
		}
		if _, err := statement.ExecContext(ctx, i, fields.ID, fields.ChannelID, fields.Start, fields.End, payload); err != nil {
			return nil, err
		}
		if !seen[fields.ChannelID] {
			seen[fields.ChannelID] = true
			v.Channels = append(v.Channels, fields.ChannelID)
		}
		if fields.End > fields.Start {
			duration := fields.End - fields.Start
			if duration < 0 { // Overflow: retain correctness for extreme timestamps.
				duration = math.MaxInt64
			}
			if duration > v.maxDuration {
				v.maxDuration = duration
			}
		}
	}
	channels, err := json.Marshal(v.Channels)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE guide_state SET metadata=?,count=?,channels=?,max_duration=? WHERE singleton=1`, metadata, v.Count, channels, v.maxDuration); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}

// SaveMetadata persists health/lineup changes without rewriting the schedule.
func (s *Store[T]) SaveMetadata(ctx context.Context, metadata []byte, view *View[T]) error {
	if view == nil || view.store != s {
		return errors.New("guide view belongs to another store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE guide_state SET metadata=? WHERE singleton=1 AND revision=?`, metadata, view.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrChanged
	}
	return err
}

func (v *View[T]) begin(ctx context.Context) (*sql.Tx, error) {
	tx, err := v.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM guide_state WHERE singleton=1`).Scan(&revision); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if revision != v.Revision {
		_ = tx.Rollback()
		return nil, ErrChanged
	}
	return tx, nil
}

func (v *View[T]) Query(ctx context.Context, query Query) ([]T, error) {
	tx, err := v.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	low := query.Start
	if low < math.MinInt64+v.maxDuration {
		low = math.MinInt64
	} else {
		low -= v.maxDuration
	}
	statement := `SELECT payload FROM guide_programs WHERE starts>=? AND starts<? AND ((ends>starts AND ends>?) OR (ends<=starts AND starts>=?))`
	args := []any{low, query.End, query.Start, query.Start}
	if query.Start == math.MinInt64 && query.End == math.MaxInt64 {
		statement, args = `SELECT payload FROM guide_programs WHERE 1`, nil
	}
	if len(query.ChannelIDs) > 0 {
		channels, err := json.Marshal(query.ChannelIDs)
		if err != nil {
			return nil, err
		}
		statement += ` AND channel_id IN (SELECT value FROM json_each(?))`
		args = append(args, string(channels))
	}
	statement += ` ORDER BY starts,id,seq`
	if query.Limit > 0 {
		statement += ` LIMIT ?`
		args = append(args, query.Limit)
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var payload []byte
		var program T
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &program); err != nil {
			return nil, err
		}
		out = append(out, program)
	}
	return out, rows.Err()
}

func (v *View[T]) Coverage(ctx context.Context, at int64) (int, int, error) {
	tx, err := v.begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var count, channels int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT channel_id) FROM guide_programs WHERE ?<=0 OR ends=0 OR ends>?`, at, at).Scan(&count, &channels)
	return count, channels, err
}
