# Sequence: sources check

Reconciles glob source patterns in config against concrete sources.

```
CLI/Server                Config                          DB
    |                       |                              |
    |--SourcesCheck()------>|                              |
    |                       |--ResolveGlobs()              |
    |                       |  for each source:            |
    |                       |    IsGlob(dir)?              |
    |                       |    yes: expandHome, Glob     |
    |                       |    collect resolved dirs     |
    |                       |<-resolved[]--                |
    |                       |                              |
    |  diff resolved vs existing sources                   |
    |  new dirs: AddSource(dir, strategy)                  |
    |  MIA dirs: flag (exist in config, gone from disk)    |
    |  orphans: concrete source, no glob owns it           |
    |                       |                              |
    |  SaveConfig()-------->|                              |
    |<-SourcesCheckResult---|                              |
```

SourcesCheckResult: { Added []string, MIA []string, Orphaned []string }
