# Changelog

All custom MetaTube Admin changes are recorded here. Release identifiers are
deployment revisions for this fork and do not replace upstream MetaTube tags.

## Unreleased

## custom-2026.09.19.5 — 2026-09-20

- Completed MetaTube2 isolation with dedicated `provider-bridge2` and
  `flaresolverr2` services, separate bridge state, ports, and restart lifecycle.
  MetaTube1/JAV Master retains the original bridge and browser solver.

- Still specified and pending: real rolling five-minute provider statistics
  beside the SETTINGS throttle controls (`provider_windows.five_minutes`).
- Specified lookup-driven provider health, fallback/degradation semantics,
  observation provenance, and bounded startup/manual health checks.

## custom-2026.09.19.4 — 2026-09-19

- Added an Admin SETTINGS policy for automatic movie-scan provider selection:
  enable/disable ordered lookup, include or exclude providers, and change their
  order without rebuilding the server. Ordered scans query sequentially and
  stop at the first match; interactive Emby Identify continues to return the
  full multi-provider candidate list.

- Added a dedicated `metatube2` Emby service at `192.168.10.167:8080`, backed
  by its own PostgreSQL data and configuration/trace volume. The existing
  `.166` service remains available to JAV Master bulk workflows.

- Fixed trace attribution so generic `python-httpx`/`python-urllib` callers are
  recorded as `python-client`, not falsely presented as Windmill. Real clients
  should send `X-MetaTube-Client` for an authoritative name.

## custom-2026.09.19.2 — 2026-09-19

- Added an error index to expanded video and actor enrichment traces. Every
  failure names its provider/component, failing stage, HTTP status or internal
  error code, message, time, attempt, and duration, and a `Focus event` control
  opens the owning run node/step, scrolls to that event, and focuses it.
- The first recorded failure expands its own run node and step once per opened
  trace, so a reader's collapse is not undone by the five-second refresh. An
  outright run failure is distinguished from a successful run that recorded
  provider failures, and a clean trace still shows an explicit `0 errors`.
- Fixed a `TypeError` that aborted the whole run → trace → step tree:
  `stepWindow()` called `getTime()` on a number that was already milliseconds,
  so the run panel rendered nothing and an error entry had no event to focus.
- Stopped the trace-level error summary from duplicating timeline failures and
  from blaming the provider that happened to be selected.
- Documented the mandatory fresh-clone/synchronize workflow and the separate
  immutable release and deployment records.
- Verified with a real headless-Chrome regression suite against the live admin
  covering all acceptance cases in `docs/METATUBE_ADMIN_NEXT_TODO.md`; the
  suite ships as `deployment/e2e/admin-error-index.js`.
- Added a mandatory fresh-clone/synchronize workflow and separate immutable
  release and deployment records to prevent stale builds from replacing newer
  code.

## custom-2026.09.19.1 — 2026-09-19

- Fixed enrichment trace rows so a job expands directly beneath the selected
  row instead of rendering its detail drawer invisibly after the table.
- Added selected-row, hover, collapse-on-second-click, and scroll-into-view
  behavior for both video and actor enrichment traces.
- Preserved the existing run → trace → step debugger and its TRACE, native, and
  Graylog log panels inside the inline expansion.
- Added UI contract checks for inline placement, visible navigation, and
  collapse behavior.
