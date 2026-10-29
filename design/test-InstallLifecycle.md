# Test Design: Install lifecycle (registration, winget, uninstall)
**Source:** crc-Registration.md, crc-WingetInstall.md, crc-Uninstaller.md, crc-CLI.md

The install's three states (fresh, installed, data without program) and the
verbs that move between them, against a temp `~/.ark` and `~/.claude`. The
registry and the Windows self-delete are verified live on the VM; here they are
pure functions or injected effects. The properties that hide when violated:
uninstall deleting an edited file or a real directory while reporting a clean
removal, a reinstall overwriting the files uninstall kept, and the winget verb
downgrading an install.

## Test: install state is marker plus executable
**Purpose:** marker and exe → installed; marker alone → reinstall; no marker → fresh (R3683, R3684)
**Input:** temp ~/.ark with each combination of marker and `ark`/`ark.exe`
**Expected:** installState reports installed, reinstall, fresh, fresh (exe without marker is the README install)
**Refs:** crc-CLI.md, seq-install.md#1.1, seq-install.md#1.14
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** drop the executable check, so marker alone reads as installed; the reinstall row goes red
**Inject:** cmd/ark/install_lifecycle.go:installState

## Test: an installed setup only registers
**Purpose:** with marker and exe present, global setup changes nothing but calls the registration once (R3683, R3680)
**Input:** seedInstall with marker and exe; a counting registration stub; a ~/.claude sentinel
**Expected:** sentinel untouched, registration called once
**Refs:** crc-CLI.md, seq-install.md#1.1, seq-install.md#1.13
**Code:** cmd/ark/setup_test.go
**Fire alarm:** return before the registration on the installed branch; the call count reads 0
**Inject:** cmd/ark/main.go:runGlobalSetup

## Test: reinstall keeps edited files and restores the rest
**Purpose:** reinstalling over kept data writes missing bundle files, keeps an edited one, activates every bundled app (R3684)
**Input:** temp ~/.ark with no manifest, an edited `skills/ark/SKILL.md`; fakeAssets holding that file, a missing one, and `apps/demo/app.lua`
**Expected:** edited content kept, missing file written, `lua/demo` a link to `apps/demo`
**Refs:** crc-CLI.md, seq-install.md#1.14
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** write every bundle file unconditionally instead of through RefreshAssets; the edited file is overwritten
**Inject:** cmd/ark/install_lifecycle.go:reinstallOverData

## Test: the Path entry is added once, whatever its spelling
**Purpose:** pathWithEntry adds the dir only when no entry names it, case-insensitively, expanded, trailing separator ignored (R3680)
**Input:** paths holding the dir in another case, as `%USERPROFILE%\.ark`, with a trailing `\`, and not at all
**Expected:** unchanged for the first three, appended once for the last; other entries kept in order
**Refs:** crc-Registration.md
**Code:** cmd/ark/registration_test.go
**Fire alarm:** compare entries case-sensitively; the other-case row appends a duplicate
**Inject:** cmd/ark/registration.go:sameDirEntry

## Test: unregistering removes every spelling and nothing else
**Purpose:** pathWithoutEntry drops each entry naming the dir and keeps the rest (R3695)
**Input:** a path with the dir twice in two spellings between other entries
**Expected:** both gone, the others in order, changed reported
**Refs:** crc-Registration.md
**Code:** cmd/ark/registration_test.go

## Test: the installed-apps entry names the uninstall command
**Purpose:** arpValues quotes the exe and gives the quiet form `--yes` (R3681)
**Input:** arkDir with a space in it, version 1.2.3
**Expected:** `UninstallString` = `"<dir>\ark.exe" uninstall`, `QuietUninstallString` adds `--yes`, `DisplayVersion` 1.2.3, `NoModify`/`NoRepair` 1
**Refs:** crc-Registration.md
**Code:** cmd/ark/registration_test.go

## Test: the winget verb never downgrades
**Purpose:** each branch of WingetInstall.Run with injected effects (R3686–R3689)
**Input:** off Windows; symlinked exe; fresh; installed older; unreadable version; installed `dev`; same; installed newer
**Expected:** refuse; refuse; Setup only; Stop+Swap+Refresh+Register (three rows); Register only; Register only
**Refs:** crc-WingetInstall.md, seq-install.md#7
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** reverse the version comparison; the installed-newer row swaps (a downgrade). And drop the release-version check; the `dev` row only registers
**Inject:** cmd/ark/winget_install.go:Run

## Test: only dotted numbers are release versions
**Purpose:** isReleaseVersion accepts `0.5.1` and `v1.2`, refuses `dev`, `0.5.1-rc1` and the empty string (R3688)
**Input:** those strings
**Expected:** as stated
**Refs:** crc-WingetInstall.md
**Code:** cmd/ark/install_lifecycle_test.go

## Test: the uninstall plan keeps what the user made
**Purpose:** Plan removes unchanged manifest files and our links; keeps edited files, the database, ark.toml, storage, a linked model, someone else's link, a real directory at a link path, and a lib dir outside ~/.ark (R3692–R3694, R3697)
**Input:** a seeded install with each of those
**Expected:** each in Remove or Kept as stated
**Refs:** crc-Uninstaller.md, seq-install.md#8.2
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** treat every manifest file as unchanged; the edited file lands in Remove
**Inject:** install_manifest.go:Unchanged

## Test: uninstall runs the plan and leaves the data
**Purpose:** after Run, the planned entries are gone and the kept ones are byte-identical (R3692, R3693, R3695, R3696, R3697)
**Input:** the same seeded install (Unix exe removal)
**Expected:** unchanged files (patterns and README.md among them), emptied directories, our links, bookkeeping, the server's logs and pid file, and the exe gone; database, ark.toml, storage, tags.md, chimes.md, searching/, edited file, the real directory and the foreign link intact
**Refs:** crc-Uninstaller.md, seq-install.md#8.5, seq-install.md#8.6, seq-install.md#8.8, seq-install.md#8.9
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** skip the IsOurLink check when planning links; the foreign link is removed. And skip the runtime-file removal; logs/ survives
**Inject:** cmd/ark/uninstall.go:planLinks, cmd/ark/uninstall.go:Run

## Test: the manifest records every bundled file
**Purpose:** setup's seed records each root the bundle ships, patterns, resources, README.md, mcp and linkapp included (R3698)
**Input:** fakeAssets with one file under each root; SeedManifest
**Expected:** every name has a manifest entry
**Refs:** crc-Updater.md
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** drop `patterns` from refreshableAssetRoots; the test names patterns/lists.md as unrecorded
**Inject:** cmd/ark/update.go:refreshableAssetRoots

## Test: uninstall changes nothing without a yes
**Purpose:** confirmation proceeds only on `y`/`yes`; `n` or EOF leaves everything (R3690)
**Input:** answers "n\n", "" (EOF), "yes\n"
**Expected:** first two: nothing removed; third: the plan runs
**Refs:** crc-Uninstaller.md, seq-install.md#8.3
**Code:** cmd/ark/install_lifecycle_test.go
**Fire alarm:** treat EOF as yes; the EOF row removes files
**Inject:** cmd/ark/uninstall.go:confirmed
