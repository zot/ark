# Sequence: address resolution and reverse expansion

Two numbered diagrams. Diagram 1 is the forward direction — a caller's
address becomes a canonical path. Diagram 2 is the reverse — a canonical
path becomes every address naming it.

Neither caches a forward resolution. The table exists for diagram 2, which
the filesystem cannot answer.

## 1. Forward: an address becomes a canonical path

```
1.    CLI/handler receives a path or pattern from outside ark
1.1.    is it a pattern?  (Matcher.IsGlob)
1.1.1.    yes → split at the longest wildcard-free leading portion (R3362)
1.1.2.    no  → the whole string is the concrete part
1.2.    resolve the concrete part component-wise (R3360)
1.2.1.    walk components; any one may be a link, not only the last (R3372)
1.2.2.    tilde expansion alone is NOT resolution — it yields a lexically
          absolute path that may still traverse links (R3372)
1.2.3.    a component that cannot be resolved → keep the literal path (R3361)
1.3.    re-attach the pattern remainder unchanged, if there was one
1.4.    compare against stored canonical paths — canonical-to-canonical,
        so nothing resolves per result row
1.5.    report in the namespace the caller asked in (R3364), unless the
        link-view selector says otherwise (R3375, R3379)
```

## 2. Reverse: a canonical path becomes every address

```
2.    caller holds a canonical path P and wants its addresses
2.1.    walk P's ancestors, longest first, against byTarget
2.2.    for each tracked link L whose resolved target prefixes P:
2.2.1.    substitute L's own path for that prefix → one alias address
2.2.2.    repeat against the result, since a link may point into a tree
          another link also targets (R3374) — iterate to a fixed point
2.2.3.    guard the iteration; the layout permits a cycle and ark already
          contains a link pointing at the farm that contains it (R3353)
2.3.    the canonical path is itself one of the addresses
2.4.    return the set
```

Diagram 2 is what `--links` exposes (R3378), what a listing uses to show
alias rows (R3364, R3380), and what ExtMap's anchor lookup consults so a
target written through a link still matches a file indexed canonically
(R3367). That last use is why the anchor key stays *unresolved*: the key is
the text the user wrote and never goes stale, while the volatile part is
recomputed here on every lookup.
