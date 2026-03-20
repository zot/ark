# Sequence: ark message subcommands

**Requirements:** R450-R478, R489-R501, R525, R530-R540, R580-R584, R617, R618, R619, R620

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
         │      if TAG == "status":                        ← R710, R711
         │        TagBlock.Set("status-date", today YYYY-MM-DD)
         │
         ├──> TagBlock.Render() → new file bytes
         │
         └──> write FILE
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
         │      Set("status-date", today YYYY-MM-DD)       ← R708
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
         │      Set("status-date", today YYYY-MM-DD)       ← R709
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
                  emit fix command (ark tag set ...)
                for each stray tag:
                  describe finding + line number
                  emit "remove line N" instruction
```

## Flow: inbox

```
CLI ──> parse flags: --project, --from, --all, --include-archived,
         │           --counts, --unmatched
         │
         ├──> withDB or server proxy:
         │      DB.Inbox(all, includeArchived) → []InboxEntry
         │
         ├──> CLI post-filters: --project, --from
         │
         ├──> pair entries by requestId:                    ← R714, R723
         │      byId[requestId] = {request, response}
         │
         ├──> if --unmatched:                               ← R713, R716
         │      keep only requests where byId[id] has no response
         │
         ├──> compute bookmark lag per pair:                 ← R719, R720, R721
         │      for each paired entry:
         │        if response exists and reqResponseHandled != respStatus
         │          → lag on request side
         │        if request exists and respRequestHandled != reqStatus
         │          → lag on response side
         │
         ├──> sort: @status:open first, then by path
         │
         ├──> if --counts:
         │      count entries per status value
         │      output tab-separated: status\tcount (sorted alphabetically)
         │    else:
         │      output tab-separated lines + lag field      ← R718, R722
```
