# UpdateBridge
**Requirements:** R3531, R3540, R3541, R3542

The UI's view of a requested update. It never fetches or swaps anything itself:
it runs the server's own executable as `update --check` / `update --stage` (an
argv, no shell) in the background and keeps the outcome where Lua can read it
synchronously. The server never restarts for an update.

## Knows
- current: the running `ark.Version`
- exe: the server's own executable (`os.Executable`)
- status: an immutable snapshot `{current, latest, newer, state, error}` behind an `atomic.Pointer`, replaced whole, so the Lua executor reads it with no lock and no actor round trip
- run (injected): runs exe with args and returns stdout or an error carrying stderr

## Does
- CheckForUpdate(): unless a check or download is in flight, set `checking` and run `update --check` in a goroutine; parse latest + newer; back to `idle`, or `failed` with the error (R3540)
- RequestUpdate(): unless one is in flight, set `downloading` and run `update --stage` in a goroutine; `staged` on success, `failed` with the error otherwise (R3541, R3531)
- Status(): the snapshot, with `state` reported as `staged` whenever `update.json` and `ark.new` both exist on disk, including a request from an earlier session (R3542)

## Collaborators
- Server: owns one bridge; `registerLuaFunctions` exposes it as `sys.checkForUpdate`, `sys.requestUpdate`, `sys.updateStatus`
- StagedUpdate: the CLI side the child processes run

## Sequences
- seq-install.md
