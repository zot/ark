# Test Design: Setup
**Source:** crc-CLI.md

Setup's decision logic against a fake install root (a temp `~/.ark` and a temp
`~/.claude`) — no live home, no server. The marker check and the byte-identical
retire guard are the invisible-when-violated properties: a wrong marker test
re-runs the global install (or skips it) silently, and a wrong retire guard
deletes a user's edited agent while reporting a clean install.

## Test: global setup is a no-op when the marker is present
**Purpose:** with the database file present, the global install does nothing (R3456)
**Input:** a fake `~/.ark` that already holds the marker (index file); record `~/.claude` link mtimes; run runGlobalSetup
**Expected:** no exe re-copy, no re-extract, links untouched (mtimes unchanged)
**Refs:** crc-CLI.md, seq-install.md#1.1
**Code:** cmd/ark/setup_test.go
**Fire alarm:** invert the marker test (treat present as absent) so setup re-runs the global install every time; the test goes red when the link mtimes move / the exe is re-copied
**Inject:** cmd/ark/main.go:runGlobalSetup

## Test: setup copies no skills or agents, only links
**Purpose:** every skill/agent under ~/.claude is a directory link, never a copied file (R3465, R3461, R3462)
**Input:** fresh fake install root with no marker; run cmdSetup; inspect ~/.claude/skills/* and ~/.claude/agents/ark
**Expected:** each is a link (os.Readlink resolves) into ~/.ark; no regular-file skill/agent was written
**Refs:** crc-CLI.md, seq-install.md#1.6
**Code:** cmd/ark/setup_test.go
**Fire alarm:** replace a MakeDirLink call in installLinks with a file copy; the test goes red when os.Readlink fails on the resulting regular file
**Inject:** cmd/ark/main.go:installLinks

## Test: retire an old agent copy only when byte-identical
**Purpose:** a flat ~/.claude/agents/<name>.md is removed only when a regular file identical to the bundle; a differing file or a link is left (R3469, R3470)
**Input:** three cases — (a) flat file identical to bundled agent, (b) flat file with edited bytes, (c) a hand-made symlink — then run retireOldAgentCopies
**Expected:** (a) removed; (b) left in place and reported; (c) left in place and reported
**Refs:** crc-CLI.md, seq-install.md#1.7
**Code:** cmd/ark/setup_test.go
**Fire alarm:** drop the byte-compare so retire removes any regular file of that name; the test goes red when case (b)'s edited file is deleted
**Inject:** cmd/ark/main.go:retireOldAgentCopies

## Test: setup seeds the install manifest
**Purpose:** the global install records each extracted refreshable asset's hash into install-manifest.json, so the first update tells a user edit from an unchanged bundle copy (R3484)
**Input:** a fake ~/.ark and a fake bundle carrying skills/agents/html files plus one non-refreshable path; run SeedManifest
**Expected:** the manifest holds an entry per refreshable file with the bundle content's hash; the non-refreshable path is not recorded
**Refs:** crc-Updater.md, seq-install.md#1.12
**Code:** cmd/ark/setup_test.go
**Fire alarm:** skip the m.Record call in SeedManifest so nothing is written; the test goes red when the manifest has no entries (first missing-entry lookup)
**Inject:** cmd/ark/update.go:SeedManifest

## Test: a failed server stop aborts the install
**Purpose:** when the graceful stop does not complete, setup/update abort rather than continue (R3481)
**Input:** a live unix socket at ~/.ark/ark.sock that accepts but never releases; run stopServerForInstall
**Expected:** stopServerForInstall returns an error (the caller aborts and tells the user to run `ark stop -f`)
**Refs:** crc-CLI.md, seq-install.md#1
**Code:** cmd/ark/setup_test.go
**Fire alarm:** make stopServerForInstall return nil after the grace period instead of an error; the test goes red because a non-stopping server is treated as stopped
**Inject:** cmd/ark/main.go:stopServerForInstall

## Test: setup --local refuses without the marker
**Purpose:** `--local` requires an existing install (R3466)
**Input:** a fake install root with no marker; run cmdSetupLocal in a temp cwd
**Expected:** non-zero / error return, and no `.claude` links created in cwd
**Refs:** crc-CLI.md, seq-install.md#2.1
**Code:** cmd/ark/setup_test.go
**Fire alarm:** remove the marker precondition so --local links into cwd on a bare machine; the test goes red when cwd/.claude links appear
**Inject:** cmd/ark/main.go:cmdSetupLocal
