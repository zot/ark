# Minispec Configuration Reference

## The two config files

Configuration lives at two scopes, matching the project's two roots.

| file | scope | holds |
|---|---|---|
| `<repo root>/.minispec/config.yaml` | the repository | `track`, plus any settings shared by every design root in it |
| `<design root>/.minispec.yaml` | one design root | only what differs from the repository config |
| `<repo root>/.minispec.yaml` | — | **an error, with no exception** |

The **design root** is the directory containing `design/`. The **repository root** is
the top of the version-controlled tree, which owns `.claude/`, `carves/` and the
trajectory files. They are often the same directory; a repository may hold several
design roots under one repository root.

**A top-level `.minispec.yaml` is always an error**, because its inheritance would be
incoherent — it would have to inherit from `.minispec/config.yaml`, a file inside its
own directory. Where the repository root *is* a design root, `.minispec/config.yaml`
is that design root's configuration too; there is no second file. A symlink from the
old path to the new one is still the forbidden shape, not a supported alias.

### Inheritance

Settings resolve in three layers, each applied over the one before: built-in defaults,
then the repository config, then the design root's own file.

- **Scalars replace** — `design_dir`, `src_dir`, `track`.
- **Maps merge per key** — a design root adding one `comment_patterns` entry keeps
  every other entry the repository set.
- **Lists merge as a union between configuration layers** — repository entries kept,
  design-root additions appended, duplicates dropped, repository order preserved. The
  **first configuration layer to set a list replaces the built-in defaults** rather
  than adding to them, so a shipped list can still be narrowed.

The rule each layer follows is the same one: **state only what you add or change,
never what you keep.** A design root whose settings match the repository needs no file
at all.

A design root can *add* to an inherited list but cannot *remove* from one. The fix is
to drop the setting from the repository config and state it in each design root that
needs it — moving the decision down a level rather than overriding it from below.

Run `minispec query config` to see every effective setting with the file it came from.

## `track` — the repository-scoped setting

`track` lives in `.minispec/config.yaml` and **only** there. A design root that sets it
is an error: it describes the repository, and a repository may hold several design
roots, so one of them cannot answer for the whole.

| value | meaning |
|---|---|
| `none` | the project is not under version control |
| `private-trajectory` | git; the trajectory files are ignored |
| `all` | git; the trajectory files are tracked with everything else |

The paths it governs are `PENDING.md`, `CURRENT.md` and `DONE.md` at the repository
root, plus `.carves/` whenever it exists. `.carves/` must be ignored under **both**
git-managed values — a directory whose whole purpose is privacy does not become public
because the queue did. Public `carves/` is never ignored.

**`track` is verified on every run, not merely stored.** The tool checks it against two
facts — whether the tree is git-managed, and the actual ignore state of those paths —
and refuses when they disagree. It duplicates what `.gitignore` says on purpose: a copy
re-checked every run cannot drift undetected, which is the whole reason to keep it.

### Creating and repairing it

`minispec init` is the **sole creator** of `.minispec/config.yaml`, and one
`--track-<style>` flag is **mandatory** — the value cannot be inferred, and a default
would make the choice for someone without their noticing.

```
minispec init --track-none
minispec init --track-private-trajectory
minispec init --track-all
```

`init` also writes the ignore lines the value implies: `.minispec/backup` under any
git-managed value, and the trajectory filenames under `private-trajectory`.

To change the value later, or to fix a mismatch:

```
minispec init --track-<style> --repair
```

`--repair` is **symmetric**: it sets `track` *and* brings `.gitignore` into agreement,
adding lines the new value requires and removing lines it forbids. Those are opposite
outcomes, and the flag value is how you choose between them — **confirm the value with
the user before running it.**

Plain `init` refuses when `.minispec/` exists; `--repair` refuses when it does not.
The precondition is inverted rather than supplemented, so neither has to guess.

### With no configuration

Only `init`, `--version`, `help` and `check-version` run without a
`.minispec/config.yaml`. Everything else refuses with a message telling the agent to
check whether the directory is a code project, ask the user whether they want one, and
then **report and wait** — running `init` is the user's decision, never the agent's.

Every project that predates `track` meets that refusal once. That is how it gets asked.

### If the config is malformed

`--repair` sets a value; it cannot undo arbitrary damage, so it refuses a file that
will not parse. **That refusal is the one case where an agent is authorised to edit the
configuration by hand.** Back the file up first, and skip the backup when it would be
byte-identical to one already there.

## Design-root config file

**Path:** `.minispec.yaml` in a design root (next to the `design/` directory).
**Format:** YAML. All fields are optional — omitted fields inherit, per the rules above.

## Schema

```yaml
design_dir: design              # Path to design directory (relative to project root)
src_dir: src                    # Path to source directory (relative to project root)
code_extensions:                # File extensions to scan for traceability comments
  - .go
  - .ts
  - .pas
comment_patterns:               # Comment-prefix regex per file extension
  .go: "//\\s*"
  .pas: "\\{\\s*"
comment_closers:                # Closing delimiter for block-comment languages
  .pas: " }"
  .css: " */"
  .md: " -->"
  .html: " -->"
```

## Comment Patterns

Each entry maps a file extension to a **regex matching the comment prefix only**. The tool appends `CRC:\s*...` to build the full traceability regex.

**Built-in defaults:**

| Extension | Pattern | Style |
|-----------|---------|-------|
| `.go` | `//\s*` | C-style single-line |
| `.js` | `//\s*` | C-style single-line |
| `.ts` | `//\s*` | C-style single-line |
| `.c` | `//\s*` | C-style single-line |
| `.h` | `//\s*` | C-style single-line |
| `.cpp` | `//\s*` | C-style single-line |
| `.py` | `#\s*` | Hash |
| `.lua` | `--\s*` | Double-dash |
| `.sh` | `#\s*` | Hash |
| `.bash` | `#\s*` | Hash |
| `.md` | `<!--\s*` | HTML comment |
| `.html` | `<!--\s*` | HTML comment |
| `.css` | `/\*\s*` | Block comment |

**Adding a new language** — put an entry in `comment_patterns`. User entries override defaults for matching extensions; unmatched extensions keep built-in patterns.

## Comment Closers

**CRITICAL for block-comment languages.** Languages like Pascal (`{...}`), CSS (`/*...*/`), and HTML (`<!--...-->`) require a closing delimiter. An unclosed comment silently swallows all subsequent code until the next accidental closer — this is catastrophic and extremely hard to diagnose.

`comment_closers` maps file extensions to the string that **must** be appended when writing traceability comments. The tool's parser already strips these terminators when reading; this field tells the AI what to emit when writing.

**Built-in languages with line-terminating comments** (Go, Python, JS, Lua, shell) do not need closers — the comment ends at the newline.

**Languages that REQUIRE closers:**

| Extension | Closer | Comment form |
|-----------|--------|--------------|
| `.pas` | ` }` | `{ CRC: ... }` |
| `.dpr` | ` }` | `{ CRC: ... }` |
| `.css` | ` */` | `/* CRC: ... */` |
| `.md` | ` -->` | `<!-- CRC: ... -->` |
| `.html` | ` -->` | `<!-- CRC: ... -->` |

When writing traceability comments, **always check `comment_closers`** for the file extension. If a closer exists, append it. If it doesn't, the comment is line-terminated and needs no closer.

### Example: Delphi/Pascal

Config:
```yaml
comment_patterns:
  .pas: "\\{\\s*"
  .dpr: "\\{\\s*"
comment_closers:
  .pas: " }"
  .dpr: " }"
```

Correct:
```pascal
{ CRC: crc-Parser.md | Seq: seq-compile.md }
procedure Parse;
```

Wrong — **will comment out everything below:**
```pascal
{ CRC: crc-Parser.md | Seq: seq-compile.md
procedure Parse;
```

## Precedence

1. **CLI flags** (`--design-dir`, `--src-dir`) override everything
2. **`<design root>/.minispec.yaml`** — the design root's own settings
3. **`<repo root>/.minispec/config.yaml`** — settings shared across the repository
4. **Built-in defaults** apply when no layer sets a value

Layers 2 and 3 merge rather than simply overriding: scalars replace, maps merge per
key, lists union. See *Inheritance* above.

## Verifying

After creating or updating either config file, run:

```bash
~/.claude/bin/minispec query config             # every setting, with the file it came from
~/.claude/bin/minispec query comment-patterns   # patterns and closers per extension
~/.claude/bin/minispec query project            # the two roots, and the resolved paths
```

`query config` is the one to reach for when a value is not what you expected — the
effective configuration is not what any single file says, so it reports each setting's
origin rather than leaving you to read two files and know the precedence by heart.
