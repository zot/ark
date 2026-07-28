# Bloodhound: two doors to the same pool

**Problem.** Ark has two ways to run a directed hunt — an in-conversation
`<BLOODHOUND>` watermark and the `ark bloodhound search` CLI — and only one of
them could reach Luhmann's pool of warm secretaries. A session with Luhmann
awake and secretaries idle a few feet away still had to spawn its own Haiku
that duplicated them.

Opened 2026-07-27. Promoted from `.scratch/` 2026-07-28 on spawning its second
item.

| Part | Status |
|---|---|
| ~~#80 — split recognition from service; pool fallback; not-served report~~ | **Landed** 2026-07-28, git `4c69e33` |
| #84 — a reaped watermark hunt is still silent | Open — **defect in landed code** |
| #83 — `/bloodhound` skill still calls the secretary spawn a precondition | Open |

**Where the decisions went.** Behavior is canonical in `specs/bloodhound.md`
("Serving a watermark") and `specs/bloodhound-cli.md` ("Tag proposals are
origin-dependent"), as R3279–R3283. This document is the reasoning trail and
the split — read the specs for what the system does.

---

## What was wrong

`<BLOODHOUND>` recognition required a secretary subscribed to **its own
session's** work tube. Three lines held it:

- `bloodhoundEnabled()` = `secretaryPresent(sessionID)` **AND** an
  `ark-bloodhound-result` subscriber.
- `secretaryPresent()` = `SubscriberCount("ark-secretary-work", sessionID) > 0`
  — session-scoped.
- `OnAppend` returned early when the secretary was absent, discarding the
  watermark *before* dispatch was ever considered.

So the two doors were separate, and only one reached the pool:

| door | reached the pool? | who curates |
|---|---|---|
| `<BLOODHOUND>` watermark | **no** — session-local secretary only | session secretary |
| `ark bloodhound search` | yes (S2 Fixer → S1 Luhmann seat) | secretary **+ Luhmann** |
| `ark bloodhound search --raw` | yes | secretary only; **caller curates** |

**The second defect was worse than the first.** Because the watermark was
dropped at the gate with no signal, an unservable hunt was indistinguishable
from a hunt that found nothing. An assistant blocked on `listen` waits forever
on a request nobody holds. That is the `try-the-handle` failure shape: an
apparently-locked door with nothing reporting that it is locked.

## How it was found

Bill launched Luhmann and said *"we don't need the secretary — hopefully ark
routes your request to luhmann."* It did not. Two watermarks were emitted after
the session secretary was killed; neither produced a finding, and
`ark luhmann status` stayed at `0 pool secretary(ies)` throughout. Reading the
gate explained why, and the reasonable expectation behind the remark turned out
to be a better design than what was implemented.

## Decisions

- **DECIDED (Bill, 2026-07-27):** the watermark path should reach the pool. The
  per-session secretary becomes an optimization, not a precondition.
- **DECIDED (Daneel, 2026-07-27, ratified by the spec):** separate *recognition*
  from *service*. Recognition gates on the bloodhound-result subscription alone;
  who serves a recognized hunt is a later, independent choice. Conflating them
  is what made a staffed pool worthless to a watermark **and** made silence the
  failure mode. → R3279/R3280.
- **DECIDED:** a pool-served watermark is always **raw**, returning to the
  emitting session rather than through Luhmann. Not a shortcut — a watermark is
  by construction emitted by an assistant holding the live context that provoked
  it, so it *is* the curate-in-your-own-context case, and Luhmann's
  separate-context judgment would be both the wrong judge and a serial step on
  the latency-sensitive path. → R3281.
- **DECIDED:** an unservable hunt **reports**; silence is the one unacceptable
  outcome. → R3282.
- **DECIDED (Bill, 2026-07-28):** tag proposals are origin-dependent —
  session-delegated hunts propose, CLI-delegated ones stay findings-only,
  because a script has no assistant to winnow proposals. → R3283.

## What the work turned up

**Proposals on the pool path were broken, not merely unwanted.**
`RecommendItem` had no branch for a request-doc cookie, so a proposal fell
through to `openResult`, which has no in-flight curation doc for a bare request
id and fails. The generalizable shape, now recorded in `specs/bloodhound-cli.md`:
**an accumulator and its flush must be keyed the same way**, or items land where
nothing drains them.

**A claim was corrected by testing it.** That defect was first written up — in
the spec, the requirement, and a code comment — as a *silent* drop. Reverting
the fix to confirm the new test actually went red produced the real error
message and corrected the claim before it shipped in a public spec. Cost: one
command. This is the `sleeping-sentry` discipline paying for itself immediately.

**Pool growth is operator-driven, not demand-driven.** Observed 2026-07-27: the
pool sat at `0 pool secretary(ies)` before *and* after a CLI hunt that
nonetheless returned findings, and only reached 2 once Bill grew it through
Luhmann's seat. A fallback that assumes an idle pool will grow itself would find
none — which is why `poolCanServe()` checks for room to grow rather than
assuming it.

## Open parts

### #84 — a reaped watermark hunt is still silent (defect in landed code)

**R3282 covers "cannot be served *at dispatch*", not "accepted, then stranded".**
`sweepRequests` reaps a pending request older than `request_ttl_seconds`
(default 900s): it drops the entry from `requests`/`pending`, removes the
request doc, logs at `-v`, and notifies nobody. For a CLI hunt that is right —
the client hit `--timeout` and exited, so there is no one to tell. For a
**watermark** hunt the emitting session is alive and still blocked on `listen`.

The path that reaches it, and it is not exotic:

1. A session with no secretary emits a watermark.
2. `poolCanServe()` returns true because the pool has *room to grow*.
3. `enqueueWatermarkHunt` queues it and pushes a stand-up onto Luhmann's `next`.
4. Nothing stands up — pool growth is **operator-driven**, as recorded above,
   and a dropped directive only earns one re-issue attempt.
5. Fifteen minutes later the request is reaped. The session waits forever.

So the silence this carve exists to eliminate was moved rather than removed:
from the gate to the reaper. Same symptom, longer fuse, and harder to spot
because the hunt appeared to be accepted.

**Fix direction (not yet a decision).** The reap path already knows the request
record, so it knows the origin. A watermark-origin reap should emit the same
not-served `## Finding:` doc R3282 defines, with a reason naming the strand
("queued but no secretary ever became available") rather than the dispatch-time
reasons. `emitNotServed` is close to reusable — it currently takes a
`bloodhoundReq`, and the reaper holds a `cliRequest`, so either the reason
string or the shared header helper wants factoring out.

Worth deciding at the same time: whether `poolCanServe()` should count *room to
grow* as servable at all when growth is operator-driven. Returning false when no
secretary is actually free would fail fast and honestly at step 2, at the cost
of refusing hunts a responsive Luhmann would have served.

### #83 — the `/bloodhound` skill understates the machinery

`.claude/skills/bloodhound/SKILL.md` presents spawning a session secretary as
**step 1, a precondition**. Since R3279/R3280 it is an optimization: a session
can subscribe with `listen` and borrow the pool with no spawn at all. Nothing
breaks — the spawn still works and is still preferred — but the skill describes
less than the system does, which is how a capability quietly goes unused.

What to say when updating it:

- The spawn is **optional** when a Luhmann pool is staffed.
- Skipping it costs the pool's scheduling latency and sharing a secretary with
  other hunts.
- It does **not** cost the tag recommends: a session-delegated pool hunt
  proposes exactly as an in-session one does (R3283).

## Interim workaround (still valid for callers outside a session)

`ark bloodhound search --raw --want passages …` — the pool hunts, the caller
curates. Verified 2026-07-27; returns a markdown locator list.
