# Custom deployment release log

Use one row for every deployed custom build. The Git commit and deployed image
must be filled with immutable identifiers so another operator or AI can verify
which code is live.

| Release | Date | Git commit | Deployed image | Change | Verification |
| --- | --- | --- | --- | --- | --- |
| `custom-2026.09.19.1` | 2026-09-19 | pending commit | pending deployment | Inline expandable video/actor enrichment jobs with run, step, native-log, and Graylog detail | Focused UI tests, full Go test suite, live page hash, click/expand/collapse check |

