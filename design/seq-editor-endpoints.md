# Sequence: Editor HTTP Endpoints
**Requirements:** R1069-R1098, R1216-R1221

HTTP endpoints serving the standalone markdown editor's HostAPI.

## Grouped Search (R1069-R1075)

```
Browser         Server              DB/Searcher
  |                |                     |
  |--POST /search/grouped-------------->|
  |  {query,mode,k,session,...}         |
  |                |--SearchGrouped---->|
  |                |  (mode dispatch,   |
  |                |   FillChunks,      |
  |                |   group by file)   |
  |                |<---[]GroupedResult--|
  |                |  (range,score,     |
  |                |   content,         |
  |                |   contentType,     |
  |                |   preview)         |
  |<--200 JSON array--------------------|
```

Session-scoped variant: if `session` field is set, wraps
SearchGrouped in Session.RunSearch for chunk caching.

## Tag Completion (R1076-R1080)

```
Browser         Server              DB/Store
  |                |                     |
  |--POST /tags/complete--------------->|
  |  {prefix:"sta"}                     |
  |                |                     |
  |  [prefix empty?]                    |
  |  yes: |--TagList()----------------->|
  |        |<---[]TagCount--------------|
  |        |--ListTagDefs([])---------->|
  |        |<---[]TagDefRecord----------|
  |        merge: name from T, desc from D
  |                |                     |
  |  no:  |--ListTagDefs([])----------->|
  |        |<---[]TagDefRecord----------|
  |        filter by prefix, dedup by name
  |                |                     |
  |<--200 [{name,description}]----------|
```

## Tag Value Completion (R1081-R1085)

```
Browser         Server              DB/Store          Disk
  |                |                     |               |
  |--POST /tags/values----------------->|               |
  |  {tag:"status",prefix:"in"}        |               |
  |                |--TagFiles([tag])--->|               |
  |                |<--[]TagFileInfo-----|               |
  |                |  (path, fileid)    |               |
  |                |                     |               |
  |                |  for each file:    |               |
  |                |--ReadFile(path)-----|-------------->|
  |                |<--content-----------|---------------|
  |                |  extract @tag: value               |
  |                |                     |               |
  |                |  aggregate values,  |               |
  |                |  filter by prefix,  |               |
  |                |  sort by count desc |               |
  |<--200 [{value,count}]---------------|               |
```

## File Save (R1086-R1089)

```
Browser         Server              DB              Indexer
  |                |                  |                 |
  |--POST /save--->|                  |                 |
  |  {path,content}|                  |                 |
  |                |--IsIndexed(path)->|                 |
  |                |<--bool-----------|                 |
  |  [not indexed: 403]               |                 |
  |                |                  |                 |
  |                |--WriteFile(path,content)---->disk   |
  |                |--RefreshFile(path)----------->|--->|
  |                |                  |            index |
  |<--200 OK-------|                  |                 |
```

## Set Tags (R1090-R1093)

```
Browser         Server              TagBlock        Disk
  |                |                     |             |
  |--POST /set-tags>|                    |             |
  |  {path,tags}   |                     |             |
  |                |--ReadFile(path)-----|------------>|
  |                |<--content-----------|-------------|
  |                |--ParseTagBlock----->|             |
  |                |  for each tag:      |             |
  |                |--Set(name,value)--->|             |
  |                |  [status? auto-set status-date]   |
  |                |--Render()---------->|             |
  |                |<--bytes-------------|             |
  |                |--WriteFile----------|------------>|
  |<--200 OK-------|                     |             |
```

Watcher picks up the file change and triggers re-indexing.

## Show in Folder (R1216-R1221)

```
Browser         Server              OS
  |                |                 |
  |--POST /file/show--------------->|
  |  {path}        |                 |
  |                |--IsInSource---->|
  |                |<--bool----------|
  |  [not in source: 403]           |
  |                |                 |
  |                |  [Linux:]       |
  |                |--gdbus call --->|
  |                |  FileManager1.  |
  |                |  ShowItems      |
  |                |                 |
  |                |  [macOS:]       |
  |                |--open -R path-->|
  |                |                 |
  |                |  [Windows:]     |
  |                |--explorer.exe-->|
  |                |  /select,path   |
  |                |                 |
  |<--200 OK-------|                 |
```
