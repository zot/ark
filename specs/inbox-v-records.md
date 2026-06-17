# Inbox from V Records

Language: Go. Environment: Linux CLI + HTTP server.

## Problem

`Inbox()` reads every file that has a `@status:` tag from disk
(`os.ReadFile` + `ParseTagBlock`) to extract tag values for filtering
and display. Most of these files are completed/denied messages that
get filtered out immediately. The V record index already has every
tag value mapped to fileids — the data is in the index, no disk reads
needed.

## Current flow

1. `TagFiles(["status"])` → all fileids with `@status:` tag
2. For each fileid: resolve path, read file from disk, parse tag block
3. Filter by status value, archived flag, `/requests/` path
4. Build InboxEntry from parsed tag values

## Proposed flow

1. Use V records to get candidate fileids and their tag values
   entirely from the index — no file reads.
2. Filter by status value, archived, and path from the index data.
3. Build InboxEntry from indexed tag values.

## New Store method

`FileTagValues(fileid uint64, tags []string) (map[string]string, error)`

For each requested tag name, scan V records with prefix
`V[tag]\x00` and check if fileid is in the varint list. Return
the first value found per tag (a file typically has one value per
tag). This is O(values-per-tag × tags) index reads per file.

For the inbox case with ~10 tags and small value sets, this is
fast. For tags with thousands of values it would be slow — but
messaging tags don't have that problem.

## Candidate set

Start from `TagFiles(["status"])` for the fileid+path set (unchanged).
Filter to `/requests/` paths. Then instead of `os.ReadFile` +
`ParseTagBlock`, call `FileTagValues` for the remaining tags.

Early exit: if `!showAll`, use `TagValueFiles("status", "completed")`
and `TagValueFiles("status", "denied")` to get fileids to skip
before the per-file tag lookup.

## Tags needed per entry

| Tag | Used for |
|-----|----------|
| status | filter + output |
| archived | filter |
| to-project | output |
| from-project | output |
| ark-request | output (requestID, kind) |
| ark-response | output (requestID, kind) |
| issue | output (summary) |
| response-handled | output |
| request-handled | output |
| status-date | output |

## Fallback

If a V record lookup returns no value for a tag that the file
actually has, the worst case is a missing field in InboxEntry —
not a crash. V records are rebuilt on `ark rebuild`.
