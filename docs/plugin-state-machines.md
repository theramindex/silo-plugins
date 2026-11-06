# Server plugin state machines and regression ledger

Audit date: 2026-11-06. This is the single cross-plugin state-machine document.
Paths below are relative to the `silo-server-plugins` workspace unless prefixed
with `pkg/`, which means this catalog repository. Function and test names are
the stable evidence anchors; line numbers change when repairs are applied.

## Scope and interpretation

Reviewed the runnable Go server modules: Dispatcharr, Xtream Codes (XC),
HDHomeRun, App Links, Local Artwork, Trakt, the legacy Backup Requests module,
and the catalog's shared EPG, sports guide and live-media packages. Reviewed
browser code that owns server sessions, retries, configuration or persisted
preferences. `silo-kodi` is a client, outside this server-plugin audit.
The deprecated OIDC plugin listed in the workspace instructions is absent.

An **explicit** state is a stored enum/string/flag. An **implicit** state is
encoded by map membership, tokens, timestamps, SQL revisions, promises,
ownership or control flow. These machines compose: a `succeeded` refresh does
not imply that every provider succeeded, and an admitted playback session need
not yet have playable segments. An error is not itself an invalid state.

The tables describe the repaired working tree. The ledger records the actual
pre-repair failures, their triggers and executable regressions. “Confirmed”
requires a reachable production call path and a regression observed failing
before the repair. This is an evidence-backed inventory, not a proof that all
possible interleavings are safe. Host behavior and upstream service guarantees
are called out where the plugin cannot enforce them.

Initial revisions before this audit:

| Checkout | Initial HEAD |
|---|---|
| Catalog/shared libraries | `80d09bdebf74487c9b033bd077d4da7ed9a65c06` |
| Dispatcharr | `d5840de5ba909be8fda34ef9f08fb839460e5de3` |
| XC | `e1658180aeefd9bca778f8cf7a471e505e40c06e` |
| HDHomeRun | `9851f2d6abf0f00c7dc8ccdde2a682c3d1443bd4` |
| Trakt | `141f9de8f207e89ed9aa57f5a290aed0e71c3e7c` |
| App Links | `9f4bf56106f153dc733f1721f4954379e7909780` |
| Local Artwork | `e754168d526b178c16def0d39d0590d9a7d90ee5` |

Backup Requests has no `.git` directory. Existing unrelated untracked files in
Dispatcharr and Local Artwork were preserved.

## Confirmed bugs, ordered by impact

P1 means persistent data loss or interruption of shared playback. P2 means
incorrect acknowledged configuration, stale cross-generation results or an
admin operation that can undo another operation. All six repair groups are
landed and released; delivery evidence appears below.

| Issue | Invalid state/transition observed before repair | Repair and regression owner | Status |
|---|---|---|---|
| [#2: XC unavailable-source retention](https://github.com/theramindex/silo-plugins/issues/2), P1 | Partial refresh succeeds; unavailable provider's channels survive but its durable guide, categories, movies and series disappear. Subsequent retries have no retained copy. | `xtream-codes/internal/app/sync.go`: `appendStaleXtreamSource` now queries the full snapshot and copies source-owned content, only for a matching configuration. `TestSyncXtreamKeepsFailedSourceStaleWhileRefreshingHealthySource` covers memory, SQLite, force refresh and disk reload. | Released in XC 0.4.3; regression passed. |
| [#1: Trakt alias consumption](https://github.com/theramindex/silo-plugins/issues/1), P1 | One remote history play satisfies two distinct local plays using different aliases; both return `NO_CHANGE`, silently dropping a play. | `trakt/provider/history.go`: every alias references the same consumable row. `TestApplyWatchedCountsPlaysAndIgnoresRepeatedEventIDs` covers movie/episode aliases, complete ID maps, duplicate event IDs and replay. | Released in Trakt 0.3.3; regression passed. Trakt issues are disabled, so tracking lives here. |
| [#3: Rewind clear/admission](https://github.com/theramindex/silo-plugins/issues/3), P1 | A newly admitted buffer reports `buffering` with two segments after an older clear deleted its files. A viewer can remain registered after its buffer is removed. | `pkg/livemedia/rewind/{manager,viewer}.go`: lifecycle locks span admission/clear; buffer and lease registration share the map lock. `TestManagerClearCannotDeleteConcurrentAdmission` and `TestViewerClearSerializesAdmissionAndReleasesReservations`. | Released in shared library 0.1.16, Dispatcharr 0.9.2, XC 0.4.3 and HDHomeRun 0.4.2. All consumer pins updated; race regressions passed. |
| [#4: XC source mutation](https://github.com/theramindex/silo-plugins/issues/4), P2 | Two successful creates load the same registry; last whole-file save loses the other acknowledged source. Edit/delete interleavings use the same path. | `xtream-codes/internal/plugin/routes.go`: one mutation lock per owning route server spans load, legacy migration and save. `TestAdminSourceMutationsPreserveConcurrentCreates`. | Released in XC 0.4.3; regression passed. Scope is one plugin process/owner, not independent processes sharing a file. |
| [#5: XC obsolete guide work](https://github.com/theramindex/silo-plugins/issues/5), P2 | Targeted guide work reaches the target with a canceled caller, after close, or with old settings after a newer catalog operation. A running targeted operation is not canceled by config replacement. | Coordinator tracks targeted executions, rejects obsolete configuration, checks cancellation after serialization and drains on close. Service rejects mismatched snapshots and canceled publication. Fill checks settings between batches; router drains refreshes before closing stores. `TestTargetedGuideRejectsCanceledClosedAndObsoleteWork`, `TestTargetedGuideCanceledOnConfigurationReplacement`. | Released in XC 0.4.3; focused race regressions passed. |
| [#6: App Links overlapping mutations](https://github.com/theramindex/silo-plugins/issues/6), P2 | Save leaves Delete enabled; Delete leaves Save enabled. A delayed upsert can recreate a deleted link; completion can reset a newly selected draft. | `app-links/internal/plugin/pages.go`: both operations share admission guards; mutation locks fields, selection and New until completion; failure unlocks retry. `scripts/admin-state.test.mjs` executes the served script with deferred responses. | Released in App Links 0.1.5; both browser-script regressions passed in CI. |

### Reachability and pre-repair observations

| Issue | Concrete trace | Evidence / why previous coverage missed it |
|---|---|---|
| #2 | Seed and persist backup guide/content → primary fetch succeeds, backup fetch fails → ordinary or force sync → query backup guide / reload disk. | `syncXtream` passed only `Current().Catalog`; `FileSnapshotStorage.save` intentionally stores programs through `Guide` and clears `Catalog.Programs`. The old helper copied no content. Observed: empty backup program query and all five content collections empty. Existing test checked channels/health only. A healthy guide count that equals or exceeds the old total avoids the normal shrink guard masking this bug. |
| #1 | Upstream row has TMDB + IMDb (or episode + show/number) → two different event IDs in one watch minute use different keys → export. | `remotePlays` independently incremented every alias count; `addNewPlays` decremented just one. Observed: second event and its repeated ID returned `NO_CHANGE`, with zero upstream writes. The same-key control passed. |
| #3 | Start old ingest → clear detaches and cancels it, then blocks waiting for its reader → admit fresh ingest and write segments → let old reader finish → clear removes the root. | Production: shared `web/controller.go` start/clear routes in all three TV plugins; Dispatcharr also exposes `timeshift_routes.go`. Test gates an actual HTTP response reader after request cancellation. Observed status: `buffering`, `SegmentCount:2`; segment read fails. Viewer test gates an actual reservation release and observes failed owner heartbeat after successful admission. |
| #4 | Two source creates both read missing file → both reach legacy-settings fallback → release both → both save and return 200 → reload. | `SourceRegistry.Load`/`Save` individually lock but not the handler transaction. Regression gates the existing settings-provider boundary; observed one acknowledged source missing. Tenant creation uses the same handler with its own owner lock. |
| #5 | Cancel before targeted call / close before call / finish newer-config catalog then call old batch; separately hold a running targeted call and queue new config. | `RunGuideChannels` used only `runMu`, bypassing `closed`, `c.cancel` and job configuration. Background `loadLiveTVGuideChannels` captures settings for multiple batches. Observed invalid calls reaching target and active old context remaining uncanceled. Tests assert target admission/cancellation, not just response text. |
| #6 | Select link → start deferred save/delete → inspect paired controls and invoke competing action/selection → fail request → retry. | Admin flags `saving` and `deleting` guarded only themselves. Observed paired action enabled on both traces. Regression additionally protects no second write, stable editor, unlock after failure and successful retry. Server upsert-by-ID permits recreation; server locking alone cannot infer intended user ordering. |

Immutable pre-repair source anchors:

- #1: [independent alias counting and consumption](https://github.com/theramindex/silo-plugin-watchprovider-trakt/blob/141f9de8f207e89ed9aa57f5a290aed0e71c3e7c/provider/history.go#L123-L216).
- #2: [partial-source merge and fallback](https://github.com/theramindex/silo-plugin-xtream-library/blob/e1658180aeefd9bca778f8cf7a471e505e40c06e/internal/app/sync.go#L283-L340), [SQLite representation](https://github.com/theramindex/silo-plugin-xtream-library/blob/e1658180aeefd9bca778f8cf7a471e505e40c06e/internal/cache/snapshot_store.go#L122-L138).
- #3: [root-wide cleanup after releasing map lock](https://github.com/theramindex/silo-plugins/blob/80d09bdebf74487c9b033bd077d4da7ed9a65c06/pkg/livemedia/rewind/manager.go#L338-L359), [separate viewer admission/clear](https://github.com/theramindex/silo-plugins/blob/80d09bdebf74487c9b033bd077d4da7ed9a65c06/pkg/livemedia/rewind/viewer.go#L47-L136).
- #4: [load/modify/save handler](https://github.com/theramindex/silo-plugin-xtream-library/blob/e1658180aeefd9bca778f8cf7a471e505e40c06e/internal/plugin/routes.go#L1185-L1305).
- #5: [targeted coordinator bypass](https://github.com/theramindex/silo-plugin-xtream-library/blob/e1658180aeefd9bca778f8cf7a471e505e40c06e/internal/plugin/refresh_coordinator.go#L175-L224), [stale-settings publication path](https://github.com/theramindex/silo-plugin-xtream-library/blob/e1658180aeefd9bca778f8cf7a471e505e40c06e/internal/app/sync.go#L898-L1119). `TestRefreshGuideChannelsNowRejectsObsoleteConfiguration` additionally reproduced actual replacement of “Current provider” guide data by “Old provider”; `TestTargetedGuideCloseCancelsAndDrainsRunningWork` reproduced missing cancellation on close. Both failed against a temporary Go overlay of pre-repair source and pass with the repair.
- #6: [independent busy flags and mutation handlers](https://github.com/theramindex/silo-plugin-app-links/blob/9f4bf56106f153dc733f1721f4954379e7909780/internal/plugin/pages.go#L242-L303).

## Dispatcharr and XC: configuration, refresh and catalog

Owners: both `internal/plugin/refresh_coordinator.go`, `internal/app/{service,sync}.go`,
`internal/cache/{store,snapshot_store}.go`, `internal/config/settings.go` and route
hydration. XC additionally owns `source_registry.go`, `tenants.go` and
`livetv_guide_fill.go`. The targeted guide machine is XC-only.

Invariants:

- At most one coordinator target executes. Only the matching job ID updates
  the public job result; an obsolete waiter's result belongs to its own run.
- Async warming cannot replace a more important same-config recovery job.
  Priority: force > catalog > channels > guide. Force covers all operations;
  catalog covers non-force operations; channel-only does not cover a guide.
- Catalog, guide and credentials belong to one configuration. Cache failure
  retains a usable same-scope snapshot; a mode/configuration change cannot
  reuse mismatched data. Durable publication must precede in-memory success.
- Durable guide rows are read through `ProgramQuery`/`WithPrograms`, not by
  assuming `Catalog.Programs` is populated. Rejected severe guide shrink
  preserves the last good guide and records a warning.
- Direct Dispatcharr login/API key can offer DVR; Xtream and M3U/XMLTV cannot.
  API-key source catalogs use the direct-login capability model.

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Configuration (implicit) | Unconfigured/invalid | Configure/edit with incomplete data | Remain invalid; connection tests/sync return validation error. |
| Configuration | Valid | Normalize and persist valid replacement | New config key; schedule refresh; retire mismatched cached state. |
| Connection editor | Idle/editing | Test / save | Testing/saving; omitted existing secrets preserved; responses redact secrets. |
| Connection editor | Testing/saving | Validation, upstream or persistence error | Editable error; prior durable connection retained. |
| Refresh (explicit) | Idle/terminal | Start accepted | Queued, unique execution ID and cancel function. |
| Refresh | Queued | Execution mutex acquired; context valid | Running; capture start time. |
| Refresh | Queued/running | Higher-priority job or different config accepted | Cancel old execution; queue replacement. |
| Refresh | Active | Same config and covering/higher-priority Start | Return existing job; no second execution. |
| Refresh | Active | Synchronous Run for same config | Wait own active run; run uncovered work afterwards. |
| Refresh | Running | Target returns success / error / canceled context | Succeeded / failed / canceled; record completion and own result. |
| Refresh | Any open | Close | Closed; cancel and drain admitted work; later Start/Run rejected. |
| Targeted guide (#5) | Not admitted | Valid current-config call, open coordinator | Register independent execution; wait execution mutex; recheck cancellation. |
| Targeted guide | Queued/running | Config replaced / caller canceled / close | Cancel; queued work cannot reach target; canceled service work cannot publish. |
| Targeted guide | Old config | Later old background batch | Reject; never sync old configuration back into the new snapshot. |
| Catalog | Empty / matching cached / mismatched cached | Hydrate | Validate scope; reuse matching cache or fetch replacement. |
| Catalog | Fetching | Successful durable save | Publish new snapshot/version and success health. |
| Catalog | Fetching | Upstream failure / save failure | Stale-with-error / publication refused; prior snapshot remains available where scope permits. |
| XC provider aggregate (#2) | Existing providers | Some enabled providers succeed | Merge fresh sources with same-config failed-source channels, programs and content; record partial failure. |
| XC provider aggregate | Existing providers | All providers fail | Preserve prior snapshot; return failure. |
| XC provider aggregate | Existing providers | Source disabled/removed or config key changed | Do not restore it through stale fallback. |
| EPG health (explicit) | Unset/ok/failed | Refresh starts | Loading; previous usable rows may still be served. |
| EPG health | Loading | Accept programs / upstream failure / reject shrink | Ok / failed retaining rows / prior guide plus warning. |
| Snapshot representation | Legacy JSON or transient slice | SQLite save/import succeeds | Durable revision/view; in-memory program slice cleared. |
| Snapshot representation | Durable view | Metadata-only save | Retain guide revision; update metadata atomically. |
| Guide/event index | Absent/current/cached window | Snapshot version, rule or time-window changes | Rebuild or invalidate bounded cache; changed revision retries with current view. |
| Dispatcharr auth client | No token | First password-backed request | Obtain access/refresh tokens under token lock. |
| Dispatcharr auth client | Access token | First HTTP 401 | Refresh or re-login, retry once; subsequent auth failure terminates. API-key requests do not password-refresh. |

### XC registry, tenant and fill machines

Invariants: stable unique source/account IDs; exactly one catalog account;
catalog credentials valid; source/account secrets omitted from public output.
`nil` registry means missing file and permits legacy fallback. A saved nonnil
empty list means intentional removal and must never revive legacy providers.
One route server owns each registry in the current process topology.

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Registry (#4) | Absent | First mutating request | Under owner mutation lock: load legacy settings, normalize, save registry. |
| Registry | Saved / explicitly empty | Create/edit/delete | Under same lock: read latest, merge/update/remove, atomic file rename, queue refresh. |
| Registry | Any | Validation/read/write/seal error | Return error; do not acknowledge mutation. |
| Registry | Draft | Test / test account / test alternate EPG | Return connectivity/coverage result; do not save draft. |
| Tenant router | Shared mode | Ordinary request | Shared settings/store/coordinator. |
| Tenant router | Per-user mode | Valid host-stamped user ID | Lazy tenant with private sealed registry/catalog; no fallback to admin providers. |
| Tenant router | Tenant absent | Scheduled refresh of persisted tenant | Load saved tenant, then normal refresh machine. |
| Tenant refresh | Open | Router shutdown | Stop fill admission, cancel/drain coordinator and fill, then close stores. |
| Background fill | Idle | Visible eligible missing guide; not cooling down | Mark attempted channels; start one fill, at most 100 channels, batches of 8. |
| Background fill | Running | Another request | No overlapping fill. |
| Background fill | Batch done/fails | Same settings, context valid | Next batch; failed attempts also enter 30-minute cooldown. |
| Background fill (#5) | Running | Settings key changed / canceled / closed | Stop further batches; obsolete results rejected. |
| Background fill | Done | Cooldown expires | Channel eligible again. Closed fill never readmits. |

## TV playback, leases, relays and browser owners

Owners: Dispatcharr/XC `internal/cache/store.go`, `internal/plugin/{media,livetv_contract,routes}.go`,
XC `account_pool.go`, `hls_relay.go`, browser `ui/{player,multiview,prefs,app,admin,guide}.js`.
Detailed shared media behavior is below. “Released” is normally map absence,
not a stored enum.

| Machine | States | Transition table | Invariants |
|---|---|---|---|
| Browser watch session | Absent, starting(attempt), active, superseded, stopped/failed | Start → increment attempt → server start → adopt if current; late start → stop returned session; active → 30s heartbeats; stop/unload → invalidate attempt + keepalive stop. | Only latest attempt owns session; abandoned late responses release server reservations. Existing `scripts/watch-session.test.mjs`. |
| Server watch session | Absent, active, ended, pruned | Start → random ID; active heartbeat → update last seen; stop → ended; repeated stop → no change; age/cap pruning → removed. | Owner/binding checked; ended session cannot resume; media adopts input reservation once. |
| XC playback account | Available, leased, expired, rejected/cooling, released | Lease → least-used eligible account under limit; reuse same session/source → same account; Touch → extend 90s; Reject → release + 15s cooldown; Release/expiry → available. | Account limits under pool mutex; browser URL never exposes account credentials; stop is idempotent. |
| XC contract lease | Absent, leased, expired, stopped | Stream with contract session ID → reserve; heartbeat → renew live only; expired heartbeat → 404; stop or unsupported format → release. | Contract/browser session namespaces distinct; failed format selection retains no lease. |
| XC stateless HLS relay | Key absent/loaded/error; token valid/expired/invalid | Register → load/create persisted key + sealed URL/origin/15m expiry; manifest fetch → child tokens; invalid/expired token → 410; upstream error → 502. | Restart can use saved key; child origin restrictions retained; credentials hidden; client cache bounded to 32. |
| Browser media attachment | Library loading, attaching, playing, error, canceled/destroyed | Shared library promise → create decoder only if current → attach; retry → destroy prior transport; stop → invalidate generation and destroy. | Old async library/decoder work cannot take control of new playback. |
| Multiview tile | Absent, starting, active, attaching, attached, removed | Add tile → one session promise; remove → invalidate attempt + stop; late start after remove → stop returned session. | One reservation per tile; bounded 1/2/4 tiles; one audio focus. |
| Browser timeshift | None, starting(attempt), polling, ready, stopped/error | Start → lease → poll; ready → attach; replacement/stop → invalidate attempt + stop lease; late start/status → discarded and lease stopped where needed. | Session, view and attempt all match before adoption. |
| Preferences | Unloaded/loading, clean, dirty/debouncing, saving, retry, profile-switched | Load → merge; edit → 400ms debounce → serialized remote read/merge/write; concurrent edit → retain dirty delta; failure → dirty + 15s retry; profile switch → discard old pending edits and reload. | Never save defaults before initial load; edits retain captured profile identity; edits during save are not erased. Existing prefs profile/sync tests. |
| Admin editor (TV UIs) | Idle/editing, testing/saving/loading, success/error, stale response | Request captures editor identity/request ID; response applies only if current; success adopts saved result; failure allows retry. | Late response for another editor cannot replace current draft. This protection was absent in App Links. |

## Shared live-media machines

Owners: `pkg/livemedia/{rewind,transcode,web,httpclient}`. Shared-media controller
routes are exposed by Dispatcharr, XC and HDHomeRun. Dispatcharr's older
timeshift routes share its `rewind.ViewerManager` with the controller.

### Rewind and MPEG-TS segmentation

Invariants: one current buffer generation per channel; unique generation
directory; returned lease registered against that generation; stopped buffers
never resume; published segment files already atomically renamed; old cleanup
cannot delete subsequently admitted generations. Viewer lease owner/expiry
checks precede data access and renewal. Reservation callbacks run once.

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Manager (#3) | Open, no channel buffer | Enabled Acquire, valid stream, capacity | Under lifecycle lock create starting generation and register lease before releasing map lock. |
| Manager | Current live buffer | Same stream Acquire | Reuse current buffer; add unique expiring lease. |
| Manager | Current terminal/different-stream buffer | Acquire | Retire old generation; install independent directory/generation. |
| Manager (#3) | Open | Clear / disable settings | Exclude new admission, detach and stop old buffers, await ingest without holding map lock, remove/recreate root, reopen admission. |
| Manager | Open | Close | Terminal closed; cancel reaper and clear; later Acquire rejected. Close admission was repaired as part of lifecycle consistency, not counted as a separately demonstrated production incident. |
| Buffer (explicit) | Starting | First supported segment written | Buffering; publish segment metadata. |
| Buffer | Starting/buffering | Upstream HTTP/read/parse/write failure | Failed; existing segments can remain until release/prune/replacement. |
| Buffer | Live/failed | Clear/replacement/idle/budget eviction | Stopped; cancel ingest; asynchronous generation cleanup where appropriate. |
| Buffer | Stopped | Late write/failure callback | Remain stopped; do not republish/recapture bytes. |
| Buffer lease | Absent | Acquire | Random lease, TTL (default 75s); last-lease time updated. |
| Buffer lease | Live | Heartbeat | Renew; release removes; prune removes expired leases; idle buffer retires after idle TTL. |
| Viewer lease (#3) | Absent | Valid owner+client Acquire | Under viewer lifecycle lock acquire buffer and register owner/expiry/callback as one admission. |
| Viewer lease | Owned/live | Correct-owner heartbeat before expiry | Extend viewer and underlying buffer leases. |
| Viewer lease | Owned/live | Release / expiry / clear / disable | Remove once, release buffer capability, invoke upstream callback once. Wrong owner cannot mutate/read it. |
| TS parser (implicit) | Probing PAT/PMT | Supported H.264 elementary stream discovered | Await first IDR/random-access keyframe. |
| TS parser | Awaiting keyframe | First keyframe | Accumulate PAT/PMT-prefixed segment. |
| TS parser | Accumulating | Keyframe after target duration | Close segment and start next keyframe-aligned segment. |
| TS parser | Any | Unsupported video / probe limit / size error | Fail ingest; never claim buffering without usable segment. |

Additional parser constraints: 188-byte TS packets, 33-bit PTS wrap handling,
bounded segment size (3 MiB), and keyframe starts. Window trimming keeps the
latest three segments until whole-buffer eviction. Eviction from an ingest
goroutine must not synchronously wait on that same goroutine; existing
`TestManagerBudgetEvictionOfOwnBufferDoesNotDeadlock` guards this.

### Conversion, relay and HTTP session ownership

| Machine | States | Transition table | Invariants |
|---|---|---|---|
| Transcode manager | Uninitialized, initialized, closed | First start → initialize root/loopback; Close → stop sessions and terminal closed; start after close → reject. | Serialized GPU starts; per-owner replacement stops/waits previous process. |
| GPU reservation | Available, held, released | Start → acquire global advisory slot; FFmpeg exit/stop → release; process death → kernel releases lock. | Two global GPU slots; callback guarded by `sync.Once`; no leaked capacity on failed startup. |
| Conversion session (explicit) | Starting, ready, failed; removed | FFmpeg start → starting; manifest with EXTINF → ready; startup timeout/process exit/low disk → failed; stop/75s lease expiry/replacement/close → removed. | New ID per attempt, owner-bound; manifest readiness determines ready, not process existence; failed status discoverable until removal. |
| Relay session | Ready, root active, root idle, removed | Create without GPU → ready; first root GET → active; completion → idle or stop for complete nonplaylist root; stop/expiry/close → removed. | One active root request; bounded child-resource map; root failure alone can trigger bounded explicit source fallback (up to eight attempts), not arbitrary child failure. |
| Shared HTTP record | Resolving, creating, published/live, stopped/expired | Resolve source → direct/compatible/rewind creation → publish; creation failure → release input; stop/idle/clear/reservation stop → remove. | Authenticated owner, mode allowlist; once-only input release; 75s record TTL; status renews session and source reservation; admin cache clear preserves direct sessions. |
| Shared browser player | Idle, stopping prior, starting, preparing, library loading, attached, playing, failed/stopped | Start increments generation → stop previous → start server session → prepare non-direct (≤35 polls) → attach; fatal codec error direct/rewind → compatible once; rewind setup failure → compatible+rewind; terminal failure/end → stop. | Generation guards all async work; stale successful start explicitly stopped; 20s heartbeat; compatible failure terminates; autoplay denial shows Press Play. |
| HTTP client pool | Absent, cached, evicted | Normalized sorted host allowlist → reuse/create client; cache bound reached → evict/close idle. | Different allowlists cannot share clients; tenant URLs use public-address restrictions. |
| Source backoff | Healthy/eligible, temporarily rejected | Open failure → cooldown/exclude; deadline elapsed → eligible. | Fallback follows explicit saved station/source identity and format, never an inferred matching name. |

## HDHomeRun

Owners: `hdhomerun/internal/hdhr/{engine,guide,guide_store,playback}.go`,
`internal/plugin/server.go`, `internal/plugin/ui/app.js`.

Invariants: configuration signature checked before and after durable
publication; stale configuration cannot publish a refresh. Engine mutex owns
tuner limits and lease identity. Failed device/guide refresh retains usable
last-known-good data. DeviceAuth is stripped from retained/public state.

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Engine | New/unconfigured | Configure | Configured signature; replace clients and retire incompatible leases. |
| Engine refresh | Idle | Periodic/manual refresh wins `refreshMu.TryLock` | Refreshing; concurrent attempts coalesce; at most four endpoints fetched in parallel. |
| Engine refresh | Refreshing | Same signature, successful persistence | Publish lineup/guide together; close replaced clients. |
| Engine refresh | Refreshing | Signature changed / error | Reject obsolete publication; close rejected generation's clients / retain prior usable state. |
| Engine | Open | Close | Closed; cancel leases and reject new work. |
| Device | Unknown | Discover + fetch lineup | Healthy device/channel state. |
| Device | Healthy | Discovery/lineup error | Device error while prior lineup remains available. |
| Guide | Due | Successful nonempty refresh | New durable view and next refresh time. |
| Guide | Due | Empty/error | Retain prior programs; guide error and 30-minute retry deferral. |
| Guide | Enabled | Guide disabled | Empty guide by policy. |
| Tuner lease | Absent | Resolve valid unprotected source under endpoint/group cap | Reserve endpoint+random token; no tune until first GET. |
| Tuner lease | Reserved | Same owner/channel/format Resolve | Renew same token; foreign owner/channel cannot adopt it. |
| Tuner lease | Reserved | Root GET | Active TS or HLS; HEAD does not tune. |
| Gateway | Opening source | Root failure; eligible fallback exists | Atomically reserve next compatible endpoint; close failed response; bounded by endpoint count and 12s open deadline. |
| Gateway | Streaming TS | 20s renewal | Extend active lease while request/token/engine remain valid. |
| Tuner lease | Reserved/active | Stop/expiry/config change/close/TS EOF | Removed; cancel producer. Old token completion cannot remove replacement token. |
| HLS gateway | Valid root/child | Playlist response | Rewrite bounded allowlisted child resource map; children remain owner-bound. |
| Browser main player | Idle | Channel selection | New outer generation; shared player owns session/decoder; stale selection ignored. |
| Browser multiview | Fewer than four tiles | Add/remove tile, audio focus | Independent shared players, removal stops own session; one audio focus. |

No separate HDHomeRun defect was confirmed in this audit. Its use of shared
rewind is covered by #3; HDHomeRun 0.4.2 now pins the repaired shared library
0.1.16.

## Trakt

Owners: `trakt/provider/{provider,device_auth,client,ratelimit,list_state,history,lists,ratings,scrobble,keys}.go`.
The adapter retains pacing state; encrypted credentials, device-flow lifetime,
continuation commitment and per-connection sync admission belong to Silo host.

| Machine | States | Transition table | Invariants |
|---|---|---|---|
| Admission | Unconfigured, configured, authenticated | Missing app ID/secret → permission fault; wrong capability → invalid request; missing access token on auth RPC → invalid request; valid input → upstream request. | No upstream request without required app/profile credentials. |
| Device auth (explicit status) | Pending, slowed pending, authorized, expired, denied, temporary failure | Start complete code response → pending; poll `authorization_pending`/plain 400 → pending; slowdown/429 → interval `max(previous+5s, Retry-After)`; valid token → authorized; expired/invalid grant/404/409/410 → expired; denial/418 → denied; transport/unreadable → temporary fault. | Positive code lifetime/interval; device code preserved across slowdown; authorization requires access token; host enforces poll timing/expiry. |
| Credential rotation | Current, refreshing, rotated, reconnect required, temporary failure | Missing refresh token/invalid grant → reconnect; refresh success → replacement credentials; omitted new refresh token → retain old one; transient fault → keep old credentials for retry. | Host serializes rotating-token use and persists replacement before next use; plugin has no token store. |
| HTTP retry | Ready, pacing wait, in flight, short retry wait, deferred, success/failure | Auth write → credential limiter; success → done; 429 with ≤10s wait, deadline room, attempts left → identical-body retry (≤2 retries); long/unstated wait or exhausted hints → account deferral (default 5m); transport/deadline → temporary fault. | Bounded retries; error body not exposed verbatim; request deadline reserve retained. |
| Pacing map | Absent, active bucket, idle bucket, evicted | First credential digest → bucket; request → reservation + last use; ≥10m idle and full replenishment → evict. | Mutex-protected; raw tokens not retained; outstanding reservation/rate bound survives eviction decisions. |
| Listing traversal | Fresh, pass 1, paused continuation, pass 2, listing complete, traversal complete, restarting, failed | Full read from page 1; multipage pass 1 → verification pass 2; matching digests → complete; response/time budget → continuation; changed count/page/digest before rows escaped → bounded restart; changed after returned rows → temporary fault/new traversal; invalid token → invalid request. | Pass 1/2, page 1..1000; partial row skip only pass 1 with page digest; no change-feed cursor; cannot silently restart after earlier RPC returned rows. |
| Page admission | Slot available, waiting, deferred, reading, last page | Paged GET uses limit 250/page; slot exceeds budget → pause/fault; pagination final/empty/short applied-limit page → end; otherwise advance. | Unpaged progress request stays unpaged; absent header does not falsely declare a full page final. |
| History window | Unchecked, first pass, verifying, retrying, oversized/split, checked, deferred | Read surrounding watch minutes; stable multipage verification → checked; >4 pages with multiple local plays → split; oversized singleton → retry; changed → up to three read attempts; budget/pacing refusal → defer affected events. | Failed or unchecked read never means absent; overlapping windows count a remote history row once. |
| Watched event (#1) | Pending, invalid, repeated ID, checked-present, sending, applied, retry/rejected | Validate movie/episode IDs + watch time; repeated event ID → first result; consume one matching remote row → NO_CHANGE; checked absent → POST → APPLIED; unchecked/read failure → retry. | Each remote row can satisfy one distinct local play across **all** aliases; kind and minute namespace matching; serial replay rechecks remote history. |
| Unwatched event | Pending, invalid, removing, applied/retry/rejected | Valid title ID → history/remove; success → applied; invalid → rejected; transient failure → retry. | Removal clears every play of title; repeating removal converges; timestamp not required. |
| Favorites/watchlist/dropped | Present/absent, pending add/remove, applied, unknown, retry | Validate kind/IDs → batched add/remove; accepted → applied; not_found add → rejected; not_found removal → NO_CHANGE; upstream fault → retry/connection fault. | Movie/show list support; dropped is shows only. Provider-item-key fallback can identify item. Trakt omits app-originated drops from readback; host must not infer their removal. |
| Ratings | Unrated/rated 1..10, pending set/remove, applied, retry | Valid movie/show set → ratings; remove → ratings/remove; success/not_found/error map like lists; invalid rating/type → reject. | Export 1..10; imported value clamped; repeated set/removal converges. |
| Playback scrobble | Upstream idle/playing/paused/stopped; pending/applied/retry | Start/pause/stop → corresponding endpoint; success → applied; 409 → NO_CHANGE; unknown title → rejection; transient failure → retry. | Adapter stores no session/order state; host orders; upstream decides stop threshold (documented 80%). |
| Event batch | Validating, ordered group execution, time exhausted, complete, connection fault | Consecutive same batched operations share request; scrobbles individual; exhausted budget → remaining results retry; connection-wide fault → batch fault; otherwise result per event. | Group order preserved; every event accounted for; no acknowledgment from a failed history check. |

Concurrency boundary: the local sibling host implementation has advisory-lock
credential refresh and per-connection sync admission. Missing adapter-local
locks alone were therefore not classified as a confirmed concurrent refresh
bug. This relies on the deployed host honoring those contracts; the plugin
cannot make an arbitrary external writer's simultaneous history check/write
atomic. Serial at-least-once replay is tested; exactly-once cross-application
Trakt writes are not promised.

## App Links

Owners: `app-links/internal/store/store.go`, `internal/plugin/{routes,pages}.go`.
FileStore mutex serializes one instance's complete read/modify/write; temp-file
rename publishes a complete JSON list. IDs remain stable on edit, generated
from normalized names (hash fallback for names with no ASCII slug).

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Link | Absent | Valid create; generated ID unused | Saved enabled/disabled link with open mode. |
| Link | Existing | Create with same generated ID | Conflict; existing link unchanged. Retry of a lost successful create is conflict, not a second link. |
| Link | Existing/absent | Valid upsert with explicit ID | Replace/create same ID. This is why concurrent delete + stale save can recreate a link. |
| Link | Existing | Delete | Absent; repeated delete succeeds without write. |
| Link | Disabled/absent | User list/open | Hidden / not found. |
| Link | Enabled | Open | HTTP redirect for new tab, iframe shell for embedded. |
| File | Missing | Read | Empty list; malformed JSON/read error → error; invalid individual records filtered on read. |
| File | Valid | Successful mutation | Sorted whole-list temp write + rename; write failure is not acknowledged. |
| User page | Loading, loaded/empty/filter-empty, error | Load → list; search → filtered list; failure → retry action → loading. |
| Admin editor (#6) | Ready/editing | Save or confirmed Delete | One busy operation; fields, row selection, New and both mutations disabled/guarded. |
| Admin editor | Busy | Another mutation or stale selection event | Ignore; do not send second write or replace editor. |
| Admin editor | Busy | Success | Reset editor, reload list, unlock; reload failure separately shows retry. |
| Admin editor | Busy | Failure | Preserve editor + show field/message error + unlock for retry. |

No cross-process file lock or optimistic stale-editor conflict protocol is
claimed. The current plugin constructs one FileStore and one route server.
Separate administrators can still intentionally replace a record by ID; #6
fixes overlapping transitions in one editor, not collaborative editing.

## Local Artwork

Owners: `silo-plugin-local-artwork/provider/provider.go`, `internal/sidecar/sidecar.go`,
`main.go`. This plugin has no durable workflow or metadata XML parser. Its
implicit state comes from filesystem contents and a process-local lookup map.

| Machine | States | Transition table | Invariants |
|---|---|---|---|
| Lookup | Missing/invalid media path, valid media/directory, no artwork, matched | Exact path → validate regular media/directory → enumerate bounded sibling candidates → safe image content → matched or empty. Unsupported search type → empty. | No recursive library scan; leaf symlinks rejected; series/season folder needs media within two levels; provider ID hashes canonical media path. |
| Lookup cache | Cold, remembered ID, alias remembered, lost on restart | Search/path-backed metadata/images match → remember result; explicit requested provider ID → cache alias; ID-only metadata/images → map lookup; restart → cold. | Map guarded by RWMutex; process-local results are immutable after publication. ID-only lookup needs a remembered mapping; it cannot invert a hashed ID after restart. |
| Image resolution | Candidate, rejected, opening, served/error | Current or legacy scheme → validate absolute recognized sibling candidate → open/read ≤8 MiB → sniff image → data URL; missing/unsafe/empty/nonimage/oversize → empty or file error. | Resolver independently revalidates actual filesystem; direct image URLs work after restart; cached metadata does not bypass safe serving. |
| Poster variant | No variant selected, selected | Sorted `poster-*` candidates → first valid variant selected → skip further variants. | Deterministic sorting; exact-name candidates precede variants. |

Cache cold-start and stale lookup metadata are limitations of the existing
process-local cache, not classified as newly confirmed regressions here.
The resolver still revalidates current files. The pre-existing host file-path
matching proposal was left untouched.

## Backup Requests (legacy, not a Git checkout)

Owners: `backuprequests/internal/{app,model,config,seerr}`, `main.go`.
Records and deduplication are process-local. Key is media type + positive TMDB
ID; show requests group at show level, not episode. `skipped` is an aggregate
counter/recent-reason log, not an entry in the request-record map.

Invariants: one active sender per record; sent is terminal; attempts increment
only on claim; waiting does not consume an attempt; ordinary retry excludes
permanent failures and exhausted retries; settings changes reset failures.
An in-flight failure from an earlier settings generation requeues with zero
attempts. These behaviors already have owner-boundary coverage.

| Machine | From | Trigger / guard | To / effects |
|---|---|---|---|
| Settings | Disabled | Event/retry | Skip/no sending; disabled event does not create a record. |
| Settings | Enabled but incomplete | Valid new event | `waiting_for_setup`; retain request, no attempt consumed. |
| Event normalization | Incoming | Supported event + valid media type/TMDB ID | Create `queued`, or update existing LastSeen without duplicate send. |
| Event normalization | Incoming | Unsupported/invalid payload | Aggregate `skipped` reason/counter; no sendable record. |
| Request (explicit) | Queued | Valid settings, atomic eligible Claim wins | `sending`, increment attempts, capture store settings generation. |
| Request | Sending/sent | Concurrent retry / stale retry snapshot | Claim rejected; no duplicate send. |
| Request | Sending | Seerr 2xx or 409 | `sent`; set success time, clear record error. |
| Request | Sending | Transport error, HTTP 429 or 5xx | `retryable_failed`; set failure info. |
| Request | Sending | Other HTTP / permanent sender error | `permanent_failed`; ordinary retry excluded. |
| Request | Sending | Failure after settings generation advanced | `retryable_failed`, attempts reset to zero. |
| Request | Waiting / retryable below attempt cap | Retry with valid enabled settings, claim wins | Sending; overlapping retries serialized by store claim. |
| Request | Retryable at cap | Ordinary retry | Stay retryable in enum but operationally exhausted; no send. This is not an invalid state. |
| Request | Waiting / eligible retryable | Retry while setup invalid | Waiting; no new attempt. |
| Request | Permanent/retryable failed | SettingsChanged | Advance generation; reset attempts; retry if enabled/configured. Sent remains sent. |
| Settings callback | Configure differs from previous | Store defaults and settings | Async SettingsChanged; request retry machine handles overlapping callbacks. |

Limits: restart loses all records/deduplication. An ambiguous network outcome
can be retried; upstream conflict handling converges when Seerr reports an
existing request, but the adapter alone does not provide durable exactly-once
delivery. No separately confirmed Backup Requests bug was added to this ledger.

## Shared persistence, sports and release machines

| Machine / owner | States | Transition table | Invariants |
|---|---|---|---|
| Guide SQL revision, `pkg/epg/store.go` | Absent, committed revision, importing, rolled back, stale view, closed | Replace → import transaction → metadata+guide+revision commit; error → rollback; later commit → old view returns `ErrChanged`; close → reject DB work. | Writer import mutex; readers pin transaction/revision; never mix guide rows and metadata from different revisions. |
| Generated sports persistence, `pkg/sportsguide/store.go` | Empty, valid scope/listings, expired/mismatched | Replace scope/listings; Query filters expired, wrong-scope or renamed generated channels; disk `ErrChanged` → one retry. | Only generated overlays persisted; scope and generated channel name match; memory results cloned. |
| Sports event cache/flight, TV `sports_flight.go`, `sports_prepared.go` | Absent, fresh, expired, fetching, failed-with-stale | Expired request → one keyed shared fetch; waiter cancel → detach; success → cloned cached payload; error → old events + error. | Bounded fetch per key; live TTL ~30s, otherwise ~5m; canceled waiter cannot cancel every other waiter. |
| Prepared sports payload | Not ready, rebuilding, complete, incomplete, stale/error | First request → background rebuild; guide fallback can publish early; complete replaces; incomplete/error retains prior complete payload; policy/guide revision invalidates. | Do not replace usable complete payload with partial matching; short retry after incomplete result. |
| Sports event classification | Scheduled, live, final | Provider/time updates → scheduled/live/completed; impossible long-live timestamps → normalize away from live. | Live cannot remain indefinitely after >18h start or >2h-ended freshness bounds. |
| Plugin release workflow | No releasable commit, queued, releasing, tagged, packaged, catalog updated, failed | Main push → serialized Release; feat→minor, fix/perf/revert→patch, breaking→major; chore/docs/test/refactor alone → no automatic release; manual workflow can force; failed step → Actions failure. | GitHub App identity; Conventional Commits determine release; every main push with releasable change ships; this audit used the authorized release workflow. |

## Validation and delivery

Initial baseline: `GOWORK=off go test ./...` passed in all eight Go modules.
Every new regression was observed failing on the pre-repair production code
for its intended invariant; the ledger records the failing observations.
The tests use existing service/route/manager/ApplyEvents boundaries, real
SQLite where needed, gated concurrent operations and the actual App Links
admin script. No production-only test hooks were introduced.

Final validation:

| Check | Result |
|---|---|
| `GOWORK=off go test -race ./... -count=1 -timeout=180s` in all eight modules | Passed. App Links initially exposed a source-text assertion for `submit.disabled = true`; removed that assertion in favor of the stronger executable guard test, retained inline-feedback coverage, and reran successfully. |
| `GOWORK=off go vet ./...` and `GOWORK=off go build ./...` in all eight modules | Passed. |
| Dispatcharr `node --test scripts/*.test.mjs` | 81 passed. |
| XC `node --test scripts/*.test.mjs` | 49 passed. |
| App Links `node --test scripts/admin-state.test.mjs` | 2 passed. CI runs them with Node 22. |
| Shared `node --test pkg/livemedia/web/media.test.mjs scripts/update-entry.test.mjs` | 5 passed. |
| Dispatcharr, XC and HDHomeRun full race suites with `GOWORK=/tmp/silo-state-audit/go.work` pointing to the repaired shared library | All passed. |
| Trakt `GOWORK=off go run . manifest` and `gofmt -l .` | Manifest succeeded; no formatting drift. |
| Changed Go formatting and `git diff --check` in each edited repo | Clean. |

Early shared-library integration used an external temporary Go workspace.
The released TV consumers now pin the published immutable `v0.1.16` module;
release checks ran with `GOWORK=off` and no committed local `replace` directive.
All five plugins' local release checks and tagged CI/publishing runs passed.

### Published repairs, 2026-11-06

| Component | Release | Source commit | Tagged build / publication |
|---|---|---|---|
| Shared live media | [0.1.16](https://github.com/theramindex/silo-plugins/releases/tag/v0.1.16) | `1f453fc5cc7e710e19f06e76bda6dc71ddbfaea2` | Shared race suite and catalog CI passed. |
| App Links | [0.1.5](https://github.com/theramindex/silo-plugin-app-links/releases/tag/v0.1.5) | `093edec5f3c57c60e84e2cf80242b63f0a4c091d` | [CI success](https://github.com/theramindex/silo-plugin-app-links/actions/runs/42267665359) |
| Trakt | [0.3.3](https://github.com/theramindex/silo-plugin-watchprovider-trakt/releases/tag/v0.3.3) | `f68785670d5b6a70719eecb61b328e041d475749` | [Publish success](https://github.com/theramindex/silo-plugin-watchprovider-trakt/actions/runs/42267665018) |
| Dispatcharr | [0.9.2](https://github.com/theramindex/silo-plugin-dispatcharr/releases/tag/v0.9.2) | `8a9f0e758a030cdc53dc1dfb7d8f0f70ae40fec3` | [CI success](https://github.com/theramindex/silo-plugin-dispatcharr/actions/runs/42268198759) |
| HDHomeRun | [0.4.2](https://github.com/theramindex/silo-plugin-hdhomerun/releases/tag/v0.4.2) | `47495493d777b014491c528240f23a659a6e9b74` | [CI success](https://github.com/theramindex/silo-plugin-hdhomerun/actions/runs/42267934685) |
| XC | [0.4.3](https://github.com/theramindex/silo-plugin-xtream-library/releases/tag/v0.4.3) | `bae13a35d5876c13cc44a969a80b7c896e5113eb` | [CI success](https://github.com/theramindex/silo-plugin-xtream-library/actions/runs/42268129617) |

Downloaded all 15 plugin binaries (Linux amd64/arm64 and Darwin arm64 for each
plugin), verified their SHA-256 values against their release checksums, and
compared the catalog binary URLs/checksums and versions with those verified
artifacts. Executed each released Darwin binary's `manifest` command and
confirmed its bumped runtime version. The catalog's final plugin update is
`ba527ec` (Dispatcharr 0.9.2); all five new plugin versions are listed and the
catalog validator passes. Local Artwork was unchanged and remains 0.2.3.

Commits, tags, releases and catalog publication used ramindex-ci. No live Silo
server deployment was performed. Keep transitions, regression names and issue
status current whenever a linked repair changes.
