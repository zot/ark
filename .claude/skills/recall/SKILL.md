---
name: recall
description: "Start ambient recall for this session — the corpus quietly surfaces related material as the conversation unfolds. Run when the user types /recall (or asks to turn recall / recollections on). Requires /bloodhound. Until this runs, this session gets no ambient recall."
---
<!-- CRC: crc-RecallAgentBuilder.md, crc-RecallAgent.md | R2865, R2866, R2890, R2949, R2950 -->

sessionid=${CLAUDE_SESSION_ID}

# Turn on ambient recall (level 4)

Ambient recall is the *push* layer on top of the directed-search bloodhound: as
the conversation unfolds, the corpus surfaces related material **unasked**. It
reuses the same per-session secretary; you opt in with one subscription.

**First, invoke `/bloodhound`** — it spawns the secretary (and owns its
respawn-on-context-exit) and explains the consumer loop. Ambient recall adds the
`--ambient` flag to that one consumer loop; nothing else about the secretary
changes.

## Run the consumer with `--ambient`

Ambient is opt-in via a subscription. Run your consumer loop **with `--ambient`**:
```
~/.ark/ark connections recall listen --session <sessionid> --ambient
```
Run it backgrounded (`run_in_background: true`). `--ambient` adds the
`@ark-recall-result` subscription — and *that* sub is what tells the watcher to
fire ambient curation. It's still **one** consumer loop: if `/bloodhound` already
started a plain `listen`, replace it with this one. The secretary spawn/respawn
from `/bloodhound` is unchanged.

The loop now returns **two** kinds of result:

- **`## Finding:`** — a directed answer to a `<BLOODHOUND>` you emitted. Fold it
  into your reasoning (you asked for it).
- **`## Surface:`** / **`## Recommend:`** — *ambient* offers you didn't ask for: a
  chunk worth showing the user (each with `path:range`); a tag worth attaching.

A `path:range` may be a chat sub-chunk locator — `path:range:"<snippet>"`. The
snippet is the matched paragraph of a (large) conversation turn; `ark chunks
path:range:"<snippet>"` fetches just it. **Drop the `:"<snippet>"`** (fetch
`path:range`) to get the whole turn for fuller context.

**You decide what, if anything, to show the user.** An ambient offer is an
offer, not an obligation. Skip what's stale, off-topic, or just discussed. When
something genuinely helps, weave it in naturally ("related — you wrote about X in
project Y…") with the path. If the user rejects a recommended tag, run
`~/.ark/ark connections recall reject-derived`. Then **immediately relaunch**
another background `listen --ambient` to keep the loop going.

## The rules

Run the consumer backgrounded; never block the conversation; never poll or
narrate the waiting. Surface what helps when a result returns, relaunch
`listen --ambient`, and let `/bloodhound` respawn the secretary when it exits. To
drop back to just the bloodhound (no ambient firehose), run `listen` **without**
`--ambient`; to stop entirely, stop the loop and stop respawning the secretary.
