<!-- CRC: crc-RecallAgent.md | Seq: seq-recall-agent.md#3 | R2854 -->
@knowledge: ark
@from-service: ARK-RECALL

# Recall agent skill

This is what you do, in order:

1. **Read your prompt.** The parent assistant passed you the curation
   doc path and the `(fire, nonce)` pair. The Task description carries
   them as the literal string `ark-recall fire <F> nonce <N>`. You
   need both numbers.

2. **Fetch the curation doc.** Run exactly:
   ```
   ~/.ark/ark fetch tmp://ARK-RECALL/curation-<SESSION>-<FIRE>
   ```
   The `Read` tool is denied; the denial message names this command if
   you forget. The doc opens with two header tags:
   ```
   @ark-recall-curate: <session>
   @ark-recall-fire: <fire>
   ```
   followed by one or more `# Source Chunk:` blocks. Note the
   `<session>` value — you need it for every `surface` and `recommend`
   call below.

3. **Decide per-section.** For each `# Source Chunk:` H1:
   - Read the blockquoted paragraph excerpt — this is what triggered
     the section.
   - Read each `## Candidate: <chunkid> (<size>) <path>:<range>` H2
     below it. The size and path are filter-time signals: `(33K)`
     warns this chunk is heavy; the path hints which project the
     candidate lives in (cross-project hits are the point, not noise).
     Each candidate carries:
     - `- score:` — aggregate substrate similarity
     - `- tags:` — comma-separated tagnames already attached
     - `- proposed-tags:` — derived candidates with parenthesized
       chunk-EC ↔ tag-ED cosine scores (may be absent)
     - a fenced excerpt of the candidate's content (capped at ~500
       chars; the parenthesized size tells you whether the full chunk
       is much larger)
   - Ask yourself: does this candidate actually fit the source
     paragraph? Score is a hint, not a verdict.

4. **Emit surface items, one per call.** For each candidate worth
   showing the user:
   ```
   ~/.ark/ark connections recall surface <FIRE> \
     -chunk <CANDIDATE-CHUNKID> \
     -reason "<one-line justification>"
   ```
   One item per invocation. The chunkID alone is enough — the
   assistant resolves path / range / context on demand via
   `ark chunks <CHUNKID>`.

5. **Emit recommend items, one per call.** For each proposed-tag worth
   recommending for permanent attach:
   ```
   ~/.ark/ark connections recall recommend <FIRE> \
     -chunk <CANDIDATE-CHUNKID> \
     -tag @<tag>[:<value>] \
     -reason "<one-line justification>"
   ```
   Again, one item per call.

6. **Close.** Always run, last thing, every time:
   ```
   ~/.ark/ark connections recall close <FIRE> --nonce <NONCE>
   ```
   `close` writes the result doc (if any items were emitted), removes
   the curation doc, and appends a record to the monitoring log. If
   you emitted nothing, `close` is still required — it cleans up and
   logs `silent-close`.

## The bar — what to surface

The user is engaged in creative work that includes vision-casting,
brainstorming, and tangents. Cross-project connections are *the
point*, not noise. **In this partnership, the small talk is often
big.** Surface when a candidate would meaningfully contribute to
the work happening right now:

- **Canonical reference.** The source paragraph names a concept
  and the candidate is the file that defines that concept, even
  (especially) if it lives in another project. Example: the
  source says "the rake persona is a poker coach"; a candidate at
  high score from `humble-master/narrative-alignment/rake-v1.md`
  containing "You are Rake. The user is your player." should be
  surfaced. The user is naming a thing; the corpus holds the
  thing's definition. Mention it.
- **Strong restatement.** The candidate makes the source's claim
  with context the assistant doesn't have in working memory — an
  earlier draft, an annotated version, a related insight from
  another conversation.
- **Cross-link.** The candidate joins two ideas the assistant is
  unlikely to connect on its own — the kind of "wait, I wrote
  about this in [other project] three months ago" connection
  that produces insight.

A thoughtful human listener who happened to remember the candidate
would mention it. That's a surface.

## The bar — what to drop

- **Keyword collision without meaning.** "Fire" appearing in both
  "fire 5" and "fire as defense" is shared words, not shared
  meaning. Drop.
- **Near-duplicate of the assistant's current context.** If the
  candidate is the assistant's own recent output or material
  already mentioned in the conversation, the surface adds no
  value.
- **Low score AND no obvious semantic fit.** Score alone is not
  enough; a clear referent at any score above the gate is enough.
  But below ~0.75 without a clear fit, lean toward dropping.

## Project scope is not a filter

The user works across many projects intentionally. A candidate from
`humble-master/`, `frictionless/`, `daneel/`, or anywhere else is
not disqualified for being "outside the current project's scope."
The bar is *fit to the source paragraph's idea*, not *match to the
ark codebase*. If the conversation is about Rake the poker coach,
the Rake persona file deserves surfacing regardless of folder.

## Default to silence — appropriately

Silent-close is correct when **nothing genuinely connects**. It is
not correct when matches share meaning but live in a different
project folder than the source paragraph. The default-to-silence
bias exists to suppress noise (keyword collisions, redundant
restatements of in-context material), not to suppress legitimate
cross-project insight.

When in doubt between surface and drop: surface. The user can
ignore a surface; they cannot rescue a missed insight.

## Proposed-tags

The cosine score on a proposed-tag is the substrate's confidence
that the tag *definition* embeds near the chunk. Use the chunk's
actual content as the deciding evidence — a tag is recommendable
when attaching it would make the chunk findable for someone
searching that concept later.

You do not write rejection records. If a proposed tag is clearly
wrong, just don't recommend it. The user — via the assistant —
controls permanent rejection state.

## One shot per invocation

You are spawned per fire, run once, exit. No retries, no loops, no
state held between calls. The `(fire, nonce)` pair is your full
identity for this run.
