# Sequence: ark message subcommands

**Requirements:** R450-R478, R489-R501, R525, R530-R540, R580-R584

## Flow: set-tags

```
CLI ──> parse args: FILE, pairs of (TAG, VALUE)
         │
         ├──> read FILE bytes
         │
         ├──> TagBlock.Parse(bytes)
         │
         ├──> for each (TAG, VALUE):
         │      TagBlock.Set(TAG, VALUE)
         │
         ├──> TagBlock.Render() → new file bytes
         │
         └──> write FILE atomically (write tmp, rename)
```

## Flow: get-tags

```
CLI ──> parse args: FILE, optional TAG names
         │
         ├──> read FILE bytes
         │
         ├──> TagBlock.Parse(bytes)
         │
         ├──> if TAG names given:
         │      for each TAG:
         │        TagBlock.Get(TAG) → print "tag\tvalue"
         │        if not found → set exit code 1
         │    else:
         │      TagBlock.Tags() → print all "tag\tvalue"
         │
         └──> exit with accumulated status
```

## Flow: new-request

```
CLI ──> parse flags: --from, --to, --issue, FILE
         │
         ├──> if FILE exists → error
         │
         ├──> derive ID from basename(FILE) without extension
         │
         ├──> build TagBlock:
         │      Set("ark-request", ID)
         │      Set("from-project", FROM)
         │      Set("to-project", TO)
         │      Set("status", "open")
         │      Set("issue", ISSUE)
         │
         ├──> Render() + append heading + issue body
         │
         ├──> if stdin is not a terminal:
         │      readStdinBody() → body text (read until lone ".")
         │      append body after scaffold
         │
         └──> write FILE
```

## Flow: new-response

```
CLI ──> parse flags: --from, --to, --request, FILE
         │
         ├──> if FILE exists → error
         │
         ├──> build TagBlock:
         │      Set("ark-response", REQUEST_ID)
         │      Set("from-project", FROM)
         │      Set("to-project", TO)
         │      Set("status", "accepted")
         │
         ├──> Render() + append "# RESP <ID>" heading
         │
         ├──> if stdin is not a terminal:
         │      readStdinBody() → body text (read until lone ".")
         │      append body after heading
         │
         └──> write FILE
```

## Flow: check

```
CLI ──> parse args: FILE
         │
         ├──> read FILE bytes
         │
         ├──> TagBlock.Parse(bytes)
         │
         ├──> TagBlock.Validate() → structural problems
         │
         ├──> TagBlock.ScanBody() → stray tag patterns
         │
         ├──> if no problems → exit 0
         │
         └──> format crank-handle output:
                for each problem:
                  describe issue + line number
                  emit fix command (ark message set-tags ...)
                for each stray tag:
                  describe finding + line number
                  emit "remove line N" instruction
```

## Flow: inbox

```
CLI ──> parse flags: --project, --from, --all, --include-archived, --counts
         │
         ├──> withDB or server proxy:
         │      DB.TagFiles(["status"]) → list of (path, size) entries
         │      filter to paths containing /requests/
         │
         ├──> for each file path:
         │      read file bytes
         │      TagBlock.Parse(bytes)
         │      Get("status"):
         │        if not --all: skip if "completed", "done", or "denied"
         │      Get("archived"):
         │        if not --include-archived: skip if present
         │      Get("to-project") → skip if --project given and doesn't match
         │      Get("from-project") → skip if --from given and doesn't match
         │      collect: status, to-project, from-project,
         │               issue (or "response:<id>"), path
         │
         ├──> sort: @status:open first, then by path
         │
         ├──> if --counts:
         │      count entries per status value
         │      output tab-separated: status\tcount (sorted alphabetically)
         │    else:
         │      output tab-separated lines (existing format)
```
