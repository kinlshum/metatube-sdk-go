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
