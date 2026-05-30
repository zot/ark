---
name: recall
description: "Start ambient recall for this session — the corpus quietly surfaces related material as the conversation unfolds. Run when the user types /recall (or asks to turn recall / recollections on). Until this runs, this session gets no recall."
---
<!-- CRC: crc-RecallAgentBuilder.md | R2865, R2866 -->

sessionid=${CLAUDE_SESSION_ID}

# Start the recall loop

Ambient recall is opt-in per session. Running this subscribes the
session to its results and starts a background loop; until you do, the
recall service does nothing for this session.

Kick it off, using the session id above:

```
~/.ark/ark connections recall listen --session <sessionid>
```

Run it as a **background** command (`run_in_background: true`) so you stay
conversational while it waits. It blocks until the corpus has a
recollection for this conversation, then completes — and the harness
notifies you.

When that background command completes, it hands you a recall result and
tells you what to do. The result is:

- `## Surface:` items — chunks the corpus thinks relate to the
  conversation (each with its `path:range`).
- `## Recommend:` items — tags worth attaching to a chunk.

**You decide what, if anything, to show the user.** A recollection is an
offer, not an obligation — you have final say. Skip what's stale,
off-topic, or something you just discussed. When something genuinely
helps, weave it in naturally ("related — you wrote about X in project Y…")
and include the path so the user can follow it. If the user rejects a
recommended tag, run `~/.ark/ark connections recall reject-derived`.

Then **immediately launch another background `~/.ark/ark connections
recall listen --session <sessionid>`** to keep the loop going.

The rules: run the listen backgrounded, never block the conversation on
it, never poll or narrate the waiting. Surface what helps when a result
returns, relaunch, repeat. To stop recall for the session, just stop
relaunching the listen.
