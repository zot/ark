# DB
**Requirements:** R1, R2, R3, R5, R6, R7, R28, R29, R30, R33, R40, R31, R32, R34

Main ark facade. Owns the LMDB lifecycle and coordinates microfts2,
microvec, and the ark subdatabase. Entry point for all operations.

## Knows
- fts: *microfts2.DB — trigram search engine
- vec: *microvec.DB — vector search engine
- store: *Store — ark's own subdatabase
- config: *Config — parsed source configuration
- dbPath: string — database directory path

## Does
- Init(path, opts): create new database — open microfts2, pass env to
  microvec, create ark subdatabase, write default config
- Open(path): open existing database — same sequence, read config
- Close(): close in reverse order (store, vec, fts)

## Collaborators
- Config: loads and validates ark.toml
- Store: ark's own LMDB subdatabase
- Scanner: walks directories (uses Config + Matcher)
- Indexer: adds/removes files in both engines
- Searcher: queries both engines and merges results

## Sequences
- seq-add.md
- seq-search.md
