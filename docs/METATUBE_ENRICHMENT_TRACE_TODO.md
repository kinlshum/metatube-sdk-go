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
- Admin UI: the `LOGS-METATUBE-VIDEO` and `LOGS-METATUBE-ACTOR` tabs with
  filters, paging, auto-follow, 5-second refresh while visible, a stage-timeline
  drawer grouped per provider and per component, redacted field-change tables,
  copy/export actions, `Show in LOGS` correlation, and `Awaiting client report`.
- Optional admin authentication (`METATUBE_ADMIN_TOKEN`) covering every
  `/admin` route, and `METATUBE_TRUSTED_PROXIES` so client IPs cannot be spoofed.
- Tests in `internal/trace` and `route` cover video/actor separation, concurrent
  event ordering, idempotency, per-run caps, retention (running traces are never
  pruned), abandoned-trace closing, redaction, store-failure safety, filters,
  tab wiring, and the payload contract the UI depends on.

Remaining, in order:

1. Correct the four post-implementation review findings in the dedicated
   handoff section below.
2. Image-fetch and translation events.
3. Reusable Windmill trace helper posting to the ingest API.
4. Emby plugin reporting.
5. FlareSolverr events forwarded from `deployment/provider-bridge/bridge.py`.

## DeepSeek corrective handoff (reviewed 2026-09-19)

The trace foundation and both admin tabs are implemented, and the targeted
packages pass their tests, but the feature is not ready to be called fully
correct or end-to-end complete. Fix these findings before adding more client
integrations.

### 1. P1: prevent a nil dereference in actor enrichment

In `engine/actor.go`, the GFriends image-injection trace constructs
`result_count` with `len(gInfo.Images)` before verifying that `gInfo` is
non-nil. A provider error may return `(nil, err)`, causing the tracing code to
panic a metadata request. This violates the requirement that tracing must never
break a lookup.

- Guard `gInfo` before reading `Images`.
- Emit `result_count: 0` when the result is nil.
- Keep the provider failure event and original error behavior intact.
- Add a unit test whose GFriends provider returns `(nil, error)` and verify no
  panic occurs.

### 2. P2: calculate `Awaiting client report` from actual downstream state

`route/admin_traces.go` currently calls `trace.RequiresReport(status)`, and
`RequiresReport` returns true for every `succeeded` or `partial` trace. The UI
therefore continues to display `Awaiting client report` even after Windmill or
Emby has reported completion.

- Derive the flag from recorded downstream evidence, not status alone.
- At minimum, distinguish: no downstream report; Windmill reported but Emby did
  not; Emby reported; and downstream completion reported.
- Prefer a persisted summary field or an efficient store query rather than
  repeatedly scanning an unbounded event collection.
- A server-only lookup may say downstream status is unavailable. A completed
  Windmill/Emby trace must not say it is still awaiting that report.
- Add route/service tests for server-only, Windmill-only, Emby-completed, and
  downstream-failed traces.

### 3. P2: make Auto-follow functional

The `traceVideoFollow` and `traceActorFollow` controls exist in
`route/admin.html`, but no JavaScript reads their checked state. Automatic
five-second list refreshes also do not refresh an open trace drawer; only a
manual refresh does.

- When Auto-follow is checked, keep the newest page selected and refresh the
  currently open drawer so new stages appear live.
- When unchecked, preserve the operator's page, scroll position, open trace,
  and current detail snapshot.
- Pause must stop both list and drawer polling. Manual Refresh must work while
  paused without silently resuming polling.
- Add UI contract tests that verify the checkbox is referenced by behavior,
  not merely present in the HTML.

### 4. P3: persist the native lookup result count

Provider events contain `details.result_count`, but server-created trace
summaries are never updated with the final result count. The drawer can show
`Results: 0` after a successful lookup.

- Update the trace summary when result selection is recorded.
- Preserve a legitimate zero-result outcome; do not use `> 0` as the only
  indication that the caller supplied a count.
- Test single-provider, all-provider, fallback, empty-result, and actor paths.

### Corrections applied (2026-09-19)

All four findings above are fixed, with the verification recorded below.

1. **Nil dereference in actor enrichment** — `engine/actor.go` now guards the
   GFriends result before reading `Images` and reports `result_count: 0` for a
   nil result while keeping the failure event. Covered by
   `engine/actor_test.go:TestGFriendsImageInjectionWithNilResultDoesNotPanic`,
   which drives a provider that returns `(nil, error)` and asserts no panic plus
   a recorded zero-image failure event.
2. **`Awaiting client report` from real downstream state** — traces now persist a
   `downstream_status` column (`windmill`, `emby`, `complete`, `failed`, empty),
   updated with the event counters in the same statement, so no event scan is
   needed. `trace.DownstreamFor` derives the state: server-only `lookup`/`test`
   traces report `unavailable`, a `succeeded`/`partial` trace that has heard from
   neither system awaits a report, one system reporting awaits the other, both
   reporting is `complete` and never awaiting, and an error-level Windmill/Emby
   event is `failed`. The list and detail APIs return the derived state
   (`downstream.status`, `downstream.awaiting_report`, `downstream.detail`), and
   the UI shows `Awaiting client report`, `windmill reported`, `Downstream
   complete`, `Downstream failed` or `Downstream n/a` accordingly. Covered by
   `internal/trace/downstream_test.go:TestDownstreamReportStates` and
   `route/admin_ui_test.go:TestTraceDownstreamStatesThroughTheAPI` (server-only,
   Windmill-only, Emby-then-Windmill complete, and downstream-failed cases).
3. **Auto-follow is functional** — `refreshTraces` reads the checkbox: when it is
   checked the newest page stays selected and the open drawer is re-fetched so new
   stages appear live (preserving the reader's scroll position); when it is
   unchecked the page, open trace and detail snapshot stay put. Pause stops both
   list and drawer polling, and Refresh works while paused without resuming
   polling. Toggling the checkbox takes effect immediately, and the status line
   reports `following newest` or `page held`. Covered by behaviour assertions in
   `route/admin_ui_test.go:TestAdminPageExposesTraceTabs` that require the
   checkbox to be read, the offset reset, the drawer refresh call, the scroll
   preservation, and the pause guard.
4. **Native lookup result count is persisted** — `trace.RunHandle.SetResult`
   writes `selected_provider`, `selected_provider_id` and `result_count`
   (including an explicit zero) whenever the engine records a selection, and
   `FinishInput.ResultCount` is now a `*int` so "no count supplied" is
   distinguishable from zero. Provider events still carry
   `details.result_count`. Covered by
   `internal/trace/downstream_test.go:TestResultCountKeepsExplicitZero` and
   `engine/actor_test.go:TestActorSearchPersistsResultCount` (single provider,
   all-provider selection, and the empty-result path), plus a real JavBus lookup
   that now shows `results=1`.

## DeepSeek handoff: expandable run, trace, step, and correlated-log viewer

### User goal

The Video and Actor trace tables must work as an operational debugger, not only
as a summary list. An operator must be able to expand a run without leaving the
table, see every related trace and ordered step, expand an individual step to
inspect its safe structured details, and open the correlated MetaTube logs for
the complete run, one trace, or one step.

The existing right-side trace drawer may remain as an optional full-detail
view, but it must not be the only way to understand a run. The primary table
needs an obvious chevron and an inline expandable detail row.

### Information hierarchy and grouping

```text
Run / workflow
  -> trace(s): lookup, identify, enrich, refresh, image, translation
       -> ordered events/steps: client, throttle, provider, fallback,
          FlareSolverr, translation, Windmill, Emby
            -> structured event detail and correlated native logs
```

For current data, a run may consist of one trace. Group multiple traces only
when they share, in priority order: `windmill_job_id`; an explicit future
`run_id`/`workflow_id`; or a `parent_trace_id` relationship. Otherwise treat
the trace as a one-trace run. Do not group merely because actor name, catalog
code, client IP, or timestamps look similar.

### Table interaction and accessibility

- Add a first column containing an actual `<button>` with a chevron,
  `aria-expanded`, `aria-controls`, and a label such as `Expand trace
  <short-id>`.
- Clicking the chevron or non-interactive summary-row area toggles an inline
  `<tr>` immediately below it. Links/buttons inside the row must not also
  toggle it.
- Preserve expanded rows and scroll position across five-second refreshes by
  trace/run ID, not DOM position. Permit multiple expanded runs.
- Render a loading row while fetching details and an inline Retry action on
  failure.
- Fetch details lazily on first expansion, cache them, and re-fetch only for a
  live run, Auto-follow, or manual Refresh. Do not download all event payloads
  for every summary row.
- Native button behavior must support keyboard Enter/Space. Add `Collapse all`
  only when at least two rows are expanded.

### Expanded run summary

Show the full run/trace ID with Copy, parent trace and child count, client and
observed IP/port, operation and normalized query, start/completion/duration,
final and downstream status, selected provider/ID, Windmill job/flow, Emby
item/person ID, and result/warning/error/event counts.

Actions: `Open full trace`, `View run logs`, `Export JSON`, and `Collapse`.

### Trace and step timeline

Render traces and their events in monotonic sequence. Each step row must show:

- sequence number;
- timestamp and delta from the previous step;
- component and stage;
- provider and attempt number where applicable;
- duration and HTTP status;
- level/status icon plus text;
- short sanitized message;
- `Details` and `View logs` actions.

Use a vertical timeline. Keep component colors consistent but never depend on
color alone. Associate provider and throttle events visually. Show retries and
failures as separate steps instead of replacing an earlier attempt.

`Details` expands directly below the step and renders safe key/value details,
field-change tables, sanitized errors, request path/status, timing, and attempt
information. Never render raw event HTML or expose authorization headers,
cookies, tokens, full metadata payloads, or image bodies.

### Correlated log viewer

#### Current log sources and retention

The homelab already has an operational central Graylog service, but this
MetaTube repository does not yet emit or search Graylog messages. Until the
integration described below is implemented, the deployed MetaTube service has
three local data sources:

1. **Structured trace events (durable):** SQLite at
   `/config/traces.db`, mounted from
   `/mnt/cache_nvme_apps/appdata/metatube-server-charleshuang233/traces.db`.
   This is the authoritative source for the run/trace/step timeline and follows
   the configured trace retention/cap limits.
2. **Admin native log buffer (ephemeral):** `internal/logbuffer` retains only
   the newest 1,000 lines in process memory. `/admin/api/logs` reads this buffer.
   It is cleared by a MetaTube restart.
3. **Container stdout (short retention):** Docker `json-file` currently keeps
   one file up to 50 MB for the `metatube` container. The application does not
   currently read that file, and its host path must never be exposed directly
   through the Admin API.

There is currently no MetaTube Graylog/GELF output or Graylog API client in
this repository. Therefore:

- use SQLite trace events to reconstruct the durable step history;
- use the native buffer only for recent correlated diagnostic lines;
- clearly label native-log retention as `Recent logs; cleared on restart`;
- never mark a trace incomplete merely because its native lines rotated away;
- do not scrape Docker log files from the browser or grant the container access
  to `/var/lib/docker`.

Graylog must be added as an optional backend through a narrow log-search
interface. Send structured GELF with `trace_id`,
`run_id`, `component`, `stage`, `provider`, and `level` fields, then query
Graylog server-side using credentials stored only in environment/secrets. The
Admin UI must work without Graylog and must never receive Graylog credentials.

#### Graylog purpose, authentication, and integration topology

> **Graylog: operational diagnostics. Store detailed MetaTube, provider
> bridge, FlareSolverr, Windmill, reverse-proxy, and container logs across
> restarts and multiple servers.**

Graylog complements, but does not replace, `traces.db`. SQLite is the durable,
authoritative workflow/run timeline; Graylog is the durable cross-service
diagnostic record used to explain what every participating service did during
that run.

Log destinations are split by traffic type. **Application** logs use Graylog1 on
Unraid `.150`, LXC hostname `graylog1`, service IP `192.168.10.155`. **System
(syslog)** logs use Graylog2 on Kraken `.170`, LXC hostname `graylog2`, service
IP `192.168.10.153`. Application logs must never be sent to Graylog2, and syslog
must never be sent to Graylog1; Graylog2 also hosts controlled Graylog/OS upgrade
testing. Graylog1 provides:

- Graylog web/API service on port `9000`, published externally as
  `https://graylog1.madtechinc.com`;
- authenticated application GELF HTTP input at
  `http://192.168.10.155:12201/gelf`;
- authenticated Vector/container GELF HTTP input at
  `http://192.168.10.155:12202/gelf` (4 MiB maximum message/frame size);
- ingestion authentication using the `X-Graylog-Token` request header; and
- Graylog administrative/API access using a dedicated administrator account
  with HTTP Basic authentication. The root-only bootstrap credential file is
  `/root/graylog-admin-credentials` inside the Graylog LXC.

The Graylog page `/system/authentication/services/create` configures human
login backends such as OIDC/LDAP. It is not the application-ingestion
credential. Normal services must never use the Graylog administrator login to
send logs.

Create a dedicated MetaTube ingestion token/input where practical. Reusing the
application input on `12201` is acceptable only if every message contains
stable routing fields and the token is independently rotatable. Store all
tokens in Docker/host secrets or protected environment variables; never commit,
display, return, export, or include them in a URL. Token rotation must update
the Graylog input and sender atomically and finish with an authenticated probe.

Server and service integration points:

| Origin | Known endpoint/location | Graylog integration requirement |
| --- | --- | --- |
| Graylog1 (applications) | Unraid `.150`; hostname `graylog1`; `192.168.10.155:9000`; GELF `12201/12202/12203`; `https://graylog1.madtechinc.com` | Production durable store for **application** logs (MetaTube, provider bridge, FlareSolverr, Windmill, Emby and container stdout), restricted streams/index sets, retention, search API, and safe UI deep links. |
| Graylog2 (syslog) | Kraken `.170`; hostname `graylog2`; `192.168.10.153:9000`; `https://graylog2.madtechinc.com` | Durable store for **system/syslog** traffic and controlled Graylog/OS upgrade testing; never an application-log target. |
| MetaTube server | `192.168.10.166:8080` | Emit structured application/provider events to authenticated GELF HTTP; search Graylog only from the server-side adapter. Include trace/run/client/provider/timing fields. |
| Provider bridge | Configured bridge endpoint (currently consumed as `192.168.10.170:9210`) | Propagate `trace_id`/`run_id`; log provider selection, request duration, throttle wait, retry, HTTP status, and sanitized failure. Never log provider cookies or authorization data. |
| FlareSolverr | Deployment endpoint discovered from runtime configuration | Emit or collect startup, Chrome/session, challenge, timeout, retry, and terminal errors. Correlate with provider and trace IDs supplied by the caller. Do not store challenge cookies. |
| Windmill | `192.168.10.170:8001` | Windmill scripts/flows send structured GELF to the authenticated application input and propagate `trace_id`, `run_id`, and `windmill_job_id` through every step. |
| Emby/plugin | Resolve base URL from the deployed Windmill/MetaTube secret or resource, not a hard-coded address | Plugin/client propagates correlation headers and reports identify, metadata merge, image, save, refresh, and downstream result events. It does not receive Graylog credentials. |
| Nginx Proxy Manager / reverse proxy | Current NPM host and proxy configuration | Forward access/error logs through Vector/container collection with upstream service, host, path template, status, latency, and request/correlation ID. Redact cookies, authorization, and query secrets. |
| Kraken/container host | `192.168.10.170`; Vector fan-out configuration under `/mnt/cache_nvme_apps/appdata/observability/vector/vector.yaml` | Vector collects Docker/container stdout and sends authenticated GELF to `12202`; enrich with server, container, image, compose project, and application fields. |
| Other MetaTube/provider hosts | Discover from deployment inventory/configuration | Install the same Vector or GELF sender contract and set a stable `server`/`node` field so one trace can be followed across machines. |

Do not hard-code deployment addresses in application logic. The addresses
above document the current topology for operators; runtime URLs and credentials
must come from configuration/secrets. Record the resolved non-secret endpoint
and node name in health/status output so configuration drift is visible.

Required common GELF fields, in addition to the correlation fields listed
later, are: `application`, `service`, `server`, `node`, `environment`,
`source_type`, `logger`, and `version`. Each integration must preserve the
original timestamp, use UTC, and keep a short human-readable message. Long or
structured details belong in sanitized fields and must respect the configured
4 MiB collector limit.

The implementation handoff must include:

1. MetaTube GELF sender with bounded timeout, retry/backoff, local failure
   accounting, and no impact on the lookup response when Graylog is down.
2. Server-side Graylog search adapter using a least-privilege Graylog API/service
   token (not the ingestion token and not a regular user password).
3. Health/status cards for ingestion reachability, last successful send, failed
   send count, queued/dropped count, last successful search, and token/config
   presence without revealing secret values.
4. A correlation test spanning MetaTube, one provider/bridge request,
   FlareSolverr when involved, Windmill, and the downstream Emby report.
5. Retention/restart verification proving Graylog evidence remains searchable
   after each source service restarts.

#### Required combined run-log experience

The expanded job/run view must expose all available evidence in one place while
keeping the sources distinct:

1. **Trace Timeline** — durable structured events from `traces.db`; this is the
   authoritative workflow record and is always shown first.
2. **Recent Native Logs** — correlated lines from the in-process MetaTube log
   buffer, explicitly labeled as short-lived and cleared on restart.
3. **Graylog Logs** — durable cross-service operational logs returned by the
   optional server-side Graylog adapter.

Use sub-tabs or clearly separated sections named exactly `TRACE TIMELINE`,
`RECENT NATIVE LOGS`, and `GRAYLOG LOGS`. Show a source badge (`TRACE`,
`NATIVE`, or `GRAYLOG`) on every result. Do not merge them into an unlabeled
stream that makes structured trace events look like raw log messages.

Every job/run, trace, and expandable step must provide:

- `View correlated logs` — opens the combined panel with the relevant IDs and
  time range already applied;
- `Open in Graylog` — opens Graylog's search UI using a server-generated safe
  deep link for the same correlation and time filters;
- `Back to trace` — restores the originating Video/Actor tab, filters,
  expansions, open step, and scroll position;
- Copy actions for trace ID, run ID, and Windmill job ID when present.

For a step, default to `trace_id` plus the event's time window. For a complete
run, query all explicit member trace IDs plus `run_id` and
`windmill_job_id` when available. The shared filter bar should control time
range, source, component, level, provider, and free text. Changing the time
range must update both Native and Graylog queries consistently.

Do not duplicate Graylog messages into `traces.db`. Correlate the systems using
the IDs and timestamps. If a native and Graylog result represent the same
message, collapse the duplicate visually using a stable fingerprint of source,
timestamp bucket, level, component, trace ID, and normalized message; retain a
way to reveal both originals. Never deduplicate structured trace events against
raw logs because they have different purposes.

Graylog is optional and failures must degrade cleanly:

- When unconfigured, show `Graylog not configured` and keep Trace/Native fully
  usable.
- When unreachable or unauthorized, show the error and last successful query
  time without failing the run detail request.
- A Graylog timeout must have a short server-side deadline and must not delay
  loading the trace timeline.
- Never infer that a workflow step failed because Graylog returned no matches.

Suggested server configuration (names may be adjusted consistently):

```text
METATUBE_GRAYLOG_ENABLED=false
METATUBE_GRAYLOG_API_URL=https://graylog.example/api
METATUBE_GRAYLOG_STREAM_ID=
METATUBE_GRAYLOG_TOKEN=<secret, server-side only>
METATUBE_GRAYLOG_EXTERNAL_URL=https://graylog.example
METATUBE_GRAYLOG_TIMEOUT_SECONDS=5
```

Add a server-side adapter interface such as `LogSearchBackend` so Native and
Graylog searches return a common safe result model without coupling the trace
routes to Graylog. The Graylog implementation must:

- use the Graylog search API, never scrape Graylog HTML;
- restrict queries to configured streams/index sets;
- escape correlation IDs and user search text using Graylog query syntax;
- enforce server-side result and time-range limits;
- redact sensitive fields before returning JSON;
- generate external deep links from the configured external base URL, not from
  request-supplied hosts;
- keep API tokens in environment/secrets and out of HTML, JSON, logs, exports,
  errors, and URLs.

Emit these structured correlation fields to Graylog where available:
`trace_id`, `run_id`, `parent_trace_id`, `windmill_job_id`, `emby_item_id`,
`client`, `component`, `stage`, `provider`, `attempt`, `level`, `duration_ms`,
and `http_status`. Preserve the human-readable message separately.

Additional acceptance tests:

- Combined view loads Trace immediately while Native and Graylog load
  independently.
- Run-, trace-, and step-scoped searches produce matching filters and time
  windows across both log backends.
- Graylog disabled, timeout, HTTP error, and empty-result cases do not break the
  trace timeline.
- Deep links contain the intended IDs/time range but no credentials.
- Duplicate Native/Graylog messages collapse while structured trace events are
  never suppressed.
- A security test confirms secrets and restricted Graylog fields cannot appear
  in responses, exports, copied URLs, or browser-visible configuration.

Support these scopes:

- **Run logs:** native log lines for every trace in the grouped run.
- **Trace logs:** lines containing the selected trace ID.
- **Step logs:** the trace ID plus a bounded window around the event (default
  event start minus two seconds through event end plus two seconds).

The action may switch to `LOGS` or open a log drawer, but must show active
scope, IDs, time range, match count, and `Back to trace`. Preserve the source
Video/Actor tab, filters, expanded rows, and scroll position when returning.

Extend native log filtering server-side rather than downloading all logs:

```text
GET /admin/api/logs?trace_id=<id>&since=<RFC3339>&until=<RFC3339>&q=<text>&limit=1000
GET /admin/api/logs?trace_id=<id1>&trace_id=<id2>&limit=1000
```

- Return effective filters and a `truncated` indicator.
- Match trace IDs exactly and safely escape pattern metacharacters.
- Continue emitting `trace=<trace-id>` in ordinary log lines.
- If none exist, display `No correlated native log lines`; do not imply the
  structured event did not occur.
- Never expose the admin token in a copied/opened URL.

When a Windmill job ID exists, show `Copy job ID`. An optional `Open Windmill
run` link may use a server-configured URL template, but do not guess Windmill
routes or place Windmill credentials in HTML/query parameters.

### API and storage requirements

- Keep the current trace detail API compatible.
- Add `GET /admin/api/trace-runs/:runID` only if grouping would otherwise fetch
  many pages. Return a bounded response and explicit `truncated` flag.
- Prefer an indexed `run_id`/`workflow_id` on new traces while retaining
  `parent_trace_id` and `windmill_job_id` compatibility.
- Avoid N+1 event queries: one bounded summary query and one ordered event query
  per expanded grouped run are acceptable.
- Existing SQLite data must migrate without deletion.
- Expanding, viewing logs, and collapsing are read-only. Deleting a run remains
  a separate confirmed action.

### Auto-follow behavior

- With Auto-follow enabled, refresh expanded running/queued runs and append new
  events without collapsing open steps.
- Stop normal polling after a terminal status, with a short grace period for
  downstream Windmill/Emby reports.
- Pause stops list, expanded-run, and log polling. Manual Refresh works while
  paused without resuming it.
- If the operator is reading older steps, show `New steps available` rather
  than forcibly scrolling. Auto-scroll only when already near the bottom.

### Required tests and acceptance

- Chevron buttons expose correct ARIA state and toggle the correct inline row.
- Multiple rows remain expanded across polling and filter changes.
- Step Details toggles without toggling the parent run.
- Run/trace/step log filters return only correlated lines and enforce limits
  and time windows.
- Grouped runs contain only explicitly related traces in sequence.
- Event details and logs remain sanitized; secrets never appear in HTML, APIs,
  exports, or copied URLs.
- Live test one actor and one video lookup: watch new steps arrive, open step
  logs, and return without losing table state.
- A provider/FlareSolverr failure displays the exact expandable failed step and
  opens the corresponding trace/time-window logs.

### Verification required before handoff completion

Run and report:

```sh
go test ./internal/trace ./route ./engine
```

Also exercise one real video lookup and one real actor lookup in `/admin` and
verify tab separation, correct result counts, live Auto-follow behavior, and
the downstream-report badge transitions. Full `go test ./...` includes live
provider/network tests and an unrelated detector fixture; record those failures
separately rather than treating them as trace regressions.


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

- [x] Add top-level tabs named exactly `LOGS-METATUBE-VIDEO` and
      `LOGS-METATUBE-ACTOR`, separate from `LOGS`.
- [x] Table columns: time, query/item, operation, client, providers, status,
      duration, Windmill job, and Emby item.
- [x] Clicking a row opens a stage timeline/drawer without navigation.
- [x] Group provider attempts and show throttle, lookup, FlareSolverr, parsing,
      translation, Windmill, and Emby timings separately. Groups are formed per
      provider (throttle + provider events) and per component; FlareSolverr,
      translation, Windmill, and Emby groups appear as soon as those events are
      recorded or reported by a client.
- [x] Green succeeded, amber partial/running, red failed, neutral queued; include
      text/icons so status does not depend on color alone.
- [x] Auto-refresh every 5 seconds only while visible, plus Refresh, Pause,
      auto-follow, filters, pagination, copy trace ID, and JSON export.
- [x] Link to matching general log lines (`Show in LOGS` filters the live log
      viewer by the trace ID).
- [x] Show `Awaiting client report` when MetaTube finished but Windmill/Emby has
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

## Graylog integration status

Implemented on this branch (see `deployment/README.md` for the operator
commands):

1. **MetaTube GELF sender** (`internal/gelf`): every structured trace record is
   mirrored to the authenticated GELF HTTP input with `application`, `service`,
   `server`, `node`, `environment`, `source_type`, `logger`, `version` plus the
   correlation fields (`trace_id`, `run_id`, `parent_trace_id`,
   `windmill_job_id`, `emby_item_id`, `client`, `component`, `stage`,
   `provider`, `attempt`, `duration_ms`, `http_status`). Delivery runs off the
   request path through a bounded queue, retries transient failures only, and
   counts drops, so an unreachable Graylog can never delay or fail a lookup.
   Records preserve the original UTC timestamp and are redacted and bounded
   before they are sent.
2. **Server-side search adapter** (`internal/logsearch`): searches
   `/api/search/universal/absolute` with a least-privilege API token that is not
   the ingestion token and not a user password. Both tokens can be mounted from
   file secrets (`METATUBE_GRAYLOG_TOKEN_FILE`, `METATUBE_GELF_TOKEN_FILE`).
   Graylog 7 requires the `fields` parameter (its answer is a CSV document with
   only those fields) and takes the stream as the `streams` parameter, so the
   adapter sends both and sorts the decoded lines newest-first. The window (24 h
   default), the result count (200/500) and the timeout (5 s) are clamped
   server-side, and the adapter reports one status per source.
3. **Health/status cards**: `/admin/api/gelf` and the ingestion strip on both
   trace tabs report reachability of the last probe, last successful send,
   failed and dropped counts, queue depth, last successful search and
   token/config presence (never a value). `POST /admin/api/gelf/probe` sends one
   probe record.
4. **Correlation test**: `internal/trace/mirror_test.go` proves start, step and
   finish records carry the run context, and the live smoke test confirms what
   the GELF input receives.
5. **Retention/restart**: Graylog is durable and independent of a MetaTube
   restart; the native buffer is labelled `Recent logs; cleared on restart`, and
   `traces.db` keeps the authoritative timeline.

Deployed on Kraken against production Graylog1 (`192.168.10.155`, API
`https://graylog1.madtechinc.com`, GELF HTTP `12203` dedicated MetaTube input;
the shared application input on `12201` and the Vector/container input on
`12202` are untouched). The search credential uses the dedicated
`metatube-search` account with the `MetaTube Search Reader` role (search
permissions only; a write attempt returns HTTP 403).

Verified live on 2026-09-19 after Graylog1 was upgraded to 7.1.9:

- Both instances report the MetaTube input `RUNNING`, each bound to its own
  address (Graylog1 `.155`, Graylog2 `.153`); a shared input copied between nodes
  needs its `bind_address` corrected, otherwise it logs
  `misfired: bind(..) failed with error(-99)`.
- MetaTube mirrors trace records to Graylog1's dedicated `12203` input and reads
  them back through the adapter; stored records carry `application`, `service`,
  `server`, `node`, `environment`, `source_type`, `logger` plus
  `trace_id`/`run_id`, `component`, `stage`, `provider` and `log_level`.
- Ingestion is asynchronous: a run-scoped search issued seconds after a lookup can
  still return no lines while the same query a minute later returns the mirrored
  steps, so the step panel's *Refresh logs* / auto-follow is the right control for
  fresh runs.
- The GELF input rejects a message with an empty mandatory `short_message` **after**
  answering `HTTP 202`, so such a record never appears in Graylog. MetaTube always
  sends a non-empty `short_message`; every other Kraken sender (provider bridge,
  Windmill, Vector) must do the same.
- The search adapter must request CSV (`Accept: text/csv`). Requesting
  `Accept: application/json` together with `fields` makes Graylog 7 return a JSON
  envelope whose `messages` array is empty, which looked like "no matches" even
  though the records were stored; the adapter now asks for CSV and decodes a JSON
  envelope defensively as well.


report their stages, the UI must say downstream status is unavailable.
