# StagedUpdate
**Requirements:** R3526, R3527, R3528, R3529, R3530, R3531, R3532, R3533, R3534, R3535, R3536, R3537, R3538, R3539

A requested update in two halves. Staging (`ark update --stage`) puts a verified
new binary beside the installed one and records the request; the startup step
(first thing `ark serve` does) validates it, swaps it in, and chains to it, and
the chained binary finishes by refreshing the assets. Lives in `cmd/ark` beside
Updater because it reuses Updater's fetch, verify and refresh. The decision
logic takes its effects as injected functions so tests need no network, no
process replacement, and no real server.

## Knows
- request: `~/.ark/update.json` — `{version, downloadAttempts}` (R3527)
- paths: `~/.ark/ark` / `ark.new` / `ark.old` (`.exe` forms on Windows) and the lock file `~/.ark/update.lock`
- maxDownloadAttempts: 3 (R3536)
- effects (injected): serverRunning, versionOf(exe), stage(attempts), refresh(), chain(exe, args), lock(path)

## Does
- Updater.Check(): latest release version and whether it is newer; changes nothing (R3528)
- Updater.Stage(attempts): R3471 version rule; download + checksum verify (shared with Update); write `ark.new.tmp`, rename to `ark.new`; write `update.json` with `attempts` (R3526, R3527). Never stops a server (R3529)
- cmdUpdate: `--check` / `--stage` dispatch; both refuse on a symlinked `~/.ark/ark` (R3530) and skip the R3481 stop (R3529)
- Run(args): the startup step, in order (seq-install.md#5):
  - `~/.ark/ark` a symlink, or the running exe not `~/.ark/ark` → return (R3533)
  - delete a leftover `ark.old`, ignoring failure, request or not (R3534)
  - no `update.json`, or a server already running → return, serve continues (R3532)
  - take the lock (an OS lock, released by the kernel on a crash); not taken → return (R3534)
  - request version == own version → refresh(), remove `update.json`, return (R3539)
  - versionOf(`ark.new`) fails or names another version → delete `ark.new`, attempts+1; below 3, go stage(attempts) in the background; at 3, remove `update.json` and log (R3535, R3536)
  - valid → rename `ark`→`ark.old`, `ark.new`→`ark` (undo the first if the second fails), release the lock, chain(`ark`, args) (R3537)
- chain: Unix `syscall.Exec`; Windows run the new binary with the same args and std streams, wait, `os.Exit` with its code (R3538)

## Collaborators
- Updater: fetch, checksum verify, RefreshAssets, ProvisionModel
- CLI: `cmdServe` runs Run before `ark.Serve`; `cmdUpdate` dispatches `--check` / `--stage`

## Sequences
- seq-install.md
