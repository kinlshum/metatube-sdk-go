# metatube-work

Custom MetaTube / Emby-Jellyfin provider work for the homelab JAV stack.

> This work moved here from the private `kinlshum/Homelab` repository on 2026-09-17, where it lived in
> that repo's `metatube-work/` directory. It is kept under a `metatube-work/` folder at this
> repository's root on purpose: every path these documents reference — and the one
> `av-scripts/scripts/update_actor_map.bash` installs from (`metatube-work/plugin/config/JAV-ACTOR-SUB.ini`,
> `metatube-work/stack/actor-resolver/…`) — stays exactly as written. The Go SDK tree around it is the
> upstream `metatube-sdk-go` code, unmodified.

| Path | Contents |
|---|---|
| `plugin/` | Emby/Jellyfin MetaTube plugin (C#), including the custom `JAV.Custom.Provider` |
| `stack/actor-resolver/` | actor name resolution service |
| `docs/` | requirements and handoff notes |

## Live deployment

The MetaTube API server runs at **`http://192.168.10.166:8080`**. It is consumed by three
clients: `jav-master-app` (Kraken), the Emby/Jellyfin plugin in `plugin/`, and
`av-scripts/scripts/javtube.bash`.

> The server's own container/compose definition is **not currently in this repo** — see the open
> questions in the requirements doc below.

## Planned work

- **[Admin page, per-provider throttling and caching](docs/ADMIN-THROTTLE-REQUIREMENTS.md)** —
  requirements / handoff brief, not started. Includes measured evidence that a single provider
  (JavLibrary) causes ~97% of search latency, and that nothing is cached.
- **[Actor editor and DB-driven actor substitution](docs/ACTOR-EDITOR-REQUIREMENTS.md)** —
  requirements / handoff brief, not started. Turns the JAV Master ACTORS tab into the editing surface,
  makes PostgreSQL the source of truth for actor identity, and has MetaTube derive its actor
  substitution from that database — either by generating `plugin/config/JAV-ACTOR-SUB.ini` (the
  current `jq` + Emby-restart path) or by pointing the actor-resolver service at PostgreSQL, which
  needs neither.
- **[Actor identity and substitution flow](../docs/ACTOR_IDENTITY_SUBSTITUTION_FLOW.md)** — current
  actor database, INI replacement, Emby plugin path, and the planned database-driven export flow.
