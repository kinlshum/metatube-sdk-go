# MetaTube Admin handoff

Updated: 2026-09-18

## Repository and branch

- GitHub: `https://github.com/kinlshum/metatube-sdk-go`
- Working branch: `codex/mdcng-fc2cmadb-providers`
- Compare with main:
  `https://github.com/kinlshum/metatube-sdk-go/compare/main...codex/mdcng-fc2cmadb-providers`
- Local checkout:
  `/Users/kin/Documents/ChatGPT/Homelab/metatube-sdk-go/server`

Continue from the working branch and merge the entire branch, not only the most
recent documentation commit.

## Deployment

- MetaTube Admin: `http://192.168.10.166:8080/admin`
- Docker host: `root@192.168.10.170` (Kraken)
- Stack directory on host:
  `/mnt/cache_nvme_apps/appdata/metatube-stack`
- Compose source in this repository: `deployment/compose.yaml`

Do not commit credentials, API keys, cookies, tokens, or host-specific secret
environment files.

## Exposure and authentication

`https://metatube-admin.madtechinc.com/admin` resolves to a public address
behind an OpenResty reverse proxy and was verified (2026-09-19) to serve the
admin with **no authentication**: `/admin`, `/admin/api/stats`,
`/admin/api/logs`, `/admin/api/provider-throttles` (the `PUT` on the same path is
equally open), `/v1/db/version`, and `/v1/providers` all answered anonymously.
The served admin page is byte-identical to this branch. `METATUBE_TOKEN` is
unset there, so `/v1` is open as well.

Application-level controls added on this branch:

- `METATUBE_ADMIN_TOKEN`: when set, every `/admin` route (page, stats, logs,
  throttles, and all trace APIs) requires the token via
  `X-MetaTube-Admin-Token`, `Authorization: Bearer`, or `?token=` (which plants
  an HttpOnly cookie for the single-page UI). Off by default.
- `METATUBE_TRUSTED_PROXIES`: forwarded headers are now ignored unless a proxy
  is explicitly trusted, so a caller can no longer spoof its own address in
  statistics, logs, or traces. Behind the public proxy, set this to the proxy
  address (for example `192.168.10.1`) or remote clients all appear as the proxy
  IP.

Still required outside this repository: protect the public hostname at the proxy
(identity-aware proxy, basic auth, VPN, or IP allowlist), and consider setting
`METATUBE_TOKEN` for the `/v1` API.

## Completed on this branch

- MetaTube Admin page at `/admin`.
- Provider throttle settings with per-provider concurrency and randomized delay.
- JAVDB defaults constrained to one worker and a 3–6 second delay.
- Provider health/status checks and request-speed statistics.
- Client attribution including observed IP and source port.
- Provider/client request breakdown.
- FlareSolverr status, performance, and recent errors.
- Provider flags indicating known FlareSolverr use.
- TEST page for querying individual/all MetaTube movie providers.
- Native rolling MetaTube server log viewer without a Dozzle dependency.
- Structured HTTP and FlareSolverr error queues in the general `LOGS` tab.
- Enrichment trace foundation: `internal/trace` store/redaction/retention,
  correlation middleware with `X-MetaTube-Trace-ID`, engine instrumentation for
  movie/actor/review lookups (provider, throttle, cache, fallback, selection),
  and the `/admin/api/traces*` ingest and query APIs.
- Admin UI tabs `LOGS-METATUBE-VIDEO` and `LOGS-METATUBE-ACTOR` with filters,
  paging, auto-follow, 5-second refresh while visible, a stage-timeline drawer
  (grouped per provider and component, including redacted field-change tables),
  copy trace ID, JSON export, `Show in LOGS` correlation and
  `Awaiting client report`.
- Optional admin token (`METATUBE_ADMIN_TOKEN`) and trusted-proxy configuration
  (`METATUBE_TRUSTED_PROXIES`).

The enrichment trace work is documented in
`docs/METATUBE_ENRICHMENT_TRACE_TODO.md` under "Implementation status". Still
outstanding: image/translation events, the Windmill trace helper, Emby plugin
reporting, and FlareSolverr events from the provider bridge.


Deployment note: `deployment/compose.yaml` now sets `METATUBE_TRACE_ENABLED`,
`METATUBE_TRACE_DSN=/config/traces.db`, `METATUBE_TRACE_RETENTION_DAYS`,
`METATUBE_TRACE_MAX_RUNS`, and `METATUBE_TRACE_MAX_EVENTS_PER_RUN`. The trace
database lives on the existing `/config` volume, so it survives restarts.

The current deployed admin UI is server-level tooling. The Emby MetaTube plugin,
Windmill, and jav-master-app are clients, not owners of provider throttling.

## Important files

- `route/admin.html` — current single-page admin UI.
- `route/admin.go` — admin APIs and bridge statistics.
- `route/route.go` — route registration and request middleware.
- `engine/metrics.go` — server, client, provider, and recent-request metrics.
- `engine/throttle.go` — provider concurrency/delay controls and metrics.
- `engine/health.go` — scheduled provider health checks.
- `internal/logbuffer/logbuffer.go` — bounded native rolling log buffer.
- `deployment/provider-bridge/bridge.py` — provider bridge and FlareSolverr work.
- `docs/METATUBE_ENRICHMENT_TRACE_TODO.md` — next major feature specification.

## Next major feature

Implement two dedicated structured trace tabs:

- `LOGS-METATUBE-VIDEO`
- `LOGS-METATUBE-ACTOR`

These must remain separate from the general `LOGS` tab. Their purpose is to
trace the full lookup/identify/enrichment path across MetaTube, providers,
throttling, FlareSolverr, translation, Windmill, and Emby.

Read and follow the complete design and acceptance criteria in:

[`docs/METATUBE_ENRICHMENT_TRACE_TODO.md`](docs/METATUBE_ENRICHMENT_TRACE_TODO.md)

The key architectural requirement is correlation-ID propagation. MetaTube can
trace only its own request/provider work unless Windmill and the Emby plugin
report their downstream stages using the same trace ID. The UI must not claim an
end-to-end success when downstream status was never reported.

## Recommended implementation sequence

1. Persistent structured trace model/store, redaction, middleware, and APIs.
2. Movie and actor instrumentation, including provider throttle and
   FlareSolverr stages.
3. Separate video/actor trace tabs with filters and timeline details.
4. Reusable Windmill trace helper and reporting from both enrichment flows.
5. Emby plugin correlation/reporting and end-to-end tests.

## Deployment record

Deployed on 2026-09-19 03:18 UTC (2026-09-18 23:18 EDT) from commit `9da50ec`:

- Image `kinlshum/metatube-server-providers:local` on Kraken, built by
  `docker compose build metatube` from the GitHub branch (context
  `https://github.com/kinlshum/metatube-sdk-go.git#codex/mdcng-fc2cmadb-providers`).
- Only the `metatube` container was recreated
  (`docker compose up -d --no-deps metatube`); postgres, flaresolverr and
  provider-bridge were left running.
- Trace store: `/mnt/cache_nvme_apps/appdata/metatube-server-charleshuang233/traces.db`
  (mounted as `/config/traces.db`). It survived the container recreation.
- Compose backups kept on the host:
  `compose.yaml.backup-20260918-231650` (pre-deploy),
  `compose.yaml.backup-20260918-232049` (before the trusted-proxy fix).
- Rollback image tag: `kinlshum/metatube-server-providers:rollback-20260918-231650`
  (still the previous build).

Rollback:

```sh
cd /mnt/cache_nvme_apps/appdata/metatube-stack
cp -p compose.yaml.backup-20260918-231650 compose.yaml     # env without tracing
docker tag kinlshum/metatube-server-providers:rollback-20260918-231650 \
           kinlshum/metatube-server-providers:local
docker compose up -d --no-deps metatube
```

Verified after deploy: `/admin` serves the new page (sha256 `8fa3074e…`) on both
`http://192.168.10.166:8080` and `https://metatube-admin.madtechinc.com`, the two
trace tabs are present, `/admin/api/traces` and `/admin/api/trace-stats` answer
`enabled: true`, `/config/traces.db` is created, and a real JavBus lookup
recorded five stages (client, throttle, provider 296 ms, result selection,
database save) and showed `awaiting_report`.

## Safety and behavior requirements

- Trace storage failure must never make a metadata lookup fail.
- Never store authorization headers, passwords, API keys, cookies, or raw
  tokens in trace events or exports.
- Keep acquisition/download/file-transfer behavior out of MetaTube Admin; it
  belongs to jav-master-app.
- Preserve the existing general logs, provider settings, stats, and TEST page.
- Use 5-second auto-refresh only while a trace tab is visible, with a manual
  Refresh and Pause control.
- Test movie/actor separation, concurrent event ordering, idempotency,
  redaction, retention, and partial failures before deployment.

## Recent commits to retain

- `37f8e49` — video/actor enrichment trace design and TODO.
- `98000bb` — native rolling logs and related admin work.

Review the complete branch history for earlier provider, health, statistics,
FlareSolverr, and admin changes.
