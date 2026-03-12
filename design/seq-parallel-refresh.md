# Sequence: Parallel Refresh

RefreshStale with worker pool and ChanSvc write serialization.

```
RefreshStale                  Workers (N=NumCPU)              ChanSvc (writeCh)
    |                              |                              |
    |-- StaleFiles() ------------->|                              |
    |<- statuses ------------------|                              |
    |                              |                              |
    |-- start ChanSvc ------------|-----> go func() {            |
    |                              |        for fn := range ch {  |
    |                              |            fn()              |
    |                              |        }                     |
    |                              |      }                       |
    |                              |                              |
    |-- fan out stale files ------>|                              |
    |   (via channel)              |                              |
    |                              |-- read file from disk        |
    |                              |-- chunk content              |
    |                              |-- extract tags, defs         |
    |                              |-- prepare fts content        |
    |                              |                              |
    |                              |-- writeCh <- func() {       |
    |                              |     fts.ReindexWithContent() |
    |                              |     vec.RemoveFile()         |
    |                              |     vec.AddFile()            |
    |                              |     store.AppendTags()       |
    |                              |     store.AppendTagDefs()    |
    |                              |   }  ----------------------->|
    |                              |                              |-- execute writes
    |                              |                              |
    |-- wait for workers --------->|                              |
    |-- close(writeCh) -----------|----------------------------->|
    |-- wait for ChanSvc ---------|----------------------------->|
    |                              |                              |
    |<- missing files, errors      |                              |
```

## Notes

- Workers read a `chan fileJob` where each job is a stale file path+strategy
- Workers send `func()` closures to `writeCh`
- ChanSvc drains `writeCh` sequentially — LMDB single-writer satisfied
- Worker errors are collected via error channel, not panics
- Missing files bypass workers entirely (collected in initial scan)
