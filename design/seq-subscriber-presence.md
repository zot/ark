# Subscriber-presence gates

How the recall watcher and `ark connections recall close` query
`PubSub.SubscriberCount` before writing the curation doc / result
doc, and what happens when no subscriber is present.

Two parallel diagrams — one for each gate point. Both consume the
same `SubscriberCount` query and both record the same
`outcome: "no-subscriber"` string in `recall.jsonl`.

## Watcher pre-curation gate

```
1. Watcher fire
   1.1. RecallWatcher.fire(sessionID)        → snapshot pendingChunks, clear slice, allocate fireCounter
   1.2. RecallWatcher.fire                   → pubsub.SubscriberCount("ark-secretary-work", sessionID)
   1.3. RecallWatcher.fire                   → branch on count (>0 builds sections via existing seq-recall-watcher.md; 0 falls through to 1.4)
   1.4. RecallWatcher.fire                   → recallMonitor.Append({fire, session, nonce:0, in_tokens:0, out_tokens:0, outcome:"no-subscriber", ...}) (only when count==0)
   1.5. recallMonitor                        → write-actor append to ~/.ark/monitoring/recall.jsonl
   1.6. RecallWatcher.fire                   → return (pendingChunks already cleared; next OnAppend starts fresh)
```

## `close` pre-result-write gate

```
2. Close (CLI verb from recall agent)
   2.1. RecallAgentBuilder.Close(fire, nonce, preserveCuration)   → check `results[fire]` for items
   2.2. RecallAgentBuilder.Close                                    → if items present: pubsub.SubscriberCount("ark-recall-result", session); else skip directly to cleanup with outcome:="silent-close"
   2.3. RecallAgentBuilder.Close                                    → on items: count > 0 → write tmp://ARK-RECALL/result-<session>-<fire>, outcome:="result-emitted"; count == 0 → skip result-doc write, outcome:="no-subscriber"
   2.4. RecallAgentBuilder.Close                                    → remove curation doc + orphan-curation sweep (regardless of outcome, unless preserveCuration)
   2.5. RecallAgentBuilder.Close                                    → discoverSubagentJSONL(nonce); sumSubagentTokens; recallMonitor.Append(outcome=...)
   2.6. RecallAgentBuilder.Close                                    → drop results[fire]; return
```

The cleanup side of `close` runs regardless of subscriber
presence (R2807) — only the result-doc write itself is gated.
The monitoring log entry always appends; `outcome` carries the
gate decision.

## SubscriberCount semantics

```
3. PubSub.SubscriberCount(tagName, tagValue)
   3.1. PubSub                       → acquire read lock
   3.2. PubSub                       → iterate subs map (sessionID → []TagSub)
   3.3. PubSub                       → for each TagSub: if Predicate.Match(TagValue{Name: tagName, Value: tagValue}) → count++
   3.4. PubSub                       → ignore Sub.FilterFiles / ExcludeFiles (R2803)
   3.5. PubSub                       → release lock; return count
```

TTL reaping happens on the server's existing ticker; the query
sees only registered, non-reaped subscriptions (R2804).
