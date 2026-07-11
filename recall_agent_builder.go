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
// CRC: crc-RecallAgentBuilder.md | R2754, R2755, R2900, R2757, R2758, R2763, R2772
type RecallAgentBuilder struct {
	db *DB

	nonceCounter uint32 // R2755: in-memory monotonic, resets on restart

	mu        sync.Mutex
	curations map[string]*RecallCurationBuilder // keyed by fire token <session>-<fire> (R2901)
	results   map[string]*recallResultDoc       // keyed by fire token <session>-<fire> (R2901)
	// Bloodhound (directed search) in-flight state, keyed by the kind-marked
	// cookie <session>-b<B> so it never collides with a recall fire token. The
	// ARK-BLOODHOUND namespace + these separate maps are what keep the two
	// independent per-session counters apart. R2937, R2943, R2946
	bloodhounds     map[string]*recallResultDoc // open finding-doc builders
	bloodhoundClues map[string]string           // cookie -> originating clue

	// CLI-bloodhound (external-CLI directed hunt) in-flight state, both keyed by
	// the request <id> (the bare cookie the watcher put in the enhanced task doc).
	// Two accumulators for two stages: cliHunts is the pool secretary's raw
	// findings, flushed to the request doc at close (R3025); cliResults is
	// Luhmann's curated JSONL, flushed to the result doc at `add --done` (R3027).
	cliHunts   map[string]*recallResultDoc // R3025: secretary raw findings -> request doc
	cliResults map[string]*recallResultDoc // R3027: Luhmann curated JSONL -> result doc

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
		db:              db,
		curations:       make(map[string]*RecallCurationBuilder),
		results:         make(map[string]*recallResultDoc),
		bloodhounds:     make(map[string]*recallResultDoc),
		bloodhoundClues: make(map[string]string),
		cliHunts:        make(map[string]*recallResultDoc),
		cliResults:      make(map[string]*recallResultDoc),
		monitorPath:     filepath.Join(monDir, "recall.jsonl"),
		fumblePath:      filepath.Join(monDir, "recall-fumbles.jsonl"),
		curationDir:     filepath.Join(home, ".ark", "recall-curation"),
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
	fmt.Fprintf(&cb.buf, "@ark-secretary-work: %s\n", session)
	fmt.Fprintf(&cb.buf, "@ark-recall-fire: %d\n", fire)
	b.mu.Lock()
	b.curations[fireToken(session, fire)] = cb
	b.mu.Unlock()
	return cb
}

// Section opens a new `# Source:` H1 with its blockquoted source
// paragraph excerpt; the heading carries the source chunk's
// path:range, no chunkid. R2898
func (c *RecallCurationBuilder) Section(sourcePath, sourceRange, paragraph string) {
	c.sections++
	excerpt := truncateUTF8(paragraph, 200)
	fmt.Fprintf(&c.buf, "\n# Source: %s:%s\n\n> %s\n", sourcePath, sourceRange, blockquoteEscape(excerpt))
}

// Candidate appends one `## Candidate:` H2 with its score, tag list,
// optional proposed-tag scores, and fenced content excerpt. The H2
// leads with the candidate's `<path>:<range>` locator, no chunkid. R2898
//
// byteSize is the full chunk byte length (pre-truncation), stamped
// after the locator so the agent can factor fetch cost into its
// surface judgment — markdown nested-list explosions and bracket-go
// function-body chunks can reach tens of kB.
//
// proposed is a parallel pair of (name, score) slices; both may be
// nil/empty to suppress the line entirely.
func (c *RecallCurationBuilder) Candidate(
	path, rangeLabel string,
	byteSize int,
	score float64,
	cell string,
	sub ChunkSubstrate,
	tagNames []string,
	proposedNames []string,
	proposedScores []float64,
	contentExcerpt string,
	tagOnly bool,
) {
	fmt.Fprintf(&c.buf, "\n## Candidate: %s:%s (%s)\n\n", path, rangeLabel, friendlySize(byteSize))
	fmt.Fprintf(&c.buf, "- score: %.2f\n", score)
	// Per-result originating cell + per-component scores, logged for
	// data-driven tuning of the 2×2 allocation. R2909
	if cell != "" {
		fmt.Fprintf(&c.buf, "- cell: %s\n", cell)
	}
	fmt.Fprintf(&c.buf, "- evidence: text-vec=%.2f text-tri=%.2f tag-vec=%.2f tag-tri=%.2f\n",
		sub.VectorEC, sub.TrigramEC, sub.TagVector, sub.TagTrigram)
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
// for the given fire token, opening it on first call. The session is
// derived from the in-flight curation doc; an unknown token returns
// an error so callers see the lost-state failure rather than
// silently producing a misaligned result.
//
// The Surface H2 leads with the candidate's `<path>:<range>` locator
// (R2899) and a friendly byte-size label (so the assistant can gauge
// fetch cost — some chunks are tens of kB, e.g. PLAN.md's 33K range —
// and may skip the giants) so it can prune by file path without
// resolving every chunk. Full content stays on-demand via
// `ark chunks <path>:<range>`.
// CRC: crc-RecallAgentBuilder.md | R2872, R2899, R2900
func (b *RecallAgentBuilder) SurfaceItem(fireToken string, loc, reason string) error {
	if reason == "" {
		return fmt.Errorf("reason required")
	}
	doc, err := b.openResult(fireToken)
	if err != nil {
		return err
	}
	path, rangeLabel := splitLoc(loc)
	// R2872: never surface a chunk in the reader's own session — it's a
	// `# Source:` / conversation paragraph already in context, the
	// redundant self-echo recall exists to avoid. The error names the fix,
	// doubling as fumble-onboarding. (recommend is NOT gated — own-session
	// tagging is the intended hypergraph path.)
	if sessionFromJSONLPath(path) == doc.session {
		return fmt.Errorf("loc %s is in the reader's own session (a `# Source:` conversation chunk, already in context) — surface a `## Candidate:` locator instead, never the source", loc)
	}
	sizeLabel := "?"
	if text := b.db.ChunkText(path, rangeLabel); text != nil {
		sizeLabel = friendlySize(len(text))
	}
	// R2899: result-doc Surface H2 leads with the path:range locator and a
	// friendly size; no chunkid.
	fmt.Fprintf(&doc.buf, "\n## Surface: %s:%s (%s)\n\nreason: %s\n", path, rangeLabel, sizeLabel, reason)
	doc.items++
	// R2894: start the surface-cooldown clock for (session, chunk) so the
	// watcher floor (R2893) will not re-offer it within the window. The RM
	// record is chunkid-keyed; resolve the loc → chunkID just-in-time
	// (best-effort — a miss merely forgoes the cooldown for this surface).
	if b.db != nil {
		if cid, ok := b.chunkIDForLoc(path, rangeLabel); ok {
			_ = b.db.MarkSurfaced(doc.session, cid)
		}
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

// RecommendItem appends a `## Recommend:` H2 to the result-doc
// builder for the given fire, opening it on first call. Session is
// derived from the in-flight curation doc (see SurfaceItem).
// CRC: crc-RecallAgentBuilder.md | R2757, R2899, R2900
func (b *RecallAgentBuilder) RecommendItem(fireToken string, loc, tagSpec, reason string) error {
	if tagSpec == "" {
		return fmt.Errorf("tag required")
	}
	if reason == "" {
		return fmt.Errorf("reason required")
	}
	doc, err := b.openResult(fireToken)
	if err != nil {
		return err
	}
	path, rangeLabel := splitLoc(loc)
	// R2899: result-doc Recommend H2 references the chunk by path:range.
	fmt.Fprintf(&doc.buf, "\n## Recommend: %s on %s:%s\n\nreason: %s\n", tagSpec, path, rangeLabel, reason)
	doc.items++
	return nil
}

// CloseResult is the single cleanup verb. Writes the result doc iff
// items were added; removes the curation doc unless preserveCuration;
// discovers the calling subagent's JSONL via the nonce → meta.json
// lookup; sums tokens; appends one record to recall.jsonl.
// CRC: crc-RecallAgentBuilder.md | Seq: seq-subscriber-presence.md | R2750, R2899, R2758, R2762, R2807, R2808
func (b *RecallAgentBuilder) CloseResult(fireToken string, nonce uint32, preserveCuration bool) error {
	// A kind-marked bloodhound cookie (<session>-b<B>) routes to the
	// directed-search close in its own namespace. R2945
	if session, bid, ok := parseBloodhoundToken(fireToken); ok {
		return b.closeBloodhound(fireToken, session, bid, nonce)
	}
	// R3025: a CLI-bloodhound hunt cookie is the bare request <id>; its close
	// appends raw findings to the request doc and flips the tag to
	// @ark-bloodhound-cli-return (not a finding- doc), handing it to the watcher.
	if b.isCLIHunt(fireToken) {
		return b.closeCLIHunt(fireToken, nonce)
	}
	b.mu.Lock()
	doc := b.results[fireToken]
	cb := b.curations[fireToken]
	delete(b.results, fireToken)
	delete(b.curations, fireToken)
	b.mu.Unlock()

	// The token is <session>-<fire>; decompose it for the tmp:// paths
	// and monitor record. Fall back to the in-flight doc's session if the
	// token is malformed. R2901
	session, fire, ok := parseFireToken(fireToken)
	if !ok {
		if doc != nil {
			session = doc.session
		} else if cb != nil {
			session = cb.session
		}
	}
	if session == "" {
		// We don't know the session — the fire was never opened on
		// this server. Fall back to writing the monitor log only.
		b.appendMonitor(fire, "", nonce, 0, 0, 0, 0, 0, 0, "error")
		return fmt.Errorf("unknown fire %q", fireToken)
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
			// R2750, R2899: result-doc head tag + Surface/Recommend H2 body.
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
func (b *RecallAgentBuilder) openResult(fireToken string) (*recallResultDoc, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d, ok := b.results[fireToken]; ok {
		return d, nil
	}
	cb, ok := b.curations[fireToken]
	if !ok {
		return nil, fmt.Errorf("unknown fire %q (curation doc not in flight)", fireToken)
	}
	d := &recallResultDoc{session: cb.session, opened: time.Now()}
	b.results[fireToken] = d
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

// fireToken is the composite `<session>-<fire>` identity used as the
// in-flight map key and the CLI cookie. Per-session fire numbers
// (R2901) are not globally unique, so the session disambiguates; the
// token is exactly the curation-doc basename minus the `curation-`
// prefix. CRC: crc-RecallAgentBuilder.md | R2901
func fireToken(session string, fire uint64) string {
	return fmt.Sprintf("%s-%d", session, fire)
}

// parseFireToken decomposes a `<session>-<fire>` token. The session
// UUID itself contains dashes, so the fire is the integer after the
// last dash and the session is everything before it. R2901
func parseFireToken(token string) (session string, fire uint64, ok bool) {
	dash := strings.LastIndex(token, "-")
	if dash < 0 {
		return "", 0, false
	}
	f, err := strconv.ParseUint(token[dash+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return token[:dash], f, true
}

// splitLoc splits a `<path>:<range>` locator into path and range on the
// last colon — file paths carry no colon and the range is `N` or `N-M`.
// R2900
func splitLoc(loc string) (path, rangeLabel string) {
	i := strings.LastIndex(loc, ":")
	if i < 0 {
		return loc, ""
	}
	return loc[:i], loc[i+1:]
}

// chunkIDForLoc resolves a (path, range) to its chunkID for the
// chunkid-keyed surface cooldown (R2894), mirroring normalizeInputs'
// path:range branch. Returns (0, false) when unresolvable — the
// cooldown is best-effort, so a miss simply forgoes it. R2900
func (b *RecallAgentBuilder) chunkIDForLoc(path, rangeLabel string) (uint64, bool) {
	fileID, ok := b.db.PathFileID(path)
	if !ok {
		return 0, false
	}
	info, err := b.db.fts.FileInfoByID(fileID)
	if err != nil {
		return 0, false
	}
	for _, c := range info.Chunks {
		if c.Location == rangeLabel {
			return c.ChunkID, true
		}
	}
	return 0, false
}

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

// --- bloodhound: directed search (ARK-BLOODHOUND namespace) ---

// bloodhoundToken is the kind-marked cookie <session>-b<B>; the `b` is what
// keeps it from colliding with a recall fire token in the in-flight maps and
// what routes `close`/`finding` to the bloodhound side. R2945
func bloodhoundToken(session string, bid uint64) string {
	return fmt.Sprintf("%s-b%d", session, bid)
}

// parseBloodhoundToken decomposes a bloodhound cookie <session>-b<B>. ok=false
// for a plain recall fire token (whose last segment is bare digits), so the
// same `close` verb routes both kinds. R2945
func parseBloodhoundToken(token string) (session string, bid uint64, ok bool) {
	dash := strings.LastIndex(token, "-")
	if dash < 0 || dash+2 > len(token) || token[dash+1] != 'b' {
		return "", 0, false
	}
	n, err := strconv.ParseUint(token[dash+2:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return token[:dash], n, true
}

func bloodhoundTaskPath(session string, bid uint64) string {
	return fmt.Sprintf("tmp://ARK-BLOODHOUND/task-%s-%d", session, bid)
}

func bloodhoundFindingPath(session string, bid uint64) string {
	return fmt.Sprintf("tmp://ARK-BLOODHOUND/finding-%s-%d", session, bid)
}

// searchCrankHandle is the self-contained CLI craft handed to the warm
// secretary for a directed hunt (Stencil: the weak agent executes without
// planning). COOKIE is substituted with the task's cookie; the agent fills its
// own nonce. R2938, R3006 (the ## Recall seed lead-in)
const searchCrankHandle = `You are the bloodhound on a directed hunt. The clue is the ## Search task above. Read its fields — clue, scope, depth, want, and any stop condition — then work the trail. Don't plan; do these in order, using ~/.ark/ark.

FIRST read the ## Recall seed above: strong candidates the deluxe combined search already found (it reaches the value→chunk tag axis your own searches can't). READ those hits (step 5) before searching — they often answer the clue outright. Run the steps below only to widen the trail or when the seed is thin (or empty).

Your ONLY tools on this hunt are ~/.ark/ark commands — nothing else. Search with ~/.ark/ark search; open any indexed file with ~/.ark/ark chunks --wrap recall <path:range> (a scoped range) or ~/.ark/ark fetch --wrap recall <path> (the whole file) — --wrap gives you clean text with a provenance tag instead of JSON; locate files by name with ~/.ark/ark files <pattern>; report with ~/.ark/ark connections recall (surface / recommend / finding / close). The Read tool, grep, find, ls, and awk are DENIED, and cat only reaches user-approved paths (it stalls on anything else) — so to look inside any indexed file use ~/.ark/ark chunks or ~/.ark/ark fetch, never Read or cat.

1. SCOPE -> filters. Turn the scope word into search filters (-files globs are cwd-relative: 'specs/**' = this project's specs; '**/' for any depth):
     code   -> -with -files '**/*.go'
     specs  -> -with -files 'specs/**'      design -> -with -files 'design/**'
     notes  -> prose: top-level *.md, .scratch/**, knowledge/**
     chat   -> step 3 only (never the corpus pass)
     all    -> no -files narrowing
2. SEARCH. Match the matcher: exact phrase -> -contains, approx/typo -> -fuzzy, meaning -> -about.
     ~/.ark/ark search "<clue terms>" <scope filters> -k 20 -scores
   One or two searches per scope, then go READ the hits (step 5). Don't re-phrase the same query with synonyms — trigram matches words, not meanings, so synonym swaps rarely add hits. Trust the index; read what it gave you.
3. CHAT (only if scope=chat, or the clue says "did we discuss / in the chat"): a SEPARATE pass that un-hides the logs —
     ~/.ark/ark search -files '~/.claude/projects/**' -fuzzy "<clue>" -k 20 -scores
   Keep it apart from the corpus pool; don't merge (fuzzy scores saturate).
4. TUNE (depth=investigate only). Too noisy? narrow: -with -files '**/*.md', -with -tag NAME[:VALUE]. Too thin? widen: -fuzzy, drop a filter, -about. -parse shows how args parsed. Loop until the stop condition holds (or 2 dry rounds). depth=lookup: one pass.
5. READ the top hits with ~/.ark/ark chunks --wrap recall <path:range> -before 2 -after 2 — NEVER the Read tool (denied here for everything but your own task doc). Corpus files open with ark chunks/fetch, not Read.
6. CURATE to the few that actually answer the clue. Drop the rest — no dumps.
7. EMIT per want — one item per call:
     answer / verdict            -> ~/.ark/ark connections recall finding COOKIE -answer "1-3 sentences" -loc <path:range>
     passages/pointers/inventory -> ~/.ark/ark connections recall finding COOKIE -loc <path:range> [-note "..."]   (repeat per item)
   "no — not in <scope>" is a valid answer; emit it with -answer.
8. ~/.ark/ark connections recall close COOKIE --nonce <your nonce>
`

// buildSearchTask renders the bloodhound task doc in order: the curate head tag
// (so it rides the tube), the ## Search task header with the cookie + raw
// payload, the pre-rendered ## Recall seed block (R3006), and the search crank
// handle with the cookie filled. R2937, R2938, R3006
func buildSearchTask(session, cookie, payload, seed string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@ark-secretary-work: %s\n\n", session)
	fmt.Fprintf(&sb, "## Search task %s\n\n%s\n\n", cookie, payload)
	if seed != "" {
		fmt.Fprintf(&sb, "%s\n", seed)
	}
	sb.WriteString(strings.ReplaceAll(searchCrankHandle, "COOKIE", cookie))
	return sb.String()
}

// RecallBloodhoundOpen writes the directed-search task doc into the
// ARK-BLOODHOUND namespace and retains the clue for the finding header. The
// seed is the watcher's pre-rendered Recall result (R3006). Go-internal,
// called by the watcher's dispatchBloodhound.
// CRC: crc-RecallAgentBuilder.md | R2937, R2938, R3006
func (b *RecallAgentBuilder) RecallBloodhoundOpen(session string, bid uint64, payload, seed string) error {
	cookie := bloodhoundToken(session, bid)
	b.mu.Lock()
	b.bloodhoundClues[cookie] = payload
	b.mu.Unlock()
	body := buildSearchTask(session, cookie, payload, seed)
	path := bloodhoundTaskPath(session, bid)
	return SyncVoid(b.db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", []byte(body))
		return e
	})
}

// openBloodhound returns the per-cookie finding-doc builder, allocating on
// first call. Reuses recallResultDoc as a plain item accumulator.
func (b *RecallAgentBuilder) openBloodhound(cookie string) *recallResultDoc {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d, ok := b.bloodhounds[cookie]; ok {
		return d
	}
	session, _, _ := parseBloodhoundToken(cookie)
	d := &recallResultDoc{session: session, opened: time.Now()}
	b.bloodhounds[cookie] = d
	return d
}

// FindingItem appends one finding to the cookie's finding doc, opening it on
// first call. A -loc finding renders a "- <path>:<range> (<size>) — <note>"
// line (size via ChunkText, no chunkid); an -answer carries synthesized text.
// One item per call. **No own-session gate** — a directed search may point at
// the requester's own session, unlike SurfaceItem.
// CRC: crc-RecallAgentBuilder.md | R2943, R2944
func (b *RecallAgentBuilder) FindingItem(cookie, loc, answer, note string) error {
	if loc == "" && answer == "" {
		return fmt.Errorf("finding requires -loc or -answer")
	}
	// R3025: a CLI-bloodhound hunt uses the same `finding` verb, but its cookie
	// is the bare request <id> (no <session>-b<B> kind-marker). Route it to the
	// CLI-hunt accumulator when a live BLOODHOUND-CLI request doc matches;
	// otherwise require the in-session bloodhound cookie shape.
	var doc *recallResultDoc
	if _, _, ok := parseBloodhoundToken(cookie); ok {
		doc = b.openBloodhound(cookie)
	} else if b.isCLIHunt(cookie) {
		doc = b.openCLIHunt(cookie)
	} else {
		return fmt.Errorf("not a bloodhound cookie: %q", cookie)
	}
	if answer != "" {
		fmt.Fprintf(&doc.buf, "\n%s\n", answer)
	}
	if loc != "" {
		path, rangeLabel := splitLoc(loc)
		sizeLabel := "?"
		if text := b.db.ChunkText(path, rangeLabel); text != nil {
			sizeLabel = friendlySize(len(text))
		}
		line := fmt.Sprintf("- %s:%s (%s)", path, rangeLabel, sizeLabel)
		if note != "" {
			line += " — " + note
		}
		fmt.Fprintf(&doc.buf, "%s\n", line)
	}
	doc.items++
	return nil
}

// closeBloodhound finalizes a directed search: writes the finding doc into
// ARK-BLOODHOUND (stamping the ## Finding: header from the retained clue) iff
// any finding was added, removes the task doc, and logs. The clue is collapsed
// to one line for the header so the assistant correlates verbatim.
// CRC: crc-RecallAgentBuilder.md | R2945, R2946
func (b *RecallAgentBuilder) closeBloodhound(cookie, session string, bid uint64, nonce uint32) error {
	b.mu.Lock()
	doc := b.bloodhounds[cookie]
	clue := b.bloodhoundClues[cookie]
	delete(b.bloodhounds, cookie)
	delete(b.bloodhoundClues, cookie)
	b.mu.Unlock()

	outcome := "silent-close"
	found := 0
	if doc != nil && doc.items > 0 {
		found = doc.items
		if b.db != nil && b.db.pubsub != nil &&
			b.db.pubsub.SubscriberCount("ark-bloodhound-result", session) == 0 {
			outcome = "no-subscriber" // R2808 parity
		} else {
			headerClue := strings.TrimSpace(strings.ReplaceAll(clue, "\n", " "))
			header := fmt.Sprintf("@ark-bloodhound-result: %s\n\n## Finding: %s\n", session, headerClue)
			body := []byte(header + doc.buf.String())
			path := bloodhoundFindingPath(session, bid)
			if err := SyncVoid(b.db, func(db *DB) error {
				_, e := db.AddTmpFile(path, "markdown", body)
				return e
			}); err != nil {
				b.appendMonitor(bid, session, nonce, 0, 0, 0, 0, 0, 0, "error")
				return fmt.Errorf("write finding doc: %w", err)
			}
			outcome = "result-emitted"
		}
	}
	// Remove the task doc — no orphan sweep (bloodhound tasks don't pile the
	// way curation docs do).
	_ = SyncVoid(b.db, func(db *DB) error {
		return db.RemoveTmpFile(bloodhoundTaskPath(session, bid))
	})

	in, out := b.lookupSubagentTokens(nonce)
	contextTokens, _ := b.ContextTokens(nonce)
	latency := 0
	if doc != nil && !doc.opened.IsZero() {
		latency = int(time.Since(doc.opened).Milliseconds())
	}
	// findings counted in the surfaced slot of the monitor record.
	b.appendMonitor(bid, session, nonce, in, out, contextTokens, latency, found, 0, outcome)
	return nil
}

// --- CLI-bloodhound: two-doc pipeline (external-CLI directed hunt) ---
//
// bloodhound-cli.md S4. Distinct from the in-session ARK-BLOODHOUND finding
// doc: a CLI hunt reuses this builder family for a two-doc pipeline. The
// request doc (tmp://BLOODHOUND-CLI/<id>) is the internal working artifact
// whose tag is a routing baton; the result doc
// (tmp://BLOODHOUND-CLI-RESULT/<id>) is the clean external JSONL.

// bloodhoundCLIResultPath is the canonical tmp:// path for a CLI-hunt result
// doc — a separate namespace from the request doc so the CLI never sees the
// internal pipeline. R3027, R3028
func bloodhoundCLIResultPath(id string) string {
	return "tmp://BLOODHOUND-CLI-RESULT/" + id
}

// isCLIHunt reports whether cookie names a live CLI-bloodhound request doc
// (the namespace discriminator, R3025). A pool secretary's finding/close
// cookie is the bare request id, so the tmp:// path is the source of truth —
// no kind-marker to parse. Best-effort: a missing doc simply routes cookie
// through the ordinary recall/bloodhound paths.
func (b *RecallAgentBuilder) isCLIHunt(cookie string) bool {
	if b.db == nil || cookie == "" {
		return false
	}
	_, err := b.db.TmpContent(bloodhoundCLIPrefix + cookie)
	return err == nil
}

// openCLIHunt returns the per-id raw-findings accumulator for a CLI hunt,
// allocating on first call. Reuses recallResultDoc as a plain item
// accumulator (session unused — the request doc, not a session-scoped result,
// is the write target). R3025
func (b *RecallAgentBuilder) openCLIHunt(id string) *recallResultDoc {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d, ok := b.cliHunts[id]; ok {
		return d
	}
	d := &recallResultDoc{opened: time.Now()}
	b.cliHunts[id] = d
	return d
}

// closeCLIHunt finalizes a CLI-bloodhound secretary hunt (R3025, R3031): it
// appends the accumulated raw findings to the request doc
// tmp://BLOODHOUND-CLI/<id> and re-tags it @ark-bloodhound-cli-return: <id>
// in one atomic write, handing the doc back to the watcher (which frees the
// secretary and routes it to Luhmann for curation). Unlike closeBloodhound it
// writes no finding-<S>-<B> doc and removes nothing — the request doc IS the
// return, and Luhmann reads it next. The secretary-facing seed + crank handle
// are dropped (cut at `## Recall seed`) so Luhmann's inlined view is just the
// query + raw findings.
// CRC: crc-RecallAgentBuilder.md | Seq: seq-bloodhound-cli.md#1.3.2 | R3025, R3031
func (b *RecallAgentBuilder) closeCLIHunt(id string, nonce uint32) error {
	b.mu.Lock()
	doc := b.cliHunts[id]
	delete(b.cliHunts, id)
	b.mu.Unlock()

	path := bloodhoundCLIPrefix + id
	data, err := b.db.TmpContent(path)
	if err != nil {
		b.appendMonitor(0, "cli-"+id, nonce, 0, 0, 0, 0, 0, 0, "error")
		return fmt.Errorf("cli-hunt close: request doc %s not found: %w", path, err)
	}
	// Keep the query context (## Search task + payload), drop the secretary's
	// seed + crank handle so Luhmann curates against a clean doc.
	query := stripLeadingTag(string(data))
	if i := strings.Index(query, "## Recall seed"); i >= 0 {
		query = strings.TrimRight(query[:i], "\n")
	}
	findings := "(no findings)\n"
	found := 0
	if doc != nil && doc.items > 0 {
		findings = strings.TrimLeft(doc.buf.String(), "\n")
		found = doc.items
	}
	// R3028/R3031: flip the head tag to @ark-bloodhound-cli-return: <id> (WITH
	// COLON — ark only extracts @word: tags, so a colon-less tag never
	// publishes and the watcher never wakes) and append the raw findings, one
	// atomic write-actor flush.
	newBody := fmt.Sprintf("@%s: %s\n\n%s\n\n## Raw findings\n\n%s",
		bloodhoundCLIReturnTag, id, query, findings)
	if err := SyncVoid(b.db, func(db *DB) error {
		return db.UpdateTmpFile(path, "markdown", []byte(newBody))
	}); err != nil {
		b.appendMonitor(0, "cli-"+id, nonce, 0, 0, 0, 0, 0, 0, "error")
		return fmt.Errorf("cli-hunt close: re-tag request doc: %w", err)
	}
	in, out := b.lookupSubagentTokens(nonce)
	contextTokens, _ := b.ContextTokens(nonce)
	latency := 0
	if doc != nil && !doc.opened.IsZero() {
		latency = int(time.Since(doc.opened).Milliseconds())
	}
	b.appendMonitor(0, "cli-"+id, nonce, in, out, contextTokens, latency, found, 0, "cli-return")
	return nil
}

// cliFinding is one curated line of a CLI-hunt result doc's JSONL output
// (R3029): at least path + range + a curated note, with an optional chunk
// excerpt so an external app need not re-fetch.
type cliFinding struct {
	Path  string `json:"path"`
	Range string `json:"range"`
	Note  string `json:"note,omitempty"`
	Chunk string `json:"chunk,omitempty"`
}

// BloodhoundCLIAdd appends one curated finding to the CLI-hunt result-doc
// accumulator (R3027), opening cliResults[id] on first call. Luhmann's result
// stencil — one item per call, the discipline of surface/finding: the model
// never hand-writes JSON, the builder does. id is the request id (derived from
// the --result path). R3027, R3029
// CRC: crc-RecallAgentBuilder.md | Seq: seq-bloodhound-cli.md#1.5.2 | R3027, R3029
func (b *RecallAgentBuilder) BloodhoundCLIAdd(id, loc, note, chunk string) error {
	path, rangeLabel := splitLoc(loc)
	if path == "" || rangeLabel == "" {
		return fmt.Errorf("add requires -loc PATH:RANGE")
	}
	line, err := json.Marshal(cliFinding{Path: path, Range: rangeLabel, Note: note, Chunk: chunk})
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.cliResults[id]
	if !ok {
		d = &recallResultDoc{opened: time.Now()}
		b.cliResults[id] = d
	}
	d.buf.Write(line)
	d.buf.WriteByte('\n')
	d.items++
	return nil
}

// BloodhoundCLIAddDone is the terminal call (R3027, R3028): it writes the
// result doc tmp://BLOODHOUND-CLI-RESULT/<id> with the head tag
// @ark-bloodhound-cli-result: <id> (WITH COLON) followed by the accumulated
// JSONL, which publishes the notification the waiting CLI is subscribed to. An
// id with no prior add writes an empty-body result doc — an empty hunt (R3029:
// no lines, exit 0). It then drops cliResults[id] and removes the internal
// request doc (Luhmann is finished with it). R3027, R3028, R3031
// CRC: crc-RecallAgentBuilder.md | Seq: seq-bloodhound-cli.md#1.5.3 | R3027, R3028, R3031
func (b *RecallAgentBuilder) BloodhoundCLIAddDone(id string) error {
	b.mu.Lock()
	d := b.cliResults[id]
	delete(b.cliResults, id)
	b.mu.Unlock()

	body := "@" + bloodhoundCLIResultTag + ": " + id + "\n\n"
	if d != nil {
		body += d.buf.String()
	}
	resultPath := bloodhoundCLIResultPath(id)
	if err := SyncVoid(b.db, func(db *DB) error {
		_, e := db.AddTmpFile(resultPath, "markdown", []byte(body))
		return e
	}); err != nil {
		return fmt.Errorf("write cli result doc: %w", err)
	}
	// The request doc has served its purpose (Luhmann curated from it); drop it
	// so the tmp:// space doesn't grow across hunts. The result doc lingers for
	// the CLI's read (wiped on restart, tmp:// being per-process).
	_ = SyncVoid(b.db, func(db *DB) error {
		return db.RemoveTmpFile(bloodhoundCLIPrefix + id)
	})
	return nil
}

// stripLeadingTag drops a single leading @word:... head-tag line (and blank
// lines after it) so inlined tmp:// doc content reads cleanly — the CLI's JSONL
// output and Luhmann's curation view must not carry the head tag.
func stripLeadingTag(s string) string {
	first, rest, found := strings.Cut(s, "\n")
	if found && strings.HasPrefix(first, "@") {
		return strings.TrimLeft(rest, "\n")
	}
	return s
}

// blockquoteEscape collapses internal newlines so a multi-line
// excerpt stays on one blockquoted line. Markdown renderers vary
// in how they handle wrapped blockquotes; one line is the simple
// invariant. R2898
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
