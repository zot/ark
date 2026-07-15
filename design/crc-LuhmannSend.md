# LuhmannSend
**Requirements:** R3129, R3131, R3132, R3133, R3134

Server-side **synchronous command bridge** — the producer half of the
orchestrator drain tube. Where `next` (crc-LuhmannCLI.md) is the orchestrator
popping work, `LuhmannSend` is a caller pushing one instruction onto the same
tube and blocking until the orchestrator has handled it, then handing the
orchestrator's turns for that instruction back to the CLI to render.

The tube never needed request/response matching — its curation and directive
producers are fire-and-forget. So rather than tax those paths with a formal
request-id column, the **one** consumer that needs correlation opts in by
riding a nonce **inside the command's own content** (Watermark): the server
recognizes it on the session JSONL tap it already owns, and brackets the reply
window from the nonce's appearance to the orchestrator's first turn completion.

## Knows
- sendCounter: uint64 — monotonic per-server nonce source, mutex-guarded.
  In-memory (like the watcher's bloodhound-id counter); a server bounce resets
  it. Rendered into the request as the backquoted marker `` `LSEND:<n>` ``
  (R3131) — inert to the orchestrator, plain to the scanner.
- (locates the orchestrator's session JSONL at call time via
  `LuhmannOwner()` → `locateSessionJSONL` — a filesystem glob of
  `~/.claude/projects/*/<uuid>.jsonl`, **not** the index: the orchestrator's
  `~/.ark/luhmann` cwd is not a corpus source, so its own log is never indexed
  and an index query (`db.SessionJSONLs`) would miss it)

## Does
- **LuhmannSend(ctx, instruction, timeout) → (window []byte, err)** — one
  blocking call does enqueue → wait → collect (Batteries Included):
  1. **Gate on a live orchestrator** (R3134): if `LuhmannOwner()` is empty,
     return an orchestrator-not-running error — the same gate the watcher
     applies before scheduling a CLI hunt (R3020). No enqueue.
  2. **Mint the nonce** (`sendCounter++` under the lock) and **build the
     markdown command request** — the instruction rendered as Baby-Food
     markdown with the backquoted `` `LSEND:<n>` `` marker embedded inertly
     (R3131). Enqueue a `command`-kind `LuhmannWork` carrying the request +
     nonce via `EnqueueLuhmann` (crc-LuhmannCLI.md, R3129, R3130). A full queue
     returns an enqueue error.
  3. **Locate the orchestrator's JSONL** (`locateSessionJSONL(owner)` — a
     filesystem glob, index-independent) and record its current end offset as
     the tail start. The command will land there as the orchestrator's
     delivered work (R3132).
  4. **Bracket by watermark** (R3132): tail the JSONL from the start offset,
     polling for appended bytes until `timeout`. Find the line carrying the
     marker (`` LSEND:<n> ``) — the **open** bracket — then the first
     `signalTurnDuration` after it (reusing `scanNewBytes`, crc-RecallWatcher.md)
     — the **close** bracket. Return the raw JSONL lines of `[open, close]`.
     A single command is one turn even across many tool calls, so multi-turn
     and interleaving fall away — the scanner reads its own watermark and stops
     at the first turn boundary, never counting turns (R3132).
  5. **Timeout / bounce** (R3133): on `timeout` before the close, return a
     timeout error — the enqueue is **not** undone, so the orchestrator may
     still act. A server bounce mid-tail is a wait condition, not an error
     (Stubborn Plumbing) — the tail redials, consistent with `next` (R3015).
- **ServeHTTP handler (`POST /luhmann/send`)** — decodes `{instruction,
  timeout}`, calls `LuhmannSend`, and writes the window's raw JSONL lines as
  the response body (or the error). Registered by `Server.Serve` (crc-Server.md).

## Collaborators
- LuhmannCLI (crc-LuhmannCLI.md): `EnqueueLuhmann` + the `command`
  `LuhmannWork` kind + `LuhmannOwner()` — the tube this bridge produces onto
  and the live-orchestrator gate (R3130, R3134).
- RecallWatcher (crc-RecallWatcher.md): reuses the package-level `scanNewBytes`
  / `signalTurnDuration` turn-boundary scanner for the close bracket (R3132).
  No stateful watcher machinery — the bridge tails the file directly for a
  snappy synchronous return, so it never waits on the indexer's debounce.
- Server (crc-Server.md): hosts the `POST /luhmann/send` route + the
  `sendCounter` state.
- CLITree (crc-CLITree.md): the `send` subcommand proxies here and renders the
  returned window with the `ark chats` transcript renderer (R3129, R3133).

## Sequences
- seq-luhmann-send.md
