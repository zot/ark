package ark

// CRC: crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md#3 | R2857, R2858, R2902

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

// bloodhoundTaskPrefix is the tmp:// path prefix for watcher-written directed-
// search task docs: tmp://ARK-BLOODHOUND/task-<session>-<B>. Its own namespace
// keeps it from colliding with curation docs. R2939
const bloodhoundTaskPrefix = "tmp://ARK-BLOODHOUND/task-"

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
	// R2902: key the curate subscription on the durable session
	// (`secretary-work-<session>`), not the volatile `recall-<nonce>`, so
	// two secretary generations across a restart share one stable, unique
	// key and the SubCount re-subscribe guard never no-ops a colliding
	// subscriber. Distinct prefix from the consumer's result-subscription
	// key (bare `<session>`, R2865) so curate and result never cross-wake.
	// The legacy bare-curate path (no session) keeps `recall-<nonce>`.
	subSession := fmt.Sprintf("recall-%d", nonce)
	if session != "" {
		subSession = "secretary-work-" + session
	}

	// Idempotent subscribe to @ark-secretary-work. Subscribe appends, so
	// guard with SubCount to keep later calls no-ops.
	if b.db.pubsub.SubCount(subSession) == 0 {
		match := "ark-secretary-work"
		if session != "" {
			match = "ark-secretary-work=" + session // R2888: per-session value-scope
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
		// R2939: directed search served ahead of ambient recall — dispatch a
		// pending bloodhound task before any curation doc. Small doc, so it
		// returns inline (no file materialization, no Read keyhole).
		if _, _, content, ok := b.lowestPendingBloodhound(session); ok {
			return recallSearchTaskPrompt(session, nonce, content), false, nil
		}
		// R3030, R3032: a CLI-bloodhound request doc the watcher routed to this
		// pool secretary via @ark-secretary-work=<composite>. Same crank-handle
		// return as an in-session bloodhound task (the enhanced doc IS one).
		if content, ok := b.lowestPendingCLIHunt(session); ok {
			return recallSearchTaskPrompt(session, nonce, content), false, nil
		}
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
			return recallDocPrompt(fire, docSession, session, nonce, path), false, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return recallKeepalivePrompt(session, nonce), false, nil
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

// lowestPendingBloodhound finds the lowest-id pending directed-search task doc
// for the session, gated on a result subscriber like curation. Mirrors
// lowestPendingCuration but over the ARK-BLOODHOUND task namespace. R2939
// CRC: crc-RecallAgentBuilder.md | R2939
func (b *RecallAgentBuilder) lowestPendingBloodhound(targetSession string) (bid uint64, docSession, content string, ok bool) {
	files, err := Sync(b.db, func(db *DB) ([]string, error) { return db.Files() })
	if err != nil {
		return 0, "", "", false
	}
	var path, bestSession string
	for _, p := range files {
		if !strings.HasPrefix(p, bloodhoundTaskPrefix) {
			continue
		}
		rest := p[len(bloodhoundTaskPrefix):]
		dash := strings.LastIndex(rest, "-")
		if dash < 0 {
			continue
		}
		n, perr := strconv.ParseUint(rest[dash+1:], 10, 64)
		if perr != nil {
			continue
		}
		session := rest[:dash]
		if targetSession != "" && session != targetSession {
			continue
		}
		if b.db.pubsub.SubscriberCount("ark-bloodhound-result", session) == 0 {
			continue // R2947: defer until a bloodhound-result consumer is present
		}
		if !ok || n < bid {
			path, bid, bestSession, ok = p, n, session, true
		}
	}
	if !ok {
		return 0, "", "", false
	}
	data, err := b.db.TmpContent(path)
	if err != nil {
		return 0, "", "", false
	}
	return bid, bestSession, string(data), true
}

// lowestPendingCLIHunt finds a CLI-bloodhound request doc the watcher routed to
// this pool secretary — a tmp://BLOODHOUND-CLI/<id> doc enhanced and re-tagged
// to @ark-secretary-work=<session> (the composite <luhmann-session>-<nonce>,
// R3032). Unlike lowestPendingBloodhound, whose ARK-BLOODHOUND path encodes the
// session, the BLOODHOUND-CLI path is only <id>, so we match on the doc's head
// tag. No result-subscriber gate: the watcher routes only to a subscribed
// secretary, and a CLI-hunt finding goes to the request doc, not a consumer.
// CRC: crc-RecallAgentBuilder.md | R3030, R3032
func (b *RecallAgentBuilder) lowestPendingCLIHunt(session string) (content string, ok bool) {
	if session == "" {
		return "", false
	}
	files, err := Sync(b.db, func(db *DB) ([]string, error) { return db.Files() })
	if err != nil {
		return "", false
	}
	head := "@ark-secretary-work: " + session
	for _, p := range files {
		if !strings.HasPrefix(p, bloodhoundCLIPrefix) {
			continue
		}
		data, derr := b.db.TmpContent(p)
		if derr != nil {
			continue
		}
		s := string(data)
		if first, _, _ := strings.Cut(s, "\n"); strings.TrimSpace(first) == head {
			return s, true
		}
	}
	return "", false
}

// recallSearchTaskPrompt wraps a dispatched bloodhound task for inline return:
// strip the leading curate head-tag, substitute the agent's nonce into the
// crank handle's close line, and tail with the loop instruction. R2940
// CRC: crc-RecallAgentBuilder.md | R2940
func recallSearchTaskPrompt(session string, nonce uint32, content string) string {
	nextCmd := fmt.Sprintf("~/.ark/ark connections recall next %d", nonce)
	if session != "" {
		nextCmd = fmt.Sprintf("~/.ark/ark connections recall next --session %s %d", session, nonce)
	}
	body := stripCurateTagLine(content)
	body = strings.ReplaceAll(body, "<your nonce>", strconv.FormatUint(uint64(nonce), 10))
	return fmt.Sprintf(
		"A directed search came in — run it now, in this same turn:\n\n%s\n\nWhen you have closed it, run %s again.\n",
		body, nextCmd)
}

// stripCurateTagLine drops a leading `@ark-secretary-work:` head-tag line (and
// the blank after it) so the inline task reads cleanly for the agent.
func stripCurateTagLine(s string) string {
	first, rest, found := strings.Cut(s, "\n")
	if found && strings.HasPrefix(first, "@ark-secretary-work:") {
		return strings.TrimLeft(rest, "\n")
	}
	return s
}

// recallDocPrompt is the crank-handle for a dispatched curation doc
// (R2858, R2896): a SHORT pointer to the materialized doc file (kept off
// the agent's truncating foreground stdout), then the actions to take.
// The agent Reads the file (the Read tool paginates; `cat` would
// re-overflow). A candidate marked `- tag-only: true` is the reader's own
// conversation — recommend a tag for it but never surface it.
// CRC: crc-RecallAgentBuilder.md | R2858, R2870, R2873, R2896
func recallDocPrompt(fire uint64, docSession, session string, nonce uint32, path string) string {
	// The surface/recommend/close cookie is the composite <session>-<fire>
	// token (R2901), built from the doc's own session so it matches the
	// in-flight map key even on the legacy all-session scan.
	token := fireToken(docSession, fire)
	nextCmd := fmt.Sprintf("~/.ark/ark connections recall next %d", nonce)
	if session != "" {
		nextCmd = fmt.Sprintf("~/.ark/ark connections recall next --session %s %d", session, nonce)
	}
	return fmt.Sprintf(
		"Curation doc %s is at:\n%s\n\n"+
			"Read that file with the Read tool (permitted for this path). It opens with the recent conversation, then `# Source:` / `## Candidate:` blocks, each headed by a `<path>:<range>` locator. Judge each candidate for genuine fit with the live conversation.\n"+
			"For each candidate worth showing: `~/.ark/ark connections recall surface %s -loc <CANDIDATE-PATH:RANGE> -reason \"...\"`.\n"+
			"For each tag worth attaching: `~/.ark/ark connections recall recommend %s -loc <CANDIDATE-PATH:RANGE> -tag @t[:v] -reason \"...\"`.\n"+
			"Always pass a `## Candidate:` locator — never the `# Source:` locator (surface will reject it).\n"+
			"A candidate marked `- tag-only: true` is from the current conversation — recommend a tag if one fits, but do NOT surface it.\n"+
			"Then `~/.ark/ark connections recall close %s --nonce %d`.\n"+
			"Then run `%s` again.\n",
		token, path, token, token, token, nonce, nextCmd)
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
func recallKeepalivePrompt(session string, nonce uint32) string {
	// Carry --session so the agent's next loop iteration stays on the
	// per-session curate subscription (R2902), never falling back to the
	// legacy bare-curate path.
	nextCmd := fmt.Sprintf("~/.ark/ark connections recall next %d", nonce)
	if session != "" {
		nextCmd = fmt.Sprintf("~/.ark/ark connections recall next --session %s %d", session, nonce)
	}
	return fmt.Sprintf("No curation doc is pending yet (no work, or no result subscriber). This is normal. Run `%s` again now to keep watching — do not stop, do not wait.\n", nextCmd)
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
func (b *RecallAgentBuilder) RecallListen(ctx context.Context, session string, ambient bool) (body string, err error) {
	// Idempotent subscribe **per capability** (R2950): always to the
	// bloodhound-result tag (findings); plus the recall-result tag (ambient
	// surfaces) when `ambient` is set — that recall-result sub is the ambient
	// opt-in the watcher keys on. Each is added only if not already present, so
	// a `/bloodhound` listen upgrades to `/recall`'s `listen --ambient` without
	// dropping the loop.
	var subs []*TagSub
	if b.db.pubsub.SubscriberCount("ark-bloodhound-result", session) == 0 {
		p, perr := ParseMatchSyntax("ark-bloodhound-result=" + session)
		if perr != nil {
			return "", perr
		}
		subs = append(subs, &TagSub{Kind: TagSubChunk, Predicate: p})
	}
	if ambient && b.db.pubsub.SubscriberCount("ark-recall-result", session) == 0 {
		p, perr := ParseMatchSyntax("ark-recall-result=" + session)
		if perr != nil {
			return "", perr
		}
		subs = append(subs, &TagSub{Kind: TagSubChunk, Predicate: p})
	}
	if len(subs) > 0 {
		b.db.pubsub.Subscribe(session, subs)
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
// JSON string, origin.kind == "human" — mirrors isGenuineUserMessage) and ok.
// R3009: keys on the positive human marker, not origin-absence, so genuine
// turns (now stamped origin.kind="human") are not dropped from injected context.
func userProse(originKind string, content json.RawMessage) (string, bool) {
	if originKind != "human" {
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

// --- CLI-bloodhound consumer verbs (bloodhound-CLI S3: R3021, R3022, R3029) ---

// bloodhoundCLIResultSession is the pubsub session the `ark bloodhound search`
// consumer subscribes + blocks on for one hunt's result. Value-scoped by id so a
// CLI wakes only on its own result (R3021).
func bloodhoundCLIResultSession(id string) string { return "bloodhound-cli-result-" + id }

// bloodhoundCLIResultWindow bounds each blocking Listen inside BloodhoundCLIResult
// so the loop honors the deadline and ctx cancellation.
const bloodhoundCLIResultWindow = 30 * time.Second

// BloodhoundCLIOpen creates a CLI-hunt request doc and subscribes the caller to
// its result tag **before the doc lands** (R3021): it mints a hunt id, subscribes
// bloodhoundCLIResultSession(id) to @ark-bloodhound-cli-result=<id>, then writes
// the request doc tmp://BLOODHOUND-CLI/<id> — head tag @ark-bloodhound-cli + the
// TERMS payload — in one atomic write so the watcher never sees a half-built doc.
// Returns the id the CLI blocks on and prints under.
// CRC: crc-RecallAgentBuilder.md | Seq: seq-bloodhound-cli.md#1.1 | R3021, R3031
func (b *RecallAgentBuilder) BloodhoundCLIOpen(payload string) (string, error) {
	id := strconv.FormatUint(uint64(b.ReserveNonce()), 10)
	p, err := ParseMatchSyntax(bloodhoundCLIResultTag + "=" + id)
	if err != nil {
		return "", err
	}
	b.db.pubsub.Subscribe(bloodhoundCLIResultSession(id), []*TagSub{{Kind: TagSubChunk, Predicate: p}})
	// Head tag carries a colon + the id as value — ark only extracts `@word:`
	// tags, so a colon-less head tag would never publish and the watcher would
	// never wake. The fixer subscribes bare, so any value matches.
	body := "@" + bloodhoundCLIRequestTag + ": " + id + "\n\n" + strings.TrimRight(payload, "\n") + "\n"
	path := bloodhoundCLIPrefix + id
	if err := SyncVoid(b.db, func(db *DB) error {
		_, e := db.AddTmpFile(path, "markdown", []byte(body))
		return e
	}); err != nil {
		return "", err
	}
	return id, nil
}

// BloodhoundCLIResult blocks until the hunt's result tag fires (R3021) — Luhmann's
// terminal `ark bloodhound add` (R3027) — or the timeout elapses, then returns the
// result doc's JSONL content (read from the event's own path, so S3 need not know
// where S4 writes it). ok is false on a clean timeout (no result), true with the
// content otherwise. R3021, R3022, R3029
// CRC: crc-RecallAgentBuilder.md | Seq: seq-bloodhound-cli.md#1.6 | R3021, R3029
func (b *RecallAgentBuilder) BloodhoundCLIResult(ctx context.Context, id string, timeout time.Duration) (jsonl string, ok bool, err error) {
	session := bloodhoundCLIResultSession(id)
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", false, nil // R3022: clean timeout
		}
		events := b.db.pubsub.Listen(session, min(remaining, bloodhoundCLIResultWindow))
		for _, ev := range events {
			data, derr := b.db.TmpContent(ev.Path)
			if derr != nil {
				continue // result doc vanished between publish and read
			}
			// Strip the @ark-bloodhound-cli-result head tag so the CLI prints
			// pure JSONL (R3029) — the tag is doc metadata, not output.
			return stripLeadingTag(string(data)), true, nil
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		default:
		}
	}
}

// --- Luhmann `next` drain tube (bloodhound-CLI S1: R3010–R3016, R3026) ---
//
// The orchestrator's sibling of RecallNext: a blocking long-poll the Luhmann
// skill backgrounds and re-invokes in a loop. Where RecallNext serves one
// session's secretary, `next` serves the single orchestrator seat — guarded by
// an in-memory ownership lease (R3012) — and drains the recall watcher's
// cross-session queue of curation tasks and supervisor directives (R3011).

// LuhmannNextKeepalive is the default idle window `ark luhmann next` blocks
// before returning a keepalive (R3016). It is a backgrounded-loop clock: the
// window stays under the ~1-hour main-agent prompt-cache TTL so a returning
// keepalive lands Luhmann's re-read inside the still-warm cache. Deliberately
// NOT the recall secretary's 90 s foreground number (recallNextKeepalive) — that
// is a ~120 s foreground-Bash auto-background artifact of a dedicated subagent.
// The tube subsumes the standalone `ark heartbeat` keepalive design.
var LuhmannNextKeepalive = 45 * time.Minute // R3016

// luhmannNextKeepaliveMax caps a caller-supplied --keepalive so it can never
// cross the 1-hour cache TTL that governs the backgrounded loop (R3016).
const luhmannNextKeepaliveMax = 55 * time.Minute // R3016

// luhmannNextQueueCap buffers the watcher→Luhmann handoff so a watcher push
// never blocks while the drain sits idle between invocations (R3011).
const luhmannNextQueueCap = 64 // R3011

// Ownership-error strings the skill's reflexes key on (R3014): they make the
// lease protocol self-converging with no persistence and no human arbitration.
const (
	luhmannErrNoSessions  = "there are no sessions"    // unowned server (post-bounce) → re-invoke --first
	luhmannErrNoOwnership = "you don't have ownership" // a foreign owner holds the seat → stand down
)

// LuhmannWork is one item the recall watcher pushes onto the server's nextQueue
// for the orchestrator's `next` drain (R3011). Two kinds: a curation task (a raw
// CLI-hunt finding — Path is the request-doc tmp:// path Luhmann refines and
// emits via `ark bloodhound add`) or a supervisor directive (Directive ∈
// stand-up/stop for the pooled Class — Luhmann spawns/stops via Task and records
// it). The keepalive is the third `next` return but is synthesized on the
// deadline, not queued.
type LuhmannWork struct { // R3011
	Kind      string // "curation" | "directive"
	Path      string // curation: request-doc tmp:// path
	Directive string // directive: "stand-up" | "stop"
	Class     string // directive: managed class (e.g. "bloodhound")
	Nonce     uint64 // directive "stop": which pool secretary to stop (R3019); 0 for stand-up
}

// luhmannNextMode is the ownership intent of one `next` call (R3013).
type luhmannNextMode int

const (
	luhmannModePlain luhmannNextMode = iota // R3013: validate, never claim
	luhmannModeFirst                        // R3013: claim if unowned or self
	luhmannModeForce                        // R3013: reclaim unconditionally
)

// luhmannNextDisposition tells the handler how to signal the CLI (R3013, R3014).
type luhmannNextDisposition int

const (
	luhmannDispOK      luhmannNextDisposition = iota // R3013: work or keepalive, exit 0
	luhmannDispExit                                  // R3014: you don't have ownership → stand down
	luhmannDispReclaim                               // R3014: there are no sessions → re-invoke --first
)

// claimLuhmann applies the in-memory ownership lease for one `next` call
// (R3012, R3013). Returns the disposition and, for the two non-OK cases, the
// error string the skill keys on (R3014). --force always claims; --first claims
// when unowned or already self, else stands the caller down; plain validates
// only — unowned yields the reclaim signal, a foreign owner stands the caller
// down. The lease is server memory (no persistence), so a bounce clears it and
// the reclaim path (R3014) re-establishes exactly one owner.
// CRC: crc-LuhmannCLI.md | R3012, R3013, R3014, R3026
func (srv *Server) claimLuhmann(session string, mode luhmannNextMode) (luhmannNextDisposition, string) {
	srv.luhmannMu.Lock()
	defer srv.luhmannMu.Unlock()
	switch mode {
	case luhmannModeForce:
		srv.luhmannOwner = session
		return luhmannDispOK, ""
	case luhmannModeFirst:
		if srv.luhmannOwner == "" || srv.luhmannOwner == session {
			srv.luhmannOwner = session
			return luhmannDispOK, ""
		}
		return luhmannDispExit, luhmannErrNoOwnership
	default: // plain
		switch srv.luhmannOwner {
		case "":
			return luhmannDispReclaim, luhmannErrNoSessions
		case session:
			return luhmannDispOK, ""
		default:
			return luhmannDispExit, luhmannErrNoOwnership
		}
	}
}

// LuhmannNext is the orchestrator's blocking drain tube (R3010): it applies the
// ownership lease (R3012–R3014, R3026 — owning the seat is the sole opt-in to
// serving CLI curation), then — once the caller owns the seat — blocks in a
// select over nextQueue, the keepalive timer, and ctx, returning one
// crank-handle body per call. Three return kinds (R3011): a curation task or a
// supervisor directive drained from nextQueue, or a keepalive on the idle
// deadline. The disposition drives the handler's headers (R3013, R3014).
// CRC: crc-LuhmannCLI.md | Seq: seq-bloodhound-cli.md#1.5 | R3010, R3011, R3016
func (srv *Server) LuhmannNext(ctx context.Context, session string, mode luhmannNextMode, keepalive time.Duration) (body string, disp luhmannNextDisposition, err error) {
	disp, msg := srv.claimLuhmann(session, mode)
	if disp != luhmannDispOK {
		return luhmannOwnershipPrompt(disp, msg, session), disp, nil
	}
	switch {
	case keepalive <= 0:
		keepalive = LuhmannNextKeepalive
	case keepalive > luhmannNextKeepaliveMax:
		keepalive = luhmannNextKeepaliveMax // R3016: never cross the 1h cache TTL
	}
	timer := time.NewTimer(keepalive)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", luhmannDispOK, ctx.Err()
	case w := <-srv.nextQueue:
		// For a curation task, inline the request doc's content (query + raw
		// findings) into the crank-handle: tmp:// paths aren't Read-able files,
		// and the doc is small. Best-effort — an unreadable/absent doc still
		// dispatches, naming the path for the `add` calls.
		content := ""
		if w.Kind == "curation" && srv.db != nil {
			if data, derr := srv.db.TmpContent(w.Path); derr == nil {
				content = stripLeadingTag(string(data))
			}
		}
		return luhmannWorkPrompt(session, w, content), luhmannDispOK, nil
	case <-timer.C:
		return luhmannKeepalivePrompt(session), luhmannDispOK, nil
	}
}

// luhmannOwnershipPrompt is the crank-handle for the two ownership signals
// (R3014). Reclaim: the server was reborn by a bounce and holds no owner, so
// re-claim with --first. Exit: another session owns the seat, so this Luhmann
// stands down — the losing side of a post-bounce --first race steps aside, so
// exactly one orchestrator survives.
// CRC: crc-LuhmannCLI.md | R3014
func luhmannOwnershipPrompt(disp luhmannNextDisposition, msg, session string) string {
	if disp == luhmannDispReclaim {
		return fmt.Sprintf("The ark server has no Luhmann owner (%s) — it was restarted. Reclaim the seat: run `~/.ark/ark luhmann next --session %s --first`.\n", msg, session)
	}
	return fmt.Sprintf("Another session owns the Luhmann seat (%s). Stand down — do not run `next` again; a single orchestrator serves CLI hunts.\n", msg)
}

// luhmannKeepalivePrompt is returned on the idle deadline (R3011, R3016): no
// work arrived within the window, so spend one cheap cached turn and re-invoke
// to hold the seat warm. Exit 0, like a work item — only the stand-down signal
// stops the loop.
// CRC: crc-LuhmannCLI.md | R3011, R3016
func luhmannKeepalivePrompt(session string) string {
	return fmt.Sprintf("No curation task or directive is pending. This is normal. Run `~/.ark/ark luhmann next --session %s` again now to keep the seat warm — do not stop.\n", session)
}

// luhmannWorkPrompt renders one drained work item as a crank-handle (R3011). A
// curation task inlines the request doc's content (query + raw findings — the
// tmp:// path is not a Read-able file) and pins the exact `ark bloodhound add`
// contract Luhmann emits per kept item, plus the terminal `--done` (R3025,
// R3027). A directive tells Luhmann to stand up or stop a pool secretary and
// record it. `content` is the inlined curation body (empty for a directive).
// CRC: crc-LuhmannCLI.md | Seq: seq-bloodhound-cli.md#1.5 | R3011, R3025, R3027
func luhmannWorkPrompt(session string, w LuhmannWork, content string) string {
	nextCmd := fmt.Sprintf("~/.ark/ark luhmann next --session %s", session)
	switch w.Kind {
	case "curation":
		return fmt.Sprintf(
			"A directed-search finding is ready to curate. The raw results:\n\n%s\n\n"+
				"Refine them — keep what genuinely answers the query, drop the noise. Emit each kept item with, one call per item:\n"+
				"  ~/.ark/ark bloodhound add --result %s --loc PATH:RANGE --note \"why it answers the query\" [--chunk \"excerpt\"]\n"+
				"When done — even if you kept nothing — finish with:\n"+
				"  ~/.ark/ark bloodhound add --result %s --done\n"+
				"which writes the result doc and notifies the waiting CLI.\n"+
				"Then run `%s` again.\n",
			content, w.Path, w.Path, nextCmd)
	case "directive":
		// Stand-up mints a fresh nonce (`reserve-nonce --luhmann`) and spawns;
		// stop names the nonce of an idle-past-cooldown secretary to retire (R3019).
		if w.Directive == "stop" {
			return fmt.Sprintf(
				"Supervisor directive: stop the `%s` pool secretary with nonce %d (idle past its cooldown). Stop its Task and record it with `~/.ark/ark luhmann exit-record --class %s --nonce %d --reason context-limit`.\n"+
					"Then run `%s` again.\n",
				w.Class, w.Nonce, w.Class, w.Nonce, nextCmd)
		}
		return fmt.Sprintf(
			"Supervisor directive: stand up another `%s` pool secretary. Reserve its nonce with `~/.ark/ark connections recall reserve-nonce --luhmann`, spawn it via the Task tool with `--session <your-session>-<nonce>` in its prompt, and record it with `~/.ark/ark luhmann spawn-record --class %s --nonce <nonce> --task-id <id>`.\n"+
				"Then run `%s` again.\n",
			w.Class, w.Class, nextCmd)
	default:
		return fmt.Sprintf("Unrecognized work kind %q — skip it and run `%s` again.\n", w.Kind, nextCmd)
	}
}

// luhmannHub is the watcher-as-Fixer's view of the orchestrator seat (S1 state
// on Server): who owns it, and the queue to push curation tasks + supervisor
// directives onto. Narrow interface so the Fixer stays decoupled from the full
// Server and is testable with a fake hub. R3011, R3020, R3024, R3025
type luhmannHub interface {
	LuhmannOwner() string            // R3012, R3020: seat owner ("" = no orchestrator)
	EnqueueLuhmann(LuhmannWork) bool // R3024, R3025: push work; false if the queue is full
}

// LuhmannOwner returns the session holding the Luhmann seat, or "" when
// unowned (R3012). The watcher gates CLI hunts on a live orchestrator (R3020)
// and composes the pool routing key `<luhmannOwner>-<nonce>` from it (R3032).
// CRC: crc-LuhmannCLI.md | R3012, R3020, R3032
func (srv *Server) LuhmannOwner() string {
	srv.luhmannMu.Lock()
	defer srv.luhmannMu.Unlock()
	return srv.luhmannOwner
}

// EnqueueLuhmann pushes a work item onto nextQueue for the `next` drain
// (R3011), non-blocking: returns false when the queue is full so the watcher
// can leave a hunt pending rather than block the Fixer loop. This is
// nextQueue's producer side (the drain is LuhmannNext). R3024, R3025
// CRC: crc-LuhmannCLI.md | R3011, R3024, R3025
func (srv *Server) EnqueueLuhmann(w LuhmannWork) bool {
	select {
	case srv.nextQueue <- w:
		return true
	default:
		return false
	}
}
