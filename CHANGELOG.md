# Changelog

All custom MetaTube Admin changes are recorded here. Release identifiers are
deployment revisions for this fork and do not replace upstream MetaTube tags.

## Unreleased

- Added an error index to expanded video and actor enrichment traces. Every
  failure names its provider/component, failing stage, HTTP status or internal
  error code, message, time, attempt, and duration, and a `Focus event` control
  opens the owning run node/step, scrolls to that event, and focuses it.
- The first recorded failure expands its own run node and step once per opened
  trace, so a reader's collapse is not undone by the five-second refresh. An
  outright run failure is distinguished from a successful run that recorded
  provider failures, and a clean trace still shows an explicit `0 errors`.
- Still specified and pending: real rolling five-minute provider statistics
  beside the SETTINGS throttle controls (`provider_windows.five_minutes`).
- Specified lookup-driven provider health, fallback/degradation semantics,
  observation provenance, and bounded startup/manual health checks.
- Added a mandatory fresh-clone/synchronize workflow and separate immutable
  release and deployment records to prevent stale builds from replacing newer
  code.
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
