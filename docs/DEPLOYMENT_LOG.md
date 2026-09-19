# MetaTube Admin deployment log

Append one immutable row for every production deployment attempt. A release
can have more than one deployment row. Do not rewrite previous rows; add a
correction row when needed.

| Deployed at (UTC) | Release | Source commit | Image digest | Admin page hash | Scope and preserved services | Live verification / rollback |
| --- | --- | --- | --- | --- | --- | --- |
| 2026-09-19 19:35 | `custom-2026.09.19.1` | `91586cb` | `sha256:a3209f6b6cb8196005809962244f72a1bf87ddfe70c7699ff26ee012e99aa3cc` | `2d61d962…` | Recreated only `metatube`; preserved Postgres, FlareSolverr, provider bridge, configuration, and `traces.db` | LAN/public Admin and APIs healthy; video/actor expand-collapse verified; roll back to the preceding image recorded by the host if needed |
