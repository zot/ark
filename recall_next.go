package ark

// CRC: crc-RecallAgentBuilder.md | Seq: seq-recall-agent.md#3 | R2857, R2858

import (
	"context"
	"fmt"
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
func (b *RecallAgentBuilder) RecallNext(ctx context.Context, nonce uint32, contextLimit int) (body string, exit bool, err error) {
	session := fmt.Sprintf("recall-%d", nonce)

	// Idempotent subscribe to @ark-recall-curate (bare). Subscribe
	// appends, so guard with SubCount to keep later calls no-ops.
	if b.db.pubsub.SubCount(session) == 0 {
		p, perr := ParseMatchSyntax("ark-recall-curate")
		if perr != nil {
			return "", false, perr
		}
		b.db.pubsub.Subscribe(session, []*TagSub{{Kind: TagSubChunk, Predicate: p}})
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
		fire, content, ok := b.lowestPendingCuration()
		if ok {
			return recallDocPrompt(fire, nonce, content), false, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return recallKeepalivePrompt(nonce), false, nil
		}
		window := min(recallNextListenWindow, remaining)
		if !b.waitForWork(ctx, session, window) {
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
func (b *RecallAgentBuilder) lowestPendingCuration() (fire uint64, content string, ok bool) {
	files, err := Sync(b.db, func(db *DB) ([]string, error) { return db.Files() })
	if err != nil {
		return 0, "", false
	}
	var path string
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
		if b.db.pubsub.SubscriberCount("ark-recall-result", session) == 0 {
			continue
		}
		if !ok || f < fire {
			path, fire, ok = p, f, true
		}
	}
	if !ok {
		return 0, "", false
	}
	data, err := b.db.TmpContent(path)
	if err != nil {
		// The doc vanished between listing and read (e.g. a concurrent
		// close); treat as nothing pending — the caller re-checks.
		return 0, "", false
	}
	return fire, string(data), true
}

// recallDocPrompt wraps a curation doc as a crank-handle (R2858): the
// content, then the next actions the agent should take.
func recallDocPrompt(fire uint64, nonce uint32, content string) string {
	var sb strings.Builder
	sb.WriteString(content)
	fmt.Fprintf(&sb,
		"\n\n---\nJudge the candidates above for genuine fit with their source paragraph. "+
			"For each chunk worth showing: `~/.ark/ark connections recall surface %d -chunk <id> -reason \"...\"`. "+
			"For each tag worth attaching: `~/.ark/ark connections recall recommend %d -chunk <id> -tag @t[:v] -reason \"...\"`. "+
			"Then `~/.ark/ark connections recall close %d --nonce %d`. "+
			"Then run `~/.ark/ark connections recall next %d` again.\n",
		fire, fire, fire, nonce, nonce)
	return sb.String()
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
// then what to do with it and the instruction to listen again.
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
	fmt.Fprintf(&sb,
		"\n\n---\nThis is ambient recall — material from the corpus related to the current conversation. "+
			"`## Surface:` items are chunks worth showing the user; `## Recommend:` items are tags worth attaching. "+
			"Decide what genuinely helps the user right now — you have final say; skip anything stale or off-topic. "+
			"If the user rejects a recommended tag, run `~/.ark/ark connections recall reject-derived`. "+
			"Then run `~/.ark/ark connections recall listen --session %s` again to keep receiving.\n",
		session)
	return sb.String()
}
