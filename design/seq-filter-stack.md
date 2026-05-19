# Sequence: CLI Filter Stack Parsing and Execution

**Requirements:** R1770–R1789, R1940, R2442, R2443, R2444, R2445, R2446, R2447, R2448, R2449, R2450, R2451, R2452, R2453, R2454, R2455, R2456

Shows how `ark search` args are parsed into a filter stack and
executed through the server.

## Parse Phase

```
User → cmdSearch(args)
  cmdSearch → parseFilterStack(args)
    parseFilterStack walks args left-to-right:
      polarity = "with" (default)
      loop:
        "-with"     → polarity = "with"
        "-without"  → polarity = "without"
        "-contains" → emit {polarity, "contains", next arg}
                       subsequent bare terms coalesce into this entry
        "-fuzzy"    → emit {polarity, "fuzzy", next arg}
        "-regex"    → emit {polarity, "regex", next arg}
        "-tag"      → emit {polarity, "tag",
                              TagMatcher.ParseMatchSyntax(next arg)}
                       (R2442–R2451: sigil parse + @ normalization)
        "-file-tag" → emit {polarity, "file-tag",
                              TagMatcher.ParseMatchSyntax(next arg)}
                       (R2453: file-tag predicate, shared parser)
        "-about"    → emit {polarity, "about", next arg}
        "-files"    → emit {polarity, "files", next arg}
        "-filter-k" → set K on most recent entry (R1940)
                       warn + ignore if no entry, or entry not "about"
        bare term   → coalesce into current contains group
                       (start new contains group if none open)
        other "-*"  → pass through to remainingArgs for flag.Parse
    parseFilterStack → (filterEntries[], remainingArgs)
  cmdSearch → flag.Parse(remainingArgs)
    parses -k, --scores, --session, --chunks, --no-tmp, etc.
```

## -parse Flag

```
  if -parse:
    cmdSearch → formatFilterStack(filterEntries)
      emit "ark search" + explicit mode/polarity/value for each entry
    cmdSearch → os.Exit(0)
```

## Build Request

```
  cmdSearch splits filterEntries:
    entries[0] → primary search fields:
      "contains" → req.Contains
      "fuzzy"    → req.Fuzzy = true, req.Query
      "regex"    → req.Regex
      "about"    → req.About
      "tag"      → req.Contains (as regex pattern)
      "files"    → prepend to ChunkFilters, promote entries[1] to primary
    entries[1:] → req.ChunkFilters []ChunkFilterRow
```

## Server Execution

```
  cmdSearch → POST /search {query, contains, chunk_filters, ...}
    handleSearch → buildSearchOpts(req)
      includes req.ChunkFilters → opts.ChunkFilters
    handleSearch → primary search (SearchCombined/SearchSplit/SearchFuzzy)
      → results []SearchResultEntry
    handleSearch → BuildChunkFilters(opts.ChunkFilters, cache, paths, store)
      for each ChunkFilterRow:
        "contains" → ContainsChunkFilter
        "fuzzy"    → FuzzyChunkFilter
        "regex"    → microfts2.WithRegexFilter
        "tag"      → TagChunkFilter(row.Predicate)        (R2442)
        "file-tag" → FileTagChunkFilter(row.Predicate)    (R2453–R2456)
        "about"    → AboutChunkFilter
        "files"    → fileIDChunkFilter (glob match)
      polarity "with"    → WithChunkFilter
      polarity "without" → WithChunkExclude
    handleSearch → apply filters to results
    handleSearch → writeJSON(results)
```

## Local Fallback

```
  if server unavailable:
    cmdSearch → withDB
      same split: entries[0] → primary search opts,
                  entries[1:] → opts.ChunkFilters
      BuildChunkFilters called inline
      search + filter + format results
```
