# MetaTube Admin next-work specification

Audience: DeepSeek/Cline implementer. Status: design approved; implementation pending.

This handoff covers four Admin/plugin gaps: identifying the exact error inside an
expanded enrichment run, showing genuine rolling five-minute provider
statistics beside each throttle setting, and allowing real provider traffic to
refresh provider health immediately, while keeping JAVDB out of automatic
library scans unless an operator explicitly requests it.

## Status (updated 2026-09-19, branch `codex/mdcng-fc2cmadb-providers`)

- **A. Error identification inside expanded traces — implemented.** The selected
  trace renders a `data-error-index` block between its header and the
  run/timeline details. It treats an event as an error when `level=error`, the
  stage ends in `_failed`, or the HTTP status is 400 or greater (a slow duration
  alone is never an error), deduplicates a failure reported by both the
  trace-level summary and the timeline, expands the first failing run node/step
  once per opened trace, and gives every entry a `Focus event` control that
  re-opens the owning group, scrolls to the exact event (`data-event-key`), and
  focuses it. A failed run is labelled differently from a successful run with
  provider failures, and a clean trace still shows an explicit `0 errors`.
  Coverage: `route/admin_ui_test.go:TestAdminPageErrorIndexControls`. The video
  and actor tabs share this drawer code. Shipped as `custom-2026.09.19.2`
  (commit `5af8278`); `deployment/e2e/admin-error-index.js` re-runs every
  acceptance case below in headless Chrome against the live admin.
- **B. Rolling `LAST 5 MIN` statistics on SETTINGS — pending.** The design,
  backend contract (`provider_windows.five_minutes`), UI, and test requirements
  in section B below are unchanged.
- **C. Lookup-driven provider health — pending.** The state and probe contract
  remains as specified below.
- **D. Selective JAVDB lookup policy — approved, pending.** Automatic scans and
  normal Identify searches exclude JAVDB; an explicit targeted manual lookup
  remains available.
- **E. Workflow — in force.** This work started from a fresh clone
  (`../metatube-admin-next-f261f67`) synchronized to `f261f671…`, and every
  release updates the changelog, release log, and deployment log.

## A. Error identification inside expanded enrichment traces

Add an error summary directly below the selected trace header and above its
run/timeline details. A badge such as `1 err` is not sufficient by itself.

Each error entry must show:

- provider or component;
- failed stage;
- HTTP status or internal error code when available;
- concise error message, timestamp, attempt number, and duration;
- a control that scrolls to and focuses the exact event in the timeline.

For example, a partial provider failure should read approximately
`JavLibrary · provider_failed · HTTP 404`, even if the overall lookup later
succeeds through another provider. Automatically expand the group containing
the failed event. Use visible text/icons in addition to color, and distinguish
an overall failure from a successful run with one or more provider failures.

Preserve the existing run → trace → step navigation and its TRACE, native
MetaTube, and Graylog panels. Implement this once for both video and actor
trace tabs.

Data rules:

- Treat an event as an error when `level=error`, its stage ends in `_failed`,
  or its HTTP status is 400 or greater.
- Deduplicate the same error when it appears in both summary and timeline
  sources; do not infer an error merely from a slow duration.
- Keep existing redaction and payload-size limits.

Acceptance tests must cover one failed provider in an otherwise successful
run, multiple distinct failures, a zero-error run, summary-to-event focus,
collapse/reopen, refresh while open, and both video and actor traces.

## B. Rolling `LAST 5 MIN` statistics on SETTINGS

Add a rightmost `LAST 5 MIN` column to each provider row. A useful compact
display is:

```text
12 calls · 11 ok · 1 err
avg 4.2 s · p95 13.4 s · peak 1
```

Show small warning badges for HTTP 429, unresolved HTTP 403, solver failure,
and timeout. Show the effective throttle policy beside the activity—for
example, JAVDB `limit 1 · delay 3–6 s`. On narrow screens render this content
as an expandable row/card rather than forcing a wide table.

Refresh every five seconds only while SETTINGS is visible, retain a manual
refresh control, and show `updated at`, stale, and load-error states.

### Backend contract

Create a real rolling five-minute window; do not relabel lifetime counters.
Use bounded time buckets (10- or 30-second buckets are acceptable) and prune
expired buckets. Report per provider:

- requests, successes, errors, current active, and peak active;
- average, p95, and average queue/throttle wait in milliseconds;
- HTTP 403, HTTP 429, timeouts;
- solver calls, solver successes, and solver errors;
- last request timestamp.

Expose this as a backwards-compatible, versioned addition to
`/admin/api/stats`, such as `provider_windows.five_minutes`; preserve all
existing fields. Bound provider/cardinality growth. Client-level breakdowns
remain on STATS.

A direct 403 followed by a successful browser-solver fallback counts as a
success with a fallback marker, not as blocking. Repeated 429s, unresolved
403s, solver failures, timeouts, or jobs ceasing to finish count as actionable
errors.

The MetaTube server sees only calls that traverse MetaTube. Direct JAV Master
requests must not be presented as combined traffic unless JAV Master later
publishes compatible telemetry. Label the scope clearly.

Tests require a deterministic clock, bucket expiry, percentile calculation,
peak concurrency, fallback semantics, backward-compatible JSON, and browser
coverage of refresh/stale/responsive behavior.

## C. Update provider health from real calls

A completed provider lookup is a health observation and must update the same
health state used by the SETTINGS light. Do not leave a provider gray while
successful traffic is already passing through it.

### State rules

- A successful provider response immediately records green/`UP`, response
  status, latency, timestamp, and source `lookup`.
- A direct HTTP 403 followed by a successful FlareSolverr/browser fallback is
  healthy. Show green with a `fallback` marker rather than red.
- HTTP 401, unresolved HTTP 403, or HTTP 429 records amber
  `CHALLENGE`/`RATE LIMITED`; these states must not be reported as fully down.
- A timeout, connection failure, parse failure that makes the provider result
  unusable, or unresolved solver failure is a failed observation.
- Do not turn a recently healthy provider red after one isolated failure.
  Record it as amber/degraded first. Red/`DOWN` requires a configurable number
  of consecutive failed observations (default 3) or a configurable failure
  window. Any later success resets the failure streak immediately.
- Cancellation caused by the caller must not count as provider failure unless
  the underlying provider request independently timed out or failed.

Extend `ProviderHealth` without breaking the current JSON fields. Recommended
additions are `state`, `observation_source` (`lookup`, `scheduled_probe`, or
`manual_probe`), `last_success_at`, `last_failure_at`, `consecutive_failures`,
and `fallback_used`. Continue populating existing `up`, `status`, `latency_ms`,
`error`, and `checked_at` fields for existing clients.

The UI must say `Last verified by lookup`, `Last verified by scheduled probe`,
or `Last verified manually`, with the timestamp and age. Gray must explicitly
say `PENDING — first check not completed`; it must not be visually ambiguous.

### Scheduled and manual probes

- Retain hourly probes for inactive providers so lack of traffic does not leave
  health stale forever.
- After server startup, run a bounded initial sweep rather than waiting almost
  an hour for the last alphabetical provider. Use at most 2–3 global workers
  and honor provider concurrency/delay policies.
- A scheduled probe must not overwrite a newer lookup observation. Apply
  observations by completion timestamp under synchronization.
- Add an optional per-provider `Check now` action. It must use the same bounded
  scheduler and throttle policy, reject/coalesce duplicates, and expose its
  source as `manual_probe`.
- Mark old observations stale after a documented threshold without erasing the
  last known result.

Instrument the common provider execution boundary so movie, actor, fallback,
and future provider calls cannot bypass the update. Avoid scattered UI-only or
provider-specific implementations.

Tests must cover successful lookup before the first scheduled probe, direct 403
plus successful solver fallback, unresolved 403/429, isolated versus repeated
failures, recovery, caller cancellation, stale observations, out-of-order probe
completion, initial-sweep concurrency limits, and compatibility of the existing
health JSON fields.

## D. Selective JAVDB policy for Emby and other clients

JAVDB must not participate in automatic Emby library scans or ordinary
all-provider Identify searches by default. It is a slow, protected fallback,
not a mandatory barrier before a fast exact match can be returned.

### Server API

- Extend movie search with an explicit exclusion, for example
  `/v1/movies/search?q=ABC-123&exclude=JavDB`.
- Preserve targeted lookup with `provider=JavDB`; it must invoke only JAVDB.
- Accept a repeatable or comma-separated exclusion list, normalize provider
  names case-insensitively, reject unknown/conflicting policy values clearly,
  and include the effective provider policy in traces.
- Keep existing clients compatible when `exclude` is absent, but update known
  bulk/automatic clients to send the safer policy.
- Add an optional staged mode: query fast providers first and invoke JAVDB only
  when no acceptable exact match exists. It must have a bounded deadline and
  must not delay a result already accepted with high confidence.

### Emby plugin behavior

- Add `Use JAVDB during automatic library scans`, default **Off**.
- Add `Use JAVDB in normal Identify search`, default **Off**.
- Add `Allow targeted JAVDB manual Identify`, default **On**.
- Add `Use JAVDB only after fast providers return no exact match`, default
  **Off** for large libraries.
- When refreshing an already identified item, use its stored provider directly.
  A JAVDB-backed item may refresh through JAVDB; AVBASE or other provider IDs
  must not trigger an unrelated JAVDB search.

The current Emby `IRemoteMetadataProvider` entry point uses the same
`GetSearchResults(MovieInfo, CancellationToken)` method for automatic and manual
searches and does not expose a trustworthy invocation-mode flag. Do not infer
the mode from timing, path shape, cancellation, or other heuristics.

Do not inject or patch Emby Web merely to add a native `Refresh Metadata
without JAVDB` submenu. That approach is brittle across Emby upgrades. Prefer:

1. a separately registered, clearly named targeted provider/action such as
   `MetaTube — Identify with JAVDB`, if Emby's supported provider registration
   produces a clean manual workflow; or
2. a targeted `Identify with JAVDB` action in `emby-custom-app` that searches
   JAVDB explicitly and applies the selected match through supported Emby APIs.

Normal manual Identify should return fast-provider results promptly, with the
targeted JAVDB action available only when those results are insufficient.

### Queue isolation and completion rules

- Automatic/bulk traffic and interactive manual lookups require separate
  bounded queues or weighted fairness. Bulk work must never starve Emby.
- All throttle waits must honor cancellation and deadlines. Remove a canceled
  waiter promptly rather than allowing abandoned work to consume a later slot.
- Deduplicate identical in-flight provider/catalog requests.
- All-provider searches must support early completion after a confident exact
  match; they must not wait for every slow provider.
- Record client, invocation policy (`automatic`, `normal_identify`,
  `targeted_javdb`, `fallback`), queue time, selected provider, and cancellation
  in trace events and five-minute statistics.

Tests must cover default exclusion, explicit targeted JAVDB, stored-provider
refresh, staged fallback, exact-match early return, cancellation while queued,
bulk versus interactive fairness, duplicate coalescing, backward-compatible
requests without the new parameter, and both plugin/manual-action workflows.

For the 57,016-item Movie AV library, acceptance requires that a scan can add
and identify new files without issuing JAVDB searches, while an operator can
still request an individual JAVDB match manually.

## E. Mandatory repository, release, and deployment workflow

Every AI or human implementer must follow this sequence:

1. Clone `https://github.com/kinlshum/metatube-sdk-go` into a new, uniquely
   named directory. Do not begin from an old worktree or copied source tree.
2. Run `git fetch --all --prune`, check out the requested branch, and
   fast-forward it from origin.
3. Record the starting commit; require a clean working tree and verify local
   `HEAD` equals the intended `origin/<branch>` before editing.
4. Read `HANDOFF.md`, this specification, `CHANGELOG.md`,
   `docs/RELEASE_LOG.md`, and `docs/DEPLOYMENT_LOG.md`.
5. Fetch again immediately before committing. Integrate newer remote work
   without force-pushing or discarding it; stop and report real conflicts.
6. Run focused tests, `go test ./...`, `git diff --check`, and real browser
   interaction tests. Record any unrelated pre-existing failure explicitly.
7. Update all three records: `CHANGELOG.md` for user-visible changes,
   `docs/RELEASE_LOG.md` for the immutable release/build row, and
   `docs/DEPLOYMENT_LOG.md` for each deployment attempt/result.
8. Commit and push normally, then fetch once more and verify the pushed remote
   commit matches local `HEAD`.
9. Deploy only pushed code and recreate only the intended service; preserve
   databases, trace storage, FlareSolverr, and provider bridges.
10. Verify the LAN and public Admin pages, page hash, APIs, trace clicks,
    container image/start time, and final clean Git status. Record rollback
    information. Never commit secrets or tokens.

## Deliverables

- Backend rolling-window metrics and compatible API response.
- Lookup-driven provider health with bounded startup/manual probes.
- Selective JAVDB server policy and Emby/manual-client controls.
- SETTINGS UI column/card and trace error index/focus behavior.
- Unit, API, and browser regression tests.
- Updated handoff, changelog, release log, and deployment log.
- Pushed commit plus documented deployment and rollback identifiers.
