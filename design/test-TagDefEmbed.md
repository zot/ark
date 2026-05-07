# Test Design: Tag-Definition Embeddings (ED records)
**Source:** crc-Store.md, crc-Librarian.md, specs/tag-def-embeddings.md

## Test: write and read ED record
**Purpose:** validate ED key/value layout matches D's (tag, fileid) shape
**Input:** open Store, call WriteTagDefEmbedding("decision", fileid=42, vec3072)
**Expected:** ReadTagDefEmbedding("decision", 42) returns the same vector;
ReadTagDefEmbedding("decision", 99) returns (nil, nil)
**Refs:** R2151, R2153, R2159

## Test: MissingTagDefEmbeddings finds D without ED
**Purpose:** batch-embed pass discovers unembedded tag defs
**Input:** UpdateTagDefs(fileid=10, {"decision":"choosing tools"});
no ED writes
**Expected:** MissingTagDefEmbeddings() returns [{tag:"decision",
fileid:10}]. After WriteTagDefEmbedding for that pair, the next
MissingTagDefEmbeddings() returns []
**Refs:** R2157

## Test: UpdateTagDefs drops fileid's ED records in same txn
**Purpose:** ED lifecycle mirrors D — replacing D drops the matching ED
**Input:** UpdateTagDefs(fileid=10, {"a":"x","b":"y"}); write ED for
(a,10) and (b,10); UpdateTagDefs(fileid=10, {"c":"z"})
**Expected:** ReadTagDefEmbedding("a",10) and ("b",10) both return
(nil, nil). MissingTagDefEmbeddings() returns [{tag:"c",fileid:10}].
**Refs:** R2154

## Test: RemoveTagDefs drops ED with D
**Purpose:** file removal removes both D and ED
**Input:** UpdateTagDefs + WriteTagDefEmbedding for (tag,fileid);
RemoveTagDefs(fileid)
**Expected:** ReadTagDefEmbedding returns (nil, nil); D records gone;
MissingTagDefEmbeddings does not include the removed pair.
**Refs:** R2155

## Test: AppendTagDefs leaves existing ED intact
**Purpose:** append path adds D without disturbing ED for unchanged tags
**Input:** UpdateTagDefs(fileid=10, {"a":"x"}); WriteTagDefEmbedding for
(a,10); AppendTagDefs(fileid=10, {"b":"y"})
**Expected:** ReadTagDefEmbedding("a",10) still returns the original
vector; MissingTagDefEmbeddings() returns [{tag:"b",fileid:10}].
**Refs:** R2156

## Test: DropEmbeddings clears all ED records
**Purpose:** model swap drops T-name, EV, and ED together
**Input:** populate T-name, EV, and ED; call DropEmbeddings()
**Expected:** all ED records gone; T-name vectors stripped (counts
remain); EV records gone.
**Refs:** R2160

## Test: rebuild regenerates ED from D
**Purpose:** ark rebuild path produces ED records for all D records
**Input:** index a corpus with tag defs, run rebuild, run batch-embed pass
**Expected:** every D record has a matching ED record;
MissingTagDefEmbeddings() returns [] after the pass.
**Refs:** R2161

## Test: Librarian batch-embed writes ED for missing pairs
**Purpose:** end-to-end — D records become ED records after the pass
**Input:** UpdateTagDefs writes D records; run BatchEmbed()
**Expected:** every (tag, fileid) in MissingTagDefEmbeddings before the
call has an ED record after; embed text passed to model is the
description alone (no tag name prefix).
**Refs:** R2152, R2158

## Test: status -db lists ED prefix
**Purpose:** prefix inventory includes ED
**Input:** populate ED records; run `ark status -db`
**Expected:** output includes a line for ED with the correct count.
**Refs:** R2162
