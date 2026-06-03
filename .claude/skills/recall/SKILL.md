---
name: recall
description: "Start ambient recall for this session — the corpus quietly surfaces related material as the conversation unfolds. Run when the user types /recall (or asks to turn recall / recollections on). Until this runs, this session gets no recall."
---
<!-- CRC: crc-RecallAgentBuilder.md | R2865, R2866, R2890 -->

sessionid=${CLAUDE_SESSION_ID}

# Start ambient recall

Ambient recall is opt-in per session and has **two roles**, both living in
your session:

- a per-session **secretary** (a Haiku subagent you spawn) that curates the
  corpus's raw matches against this conversation, and
- **you**, the consumer, who decides what (if anything) to surface to the
  user.

You spawn and supervise the secretary; you run the consumer loop. Until
both are running, the recall service does nothing for this session.

## 1. Spawn the secretary

Reserve a nonce:

```
~/.ark/ark connections recall reserve-nonce
```

Then launch the secretary with the **Task tool**, `run_in_background: true`:

- `subagent_type`: `ark-recall-agent`
- `description`: `ark-recall secretary loop nonce <N>` — the `nonce <N>`
  substring is how the server finds the subagent's transcript; it must be
  present.
- `prompt`: `Start the recall secretary loop now. Session: <sessionid>.
  Nonce: <N>.`

The secretary loops internally (`recall next --session <sessionid> <N>`),
curating this session's matches, until its context fills — then it exits
and the harness notifies you (step 3).

## 2. Run the consumer loop

Start the consumer, backgrounded, using the session id above:

```
~/.ark/ark connections recall listen --session <sessionid>
```

Run it with `run_in_background: true` so you stay conversational. It blocks
until the secretary has a recollection for this conversation, then
completes and the harness notifies you. The result is:

- `## Surface:` items — chunks worth showing the user (each with `path:range`).
- `## Recommend:` items — tags worth attaching to a chunk.

A `path:range` may be a chat sub-chunk locator — `path:range:"<snippet>"`. The
snippet is the matched paragraph of a (large) conversation turn; `ark chunks
path:range:"<snippet>"` fetches just it. **Drop the `:"<snippet>"`** (fetch
`path:range`) to get the whole turn for fuller context.

**You decide what, if anything, to show the user.** A recollection is an
offer, not an obligation. Skip what's stale, off-topic, or just discussed.
When something genuinely helps, weave it in naturally ("related — you wrote
about X in project Y…") with the path. If the user rejects a recommended
tag, run `~/.ark/ark connections recall reject-derived`. Then **immediately
relaunch** another background `listen` to keep the loop going.

## 3. Respawn the secretary when it exits

When the secretary subagent **completes** (the harness notifies you), it
hit its context limit — that is normal and expected, not a failure.
Reserve a fresh nonce and spawn it again exactly as in step 1. That is the
whole of supervision: no streak machine, no backoff. If the secretary ever
fails *repeatedly* in quick succession (not a clean context-limit exit),
stop respawning and tell the user something is wrong.

## The rules

Run both the secretary and the `listen` backgrounded; never block the
conversation on either; never poll or narrate the waiting. Surface what
helps when a result returns, relaunch `listen`, respawn the secretary when
it exits. To stop recall for the session, stop relaunching `listen` and
stop respawning the secretary.
