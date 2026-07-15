# Sequence: `ark luhmann events` — Frictionless events onto the tube

**Requirements:** R3145, R3146, R3147, R3148

How a Lua app's `mcp.pushState()` reaches the hosted orchestrator instead of
`ark ui event`. The opt-in starts a single in-process reader of flib's `/wait`;
that reader produces onto the same tube `next` drains.

Participants: **Luhmann** (the hosted orchestrator session) · **LuhmannEvents**
(crc-LuhmannEvents.md) · **Gate** (`gateFrictionlessWait`, wraps ark's mux) ·
**Mux** (ark's mux; flib registered `/wait` on it, crc-FlibRuntime.md) ·
**Tube** (`nextQueue` + `LuhmannNext`, crc-LuhmannCLI.md) · **App** (a
Frictionless Lua app) · **Agent** (any non-Luhmann `ark ui event` caller).

```
1.    Luhmann: ark luhmann events --session S            [opt-in; already owns the next seat]
1.1     Luhmann -> LuhmannEvents: POST /luhmann/events {session:S, off:false}
1.2     LuhmannEvents: under luhmannMu — S != luhmannOwner ? -> 4xx "you don't have ownership" (R3145)
1.3     LuhmannEvents: eventOwner := S ; start eventPump(ctx) ; store eventPumpCancel  (idempotent for S)
1.4     LuhmannEvents -> Mux: ServeHTTP(rec, GET /wait?timeout=120)   [in-process; bypasses Gate by construction]
1.5     App -> Mux: mcp.pushState({task:"summarize this listing, add a job item"}) -> stateQueue, signals waiters
1.6     Mux -> LuhmannEvents: 200 [event, ...]           (204 idle -> loop ; 404 no UI session -> pause, loop)
1.7     LuhmannEvents -> Tube: EnqueueLuhmann(frictionless-event{event}) per element  (R3147)
1.8     Tube -> Luhmann: LuhmannNext returns the frictionless-event crank-handle (leads w/ re-launch-first, R3036)
1.9     Luhmann: fires next#2 (bg), does the work; effects reach the user via app-data + conversation
1.10    LuhmannEvents: loop to 1.4 until ctx cancelled                 [the single reader, R3146]
```

```
2.    Agent: ark ui event                                [while routing is owned]
2.1     Agent -> Gate: GET /wait?timeout=120
2.2     Gate: EventOwner() != "" ? -> 409 "orchestrator <S> owns event routing" ; never reaches Mux (R3146)
2.3     Gate: EventOwner() == "" -> pass through to Mux -> flib handleWait, exactly as before (R3146)
```

```
3.    Luhmann': ark luhmann next --session S' --force     [a new orchestrator takes the seat]
3.1     Luhmann' -> LuhmannCLI: claimLuhmann(S', force) under luhmannMu
3.2     LuhmannCLI: luhmannOwner := S'  AND  clearEventRouting() — same lock, one step (R3148)
3.3     LuhmannEvents: eventOwner := "" ; eventPumpCancel() -> pump exits at 1.10
3.4     Agent: ark ui event -> serves again (2.3); Luhmann' has no routing until it asks for itself (R3148)
```

Note — 3.2 is why `eventOwner` shares `luhmannMu` rather than taking a lock of
its own: seat change and routing clear are one atomic step, so an orchestrator
can never observe itself owning a seat whose routing still belongs to its
predecessor. The pump at 1.4 calls the mux directly while every external caller
arrives through the Gate wrapping it, so the single-reader invariant needs no
exemption marker on the pump's own request.
