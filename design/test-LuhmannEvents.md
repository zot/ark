# Test Design: LuhmannEvents
**Source:** crc-LuhmannEvents.md

The routing state machine and the gate are pure decision logic over one
mutex-guarded pair of fields, so they test against a zero-value `Server` with
no DB, no listener, and no live UI. The pump tests substitute a fake `/wait`
handler on a throwaway mux — the same seam the real pump reads, so no HTTP
client, socket, or Frictionless session is involved.

## Test: opt-in requires the seat
**Purpose:** routing is a privilege of the `next` seat, not a second identity (R3145)
**Input:** `luhmannOwner = "A"`; call `LuhmannEvents("B", false)`
**Expected:** errors with R3013's `you don't have ownership` string; `eventOwner` stays `""`; no pump started
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.2

## Test: opt-in refused on an unowned seat
**Purpose:** an unowned seat grants no privilege — no routing without an orchestrator (R3145)
**Input:** `luhmannOwner = ""`; call `LuhmannEvents("A", false)`
**Expected:** errors `you don't have ownership`; `eventOwner` stays `""`
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.2

## Test: the seat owner claims routing
**Purpose:** the happy path sets ownership and starts exactly one pump (R3145)
**Input:** `luhmannOwner = "A"`; call `LuhmannEvents("A", false)`
**Expected:** no error; `EventOwner() == "A"`; `eventPumpCancel != nil`
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.3

## Test: re-claiming is idempotent
**Purpose:** a second request from the owner must not start a second reader — two pumps would split the stream, the exact harm the single-reader rule exists to prevent (R3146)
**Input:** `luhmannOwner = "A"`; call `LuhmannEvents("A", false)` twice, capturing `eventPumpCancel` after each
**Expected:** no error; `EventOwner() == "A"`; the cancel func is unchanged, so the first pump was neither replaced nor duplicated
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.3

## Test: `--off` releases routing
**Purpose:** release restores `ark ui event` (R3145)
**Input:** `luhmannOwner = "A"`, routing claimed; call `LuhmannEvents("A", true)`
**Expected:** no error; `EventOwner() == ""`; the pump's context is cancelled; `eventPumpCancel == nil`
**Refs:** crc-LuhmannEvents.md

## Test: routing does not inherit across a seat change
**Purpose:** the no-inheritance rule — a new orchestrator must ask for itself (R3148)
**Input:** `luhmannOwner = "A"` with routing claimed; `claimLuhmann("B", luhmannModeForce)`
**Expected:** `luhmannOwner == "B"`; `EventOwner() == ""`; the pump's context is cancelled
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#3.2

## Test: re-claiming the seat as the same session keeps routing
**Purpose:** the no-inheritance clear keys on a *different* session, so an orchestrator re-establishing its own seat (`--first` after a reconnect) does not lose the routing it already asked for (R3148)
**Input:** `luhmannOwner = "A"` with routing claimed; `claimLuhmann("A", luhmannModeFirst)`
**Expected:** `luhmannOwner == "A"`; `EventOwner() == "A"`; the pump is untouched
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#3.2

## Test: gate refuses `/wait` while routing is owned
**Purpose:** the single-reader invariant is enforced at the door (R3146)
**Input:** routing owned by `"A"`; `GET /wait` through `gateFrictionlessWait(inner)`
**Expected:** 409; the response names the owner; `inner` is never invoked
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#2.2

## Test: gate passes `/wait` through when unowned
**Purpose:** the opt-in changes nothing for a session that never asks (R3146)
**Input:** `eventOwner == ""`; `GET /wait` through `gateFrictionlessWait(inner)`
**Expected:** `inner` is invoked and its response is returned unmodified
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#2.3

## Test: gate leaves every other path alone
**Purpose:** the wrapper gates one path, so flib's other routes are untouched even while routing is owned (R3146)
**Input:** routing owned; `GET /state` and `POST /api/ui_run` through the gate
**Expected:** both reach `inner` unmodified
**Refs:** crc-LuhmannEvents.md

## Test: pump enqueues one work item per event
**Purpose:** a 200 batch fans out to individual tube items of the new kind (R3147)
**Input:** fake `/wait` returns `200 [{"a":1},{"b":2}]` once, then blocks until ctx cancel; pump runs against a `Server` with an empty `nextQueue`
**Expected:** two `LuhmannWork` items drain off `nextQueue`, each `Kind == "frictionless-event"`, carrying `{"a":1}` and `{"b":2}` respectively, in order
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.7

## Test: pump loops on an idle timeout
**Purpose:** 204 is the normal idle case, not a stop condition (R3147)
**Input:** fake `/wait` returns 204 twice, then `200 [{"a":1}]`
**Expected:** nothing enqueued for the 204s; the event from the third call enqueues; the handler was called three times
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.6

## Test: pump treats a missing UI session as a wait condition
**Purpose:** the UI is optional and may arrive later — Stubborn Plumbing, not an error (R3147)
**Input:** fake `/wait` returns `404 No active session`, then `200 [{"a":1}]`
**Expected:** the pump does not exit; nothing enqueued for the 404; the later event enqueues
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.6

## Test: pump exits on cancel
**Purpose:** pump lifetime is bounded by routing ownership (R3145, R3148)
**Input:** pump running against a fake `/wait`; cancel its context
**Expected:** the pump goroutine returns; no further `/wait` calls are made
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.10

## Test: a full queue drops rather than blocking the pump
**Purpose:** `EnqueueLuhmann`'s non-blocking contract (R3024) must not be defeated by the pump — a stalled orchestrator cannot wedge the reader
**Input:** `nextQueue` filled to capacity; fake `/wait` returns `200 [{"a":1}]`
**Expected:** the pump does not block; it proceeds to its next `/wait` call
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.7

## Test: the `POST /luhmann/events` handler returns the statuses the CLI expects
**Purpose:** the CLI's `proxyRaw` treats any status but 200 as an error, so a "successful" 204 reaches the user as `server error (204)`. The tests above call `LuhmannEvents` directly and cannot see the round trip; this one pins it (R3145)
**Input:** seat owner `"A"`; POST bodies `{"session":"A","off":false}`, `{"session":"A","off":true}`, `{"session":"B","off":false}`, and a malformed body
**Expected:** 200, 200, 409, 400 respectively
**Refs:** crc-LuhmannEvents.md, seq-luhmann-events.md#1.1

## Test: `frictionless-event` renders a crank handle that leads with re-launch-first
**Purpose:** the new kind joins the loop-continuity contract every work kind honors (R3036, R3147)
**Input:** `luhmannWorkPrompt("S", LuhmannWork{Kind: "frictionless-event", Event: "{\"task\":\"x\"}"}, "")`
**Expected:** the body opens with the `relaunchFirst` preamble naming `next --session S`, and carries the event payload; it is not the unrecognized-kind fallback
**Refs:** crc-LuhmannCLI.md, seq-luhmann-events.md#1.8
