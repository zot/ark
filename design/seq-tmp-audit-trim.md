# tmp:// audit destination + log_cap trim

How a `lifecycle = "tmp"` schedule tag writes its audit log to a
tmp:// document, and how the `log_cap` policy trims old fired
entries when the chunk grows past its cap. Identical trim logic
applies to `lifecycle = "disk"` against the disk log file.

## Fire path → tmp:// audit append + trim

```
1. Scheduled fire of a lifecycle="tmp" event
1.1. EventScheduler.fire             → pops head event, publishes through PubSub. (R806)
1.2. EventScheduler.fire              → reads Config.Lifecycle(event.Tag) → "tmp". (R2822, R2824)
1.3. EventScheduler.fire              → chime override (if chime tag, replaces event.Value with NOW). (R2778)
1.4. EventScheduler.fire              → reads the existing audit chunk:
     dest path = tmp://schedule/TAG/SOURCE-ENCODED
     calls db.ReadTmpFile(dest) → content or NotFound.
1.5. EventScheduler.fire              → parses chunk into LogChunk (SpecMarkers + Fired).

2. Append-with-trim
2.1. EventScheduler.appendFiredEntry  → cap = Config.LogCap(event.Tag) (default 1000). (R2827)
2.2. EventScheduler.appendFiredEntry  → if len(chunk.Fired) + 1 > cap:
                                          dropCount = (len(chunk.Fired) + 1) - cap/2
                                          chunk.Fired = chunk.Fired[dropCount:]    ← keep newer half
                                          // SpecMarkers untouched. (R2827, R2829)
2.3. EventScheduler.appendFiredEntry  → chunk.Fired = append(chunk.Fired, NOW formatted).

3. Write back through the centralized tmp:// publish path
3.1. EventScheduler.writeAuditChunk   → serializes the chunk to markdown.
3.2. EventScheduler.writeAuditChunk   → calls db.UpdateTmpFile(dest, "markdown", content). (R2824, R2281)
3.3. db.UpdateTmpFile                  → routes through the write actor, replaces tmp:: overlay entry, indexes the new content.
3.4. db.UpdateTmpFile                  → publishes the resulting tag-changes to PubSub (existing R2281 path).

4. Re-enqueue the next occurrence (recurring tags only)
4.1. EventScheduler.fire              → if event.Recurring != "":
                                          next = ComputeNext(event.Recurring, NOW, …)
                                          Add(ScheduledEvent{Recurring: event.Recurring, …}). (R807, R2812, R2820)
4.2. EventScheduler.fire              → resetTimer().
```

## Disk variant

Identical structure with two substitutions:

- 1.4 / 1.5: read disk file at `~/.ark/schedule/HASH.md` via
  `ReadLogFile` (no tmp:: overlay).
- 3.2 / 3.3: write disk file via `WriteLogFile`; no tmp:// publish
  step. PubSub events for disk audit appends are not part of the
  contract (the source-file change that triggered the spec marker
  already published).

Trim logic in step 2 is identical regardless of destination — same
`log_cap` semantics, same older-half-drop, same spec-marker
preservation. (R2828)

## What trim preserves

- `@ark-event:`, `@ark-event-source:` — chunk identity.
- `@ark-event-spec-initial:` — always preserved (era anchor for the
  oldest fires that survive trim).
- `@ark-event-spec-changed:` markers — all preserved. Spec history
  is bounded by the count of historical changes, not by fire
  cadence, so it doesn't need trimming.
- `@ark-event-start:` / `@ark-event-end:` bounds — preserved
  (mirrored from current source spec).

## What trim drops

- The older half of `@ark-event-fired:` entries when the chunk's
  total fired count would exceed `log_cap` after the new append.
  Older = earlier in document order.

## Edge cases

- `log_cap = 0` is treated as `log_cap = 1` (always keep the most
  recent fire); a value of `0` from a config that wanted "no fires"
  should use `lifecycle = "none"` instead.
- If the chunk has fewer fired entries than `log_cap/2`, no trim
  runs even when appending pushes over the cap — this should be
  unreachable given normal arithmetic, but the implementation
  guards against negative drop counts.
- The trim runs synchronously in the fire path; for a 1000-entry
  cap that's a single slice operation, no I/O penalty.
