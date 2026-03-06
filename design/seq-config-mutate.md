# Sequence: Config Mutation

## Add Include Pattern (via CLI, cold-start)

```
CLI                    Config                 Matcher
 │                       │                      │
 │  Load(tomlPath)       │                      │
 │──────────────────────>│                      │
 │                       │                      │
 │  AddInclude(pat,src)  │                      │
 │──────────────────────>│                      │
 │                       │  ParsePattern(pat)   │
 │                       │─────────────────────>│
 │                       │  valid/invalid       │
 │                       │<─────────────────────│
 │                       │                      │
 │                       │  Validate()          │
 │                       │  (check conflicts)   │
 │                       │                      │
 │  Save(tomlPath)       │                      │
 │──────────────────────>│                      │
 │  ok / error           │                      │
 │<──────────────────────│                      │
```

## Show Why (via CLI, cold-start)

```
CLI                    Config                 Matcher
 │                       │                      │
 │  Load(tomlPath)       │                      │
 │──────────────────────>│                      │
 │                       │                      │
 │  ShowWhy(filePath)    │                      │
 │──────────────────────>│                      │
 │                       │  EffectivePatterns() │
 │                       │  (global + source)   │
 │                       │                      │
 │                       │  read .gitignore,    │
 │                       │  .arkignore if exist │
 │                       │                      │
 │                       │  Classify(inc,exc,p) │
 │                       │─────────────────────>│
 │                       │  result + reason     │
 │                       │<─────────────────────│
 │                       │                      │
 │  WhyResult{           │                      │
 │    status, patterns,  │                      │
 │    sources, conflict  │                      │
 │  }                    │                      │
 │<──────────────────────│                      │
```

## Via Server (proxy path)

```
CLI                    Server                 Config
 │                       │                      │
 │  POST /config/...     │                      │
 │──────────────────────>│                      │
 │                       │  Config.Method(...)  │
 │                       │─────────────────────>│
 │                       │  result              │
 │                       │<─────────────────────│
 │  JSON response        │                      │
 │<──────────────────────│                      │
```
