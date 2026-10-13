# Test Design: App Activation
**Source:** crc-AppActivation.md, crc-CLI.md, crc-Updater.md

## Test: activate makes the one link, deactivate removes it
**Purpose:** `lua/<app>` is a directory link to `apps/<app>` and nothing else is made; removing it leaves the app directory (R3521)
**Input:** arkDir with `apps/demo/`; ActivateApp then DeactivateApp
**Expected:** `lua/demo` resolves to a directory and is the only `lua/` entry; after deactivation it is gone and `apps/demo` remains
**Refs:** crc-AppActivation.md
**Code:** appactivation_test.go
**Fire alarm:** make DeactivateApp a no-op. Red: `lua/demo survived deactivation`.
**Inject:** appactivation.go:DeactivateApp
**Pulled:** 2026-10-07 — rang: `lua/demo survived deactivation`

## Test: repair re-makes a dangling link, leaves an unrepairable one, creates nothing
**Purpose:** a link left by a moved `~/.ark` is re-made to `~/.ark/apps/<entry>`; a dangling link with no app is logged and left; an app with no entry stays inactive (R3522)
**Input:** `lua/moved` pointing at a deleted old home while `apps/moved` exists; `lua/gone` dangling with no `apps/gone`; `apps/off` with no entry
**Expected:** `lua/moved` → `apps/moved`; `lua/gone` still present and logged; no `lua/off`
**Refs:** crc-AppActivation.md
**Code:** appactivation_test.go
**Fire alarm:** (a) skip the re-make, red: `lua/moved` not re-made; (b) treat reconcile as "activate every installed app", the user-override bug, red: `repair activated an app that had no entry`.
**Inject:** appactivation.go:RepairActivationLinks
**Pulled:** 2026-10-07 — rang: (a) `dangling lua/moved was not re-made`, (b) `repair activated an app that had no entry`

## Test: a clean bundle source passes
**Purpose:** distinct viewdefs across apps and no symlinks pass the check (R3518, R3519)
**Input:** apps `a` and `b` shipping different viewdefs
**Expected:** no error
**Refs:** crc-CLI.md
**Code:** cmd/ark/bundlecheck_test.go

## Test: a symlink in the bundle source is refused, named
**Purpose:** the bundle ships no symlinks (R3518)
**Input:** the clean source plus `lua/a` → `../apps/a`
**Expected:** an error naming `lua/a`
**Refs:** crc-CLI.md
**Code:** cmd/ark/bundlecheck_test.go
**Fire alarm:** skip the symlink test. Red: the source passes.
**Inject:** cmd/ark/bundlecheck.go:checkBundleSource
**Pulled:** 2026-10-07 — rang: `symlink not refused by name: <nil>`

## Test: two built-in apps shipping one viewdef name are refused, named
**Purpose:** the built-in app set is collision-free by construction (R3519)
**Input:** apps `a` and `b` both shipping `T.DEFAULT.html`
**Expected:** an error naming `T.DEFAULT.html` and `a, b`
**Refs:** crc-CLI.md
**Code:** cmd/ark/bundlecheck_test.go
**Fire alarm:** require three owners for a collision. Red: the source passes.
**Inject:** cmd/ark/bundlecheck.go:checkBundleSource
**Pulled:** 2026-10-07 — rang: `collision not refused with names: <nil>`

## Test: a refresh activates only apps it extracts for the first time
**Purpose:** update activates new built-ins and never re-activates an app the user deactivated (R3517)
**Input:** `apps/old/` installed with no `lua/old`; a bundle shipping `apps/old` and `apps/new`
**Expected:** `lua/new` → `apps/new`; no `lua/old`
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** treat every bundled app as new. Red: `refresh re-activated an app the user had deactivated`.
**Inject:** cmd/ark/update.go:appsNotYetInstalled
**Pulled:** 2026-10-07 — rang: `refresh re-activated an app the user had deactivated`

## Test: conflicts computed from disk name the viewdef, winner and loser
**Purpose:** `ViewdefConflicts` runs the ui-engine resolver over `~/.ark` (R3525)
**Input:** arkDir with active apps `aa` and `zz`, both shipping `viewdefs/T.DEFAULT.html`
**Expected:** one conflict: `T.DEFAULT`, `aa` over `zz`
**Refs:** crc-AppActivation.md
**Code:** appactivation_test.go
**Fire alarm:** return no conflicts from ViewdefConflicts. Red: `ViewdefConflicts = [], want T.DEFAULT, aa over zz`.
**Inject:** appactivation.go:ViewdefConflicts
**Pulled:** 2026-10-13 — rang: `ViewdefConflicts = [], want T.DEFAULT, aa over zz`

## Test: the server answers /ui/conflicts from disk when its UI is not running
**Purpose:** with no ui-engine, the route still gives the same list (R3524)
**Input:** a test DB whose directory holds the `aa`/`zz` collision; `Server{db}` with no uiRuntime
**Expected:** the JSON array names `aa` over `zz`
**Refs:** crc-Server.md
**Code:** appactivation_test.go
**Fire alarm:** answer an empty list when uiRuntime is nil. Red: `GET /ui/conflicts = [], want T.DEFAULT, aa over zz`.
**Inject:** server.go:handleUIConflicts
**Pulled:** 2026-10-13 — rang: `GET /ui/conflicts = [], want T.DEFAULT, aa over zz`

## Test: conflicts print one entry per losing copy, or a none line
**Purpose:** the CLI output shape (R3523)
**Input:** an empty list; one `T.DEFAULT` conflict
**Expected:** `no viewdef conflicts`; `T.DEFAULT: aa wins over zz` then the two files indented
**Refs:** crc-CLI.md
**Code:** cmd/ark/conflicts_test.go
**Fire alarm:** print the loser before the winner. Red: `printed "T.DEFAULT: zz wins over aa…"`.
**Inject:** cmd/ark/main.go:printViewdefConflicts
**Pulled:** 2026-10-13 — rang: `printed "T.DEFAULT: zz wins over aa…"`
