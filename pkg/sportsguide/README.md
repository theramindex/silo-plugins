# Generated sports guide overlays

`Generate` accepts channels and an independent sports schedule, applies bounded
per-group league/matching hints and manual event links, and returns attributed
pregame/game/postgame listings. It deliberately accepts no EPG input. Automatic
matches require both competitors, or a complete normalized tournament/session
title. Ambiguous fixtures, expired events, and EPG-derived events are rejected.
Every generated window is identified as an estimate of stream coverage.

`FillGaps` subtracts provider coverage without modifying the original listings.
Adapters query coverage over complete generated blocks so clipped IDs and times
are consistent between adjacent guide-window requests.

`Store` keeps overlays in a separate private SQLite database, using the shared
EPG transaction and WAL implementation. Scope fingerprints include the source
configuration and normalized policy. Reads validate the current channel name
and an explicit 24-hour expiry. Failed imports preserve the prior commit.
An empty path provides in-memory storage for hosts without a persistent cache.

Adapters retain provider listings as the sole guide input to sports matching;
overlays are applied only when producing HTTP guide responses. Generation and
network fetches run in the existing background sports builder.

Validate with `go test -race ./pkg/sportsguide`.
