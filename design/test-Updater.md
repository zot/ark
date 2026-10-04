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
