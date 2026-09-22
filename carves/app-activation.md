# App activation and loading

Frictionless apps register with the ark runtime by symlink, and it shows.
`linkapp` links each app's `app.lua`, its directory, and every viewdef into
aggregation points under `~/.ark/lua` and `~/.ark/viewdefs`; at least one app
(`apps/mcp/app.lua:37-52`) then adds its own `ln -sfn` on top. Three problems
ride together, and one design move settles all three: make activation a single
directory link per app and have the runtime read apps where they sit.

<a id="problems"></a>
## The three problems

- **Non-portable.** `os.Symlink` needs a privilege on Windows and `ln` does not
  exist there at all, so the shipped Windows binary can neither install nor
  self-link. This is what [windows-support.md Part 3](windows-support.md#3) is
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
one dir link. On Windows the junction is unprivileged, survives a bundle
re-extract, and works across volumes, so the whole per-file copy-or-manifest
question that Part 3 was circling never arises.

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
  override one, so no separate override marker is needed.
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
re-registering. Stale old-scheme `lua/<app>.lua` files are detected here too and
griped about in the log and UI.

<a id="split"></a>
## What changes, and who owns it

Almost all of this is Go, so mini-spec: the one-link `linkapp` plus its Windows
junction branch (`cmd/ark/main.go:612` and the sibling near `4100`), the startup
reconcile and lua-dir watch, the stale-`app.lua` detector, the bundle-time
conflict check, the `ark` conflicts verb, and any portable link primitive. The
deterministic-collision and enumerate-and-load logic lives in the runtime's
viewdef loader (`ui-engine/internal/server/server.go:323`, plus the hotloader),
which is Go as well but in the ui-engine repo, so whether it routes through ark's
mini-spec or ui-engine's own process is an open routing question, not a free
choice.

The genuine `apps/*` (ui-*) tail is small and measured, not guessed:

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
both Unix-only. Those belong to windows-support's Part 7 "os/exec of Unix
helpers" class and want the same portable-primitive treatment.

<a id="decisions"></a>
## Decisions (2026-09-22, Bill)

- Activation = one dir link `~/.ark/lua/<app>` → `~/.ark/apps/<app>`; junction on
  Windows; computed at run time; reconciled and repaired on startup.
- Runtime enumerates `~/.ark/lua/*`, auto-loads `<app>/app.lua`, registers
  `<app>/viewdefs/*`; the shared `~/.ark/viewdefs` retires.
- Collisions resolve built-in-then-alphabetical, first-wins; built-in = what
  `ark ls` shows; built-ins extracted to `~/.ark/apps` for in-place override;
  bundling fails on a built-in viewdef collision.
- Conflicts are logged at startup and at run time and exposed via an `ark` verb
  and a UI Lua method, all from one resolver.
- Module resolution is local-first; the lua dir is watched and re-scanned on
  change.
