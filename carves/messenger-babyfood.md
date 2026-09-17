# Messenger tools speak baby food

`ark message inbox` prints one TSV row per file. The messenger agent that reads
it is a Haiku, and on 2026-09-16 it missed two completed replies from mini-spec
that were sitting in the listing. This carve moves the messenger-facing outputs
to the same agent-readable markdown that `ark search -tags` already uses
([specs/tags-baby-food.md](../specs/tags-baby-food.md)), and keeps the TSV
behind a flag.

## Status

- [x] **Item 1 — `inbox` in baby food, `--compact` keeps the TSV.** **IMPLEMENTED — NOT COMMITTED (#128, 2026-09-16.)** R3404-R3409; `printInboxNode`/`printInboxCompact` in `cmd/ark/message_cli.go`, tests + 3 alarms in `test-InboxTree.md` (pulled, rang).
- [x] **Item 2 — say when a filter matched nothing and what it could have matched.** **IMPLEMENTED — NOT COMMITTED (2026-09-16.)** R3410; `printInboxEmptyHint`. Was cheap (one pass over the unfiltered entries), so folded in.
- [ ] **Item 3 — the other message verbs (`get-tags`, `check`, `--counts`).** **OPEN.**
- [x] **Item 4 — the readers follow: `hermes-messaging.md`, `ark-usage`, `cli-commands.md`.** **IMPLEMENTED — NOT COMMITTED (2026-09-16.)** All three plus `specs/messaging.md` describe the tree default, `--compact`, and the reading rule (a reply's status is on the nested line; `no response` is stated).

## How it surfaced

Bill asked whether mini-spec had answered microfts2's harvest-scope request.
The messenger ran the right command, `ark message inbox --project microfts2`,
and reported no response. Both replies existed, written that day. They were
in the listing, encoded like this:

```
2026-08-06  open  mini-spec  microfts2  ...harvest scope...  .../requests/minispec-impl-ref-harvest-scope.md  lag:microfts2:completed
```

The response is `completed`, and the inbox hides completed and denied
messages by default, so it gets no row of its own. Its existence, its status, and the fact that
microfts2 has not handled it are all folded into the seventh column of the
*request's* row, while the second column still says `open`. A Haiku read
column two and stopped. So did Daneel, on the first pass.

The second half of the miss: the messenger narrowed with `--from minispec`.
The project is `mini-spec`. The filter matched nothing, and nothing looked
like confirmation.

## Why the row is the wrong unit

The code already knows the pair. `messageInboxAction` builds `byID` and pairs
every request with its response before filtering, and it uses the pair for
`--unmatched`, for the display date, and for lag. Then it flattens the pair
back into independent rows and hands the reader the job of re-pairing them
from a suffix. The reader is the least capable party in the chain.

`-tags` solved the same problem for search: a bullet tree whose structure
*is* the relationship, so nothing has to be decoded. The inbox has a natural
tree too: request, then its response nested beneath, then each side's
bookmark stated as a sentence.

## Shape (thinking, not decided)

```
- open  microfts2 → mini-spec  impl-ref harvest scope  (2026-08-06)
  /home/deck/work/microfts2/requests/minispec-impl-ref-harvest-scope.md
  - response: completed  mini-spec → microfts2  (2026-09-16)
    /home/deck/work/mini-spec/requests/RESP-minispec-impl-ref-harvest-scope.md
    - microfts2 has not handled this response yet
- open  ui-engine → ark  Svc loses the closure actor's delivery-order guarantee  (2026-08-01)
  /home/deck/work/ark/requests/svc-ordering.md
  - no response
```

Things the shape has to get right:

- **Status words on the line, not in a suffix.** "not handled yet" instead of
  `lag:microfts2:completed`. The lag encoding stays in `--compact`.
- **A response nests under its request even when the default completed-hide
  or a directional filter would drop it as a row.** That is the exact case
  that bit; the pairing already happens before filtering, so this is display
  only.
- **`no response` is stated.** Silence is what the messenger read as absence.
- **`--compact` is the current TSV, byte for byte.** Scripts and the dashboard
  keep working; the ark UI reads the HTTP JSON and is not touched either way.

## Item 2: an empty result should say what it did not match

`--from minispec` printed nothing. A near-miss hint costs one pass over the
unfiltered entries: "no messages match `--from minispec`; from-projects
present: ark, mini-spec, microfts2, ...". Cheap, and it converts a silent
miss into a correctable one. Belongs to the same change because it is the
same reader.

## Item 3: the smaller verbs

`get-tags` prints `tag<TAB>value`, which is fine for a Haiku reading two
columns. `--counts` prints `status<TAB>count`. `check` prints nothing on
success. Worth a look for consistency once Item 1 sets the style, but none
of them has caused a miss. Lowest priority; may end up as "compact is fine".

## Item 4: the readers

Three documents describe the TSV columns and the lag field:
`.claude/skills/ark/hermes-messaging.md` (what the messenger is primed with),
`.claude/skills/ark-usage/SKILL.md`, and `specs/cli-commands.md`. All three
change with Item 1, and the messenger's reference should carry the reading
rule for the new shape. CLI changes also touch the `message_cli.go` node
declaration; help text follows from the command tree.

## Decisions

- **DECIDED (Bill, 2026-09-16): baby food is the default; the TSV survives as
  `--compact`.** The agent is the primary reader of this command, and the
  default should serve the reader who cannot decode a suffix.
