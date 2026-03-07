# Ark

**Version: 2.5.0**

A subconsciousness for Claude. Ark indexes everything on your machine — chat
logs, projects, personal notes — and gives Claude long-term memory across
sessions.

- **Remind and inspire.** Ark can "remind" Claude of anything you've discussed
  in past conversations and "inspire" it with relevant knowledge from your
  files — automatically, as you talk.
- **Approve once, recall everything.** You approve ark once. After that, Claude
  can read any file you've indexed — no per-file permission popups.
- **Ungodly fast.** Results in microseconds. Suitable for interactive search,
  instant queries from Claude, and auto-querying on every message.
- **Works without a model.** Trigram search and tags give fully functional
  recall on any hardware. Vector search enhances results when available but
  isn't required.
- **Tags as a living vocabulary.** `@decision:`, `@pattern:`, `@question:` —
  tags appear in any file, describe themselves in any file, and form a
  navigable graph of your knowledge. No setup, no taxonomy — they emerge
  from use.
- **Knowledge vs memory.** Ark distinguishes distilled facts (notes, code,
  docs) from experience (conversations, process, wrong turns). Claude knows
  which kind of recall it's looking at.
- **Files on disk, index in LMDB.** Ark doesn't slurp in your files — it
  keeps a lightweight index and tracks when files change, disappear, or show
  up new.
- **One server, all agents.** A single ark server shares the index across
  every Claude session on your machine. CLI commands proxy automatically.

## Build

```
make install
```

Or without make:

```
go build -buildvcs=false -o ~/.ark/ark ./cmd/ark
```
