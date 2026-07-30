---
name: recall
description: "Start ambient recall for this session — the corpus quietly surfaces related material as the conversation unfolds. Run when the user types /recall (or asks to turn recall / recollections on). Requires /bloodhound. Until this runs, this session gets no ambient recall."
---
<!-- CRC: crc-RecallAgentBuilder.md, crc-RecallAgent.md | R2865, R2866, R2890, R2950, R3294, R3296, R3302, R3303, R3304, R3312 -->

# Turn on ambient recall (level 4)

Ambient recall is the *push* layer on top of the directed-search bloodhound: as
the conversation unfolds, the corpus surfaces related material **unasked**. You
opt in with one subscription, and that subscription is the whole of it.

**First, invoke `/bloodhound`** — it explains the consumer loop this builds on,
and the optional secretary. Ambient recall adds the `--recall` flag to that one
consumer loop; nothing else changes.

**You do not need a secretary of your own.** This used to be the one level that
did, and it is the sentence most likely to be remembered wrongly, so: the gate
is your subscription and nothing else. Who *serves* a recollection is a separate
decision the watcher makes — your own secretary if you have one, otherwise a
Luhmann-hosted `recall`-pool secretary. Spawn one only for the reasons
`/bloodhound` gives (no Luhmann seated, or heavy iterative search you would
rather not share), never because ambient demands it.

## Run the consumer with `--recall`

Ambient is opt-in via a subscription. Run your consumer loop **with `--recall`**:
```
~/.ark/ark bloodhound listen --recall
```
Run it backgrounded (`run_in_background: true`). `--recall` adds the
`@ark-recall-result` subscription — and *that* sub is what tells the watcher to
fire ambient curation. It's still **one** consumer loop: if `/bloodhound`
already started a plain `listen`, replace it with this one.

(`--ambient` is an accepted alias for `--recall`, and `ark connections recall
listen --ambient` is the older spelling of the whole verb. Both still work;
prefer the form above.)

The loop returns **three** kinds of result:

- **`## Finding:`** — a directed answer to a `<BLOODHOUND>` you emitted. Fold it
  into your reasoning (you asked for it).
- **`## Surface:`** / **`## Recommend:`** — *ambient* offers you didn't ask for: a
  chunk worth showing the user (each with `path:range`); a tag worth attaching.
- **`## Unservable:`** — nothing can serve your recollections, and the watcher is
  telling you rather than going quiet. Almost always no Luhmann is seated and
  you have no secretary. **This one needs a decision from you**, not a mention:
  spawn a secretary per `/bloodhound` step 2, or tell the user the orchestrator
  is down. A merely *busy* pool never sends this — it stays silent and arrives
  late, so silence means waiting, and this message means waiting forever.

A `path:range` may be a chat sub-chunk locator — `path:range:"<snippet>"`. The
snippet is the matched paragraph of a (large) conversation turn; `ark chunks
path:range:"<snippet>"` fetches just it. **Drop the `:"<snippet>"`** (fetch
`path:range`) to get the whole turn for fuller context.

**You decide what, if anything, to show the user.** An ambient offer is an
offer, not an obligation. Skip what's stale, off-topic, or just discussed. When
something genuinely helps, weave it in naturally ("related — you wrote about X in
project Y…") with the path. If the user rejects a recommended tag, run
`~/.ark/ark connections recall reject-derived`. Then **immediately relaunch**
another background `listen --recall` to keep the loop going.

## The rules

Run the consumer backgrounded; never block the conversation; never poll or
narrate the waiting. Surface what helps when a result returns, relaunch
`listen --recall`, and let `/bloodhound` respawn any secretary you chose to
start. To drop back to just the bloodhound (no ambient firehose), run `listen`
**without** `--recall`; to stop entirely, stop the loop.
