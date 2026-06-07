# Migration: ark CLI → `urfave/cli` v3 command tree (self-documenting help)

Replace ark's hand-rolled CLI dispatch and its scattered, hand-maintained
help strings with a **`urfave/cli` v3** command tree, so help is generated
**from the command tree** — one source per command — and can no longer
drift from the code. This is the structural enforcement of principles.md
**"the documentation tells the truth"** for the CLI: drift becomes
impossible by construction instead of being held off by vigilance.

**Language/environment:** Go. Touches `cmd/ark/main.go` (the dispatch
`switch`, `usage()`, the per-command `--help` printers) and the `cmd*`
handler bodies it routes to. Adds one dependency,
`github.com/urfave/cli/v3` (**MIT** — non-viral, safe for ark's release
posture). The import alias `cli` is already taken by
`github.com/zot/ui-engine/cli` (it provides `ExpandVerbosityFlags`), so
urfave is imported under a distinct alias.

See also: [cli-commands.md](../cli-commands.md) (the CLI inventory summary
spec — the behavioral contract this migration must preserve),
[main.md](../main.md), and the master plan
[.scratch/CLI-REVAMP-20260607.md](../../.scratch/CLI-REVAMP-20260607.md)
(+ spike evidence `.scratch/CLI-HELP-SPIKE-20260607.md`).

## Problem (state A)

Help drift is **structural**. `cli-commands.md` already names it: a CLI
shape change has to be landed in **four** places, none of which catches
the others —

1. the dispatch `switch` in `main()` (what makes the command work);
2. the top-level `usage()` command list (hand-written, *not* derived from
   the switch);
3. the command's own `--help` printer (`printConnectionsHelp`, `uiUsage`,
   `printConfigHelp`, the `luhmann` / `schedule` usage blocks, …) — each an
   unanchored string literal `minispec validate` cannot see;
4. `cli-commands.md` itself.

Because the parent help and each child's `flag.FlagSet.Usage` are
**separate hand-maintained strings, free to diverge**, they did: `recall
surface`/`recommend` help said `-chunk` while the code took `-loc` (fixed
by hand in `419fb3a`). A 2026-05-30 audit found nine such drifts at once.
The hand-written `usage()` and per-command printers are a standing tax on
every CLI change and a standing risk on every one that's missed.

Asking the question the system is supposed to answer — *does the
documentation tell the truth?* — the honest answer in state A is "only as
long as four hand-edited surfaces happen to agree."

## State B — help generated from the command tree

ark builds a `urfave/cli` v3 `*cli.Command` tree: one node per command and
subcommand, each declaring its own `Name`, `Usage` (one-line synopsis), and
`Flags` (each flag carrying its own `Usage`). `cmd.Run` parses args, routes
to the matching node's `Action`, and **generates `--help` from the tree
itself**. There is exactly one source of help text per command, and it sits
on the same declaration that makes the command work. The four surfaces
collapse to one.

The command *bodies* are unchanged — urfave only **routes**. Every existing
handler (`cmdStatus`, `cmdConnections`, …) keeps its logic, its server
detection, its `tmp://` handling, its exit codes; it is reached through an
`Action` callback instead of a `switch` case, reading flag values from the
command context instead of a `flag.FlagSet`.

## Help-system behavior (the new contract)

- Every command and subcommand **documents itself**: its `--help` output is
  generated from its own `Name`, one-line `Usage`, and its flags' `Usage`
  strings — no separate hand-maintained help printer.
- `--help` / `-h` works at **every node** of the tree natively: top-level
  (`ark --help`), every group (`ark connections --help`), and every leaf
  (`ark connections recall close --help`), each showing the full command
  path. (The state-A `config --help` empty-usage bug disappears for free.)
- A **parent's help auto-lists its children** with their one-line synopses;
  the parent does not restate child help. Help composes down the tree from
  single per-node sources.
- A command's flags appear in its help **derived from the flag
  declarations** — adding or renaming a flag updates its help with no second
  edit, so a flag can never be missing from or stale in its own help.
- **DSL commands document via a single hand-written `Description`.** A
  command that custom-parses its arguments (the search filter stack) cannot
  derive flag help from declarations; it carries one hand-written
  `Description` blurb instead. This is still **single-source** (one place,
  drift-proof) — just authored rather than auto-derived.

## Behavior preserved (the contract `cli-commands.md` pins)

Everything `cli-commands.md` documents must work **identically** after the
migration. Specifically:

- **Single-dash-long flags.** `-scores` ≡ `--scores`, `-k` and `-with`,
  etc. urfave/cli is built on stdlib `flag`, which accepts one or two
  dashes; the whole surface, every skill, every script, and every
  `cli-commands.md` example depends on this and keeps working. (This is the
  reason GNU-style libraries — kong, cobra — were rejected.)
- **Global flags parsed before dispatch.** `--dir PATH` (and `--dir=PATH`)
  selects the database directory (default `~/.ark/`); `-v` increases
  verbosity, with `-vvvv` expanding to four levels. They are recognized
  ahead of the subcommand, as today, via urfave global flags.
- **The search filter-stack DSL is preserved verbatim.** Its order-sensitive,
  sticky `-with`/`-without` polarity and its repeated heterogeneous
  `(polarity, mode, query)` tuples are handed to the existing
  `parseFilterStack` parser unchanged — the search node takes **all** its
  raw args (urfave `SkipFlagParsing`) rather than letting urfave parse them.
- **`search -parse` still disambiguates and exits** without searching,
  under the custom-parse path.
- **Exit codes are preserved.** `1` = error (matching `fatal()`); `0` =
  success; and the meaningful non-`1` codes stay — e.g. `connections recall
  next` exits `2` to signal "done/exit" vs `0` "handed you a doc", and the
  removed connections flags (`--wait`/`--fetch`/`--result`/`--error`)
  print a one-line hint and exit `2`.
- **Error format is preserved.** A failing command prints `error: <msg>` to
  stderr (the `fatal()` shape), not urfave's default rendering.
- **Command bodies are untouched.** `tmp://` path handling, server
  detection (`serverClient` / `withDB` / `requireServer`), server-first
  proxy-or-cold-start dispatch, and all per-command output formats live in
  the handler bodies and are reached unchanged through `Action`.
- **Aliases and shims are preserved.** `message set-tags` ≈ `tag set`,
  `message get-tags` ≈ `tag get`, `message check` ≈ `tag check`; the legacy
  connections flag shims above. These remain reachable with identical
  behavior.

## Staged transition (un-migrated commands keep working)

The port is large (~40 top-level commands + ~60 subcommands) and proceeds
group by group. During the transition the root command carries a
**legacy catch-all**: any command not yet migrated to a `*cli.Command`
node falls through to the existing hand-rolled `switch` dispatch, so the CLI
keeps working end-to-end at every step. The pilot is `connections` (the
deepest tree, three levels — `connections recall close` — and where the
drift lived); the search-DSL siblings migrate last. When the last group
lands, the catch-all and the legacy `switch` are removed.

## The four-surface drift retires

When the migration completes, surfaces (2) and (3) above no longer exist as
hand-maintained text: `usage()` and the per-command `--help` printers are
generated from the tree. `cli-commands.md` stays as the CLI **inventory
summary spec** and the behavioral contract — it is still maintained by hand
as a mirror, but it no longer shadows two other hand-written surfaces. The
CLAUDE.md cross-cutting note that lists "update `usage()` and every `--help`
printer" is rewritten to reflect that the principle is now structurally
enforced for the binary's own help, leaving `cli-commands.md` as the one
hand-kept mirror.

## What this deliberately does not do

- **No command-body rewrites.** Logic, output formats, server interaction,
  and exit codes inside the `cmd*` handlers are out of scope; the migration
  changes only how a command is *reached* and how its help is *produced*.
- **No flag-surface changes.** No flag is added, removed, renamed, or
  switched to GNU-only `--long`. The single-dash-long surface is frozen.
- **Help *format* may change, cosmetically.** urfave's generated layout
  (`NAME` / `USAGE` / `COMMANDS` / `OPTIONS`) differs from the old
  hand-rolled look. Matching the old format exactly via a custom urfave
  help *template* is optional polish, not a requirement; the standard format
  is acceptable. This is cosmetic, not behavioral — `cli-commands.md`
  documents conventions, not pixel-exact help text.
- **DSL commands stay custom-parsed.** `search` (and any sibling sharing the
  filter-stack flags) keeps parsing all of its own args; its help is the
  single hand-written `Description`, not auto-derived. The other ~95% of
  commands get full auto-help.
