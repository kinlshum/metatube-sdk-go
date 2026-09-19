# Changelog

All custom MetaTube Admin changes are recorded here. Release identifiers are
deployment revisions for this fork and do not replace upstream MetaTube tags.

## custom-2026.09.19.1 — 2026-09-19

- Fixed enrichment trace rows so a job expands directly beneath the selected
  row instead of rendering its detail drawer invisibly after the table.
- Added selected-row, hover, collapse-on-second-click, and scroll-into-view
  behavior for both video and actor enrichment traces.
- Preserved the existing run → trace → step debugger and its TRACE, native, and
  Graylog log panels inside the inline expansion.
- Added UI contract checks for inline placement, visible navigation, and
  collapse behavior.

