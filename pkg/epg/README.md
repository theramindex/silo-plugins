# Local EPG storage

`epg.Store[T]` is a pure-Go SQLite guide cache shared by the Live TV plugins.
Each plugin catalogue/account owns its database file; HDHomeRun namespaces raw
channel IDs by endpoint before querying and applying display-channel mappings.

- `Replace` commits metadata and a complete programme refresh together. JSON
  encoding, SQL, and cancellation failures roll back the entire import.
- `SaveMetadata` records health/lineup changes without rewriting programmes.
- `Load` reads metadata and guide statistics without decoding programme rows.
- `View.Query` reads half-open channel/time windows, ordered by start, ID, then
  import order. Duplicate upstream programme IDs across channels are retained.
  Use the full `MinInt64`/`MaxInt64` window for compatibility exports.
- `View.Coverage` supports the adapters' last-good-guide regression checks.

Views are immutable. A query or metadata write using an outdated guide revision
returns `ErrChanged`; callers reload the catalogue and retry rather than combine
one source's metadata with another source's programmes. Each query holds a read
transaction so a refresh cannot change its rows halfway through a response.

The database uses WAL, FULL synchronization, a bounded 2 MiB page cache per
connection, and a maximum of four connections. One idle connection is retained
and idle connections expire after a minute. Imports serialize while readers
continue on the previously committed guide. Database and sidecar files belong
in a private directory on local storage, never on a network filesystem. The
main file is created with mode 0600; metadata can contain private source URLs.

Adapters retain legacy JSON/gzip files for rollback and import them only when
SQLite has no committed state. Manifest probes skip cache initialization.
Background migration must not prevent the legacy guide from being served.

Validation and reproducible benchmarks:

```sh
go test -race ./pkg/epg
go test -run '^$' -bench BenchmarkGuideStorage -benchmem ./pkg/epg
```

The benchmark models 250,000 programmes across 5,000 channels. It compares
JSON file restoration with opening/loading SQLite metadata, plus a six-row
visible window against the original binary-search memory strategy. It measures
allocation volume per operation, not process RSS. XMLTV download/parsing is
still required during refresh; SQLite does not eliminate that work.
