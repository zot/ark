# Sequence: retrieving `@ext` tags, sewn

An operation that retrieves `@ext` tags may have to *mutate* — a link whose
resolution moved invalidates X records and the ExtMap maps built from it. A
read path cannot do that work itself without touching actor-owned state off
the actor, so the operation is **sewn**: each step runs where its state
lives, and the result travels forward as a posted continuation. Nothing is
held at any instant.

Soundness depends on R3344–R3350. Posting the continuation behind the
mutation is Actor Queue Ordering, and before the closure actor's delivery
guarantee was restored the continuation could overtake the mutation it was
sequenced after.

## 1. A bulk retrieval (`/content`, and anything else asking for ext tags)

```
1.    handler needs the @ext tags for a set of chunks
1.1.    post to the actor owning ext state: update-and-retrieve for this fileid
1.2.      gather the link associations this fileid depends on (R3387)
1.2.1.      single-hop: trusted as-is — invalidated precisely, by an event
            naming the link's own path (R3384)
1.2.2.      internal multi-hop: recompute if dirty, then clear dirty (R3384)
1.2.3.      external multi-hop: recompute unconditionally, never cached (R3384)
1.3.      re-derive each recomputed association's TIER, not only its fileid
          (R3385) — a link changes shape, and a stale tier keeps a cheap
          verification schedule for a shape the link no longer has
1.4.      compare each result against its recorded premise (target, fileid) (R3382)
1.4.1.      fileid unchanged → no X-record work, no map updates, nothing written
1.4.2.      fileid changed → post the re-resolution to the write actor, reusing
            applyReresolve so X records and every ExtMap map move together (R3388)
1.4.3.      cannot resolve → the routing is unresolved, never last-known-good;
            count it in ext-unresolved (R3386, R3371)
1.5.    post the retrieved tags forward as a continuation
1.6.  handler resumes with the tags in hand, holding nothing
```

Step 1.4.1 is the common path and does no I/O and no writes: the recorded
premise is what turns verification into a comparison instead of a
re-derivation.

Step 1.4.2 is a write arising from a read. It is posted, never awaited —
the response is built from what step 1.2 just resolved, so the answer is
correct immediately while durable state catches up behind it.

**Do not "optimize" 1.4.2 away** on the reasoning that the value will be
recomputed next time. That holds only for the external multi-hop tier,
which never caches; for the other two the stored value *is* the cached
answer, and skipping the write leaves them wrong until something else
happens to recompute them.
