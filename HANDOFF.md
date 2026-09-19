# MetaTube Admin handoff

Updated: 2026-09-18

## Repository and branch

- GitHub: `https://github.com/kinlshum/metatube-sdk-go`
- Working branch: `codex/mdcng-fc2cmadb-providers`
- Compare with main:
  `https://github.com/kinlshum/metatube-sdk-go/compare/main...codex/mdcng-fc2cmadb-providers`
- Local checkout:
  `/Users/kin/Documents/ChatGPT/Homelab/metatube-sdk-go/server`

Continue from the working branch and merge the entire branch, not only the most
recent documentation commit.

## Deployment

- MetaTube Admin: `http://192.168.10.166:8080/admin`
- Docker host: `root@192.168.10.170` (Kraken)
- Stack directory on host:
  `/mnt/cache_nvme_apps/appdata/metatube-stack`
- Compose source in this repository: `deployment/compose.yaml`

Do not commit credentials, API keys, cookies, tokens, or host-specific secret
environment files.

## Exposure and authentication

`https://metatube-admin.madtechinc.com/admin` resolves to a public address
behind an OpenResty reverse proxy and was verified (2026-09-19) to serve the
admin with **no authentication**: `/admin`, `/admin/api/stats`,
`/admin/api/logs`, `/admin/api/provider-throttles` (the `PUT` on the same path is
equally open), `/v1/db/version`, and `/v1/providers` all answered anonymously.
The served admin page is byte-identical to this branch. `METATUBE_TOKEN` is
unset there, so `/v1` is open as well.

Application-level controls added on this branch:

- `METATUBE_ADMIN_TOKEN`: when set, every `/admin` route (page, stats, logs,
  throttles, and all trace APIs) requires the token via
  `X-MetaTube-Admin-Token`, `Authorization: Bearer`, or `?token=` (which plants
  an HttpOnly cookie for the single-page UI). Off by default.
- `METATUBE_TRUSTED_PROXIES`: forwarded headers are now ignored unless a proxy
  is explicitly trusted, so a caller can no longer spoof its own address in
  statistics, logs, or traces. Behind the public proxy, set this to the proxy
  address (for example `192.168.10.1`) or remote clients all appear as the proxy
  IP.

Still required outside this repository: protect the public hostname at the proxy
(identity-aware proxy, basic auth, VPN, or IP allowlist), and consider setting
`METATUBE_TOKEN` for the `/v1` API.

## Completed on this branch

- MetaTube Admin page at `/admin`.
- Provider throttle settings with per-provider concurrency and randomized delay.
- JAVDB defaults constrained to one worker and a 3–6 second delay.
- Provider health/status checks and request-speed statistics.
- Client attribution including observed IP and source port.
- Provider/client request breakdown.
- FlareSolverr status, performance, and recent errors.
- Provider flags indicating known FlareSolverr use.
- TEST page for querying individual/all MetaTube movie providers.
- Native rolling MetaTube server log viewer without a Dozzle dependency.
- Structured HTTP and FlareSolverr error queues in the general `LOGS` tab.
- Enrichment trace foundation: `internal/trace` store/redaction/retention,
  correlation middleware with `X-MetaTube-Trace-ID`, engine instrumentation for
  movie/actor/review lookups (provider, throttle, cache, fallback, selection),
  and the `/admin/api/traces*` ingest and query APIs.
- Admin UI tabs `LOGS-METATUBE-VIDEO` and `LOGS-METATUBE-ACTOR` with filters,
  paging, auto-follow, 5-second refresh while visible, a stage-timeline drawer
  (grouped per provider and component, including redacted field-change tables),
  copy trace ID, JSON export, `Show in LOGS` correlation and
  `Awaiting client report`.
- Optional admin token (`METATUBE_ADMIN_TOKEN`) and trusted-proxy configuration
  (`METATUBE_TRUSTED_PROXIES`).

The enrichment trace work is documented in
`docs/METATUBE_ENRICHMENT_TRACE_TODO.md` under "Implementation status". Still
outstanding: image/translation events, the Windmill trace helper, Emby plugin
reporting, and FlareSolverr events from the provider bridge.


Deployment note: `deployment/compose.yaml` now sets `METATUBE_TRACE_ENABLED`,
`METATUBE_TRACE_DSN=/config/traces.db`, `METATUBE_TRACE_RETENTION_DAYS`,
`METATUBE_TRACE_MAX_RUNS`, and `METATUBE_TRACE_MAX_EVENTS_PER_RUN`. The trace
database lives on the existing `/config` volume, so it survives restarts.

The current deployed admin UI is server-level tooling. The Emby MetaTube plugin,
Windmill, and jav-master-app are clients, not owners of provider throttling.

## Important files

- `route/admin.html` — current single-page admin UI.
- `route/admin.go` — admin APIs and bridge statistics.
- `route/route.go` — route registration and request middleware.
- `engine/metrics.go` — server, client, provider, and recent-request metrics.
- `engine/throttle.go` — provider concurrency/delay controls and metrics.
- `engine/health.go` — scheduled provider health checks.
- `internal/logbuffer/logbuffer.go` — bounded native rolling log buffer.
- `deployment/provider-bridge/bridge.py` — provider bridge and FlareSolverr work.
- `docs/METATUBE_ENRICHMENT_TRACE_TODO.md` — next major feature specification.

## Next major feature

Implement two dedicated structured trace tabs:

- `LOGS-METATUBE-VIDEO`
- `LOGS-METATUBE-ACTOR`

These must remain separate from the general `LOGS` tab. Their purpose is to
trace the full lookup/identify/enrichment path across MetaTube, providers,
throttling, FlareSolverr, translation, Windmill, and Emby.

Read and follow the complete design and acceptance criteria in:

[`docs/METATUBE_ENRICHMENT_TRACE_TODO.md`](docs/METATUBE_ENRICHMENT_TRACE_TODO.md)

The key architectural requirement is correlation-ID propagation. MetaTube can
trace only its own request/provider work unless Windmill and the Emby plugin
report their downstream stages using the same trace ID. The UI must not claim an
end-to-end success when downstream status was never reported.

## Recommended implementation sequence

1. Persistent structured trace model/store, redaction, middleware, and APIs.
2. Movie and actor instrumentation, including provider throttle and
   FlareSolverr stages.
3. Separate video/actor trace tabs with filters and timeline details.
4. Reusable Windmill trace helper and reporting from both enrichment flows.
5. Emby plugin correlation/reporting and end-to-end tests.

## Deployment record

Deployed on 2026-09-19 03:18 UTC (2026-09-18 23:18 EDT) from commit `9da50ec`:

- Image `kinlshum/metatube-server-providers:local` on Kraken, built by
  `docker compose build metatube` from the GitHub branch (context
  `https://github.com/kinlshum/metatube-sdk-go.git#codex/mdcng-fc2cmadb-providers`).
- Only the `metatube` container was recreated
  (`docker compose up -d --no-deps metatube`); postgres, flaresolverr and
  provider-bridge were left running.
- Trace store: `/mnt/cache_nvme_apps/appdata/metatube-server-charleshuang233/traces.db`
  (mounted as `/config/traces.db`). It survived the container recreation.
- Compose backups kept on the host:
  `compose.yaml.backup-20260918-231650` (pre-deploy),
  `compose.yaml.backup-20260918-232049` (before the trusted-proxy fix).
- Rollback image tag: `kinlshum/metatube-server-providers:rollback-20260918-231650`
  (still the previous build).

Rollback:

```sh
cd /mnt/cache_nvme_apps/appdata/metatube-stack
cp -p compose.yaml.backup-20260918-231650 compose.yaml     # env without tracing
docker tag kinlshum/metatube-server-providers:rollback-20260918-231650 \
           kinlshum/metatube-server-providers:local
docker compose up -d --no-deps metatube
```

Verified after deploy: `/admin` serves the new page (sha256 `8fa3074e…`) on both
`http://192.168.10.166:8080` and `https://metatube-admin.madtechinc.com`, the two
trace tabs are present, `/admin/api/traces` and `/admin/api/trace-stats` answer
`enabled: true`, `/config/traces.db` is created, and a real JavBus lookup
recorded five stages (client, throttle, provider 296 ms, result selection,
database save) and showed `awaiting_report`.

Second deploy (2026-09-19 04:05 UTC) shipped the four trace review corrections
from commit `cb1a5f2`:

- Image rebuilt and only `metatube` recreated; compose unchanged
  (`compose.yaml.backup-20260919-000306` kept).
- Rollback image tag:
  `kinlshum/metatube-server-providers:rollback-20260919-000306`.
- The existing trace store gained the `downstream_status` column through
  AutoMigrate (27 columns verified) and kept its rows.
- Verified live: the served page carries the Auto-follow logic (sha256
  `6132d42f…`), a real JavBus lookup reports `results=1` with the five-stage
  timeline, `lookup` traces report downstream `unavailable` while `identify`
  traces await a client report, Windmill plus Emby reporting flips the same
  trace to `complete` (no longer awaiting), and an explicit `result_count: 0`
  from the ingest API is preserved. The temporary verification traces were
  deleted afterwards.

## DeepSeek trace corrections (reviewed 2026-09-19)

A code review after the first deployment found four issues; all four are fixed,
tested and verified on this branch:

1. **P1** — `engine/actor.go` no longer dereferences a nil GFriends result while
   building the image-injection trace event; a nil result reports zero images and
   the failure event is kept. Regression test:
   `engine/actor_test.go:TestGFriendsImageInjectionWithNilResultDoesNotPanic`.
2. **P2** — `Awaiting client report` is derived from a persisted
   `downstream_status` field rather than from the trace status, so
   server-only lookups report `unavailable`, one reporter awaits the other,
   `complete` never awaits, and a downstream error is `failed`.
3. **P2** — Auto-follow is functional: the newest page stays selected, the open
   drawer refreshes live while preserving scroll position, Pause stops both list
   and drawer polling, and Refresh works while paused without resuming polling.
4. **P3** — Native lookups persist their result count (including an explicit
   zero), so a successful lookup no longer displays `Results: 0`.

Verification:

```sh
go test ./internal/trace ./route ./engine   # ok, ok, ok
go test -race ./internal/trace ./route ./engine
go vet ./...
```

`go test ./...` still shows the unrelated pre-existing failures in
`detector` (fixture `detector/345a376e579ff02a518b831b1b2b4602.jpg`),
`provider/duga` and `provider/faleno` (live provider/network tests).

Implementation details are in the **DeepSeek corrective handoff (reviewed
2026-09-19)** section of
[`docs/METATUBE_ENRICHMENT_TRACE_TODO.md`](docs/METATUBE_ENRICHMENT_TRACE_TODO.md),
which now also carries the corrections-applied record. Do not expand scope into
acquisition or file management. Next: the remaining image/translation,
FlareSolverr, Windmill, and Emby client integrations in the documented order.

## DeepSeek next feature: expandable trace/run debugger

The operator wants each row in `LOGS-METATUBE-VIDEO` and
`LOGS-METATUBE-ACTOR` to expand inline and show the complete run, its related
traces, and every ordered step. Each run, trace, and step must provide a
correlated-log action so a failed stage can open the exact native MetaTube log
window and then return without losing table state.

This is a master/detail debugger, not a replacement for general `LOGS` and not
an acquisition/download feature. Keep the current drawer as an optional
full-trace view, but add an accessible chevron and inline expansion beneath the
summary row.

The complete coding contract is in **DeepSeek handoff: expandable run, trace,
step, and correlated-log viewer** in
[`docs/METATUBE_ENRICHMENT_TRACE_TODO.md`](docs/METATUBE_ENRICHMENT_TRACE_TODO.md).
It specifies explicit run grouping, lazy loading, ordered step timelines,
expandable safe event details, run/trace/step log filtering, API and migration
constraints, accessibility, polling behavior, security, and required tests.

Current storage must be understood correctly: structured traces are durable in
`/config/traces.db`; Admin `LOGS` is only a 1,000-line in-memory buffer; Docker
keeps one 50 MB `json-file`; and no Graylog/GELF integration exists. The
structured SQLite events are therefore the timeline source of truth. Native
logs are recent supporting evidence and must be labeled as ephemeral. The spec
documents a future optional Graylog adapter without making Graylog mandatory.

The requested job/run detail view must show all three sources together but
clearly labeled: `TRACE TIMELINE` from `traces.db`, `RECENT NATIVE LOGS` from
the ephemeral in-process buffer, and `GRAYLOG LOGS` from the optional durable
backend. Every run, trace, and step needs both `View correlated logs` and `Open
in Graylog`, plus `Back to trace` with UI state restoration. Graylog failure or
absence must never block the trace timeline. Do not copy Graylog messages into
SQLite; correlate by trace/run/job IDs and time ranges. The full adapter,
configuration, deep-link, deduplication, security, and test requirements are in
the spec section titled **Required combined run-log experience**.

Do not group work using fuzzy actor/time similarity, expose admin or Windmill
credentials in links, or let expand/collapse/log actions mutate trace records.
Implement and verify this before declaring the trace UI an operational
end-to-end debugger.

## Safety and behavior requirements

- Trace storage failure must never make a metadata lookup fail.
- Never store authorization headers, passwords, API keys, cookies, or raw
  tokens in trace events or exports.
- Keep acquisition/download/file-transfer behavior out of MetaTube Admin; it
  belongs to jav-master-app.
- Preserve the existing general logs, provider settings, stats, and TEST page.
- Use 5-second auto-refresh only while a trace tab is visible, with a manual
  Refresh and Pause control.
- Test movie/actor separation, concurrent event ordering, idempotency,
  redaction, retention, and partial failures before deployment.

## Recent commits to retain

- `37f8e49` — video/actor enrichment trace design and TODO.
- `98000bb` — native rolling logs and related admin work.

Review the complete branch history for earlier provider, health, statistics,

## Graylog integration session (2026-09-19)

Implemented, tested and deployed on Kraken (branch commits `a2e696b`, `6e047c5`,
`66532a4`):

- `internal/gelf`: bounded, non-blocking GELF sender implementing `trace.Mirror`.
  The trace service mirrors start, step and finish records with the required
  common fields plus every correlation field. Tokens can be mounted from file
  secrets (`METATUBE_GELF_TOKEN_FILE`, `METATUBE_GRAYLOG_TOKEN_FILE`).
- `internal/logsearch`: Graylog 7 search contract verified live — `fields` is
  mandatory and makes the answer CSV, the stream travels as the `streams`
  parameter, and the decoded lines are sorted newest-first. Per-line origin
  fields (`application`, `service`, `server`, `node`, `environment`) let one run
  be followed across machines.
- Admin: run → trace → step tree in the drawer, step-scoped log panel
  (`TRACE TIMELINE` / `RECENT NATIVE LOGS` / `GRAYLOG LOGS`), badges, retention
  labels, duplicate hiding with reveal, per-step filter and auto-follow, Graylog
  deep link, plus an ingestion status strip with a probe button on both tabs.
- Endpoints: `GET /admin/api/gelf`, `POST /admin/api/gelf/probe`,
  `GET /admin/api/logs/search`, `GET /admin/api/trace-runs/:runID`, ingestion
  block in `GET /admin/api/trace-stats`.
- Deployment: dedicated MetaTube GELF HTTP input on `192.168.10.153:12203` with
  its own rotatable token (shared `12201`/`12202` inputs untouched), search
  credential `metatube-search` with the `MetaTube Search Reader` role
  (`searches:*`, `streams:read`, `messages:read/analyze`; writes return 403).
  Both credentials live in `/mnt/cache_nvme_apps/appdata/metatube-stack/secrets`
  and are mounted as Docker secrets.

Verified live: mirrored records stored with
`application=metatube service=metatube-server server=kraken node=kraken-docker
environment=homelab source_type=trace` and the correct `trace_id`/`run_id`, and
retrievable through the adapter's query shape. Sender counters show deliveries
and zero failures/drops; the token only travels in the `X-Graylog-Token` header.

Outstanding Graylog-side issue (not a MetaTube defect): the Graylog LXC stopped
storing messages at `2026-09-19T15:19:44Z` (Emby-sourced logs stopped earlier, at
~`09:19Z`) while its inputs keep accepting (`HTTP 202`, input counter
`incomingMessages` = 13), the process buffer usage is 0, the journal has no
unread segments and Graylog reports zero indexing failures. Cluster health is
green, the deflector targets `graylog_2`, and the LXC filesystem is at 93% (above
OpenSearch's default 90% high disk watermark). Until that instance is fixed
(free space and/or raise the datanode watermarks, then restart `graylog-server`),
new records stay invisible in the `GRAYLOG` section; the sender keeps delivering
in the meantime and no MetaTube change is required afterwards.

FlareSolverr, and admin changes.
