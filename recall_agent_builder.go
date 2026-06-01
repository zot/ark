package ark

// CRC: crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RecallAgentBuilder owns the curation-doc and result-doc builder
// state machines for the Simple Recall v2 pipeline. The watcher
// drives the Go-internal curation builder; the recall agent shells
// out to four CLI verbs (reserve-nonce, surface, recommend, close)
// that route through this component.
//
// All in-flight state lives in this process. tmp:// is per-process,
// so a server restart wipes the maps and counter cleanly — no
// persistence required.
//
// CRC: crc-RecallAgentBuilder.md | R2754, R2755, R2756, R2757, R2758, R2763, R2772
type RecallAgentBuilder struct {
	db *DB

	nonceCounter uint32 // R2755: in-memory monotonic, resets on restart

	mu        sync.Mutex
	curations map[uint64]*RecallCurationBuilder // keyed by fire
	results   map[uint64]*recallResultDoc       // keyed by fire

	// Monitoring log paths. Default to ~/.ark/monitoring/ but can
	// be overridden for testing.
	monitorPath string
	fumblePath  string
	// curationDir is where `next` materializes the per-fire curation doc
	// as a real file the secretary Reads (default ~/.ark/recall-curation).
	// R2896
	curationDir string
	logMu       sync.Mutex // serializes appends across both files
}

// NewRecallAgentBuilder constructs a builder anchored at db.
// CRC: crc-RecallAgentBuilder.md
func NewRecallAgentBuilder(db *DB) *RecallAgentBuilder {
	home, _ := os.UserHomeDir()
	monDir := filepath.Join(home, ".ark", "monitoring")
	return &RecallAgentBuilder{
		db:          db,
		curations:   make(map[uint64]*RecallCurationBuilder),
		results:     make(map[uint64]*recallResultDoc),
		monitorPath: filepath.Join(monDir, "recall.jsonl"),
		fumblePath:  filepath.Join(monDir, "recall-fumbles.jsonl"),
		curationDir: filepath.Join(home, ".ark", "recall-curation"),
	}
}

// ReserveNonce returns the next monotonic nonce for tagging a recall
// agent's Task description. CRC: crc-RecallAgentBuilder.md | R2755
func (b *RecallAgentBuilder) ReserveNonce() uint32 {
	return atomic.AddUint32(&b.nonceCounter, 1)
}

// --- curation-doc builder (Go-internal, called by watcher) ---

// RecallCurationBuilder buffers the body of one curation doc and
// writes it via the DB write actor on Close. R2754
type RecallCurationBuilder struct {
	parent  *RecallAgentBuilder
	session string
	fire    uint64

	buf      strings.Builder
	sections int
}

// RecallCurationOpen returns a builder for the given (session, fire).
// CRC: crc-RecallAgentBuilder.md | R2754
func (b *RecallAgentBuilder) RecallCurationOpen(session string, fire uint64) *RecallCurationBuilder {
	cb := &RecallCurationBuilder{
		parent:  b,
		session: session,
		fire:    fire,
	}
	// R2748: head-of-chunk tags, no blank line before first section.
	fmt.Fprintf(&cb.buf, "@ark-recall-curate: %s\n", session)
	fmt.Fprintf(&cb.buf, "@ark-recall-fire: %d\n", fire)
	b.mu.Lock()
	b.curations[fire] = cb
	b.mu.Unlock()
	return cb
}

// Section opens a new `# Source Chunk:` H1 with its blockquoted
// paragraph excerpt. R2749
func (c *RecallCurationBuilder) Section(sourceChunkID uint64, paragraph string) {
	c.sections++
	excerpt := truncateUTF8(paragraph, 200)
	fmt.Fprintf(&c.buf, "\n# Source Chunk: %d\n\n> %s\n", sourceChunkID, blockquoteEscape(excerpt))
}

// Candidate appends one `## Candidate:` H2 with its score, tag list,
// optional proposed-tag scores, and fenced content excerpt. R2749
//
// byteSize is the full chunk byte length (pre-truncation), stamped
// after the chunkID so the agent can factor fetch cost into its
// surface judgment — markdown nested-list explosions and bracket-go
// function-body chunks can reach tens of kB.
//
// proposed is a parallel pair of (name, score) slices; both may be
// nil/empty to suppress the line entirely.
func (c *RecallCurationBuilder) Candidate(
	chunkID uint64,
	path, rangeLabel string,
	byteSize int,
	score float64,
	tagNames []string,
	proposedNames []string,
	proposedScores []float64,
	contentExcerpt string,
	tagOnly bool,
) {
	fmt.Fprintf(&c.buf, "\n## Candidate: %d (%s) %s:%s\n\n", chunkID, friendlySize(byteSize), path, rangeLabel)
	fmt.Fprintf(&c.buf, "- score: %.2f\n", score)
	fmt.Fprintf(&c.buf, "- tags: %s\n", strings.Join(tagNames, ", "))
	if len(proposedNames) > 0 {
		parts := make([]string, 0, len(proposedNames))
		for i, name := range proposedNames {
			s := 0.0
			if i < len(proposedScores) {
				s = proposedScores[i]
			}
			parts = append(parts, fmt.Sprintf("%s (%.2f)", name, s))
		}
		fmt.Fprintf(&c.buf, "- proposed-tags: %s\n", strings.Join(parts, ", "))
	}
	// tag-only marks an own-session candidate: the agent may recommend a
	// tag for it but must never surface it (the reader's own conversation
	// is already in context). CRC: crc-RecallAgentBuilder.md | R2869
	if tagOnly {
		fmt.Fprintf(&c.buf, "- tag-only: true\n")
	}
	if contentExcerpt != "" {
		fmt.Fprintf(&c.buf, "\n```\n%s\n```\n", truncateUTF8(contentExcerpt, 500))
	}
}

// Sections returns the number of `# Source Chunk:` H1s emitted so
// far. The watcher checks this before calling Close so it can drop
// the builder silently when nothing cleared the gate.
func (c *RecallCurationBuilder) Sections() int { return c.sections }

// Close writes tmp://ARK-RECALL/curation-<session>-<fire> via the
// DB write actor. The builder's registry entry in
// `curations[fire]` is NOT deleted here — it stays alive as the
// fire-registry so the agent's later surface/recommend calls can
// resolve the session via openResult. CloseResult (the single
// cleanup verb) deletes both maps atomically.
// CRC: crc-RecallAgentBuilder.md | R2747, R2748
func (c *RecallCurationBuilder) Close() error {
	path := curationDocPath(c.session, c.fire)
	body := []byte(c.buf.String())
	return SyncVoid(c.parent.db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", body)
		return e
	})
}

// --- result-doc builder (CLI-driven, called by recall agent) ---

// recallResultDoc buffers the body of one result doc. Built up by
// SurfaceItem / RecommendItem; flushed on Close.
type recallResultDoc struct {
	session string
	buf     strings.Builder
	items   int
	opened  time.Time
}

// SurfaceItem appends a `## Surface:` H2 to the result-doc builder
// for the given fire, opening it on first call. The session is
// derived from the in-flight curation doc; an unknown fire returns
// an error so callers see the lost-state failure rather than
// silently producing a misaligned result.
//
// The Surface H2 carries the chunkID, a friendly byte-size label
// (so the assistant can gauge fetch cost — some chunks are tens of
// kB, e.g. PLAN.md's 33K range — and may skip the giants), and the
// chunk's path:range (via chunkLocator) so the consuming assistant
// can prune by file path without resolving every chunk. Full
// content stays on-demand via `ark chunks <chunkID>`.
// CRC: crc-RecallAgentBuilder.md | R2751, R2756, R2872
func (b *RecallAgentBuilder) SurfaceItem(fire uint64, chunkID uint64, reason string) error {
	if reason == "" {
		return fmt.Errorf("reason required")
	}
	doc, err := b.openResult(fire)
	if err != nil {
		return err
	}
	// R2872: never surface a chunk in the reader's own session — it's a
	// `# Source Chunk:` / conversation paragraph already in context, the
	// redundant self-echo recall exists to avoid. The error names the fix,
	// doubling as fumble-onboarding. (recommend is NOT gated — own-session
	// tagging is the intended hypergraph path.)
	if info, e := b.db.ChunkInfo(chunkID); e == nil && sessionFromJSONLPath(info.Path) == doc.session {
		return fmt.Errorf("chunk %d is in the reader's own session (a `# Source Chunk:` conversation chunk, already in context) — surface a `## Candidate:` chunkid instead, never the source id", chunkID)
	}
	loc, sizeLabel := b.chunkLocator(chunkID, true)
	fmt.Fprintf(&doc.buf, "\n## Surface: %d (%s)%s\n\nreason: %s\n", chunkID, sizeLabel, loc, reason)
	doc.items++
	// R2894: start the surface-cooldown clock for (session, chunk) so the
	// watcher floor (R2893) will not re-offer it within the window.
	if b.db != nil {
		_ = b.db.MarkSurfaced(doc.session, chunkID)
	}
	return nil
}

// friendlySize formats a byte count for the result-doc Surface
// header. Small chunks show as bare bytes; >= 1KB rounds to KB;
// >= 1MB shows one decimal. Decimal base (1000), not binary —
// matches the way humans naturally read sizes ("33K", not
// "32.6Ki").
func friendlySize(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%db", n)
	case n < 1_000_000:
		return fmt.Sprintf("%dK", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// chunkLocator resolves a chunk's " <path>:<range>" suffix (leading
// space; empty string when the chunk can't be resolved) so both
// result-doc H2 kinds can carry the path — the consuming assistant
// prunes surfaces/recommends by file without resolving every chunk
// (R2751). When withSize is true it also returns the friendly
// byte-size label ("?" if unresolved); Recommend passes false to
// skip the chunk-text read it doesn't need.
// CRC: crc-RecallAgentBuilder.md | R2751
func (b *RecallAgentBuilder) chunkLocator(chunkID uint64, withSize bool) (loc, size string) {
	size = "?"
	info, err := b.db.ChunkInfo(chunkID)
	if err != nil {
		return "", size
	}
	loc = " " + info.Path + ":" + info.Range
	if withSize {
		if text := b.db.ChunkText(info.Path, info.Range); text != nil {
			size = friendlySize(len(text))
		}
	}
	return loc, size
}

// RecommendItem appends a `## Recommend:` H2 to the result-doc
// builder for the given fire, opening it on first call. Session is
// derived from the in-flight curation doc (see SurfaceItem).
// CRC: crc-RecallAgentBuilder.md | R2751, R2757
func (b *RecallAgentBuilder) RecommendItem(fire uint64, chunkID uint64, tagSpec, reason string) error {
	if tagSpec == "" {
		return fmt.Errorf("tag required")
	}
	if reason == "" {
		return fmt.Errorf("reason required")
	}
	doc, err := b.openResult(fire)
	if err != nil {
		return err
	}
	loc, _ := b.chunkLocator(chunkID, false)
	fmt.Fprintf(&doc.buf, "\n## Recommend: %s on %d%s\n\nreason: %s\n", tagSpec, chunkID, loc, reason)
	doc.items++
	return nil
}

// CloseResult is the single cleanup verb. Writes the result doc iff
// items were added; removes the curation doc unless preserveCuration;
// discovers the calling subagent's JSONL via the nonce → meta.json
// lookup; sums tokens; appends one record to recall.jsonl.
// CRC: crc-RecallAgentBuilder.md | Seq: seq-subscriber-presence.md | R2750, R2751, R2758, R2762, R2807, R2808
func (b *RecallAgentBuilder) CloseResult(fire uint64, nonce uint32, preserveCuration bool) error {
	b.mu.Lock()
	doc := b.results[fire]
	cb := b.curations[fire]
	delete(b.results, fire)
	delete(b.curations, fire)
	b.mu.Unlock()

	session := ""
	if doc != nil {
		session = doc.session
	} else if cb != nil {
		session = cb.session
	}
	if session == "" {
		// We don't know the session — the fire was never opened on
		// this server. Fall back to writing the monitor log only.
		b.appendMonitor(fire, "", nonce, 0, 0, 0, 0, 0, 0, "error")
		return fmt.Errorf("unknown fire %d", fire)
	}

	outcome := "silent-close"
	surfaced, recommended := 0, 0
	if doc != nil && doc.items > 0 {
		// R2807: subscriber-presence gate. Skip the result-doc write
		// when no listener for this session's result events. Cleanup
		// (curation removal, orphan sweep, monitor append) still runs.
		if b.db != nil && b.db.pubsub != nil &&
			b.db.pubsub.SubscriberCount("ark-recall-result", session) == 0 {
			outcome = "no-subscriber" // R2808
			surfaced, recommended = countItems(doc.buf.String())
		} else {
			// R2750, R2751: result-doc head tag + Surface/Recommend H2 body.
			header := fmt.Sprintf("@ark-recall-result: %s\n", session)
			body := []byte(header + doc.buf.String())
			path := resultDocPath(session, fire)
			if err := SyncVoid(b.db, func(db *DB) error {
				_, e := db.AddTmpFile(path, "markdown", body)
				return e
			}); err != nil {
				b.appendMonitor(fire, session, nonce, 0, 0, 0, 0, 0, 0, "error")
				return fmt.Errorf("write result doc: %w", err)
			}
			outcome = "result-emitted"
			surfaced, recommended = countItems(doc.buf.String())
		}
	}

	if !preserveCuration {
		curPath := curationDocPath(session, fire)
		_ = SyncVoid(b.db, func(db *DB) error {
			return db.RemoveTmpFile(curPath)
		})
		// Remove the materialized curation file the secretary Read (R2896).
		_ = os.Remove(b.curationFilePath(session, fire))
		// Sweep orphan curation docs for the same session (older
		// fires the assistant missed handling). Same-session scope
		// protects multi-session deployments from cross-cleanup.
		b.sweepSessionOrphans(session, fire)
	}

	in, out := b.lookupSubagentTokens(nonce)
	contextTokens, _ := b.ContextTokens(nonce)
	latency := 0
	if doc != nil && !doc.opened.IsZero() {
		latency = int(time.Since(doc.opened).Milliseconds())
	}
	b.appendMonitor(fire, session, nonce, in, out, contextTokens, latency, surfaced, recommended, outcome)
	return nil
}

// openResult returns the per-fire result-doc builder, allocating
// on first call. The session is derived from the in-flight curation
// doc; an unknown fire returns an error.
func (b *RecallAgentBuilder) openResult(fire uint64) (*recallResultDoc, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d, ok := b.results[fire]; ok {
		return d, nil
	}
	cb, ok := b.curations[fire]
	if !ok {
		return nil, fmt.Errorf("unknown fire %d (curation doc not in flight)", fire)
	}
	d := &recallResultDoc{session: cb.session, opened: time.Now()}
	b.results[fire] = d
	return d, nil
}

// sweepSessionOrphans removes any tmp://ARK-RECALL/curation-<session>-<F>
// docs whose fire number is less than the closing fire. These are
// older fires the assistant missed handling — opportunistic cleanup
// so the orphan list doesn't grow unbounded across a long session.
//
// Same-session scope: another session's orphans are not touched
// (multi-session safety). The closing fire's own doc is removed by
// the explicit RemoveTmpFile call above this; this sweep only
// addresses prior fires.
//
// CRC: crc-RecallAgentBuilder.md | R2758
func (b *RecallAgentBuilder) sweepSessionOrphans(session string, currentFire uint64) {
	prefix := fmt.Sprintf("tmp://ARK-RECALL/curation-%s-", session)
	var stale []string
	for _, path := range b.db.TmpFiles() {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(path, prefix)
		fire, err := strconv.ParseUint(suffix, 10, 64)
		if err != nil || fire >= currentFire {
			continue
		}
		stale = append(stale, path)
	}
	if len(stale) == 0 {
		return
	}
	_ = SyncVoid(b.db, func(db *DB) error {
		for _, p := range stale {
			_ = db.RemoveTmpFile(p)
		}
		return nil
	})
}

// --- subagent JSONL discovery + token sum ---

// lookupSubagentTokens walks the assistant's subagents directory
// for a .meta.json whose description contains "nonce <N>", then
// sums usage.input_tokens / usage.output_tokens from the paired
// JSONL. Returns (0, 0) when discovery fails — close never errors
// on missing tokens.
//
// Source enumeration replaces the older cwd-derived path: the
// agent's cwd isn't reliable, but ark already knows which
// `~/.claude/projects/<encoded>` directories are watched (the
// chat-jsonl sources). We glob under each one for the parent
// session's subagents directory.
//
// meta.json files persist across `ark serve` restarts, but the
// nonce counter resets — so a stale meta.json from a previous
// run could collide on the same `nonce <N>` substring. To resolve
// that, candidates are checked in JSONL-mtime descending order:
// the just-spawned subagent's transcript is the most recently
// modified file, so it wins any ambiguity.
//
// CRC: crc-RecallAgentBuilder.md | R2759, R2760, R2761
func (b *RecallAgentBuilder) lookupSubagentTokens(nonce uint32) (in, out int) {
	jsonl := b.findSubagentJSONL(nonce)
	if jsonl == "" {
		return 0, 0
	}
	return sumSubagentTokens(jsonl)
}

// findSubagentJSONL resolves a nonce to the calling subagent's
// transcript JSONL path. Returns "" when discovery fails. Shared
// helper for token sums (close-time) and live context inspection
// (the lotto-tube agent's self-recycle check).
//
// Strategy: enumerate ark sources under ~/.claude/projects, look
// under each `<source>/<parent_session>/subagents/` for .meta.json
// files, take JSONL-mtime-descending order (freshest wins to
// resolve cross-restart nonce collisions), match the first whose
// description contains "nonce <N>".
//
// CRC: crc-RecallAgentBuilder.md | R2759, R2760
func (b *RecallAgentBuilder) findSubagentJSONL(nonce uint32) string {
	parent := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if parent == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	projectsPrefix := filepath.Join(home, ".claude", "projects") + string(os.PathSeparator)
	needle := fmt.Sprintf("nonce %d", nonce)
	type candidate struct {
		meta  string
		mtime time.Time
	}
	var cands []candidate
	for _, src := range b.db.Config().Sources {
		if !strings.HasPrefix(src.Dir, projectsPrefix) {
			continue
		}
		dir := filepath.Join(src.Dir, parent, "subagents")
		matches, _ := filepath.Glob(filepath.Join(dir, "*.meta.json"))
		for _, meta := range matches {
			jsonl := strings.TrimSuffix(meta, ".meta.json") + ".jsonl"
			info, err := os.Stat(jsonl)
			if err != nil {
				continue
			}
			cands = append(cands, candidate{meta: meta, mtime: info.ModTime()})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].mtime.After(cands[j].mtime)
	})
	for _, c := range cands {
		data, err := os.ReadFile(c.meta)
		if err != nil {
			continue
		}
		var doc struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}
		if !strings.Contains(doc.Description, needle) {
			continue
		}
		return strings.TrimSuffix(c.meta, ".meta.json") + ".jsonl"
	}
	return ""
}

// ContextTokens returns the subagent's current context fill,
// computed as `cache_creation_input_tokens + cache_read_input_tokens`
// from the most recent assistant record in the JSONL that carries a
// usage object. That sum represents the cumulative input the model
// just loaded — what Claude Code's status indicator reads off. Used
// by the lotto-tube recall agent (Phase 2) to self-recycle when its
// context grows past a configurable limit.
//
// Returns (0, false) when discovery or parse fails so callers can
// distinguish "couldn't measure" from a real 0.
// CRC: crc-RecallAgentBuilder.md | R2777
func (b *RecallAgentBuilder) ContextTokens(nonce uint32) (int, bool) {
	jsonl := b.findSubagentJSONL(nonce)
	if jsonl == "" {
		return 0, false
	}
	data, err := os.ReadFile(jsonl)
	if err != nil {
		return 0, false
	}
	// Scan from end so we find the latest assistant turn fast.
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					CacheCreation int `json:"cache_creation_input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" {
			continue
		}
		total := rec.Message.Usage.CacheCreation + rec.Message.Usage.CacheRead
		if total == 0 {
			// Some assistant records carry no usage block (e.g.,
			// tool-use-only intermediate records). Keep scanning.
			continue
		}
		return total, true
	}
	return 0, false
}

// sumSubagentTokens reads a subagent JSONL transcript and totals
// usage.input_tokens / usage.output_tokens across "type":"assistant"
// records. R2761, R2762
func sumSubagentTokens(jsonl string) (in, out int) {
	data, err := os.ReadFile(jsonl)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					Input  int `json:"input_tokens"`
					Output int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" {
			continue
		}
		in += rec.Message.Usage.Input
		out += rec.Message.Usage.Output
	}
	return in, out
}

// --- monitor + fumble logs ---

// monitorRecord is one line in recall.jsonl. Field order matches
// the spec table for diagnostic-friendliness.
//
// ContextTokens is the agent's cumulative context at close time
// (cache_creation_input_tokens + cache_read_input_tokens from the
// last assistant record). In v2 one-shot fires this is roughly
// per-fire static; in Phase 2 lotto-tube runs it's the load-
// bearing signal showing context creep across fires until the
// agent self-recycles.
type monitorRecord struct {
	Fire          uint64 `json:"fire"`
	Session       string `json:"session"`
	Nonce         uint32 `json:"nonce"`
	InTokens      int    `json:"in_tokens"`
	OutTokens     int    `json:"out_tokens"`
	ContextTokens int    `json:"context_tokens"`
	LatencyMs     int    `json:"latency_ms"`
	Surfaced      int    `json:"surfaced"`
	Recommended   int    `json:"recommended"`
	Outcome       string `json:"outcome"`
	Timestamp     string `json:"timestamp"`
}

func (b *RecallAgentBuilder) appendMonitor(fire uint64, session string, nonce uint32, in, out, context, latency, surfaced, recommended int, outcome string) {
	rec := monitorRecord{
		Fire: fire, Session: session, Nonce: nonce,
		InTokens: in, OutTokens: out, ContextTokens: context,
		LatencyMs: latency,
		Surfaced:  surfaced, Recommended: recommended,
		Outcome: outcome, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	b.appendJSONL(b.monitorPath, rec)
}

// LogFumble appends one entry to the Fumble Log. Called by the CLI
// flag-parser on malformed surface/recommend/close invocations.
// R2772
func (b *RecallAgentBuilder) LogFumble(fire uint64, nonce uint32, command, args, errMsg string) {
	rec := struct {
		Timestamp string `json:"timestamp"`
		Fire      uint64 `json:"fire"`
		Nonce     uint32 `json:"nonce"`
		Command   string `json:"command"`
		Args      string `json:"args"`
		Error     string `json:"error"`
	}{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Fire:      fire, Nonce: nonce,
		Command: command, Args: args, Error: errMsg,
	}
	b.appendJSONL(b.fumblePath, rec)
}

// appendJSONL serializes one record and appends a single line to
// path. Errors are swallowed — monitoring is best-effort, never on
// the critical path.
func (b *RecallAgentBuilder) appendJSONL(path string, rec any) {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(rec) // Encode writes the trailing newline.
}

// --- helpers ---

// curationDocPath is the canonical tmp:// path for a curation doc.
// R2747
func curationDocPath(session string, fire uint64) string {
	return fmt.Sprintf("tmp://ARK-RECALL/curation-%s-%d", session, fire)
}

// curationFilePath is the real-filesystem path where `next` materializes
// a curation doc for the secretary to Read (R2896). One keyhole the guard
// opens for the otherwise Read-denied secretary.
// CRC: crc-RecallAgentBuilder.md | R2896
func (b *RecallAgentBuilder) curationFilePath(session string, fire uint64) string {
	return filepath.Join(b.curationDir, fmt.Sprintf("curation-%s-%d.md", session, fire))
}

// writeCurationFile materializes the (conversation-injected) curation doc
// content to curationFilePath so `next` can return a short pointer instead
// of the large doc inline — keeping it off the agent's foreground-Bash
// stdout, which the harness truncates. The secretary Reads the file (the
// Read tool paginates; `cat` would re-overflow). R2896
// CRC: crc-RecallAgentBuilder.md | R2896
func (b *RecallAgentBuilder) writeCurationFile(session string, fire uint64, content string) (string, error) {
	if err := os.MkdirAll(b.curationDir, 0o755); err != nil {
		return "", err
	}
	path := b.curationFilePath(session, fire)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// resultDocPath is the canonical tmp:// path for a result doc. R2747
func resultDocPath(session string, fire uint64) string {
	return fmt.Sprintf("tmp://ARK-RECALL/result-%s-%d", session, fire)
}

// blockquoteEscape collapses internal newlines so a multi-line
// excerpt stays on one blockquoted line. Markdown renderers vary
// in how they handle wrapped blockquotes; one line is the simple
// invariant. R2749
func blockquoteEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
}

// countItems totals the `## Surface:` and `## Recommend:` H2s in a
// result-doc body. Cheap line scan, used for the monitor record.
func countItems(body string) (surfaced, recommended int) {
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "## Surface:"):
			surfaced++
		case strings.HasPrefix(line, "## Recommend:"):
			recommended++
		}
	}
	return surfaced, recommended
}
