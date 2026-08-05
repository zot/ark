# Scaling the orchestrator tier

**Problem.** ark has one Luhmann seat. It is the only thing that holds the Task
tool, so it is the only thing that can stand a secretary up, and it is the only
thing that curates a returning hunt. Everything about that is fine at one
seat and nothing about it is written down as a *choice*. Before there is a
reason to add a second, it is worth knowing what a second one would buy, what it
would cost, and which parts of the code already anticipate it.

Opened 2026-07-29, out of the cost measurement in
[done/recall-borrows-the-pool.md](done/recall-borrows-the-pool.md) §#88.

| Part | Status |
|---|---|
| A — decide whether the seat becomes a set, and what selects among them | Open (discussion) |
| B — decide whether `pool_max` is per seat or global | Open (discussion) |
| C — top-level Haiku workers for bursty load | Open (discussion → `/mini-spec`) |

Nothing here is decided. This carve exists so the reasoning survives until
something forces the question.

---

## Status

Nothing here has started; this carve is still a discussion (#95), and the parts
below are its shape rather than a committed split. The checkbox is
authoritative.

- **Part A — the addressing.** Already in place, and not work: a secretary's
  tube is `<luhmann-session>-<nonce>` (R3032), so two seats' pools namespace
  apart with no collision. Built for routing rather than scale-out, but it
  anticipates it. Carries no checkbox because there is nothing to do.
- [ ] **Part B — a second seat currently adds no capacity.** **OPEN, not
      started.** `pool_max` is checked against the watcher's per-class roster,
      which is **global rather than per-owner**, so two seats would share one
      budget of three `recall`-class secretaries between them.
- [ ] **Part C — top-level Haiku workers for bursty load.** **OPEN, not
      started.** Proposed by Bill, 2026-07-29: the lotto tube does not require
      its consumer to be a subagent, so a top-level Haiku session could take
      work the same way, likely cheaper for infrequent high-intensity bursts.

## The shape it already has

**Noted by Bill, 2026-07-29:** this is the shape Classic Blend Smalltalk servers
scaled with in the late 90s. Sessions were slim and **cooperatively** scheduled
as coroutines within a process; processes were fat and **preemptively** scheduled
against each other; a load balancer sat above them.

ark's tiers line up, and one part is literal rather than analogous:

| Blend | ark |
|---|---|
| coroutine session (slim, cooperative) | pool secretary (Haiku subagent) |
| OS process (fat, preemptive) | Luhmann seat (Opus session) |
| load balancer | the watcher — but see below |

**The cooperative scheduling is real, not a metaphor.** A secretary blocks in
`recall next`, is handed one work item or a keepalive, and returns to the queue
of its own accord. Nothing preempts it, and the watcher never interrupts one
mid-item. Two Luhmann seats, by contrast, would share nothing but the ark
server, which is as preemptive as two processes.

## Where the analogy breaks, and why that decides the question

Blend's coroutines **shared the process heap**, and that sharing was the whole
economy: packing more sessions into a fat process amortized the fat process
across them.

ark's secretaries share nothing with Luhmann but a routing prefix. They are
separate subagents with their own contexts; Luhmann spawns them and curates what
they return. **Luhmann is not a container that amortizes anything.** So the
Blend intuition — add processes to add capacity — does not transfer, and the
scarcity has to be located somewhere else.

It is located in exactly two places:

- **The seat lease.** `srv.luhmannOwner` is a single string, read at 13 call
  sites. That is the entire mechanism.
- **Curation judgment.** The discernment a Haiku secretary lacks, which is what
  the seat exists to apply.

Neither is memory, and neither gets cheaper by packing.

## What is already multi-seat, and what is not

**Already ready: the addressing.** A secretary's tube is
`<luhmann-session>-<nonce>` (R3032). Two seats' pools namespace apart with no
collision, because the owning session is already part of every secretary's
identity. This was built for routing, not for scale-out, but it anticipates it.

**Singular:** the `luhmannOwner` lease, and `EnqueueLuhmann`'s single directive
queue. Turning the seat into a set is turning one string into a collection and
giving the watcher a policy for choosing among them — which is precisely the
load-balancer tier of the analogy.

**A structural difference worth deciding on rather than inheriting.** Blend had
two schedulers at two levels: the balancer across processes, the coroutine
scheduler within one. ark has one. The watcher already routes work to individual
secretaries (`pump`, `pumpCuration`), so adding seat selection would put both
tiers in the same component. That is mostly good — one place reasons about
occupancy — but it makes the Fixer the only thing that understands the whole
topology, and that should be a choice rather than a side effect.

## Part B: a second seat currently adds no capacity

`pool_max` is checked against `len(pool.secretaries)` on the watcher's per-class
roster, which is **global rather than per-owner**. Two seats would share one
budget of three `recall`-class secretaries between them.

So a second Luhmann today buys a second curator and a second spawner, and **zero
additional throughput.** Whether that is right is a genuine question and the two
answers are both defensible: global means the cap is a total worker budget,
per-seat means each orchestrator gets its own pool the way Blend gave each
process its own sessions. What is not defensible is arriving at one of them by
accident, which is the current state.

## Part C: top-level Haiku workers for bursty load

**Proposed by Bill, 2026-07-29.** The lotto tube does not require its consumer
to be a subagent. A **top-level** Haiku session could take work the same way, and
for high-intensity, infrequent bursts it is likely cheaper.

The basis is the TTL asymmetry measured in
[done/recall-borrows-the-pool.md](done/recall-borrows-the-pool.md) §#88:
**subagents write prompt cache at a 5-minute TTL, top-level sessions at 1 hour**
(measured from the per-TTL breakdown in transcript usage records — 100%/0% split
either way across 35K turns).

That difference is the whole argument:

- A **subagent** has no cheap idle state. Its cache dies in five minutes, so
  staying warm means polling, and polling is the entire idle cost (~$0.16 per
  standing-hour). Retire it and the next burst pays a cold cache creation.
- A **top-level session** can sit idle at no cost for up to an hour and still
  resume warm.

So for bursts spaced further apart than the ~16-minute retire-vs-keep-warm
break-even but closer than an hour, a woken top-level worker avoids **both** the
idle burn and the cold restart. That is a regime the subagent pool cannot reach
at any tuning.

**Most of the machinery exists.** `PtyHost.Launch(bootstrap)` already hosts a
Claude session; `ark luhmann send` (R3130/R3131) already injects a synchronous
instruction into a hosted session, which is the wake. The blocker is narrow:
`ptyhost.go:196` hardcodes `exec.Command("claude", "--model", "opus")`, so the
host cannot currently run a Haiku worker at all. Making that model a
configuration setting is already queued as its own item (#64), and this makes it
a prerequisite rather than a tidy-up.

### The context floor is controllable — `--system-prompt`

**Noted by Bill, 2026-07-29.** The concern above — that a top-level session
carries the full Claude Code harness prompt and so has a fatter context floor
than a subagent — is not fixed. The `claude` CLI takes `--system-prompt` and
`--system-prompt-file` to **replace** the default outright, and
`--append-system-prompt[-file]` to add to it. A Luhmann is a curator and an
orchestrator; it is not writing code, so most of the coding harness is dead
weight in its prefix. A top-level secretary needs less still.

A third flag is squarely on this path: **`--exclude-dynamic-system-prompt-sections`**
moves the per-machine sections (cwd, env info, memory paths, git status) out of
the system prompt and into the first user message, explicitly "to improve
cross-user prompt-cache reuse". Those are the classic silent cache invalidators
— git status in particular changes constantly in a working repo — and with them
in the prefix, two spawns of the same worker cannot share a cached prefix. The
flag applies only to the default prompt and is ignored alongside
`--system-prompt`, where you control the contents anyway.

### The trap: Haiku 4.5's cache minimum is 4096 tokens

Trimming the prompt is right, and trimming it *too far* silently costs more.
Minimum cacheable prefixes are model-dependent and **not monotonic** — the newest
large models cache from 512 or 1024 tokens, while **Haiku 4.5 needs 4096**, the
strictest tier. Below the minimum, nothing caches, with no error: just
`cache_creation_input_tokens: 0` and every turn billed at full input rate.

Priced for Haiku 4.5 ($1/MTok input, cache read 0.1×):

| prefix | cacheable | $/turn |
|---|---|---|
| 4,000 tokens | **no** | $0.00400 |
| 34,000 tokens (today's secretary) | yes | $0.00340 |
| 5,000 tokens | yes | $0.00050 |

So a worker trimmed to 4,000 tokens is **more expensive per turn than today's
fat one**, despite an eight-fold smaller prompt, because it fell off the caching
cliff. Nudge it to 5,000 and it is seven times cheaper than today. The whole
saving lives in a thousand-token band, on the correct side of a boundary nothing
warns you about.

**Therefore, for any trimmed-prompt worker:** size the prefix comfortably above
4096 rather than as small as possible, and verify empirically —
`usage.cache_read_input_tokens > 0` across repeated turns is the only proof that
caching engaged.

### What still has to be measured

Whether successive spawns of the same worker **share** a cached prefix. If they
do — identical system prompt, same model, inside the 5-minute TTL — then a
respawn pays a cache *read* rather than a *write*, which is 12.5× cheaper and
would move the retire-versus-keep-warm break-even sharply toward retiring early.
That would revise the cooldown reasoning rather than confirm it. Prefix sharing
is exactly what `--exclude-dynamic-system-prompt-sections` (or a fixed
`--system-prompt`) makes possible, and exactly what per-machine sections in the
default prompt prevent.

The comparison overall is *many thin polls* against *few fat wakes*, and which
wins depends on burst shape and on the prefix question above. This carve should
not assert a saving before someone counts it; that is the mistake §#88 was
opened to correct and then had to correct twice in its own write-up.

## Open questions

- **What selects a seat?** Least-loaded, round-robin, affinity by originating
  session, or by class (one seat per pool class)? Affinity has a real argument:
  a hunt's curation is better if the curator has seen that session's earlier
  hunts.
- **Who curates a return when the spawning seat is gone?** A secretary outlives
  a bounce of its owner's session today only by accident. With several seats,
  "the seat that spawned it" and "a seat that can curate it" come apart.
- **Does a top-level worker use the same lotto tube verb, or its own?** The
  crank handle currently tells a secretary to call `recall next --session <S>
  <N>`. A top-level worker's loop shape differs — it ends its turn rather than
  blocking — so the crank handle's next-command may need a second form.
- **Is `pool_max` even the right knob for a mixed fleet?** With subagent
  secretaries and top-level workers serving the same classes, a single count
  caps two things with different cost shapes.
