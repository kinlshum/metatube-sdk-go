# Custom deployment release log

Use one row for every deployed custom build. The Git commit and deployed image
must be filled with immutable identifiers so another operator or AI can verify
which code is live.

| Release | Date | Git commit | Deployed image | Change | Verification |
| --- | --- | --- | --- | --- | --- |
| `custom-2026.09.19.1` | 2026-09-19 | `91586cb` | `sha256:a3209f6b6cb8196005809962244f72a1bf87ddfe70c7699ff26ee012e99aa3cc` | Inline expandable video/actor enrichment jobs with run, step, native-log, and Graylog detail | Focused tests passed; live page hash `2d61d962…`; click/expand/collapse verified in browser |
