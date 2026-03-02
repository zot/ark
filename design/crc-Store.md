# Store
**Requirements:** R6, R15, R45, R103, R104, R105, R106, R107

Ark's own LMDB subdatabase. Manages missing files, unresolved files,
and ark-level settings.

## Knows
- env: *lmdb.Env — shared LMDB environment
- dbi: lmdb.DBI — ark subdatabase handle

## Does
- Open(env): open the ark subdatabase
- AddMissing(fileid, path, lastSeen): store missing file record (M prefix)
- RemoveMissing(fileid): remove missing record
- ListMissing(): all missing file records
- AddUnresolved(path, dir): store unresolved file record (U prefix)
- RemoveUnresolved(path): remove unresolved record
- ListUnresolved(): all unresolved file records
- CleanUnresolved(): remove entries for files no longer on disk
- DismissByPattern(patterns): remove missing records matching patterns
- ResolveByPattern(patterns): remove unresolved records matching patterns
- GetSettings(): read ark settings (I key)
- PutSettings(settings): write ark settings

## Collaborators
- Matcher: used by DismissByPattern and ResolveByPattern

## Sequences
- seq-add.md
- seq-server-startup.md
