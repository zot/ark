# Sequence: ark message subcommands

**Requirements:** R450-R478

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
         │      Set("request", ID)
         │      Set("from-project", FROM)
         │      Set("to-project", TO)
         │      Set("status", "open")
         │      Set("issue", ISSUE)
         │
         ├──> Render() + append heading + issue body
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
         │      Set("response", REQUEST_ID)
         │      Set("from-project", FROM)
         │      Set("to-project", TO)
         │      Set("status", "done")
         │
         ├──> Render() + append "# RESP <ID>" heading
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
