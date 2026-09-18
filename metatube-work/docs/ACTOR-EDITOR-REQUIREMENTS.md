# Actor editor and DB-driven MetaTube actor substitution

Status: **requirements / handoff — not started**
Written: 2026-09-17
Owner surfaces: `jav-master-app` ACTORS tab (editor), `metatube-work/plugin` (MetaTube actor
substitution), `metatube-work/stack/actor-resolver`

> This document is a self-contained build brief for the AI or human who picks up the custom
> MetaTube / actor-identity work. It assumes no prior chat history. Every claim below was verified
> against the repository and the live deployment on 2026-09-17; the files, line numbers and
> commands are included so you can re-check before changing anything.
>
> See also [ADMIN-THROTTLE-REQUIREMENTS.md](ADMIN-THROTTLE-REQUIREMENTS.md) for the MetaTube
> server-side work, and `jav-master-app/docs/jav-master-functional-spec.md` §ACTORS for the
> product direction (alias editor, conflict resolution, version history).

---

## 1. Why this work exists

Actor identity data is currently split across three places, and only one of them is editable:

| Where | What it holds | How it is changed |
|---|---|---|
| PostgreSQL (`actor_catalog`, `actors`, `actor_aliases`) | 1,707 canonical actors, aliases, birth data, measures, site profiles | Only `Enrich` / `Update Emby` from the ACTORS tab, one actor at a time, overwrite-style |
| `metatube-work/plugin/config/JAV-ACTOR-SUB.ini` | 4,902 lines of `source=canonical` substitutions — the operator's real corrections | Hand-edited in VS Code, then pushed by a shell script |
| `metatube-work/stack/actor-resolver/overrides.json` | 7 hand-written actor overrides used by the online resolver | Hand-edited |

The operator edits the same identity facts in two formats and has to remember which one wins. The
request is: **edit actors once in the ACTORS tab, save to the database, and let MetaTube derive its
actor substitution from that database** instead of a hand-maintained file that needs a `jq` rewrite
and an Emby restart per change.

---

## 2. Current state — verified facts

### 2.1 The ACTORS tab is read-only

- `jav-master-app/src/views/ActorsView.vue` renders a sortable, searchable, paginated table and two
  per-actor buttons, `Enrich` and `Update Emby`. The expanded row shows aliases, overview and site
  profiles. There is no input field, no save path, and no delete or merge anywhere in the view.
- `jav-master-app/src/api/actors.ts` exposes exactly three calls: `listActors`, `enrichActor`,
  `updateActorEmby`.
- `jav-master-app/backend/main.py` actor routes: `GET /actors` (line 8790),
  `POST /actors/{actor_id}/enrich` (8964), `POST /actors/{actor_id}/update-emby` (8969).
  `enrich_actor()` (8833) is the **only** writer of actor data: it resolves the name through the
  actor-resolver, then runs `UPDATE actors … metadata_status='completed', metadata_source='metatube'`
  (8921-8934) and `INSERT INTO actor_aliases (actor_id,alias,source_site) VALUES (…,'metatube')
  ON CONFLICT DO NOTHING` (8935-8940). `overview` is written unconditionally; the other columns use
  `COALESCE`, so an existing value survives an enrichment run.

### 2.2 The actor tables exist only in the live database

`GET /actors` joins `actor_catalog c` with `actors a` and returns `c.*` plus `a.birth_date`,
`a.overview`, `a.updated_at` (8820-8826). Observed columns:

- `actor_catalog` — `id, western_name, japanese_name, emby_name, birth_year, country_code,
  photo_url, aliases (jsonb array), site_profiles (jsonb object), movie_count`
- `actors` — `id, birth_date, overview, updated_at`, plus the columns written by `enrich_actor`:
  `height_cm, bust_cm, waist_cm, hip_cm, cup_size, blood_type, debut_date, social_links, tags,
  metadata_status, metadata_source`
- `actor_aliases` — `actor_id, alias, source_site, created_at`

Note that `actor_catalog.aliases` is what the ACTORS search filters on (`(c.western_name ILIKE %s OR
c.japanese_name ILIKE %s OR c.emby_name ILIKE %s OR c.aliases::text ILIKE %s)`, 8806-8807), while
`actor_aliases` is the per-alias table `enrich_actor` appends to — the two must be kept in step.

**None of these three tables is created anywhere in this repository.** A repo-wide search for
`CREATE TABLE … actor` and for the table names finds only the reads and writes in
`jav-master-app/backend/main.py`; the DDL was applied out-of-band to the Kraken database. A fresh
database therefore cannot serve the ACTORS tab at all, which makes schema ownership part of this
work (R2).

Re-verify the shape and the row count with:

```bash
curl -s 'http://192.168.10.170:7808/master-api/actors?page_size=1&sort=movies&direction=desc' \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('total', d['total']); print(sorted(d['items'][0]))"
# -> total 1707
# -> ['aliases','birth_date','birth_year','country_code','emby_name','id','japanese_name',
#     'movie_count','overview','photo_url','site_profiles','updated_at','western_name']
```

### 2.3 The MetaTube plugin already has two places to get actor identity

In `metatube-work/plugin/Jellyfin.Plugin.MetaTube`:

- `Configuration/PluginConfiguration.cs` — `EnableActorSubstitution` (line 308),
  `ActorRawSubstitutionTable` (316, the textarea shown in the Emby settings page),
  `ActorResolverUrl` (128, default `http://192.168.10.170:9211`), `ReuseExistingEmbyActors`
  (134, default true).
- `Helpers/SubstitutionTable.cs` — parses the table as `key=value` lines into a case-insensitive
  dictionary. A line with no `=` (or an empty right-hand side) maps the source name to *null*, which
  `Substitute(IEnumerable<string>)` turns into "drop this actor" — the behaviour the settings-page
  help text describes.
- `Providers/MovieProvider.cs` (101-107) — applies the substitution table first, then calls
  `ResolveCanonicalActorNames()`. That resolves each remaining name to an existing Emby person when
  possible and otherwise asks `{ActorResolverUrl}/resolve?name=…` for `data.name` (324-345).

Both paths exist today and neither reads PostgreSQL: the table comes from the `.ini` file, the
resolver comes from `overrides.json` plus live provider scraping.

### 2.4 The substitution table is a hand-maintained file with a scripted push

- Source of truth today: `metatube-work/plugin/config/JAV-ACTOR-SUB.ini` — **4,902 lines, 3,183 of
  them mappings**, grouped into actors separated by blank lines, with hand-written `# duplicates`
  comments. It also contains entries a naive regeneration would silently lose: line 1653 ends with a
  stray `\`, and `AIKA（三浦あいか)` (half-width closing paren) and `AIKA（三浦あいか）` both exist as
  separate keys. `metatube-work/plugin/config/README.md` documents the file as the version-controlled
  source.
- Distribution: `av-scripts/scripts/update_actor_map.bash` — validates the file (warns about lines
  with no `=` and about duplicate source aliases, "the last entry wins"), `scp`s it to `.170`, `.150`
  and `.210`, then on Kraken rewrites
  `/mnt/cache_nvme_apps/appdata/EmbyServer/plugins/configurations/MetaTube.json` with
  `jq --rawfile` setting `.ActorRawSubstitutionTable`, `.EnableActorSubstitution = true` and
  `.ActorResolverUrl = "http://192.168.10.170:9211"`, restarts the `EmbyServer` container, waits for
  Emby, and finally asserts that the installed mapping count equals the local one.
- `av-scripts/scripts/update_metatube_actor_sub.sh` is an older variant of the same flow
  (`JAV-ACTOR-SUB.txt` → `MetaTube.json` → `docker restart EmbyServer`).
- The resolver itself (`metatube-work/stack/actor-resolver/resolver.py`) reads
  `OVERRIDES_FILE=/data/overrides.json` (7 actors), scrapes providers behind FlareSolverr, and caches
  results to `/data/cache.json`. It is the service the plugin calls at `192.168.10.170:9211`.

### 2.5 The generation rule is mechanical, with three edge cases

The `.ini` target strings match `actor_catalog.emby_name`, so the table is a deterministic projection
of the database. The live actor `Leona Fujisaki / 藤咲れおな` carries
`emby_name = "Leona Fujisaki (JAP、1998、藤咲れおな)"`, and the file maps every alias of that actor to
exactly that string. Rule: for each actor, emit `alias=<emby_name>` for every alias in
`japanese_name`, `western_name`, `emby_name` itself, and each `actor_aliases.alias`.

Edge cases that must be handled explicitly, not by accident:

1. **Duplicate source aliases across actors** — the current file resolves them by order ("last entry
   wins"), so generation needs a defined tie-break and the push script's duplicate warning must stay
   quiet.
2. **Deliberate suppression** — the plugin's blank/`=`-less line deletes a source actor. The editor
   needs a first-class "suppress this name" state; a deleted alias must not be re-added by the next
   `Enrich` run (today `enrich_actor` re-inserts whatever the resolver returns).
3. **Legacy content** — malformed lines, full-width/half-width duplicates and `# duplicates` markers
   exist in the hand-edited file; decide whether they are preserved, normalised or dropped, and say
   so in the generated header.

---

## 3. Requirements

**R1 — The database is the editing source of truth.** Every actor fact the editor can change is
stored in PostgreSQL and re-read from the API; no browser-only state, no file as the primary store.
The `.ini` becomes a generated artifact (or disappears entirely, see R7).

**R2 — The backend owns the actor schema.** Add idempotent bootstrap DDL to
`jav-master-app/backend/main.py` next to the existing `CREATE TABLE IF NOT EXISTS …` block:
`actors`, `actor_catalog`, `actor_aliases`, each guarded with `CREATE TABLE IF NOT EXISTS` plus
`ALTER TABLE … ADD COLUMN IF NOT EXISTS` for every column listed in §2.2. On the live database this
must be a no-op (the tables exist); on a fresh database it must reproduce the current shape. Do not
invent new columns for existing data — add only what R4/R8 need.

**R3 — Editing endpoints.** Alongside the existing routes, follow the file's own conventions
(`PATCH`/`PUT` for updates, `POST` for actions, `DELETE` for removal):

- `PATCH /actors/{actor_id}` — western name, Japanese name, Emby name, country code, birth year,
  birth date, photo URL, overview, measures, debut date, social links, tags.
- `POST /actors/{actor_id}/aliases` and `POST /actors/{actor_id}/aliases/delete` — add/remove a
  single alias (deletes are recorded, not hard-deleted, so enrichment cannot resurrect them).
- `POST /actors/{actor_id}/merge` — move aliases, filmography links and the Emby person id onto a
  target actor, then delete the loser. The functional spec calls this "conflict resolution"; the
  Editor needs it because duplicate identities are the common real-world case.
- `DELETE /actors/{actor_id}` — remove the actor, its aliases and its links; offer to emit a
  suppression entry so MetaTube stops re-creating the name.

Every write must update `actor_catalog` (the search projection, §2.2) and `actor_aliases` in the
same transaction, or the tab's own search goes stale.

**R4 — Provenance and locking.** Manual edits must survive the next `Enrich`:

- record provenance per value (`metadata_source='manual'`, alias `source_site='manual'`),
- add a lock (per actor, or per field) that makes `enrich_actor()` skip locked values — today
  `overview` is overwritten unconditionally and aliases are re-inserted,
- report what an enrichment run skipped so the operator can tell "nothing new" from "locked".

**R5 — Suppression is a first-class state.** A removed alias is remembered as suppressed rather than
deleted, and the generator emits it in the plugin's delete form (blank target). This is the only way
to keep a wrong source name out of Emby permanently.

**R6 — Deterministic generation.** `GET /actors/export.ini` (plus a preview in the ACTORS tab)
renders the substitution table from the database: stable sort, no timestamps inside the mapping, a
header comment carrying generation time and mapping count so the push script's count check still
means something, CRLF-free output, and the exact `key=value` format `SubstitutionTable.Parse`
accepts. Same database state must always produce byte-identical output.

**R7 — One delivery path to MetaTube.** Pick one, and make the other explicit in the docs:

- **Route A (file):** generate `JAV-ACTOR-SUB.ini` from the database and change
  `av-scripts/scripts/update_actor_map.bash` to consume the generated file (or a `curl` of the export
  endpoint) instead of the hand-edited one. Keeps today's `jq` + `EmbyServer` restart flow; the cost
  is a restart per change.
- **Route B (resolver):** teach `metatube-work/stack/actor-resolver/resolver.py` to read
  `actor_catalog` + `actor_aliases` (with a short cache) instead of only `overrides.json`. The plugin
  already calls the resolver for every actor name, so DB edits take effect with **no** `.ini`, no
  `jq` and no Emby restart. The plugin's substitution table then remains only for legacy one-offs and
  suppressions.

Route A matches the operator's stated plan; Route B is the cheaper steady state. They can coexist.

**R8 — Audit trail.** Every edit, merge, delete and suppression records who/when/what (a small
`actor_alias_edits`-style table is enough; there is no user model in this app, so "who" means the
request source). The functional spec already promises version history and rollback for aliases.

**R9 — Never break the plugin contract.** The generated table must stay valid for
`SubstitutionTable.Parse` (line-based, `=`, blank target = delete), and the count the push script
verifies must be computed the same way (non-comment, non-blank lines containing `=`).

---

## 4. Suggested delivery order

**Phase 1 — the editor (self-contained, no MetaTube change).** R2 schema bootstrap, R3 endpoints
(edit + aliases + merge + delete), R4 provenance/locking, R8 audit, and the ACTORS tab UI: inline
fields with a dirty indicator and Save, alias chips with `×` plus an add box, `Merge into…`,
`Delete`, and an `Enrich` that reports skipped/locked values. This phase is worth shipping on its own
— it turns the tab into the place where identity data is corrected, with no Emby side effects.

**Phase 2 — the bridge.** R6 export endpoint (+ preview/download buttons in the ACTORS tab) and R7
Route A and/or Route B. Phase 2 also absorbs R5 suppression and the one-off migration that folds the
existing 4,902-line `.ini` into the database: parse it, match keys to actors, and insert what is
missing as `source_site='legacy-ini'` so nothing the operator wrote by hand is lost. That migration
must be reviewable — dump the unmatched keys and decide them explicitly rather than dropping them.

Phase 1 and Phase 2 are independent enough to be separate requests, and Phase 1 does not need the
MetaTube deployment at all.

---

## 5. Acceptance criteria

1. Editing an actor's names/birth data/overview in the ACTORS tab persists after a browser reload and
   is visible through `GET /actors`.
2. Adding an alias makes it searchable in the ACTORS search box (proves `actor_catalog.aliases` and
   `actor_aliases` were both updated).
3. `Enrich` on a manually edited actor does not revert the manual values and reports what it skipped.
4. Merging two actors moves their aliases and filmography to the target, leaves exactly one actor
   row, and leaves no alias pointing at the deleted id.
5. Deleting an actor removes it from the table and search; a suppressed name does not come back after
   `Enrich`.
6. The export endpoint returns `key=value` lines that `SubstitutionTable.Parse` accepts, is
   byte-stable for unchanged data, and its mapping count matches the number of `=` lines.
7. Whichever of Route A / Route B is chosen: changing one actor in the tab changes the resolved actor
   name in Emby for a re-scanned title, and the push script's count check still passes.
8. `npm run build` is clean, the app deploys with `./scripts/deploy-kraken.sh`, and the affected
   routes are verified live (ACTORS tab plus one Emby scan).

---

## 6. Risks and traps

- **`Enrich` overwrites work.** `enrich_actor()` writes `overview` unconditionally and re-inserts
  every alias the resolver returns (8927-8940). Without R4, an enrichment run silently undoes manual
  edits. Do not start the editor without the lock/provenance work.
- **Two alias stores.** Search reads `actor_catalog.aliases`; enrichment writes `actor_aliases`.
  Update both in one transaction (R3) or the tab will show actors that its own search cannot find.
- **Nothing in this repo currently writes `actor_catalog`.** Whatever seeded it (1,707 rows) and keeps
  `movie_count`/`site_profiles` current is not in this repository. Before letting the editor write to
  it, find out whether an external importer could overwrite manual columns; if it exists, it needs the
  same lock respect as `enrich_actor()`.
- **The `.ini` is not clean data.** Duplicates (last wins), full-width/half-width key variants,
  `# duplicates` comments and at least one malformed line (§2.4) mean the migration needs a diff
  report, not a silent import.
- **Emby restart cost (Route A).** Every push restarts `EmbyServer`; batching changes and pushing on
  demand is better than pushing per saved actor.
- **Plugin ordering matters.** The substitution table runs *before* the resolver (MovieProvider 101-107).
  If both the generated table and a DB-backed resolver are active, an alias with a substitution entry
  never reaches the resolver — decide which one owns canonicalisation to avoid contradictory results.
- **Resolver cache.** `/data/cache.json` with `CACHE_TTL=604800` (7 days) is keyed by lookup, not by
  database version; a Route B implementation needs an invalidation rule, or edits appear to do nothing
  for a week.
- **Do not touch the other stacks.** `jav-dl-db-stack/app/models.py` has its own unrelated `actors`
  table (integer id, `ini_name`); it is a different database and not part of this work. The MetaTube
  and Emby projects stay separate from `jav-master-app` (AI-HANDOFF hard rule).

---

## 7. Verification commands

```bash
# Live actor shape and count (through the deployed API on Kraken)
curl -s 'http://192.168.10.170:7808/master-api/actors?page_size=1' \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['total'], sorted(d['items'][0]))"

# The substitution file the plugin receives today: 4,902 lines, 3,183 mapping lines.
wc -l metatube-work/plugin/config/JAV-ACTOR-SUB.ini
awk '/^[[:space:]]*($|#)/ { next } index($0, "=") > 0 { count++ } END { print count + 0 }' \
  metatube-work/plugin/config/JAV-ACTOR-SUB.ini

# Installed table in Emby (MetaTube.json): flags, then the mapping count the push script checks
ssh root@192.168.10.170 \
  "jq -r '.EnableActorSubstitution, .ActorResolverUrl' \
     /mnt/cache_nvme_apps/appdata/EmbyServer/plugins/configurations/MetaTube.json"
ssh root@192.168.10.170 \
  "jq -r '.ActorRawSubstitutionTable' \
     /mnt/cache_nvme_apps/appdata/EmbyServer/plugins/configurations/MetaTube.json" \
  | awk '/^[[:space:]]*($|#)/ { next } index($0, "=") > 0 { count++ } END { print "installed mappings: " count + 0 }'

# Resolver behaviour for one name (returns {data:{name:…}})
curl -s 'http://192.168.10.170:9211/resolve?name=%E8%97%A4%E5%92%B2%E3%82%8C%E3%81%8A%E3%81%AA' \
  | python3 -m json.tool | head -20
```

Frontend/back-end validation before deploying: `npm run build` (runs `vue-tsc` then `vite build`)
and `python3 -c 'import ast, pathlib; ast.parse(pathlib.Path("backend/main.py").read_text())'`.
Deploy only with `./scripts/deploy-kraken.sh`.

---

## 8. Open decisions for the operator

1. **Delivery route (R7):** generate the `.ini` and keep the `jq` + restart flow (Route A), make the
   resolver read PostgreSQL so no file/restart is needed (Route B), or both.
2. **Lock scope (R4):** lock the whole actor after a manual edit, or only the fields that were edited.
3. **Suppression semantics (R5):** should deleting an alias mean "stop MetaTube using this name"
   (blank-target entry) or only "stop showing it in JAV Master"?
4. **Migration policy (§4 Phase 2):** import the existing `.ini` keys into the database as legacy
   aliases, or regenerate the file from the database and archive the hand-edited one for diffing.




