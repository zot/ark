# AppActivation
**Requirements:** R3515, R3516, R3517, R3521, R3522

An app is active when `~/.ark/lua/<app>` is a directory link to
`~/.ark/apps/<app>`; that one link is the whole on/off bit (the runtime reads
everything through it). This card owns making, removing and repairing those
links, and naming the apps ark's bundle ships.

## Knows
- arkDir: `~/.ark`
- bundleAppsDir: `apps` — the bundle directory whose children are apps

## Does
- ActivateApp(arkDir, app): DirLink.MakeDirLink(`lua/<app>`, `apps/<app>`);
  idempotent (R3516, R3521)
- DeactivateApp(arkDir, app): DirLink.RemoveDirLink(`lua/<app>`) (R3521)
- RepairActivationLinks(arkDir, logf): for each `lua/` entry that is a link
  which no longer resolves, re-make it to `apps/<entry>` when that directory
  exists, else log it as dangling; never creates an absent entry (R3522)
- BundledAppNames(): the top-level directories under the bundle's `apps/`;
  empty for an unbundled binary (R3515, R3516, R3517)
- builtinAppNames(files): the pure listing → names step (R3515)

## Collaborators
- DirLink: the portable link primitive (symlink / junction)
- CLI: setup activates bundled apps; `ark ui linkapp`
- Updater: activates apps a refresh extracts for the first time
- Server: repairs before starting the ui-engine; passes BundledAppNames as
  `flib.Config.BuiltinApps`

## Sequences
- seq-install.md
