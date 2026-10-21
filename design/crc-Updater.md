# Updater
**Requirements:** R3463, R3471-3484, R3517, R3547-3551

`ark update` and the shared refresh machinery it lends to `ark setup`. Brings
an install up to the running project's latest GitHub release, or (with
`--no-exe`) refreshes assets from the running binary's own bundle. Owns the
install manifest and the model provisioner, both reused by setup.

## Knows
- currentVersion: build-time `ark.Version` (`dev` for a plain `go build`)
- releaseURL: `https://github.com/zot/ark/releases` (latest tag + per-platform assets + checksum)
- releaseDocumentURL: where the release document is read: `ARK_RELEASE_URL` when set, else GitHub's latest-release API; fixed when the fetcher is built (R3549)
- manifestPath: `~/.ark/install-manifest.json`
- manifest: InstallManifest — `{version, files: map[path]{hash, onCollision, group}}` (R3479)

## Does
- Update(noExe bool): the top-level flow.
  - Stops a running ark server first, through the graceful `ark stop` path
    (POST /shutdown, then poll the socket for release — the CLI's
    `stopServerForInstall` reuses that path rather than shelling out), and
    never starts one afterward; on Windows a live server holds `~/.ark/lib`
    and loaded apps that cannot be overwritten (R3481).
  - Version-check first: compare `currentVersion` against the latest release
    tag; no-op when current, never downgrade (R3471). `--no-exe` assumes the
    running exe is already current and skips this check.
  - Symlinked `~/.ark/ark`: plain update gripes, naming `--no-exe`, and
    refreshes nothing (R3477). With `--no-exe` it proceeds normally (R3478).
  - Two-phase (plain): the running (old) binary downloads the new
    release binary for the current platform, verifies it against the
    published checksum, swaps it into `~/.ark/`, then execs the new binary
    to refresh the rest from its own bundle (R3472). A failed download,
    checksum, or swap leaves the old binary and `~/.ark/` intact (R3476).
  - `--no-exe`: skip the download and swap; refresh every asset (skills,
    agents, apps, model-if-missing) from the *running* binary's bundle,
    downloading nothing for the exe (R3478).
- ReleaseAssetName(version, goos, goarch): the one naming function for release
  binaries, `ark-<version>-<os>-<arch>` with `.exe` for windows and the
  version's leading `v` dropped. Update calls it for the running platform to
  choose its download; `ark _release-asset` calls it for any platform so the
  Makefile names its release binaries with the same function (R3547). The
  command answers in baby-food bullets (`asset`, `version`, `platform`) by
  default and with the bare name under `--terse` (R3548).
- NewReleaseFetcher(): the one place the release source is chosen. Reads
  `ARK_RELEASE_URL` once, so plain update, `--check`, `--stage` and the UI bridge
  all honour it (R3549). The fetcher resolves each asset URL against the
  document's URL, so a test-served document can name assets by file name
  (R3550); a document it cannot fetch or parse is an error before any download
  (R3551).
- SwapExe(newBinary): replace `~/.ark/ark`. On Windows, rename the running
  `~/.ark/ark.exe` aside, write the new binary at the original name, and
  delete the aside copy on a later run — a running exe cannot delete its own
  aside copy (R3473). Split by build tag (`update_windows.go` /
  `update_unix.go`).
- RefreshAssets(bundle): extract bundled assets through the manifest so a
  user-edited file is never clobbered and a link is never replaced by a copy
  (R3474). Per-file collision policy comes from the manifest entry's
  `onCollision`; a hash match or a manifest miss with matching content is a
  safe overwrite. Writes the updated `{hash, onCollision, group}` back
  (R3479). A file already equal to the bundle is **recorded** without being
  rewritten, so an install predating the manifest is seeded by one refresh
  (R3484). Built-in apps extracted to `~/.ark/apps` keep their in-place
  edits across a refresh.
- RefreshAssets also activates every bundled app whose `~/.ark/apps/<app>` did not exist before the refresh (AppActivation.ActivateApp); an app that existed keeps the user's activation (R3517)
- SeedManifest(arkDir, bundle): setup's manifest step. Records the hash of
  every refreshable bundled asset into `~/.ark/install-manifest.json` (same
  Record/Save path and defaults RefreshAssets uses), so the first `ark update`
  can tell a real user edit from an unchanged bundle copy rather than
  preserving everything (R3484). Called by `cmdSetup`'s global install right
  after the bundle extraction.
- ProvisionModel(cfg): download the configured embedding model when the
  model file is absent, from the configured per-model URL, verifying against
  the configured pinned SHA-256 (R3463, R3475). "If needed" means the file
  is absent; a present file or a link is left alone. The URL and hash come
  from `[embedding]` `model_url` / `model_sha256` (R3480), so the download is
  model-neutral. When the model is absent and those keys are unset, it reports
  how to add them (an `ErrModelKeysMissing` the caller prints, not a fatal
  error) rather than downloading (R3483). Reuses `llamalibs.go`'s GitHub
  download path (`downloadFileVerified`, a single verified file rather than an
  archive).

## Collaborators
- CLI: `cmdUpdate` dispatches here; `cmdSetup` reuses RefreshAssets / ProvisionModel / the manifest
- DirLink: RefreshAssets preserves existing directory links rather than copying over them
- Bundle: the asset source for the refresh
- LlamaLibs: shares the download-and-verify path (`downloadAndExtract`)

## Sequences
- seq-install.md
