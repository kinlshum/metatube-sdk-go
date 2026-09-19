# MetaTube all-in-one stack

This Compose project deploys the custom MetaTube server, provider bridge,
FlareSolverr, and PostgreSQL as one managed stack while keeping each service
isolated in its own container.

```sh
docker compose -f compose.yaml up -d --build
```

MetaTube remains available at `http://192.168.10.166:8080`. FlareSolverr is
available at port `8191` for diagnostics, while provider requests use the
private Compose network.

The existing MetaTube configuration and PostgreSQL database use their current
Kraken bind mounts and survive stack recreation.

The provider bridge is included in this deployment directory so building the
stack does not require credentials for a second repository.

## Environment variables

Set these on the host (or in a host-only `.env` file next to `compose.yaml`,
which must never be committed):

| Variable | Purpose |
| --- | --- |
| `METATUBE_TOKEN` | Bearer token required by the `/v1` metadata API. Empty disables authentication entirely. |
| `METATUBE_ADMIN_TOKEN` | When set, every `/admin` route (page, stats, logs, throttles and traces) requires this token. Empty leaves the admin unauthenticated. |
| `METATUBE_TRUSTED_PROXIES` | Proxy IPs whose `X-Forwarded-For` may be trusted for client attribution. Defaults to the OpenResty proxy (`192.168.10.172,192.168.10.1`). Set it to an empty value in `.env` to trust nothing, so client IPs always come from the connection. |

Admin token usage, once set:

```sh
# one-time browser bootstrap: plants an HttpOnly cookie for the single-page UI
https://<host>/admin?token=<METATUBE_ADMIN_TOKEN>

# or per request
curl -H "X-MetaTube-Admin-Token: $METATUBE_ADMIN_TOKEN" https://<host>/admin/api/stats
curl -H "Authorization: Bearer $METATUBE_ADMIN_TOKEN" https://<host>/admin/api/stats
```

If the stack is exposed through a reverse proxy (for example
`https://metatube-admin.madtechinc.com`), set `METATUBE_TRUSTED_PROXIES` to the
proxy address so statistics and traces show the real client, and make sure the
proxy itself enforces authentication (basic auth, an identity-aware proxy such
as Cloudflare Access, or an IP allowlist). The application-level admin token is
a second layer, not a replacement for protecting a public hostname.

## Enrichment traces

`METATUBE_TRACE_ENABLED`, `METATUBE_TRACE_DSN`, `METATUBE_TRACE_RETENTION_DAYS`,
`METATUBE_TRACE_MAX_RUNS`, and `METATUBE_TRACE_MAX_EVENTS_PER_RUN` control the
structured enrichment traces described in
`../docs/METATUBE_ENRICHMENT_TRACE_TODO.md`. The trace database lives at
`/config/traces.db` on the existing `/config` volume.

