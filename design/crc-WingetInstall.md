# WingetInstall
**Requirements:** R3686-3689

`ark _winget-install`, the installer the winget package runs. winget downloads
`ark.exe` to a temporary folder and runs it with this verb, on install and on
upgrade alike, so the verb decides from what it finds. Internal (the leading
underscore), Windows only, never prompts.

## Knows
- arkDir, and the installed exe `arkDir\ark.exe`
- runningVersion: build-time `ark.Version`
- installedVersion: `VersionOf(installed exe)`, via `binaryVersion` (the same
  `<exe> version` read the staged update uses); an error, or a version that is
  not a release version (`dev` from a plain `go build`), means "older"

## Does
- Run(): the decision (R3686–R3689).
  - Refuse when not on Windows, and on a symlinked `~/.ark/ark` (R3686).
  - No install present (R3683): run global setup, which installs fresh or
    reinstalls over the kept data and registers (R3687).
  - Installed version older than the running one, unreadable, or not a release
    version: stop a
    running server (R3481), swap the running binary's own bytes over the
    installed exe with SwapExe (R3473), then Updater.RefreshInstalled, which
    refreshes from this binary's bundle and provisions the model, then Register
    with this binary's version (R3688). RefreshInstalled does not register here
    itself, because the running exe is winget's temporary download, not the
    installed one.
  - Otherwise: Register alone, never a downgrade (R3689).
  - Every error is returned, so the command exits non-zero and winget reports
    the failure; a failed swap leaves the old exe (R3476).
- Each effect is a field (Setup, Stop, Swap, Refresh, Register, VersionOf), so
  the decision is tested without a registry, a server or a second binary.

## Collaborators
- CLI: cmdWingetInstall builds it with the real effects
- Updater: SwapExe, RefreshInstalled
- Registration: Register
- StagedUpdate: binaryVersion

## Sequences
- seq-install.md#7
