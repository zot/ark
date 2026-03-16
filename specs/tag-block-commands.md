# Tag Block Commands

`set-tags`, `get-tags`, and `check` are generic tag block operations
that currently live under `ark message`. They operate on any file
with a tag block — not just messages. Moving them to `ark tag` makes
them discoverable for general use and keeps `ark message` focused on
messaging lifecycle.

## ark tag set

```
ark tag set FILE TAG VALUE [TAG VALUE ...]
```

Same behavior as `ark message set-tags`: updates or adds tags in the
tag block. Arguments are pairs. Tag name without `@` or `:`. Existing
tags are updated in place, new tags are appended. Body is untouched.

## ark tag get

```
ark tag get FILE [TAG ...]
```

Same behavior as `ark message get-tags`: reads tags from the tag block.
Outputs `tag\tvalue` per line. Specific tags: outputs only those.
No tags: outputs all. Exits 1 if a requested tag is not found.

## ark tag check

```
ark tag check FILE [HEADING ...]
```

Generic tag block validation. If the file is valid, exits 0 with no
output. If invalid, outputs problems to stderr, exits 1.

When heading arguments are provided, also flags headings in the body
that are not in the allowed list. This lets `ark message check` pass
the expected message headings and catch stray sections.

Without heading arguments, only structural validation runs (malformed
tags, blank lines in tag block, missing separator, tag-like patterns
in body).

## ark message check

Becomes a thin wrapper:

```
ark message check FILE
```

Calls `ark tag check FILE` with the standard message heading list
hardcoded. This is a terser crank-handle for agents — they don't need
to know which headings are valid, just run `ark message check`.

## Migration

`ark message set-tags` and `ark message get-tags` become aliases for
`ark tag set` and `ark tag get`. They continue to work but the help
text points to `ark tag`. No breakage for existing scripts or agent
prompts.

## References to update

After this lands, update references in:
- ark skill (`.claude/skills/ark/SKILL.md`)
- hermes agent docs
- Franklin agent docs
- ARK-MESSAGING.md
