# Sequence: Install Flow

Covers the three install commands and how they chain together.

## Participants
- CLI
- Bundle (embedded zip assets)
- DB

## Flow: ark ui install (single entry point)

```
CLI.cmdUIInstall(cwd)
  │
  ├──> CLI.cmdInit(--if-needed)
  │     │
  │     ├──> check data.mdb exists in dbPath
  │     │     └── exists → skip DB creation
  │     │
  │     ├──> check html/ exists in dbPath
  │     │     └── missing → CLI.cmdSetup()
  │     │
  │     └──> (if DB needed) create DB normally
  │
  ├──> create cwd/.claude/skills/ if needed
  │
  ├──> symlink cwd/.claude/skills/ark → ~/.ark/skills/ark
  ├──> symlink cwd/.claude/skills/ui  → ~/.ark/skills/ui
  │
  └──> print crank-handle prompt for CLAUDE.md
```

## Flow: ark setup (idempotent bootstrap)

```
CLI.cmdSetup()
  │
  ├──> create ~/.ark/ if needed
  │
  ├──> Bundle.ExtractBundle → ~/.ark/
  │     └── html/, lua/, viewdefs/, apps/
  │
  ├──> linkapp add ark
  │     └── creates lua/ and viewdefs/ symlinks
  │
  ├──> install ~/.claude/skills/ark/SKILL.md
  ├──> install ~/.claude/skills/ui/SKILL.md
  ├──> install ~/.claude/agents/ark.md
  │
  └──> report what was installed/updated
```

## Flow: ark init (DB creation with auto-setup)

```
CLI.cmdInit(flags)
  │
  ├──> if --if-needed && data.mdb exists → exit 0
  │
  ├──> if !--no-setup && html/ missing in dbPath
  │     └──> CLI.cmdSetup()
  │
  ├──> seed from ark.toml if present
  ├──> microfts2.Create(dbPath, opts)
  ├──> microvec.Create(env, opts)
  ├──> Store.Init(env)
  ├──> write ark.toml
  │
  └──> print "initialized ark database at <dbPath>"
```

## Notes

The common case is `ark ui install` in a project directory. First
time: extracts assets, creates DB, symlinks skills, prints
CLAUDE.md instructions. Second time: everything exists, symlinks
are refreshed, crank-handle re-printed (idempotent).

`ark setup` alone is for binary updates — refresh assets and skills
without touching the DB or any project.
