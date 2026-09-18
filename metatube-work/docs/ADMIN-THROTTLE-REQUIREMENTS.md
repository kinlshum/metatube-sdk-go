# MetaTube server: admin page, per-provider throttling and caching

Status: **requirements / handoff — not started**
Written: 2026-09-17
Target server: `http://192.168.10.166:8080` (MetaTube API, headless)

> This document is a self-contained build brief for the AI or human who picks up the custom
> MetaTube / provider work. It assumes no prior chat history. Every performance claim below was
> measured against the live server on 2026-09-17; the commands are included so you can re-verify
> before changing anything.

---

## 1. Why this work exists

MetaTube is a **proxy to public JAV sites**. When a client asks it for movie info, MetaTube goes
out and scrapes JavBus / AVBASE / JAV321 / JavLibrary *on its own IP* and returns a merged result.

Three separate clients hit the same MetaTube instance:

| Client | Location | Traffic profile |
|---|---|---|
| `jav-master-app` movie-info workers | Kraken `192.168.10.170`, container `jav-master-api` | sustained; thousands of queued jobs |
| Emby/Jellyfin MetaTube plugin | `metatube-work/plugin` | bursty; library scans |
| `av-scripts/scripts/javtube.bash` | ad hoc CLI | occasional, manual |

Because MetaTube itself has **no rate limiting and no configuration for it**, the only throttle in
the system today is a client-side delay inside `jav-master-app` (3–6 s between calls, keys
`metatube_request_delay_min_seconds` / `metatube_request_delay_max_seconds`). That covers **one of
three clients**. An Emby library scan bypasses it completely, and any resulting IP ban lands on
MetaTube's address — which then breaks all three clients at once.

Throttling belongs on the server that actually makes the upstream requests. That is this work.

---

## 2. Current state — verified facts

### 2.1 There is no admin surface at all

`/` returns only `{"data":{"app":"metatube","version":"v-"}}`. Probed and confirmed **404**:
`/v1/config`, `/v1/configs`, `/v1/settings`, `/v1/admin`, `/v1/status`, `/v1/info`, `/v1/limits`,
`/metrics`, `/health`, `/docs`, `/swagger/index.html`.

The only working endpoints are `/v1/movies/search`, `/v1/movies/{provider}/{id}`, `/v1/actors*`,
`/v1/images/*`, `/v1/translate`, `/v1/providers`.

### 2.2 The entire configuration surface is CLI flags

From upstream [`cmd/cmd.go`](https://github.com/metatube-community/metatube-sdk-go/blob/main/cmd/cmd.go)
(parsed with `ff` so each also reads an env var):

| Flag | Purpose |
|---|---|
| `bind`, `port`, `token`, `dsn` | wiring |
| `request-timeout` | timeout per request (global, not per provider) |
| `db-max-idle-conns`, `db-max-open-conns`, `db-auto-migrate`, `db-prepared-stmt` | database |
| `version` | — |

There is **no** rate limit, no per-provider control, no concurrency cap, no cache TTL.
Note a `token` flag already exists — reuse it to authenticate the new admin surface (§4 R6).

### 2.3 Both clients call the API identically

`jav-master-app` (`backend/main.py`, `_metatube_exact_search` and the detail fetch) and the Emby
plugin (`Jellyfin.Plugin.MetaTube/ApiClient.cs`) issue the same two requests:

```
GET /v1/movies/search?q=<code>&provider=&fallback=True
GET /v1/movies/{provider}/{id}?lazy=True
```

`provider=` is **empty**, which makes the search a broadcast to every provider. Neither client can
restrict the fan-out; the plugin's `EnableMovieProviderFilter` only filters the result list
*after* the search returns (`MovieProvider.cs`, `searchResults.RemoveAll(...)`), so it saves no
upstream work and no time.

### 2.4 The server registers 27 movie providers

`10musume, 1Pondo, AVBASE, C0930, Caribbeancom, CaribbeancomPR, DAHLIA, FC2, FC2CMADB, Gcolle,
Getchu, H0930, H4610, HEYZO, HeyDouga, JAV321, JAVFREE, JavBus, JavDB, JavLibrary, KIN8, MDC-NG,
MURAMURA, MYWIFE, PACOPACOMAMA, TOKYO-HOT, fc2hub`

Plus 4 actor providers: `AV-LEAGUE, Gfriends, Minnano-AV, XsList`.

---

## 3. The central finding: one provider causes ~97% of search latency

A broadcast search waits for the **slowest** provider. Measured, same server, same minute:

| Search | Time | Result |
|---|---|---|
| **broadcast** (`provider=`) — what both clients send | **23.78 s** | 5 hits from AVBASE, JAV321, JavBus, JavLibrary |
| `provider=AVBASE` | 0.34 s | 2 hits |
| `provider=JAV321` | 0.52 s | 1 hit |
| `provider=JavBus` | 0.65 s | 1 hit |
| `provider=JavLibrary` | **23.01 s** | 1 hit |
| `provider=JavDB` | 1.47 s | **HTTP 500** — soft-blocked, see §3.1 |

Confirmed on further codes — JavLibrary `MIDE-090` 23.21 s, `ABP-123` 11.66 s, versus JavBus
0.26 s and 0.24 s for the same codes.

**JavLibrary alone accounts for essentially all of the 23 s.** Every other useful provider answers
in well under a second. A broadcast search is therefore ~35–90x slower than it needs to be, purely
because it blocks on one slow provider, and that cost is paid by *every* movie-info job and
*every* Emby scan.

Two further observations:

- **`JavDB` returns HTTP 500** on explicit search and never appears in broadcast results — see
  §3.1, this is an IP-level soft block and is *not* fixable by retrying.
- **Nothing is cached.** Three consecutive identical searches for `SSIS-001` took 25.66 s /
  23.82 s / 23.13 s, and `MIDE-090` 23.47 / 23.52 / 23.40 s. Despite the server having a database
  (`dsn`), repeat queries re-scrape upstream every time. This is the single largest avoidable
  load on the upstream sites.

### 3.1 JavDB is soft-blocked at this server's IP — do not treat it as a bug to fix

The full error body is:

```json
{"error":{"code":500,"message":"provider bridge returned 404 Not Found"}}
```

Two things follow from that message.

**First, this deployment is not stock upstream.** "provider bridge" is not an upstream MetaTube
string. The server delegates at least some providers to an internal service — see
`PROVIDER_BRIDGE_URL` defaulting to `http://provider-bridge:9210` in
`stack/actor-resolver/resolver.py`. Port 9210 is not exposed off-host, so it could not be probed
from outside. **Whoever picks this up must locate the provider-bridge source and deployment**;
it is a second component that any throttling/caching design has to account for, and it is not in
this repo. This materially changes §5 — you may be modifying the bridge rather than, or as well
as, forking MetaTube.

**Second, the 404 is a content-level block, not a missing page.** Per the operator: javdb.com is
still reachable from that host and still serves a page, but returns **no content** — it redirects
to an error/empty page. The bridge sees empty content and reports 404, which MetaTube wraps as
500. The ~1.4–2.2 s latency confirms a real network round trip happens every time (contrast
FC2/HEYZO/MDC-NG, which reject an unsuitable code format in **0.00 s** with a 400, without
fetching).

Practical consequences:

- **Retries, backoff and longer timeouts will not help.** The request succeeds at the HTTP level;
  the site simply withholds content from this IP.
- **A Cloudflare-style solver will not necessarily help either**, since this is not a JS/captcha
  challenge — it is suppression keyed to the caller.
- **Continuing to poll it is actively harmful.** Every broadcast search makes another failing
  request to a site that is already blocking this IP, which is exactly the behaviour that turns a
  soft block into a permanent one. This is an argument for R1 (disable) being urgent, not
  cosmetic.
- Fixing it properly needs a **different egress IP** (proxy/VPN for that provider) or an
  authenticated session. Note `jav-master-app` holds a working logged-in JavDB session for its own
  direct scraping — that credential/cookie is a candidate to reuse in the bridge, but the block
  may be IP-based rather than session-based, in which case a cookie alone will not lift it.

Re-verify with:

```bash
python3 - <<'EOF'
import urllib.request, urllib.parse, json, time
U='http://192.168.10.166:8080'
def t(params,label):
    q=urllib.parse.urlencode(params); s=time.time()
    try:
        with urllib.request.urlopen(f'{U}/v1/movies/search?{q}',timeout=90) as r:
            d=json.loads(r.read()).get('data') or []
        print(f'{label:34s} {time.time()-s:6.2f}s hits={len(d)}')
    except Exception as e:
        print(f'{label:34s} {time.time()-s:6.2f}s ERR {str(e)[:60]}')
t({'q':'SSIS-001','provider':'','fallback':'True'},'broadcast')
for p in ['JavBus','AVBASE','JAV321','JavLibrary','JavDB']:
    t({'q':'SSIS-001','provider':p,'fallback':'True'}, f'provider={p}')
EOF
```

---

## 4. Requirements

Ordered by value. **R1 and R2 deliver almost all the benefit** — consider shipping them before
building the GUI, since they are pure server-side behaviour and need no UI to be useful.

### R1 — Per-provider enable/disable (highest value, likely smallest change)

Allow each of the 27 movie providers (and 4 actor providers) to be turned off, so a broadcast
search never contacts them.

- Disabling `JavLibrary` alone should cut broadcast search from ~23 s to **under 1 s**.
- Disabling `JavDB` stops the server repeatedly hitting a site that is soft-blocking its IP
  (§3.1) — this is the urgent one, since continued polling risks hardening the block.
- Must apply to the **broadcast path**, not just explicit `provider=` requests — i.e. it must
  prevent the outbound scrape, not filter results afterwards. Filtering after the fact is what the
  Emby plugin already does and it saves nothing.
- An explicit request for a disabled provider should fail fast and clearly (suggest `403` with a
  message naming the provider) rather than hanging or silently returning empty.

### R2 — Result caching with a configurable TTL

Cache search and detail results in the existing database.

- Configurable TTL, separately for search and detail; sensible default 24 h for detail (movie
  metadata is effectively immutable) and shorter for search.
- Cache negative results too, with a shorter TTL — `jav-master-app` repeatedly queries
  amateur-label codes that legitimately have no match, and each of those currently costs a full
  upstream fan-out.
- Provide a way to bypass/refresh (e.g. `?refresh=true`) so a bad cache entry can be corrected.
  The plugin already sends `lazy` and has an `Update` concept — check for overlap before inventing
  a new parameter.
- Expose hit/miss counts (see R5).

### R3 — Per-provider throttling

Rate limit MetaTube's **outbound** requests, independently per provider.

- Per provider: minimum delay between requests (or requests/minute), and a max-concurrency cap.
- Global outbound cap as a backstop across all providers.
- Queue rather than reject when the limit is hit, up to a bounded wait, so clients see slowness
  instead of errors. Note `jav-master-app` already treats a MetaTube failure as a job failure.
- Per-provider **timeout** override, so one slow provider cannot dominate a broadcast search
  (directly addresses §3). A per-provider timeout is a good cheap partial substitute for R1 if
  full enable/disable proves hard.
- Defaults must be conservative but non-breaking: the goal is protecting upstream IPs, not
  throttling a mostly-cached workload into uselessness.

### R4 — Admin web page

A simple served HTML page (no SPA build step needed) at e.g. `/admin`, plus the JSON endpoints
behind it so automation can use the same controls.

Must allow:

1. Listing all providers with current state: enabled/disabled, throttle settings, timeout.
2. Toggling enable/disable per provider.
3. Editing throttle and timeout values per provider.
4. Editing cache TTLs and clearing the cache.
5. Viewing live stats (R5).
6. A per-provider "test" button that issues one search and reports latency + status — this is how
   an operator will notice the next JavLibrary-style regression.

Settings must **persist across container restarts** (database, or a mounted config file). CLI
flags/env vars should remain as defaults that the stored config overrides.

### R5 — Observability

Per provider, since startup: request count, error count, HTTP status breakdown, mean/p95 latency,
cache hit rate, throttle-wait time. Surfaced on the admin page and as JSON.

A `/metrics` endpoint currently 404s; Prometheus format there would be a natural fit but is
optional.

### R6 — Authentication

The server already accepts a `token` flag. The admin page and its JSON endpoints must require it.
The read-only movie APIs may stay open for compatibility — all three existing clients are
currently configured **without** a token, so do not make the existing endpoints mandatory-auth
without coordinating; it would break all three at once.

---

## 5. Implementation notes

- Upstream is **[metatube-community/metatube-sdk-go](https://github.com/metatube-community/metatube-sdk-go)**
  (Go, gin, `ff` flag parsing). `cmd/cmd.go` builds the engine and router; providers live under
  `provider/<name>/`. Engine options are applied as `engine.With...` functions — that is the
  natural seam for throttle/cache wrappers.
- Prefer implementing as a **wrapper around the provider interface** (a decorator applying limit +
  cache + timeout) rather than editing 27 providers individually.
- This will be a **fork of upstream**. Record the base commit/tag and keep changes isolated so
  rebasing on upstream stays feasible. Consider upstreaming R1/R3 — they are generally useful.
- `metatube-work/stack/` currently holds only `actor-resolver`. A fork of the server should live
  alongside it with its own Dockerfile so the whole stack builds together.

### Client-side coordination (do not skip)

Once server-side throttling exists, the client-side delay in `jav-master-app` becomes redundant
and should be reduced or removed — otherwise the two delays stack and the movie-info queue is
needlessly slow. Keys to change, in `jav-master-app/backend/main.py`:

- `metatube_request_delay_min_seconds` (default 3)
- `metatube_request_delay_max_seconds` (default 6)

They are editable live at **Settings → URL / Sources → MetaTube**, so they can be set to 0 for
testing without a redeploy. Set them to 0 when validating server-side throttling, so you are not
measuring the client delay by mistake.

Also relevant: `jav-master-app` hardcodes a provider preference order
`("AVBASE", "JavBus", "JAV321", "JavLibrary", "JavDB")` when choosing which search hit to fetch
details from. If R1 lands, that list should be revisited (JavDB is blocked at this server's IP,
JavLibrary is slow).

---

## 6. Open questions for whoever picks this up

1. **How is MetaTube deployed on `192.168.10.166`?** SSH on port 22 is refused from the Kraken
   worktree host, so the container/compose definition was not locatable from this repo. It is not
   defined anywhere in `metatube-work/` — only `stack/actor-resolver` exists. Find the compose
   file and bring it into this repo before forking, or the rebuild path is undefined.
2. **Is the JavLibrary 23 s a block/ban, or just a slow site?** It varied (11.66 s–23.21 s), which
   smells like a timeout/retry rather than steady latency. If it is a Cloudflare challenge, a
   FlareSolverr-style solver may fix it properly rather than disabling the provider. Worth one
   packet capture or a direct curl to JavLibrary from that host before deciding.
3. **`JavDB`'s HTTP 500 is a soft IP block, not a config bug** — resolved during writing, see
   §3.1. Remaining question is only *how* to restore it: a dedicated egress IP/proxy for that
   provider, or reusing the working logged-in session `jav-master-app` already holds. Until then
   it should be disabled so the server stops making failing requests to a site that is blocking
   it.
4. **Where does `provider-bridge` live?** The 500's error text proves this deployment routes
   providers through an internal `provider-bridge:9210` service that is not in this repo and not
   reachable off-host. Find its source and deployment before designing anything — throttling and
   caching may belong in the bridge rather than in MetaTube.
5. **Does upstream already have relevant work in flight?** Check issues/PRs for rate limiting and
   caching before building — this is an obvious gap and may already be addressed.

---

## 7. Acceptance criteria

- [ ] Broadcast search for a cache-miss code completes in **< 2 s** with JavLibrary disabled
      (baseline today: ~23 s).
- [ ] A repeated identical search is served from cache in **< 100 ms** (baseline: no caching,
      full ~23 s every time).
- [ ] Disabling a provider demonstrably stops outbound requests to it, verified at the network
      level — not merely absent from the response.
- [ ] Per-provider throttle observably spaces outbound requests under concurrent load from two
      clients at once.
- [ ] Settings survive a container restart.
- [ ] Admin page reachable, token-protected, and shows live per-provider stats.
- [ ] All three existing clients (`jav-master-app`, Emby plugin, `javtube.bash`) keep working
      unchanged throughout.
