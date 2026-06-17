# Config Tracking

The database stores a copy of the full configuration so it can detect
changes, recover from accidental config corruption, and prevent silent
index staleness.

## I Records: Config Storage

Each config field gets its own index I record (`I[name] → value`),
following the same pattern as microfts2. Scalar fields store their
string representation; compound fields store JSON. Operational
non-config state (e.g. `next_tvid` counter) also lives in I records.

The known-name set, encoding per field, and schema markers
(`ec_version`, etc.) are listed in
[record-formats.md](record-formats.md) (Configuration Records section).

### Lifecycle

**Init:** Write all config fields from ark.toml to I records.

**Open:** Read I records and diff against loaded ark.toml. Classify
changes (see Change Classification below). Update I records for
benign changes.

**Config mutation** (`ark config add-source`, etc.): The existing
flow writes ark.toml. On next Open or watcher reload, the diff
detects the change and updates I records.

**Rebuild:** Clear all I records (and E records), write fresh config.
This is the hard reset.

## E: Records: Error Conditions

E: records hold persistent error or warning conditions surfaced in
`ark status`. They survive restarts and are cleared when the
condition resolves — either by config changing back, by
`ark rebuild`, or by manual `ark config` commands that fix the issue.

Record key/value layout and the disambiguating colon prefix: see
[record-formats.md](record-formats.md) (E: section). Known
condition names (`model_mismatch`, `index_stale`,
`config_catastrophe`) are also listed there.

## Change Classification

When the loaded config differs from the stored I records, classify
each changed field (two options):

### Defer (option 1): loud error, ignore change, defer to restart

- `case_insensitive` — FTS index built with different setting
- `aliases` — FTS trigrams computed differently (not currently in
  Config, but would be if added)
- `chunkers` — chunks for affected strategies are wrong
- All sources gone — likely accidental config wipe

At startup, these changes cause `ark serve` to error out — the user
must fix ark.toml, run `ark rebuild`, or pass `--force`.

At runtime (watcher reload), these changes write an E record, log a
loud warning, and do NOT update the I records for the changed fields.
The stored config remains authoritative. The E record ensures the
next startup will gate on the problem.

### Fix-minimal (option 2): loud error, small targeted fix

- `tag_model` — delete all embedding records (T vectors, EV records),
  update the I record to the new model. Embeddings regenerate on next
  reconcile. Brief delay, no stale data.

### Benign: update silently

- `sources` add/remove — scan/reconcile handles it
- `global_include` / `global_exclude` — next scan handles it
- `dotfiles` — next scan handles it
- `search_exclude` — search-time only, no index impact
- `session_ttl` — runtime only
- `schedule` — runtime only
- `strategies` map — next scan assigns strategies
- `embed_cmd` / `query_cmd` — runtime only

Benign changes update the I records immediately and proceed normally.

## Startup Behavior

On `ark serve` startup:

1. Load ark.toml → Config
2. Read I records → stored config
3. Diff each field
4. If any E records exist from prior run, check if condition resolved
5. If deferred changes or unresolved E records detected: error out,
   print diagnostic showing stored vs current config for the affected
   fields, suggest `ark rebuild` or `--force`
6. `--force` on startup: clear E records, accept current config,
   update all I records, apply fix-minimal where applicable. The user
   is saying "I know what I'm doing."
7. If fix-minimal changes detected: apply fix, update I records, warn
8. Benign changes: update I records, proceed

## Runtime Behavior (Watcher Reload)

The watcher already reloads ark.toml on file change. After reload:

1. Diff new config against I records
2. Deferred changes: write E record, log error, keep running with
   stored config for those fields
3. Fix-minimal changes: apply fix, update I records, log warning
4. Benign changes: update I records, proceed

## Status Display

`ark status` shows E record conditions after the normal output:

```
warnings:
  model_mismatch: tag_model changed from "nomic-v1.5" to "bge-small"
    embeddings may be stale — run "ark rebuild" to regenerate
  index_stale: case_insensitive changed
    FTS index was built case-sensitive — run "ark rebuild" to reindex
```

When no E records exist, nothing extra is printed.

## Recovery

`ark rebuild` is the universal fix: it deletes the database, re-runs
init (which writes fresh I records from ark.toml), then re-scans and
re-indexes everything. All E records are gone because the database
was recreated.

If ark.toml is corrupted or missing, `ark config recover` (new
command) reads the stored config from I records and writes it to
ark.toml. This is the disaster recovery path.
