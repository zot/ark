---
name: bloodhound
description: "Turn on the warm search bloodhound for this session — spawn the per-session secretary and listen for findings, so you can direct a search by emitting a <BLOODHOUND>…</BLOODHOUND> watermark and get a curated answer back. Run when the user types /bloodhound or asks to turn on directed search / the bloodhound. Requires /ark."
---
<!-- CRC: crc-RecallAgent.md, crc-RecallAgentBuilder.md | R2890, R2947, R2950 -->

sessionid=${CLAUDE_SESSION_ID}

# Turn on the bloodhound (directed search — level 3)

**First, invoke `/ark`** (which loads `/ark-search` — the detective's craft and
how to direct the bloodhound). The bloodhound is the *warm* path for that craft:
with it running, you direct a search by emitting a `<BLOODHOUND>…</BLOODHOUND>`
watermark in your normal output, and a curated **finding** comes back through
your `listen`. (Ambient recall — the corpus surfacing material *unasked* — is the
next level up: `/recall`, which builds on this.)

Two roles, both in your session: a per-session **secretary** (a Haiku subagent
you spawn) that runs the hunt, and **you**, who consume its findings. Until both
run, the bloodhound does nothing.

## 1. Spawn the secretary

Reserve a nonce:
```
~/.ark/ark connections recall reserve-nonce
```
Then launch it with the **Task tool**, `run_in_background: true`:
- `subagent_type`: `ark-recall-agent`
- `description`: `ark-recall secretary loop nonce <N>` — the `nonce <N>`
  substring is how the server finds the subagent's transcript; it must be present.
- `prompt`: `Start the recall secretary loop now. Session: <sessionid>. Nonce: <N>.`

The secretary loops internally (`recall next --session <sessionid> <N>`), draining
its `@ark-secretary-work` tube — search tasks now, curation docs too once ambient
is on — until its context fills, then it exits and the harness notifies you
(step 3).

## 2. Run the consumer loop

Start the consumer, backgrounded:
```
~/.ark/ark connections recall listen --session <sessionid>
```
Run it with `run_in_background: true` so you stay conversational. It subscribes to
`@ark-bloodhound-result` (the bloodhound opt-in) and blocks until a result
arrives, then completes and the harness notifies you. At this level the results
are **`## Finding:`** items — a curated answer to a `<BLOODHOUND>` you emitted,
headed by your own clue echoed back. **Fold a finding into your reasoning** (you
asked for it); surface it to the user only if it helps them. Then **relaunch**
`listen` to keep the loop going.

## 3. Respawn the secretary when it exits

When the secretary subagent **completes** (the harness notifies you), it hit its
context limit — normal and expected, not a failure. Reserve a fresh nonce and
spawn it again exactly as in step 1. That is the whole of supervision: no streak
machine, no backoff. If it fails *repeatedly* in quick succession (not a clean
context-limit exit), stop respawning and tell the user something is wrong.

## The rules

Run both the secretary and `listen` backgrounded; never block the conversation;
never poll or narrate the waiting. A finding is **async** — emit the watermark,
keep working, fold the answer in when it lands. To stop the bloodhound, stop
relaunching `listen` and stop respawning the secretary (its subscriptions drop
and the service goes quiet for this session). To add **ambient recall** on top,
run `/recall`.
