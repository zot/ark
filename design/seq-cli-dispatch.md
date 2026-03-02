# Sequence: CLI Dispatch

Covers how the CLI decides to proxy to the server or cold-start.

## Participants
- CLI
- Server (running)
- DB

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

## Notes

Cold-start pays the embedding model load cost on every invocation.
For search-heavy workflows, `ark serve` amortizes this. For
occasional maintenance commands (status, files, config), cold-start
is fine — those don't need the embedding model.
