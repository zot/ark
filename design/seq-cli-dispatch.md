# Sequence: CLI Dispatch

Covers how the CLI decides to proxy to the server or cold-start.

## Participants
- CLI
- Server (running)
- DB

## Flow: Global Flag Parsing (before dispatch)

```
CLI ──> expandVerbosityFlags(os.Args)
         └── -vvv → -v -v -v

CLI ──> parse --dir and -v from expanded args
         ├── --dir sets arkDir (default ~/.ark/)
         ├── each -v increments verbosity counter
         └── remaining args passed to subcommand
```

## Flow: Server Running

```
CLI ──> CLI.DetectServer(dbPath)
         │
         ├──> socketPath = dbPath + "/ark.sock"
         ├──> conn = net.Dial("unix", socketPath)
         │     └── success → server is running
         │
         └──> CLI.Proxy(conn, request)
               ├── build HTTP request (method, path, JSON body)
               ├── send over Unix socket connection
               ├── read HTTP response
               └── output JSON result to stdout
```

## Flow: No Server (cold-start)

```
CLI ──> CLI.DetectServer(dbPath)
         │
         ├──> conn = net.Dial("unix", socketPath)
         │     └── error → no server
         │
         ├──> CLI.CleanStaleSocket(dbPath)
         │     └── if socket file exists, remove it
         │
         └──> CLI.ColdStart(dbPath)
               ├── DB.Open(dbPath)
               │    ├── microfts2.Open (loads LMDB env)
               │    ├── microvec.Open (receives env)
               │    └── Store.Open
               │
               ├── execute the requested operation
               │    (search, add, remove, etc.)
               │
               ├── output result to stdout
               │
               └── DB.Close()
```

## Flow: Search (server-first)

```
CLI ──> cmdSearch: parse flags, build request struct
         │
         ├──> CLI.DetectServer(dbPath)
         │     └── try Unix socket connect
         │
         ├── [server running]
         │    └──> proxyDecode(POST /search, request)
         │          ├── success → print results, return
         │          └── error → fall through to local
         │
         └── [no server, or proxy failed]
              └──> ColdStart(dbPath)
                    ├── DB.Open
                    ├── execute search locally
                    └── DB.Close
```

Search always tries the server first because the server keeps
caches warm (file name map, LMDB pages, session chunk caches).
The server path avoids the cold-start DB open cost entirely.
If the server is unavailable or the proxy fails, local search
is the fallback.

## Notes

Cold-start pays the DB open cost on every invocation.
For search-heavy workflows, `ark serve` amortizes this. For
occasional maintenance commands (status, files, config), cold-start
is fine.
