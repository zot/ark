# Sequence: derived tag definitions from pattern frontmatter

How a pattern file's `summary` becomes a D record, and why the append
path derives nothing.

Participants: Indexer, Patterns, Store, Config.

## 1. Full index or full refresh of a pattern file

```
1. Indexer.executeFullRefresh / AddFile  — content = the whole file
   1.1. Indexer.fileLevelTags(path, content, strategy)
        1.1.1. strategy == "pdf" → return (nil, nil); raw bytes are not text
        1.1.2. defs = ExtractTagDefs(content)          — any literal @tag: lines
        1.1.3. Patterns.DerivedDef(path, content)
               1.1.3.1. path is *.md directly inside Config.patterns_dir?  else ok=false
               1.1.3.2. parseFrontmatter(content) yields name and summary? else ok=false
               1.1.3.3. return (slug(path), summary, true)
        1.1.4. ok → defs[slug] = summary                — merged into the same map
   1.2. Store.UpdateTagDefs(fileid, defs)               — replaces this file's D records
```

Step 1.1.4 merges rather than replacing, so a pattern file that *also*
carries a literal `@tag:` line keeps both. The derived entry wins only
on its own slug key.

## 2. Append to an already-indexed file

```
2. Indexer.executeAppend                                — content = appended bytes only
   2.1. Indexer.fileLevelTags(path, newBytes, strategy)
        2.1.1. defs = ExtractTagDefs(newBytes)
        2.1.2. Patterns.DerivedDef(path, newBytes)
               2.1.2.1. path check passes — it is the same file
               2.1.2.2. parseFrontmatter(newBytes) finds no leading ---  → ok=false
        2.1.3. no derived entry
   2.2. Store.AppendTagDefs(fileid, defs)               — adds without removing
```

The definition written by step 1.2 survives untouched. This is correct
rather than merely convenient: an append cannot have altered the head of
the file, so the derived definition cannot have gone stale (R3275).

The content test at 2.1.2.2 is what carries this — nothing checks
"am I on the append path", so the two paths need no coordination and
cannot disagree.
