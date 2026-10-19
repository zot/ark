# Sequence: Install and Update Flow

`ark setup` is the install path; `ark setup --local` connects a project;
`ark update` refreshes an install. See specs/main.md "Setup and Install".

## Participants
- CLI
- DirLink (crc-DirLink.md)
- Updater (crc-Updater.md)
- Bundle (embedded zip assets)
- DB

## Diagram 1: `ark setup` (global install)

Idempotent on the marker (the database file). With the marker present the
global part does nothing.

```
1. CLI.cmdSetup()
   1.1. marker present (~/.ark index file exists)? → global part is a no-op, return
   1.11. stop a running server first, via the graceful `ark stop` path (never start one); runs before 1.2 (R3481)
   1.2. create ~/.ark if absent
   1.3. copy the running exe into ~/.ark (ark.exe on Windows); skip if already running from ~/.ark
   1.4. Bundle.ExtractBundle → ~/.ark (html/, lua/, viewdefs/, apps/, skills/, agents/); the bundle holds no symlinks
      1.4.1. AppActivation.ActivateApp for each AppActivation.BundledAppNames() → one ~/.ark/lua/<app> directory link each (R3516)
   1.12. Updater.SeedManifest — record each extracted refreshable asset's hash into install-manifest.json (R3484)
   1.5. setupDatabase() — shared with cmdRebuild; creating the DB leaves the marker (no init→setup recursion)
   1.6. installLinks(~/.claude)
        1.6.1. for each ~/.ark/skills/* → DirLink.MakeDirLink(~/.claude/skills/<name>, that dir)
        1.6.2. DirLink.MakeDirLink(~/.claude/agents/ark, ~/.ark/agents)
   1.7. retireOldAgentCopies(~/.claude) — remove a flat agent copy only when byte-identical; report+leave otherwise
   1.8. provision llama libs when a model is configured and they are absent
   1.9. Updater.ProvisionModel(cfg) — download the configured model if the file is absent (pinned SHA-256); report how to add model_url/model_sha256 when unset, no download (R3483)
   1.10. report, and print that new agents appear in the next Claude session
```

## Diagram 2: `ark setup --local` (connect a project)

```
2. CLI.cmdSetupLocal()
   2.1. marker absent? → refuse (connects a project to an install, does not create one)
   2.2. installLinks(cwd/.claude)
        2.2.1. for each ~/.ark/skills/* → DirLink.MakeDirLink(cwd/.claude/skills/<name>, that dir)
        2.2.2. DirLink.MakeDirLink(cwd/.claude/agents/ark, ~/.ark/agents)
   2.3. retireOldAgentCopies(cwd/.claude)
   2.4. print the crank-handle CLAUDE.md hint ("load /ark first"); never edit CLAUDE.md
```

## Diagram 3: `ark update` (refresh an install)

```
3. CLI.cmdUpdate(noExe) → Updater.Update(noExe)
   3.4. stop a running server first, via the graceful `ark stop` path (never start one); runs before 3.1 (R3481)
   3.1. ~/.ark/ark is a symlink and not --no-exe? → gripe (use --no-exe) and refresh nothing
   3.2. not --no-exe path:
        3.2.1. compare ark.Version to the latest release tag; no-op when current, never downgrade
        3.2.2. download the new platform binary; verify against the published checksum
        3.2.3. Updater.SwapExe — Unix replace in place; Windows rename-aside, write new, defer-delete the aside copy
        3.2.4. exec the new binary to refresh assets from its own bundle (continues at 3.3)
        3.2.5. any failure at 3.2.2–3.2.3 leaves the old binary and ~/.ark intact
   3.3. --no-exe path (and the re-exec'd new binary): refresh from the running binary's own bundle
        3.3.1. Updater.RefreshAssets — manifest-aware; never clobber a user-edited file, never replace a link with a copy
        3.3.2. Updater.ProvisionModel(cfg) — download the configured model if the file is absent; report how to add model_url/model_sha256 when unset, no download (R3483)
```

## Diagram 4: `ark update --stage` (a requested update, staged)

```
4. CLI.cmdUpdate(--stage) → Updater.Stage(attempts)
   4.1. ~/.ark/ark is a symlink? → refuse, as plain update does (R3530); no server stop either way (R3529)
   4.2. compare ark.Version to the latest release tag; no-op when current, never downgrade (R3471)
   4.3. download the platform binary; verify against the published checksum (shared with 3.2.2)
   4.4. write ark.new.tmp, rename to ark.new — a partial download never carries the staged name (R3526)
   4.5. write update.json {version, downloadAttempts: attempts} (R3527)
```

## Diagram 5: `ark serve` startup step (taking a staged update)

```
5. CLI.cmdServe → StagedUpdate.Run(args), before ark.Serve
   5.1. no update.json, or a server already running → continue to serve (R3532)
   5.2. ~/.ark/ark a symlink, or running exe not ~/.ark/ark → continue to serve (R3533)
   5.3. take update.lock; not taken → continue (R3534)
   5.8. delete a leftover ark.old, ignoring failure, on every start past 5.2, request or not (R3534)
   5.4. update.json names this binary's own version → the chained new binary:
        5.4.1. Updater.RefreshAssets(own bundle) + ProvisionModel, as --no-exe (R3539)
        5.4.2. remove update.json; continue to serve
   5.5. run `ark.new version`; must succeed and name update.json's version (R3535)
   5.6. invalid:
        5.6.1. delete ark.new; downloadAttempts+1
        5.6.2. below 3 → Updater.Stage(attempts) in the background; continue to serve (R3535)
        5.6.3. at 3 → remove update.json, log that the update was abandoned; continue to serve (R3536)
   5.7. valid:
        5.7.1. rename ark → ark.old, ark.new → ark; undo the first if the second fails (R3537)
        5.7.2. release the lock; chain(ark, args): Unix exec; Windows run, wait, exit with its code (R3538)
        5.7.3. the new binary starts at 5 and takes 5.4
```

## Diagram 6: the UI's update calls

```
6. Lua → sys.* → Server.UpdateBridge
   6.1. sys.checkForUpdate() → state checking; goroutine runs `<exe> update --check` (no shell) → idle | failed (R3540)
   6.2. sys.requestUpdate() → state downloading; goroutine runs `<exe> update --stage` → staged | failed (R3541)
   6.3. sys.updateStatus() → snapshot; staged whenever update.json and ark.new exist (R3542), with stagedVersion from update.json (R3543)
```

## Notes

`installLinks` and `retireOldAgentCopies` are shared by global setup
(~/.claude) and `--local` (cwd/.claude). `RefreshAssets` and `ProvisionModel`
are shared by update and setup. The install manifest at
`~/.ark/install-manifest.json` (`{hash, onCollision, group}`) is what lets a
refresh tell a user edit from a stale bundle copy.
