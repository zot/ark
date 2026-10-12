# App activation and loading

Frictionless apps register with the ark runtime by symlink, and it shows.
`linkapp` links each app's `app.lua`, its directory, and every viewdef into
aggregation points under `~/.ark/lua` and `~/.ark/viewdefs`; at least one app
(`apps/mcp/app.lua:37-52`) then adds its own `ln -sfn` on top. Three problems
ride together, and one design move settles all three: make activation a single
directory link per app and have the runtime read apps where they sit.

## Status

- [x] ~~**Item 1 — runtime discovers apps per directory.**~~ **LANDED (2026-10-07 — `#138`.)**
- [x] ~~**Item 2 — per-app static serving.**~~ **LANDED (2026-10-07 — `#138`.)**
- [x] ~~**Item 3 — the bundle ships no symlinks.**~~ **LANDED (2026-10-07 — `#138`.)**
- [x] ~~**Item 4 — one-link `linkapp` and startup repair.**~~ **LANDED (2026-10-07 — `#138`.)**
- [ ] **Item 5 — conflict surfaces.** **OPEN (not queued.)**
- [x] ~~**Item 6 — the mcp app tail.**~~ **LANDED (2026-10-12 — `#138`.)**

<a id="problems"></a>
## The three problems

- **Non-portable.** `os.Symlink` needs a privilege on Windows and `ln` does not
  exist there at all, so the shipped Windows binary can neither install nor
  self-link. This is what [windows-support.md Item 3](windows-support.md#3) is
  about; that part now rides on this carve.
- **Non-deterministic.** Every app's viewdefs land in one flat `~/.ark/viewdefs`,
  so when two apps ship a viewdef of the same name the resolution is
  last-writer-wins, and not even alphabetical: whichever app was linked most
  recently wins and silently cuts the other off. The clobber hides the conflict
  rather than reporting it.
- **A self-imposed hack.** These aggregation links feed ark's *own* runtime. We
  own the reader, so discovering apps through filesystem links is talking to
  ourselves the long way round; the honest fix is to change what the runtime
  reads.

<a id="model"></a>
## The model

Activation is one directory link per app: `~/.ark/lua/<app>` → `~/.ark/apps/<app>`,
a symlink on Unix and a junction on Windows, both created by ark at run time. Its
existence is the whole on/off bit: `linkapp add`/`remove` creates or removes that
one link and nothing else. The old entry-file link (`lua/<app>.lua`) and the
per-viewdef links into the shared `~/.ark/viewdefs` are gone.

The runtime discovers apps by enumerating `~/.ark/lua/*`: for each it auto-loads
`<app>/app.lua` and registers `<app>/viewdefs/*`, reading everything through that
one dir link. ("Auto-loads" means *loaded on request*, as today: an app's entry
file is found at `<app>/app.lua` when the app is first asked for, never eagerly at
session start. See [Item 1's decisions](#item-1-decisions).) On Windows the junction is unprivileged, survives a bundle
re-extract, and works across volumes, so the whole per-file copy-or-manifest
question that windows-support Item 3 was circling never arises.

<a id="collisions"></a>
## Collisions: deterministic and visible

The winner of a viewdef-name collision is deterministic: built-in apps before
user apps, alphabetical within each tier, first registration of a name wins. A
user app can therefore never shadow a built-in by accident, and the outcome no
longer turns on link order.

- **Built-in** means an app in the ark image, as listed by `ark ls`, not the
  repo's `apps/`, which a user may have reconfigured. Setup extracts the built-in
  apps into `~/.ark/apps` so a user can edit or patch them in place through ark's
  own `ls`/`cat`/fs verbs; editing an extracted built-in is the sanctioned way to
  override one, so no separate override marker is needed. `ark update` must
  therefore preserve such edits, which is why it works from an install manifest
  of file hashes ([setup-and-update.md](setup-and-update.md)).
- **Bundling fails** on a viewdef-name collision among built-ins, so the built-in
  set is conflict-free by construction and a live collision can only involve a
  user app.
- **Nothing is masked.** Conflicts are logged at startup and whenever they occur
  at run time, and surfaced through both an `ark` conflicts verb and a UI Lua
  method, from one resolver feeding all four surfaces so they always agree (the
  CLI proxies to the server when one is up).

Module resolution is namespaced by app in practice (`require("<app>.<module>")`,
e.g. `apps/ark/app.lua:4` requires `mcp.json`), so it is local-first by
construction and the deterministic rule removes any remaining ambiguity. The one
constraint to preserve is the cross-app dependency that naming implies: `ark`
requires `mcp.json`, so `mcp` must be loadable before `ark`.

<a id="reconcile"></a>
## Startup reconcile and the lua-dir watch

Because a junction stores an absolute target, ark computes the links at run time
rather than trusting persisted ones. On startup it scans `~/.ark/lua/*`, verifies
each resolves, and repairs what it can, so a moved `~/.ark` self-heals instead of
leaving dead links. At run time it watches the lua directory and re-scans on
change, and that watch is the trigger for re-detecting conflicts and
re-registering. (Old-scheme `lua/<app>.lua` links were to be detected and
griped about here; superseded by Item 4's decisions: converted by hand.)

<a id="static"></a>
## Static files: a fourth link

The aggregation layer has one more link that is not about activation at all.
`html/mcp` → `../apps/mcp` exists so the HTTP server can serve the mcp app's
CSS at `/mcp/css/*.css` (`MCP.DEFAULT.html` and `MCP.VariableBrowser.DEFAULT.html`
load it), and it arrives twice: shipped in the bundle image, and re-made by the
mcp app's own `ensureSymlink`. It fails on an unprivileged Windows account for
the same reason as the others, and it is the first link `ark setup` trips on.

The fix is the same argument as the rest of the carve: we own the reader. The
runtime serves `/<app>/…` straight from the app's directory, reached through
its activation link, and `html/<app>` retires. This is Item 2.

<a id="split"></a>
## What changes, and who owns it

Almost all of this is Go, so mini-spec: the one-link `linkapp` plus its Windows
junction branch (`cmd/ark/main.go:612` and the sibling near `4100`), the startup
reconcile and lua-dir watch, the stale-`app.lua` detector, the bundle-time
conflict check, the `ark` conflicts verb, and any portable link primitive. The
deterministic-collision and enumerate-and-load logic lives in the runtime's
viewdef loader (`ui-engine/internal/server/server.go:323`, plus the hotloader),
which is Go as well but in the ui-engine repo. Each repo runs its own mini-spec
pass for its share (decided 2026-10-07, superseding a 2026-10-06 call to route
it all through ark's); see [Item 1](#parts).

The genuine `apps/*` (`ui-*`) tail is small and measured, not guessed:

- **Delete the mcp self-symlink block** (`apps/mcp/app.lua:37-52`), which
  duplicates `linkapp`'s job with its own `ln -sfn`. Once activation and per-app
  loading exist, it has nothing to do.
- **No entry-convention fixups.** Audited: every app uses
  `require("<app>.<module>")`, never a bare `require("<app>")`, so dropping the
  `lua/<app>.lua` entry link breaks nothing.
- **No viewdef renames today.** The two bundled apps (`ark`, `mcp`) have zero
  colliding viewdef basenames, so the new bundle check passes as-is; the rename
  path only matters once a user app collides.

Bycatch from grounding, not activation but recorded so it is not lost: the mcp
app also shells out to `mkdir -p` (`app.lua:86`, `762`) and `base64 -d` (`772`),
both Unix-only. Those belong to windows-support's Item 7 "os/exec of Unix
helpers" class and want the same portable-primitive treatment.

<a id="parts"></a>
## Parts

In dependency order. PENDING #138 (non-admin `ark setup` on Windows) closes
when Items 1 through 4 and 6 have landed and setup is re-measured as a standard
user; Item 5 can follow.

**Item 1.** Specified in the sibling repos:
[ui-engine app-discovery.md](../../ui-engine/specs/app-discovery.md) (discovery,
order, viewdef collisions, hot reload) and
[Frictionless mcp.md](../../frictionless/specs/mcp.md) §2.6 and §4.3 (`flib`
built-in list, entry files) plus
[helper-scripts.md](../../frictionless/specs/helper-scripts.md) (`linkapp`).
These links assume ark, ui-engine and frictionless are checked out side by
side, as in the shared Go workspace; outside that layout, the same paths
resolve from each repo's root.

ui-engine enumerates `lua/*`, finds an app's entry file at
`<app>/app.lua` when the app is asked for, registers `<app>/viewdefs/*` under the
built-in-then-alphabetical rule, and the hotloaders watch per app. Discovery reads
*through* each `lua/<app>` entry and never tests it for being a symlink: since Go
1.23, `Lstat` reports a Windows junction as a directory rather than as a symlink.

<a id="item-1-decisions"></a>
### Item 1's decisions

- ~~DECIDED (Bill, 2026-10-06): the work lives in ui-engine and routes through
  ark's mini-spec.~~ Superseded 2026-10-07, below.
- DECIDED (Bill, 2026-10-07): "auto-load" in [the model](#model) and the
  2026-09-22 decisions means *load on request*, which is what the runtime does
  today. Entry files are found at `<app>/app.lua` when an app is first asked for;
  nothing loads eagerly at session start.
- DECIDED (Bill, 2026-10-07): each repo does its own mini-spec pass, so its
  specs, design and traceability are self-contained and make sense in that
  repo's own terms. ark's share is passing its bundled app names as
  `flib.Config.BuiltinApps` ([embedded-ui.md](../specs/embedded-ui.md)).
- DECIDED (Bill, 2026-10-07): ui-engine keeps the site's own `viewdefs/` as
  the lowest-ranked source, below every app, since its demo and other plain
  sites keep real viewdefs there. A missing `viewdefs/` is normal and silent; one
  created while the server runs is noticed at the next start. A missing site
  directory is a startup error, returned rather than panicked.

**Item 2.** The runtime serves `/<app>/…` from the app's directory, and
`html/<app>` retires ([static](#static)).

### Item 2's decisions

- DECIDED (Bill, 2026-10-06): per-app static serving joins this carve.

**Item 3.** Setup creates `lua/<app>` with the portable directory-link helper (a
junction on Windows), and bundling fails on a built-in viewdef-name collision.
ark plus Frictionless `installFile`. The Makefile strips the cache's symlinks
and `ark bundle` refuses any it is handed.

*Measured 2026-10-07, after Items 1-4:* on the Windows test VM a standard user
(`arktest`, fresh `USERPROFILE=C:\t1`, from a scheduled task) ran `ark setup`
to completion; `lua/` held six junctions (`viewlist` included), `html/` and
`viewdefs/` held no links, and `ark serve` loaded 48 viewdefs from 6 apps with
no collisions and served `/mcp/css/panel.css` from the app directory. Found on
the way: Frictionless's `viewlist` library app had no activation link, so its
ViewList viewdefs reached the runtime only through the per-viewdef links being
retired (fixed in Frictionless `f13e227`; setup now activates every bundled
app regardless).

*Measured 2026-10-07 on the Windows test VM (build 26200):* a standard user
creates a junction without privilege and reads through it from a scheduled task,
and a desktop process behaves the same. Over ssh, every reader (administrators
included) is refused a junction a standard user made, and can follow one an
administrator made. So the creator's standing is what ssh's RedirectionTrust
checks, and a standard user cannot make a junction an ssh session will trust;
see [windows-support.md Item 3](windows-support.md#3) for the mitigation.

**Item 4.** `linkapp add`/`remove` makes or removes the one dir link; startup
repairs dangling activation links ([reconcile](#reconcile)) and never creates
an absent one.

### Item 4's decisions

- DECIDED (Bill, 2026-10-07): no upgrade path and no stale-scheme detector.
  There are no users but us, so old-scheme links are converted by hand (after
  a Fossil backup) rather than detected or cleaned at startup. This
  supersedes the "gripes about old-scheme `lua/<app>.lua` files" above and in
  [reconcile](#reconcile). Bill's own `~/.ark` was converted 2026-10-07
  (fossil checkin `3b2b60b1` in `ark.fossil`).
- DECIDED (Bill, 2026-10-07): setup and update activate built-in apps;
  startup only repairs, so a deactivated app stays deactivated.

**Item 5.** One resolver feeding the logs, an `ark` verb, and a UI Lua method
([collisions](#collisions)).

**Item 6.** `ui-*` work in `apps/mcp`: delete `ensureSymlink`. The `mkdir -p` and
`base64 -d` bycatch goes to windows-support Item 7.

<a id="decisions"></a>
## Decisions (2026-09-22, Bill)

- Activation = one dir link `~/.ark/lua/<app>` → `~/.ark/apps/<app>`; junction on
  Windows; computed at run time; reconciled and repaired on startup.
- Runtime enumerates `~/.ark/lua/*`, auto-loads `<app>/app.lua`, registers
  `<app>/viewdefs/*`; the shared `~/.ark/viewdefs` retires. ("Auto-loads" means
  load on request; clarified 2026-10-07 in [Item 1's decisions](#item-1-decisions).)
- Collisions resolve built-in-then-alphabetical, first-wins; built-in = what
  `ark ls` shows; built-ins extracted to `~/.ark/apps` for in-place override;
  bundling fails on a built-in viewdef collision.
- Conflicts are logged at startup and at run time and exposed via an `ark` verb
  and a UI Lua method, all from one resolver.
- Module resolution is local-first; the lua dir is watched and re-scanned on
  change.

DECIDED (Bill, 2026-10-06): the carve splits into the six [parts](#parts)
above. Decisions naming one part sit with that part.
