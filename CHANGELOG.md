# Changelog

All custom MetaTube Admin changes are recorded here. Release identifiers are
deployment revisions for this fork and do not replace upstream MetaTube tags.

## Unreleased

- Specified a clickable error index for expanded video and actor enrichment
  traces so an error count identifies the exact provider, stage, and event.
- Specified real rolling five-minute provider statistics beside SETTINGS
  throttle controls, including latency, peak concurrency, and blocking signals.
- Specified lookup-driven provider health, fallback/degradation semantics,
  observation provenance, and bounded startup/manual health checks.
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
