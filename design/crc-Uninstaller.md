# Uninstaller
**Requirements:** R3690-3697, R3699

`ark uninstall`: remove the program, keep the data. Builds a plan first, so
what it prints and asks about is exactly what it then does, and so the plan is
tested without deleting anything.

## Knows
- arkDir and claudeDir (`~/.claude`)
- the install manifest (`install-manifest.json`), which records what the
  bundle wrote and the hash it wrote
- the configured model and lib dir, from `ark.toml`

## Does
- Plan() → UninstallPlan: the lists the user is shown.
  - Files: each manifest path whose content still matches its recorded hash
    (InstallManifest.Unchanged). An edited or unreadable one goes to Kept
    (R3692).
  - Links: each `installLinkSet` link under claudeDir and each
    `~/.ark/lua/<app>` for a bundled app, only when IsOurLink says it points at
    its `~/.ark` target (R3693).
  - Lib dir when it lies inside arkDir; the model when Lstat shows a regular
    file inside arkDir (R3694).
  - Bookkeeping: the manifest, `ark.new`, `ark.old` and their `.exe` forms,
    `update.json`, those that exist (R3695).
  - Runtime: the server's `logs/`, `log/` and pid file (R3699).
  - The exe (R3696).
  - Kept: the database, `ark.toml`, the storage directory, the edited files
    (R3697); and, never planned at all, what is made with the database:
    `tags.md`, `chimes.md`, `searching/` (R3699).
- Run(plan): stop a running server (R3691), remove the files, then every
  directory under the refreshable roots that is now empty, deepest first
  (R3692); remove the links through RemoveDirLink (R3693); remove lib dir and
  model (R3694); remove the bookkeeping and Unregister (R3695); remove the
  runtime files (R3699); remove the exe
  last (R3696). A failure on one entry is reported and the rest proceed, so a
  locked file does not strand a half-removed install with no report.
- removeExe(exe): Unix removes it. On Windows, when the running process is that
  exe, it starts a detached, windowless helper that waits for this process to
  exit and then deletes the file; any other exe is removed directly (R3696).
- Report: what was kept, where, and that deleting `~/.ark` removes the rest
  (R3697).
- Confirmation (CLI side): print the plan; unless `--yes`, read one line and
  proceed only on `y` or `yes`; EOF or anything else changes nothing (R3690).

## Collaborators
- CLI: cmdUninstall (flags, confirmation, the symlink refusal, R3690)
- InstallManifest: Unchanged
- DirLink: IsOurLink, RemoveDirLink (R3454)
- Registration: Unregister
- stopServerForInstall (R3481 path)

## Sequences
- seq-install.md#8
