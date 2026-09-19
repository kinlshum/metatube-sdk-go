# MetaTube video and actor enrichment trace tabs

Status: design/TODO for implementation by DeepSeek or another coding agent.

Owner repository: `kinlshum/metatube-sdk-go`

Target: MetaTube server admin page at `/admin`. The Emby MetaTube plugin,
`jav-master-app`, and Windmill are clients. Provider execution and throttling
remain server-side responsibilities.

## Goal

Add two dedicated admin tabs that explain the complete path of one lookup,
identify, and enrichment operation:

- `LOGS-METATUBE-VIDEO`
- `LOGS-METATUBE-ACTOR`

These are structured workflow traces, not another view of the existing general
`LOGS` stream. `LOGS` remains the rolling server/process log for Gin, GORM,
engine, FlareSolverr, panics, and miscellaneous HTTP errors.

Each trace must answer:

1. Which client started it (Emby plugin, jav-master-app, Windmill, admin test)?
2. Which providers ran, in what order, and after what throttle wait?
3. What did each provider return, reject, or time out on?
4. Were FlareSolverr, translation, fallback, cache, or database used?
5. Which result was selected for identify/enrichment?
6. Which Windmill job ran and what did it update?
7. Did the Emby write/refresh complete, partially complete, or fail?

## Implementation status (2026-09-19)

Server-side tracing is implemented on `codex/mdcng-fc2cmadb-providers`.

Implemented:

- `internal/trace`: thread-safe `Start`/`Event`/`Finish`, context propagation,
  redaction, per-run event cap, bounded SQLite store, retention pruning, delete,
  and confirmed purge.
- Correlation middleware: accepts `X-MetaTube-Trace-ID` or generates one,
  returns it in the response header, mirrors it into ordinary log lines, records
  `request_received` and the request outcome, and finishes only the traces it
  opened itself.
- Engine instrumentation through context-aware methods: provider attempts and
  durations, throttle queue/delay/concurrency, HTTP status on fetches,
  database/cache lookups, fallback usage, keyword normalisation, and result
  selection for movie, actor, and review paths.
- Admin APIs: start/events/finish for client reporting, list with filters,
  detail, sanitized export, delete, purge-expired, and stats.
- Tests in `internal/trace` and `route` cover video/actor separation, concurrent
  event ordering, idempotency, per-run caps, retention (running traces are never
  pruned), abandoned-trace closing, redaction, store-failure safety, filters,
  and disabled-mode behaviour.

Remaining, in order:

1. `LOGS-METATUBE-VIDEO` and `LOGS-METATUBE-ACTOR` admin tabs (step 3) reading
   `/admin/api/traces`.
2. Image-fetch and translation events.
3. Reusable Windmill trace helper posting to the ingest API.
4. Emby plugin reporting.
5. FlareSolverr events forwarded from `deployment/provider-bridge/bridge.py`.
6. Authentication for `/admin/*` (currently unauthenticated).

Client-side notes:

- A trace is opened for `/v1/movies`, `/v1/actors`, `/v1/reviews`, and for
  `/v1/images` only when the client supplies a trace ID.
- A request that arrives with an existing trace ID appends its events to that
  trace and never finishes it. Only the request that opened a trace completes it,
  so an image fetch can never close a client's identify flow early.
- When no `X-MetaTube-Client` header is sent, the client name is inferred from
  the user agent and flagged as `client_inferred` in the first event.

## Scope and ownership boundary

MetaTube can trace requests it receives and provider work it performs. It cannot
infer downstream Windmill or Emby completion from a normal HTTP response.
Clients must propagate a correlation ID and report later stages to MetaTube.

- Emby MetaTube plugin: send trace headers on lookup/identify requests and post
  Emby identify, metadata merge, image, save, and refresh outcomes.
- Windmill: preserve the incoming trace ID or create one, then post job start,
  component results, field changes, Emby update, and completion.
- jav-master-app: send the trace header for lookup/test calls and report later
  stages only when it performs them.

Do not add acquisition, download, renaming, moving, or file-transfer traces.
Those belong to `jav-master-app`, not MetaTube Admin.

## Correlation contract

Use these headers:

- `X-MetaTube-Trace-ID`: UUID/ULID for the end-to-end operation.
- `X-MetaTube-Client`: `emby-plugin`, `jav-master-app`, `windmill`, or
  `metatube-admin`.
- `X-MetaTube-Operation`: `lookup`, `identify`, `enrich`, `refresh`, or `test`.
- `X-MetaTube-Parent-Trace-ID`: optional parent for spawned work.

If the trace ID is absent, MetaTube generates one and returns it in the response
header. Include it in JSON error responses where practical. Source IP/port are
observations; client name and trace ID are authoritative because source ports
are ephemeral.

## Trace data model

Use structured events instead of parsing free-form logs.

### Trace summary

- `trace_id`, optional `parent_trace_id`
- `kind`: `video` or `actor`
- `operation`: lookup/identify/enrich/refresh/test
- `query`, `normalized_query`
- `client_name`, `client_ip`, `client_port`, `user_agent`
- `started_at`, `completed_at`, `duration_ms`
- `status`: queued/running/succeeded/partial/failed/cancelled
- `selected_provider`, `selected_provider_id`
- optional `emby_item_id`, `windmill_job_id`, `windmill_flow_path`
- `result_count`, `warning_count`, `error_count`
- final sanitized error code/message

### Trace event

- monotonic `sequence`, `at`, `duration_ms`, `level`
- `component`: client/metatube/cache/database/throttle/provider/flaresolverr/
  translation/windmill/emby
- `stage`: request_received, normalized, cache_lookup, throttle_wait,
  provider_started, provider_completed, provider_failed, fallback_started,
  result_selected, translation_started, translation_completed,
  windmill_started, windmill_step, emby_write, emby_refresh, completed, etc.
- `provider`, `attempt`, `http_status`, short `message`, safe `details`

For enrichment, store a redacted field-change summary: field, action (added,
updated, unchanged, skipped, failed), source provider, and value length. Do not
store full metadata payloads or image bodies by default.

## Storage and retention

- Add a bounded persistent store so traces survive restarts; prefer SQLite in
  the MetaTube data directory with indexed trace/event tables.
- Default retention: 30 days and 10,000 summaries, configurable by environment.
- Prune oldest completed traces; never prune running traces.
- Allow deletion of one trace and a separate confirmed purge-expired action.
- Never store authorization headers, keys, passwords, cookies, or raw tokens.

```text
METATUBE_TRACE_ENABLED=true
METATUBE_TRACE_DSN=/config/traces.db
METATUBE_TRACE_RETENTION_DAYS=30
METATUBE_TRACE_MAX_RUNS=10000
METATUBE_TRACE_MAX_EVENTS_PER_RUN=500
```

Implemented behaviour: the store is SQLite at `METATUBE_TRACE_DSN` (default
`/config/traces.db`, on the volume the deployment already mounts), with a single
writer connection and `trace_runs`/`trace_events` tables. Pruning runs at start
and every 10 minutes, and the manual `purge-expired` API requires an explicit
confirmation value. Running traces are never pruned; a trace that is never
finished is first closed as `failed` with `error_code=abandoned` after 6 hours,
which is what eventually makes it eligible for retention. A store failure
disables tracing and is recorded in the counters instead of affecting lookups.

## Server instrumentation TODO

- [x] Add `internal/trace` with thread-safe Start/Event/Finish, querying,
      pruning, redaction, and persistence.
- [x] Add middleware to accept/generate trace IDs and attach context to Gin and
      the Go request context.
- [x] Return `X-MetaTube-Trace-ID` on traced responses.
- [x] Instrument movie search/info, cache/database, provider fallback, and
      result selection.
- [x] Instrument equivalent actor search/info/enrichment stages.
- [x] Instrument provider throttle queue/wait/active/completion with configured
      concurrency, chosen delay, queue time, and run time.
- [ ] Instrument the image-fetch entry points (`engine/image.go`) with the same
      context-aware pattern.
- [ ] Instrument translation stages.
- [ ] Attach FlareSolverr solve/session/error events to the active trace.
- [x] Record HTTP status, provider failures, and empty results as structured
      events (`provider_fetch` in `engine.FetchContext` sets `http_status`).
- [ ] Record retries, challenges, parse errors, and cancellations as distinct
      structured events.
- [x] Preserve current metrics/logs; trace failure must never fail a lookup.
- [x] Include trace ID in ordinary log lines for cross-reference with `LOGS`.

## Client event-ingest API TODO

Use existing admin/API authentication, validate payload size, rate-limit per
client, and accept an idempotency key for retried events.

Payload size is enforced with `http.MaxBytesReader`, ingest is rate-limited per
client, and idempotency keys are honoured.

Admin authentication now exists: setting `METATUBE_ADMIN_TOKEN` requires a token
(`X-MetaTube-Admin-Token`, `Authorization: Bearer`, or `?token=` which plants an
HttpOnly cookie) on every `/admin` route, including all trace APIs. **It is off
by default**, and the public hostname
`https://metatube-admin.madtechinc.com/admin` was verified to be reachable from
the internet with no authentication, exposing statistics, logs, provider
throttles (writable via `PUT`), the provider list, and the database version
(`METATUBE_TOKEN` is also unset there). Protect that hostname at the proxy as
well: an identity-aware proxy, basic auth, or an IP allowlist.

- [x] `POST /admin/api/traces/start`
- [x] `POST /admin/api/traces/:traceID/events`
- [x] `POST /admin/api/traces/:traceID/finish`
- [x] `GET /admin/api/traces?kind=video|actor&...`
- [x] `GET /admin/api/traces/:traceID`
- [x] `GET /admin/api/traces/:traceID/export` (sanitized JSON)
- [x] `DELETE /admin/api/traces/:traceID`
- [x] `POST /admin/api/traces/purge-expired` (requires
      `{"confirm":"purge-expired"}`)
- [x] `GET /admin/api/trace-stats` (counters plus effective configuration)


Filters: query/text, trace ID, catalog code/name, Emby item ID, Windmill job ID,
client, provider, operation, status, component, error-only, and time range.

## Windmill integration TODO

- [ ] Add one reusable trace helper for video and actor flows.
- [ ] Keep MetaTube URL and credentials in Windmill resources/secrets.
- [ ] Video traces: job/flow ID, every enrichment component, providers,
      translation, trailer/artwork/custom fields, Emby write/refresh, final state.
- [ ] Actor traces: identify query, selected actor/provider ID, aliases,
      translation, artwork, custom fields, Emby person ID, final state.
- [ ] Use `try/finally` so failed/cancelled jobs report a final event whenever
      MetaTube is reachable.
- [ ] Idempotency key: Windmill job ID plus step name.

## Emby plugin integration TODO

- [ ] Start/preserve a trace ID for Identify and Refresh.
- [ ] Send client and operation headers to MetaTube.
- [ ] Report chosen result/provider ID.
- [ ] Report metadata fields added/updated/skipped, people changes, images,
      translation, save, and refresh results.
- [ ] Use `partial` when lookup succeeds but enrichment/write stages fail.

## Admin UI TODO

- [ ] Add top-level tabs named exactly `LOGS-METATUBE-VIDEO` and
      `LOGS-METATUBE-ACTOR`, separate from `LOGS`.
- [ ] Table columns: time, query/item, operation, client, providers, status,
      duration, Windmill job, and Emby item.
- [ ] Clicking a row opens a stage timeline/drawer without navigation.
- [ ] Group provider attempts and show throttle, lookup, FlareSolverr, parsing,
      translation, Windmill, and Emby timings separately.
- [ ] Green succeeded, amber partial/running, red failed, neutral queued; include
      text/icons so status does not depend on color alone.
- [ ] Auto-refresh every 5 seconds only while visible, plus Refresh, Pause,
      auto-follow, filters, pagination, copy trace ID, and JSON export.
- [ ] Link to matching general log lines.
- [ ] Show `Awaiting client report` when MetaTube finished but Windmill/Emby has
      not reported downstream completion.
- [ ] Desktop-first is acceptable; mobile is not a release blocker.

## Stage timeline

```text
request -> normalize/cache/database -> throttle -> provider attempt(s)
        -> FlareSolverr if required -> parse/select/fallback -> translate
        -> Windmill components -> Emby write -> Emby refresh -> final state
```

## Acceptance criteria

1. Emby movie Identify produces one video trace with provider attempts, timings,
   chosen result, and returned trace ID.
2. Actor Identify produces an actor trace and never appears in the video tab.
3. Windmill continues the original trace with job ID and per-step outcomes.
4. Provider challenge or FlareSolverr failure identifies the exact failing stage.
5. Emby update failure is `partial`/`failed`, never a false success.
6. General server logs stay in `LOGS`; new tabs contain structured traces only.
7. Trace recording adds negligible latency and cannot make lookup fail.
8. No secret appears in list, detail, export, or ordinary logs.
9. Tests cover correlation, video/actor separation, concurrent ordering,
   idempotency, redaction, retention, and failed trace storage.

## Recommended implementation order

1. Model/store, redaction, middleware, and APIs.
2. Movie/actor instrumentation plus provider/throttle/FlareSolverr events.
3. Two admin tabs and trace timeline.
4. Windmill helper and flow reporting.
5. Emby plugin reporting and end-to-end tests.

Do not claim full end-to-end completion after steps 1–3. Until Windmill and Emby
report their stages, the UI must say downstream status is unavailable.
