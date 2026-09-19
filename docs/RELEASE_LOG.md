# Custom deployment release log

Use one row for every deployed custom build. The Git commit and deployed image
must be filled with immutable identifiers so another operator or AI can verify
which code is live.

Runtime deployment attempts and verification belong in
[`DEPLOYMENT_LOG.md`](DEPLOYMENT_LOG.md); do not use this release table as a
replacement for that operational history.

| Release | Date | Git commit | Deployed image | Change | Verification |
| --- | --- | --- | --- | --- | --- |
| `custom-2026.09.19.1` | 2026-09-19 | `91586cb` | `sha256:a3209f6b6cb8196005809962244f72a1bf87ddfe70c7699ff26ee012e99aa3cc` | Inline expandable video/actor enrichment jobs with run, step, native-log, and Graylog detail | Focused tests passed; live page hash `2d61d962…`; click/expand/collapse verified in browser |
| `custom-2026.09.19.2` | 2026-09-19 | `5af8278` | `sha256:0129e6d41e6c522e17b3e7c02cec572a20940aa6f83564414ad5c2f4b945908c` | Expanded-trace error index with `Focus event`, plus fixes for the run/step tree `TypeError` and duplicated trace-level errors | `go test` for engine/route/internal passed; live page hash `2fb1ccd4…` on LAN and public; 18/18 headless-Chrome checks passed |
