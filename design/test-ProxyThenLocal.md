# Test Design: A successful proxy is the end of the road

**Source:** crc-CLI.md

Guards R3392–R3395. The defect these exist for is not a wrong answer but a
*second* answer sought from the wrong place: a command reached the server, got
what it asked for, and then opened the index for the rest — which the server
still holds. Before R3391 that hung; with the bound it fails, blaming the lock
for something the server could have returned.

It hid for the same reason both times. Each command's proxy branch is obviously
correct, and the local open sits far enough below it to read as the *other* arm
of the same choice rather than as a step that runs after the first arm already
won. So the tests here pin the **decision**, not the plumbing — a future command
copying `cmdStatus` gets the shape from `planStatus` or fails these.

## Test: the status dispatch decision

**Purpose:** R3392, R3393, R3394 — where each part of `ark status`'s output
comes from, for every combination of (server up, `--chunks`, `--tokenize`).
**Input:** `planStatus(serverUp, wantChunks, wantTokenize)` — a pure function
over three booleans, so no socket, no lock, no subprocess, and all eight cases
are one table.
**Expected:**
- server up, `--chunks --tokenize` → refuse, naming the server as the thing to
  stop. Never a plan that opens the index.
- server up, `--chunks` → proxy both the status block and the chunk statistics.
  `localChunks` is false; this is the case that used to hang.
- server up, plain → proxy, no chunk work at all.
- no server → one local open answers everything, `--tokenize` allowed.
**Fire alarm:** make the `serverUp && wantChunks` case return `localChunks:
true` — the literal pre-repair behaviour — and confirm the table goes red on
that row and only that row. A change that reintroduces the defect cannot be
subtle here: it *is* this field.
**Refs:** crc-CLI.md, specs/cli-dispatch.md, specs/chunk-stats.md, R3392,
R3393, R3394
**Code:** cmd/ark/main_test.go

## Test: warnings survive the proxy

**Purpose:** R3395 — E-record conditions ride in `StatusInfo`, so they print on
either path. Previously they were read inside whatever local open `ark status`
happened to make, which meant the plain proxied form printed none: a condition
serious enough to warrant a warning was invisible in exactly the state ark
normally runs in, and removing the local open would have made that permanent.
**Input:** a `Store` with two E records written; call `Store.Warnings()`, the
conversion `Status()` delegates to. Asserted at the Store rather than through
`Status()` because no test in the repo builds a whole `*DB` — see below.
**Expected:** both conditions come back, keyed by name, each carrying its text
(a name with no text tells the user nothing to do, R1566). Empty map, not a
nil-crash, on a fresh store, so R1567's "nothing extra is printed" still holds.
**Fire alarm:** make the conversion drop its values and confirm red. This is the
one that would otherwise pass by default — a status carrying no warnings looks
exactly like a healthy index, which is the whole reason the old arrangement went
unnoticed.
**Refs:** crc-Store.md, crc-DB.md, crc-Server.md, specs/config-tracking.md,
R3395, R1565, R1566, R1567
**Code:** store_test.go

## Test: no command treats a refused proxy as an absent server

**Purpose:** R3398 — dispatch has three outcomes (nothing answered, served,
refused) and only the first licenses opening the index. Two commands dialed for
themselves and wrote the natural spelling, `if err := proxyDecode(...); err ==
nil { print; return }`, which drops the error and continues into the local arm.
Since a running server holds the index, that arm can only fail — so the command
reports a *lock* where the server had reported the real problem.
**Input:** every `.go` file under `cmd/ark/`, source-scanned for a line that
calls `proxyDecode` / `proxyRaw` / `proxyOK` and tests `err == nil`. Comments are
stripped first: the prose here describes the trap, and only code sets it.
**Expected:** zero occurrences. A hit names the file and line, and says what to
use instead and why.
**Fire alarm:** put the literal pre-repair line back into `message inbox` and
confirm this goes red naming that file. Chosen over `search` deliberately —
inbox is the site the `#115` audit could never make fail, because nothing would
make `/inbox` error, so the alarm is the only evidence that half of the repair
was needed at all.
**Refs:** crc-CLI.md, specs/cli-dispatch.md, R3398, R3003, R2996
**Code:** cmd/ark/main_test.go

This is a Sleeping Sentry, and a source scan for the same reason
`test-LocalOpenBound.md` is one: the behaviour needs a live server that errors
on demand, while the invariant that regressed is one grep wide — and a command
added next year gets this wrong by copying whichever neighbour it reads first.

## Not covered here, and deliberately

Two wirings are asserted by review rather than by test, and both are one call
site each.

That the *CLI* honours the plan — that `cmdStatus` calls `planStatus` and obeys
it — needs the command body, which ends in `fatal` (`os.Exit`) on the refusal
branch. That is the same subprocess harness gap O198 already names for R3391's
timeout behaviour. The decision is the part that was got wrong and the part a
future command will copy.

That `Status()` calls `Store.Warnings()` is likewise unasserted, because no test
in the repo builds a full `*DB` — doing so means a first harness (microfts2
open, config, strategy registration) for three lines of fill. Banked as a gap
rather than left unsaid; it is the honest weak point of this pair, since the
failure it would catch is silent by nature.
