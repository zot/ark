# Setup and update: one install verb, links not copies

Ark has four ways to get installed, and they disagree. `ark setup` extracts the
bundle and *copies* skills and agents into `~/.claude/`. `ark init` creates the
database and quietly runs `setup` on an unbootstrapped machine. `ark ui install`
runs `init --if-needed`, starts a server, and *symlinks* the same skills and
agents into the project's `.claude/`, and `ark install` is an alias for it. The
specs disagree too: `specs/main.md` makes `ark ui install` "the single entry
point," while `specs/features.md` says the user runs `ark setup` once.

The pieces belong together because every question here has the same answer: one
verb installs, one verb refreshes, and nothing is copied where a directory link
will do. The link half grew out of
[windows-support.md Item 3](windows-support.md#3), where copying and per-file
symlinks both failed on Windows. Once the links became directory links into
`~/.claude/`, the project-level install had nothing left to do, and the install
surface was worth redrawing on every platform, not just Windows.

## Status

- [x] ~~**Item 1 — `ark setup` is the only install verb.**~~ **LANDED (2026-10-04, #136.)** Idempotent on
      a marker, self-copies into `~/.ark`, creates the database, links into
      `~/.claude/`.
- [x] ~~**Item 2 — `ark setup --local`.**~~ **LANDED (2026-10-04, #136.)** Project-scoped links on an
      existing install.
- [x] ~~**Item 3 — retire the old copies and report duplicate names.**~~ **LANDED (2026-10-04, #136.)**
- [ ] **Item 4 — retire `ark init`, `ark install`, `ark ui install`.** **OPEN (not queued.)**
      Includes the docs sweep.
- [x] ~~**Item 5 — `ark update`, manifest-aware.**~~ **LANDED (2026-10-04, #136.)** Needs Item 1's
      manifest and the Windows exe-replacement dance.
- [ ] **Item 6 — audit `init`'s flags against `ark.toml`.** **OPEN (not queued.)** Not verified:
      Measure before Item 4 drops the verb.
- [ ] **Item 7 — bundle whole skill directories.** **OPEN (not queued.)** The Makefile ships
      `SKILL.md` alone, and the ui-* skills are built from a hand-kept duplicate
      (refreshed 2026-09-24, but still a second copy that can drift).
- [ ] **Item 8 — message to Frictionless.** **OPEN (not queued.)** Drafted, not sent (three
      unknowns listed under "Before sending"). Its skill
      install is hardcoded, per-file, copying, and project-scoped, and it
      creates symlinks; the Frictionless-side halves of Items 3, 7, and
      Windows Item 3.

Preconditions, no checkbox: the portable directory-link helper (symlink on Unix,
junction on Windows) is decided in windows-support Item 3 and shared with
[app-activation.md](done/app-activation.md). Items 1 and 2 consume it.

<a id="decisions"></a>
## Decisions

**DECIDED (Bill, 2026-09-24): `ark setup` is the one install verb.** `ark init`,
`ark install`, and `ark ui install` go, because extra verbs that overlap are more
confusing than one verb that does it all. `init`'s internals stay as a function,
since `rebuild` recreates the database through them (`init --no-setup` today).

**DECIDED (Bill, 2026-09-24): setup keys on a marker, not on `~/.ark` existing.**
The README install places the downloaded binary at `~/.ark/ark` *before* running
it, so `~/.ark` exists on every fresh install and cannot be the test. The marker
is something setup itself leaves; the database file is the natural one, since
"installed" means the index exists. (`isBootstrapped()` tests `~/.ark/html`
today.) With the marker present, the global part of setup does nothing.

**DECIDED (Bill, 2026-09-24): an absent or empty `~/.ark` is created, and the
running executable is copied into it.** So setup can run from wherever the user
downloaded it. On Windows the destination is `ark.exe`. When setup is already
running from `~/.ark`, the copy is skipped.

**DECIDED (Bill, 2026-09-24): skills and agents install by directory link only,
into `~/.claude/`.** `~/.claude/skills/ark`, `~/.claude/skills/ui`, and
`~/.claude/agents/ark`, each linked to its `~/.ark` directory. `~/.ark` is
already one install per user, so a per-project link only pointed every project
at the same directory, and a project definition still overrides a user one when
someone needs that.

**DECIDED (Bill, 2026-09-24): setup installs every bundled skill, the ui-*
skills included.** Ark is built on Frictionless, and the ui-* skills are
necessary for full operation. That retires `specs/main.md`'s rule that
per-project setup "does NOT install UI building skills… never global"; the rule
was written for a per-project install that no longer exists. The canonical
ui-* copies live in the Frictionless repo, and they reach ark's image through
Frictionless's `install/skills/`. So setup links every `~/.ark/skills/*`
directory, globbed like the agents, and the hardcoded `ark`/`ui` pair goes.

Measured against the shipped image (2026-09-24), three gaps this closes or
exposes:

- **Bundled but never installed.** The image carries `ui-basics`, `ui-fast`,
  `ui-highlight`, `ui-testing`, `ui-themer`, `ui-thorough`, and `ui-variables`,
  and setup extracts them to `~/.ark/skills/`, but only `ark` and `ui` reach
  `~/.claude/`. On a clean install the rest are present and invisible to Claude.
- **The ui-* skills exist in two hand-kept copies in the Frictionless repo.**
  The image is built from `install/skills/`, and the skills Frictionless's own
  sessions use are real directories in `.claude/skills/`. The two had drifted:
  the `install/skills/` `ui-basics` lacked the "Slimmest DOM Possible"
  principle, and it had no `ui-diagnostics` at all. Bill refreshed it the same
  day, and all nine ui skills now match. The drift is repaired; the duplicate
  that let it happen is still there. **DECIDED (Bill, 2026-09-24):
  `install/skills/` is the canonical copy**, and Frictionless's
  `.claude/skills/ui*` become links into it, so the bundle's source needs no
  change.

**DECIDED (Bill, 2026-09-24): the ui-* skills are global only, and ark's repo
copies go.** ark's `.claude/skills/ui*` were a third copy of the same files.
They are removed, and ark sessions get the ui skills from `~/.claude/skills/`
like every other project. Every `~/.claude/skills/*` entry is a link into
`~/.ark/skills/`, which is the model setup will implement. Done by hand on the
dev box the same day, after checkpointing every fossil checkout involved:
`~/.claude/skills/ui*` link to `~/.ark/skills/ui*`, which on the dev box link on
into Frictionless's `install/skills/` so edits show live (an installed user's
are the extracted copies). The two projects that linked into ark's copies
had those links removed, so they pick up the global skills too.
- **ark's own skills ship as `SKILL.md` alone.** The Makefile's bundle loop
  copies one file per skill, so the `ark` skill's guard scripts,
  `ark-recall.md`, and `hermes-*.md`, and `luhmann`'s `persona.md`, never reach
  the image. This is the build-time twin of setup's copy defect below, and
  directory links cannot fix it; the bundle has to carry the whole directory.

**No copying anywhere.** Today's copy path in `installBundledSkillsAndAgent` has
two defects of its own, which the directory links retire:

- It copies only each skill's `SKILL.md`, so any other file in a skill directory
  never arrives.
- `os.WriteFile` follows a symlink. Where a user has hand-linked
  `~/.claude/agents/<name>.md` into a source tree (as on the dev box), every
  `setup` overwrites that source file with the bundled copy.

**DECIDED (Bill, 2026-09-24): `--local` needs an existing install.**
`ark setup --local` links into the current directory's `.claude/` and prints the
CLAUDE.md hint. It is the first time setup reads the cwd, so its help must say
so. It refuses to run without the marker; it connects a project to an install
and does not create one.

**The CLAUDE.md line stays a printed hint.** This was decided before this carve
and is recorded so nobody reopens it: `specs/main.md` says the binary "cannot
edit CLAUDE.md intelligently," and the hint is written so that any model tier,
Haiku included, can follow it.

**DECIDED (Bill, 2026-09-24): `ark update` refreshes `~/.ark`.** It brings the
bundled skills, agents, and apps up to the running binary's versions. It must
not clobber sanctioned edits: app-activation extracts built-in apps into
`~/.ark/apps` precisely so a user can edit them in place, and `~/.claude` links
straight to the skill and agent directories.

**DECIDED (Bill, 2026-09-24): `ark update` downloads the latest release from
GitHub** (`https://github.com/zot/ark/releases`). This settles where the new
bundle comes from; it was open until now, between a downloaded binary installing
itself and `~/.ark/ark` refreshing from its own bundle. What follows from it,
measured the same day:

- **Two phases.** The running binary carries the *old* bundle, so it can only
  download the new binary and swap it into `~/.ark`. The refresh of skills,
  agents, and apps has to come from the new binary, which the old one runs after
  the swap. On Windows the swap is the rename-aside path under *Measured*.
- **Version check first.** The binary knows its version from the build
  (`-X github.com/zot/ark.Version=…` in the Makefile); `update` compares it
  against the latest release tag and does nothing when it is already current,
  never downgrading. There is no `--version` flag today.
- **A symlinked binary is a developer install.** Where `~/.ark/ark` is a link
  into a source tree, as on the dev box, a swap would replace the link with a
  downloaded file and break the build-and-run loop. `update` leaves such a
  binary alone. **DECIDED (Bill, 2026-09-24): on a symlinked binary, `update`
  gripes and does not refresh anything**, since refreshing assets around a
  binary it did not install risks mismatches.
- **DECIDED (Bill, 2026-09-24): `ark update --no-exe`** updates everything
  except the ark executable, which it assumes is already new, as with a
  developer symlink to a freshly built binary. The skills, agents, apps, and
  model come from that running exe's own bundle and nothing is downloaded for
  the exe, so the assets always match the binary that carried them. It is the
  update path for a developer install: plain `update` on a symlinked exe gripes,
  points at `--no-exe`, and changes nothing, while `update --no-exe` proceeds.
  Pairing a new release's assets with the old exe would be the mismatch the
  gripe exists to avoid. The refresh is
  manifest-aware, so a link (as on the dev box, where `~/.ark/skills/*` point
  into source trees) is left as a link, never replaced by a copy.
- **The release has to carry what `update` fetches.** Today's only release,
  v0.5.0, has a single asset (`ark-0.5.0-linux-amd64`), no Windows asset, and no
  checksums. `update` needs one asset per platform under a predictable name and
  a published checksum to verify before swapping, so the release process changes
  with it, and a new release is needed before `update` has anything to find.
- **Failure leaves everything as it was.** A failed download, checksum, or swap
  must leave the old binary and `~/.ark` intact. `llamalibs.go` already has a
  GitHub download path (`downloadAndExtract`) to reuse.

**DECIDED (Bill, 2026-09-24): `ark update` downloads the nomic embedding model
when it is needed.** The bundled `ark.toml` names
`nomic-embed-text-v1.5.Q8_0.gguf` as the model, and ark starts with it, but no
code fetches the file: `setup` provisions only the llama.cpp *libraries*. So a
default install points at a missing model and search silently runs without
vectors (the trigram fallback of R1276). Measured 2026-09-24:

- **Source.** `https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.Q8_0.gguf`
  answers 200, 146,146,432 bytes.
- **A pinnable hash.** Hugging Face's `x-linked-etag` for it is
  `3e24342164b3d94991ba9692fdc0dd08e3fd7362e0aacc396a9a5c54a544c3b7`, which is
  the SHA-256 of the copy the dev box has been running. So the download can be
  verified against a pinned hash, not just trusted.
- **"If needed" means the configured model file is absent.** A present file or
  a link (the dev box links it from a sibling checkout) is left alone.

The same gap exists on a fresh `setup`, which leaves the default config naming
a model it never fetched. **DECIDED (Bill, 2026-09-24): `setup` downloads it
too**, the same way `update` does, so a fresh install searches with vectors from
the start.

Which model is the default is itself under review in
[embedding-review.md](embedding-review.md). `update` is model-neutral either
way: it fetches whatever model the config names, from a per-model URL and a
pinned hash, so that review never blocks this carve.

**DECIDED (Bill, 2026-09-24): installed-file hashes live in their own file in
`~/.ark`.** Proposed name `install-manifest.json`. Not `ark.toml`, for three
reasons:

- `ark.toml` is hand-edited config, and every `update` rewrites every hash. TOML
  encoders do not round-trip comments or ordering.
- `~/.ark` is often a version-controlled checkout. A separate file can be tracked
  or ignored on its own; in `ark.toml` the hash churn would land in every config
  diff.
- The shape already exists. Frictionless's `ManifestEntry` is
  `{hash, onCollision, group}`, and the per-file collision policy (skip, back up,
  or report `user_modified`) is exactly what `update` needs. Reuse the shape, not
  the location: Frictionless stores its manifest inside `settings.json`.

<a id="measured"></a>
## Measured (2026-09-24, Windows build 26200)

A running exe on Windows, acting on its own file:

| Step | Result |
|---|---|
| Copy itself elsewhere | works |
| Overwrite itself in place | fails, "being used by another process" |
| Rename itself aside | works |
| Write a new exe at the original name | works |
| Delete the renamed-aside copy while still running | fails, "Access is denied" |

So setup's self-copy into `~/.ark` works. `update` replacing a running
`~/.ark/ark.exe` has to rename it aside, write the new one, and delete the old
one on a later run; the aside copy deletes normally once its process has exited.

<a id="open"></a>
## Open

- **Server start.** `ark ui install` starts a server today. The proposal is to
  drop it from setup, since installing and running are different acts and
  `ark serve` is one command away. Not decided.

<a id="items"></a>
## Items

- **Item 1.** Split `cmdInit`'s database step into a function both `setup` and
  `rebuild` call, so the `init` → `setup` recursion does not come back. Setup, in
  order: create `~/.ark` and self-copy; extract the bundle; create the database
  (the marker); make the `~/.claude/` directory links; provision llama; print
  that new agents appear in the *next* Claude session, since agent definitions
  are read at session start.
- **Item 2.** `--local`: marker check, links into the cwd's `.claude/`, the
  CLAUDE.md hint.
- **Item 3.** Earlier setups left flat `~/.claude/agents/<name>.md` copies where
  the `ark/` link now goes, and each is a duplicate name. When two files in one
  agents tree declare the same `name:`, Claude Code loads only one, chosen by
  filesystem read order. Setup removes a flat copy only when it is a regular file
  identical to the bundled agent. A changed file or a hand-made link is reported
  and left, since either may be the user's.
- **Item 4.** Drop the verbs from the command tree (`flat_cli.go`, the `ui`
  subtree). Then the docs: `README.md`'s install steps, `specs/cli-commands.md`,
  `specs/main.md`, `specs/infrastructure.md`, `specs/features.md`,
  `design/seq-install.md`, and the ark skill's setup section. Retire R331–R337
  and R436.
- **Item 5.** `update` over the manifest, with the rename-aside binary
  replacement on Windows.
- **Item 6.** `init` takes `--embed-cmd`, `--query-cmd`, `--case-insensitive`,
  and `--aliases`. Check that each has an `ark.toml` equivalent before the verb
  goes, rather than moving them onto `setup` by reflex.
- **Item 7.** The Makefile's skill loop copies `.claude/skills/<s>/SKILL.md`
  only; it should copy the whole directory. The ui-* half is Frictionless's to
  do (Item 8): its `.claude/skills/ui*` become links into the canonical
  `install/skills/`, so there is one copy to edit and the image cannot drift
  from it.
- **Item 8.** A cross-project message to Frictionless, sent through ark's
  messaging, not a direct edit of that repo. **Draft: send only once "Before
  sending" below is empty.** Frictionless's own skill install is
  `Server.Install` in `internal/mcp/tools.go` (the `skillFiles` list). It runs
  from three doors: the `ui_install` MCP tool (`handleInstall`), `ui_update`,
  and `Configure`'s auto-install, which fires whenever `<baseDir>/README.md` is
  missing. It has the same shape ark is retiring, and ark's new install collides
  with it. The points:
  - **The skill list is hardcoded per file.** Nine `{category, file}` pairs, so
    `ui-diagnostics` is never installed and any new file in a skill directory
    is silently dropped. Enumerating `install/skills/` would fix both.
  - **`.claude/skills/ui*` duplicates `install/skills/`.** The two had drifted
    (stale `ui-basics`, no `ui-diagnostics`) until the 2026-09-24 refresh.
    `install/skills/` is canonical, so the `.claude/skills/ui*` entries become
    directory links into it, leaving one copy to edit.
  - **It copies into the project's `.claude/skills/`, and that now shadows
    ark.** ark's setup will link every bundled skill, ui-* included, into
    `~/.claude/skills/`. Claude Code prefers a project definition over a user
    one, so in any project where Frictionless's install has run, its frozen
    copies override the live linked ones, and they never update. The ask is to
    move to the same model (directory links at user scope), or at least to stop
    installing ui-* per project when ark's links are present. Measured on the
    dev box (2026-09-24): six project directories hold real
    `.claude/skills/ui*` copies (ark and Frictionless among them), and two hold
    links pointing into ark's `.claude/skills/`. So the shadowing is present
    now, not hypothetical.
  - **`installFile` creates symlinks with `os.Symlink`** (the bundle-symlink
    branch at its top), which fails for an unprivileged Windows user; see
    [windows-support.md Item 3](windows-support.md#3). The job-tracker app's
    `ln -sfn` (`apps/job-tracker/app.lua`) is the same problem in Lua, with a
    Unix binary Windows does not have.
  - **FYI, the manifest.** ark's `update` will reuse Frictionless's
    `ManifestEntry` shape (`{hash, onCollision, group}`) in its own
    `~/.ark/install-manifest.json`. That may be worth sharing as code, not just
    as shape.

  **Before sending.** What we do not yet know, and the message is weaker
  without:
  - **Does ark's embedded runtime trigger Frictionless's auto-install?** ark
    embeds Frictionless through flib. If ark's runtime reaches `Configure` with
    a base dir whose `README.md` is missing, `Server.Install` runs and copies
    ui skills into some project's `.claude/`. Which base dir and which project
    root ark passes decides whether ark itself is a source of the shadowing
    copies. Trace it before claiming either way.
  - **Which of the six copies came from `Server.Install`, and which by hand?**
    The manifest in each project's Frictionless `settings.json`
    (`installManifest`) would say. It matters for the ask: if most came from
    hand-copying, changing `Server.Install` alone would not clear them.
  - **Frictionless's Windows position.** Whether Frictionless ships a Windows
    build of its own, or only reaches Windows embedded in ark, changes how hard
    to press the `os.Symlink` and `ln -sfn` points.
