# Registration
**Requirements:** R3680, R3681, R3682, R3685, R3695

The per-user Windows registration of an install: `~\.ark` on the user `Path`,
and ark's entry in Windows' installed-apps list, which is how winget recognizes
the install. Written by setup on every run, by the refresh an installed binary
runs after an exe swap, and by `_winget-install`; removed by `ark uninstall`.
Neither entry needs elevation (both are under `HKCU`). Off Windows every
operation is a no-op.

## Knows
- pathKey: `HKCU\Environment`, value `Path` (REG_EXPAND_SZ; its existing type is kept)
- arpKey: `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zot.Ark`,
  also the display name: plain `ark` is what KDE's archive manager claims in
  winget's catalog, and winget matched an `ark` entry to `KDE.Ark` (measured
  2026-10-29; Bill chose `Zot.Ark`)
- publisher: `Bill Burdick`, the package's publisher

## Does
- Register(arkDir, version): ensure the `Path` entry, then write every
  installed-apps value (R3680, R3681). Reports whether `Path` changed, so setup
  can tell the user to open a new terminal.
- Unregister(arkDir): remove every `Path` entry naming `arkDir` and delete the
  installed-apps key (R3695). A missing entry or key is not an error.
- pathWithEntry(path, dir) → (newPath, changed): append `dir` unless some entry
  already names it. Entries compare case-insensitively after expanding
  environment variables and dropping a trailing separator; empty entries are
  dropped only when the value is rewritten (R3680). Pure, so it is tested on
  every platform.
- pathWithoutEntry(path, dir) → (newPath, changed): the inverse, dropping every
  entry that names `dir` (R3695). Pure.
- arpValues(arkDir, version) → the string values and the DWORD values of the
  entry: `DisplayName`, `DisplayVersion`, `Publisher`, `InstallLocation`,
  `DisplayIcon`, `UninstallString` (`"<exe>" uninstall`),
  `QuietUninstallString` (`"<exe>" uninstall --yes`), `NoModify` and `NoRepair`
  (R3681). Pure.
- announce(): broadcast `WM_SETTINGCHANGE` with `"Environment"` so shells
  started afterwards read the new `Path`, only after `Path` was rewritten.
  Bounded by `SendMessageTimeout`, so a hung window cannot stall setup.

## Collaborators
- CLI: runGlobalSetup calls Register on every Windows run (R3680)
- Updater: RefreshInstalled calls Register when the running exe is the installed one (R3685)
- WingetInstall: calls Register in every branch that leaves an install
- Uninstaller: calls Unregister (R3695)

## Sequences
- seq-install.md#1.13
- seq-install.md#3.3.3
- seq-install.md#8.8
