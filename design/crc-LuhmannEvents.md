# LuhmannEvents
**Requirements:** R3145, R3146, R3147, R3148

Server-side **event-routing bridge** — the third producer onto the
orchestrator drain tube. Frictionless events (a Lua app's `mcp.pushState()`
payloads) are drained today by `ark ui event`, the first lotto tube and the
one this whole family grew out of. On a session's opt-in this card reroutes
them onto that session's `next` tube, so a pty-hosted orchestrator handles UI
work in the same serialized thread it already uses for curation, directives,
and commands. Not a tube to consolidate — one tube coming home.

The routing privilege hangs off the `next` seat (crc-LuhmannCLI.md) rather
than carrying an identity of its own, so this card adds one piece of state
under the seat's existing lock and one goroutine under the seat's lifecycle.

## Knows
- eventOwner: string — the session owning event routing; `""` when unowned
  (R3145). Guarded by the seat's `luhmannMu`, **not** a lock of its own: the
  two are lifecycle-coupled (routing clears on a seat change, R3148), so one
  lock keeps the invariant *eventOwner is empty or equals luhmannOwner*
  indivisible. In-memory like the lease; a bounce clears both.
- eventPumpCancel: context.CancelFunc — stops the running pump; nil when no
  pump runs. Set under the same lock, so pump lifetime and routing ownership
  can never disagree.
- uiMux: \*http.ServeMux — the mux flib registered `/wait` on
  (crc-FlibRuntime.md). The pump reads it **in-process**; external callers
  reach it only through the gate (below), which wraps the mux from outside.
  So the pump bypasses its own gate by construction and needs no exemption
  marker.

## Does

### `luhmann events --session S [--off]` → `LuhmannEvents(session, off)`
- Server-required. Under `luhmannMu`, gate on the seat (R3145): proceed only
  when `S == luhmannOwner`, else return `you don't have ownership` — R3013's
  string, driving the orchestrator's existing stand-down reflex. Routing is a
  privilege of the seat, so an unowned seat refuses too.
- **on**: set `eventOwner := S` and start the pump (idempotent — a second
  request from the owner is a no-op, not a second pump).
- **`--off`**: clear `eventOwner` and cancel the pump. `ark ui event` serves
  again on the next call.

### `eventPump(ctx)` (R3147)
The single reader. Loops until ctx is cancelled:
1. Call `uiMux.ServeHTTP(rec, GET /wait?timeout=<window>)` in-process against
   a minimal capturing ResponseWriter — the same long-poll `ark ui event`
   makes, without a socket round trip or an HTTP client.
2. **200** → decode the JSON array and `EnqueueLuhmann` one
   `frictionless-event` `LuhmannWork` per element, each carrying that event's
   raw JSON (crc-LuhmannCLI.md). A full queue drops the event rather than
   blocking the pump, consistent with `EnqueueLuhmann`'s non-blocking contract
   (R3024).
3. **204** (idle timeout, no events) → loop immediately; the long-poll is its
   own pacing.
4. **404** (no active UI session) → pause briefly, then loop. The UI is
   optional and may arrive later, so this is a wait condition, not an error
   (Stubborn Plumbing).
5. An **undecodable** body is logged and dropped; the pump keeps reading.

flib's `/wait` does not watch the request context, so a cancel can land while
a poll is in flight. The pump **enqueues whatever that poll drained anyway**,
before honoring the cancel. The drain is destructive, so discarding the batch
would silently eat a user's event, while delivering one final batch to a tube
that still exists is merely late. Loss is the worse failure of the two.

### `gateFrictionlessWait(next http.Handler) → http.Handler` (R3146)
Wraps ark's whole mux at `http.Serve`. A `GET /wait` while `EventOwner()` is
non-empty gets **409** naming the routing owner; everything else passes
through untouched. The gate is a wrapper rather than a route because flib
already registered `/wait` on the mux and a second registration would collide;
wrapping also keeps flib's route list out of ark, so a new flib endpoint needs
no change here.

The reason exactly one reader is served: the drain is **destructive** — each
event is delivered once and cleared — so two readers would split the stream,
each seeing an arbitrary half and neither the whole. The second reader is
refused rather than served badly.

### `EventOwner() → string`
Accessor under `luhmannMu`; the gate's predicate.

### `clearEventRouting()` — called from `claimLuhmann` (R3148)
Routing does not inherit. When the seat changes to a *different* session
(`--first` on an unowned seat, or `--force`), clear `eventOwner` and cancel
the pump, so the incoming orchestrator starts without routing and must ask for
itself. Called with `luhmannMu` already held, which is what makes seat change
and routing clear one atomic step — an orchestrator can never observe itself
owning a seat whose routing belongs to its predecessor. Until it asks, the
events fall back to `ark ui event` (R3146).

## Collaborators
- LuhmannCLI (crc-LuhmannCLI.md): the seat this privilege hangs off —
  `luhmannMu` + `luhmannOwner` (R3012) for the gate and the no-inheritance
  clear, `EnqueueLuhmann` + the `frictionless-event` kind for delivery
  (R3147).
- FlibRuntime (crc-FlibRuntime.md): registers the `/wait` handler this card
  reads in-process and gates from outside. Read-only — no flib change.
- Server (crc-Server.md): hosts the `POST /luhmann/events` route, the
  `eventOwner` / `eventPumpCancel` state, the mux reference, and installs the
  gate wrapper at `http.Serve`.
- CLITree (crc-CLITree.md): the `events` subcommand proxies here (R3145).

## Sequences
- seq-luhmann-events.md
