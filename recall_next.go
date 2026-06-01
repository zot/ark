package ark

// CRC: crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md#3 | R2857, R2858

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// curationPrefix is the tmp:// path prefix for watcher-written curation
// docs: tmp://ARK-RECALL/curation-<session>-<fire>.
const curationPrefix = "tmp://ARK-RECALL/curation-"

// recallNextListenWindow bounds each blocking Listen inside RecallNext.
// The loop re-checks pending docs and honors request cancellation
// between windows.
const recallNextListenWindow = 30 * time.Second

// recallNextKeepalive caps how long RecallNext blocks before returning a
// keepalive directive (R2857). The binding constraint is the harness
// foreground-Bash auto-background threshold (~120s default): if `next`
// blocks past it, the harness detaches the call and the recall subagent
// ends its turn — which makes it emit a "completed" notification per
// loop cycle (heartbeat) that the orchestrator can't tell from a real
// exit. Returning within the threshold keeps `next` in the foreground,
// so the subagent stays in one continuous turn and only "completes" on a
// true context-limit exit. 90s leaves margin under the 120s default. A
// continuous foreground turn stays cache-warm by activity, so the
// prompt-cache TTL isn't the constraint here.
const recallNextKeepalive = 90 * time.Second

// RecallNext is the daemon's single loop verb (R2857, R2858). It
// idempotently subscribes the nonce's curate session, context-gates
// against the limit, then returns the lowest-fire pending curation doc
// — blocking (true lotto-tube) until one exists. Returns the
// crank-handle body and whether the directive is an exit.
func (b *RecallAgentBuilder) RecallNext(ctx context.Context, nonce uint32, session string, contextLimit int) (body string, exit bool, err error) {
	subSession := fmt.Sprintf("recall-%d", nonce)

	// Idempotent subscribe to @ark-recall-curate (bare). Subscribe
	// appends, so guard with SubCount to keep later calls no-ops.
	if b.db.pubsub.SubCount(subSession) == 0 {
		match := "ark-recall-curate"
		if session != "" {
			match = "ark-recall-curate=" + session // R2888: per-session value-scope
		}
		p, perr := ParseMatchSyntax(match)
		if perr != nil {
			return "", false, perr
		}
		b.db.pubsub.Subscribe(subSession, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
	}

	// Context-gate: at or over the limit is the daemon's only clean
	// exit (R2857). A nonce with no subagent JSONL reports not-found
	// and never trips the gate.
	if contextLimit > 0 {
		if tokens, ok := b.ContextTokens(nonce); ok && tokens >= contextLimit {
			return recallExitPrompt(tokens, contextLimit), true, nil
		}
	}

	// Bounded lotto-tube: dispatch the lowest-fire pending doc that has a
	// result subscriber, else wait. At the keepalive deadline, return a
	// keepalive directive instead of blocking forever (R2857) — a doc and
	// a keepalive both exit 0 and both tell the agent to run `next` again;
	// only the context-limit exit (above) stops, so there is no ambiguous
	// empty to misread.
	deadline := time.Now().Add(recallNextKeepalive)
	for {
		fire, docSession, content, ok := b.lowestPendingCuration(session)
		if ok {
			if session != "" {
				content = b.injectConversation(session, content) // R2891
			}
			// R2896: write the doc to a real file and hand back a short
			// pointer — the large content never touches the agent's
			// foreground-Bash stdout (which the harness truncates).
			path, werr := b.writeCurationFile(docSession, fire, content)
			if werr != nil {
				return "", false, werr
			}
			return recallDocPrompt(fire, session, nonce, path), false, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return recallKeepalivePrompt(nonce), false, nil
		}
		window := min(recallNextListenWindow, remaining)
		if !b.waitForWork(ctx, subSession, window) {
			return "", false, ctx.Err()
		}
	}
}

// waitForWork blocks until something may make a curation doc
// dispatchable, then returns true; it returns false only on context
// cancellation. It wakes on a curate event (the session queue), a new
// subscription (SubChanged — so a late result subscriber dispatches
// piled docs at once instead of after the re-check tick), the re-check
// window elapsing, or cancellation. Because the loop waits here rather
// than in PubSub.Listen, it refreshes last-listen each cycle so Reap
// doesn't drop the subscription (R803, R2857).
func (b *RecallAgentBuilder) waitForWork(ctx context.Context, session string, window time.Duration) bool {
	ps := b.db.pubsub
	timer := time.NewTimer(window)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-ps.SubChanged():
	case <-ps.QueueChan(session):
	case <-timer.C:
	}
	ps.TouchListen(session)
	return true
}

// lowestPendingCuration finds the lowest-fire pending curation doc whose
// originating session has a result subscriber, and reads its content.
// Docs for sessions with no subscriber are skipped — left to pile up so
// the daemon defers (never discards) their work (R2857). The curation
// path is tmp://ARK-RECALL/curation-<session>-<fire>; the session UUID
// itself contains dashes, so the fire is the integer after the *last*
// dash and the session is everything before it. Processing
// lowest-fire-first keeps Close's same-session orphan sweep (R2758) safe.
// With targetSession set, only that session's docs are dispatched (R2889).
// CRC: crc-RecallAgentBuilder.md | R2889
func (b *RecallAgentBuilder) lowestPendingCuration(targetSession string) (fire uint64, docSession string, content string, ok bool) {
	files, err := Sync(b.db, func(db *DB) ([]string, error) { return db.Files() })
	if err != nil {
		return 0, "", "", false
	}
	var path, bestSession string
	for _, p := range files {
		if !strings.HasPrefix(p, curationPrefix) {
			continue
		}
		rest := p[len(curationPrefix):]
		dash := strings.LastIndex(rest, "-")
		if dash < 0 {
			continue
		}
		f, perr := strconv.ParseUint(rest[dash+1:], 10, 64)
		if perr != nil {
			continue
		}
		// Gate on a result subscriber for this doc's session (R2857):
		// only dispatch work whose result `close` will be able to deliver.
		// With no subscriber the doc is left pending (it piles up) and the
		// daemon keeps blocking — the work is deferred, never discarded.
		// The session is the path segment before the trailing -<fire>.
		session := rest[:dash]
		if targetSession != "" && session != targetSession {
			continue // R2889: per-session secretary dispatches only its own docs
		}
		if b.db.pubsub.SubscriberCount("ark-recall-result", session) == 0 {
			continue
		}
		if !ok || f < fire {
			path, fire, bestSession, ok = p, f, session, true
		}
	}
	if !ok {
		return 0, "", "", false
	}
	data, err := b.db.TmpContent(path)
	if err != nil {
		// The doc vanished between listing and read (e.g. a concurrent
		// close); treat as nothing pending — the caller re-checks.
		return 0, "", "", false
	}
	return fire, bestSession, string(data), true
}

// recallDocPrompt is the crank-handle for a dispatched curation doc
// (R2858, R2896): a SHORT pointer to the materialized doc file (kept off
// the agent's truncating foreground stdout), then the actions to take.
// The agent Reads the file (the Read tool paginates; `cat` would
// re-overflow). A candidate marked `- tag-only: true` is the reader's own
// conversation — recommend a tag for it but never surface it.
// CRC: crc-RecallAgentBuilder.md | R2858, R2870, R2873, R2896
func recallDocPrompt(fire uint64, session string, nonce uint32, path string) string {
	nextCmd := fmt.Sprintf("~/.ark/ark connections recall next %d", nonce)
	if session != "" {
		nextCmd = fmt.Sprintf("~/.ark/ark connections recall next --session %s %d", session, nonce)
	}
	return fmt.Sprintf(
		"Curation doc for fire %d is at:\n%s\n\n"+
			"Read that file with the Read tool (permitted for this path). It opens with the recent conversation, then `# Source Chunk:` / `## Candidate:` blocks. Judge each candidate for genuine fit with the live conversation.\n"+
			"For each candidate worth showing: `~/.ark/ark connections recall surface %d -chunk <CANDIDATE-CHUNKID> -reason \"...\"`.\n"+
			"For each tag worth attaching: `~/.ark/ark connections recall recommend %d -chunk <CANDIDATE-CHUNKID> -tag @t[:v] -reason \"...\"`.\n"+
			"Always pass a `## Candidate:` chunkid — never the `# Source Chunk:` id (surface will reject it).\n"+
			"A candidate marked `- tag-only: true` is from the current conversation — recommend a tag if one fits, but do NOT surface it.\n"+
			"Then `~/.ark/ark connections recall close %d --nonce %d`.\n"+
			"Then run `%s` again.\n",
		fire, path, fire, fire, fire, nonce, nextCmd)
}

// recallExitPrompt is the crank-handle returned when the nonce's
// context has reached the limit — the daemon's clean exit (R2857).
func recallExitPrompt(tokens, limit int) string {
	return fmt.Sprintf("Context limit reached (%d ≥ %d tokens). This generation is done — stop now; the orchestrator will recycle you with a fresh nonce.\n", tokens, limit)
}

// recallKeepalivePrompt is returned when no dispatchable doc appears
// within the keepalive window (R2857). Like a doc it exits 0 and tells
// the agent to run `next` again; the short window keeps `next` returning
// inline (foreground) before the harness auto-backgrounds it, never
// reading as a stop.
func recallKeepalivePrompt(nonce uint32) string {
	return fmt.Sprintf("No curation doc is pending yet (no work, or no result subscriber). This is normal. Run `~/.ark/ark connections recall next %d` again now to keep watching — do not stop, do not wait.\n", nonce)
}

// recallListenWindow bounds each blocking Listen inside RecallListen so
// the loop honors ctx cancellation; the loop keeps re-listening until a
// result arrives (no keepalive — the consumer wakes only on real work).
const recallListenWindow = 60 * time.Second

// RecallListen is the consumer-side loop verb (R2865), the mirror of
// RecallNext for a user-facing assistant rather than the daemon. It
// idempotently subscribes the session to its own value-scoped result
// tag, blocks until at least one result doc is published, and returns
// the doc content(s) plus a crank-handle telling the assistant to
// surface what helps the user and run `recall listen` again. No
// keepalive and no context-gate: the assistant runs this backgrounded
// and should wake only on a real result; the sole non-result return is
// ctx cancellation.
func (b *RecallAgentBuilder) RecallListen(ctx context.Context, session string) (body string, err error) {
	// Idempotent subscribe to the value-scoped result tag (mirrors the
	// hand-run `ark subscribe --tag ark-recall-result=<session>`).
	if b.db.pubsub.SubCount(session) == 0 {
		p, perr := ParseMatchSyntax("ark-recall-result=" + session)
		if perr != nil {
			return "", perr
		}
		b.db.pubsub.Subscribe(session, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
	}
	for {
		events := b.db.pubsub.Listen(session, recallListenWindow)
		if len(events) > 0 {
			return b.recallListenPrompt(session, events), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
	}
}

// recallListenPrompt fetches each result doc the events point at and
// wraps them as a consumer crank-handle (R2865): the result content,
// then what to do with it and the instruction to listen again. When a
// result references a chat-JSONL chunk (a Surface/Recommend path ending
// in `.jsonl`), it adds external-tag guidance (R2871).
// CRC: crc-RecallAgentBuilder.md | R2865, R2871
func (b *RecallAgentBuilder) recallListenPrompt(session string, events []Event) string {
	var sb strings.Builder
	for i, ev := range events {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		data, derr := b.db.TmpContent(ev.Path)
		if derr != nil {
			// Doc vanished between publish and fetch (e.g. a clean) —
			// name it and move on.
			fmt.Fprintf(&sb, "(recall result %s is no longer available)\n", ev.Path)
			continue
		}
		sb.Write(data)
	}
	// R2871: if any referenced chunk lives in a chat-JSONL file (a past
	// conversation), remind the assistant to tag those via external (@ext)
	// tags — the file is append-only source of truth — which is how
	// conversations enter the hypergraph. Conditional; the internal-vs-@ext
	// choice stays the assistant's. The `.jsonl:` shape matches a
	// Surface/Recommend `<path>:<range>` whose path is a session JSONL.
	extTagNote := ""
	if strings.Contains(sb.String(), ".jsonl:") {
		extTagNote = "Some referenced chunks live in chat-JSONL files (past conversations) — apply any tag on those as an external (`@ext`) tag, never an inline edit, since the file is append-only source of truth; that is how conversations enter the hypergraph. "
	}
	fmt.Fprintf(&sb,
		"\n\n---\nThis is ambient recall — material from the corpus related to the current conversation. "+
			"`## Surface:` items are chunks worth showing the user; `## Recommend:` items are tags worth attaching. "+
			"Decide what genuinely helps the user right now — you have final say; skip anything stale or off-topic. "+
			"%s"+
			"If the user rejects a recommended tag, run `~/.ark/ark connections recall reject-derived`. "+
			"Then run `~/.ark/ark connections recall listen --session %s` again to keep receiving.\n",
		extTagNote, session)
	return sb.String()
}

// injectConversation prepends up to [recall].context_turns trailing turns
// of the session's conversation to a curation doc, so the per-session
// secretary judges with the live conversation rather than excerpts alone
// (R2891). Best-effort: returns the doc unchanged when context is off or
// the JSONL can't be read.
// CRC: crc-RecallAgentBuilder.md | R2891
func (b *RecallAgentBuilder) injectConversation(session, doc string) string {
	n := b.db.Config().Recall.EffectiveContextTurns()
	if n <= 0 {
		return doc
	}
	block := b.recentConversation(session, n)
	if block == "" {
		return doc
	}
	return block + "\n\n---\n\n" + doc
}

// recentConversation reads the session's JSONL and renders the last n
// turns (from the n-th-from-last genuine user message onward) as a short
// transcript. Returns "" on any failure. R2891
func (b *RecallAgentBuilder) recentConversation(session string, n int) string {
	paths, err := b.db.SessionJSONLs([]string{session})
	if err != nil || len(paths) == 0 {
		return ""
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		return ""
	}
	type entry struct{ role, text string }
	var entries []entry
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Type   string `json:"type"`
			Origin struct {
				Kind string `json:"kind"`
			} `json:"origin"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		switch rec.Type {
		case "user":
			if txt, ok := userProse(rec.Origin.Kind, rec.Message.Content); ok {
				entries = append(entries, entry{"user", txt})
			}
		case "assistant":
			if txt := assistantText(rec.Message.Content); txt != "" {
				entries = append(entries, entry{"assistant", txt})
			}
		}
	}
	if len(entries) == 0 {
		return ""
	}
	// "Last n turns" = from the n-th-from-last user message onward.
	start, users := 0, 0
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].role == "user" {
			users++
			if users >= n {
				start = i
				break
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("## Recent conversation (context for your relevance judgment)\n\n")
	for _, e := range entries[start:] {
		fmt.Fprintf(&sb, "**%s:** %s\n\n", e.role, truncateUTF8(strings.TrimSpace(e.text), 400))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// userProse returns the prose of a genuine user message (content is a
// JSON string, no harness origin — mirrors isGenuineUserMessage) and ok.
func userProse(originKind string, content json.RawMessage) (string, bool) {
	if originKind != "" {
		return "", false
	}
	c := bytes.TrimSpace(content)
	if len(c) == 0 || c[0] != '"' {
		return "", false
	}
	var str string
	if json.Unmarshal(c, &str) != nil {
		return "", false
	}
	return str, true
}

// assistantText extracts the text of an assistant record: a bare string
// content, or the concatenated "text" blocks of an array content (tool_use
// blocks are skipped). Returns "" when there is no text.
func assistantText(content json.RawMessage) string {
	c := bytes.TrimSpace(content)
	if len(c) == 0 {
		return ""
	}
	if c[0] == '"' {
		var str string
		if json.Unmarshal(c, &str) == nil {
			return str
		}
		return ""
	}
	if c[0] == '[' {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(c, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, bl := range blocks {
			if bl.Type == "text" && strings.TrimSpace(bl.Text) != "" {
				parts = append(parts, bl.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}
