# Sequence: suggestExtLocator (three-layer algorithm)

Covers `DB.SuggestExtLocator(chunkID)` — the workshop's
authoring widget calls it to populate the base + locator
defaults for an `@ext` routing targeting `chunkID`. The
algorithm is deterministic, runs sub-millisecond on typical
files (10–50 chunks, KB-sized text), and does not require
embeddings or external services.

## Participants
- Lua viewdef / app code
- Server (mcp bridge)
- DB
- microfts2 (chunk retrieval)
- Store (tag lookup for `@id`)
- ExtMap (cross-file scope computation)

## Flow

```
1.   Lua viewdef ──> mcp.suggestExtLocator(chunkID)
1.1.   Server bridge ──> DB.SuggestExtLocator(chunkID)
1.2.     resolve chunkID → fileID → path
1.2.1.     DB.chunkFileID(txn, chunkID) → fileID
1.2.2.     DB.fileIDPath(fileID) → path
1.3.     read target chunk's text and @id (if any)
1.3.1.     microfts2.GetChunk(path, ...) → chunk (Range, Content,
              ByteStart, ByteEnd)
1.3.2.     Store.TagValuesForChunk(chunkID, "id") → []idValue
1.4.     decide base:
            if len(idValues) > 0:
              base       = "uuid"
              baseValue  = "%" + idValues[0]
            else:
              base       = "path"
              baseValue  = path
1.5.     decide locator path:
            if path is under hardcoded read-only zone
              (`~/.claude/projects/**`) or chunker reports
              IsWritable() == false:
              go directly to Layer 3 (absolute fallback)
            if base == "uuid" and len(within-file-@id-chunks) == 1:
              locatorKind = "bare"; locatorText = ""
              skip to step 1.9
1.6.     Layer 1 — line-prefix token minimum:
1.6.1.     get all chunks in `path` via DB.AllChunks(path)
1.6.2.     for each line L in target chunk:
              tokens = tokenize(L)  // whitespace + punctuation
              for n = 1 .. len(tokens):
                prefix = join(tokens[:n])
                if prefix unique among other chunks' length-n
                    line-prefixes (case-insensitive compare):
                  candidate = (n, line_idx, original_case_prefix)
                  break
1.6.3.     pick candidate with smallest n; tiebreak earliest line
1.6.4.     if candidate found:
              if prefix contains a literal `"`:
                locatorKind = "regex"
                locatorText = "/" + regex_escape(prefix) + "/"
              else:
                locatorKind = "string"
                locatorText = `"` + prefix + `"`
              skip to step 1.9
1.7.     Layer 2 — rare-trigram-anchored substring:
1.7.1.     extract trigrams from target chunk's content
1.7.2.     for each trigram t (in order of rarity within file):
              if t does not appear in any other chunk's content:
                find t's first occurrence in target chunk
                expand to word boundaries
                clamp to 12–60 characters
                candidate = clamped substring
                break
1.7.3.     if candidate found:
              locatorKind = "string"  (or "regex" if contains `"`)
              locatorText = `"` + candidate + `"`
              skip to step 1.9
1.8.     Layer 3 — absolute (range string fallback):
1.8.1.     range := chunk.Range
1.8.2.     if range starts with `"` or `/`  (non-conforming
              chunker, soft contract violation):
              locatorKind = "bare"   // can't address uniquely
              locatorText = ""
              // workshop UI surfaces a warning
            else:
              locatorKind = "absolute"
              locatorText = range
1.9.     compute scope and dup counts:
1.9.1.     withinFileDupCount = (count of chunks in `path` sharing
              the same @id) - 1   // 0 means no dups
1.9.2.     resolve the proposed locator end-to-end to get the
              chunk set:
                target := baseValue + (locator-as-suffix)
                chunks := DB.ResolveExtTarget(target, "")
                crossFileScope.chunks = len(chunks)
                crossFileScope.files  = distinct fileIDs in chunks
1.10.    return LocatorSuggestion{
              Base, BaseValue, Locator, LocatorKind, LocatorText,
              WithinFileDupCount, CrossFileScope
           }
2.   Server bridge converts struct to Lua table; Lua viewdef
       receives the suggestion and binds it into the @ext
       authoring widget's preview state.
```

## Notes

Tokenization (1.6.2): splits on whitespace AND punctuation;
punctuation is kept as its own token where meaningful for
prefix uniqueness. Case-insensitive for the uniqueness compare,
but the emitted locator preserves the original case from the
target chunk.

Trigram extraction (1.7.1): the algorithm uses content trigrams
directly (no FTS index lookup needed) because the target file's
chunk set is small. For a file with N chunks averaging K
characters, the cost is O(N × K) — typical N=20, K=500, so ~10K
char comparisons per call.

Cross-file scope (1.9.2) calls back into `ResolveExtTarget`
with the proposed locator to get the same chunk set the real
resolver would return. For UUID bases this scans every chunk
with the matching `@id` across all files; for path bases the
scope is constrained to the one file.

Soft-contract degradation (1.8.2): when the range string starts
with `"` or `/`, no locator form can uniquely address the chunk
via `@ext`. The function returns `locatorKind = "bare"` and the
workshop UI flags the chunk visibly so the user knows their
authored `@ext` will route by base only (file preamble or all
`@id` chunks across files).
