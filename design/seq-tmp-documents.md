# Sequence: Temporary Documents

Covers tmp:// add/remove through CLI and Lua, and the onlyIfTmp
search optimization.

## Participants
- CLI
- Server
- DB
- microfts2.Overlay

## Flow: ark add tmp://name (CLI)

```
CLI ──> detect tmp:// prefix in path
         │
         ├──> read content from stdin
         │
         ├──> serverClient(arkDir)
         │     └── must be running (tmp is server-side)
         │
         └──> proxy POST /tmp/add {path, strategy, content}
               │
               └──> Server.HandleTmpAdd
                     │
                     ├──> DB.AddTmpFile(path, strategy, content)
                     │     ├── microfts2.AddTmpFile → overlay
                     │     └── extract tags from content
                     │
                     └──> return fileid
```

## Flow: ark remove tmp://name (CLI)

```
CLI ──> detect tmp:// prefix in path
         │
         ├──> serverClient(arkDir)
         │
         └──> proxy POST /tmp/remove {path}
               │
               └──> Server.HandleTmpRemove
                     │
                     └──> DB.RemoveTmpFile(path)
                           └── microfts2.RemoveTmpFile → overlay
```

## Flow: Lua tmp_add

```
Lua ──> mcp.tmp_add("tmp://notes", content, "lines")
         │
         └──> Go function (registered via WithLua)
               │
               └──> DB.AddTmpFile(path, strategy, content)
                     ├── microfts2.AddTmpFile → overlay
                     └── extract tags
```

## Flow: Search with onlyIfTmp (CLI, no --session)

```
CLI ──> cmdSearch (no --session, no --no-tmp)
         │
         ├──> serverClient(arkDir)
         │     ├── nil → local search (no server, no tmp possible)
         │     └── non-nil → server running
         │
         └──> proxy POST /search {query, opts, onlyIfTmp: true}
               │
               └──> Server.handleSearch
                     │
                     ├──> onlyIfTmp && !DB.HasTmp()
                     │     └── return HTTP 204 (no content)
                     │
                     └──> onlyIfTmp && DB.HasTmp()
                           ├── run search (includes overlay)
                           └── return results
         │
         ├── HTTP 204 → CLI searches locally via withDB
         └── results → CLI uses server results
```

## Flow: Search with --no-tmp

```
CLI ──> cmdSearch --no-tmp
         │
         └──> withDB (always local, skip server probe)
               │
               └──> search with WithNoTmp() option
                     │
                     └──> microfts2 skips overlay entirely
```

## Notes

- tmp:// documents only exist in server memory. CLI cold-start
  never sees them — by design.
- The onlyIfTmp optimization avoids proxying when there are no
  tmp docs, which is the common case. The CLI pays one HTTP
  round-trip to check, which is cheaper than always proxying.
- When --session is used, search always goes through the server
  (for the session cache), so tmp docs are included automatically.
