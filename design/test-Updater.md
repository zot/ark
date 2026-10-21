# Test Design: Updater
**Source:** crc-Updater.md

Update's decision logic against a fake install root and a fake release source
(a stub that returns a chosen version, bytes, and checksum) — no network. The
whole class here is invisible when violated: a refresh that replaces a link
with a copy, a downgrade, and a failed swap that ate the old binary all leave a
green happy path while breaking exactly the guarantee the field exists for.

## Test: refresh never replaces a link with a copy
**Purpose:** a directory link under ~/.ark survives a manifest-aware refresh untouched (R3474, R3478)
**Input:** a fake ~/.ark where `skills/ui` (or an app dir) is a directory link; run Updater.RefreshAssets from a bundle carrying that path
**Expected:** the path is still a link (os.Readlink resolves) afterward, not a copied directory
**Refs:** crc-Updater.md, seq-install.md#3.3.1
**Code:** cmd/ark/update_test.go
**Fire alarm:** make RefreshAssets extract over the path unconditionally instead of skipping an existing link; the test goes red when the link becomes a regular directory
**Inject:** cmd/ark/update.go:RefreshAssets

## Test: refresh records an identical un-manifested file without rewriting it
**Purpose:** a file absent from the manifest but byte-identical to the bundle is recorded during a refresh (seeding an install that predates the manifest) without being rewritten (R3484, R3479)
**Input:** a fake ~/.ark with skills/ark/SKILL.md already equal to the bundle and its mtime backdated; run Updater.RefreshAssets from a bundle carrying that same content
**Expected:** the manifest gains an entry with the content's hash, and the file's mtime is unchanged (it was recorded, not rewritten)
**Refs:** crc-Updater.md, seq-install.md#3.3.1
**Code:** cmd/ark/update_test.go
**Fire alarm:** drop the record-without-rewrite branch (leave `if !write { continue }` recording nothing); the test goes red when the manifest has no entry for the file
**Inject:** cmd/ark/update.go:RefreshAssets

## Test: no downgrade, and no-op when current
**Purpose:** update does nothing when the running version is >= the latest release tag (R3471)
**Input:** stub release tag equal to, then older than, currentVersion; run Update(false)
**Expected:** no download, no swap in either case
**Refs:** crc-Updater.md, seq-install.md#3.2.1
**Code:** cmd/ark/update_test.go
**Fire alarm:** change the version comparison to `!=` (or drop it) so an older/equal release still swaps; the test goes red when the equal/older case triggers a download+swap
**Inject:** cmd/ark/update.go:Update

## Test: a failed swap leaves the old binary intact
**Purpose:** a download/checksum/swap failure leaves the old binary and ~/.ark intact (R3476)
**Input:** stub a checksum mismatch (or a swap error) after the new bytes are fetched; record the old binary's bytes; run Update(false)
**Expected:** Update returns an error and ~/.ark/ark still holds the original bytes
**Refs:** crc-Updater.md, seq-install.md#3.2.5
**Code:** cmd/ark/update_test.go
**Fire alarm:** move the exe overwrite before the checksum verify so a bad download is written before the mismatch is caught; the test goes red when the old binary's bytes are gone
**Inject:** cmd/ark/update.go:Update

## Test: symlinked ~/.ark/ark gripes and refreshes nothing
**Purpose:** on a symlinked binary, plain update refreshes nothing; --no-exe proceeds (R3477, R3478)
**Input:** a fake ~/.ark/ark that is a symlink; run Update(false), then Update(true); record asset mtimes
**Expected:** Update(false) changes nothing and gripes (names --no-exe); Update(true) refreshes assets without touching the exe
**Refs:** crc-Updater.md, seq-install.md#3.1
**Code:** cmd/ark/update_test.go
**Fire alarm:** drop the symlink check so plain update swaps a linked binary; the test goes red when the symlink is replaced by a file (or assets refresh on the plain path)
**Inject:** cmd/ark/update.go:Update

## Test: release asset names come from one function
**Purpose:** `ark _release-asset` reports exactly the name update downloads, for any platform: `ark-<version>-<os>-<arch>`, `.exe` only on windows, no leading `v` (R3547)
**Input:** ReleaseAssetName for (v0.6.0, linux, amd64), (0.6.0, windows, amd64), (0.6.0, darwin, arm64); and the command's `--terse` output for the running platform at a stamped version
**Expected:** `ark-0.6.0-linux-amd64`, `ark-0.6.0-windows-amd64.exe`, `ark-0.6.0-darwin-arm64`; the terse output equals ReleaseAssetName(version, runtime.GOOS, runtime.GOARCH), the name Update fetches
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** make the command build its own name string instead of calling ReleaseAssetName (e.g. forget the `.exe`, or keep the `v`); the windows or terse-equality case goes red
**Inject:** cmd/ark/release_asset_cli.go:releaseAssetReport
**Pulled:** 2026-10-21 — rang: own Sprintf name without `.exe`; terse windows case red (`ark-0.6.0-windows-amd64`, want `.exe`), baby-food case red too

## Test: release asset report is baby food unless --terse
**Purpose:** the default answer is `- asset:` / `- version:` / `- platform:` bullets, and `--terse` is the bare name on one line (R3548)
**Input:** the report for (0.6.0, windows, amd64), with and without terse
**Expected:** default: three bullets in that order naming `ark-0.6.0-windows-amd64.exe`, `0.6.0`, `windows/amd64`; terse: `ark-0.6.0-windows-amd64.exe` followed by one newline and nothing else
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** swap the branches so the default prints the bare name; the default case goes red
**Inject:** cmd/ark/release_asset_cli.go:releaseAssetReport
**Pulled:** 2026-10-21 — rang: `if !terse`; default report red, printed the bare name

## Test: update fetches the running platform's release asset
**Purpose:** the name Update asks the release for, for the binary and for its checksum, is ReleaseAssetName's answer for the running platform, so it matches what `make release` produced (R3472, R3547). Found by probing past the alarm list 2026-10-21: a hardcoded platform at the call site left the whole suite green.
**Input:** fake fetcher recording every name it is asked for; fetchVerified at latest `v0.6.0`
**Expected:** exactly two requests, both `ReleaseAssetName("0.6.0", runtime.GOOS, runtime.GOARCH)`
**Refs:** crc-Updater.md, seq-install.md#3
**Code:** cmd/ark/update_test.go
**Fire alarm:** hardcode a platform at the call site (`releaseAssetName(latest, "windows", runtime.GOARCH)`); red on any non-windows host, naming the wrong asset
**Inject:** cmd/ark/update.go:fetchVerified
**Pulled:** 2026-10-21 — rang: hardcoded `"windows"`; asked for `ark-0.6.0-windows-amd64.exe` twice, want `ark-0.6.0-linux-amd64`

## Test: ARK_RELEASE_URL chooses the release document
**Purpose:** the fetcher every update path uses reads its document from `ARK_RELEASE_URL` when set, else from GitHub's API (R3549)
**Input:** newReleaseFetcher() with the variable unset, then set to `http://example.test/release.json`
**Expected:** unset: the GitHub latest-release API URL; set: exactly the variable's value. No network either way
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** make newReleaseFetcher ignore the variable (always the GitHub constant); the set case goes red, naming the GitHub URL
**Inject:** cmd/ark/update.go:newReleaseFetcher
**Pulled:** 2026-10-21 — rang: constant only; set case read the GitHub URL, want the override

## Test: relative asset names resolve against the document
**Purpose:** a served release directory works with a document naming assets by file name, and absolute URLs pass through (R3550)
**Input:** an httptest server serving `release.json` (tag `v0.5.1`, one relative asset `ark-0.5.1-linux-amd64`, one absolute asset pointing at the same server under another path) and both files
**Expected:** Latest is `v0.5.1`; Download of each asset returns the served bytes
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** return the asset's URL unresolved; the relative download fails (no scheme)
**Inject:** cmd/ark/update.go:githubReleaseFetcher.assetURL
**Pulled:** 2026-10-21 — rang: unresolved URL; relative download failed with `unsupported protocol scheme ""`

## Test: a malformed release document fails before any download
**Purpose:** an unparseable document is an error from Latest, so update stops before downloading or swapping (R3551)
**Input:** an httptest server serving `not json` as the document
**Expected:** Latest returns an error; no asset request reaches the server
**Refs:** crc-Updater.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** discard the decode error in load (treat the empty document as loaded); Latest returns "" with no error and the case goes red
**Inject:** cmd/ark/update.go:githubReleaseFetcher.load
**Pulled:** 2026-10-21 — rang: decode error discarded; Latest returned "" with no error

## Test: the production updater's fetcher honours ARK_RELEASE_URL
**Purpose:** newUpdater, which every update path builds, gets its fetcher from newReleaseFetcher, so the override reaches plain update, `--check`, `--stage` and the UI bridge (R3549). Found by probing past the alarm list 2026-10-21: a bare `&githubReleaseFetcher{}` in newUpdater left the whole suite green.
**Input:** `ARK_RELEASE_URL` set to `http://example.test/release.json`; newUpdater()
**Expected:** its Fetcher is a githubReleaseFetcher reading exactly that URL
**Refs:** crc-Updater.md, crc-StagedUpdate.md
**Code:** cmd/ark/update_test.go
**Fire alarm:** build a bare `&githubReleaseFetcher{}` in newUpdater; red, reading "" instead of the override
**Inject:** cmd/ark/staged_update.go:newUpdater
**Pulled:** 2026-10-21 — rang: bare fetcher; read "" instead of the override
