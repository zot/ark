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
   1.4. Bundle.ExtractBundle → ~/.ark (html/, lua/, viewdefs/, apps/, skills/, agents/)
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

## Notes

`installLinks` and `retireOldAgentCopies` are shared by global setup
(~/.claude) and `--local` (cwd/.claude). `RefreshAssets` and `ProvisionModel`
are shared by update and setup. The install manifest at
`~/.ark/install-manifest.json` (`{hash, onCollision, group}`) is what lets a
refresh tell a user edit from a stale bundle copy.
