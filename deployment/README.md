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

## Graylog integration (durable cross-service diagnostics)

`traces.db` stays the authoritative workflow timeline. Graylog stores the same
records durably, next to container, provider, FlareSolverr, Windmill and
reverse-proxy logs, so one run can be followed across services and across
restarts. The admin shows the three sources side by side with `TRACE`, `NATIVE`
and `GRAYLOG` badges, each with its own retention label.

Both credentials are mounted as Docker secrets and are never placed in the
environment, a response, an export, or a log. An empty secret file is valid: the
feature stays visible and reports `not configured`.

| Secret file | Environment | Purpose |
| --- | --- | --- |
| `./secrets/graylog_search_token` | `METATUBE_GRAYLOG_TOKEN_FILE` | Least-privilege Graylog API token used only by the server-side search adapter (`/api/search/universal/absolute`). |
| `./secrets/gelf_ingest_token` | `METATUBE_GELF_TOKEN_FILE` | GELF ingestion token sent as `X-Graylog-Token` to the GELF HTTP input. |

Create the files on the host (the directory is root-only):

```sh
install -d -m 700 ./secrets
printf '%s' '<graylog-api-token>' > ./secrets/graylog_search_token
openssl rand -hex 24          > ./secrets/gelf_ingest_token
chmod 600 ./secrets/graylog_search_token ./secrets/gelf_ingest_token
```

The search token must come from a dedicated account, not the Graylog admin
login and not the ingestion token:

```sh
# 1. create a role that can only search (Graylog 7: the built-in Reader role
#    lacks searches:absolute/relative/keyword, which yields HTTP 403)
curl -u admin:<password> -H 'X-Requested-By: provision' -H 'Content-Type: application/json' \
  -d '{"name":"MetaTube Search Reader","description":"Read-only search for the MetaTube admin",
       "permissions":["searches:absolute","searches:relative","searches:keyword",
                      "streams:read","messages:read","messages:analyze"]}' \
  http://192.168.10.153:9000/api/roles

# 2. create the service account (first_name/last_name are required in Graylog 7)
curl -u admin:<password> -H 'X-Requested-By: provision' -H 'Content-Type: application/json' \
  -d '{"username":"metatube-search","password":"<random>","email":"metatube@localhost",
       "first_name":"MetaTube","last_name":"Search","permissions":[],"roles":["MetaTube Search Reader"],
       "timezone":"UTC"}' \
  http://192.168.10.153:9000/api/users

# 3. create an API token for that user (tokens are keyed by the 24-character user id)
curl -u admin:<password> -H 'X-Requested-By: provision' -X POST \
  "http://192.168.10.153:9000/api/users/<user-id>/tokens/metatube-$(date +%Y%m%d)"
```

The composition file already sets the non-secret defaults:

| Variable | Default | Purpose |
| --- | --- | --- |
| `METATUBE_GRAYLOG_ENABLED` | `false` | Turns the search adapter on. |
| `METATUBE_GRAYLOG_API_URL` | `https://graylog.madtechinc.com/api` | Graylog REST base used by the adapter. |
| `METATUBE_GRAYLOG_EXTERNAL_URL` | `https://graylog.madtechinc.com` | Browser-facing base used for deep links into Graylog. |
| `METATUBE_GRAYLOG_STREAM_ID` | empty | Restricts every search to one stream (`000000000000000000000001` is the Default Stream). |
| `METATUBE_GRAYLOG_TIMEOUT_SECONDS` | `5` | Bound per search request. |
| `METATUBE_GRAYLOG_MAX_RESULTS` | `200` | Hard cap per query (server-side maximum is 500). |
| `METATUBE_GRAYLOG_MAX_RANGE_HOURS` | `24` | Hard cap on the searched window. |
| `METATUBE_GELF_ENABLED` | `false` | Mirrors every trace record to the GELF HTTP input. |
| `METATUBE_GELF_URL` | `http://192.168.10.153:12203/gelf` | Dedicated MetaTube GELF HTTP input, so its token can be rotated without touching the shared `12201` application input or the `12202` Vector/container input. |
| `METATUBE_GELF_SERVER` / `_NODE` / `_ENVIRONMENT` | `kraken` / `kraken-docker` / `homelab` | Required common fields used to tell machines apart. |
| `METATUBE_GELF_TIMEOUT_SECONDS`, `_QUEUE`, `_MAX_RETRIES` | `3`, `512`, `2` | Bounded delivery: the sender never blocks a lookup and counts drops instead. |

Health and reachability (both endpoints never return a token):

```sh
curl -sS -H "X-MetaTube-Admin-Token: $METATUBE_ADMIN_TOKEN" \
  http://192.168.10.166:8080/admin/api/gelf          # status card + counters
curl -sS -X POST -H "X-MetaTube-Admin-Token: $METATUBE_ADMIN_TOKEN" \
  http://192.168.10.166:8080/admin/api/gelf/probe    # one probe record
```

Useful checks while deploying:

```sh
# the mirrored records carry trace_id/run_id and the token travels in the header
docker logs metatube 2>&1 | grep '\[GELF\]'
# a search for one run, as the adapter performs it (Graylog 7 requires `fields`
# and answers with CSV; the stream is passed as the `streams` parameter)
curl -u "<api-token>:token" -H 'X-Requested-By: metatube' \
  'https://graylog.madtechinc.com/api/search/universal/absolute?query=run_id:"run-example"&from=2026-09-19T00:00:00.000Z&to=2026-09-20T00:00:00.000Z&limit=10&streams=000000000000000000000001&fields=timestamp,message,trace_id,run_id'
```


