# Librarian
**Requirements:** R1235, R1236, R1237, R1238, R1239, R1240, R1241, R1242, R1243, R1244, R1245, R1246, R1247, R1248, R1249, R1250, R1251, R1252, R1253, R1254, R1268, R1269, R1270, R1271, R1272, R1273

Closure actor managing Haiku-powered query expansion. Runs a
three-step pipeline: expand (Haiku suggests alternatives) →
search (fuzzy match against V records) → curate (Haiku prunes
results). Each Haiku interaction is a `claude --print` invocation
with `--resume` for session persistence. The actor serializes
access from concurrent HTTP handlers.

## Knows
- ch: chan func(*librarianState) — actor message channel
- available: bool — whether `claude` was found on PATH at startup
- db: *DB — for V record queries and search during pipeline

### librarianState (actor-private)
- sessionID: string — claude session ID for conversation persistence
- promptFile: string — path to ~/.ark/searching/CLAUDE.md
- timer: *time.Timer — TTL countdown, reset on each request
- ttl: time.Duration — idle timeout before expiring the session

## Does
- ExpandTags(tag, value string) ([]GroupedResult, error): run the
  full tag expansion pipeline:
  1. callClaude() with tag+value → parse expanded alternatives
  2. fuzzyMatchTags() against V records
  3. callClaude() with numbered match list → parse selected numbers
  4. Fetch search results for curated tags
  Returns results marked with source: "expansion".
- callClaude(prompt string) (string, error): spawn `claude --print
  --model haiku --output-format json --system-prompt-file <promptFile>
  --tools ""`, with `--resume <sessionID>` if session exists. Parse
  JSON result, store session_id for next call. Internal to actor.
- fuzzyMatchTags(alternatives []TagAlt) []TagMatch: for each
  alternative, scan V records with fuzzy trigram comparison.
  Returns (tag, value, count, score) tuples above threshold.
- handleTTL(): timer fires — clear sessionID. Next ExpandTags
  starts a fresh conversation.
- loop(): actor goroutine. Reads from ch, executes closures
  against state. Handles TTL timer channel.
- Available() bool: returns whether spectral search is possible
  (claude found on PATH). Non-blocking, reads a fixed value.

## Collaborators
- Server: owns the Librarian, creates at startup, calls ExpandTags
  from handleSearchExpand, calls Available for status endpoint
- Store: V record queries for fuzzy match step
- Searcher: fetch results for curated tags in final step
- claude CLI: spawned per pipeline step, session persisted via ID

## Sequences
- seq-spectral-expand.md
