package ark

// CRC: crc-Librarian.md | Seq: seq-recall.md#1.4 | R2617, R2618, R2620, R2622, R2623, R2624, R2625, R2626, R2629, R2634, R2639, R2640, R2641, R2643, R2644

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zot/microfts2"
)

// RecallOpts configures top-K retrieval, content loading, and the
// tagless-chunk filter.
// CRC: crc-Librarian.md | R2627, R2647, R2655, R2660, R2667, R2668, R2677
type RecallOpts struct {
	K              int  `json:"k"`
	IncludeContent bool `json:"includeContent"`
	// KeepTagless retains chunks that carry no V records. The default
	// (false) drops them during scoring so the substrate returns top-K
	// chunks that can actually contribute tag information downstream.
	// R2647
	KeepTagless bool `json:"keepTagless,omitempty"`
	// Session is the conversation/session ID whose discussed-tag set
	// the substrate should load via Store.ListDiscussed and apply as
	// part of the exclusion set. Empty disables the session load.
	// R2655
	Session string `json:"session,omitempty"`
	// Discussed is the caller-supplied exclusion set. Each entry
	// matches by (Tag, Value): empty Value means any value under
	// that name; non-empty Value means exact pair. When combined
	// with Session, the substrate takes the union. Empty slice
	// disables the explicit-list contribution. R2655, R2660
	Discussed []Discussed `json:"discussed,omitempty"`
	// DiscussedTTL is the TTL applied when loading the Session's RD
	// records (lazy expiry). Zero falls back to the [recall]
	// discussed_ttl default (24h). R2659
	DiscussedTTL time.Duration `json:"discussedTtl,omitempty"`
	// Propose runs the compute-for-display derivation pass on the
	// substrate's full scored chunk set (#36). It computes per-chunk tag
	// proposals and surfaces them via RecalledChunk.ProposedTags; it
	// authors nothing and writes no RC/RF. The caller's surfaced result
	// is unchanged except by the ProposedTags enrichment.
	// R2667, R2668, R3079
	Propose bool `json:"propose,omitempty"`
	// ConversationChunks is the optional live-conversation chunk-ID set
	// (source-seed chunk + recent-N turn chunks) folded into the --propose
	// compute with A66 self-exclusion bypassed, so the conversation earns
	// its own tag proposals. Watcher-populated; empty for a directed
	// bloodhound seed. These chunks are own-session, so the watcher renders
	// them tag-only (R2869): their proposals surface for the calling agent
	// to author, their content never surfaces.
	// R3082
	ConversationChunks []uint64 `json:"conversationChunks,omitempty"`
}

// discussedExclusion is the per-chunk lookup table built from the
// union of Session and Discussed. anyValue[name] = true means "skip
// any value under this name"; exact[name][value] = true means "skip
// this exact pair." R2657
type discussedExclusion struct {
	anyValue map[string]bool
	exact    map[string]map[string]bool
	active   bool
}

func newDiscussedExclusion(entries []Discussed) discussedExclusion {
	if len(entries) == 0 {
		return discussedExclusion{}
	}
	x := discussedExclusion{
		anyValue: make(map[string]bool),
		exact:    make(map[string]map[string]bool),
		active:   true,
	}
	for _, e := range entries {
		if e.Value == "" {
			x.anyValue[e.Tag] = true
			continue
		}
		m, ok := x.exact[e.Tag]
		if !ok {
			m = make(map[string]bool)
			x.exact[e.Tag] = m
		}
		m[e.Value] = true
	}
	return x
}

// excludes reports whether the (tag, value) pair is in the
// exclusion set per the matching rule. R2657
func (x discussedExclusion) excludes(tag, value string) bool {
	if !x.active {
		return false
	}
	if x.anyValue[tag] {
		return true
	}
	if m, ok := x.exact[tag]; ok && m[value] {
		return true
	}
	return false
}

// filter returns the surviving tags after stripping exclusion-set
// matches. R2656
func (x discussedExclusion) filter(tags []TagValue) []TagValue {
	if !x.active || len(tags) == 0 {
		return tags
	}
	out := tags[:0:0]
	for _, t := range tags {
		if !x.excludes(t.Tag, t.Value) {
			out = append(out, t)
		}
	}
	return out
}

// RecallResult holds the matched chunks and any warning message.
// CRC: crc-Librarian.md | R2617
type RecallResult struct {
	Chunks  []RecalledChunk `json:"chunks"`
	Warning string          `json:"warning,omitempty"`
}

// RecalledChunk is one retrieved chunk with similarity scores and metadata.
// CRC: crc-Librarian.md | R2624, R2686, R2907, R2909
type RecalledChunk struct {
	ChunkID      uint64         `json:"chunkID"`
	Path         string         `json:"path"`
	Range        string         `json:"range"`
	Score        float64        `json:"score"`
	PerSubstrate ChunkSubstrate `json:"perSubstrate"`
	// Cell is the 2×2 grid cell this chunk was surfaced in:
	// "{main|conversation}-{meaning|tags}" (R2907), logged per-result
	// for data-driven tuning (R2909). Empty until the 2×2 allocation.
	Cell    string      `json:"cell,omitempty"`
	Tags    []RecallTag `json:"tags"`
	Content string      `json:"content,omitempty"`
	// ProposedTags carries derived-tag candidates for this chunk,
	// ordered by chunk-EC ↔ tag-ED cosine similarity desc. Populated
	// only when RecallOpts.Propose is true and the chunk has at least
	// one accumulated RC record. R2684, R2685, R2686
	ProposedTags []string `json:"proposedTags,omitempty"`
	// ProposedTagScores is the chunk-EC ↔ tag-ED cosine for each
	// entry in ProposedTags, same length and ordering. Surfaced in
	// the recall stencil as `tagname (0.NN)` so the floor can be
	// tuned by eye. R2743
	ProposedTagScores []float64 `json:"proposedTagScores,omitempty"`
}

// RecallTag is a tag name + value pair for JSON serialization.
// CRC: crc-Librarian.md | R2624
type RecallTag struct {
	Tag   string `json:"tag"`
	Value string `json:"value,omitempty"`
}

// ChunkSubstrate carries similarity scores per substrate. The text pair
// (VectorEC, TrigramEC) is the content axis; the tag pair (TagVector,
// TagTrigram) is the value→chunk tag axis (R2905, R2906).
// CRC: crc-Librarian.md | R2620, R2905, R2906
type ChunkSubstrate struct {
	VectorEC   float64 `json:"vectorEc"`
	TrigramEC  float64 `json:"trigramEc"`
	TagVector  float64 `json:"tagVector"`
	TagTrigram float64 `json:"tagTrigram"`
}

// RenderRecallChunks writes the per-chunk markdown block for a recall
// result, matching the stencil documented in specs/recall.md. The
// caller writes the surrounding header (`## Chunks`, `## Recalled
// chunks`, etc.). One blank line precedes each chunk.
//
// Shared between `ark connections recall` (via cmd/ark/main.go's
// printRecallResult) and the simple-recall watcher (recall_watcher.go).
// CRC: crc-Librarian.md | R2645, R2684, R2685, R2704, R2743, R2906, R2907, R2909
func RenderRecallChunks(out io.Writer, chunks []RecalledChunk) {
	for _, chunk := range chunks {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "- @chunk-id: %d\n", chunk.ChunkID)
		fmt.Fprintf(out, "  @chunk-path: %s\n", chunk.Path)
		fmt.Fprintf(out, "  @chunk-range: %s\n", chunk.Range)
		fmt.Fprintf(out, "  @chunk-score: %.2f\n", chunk.Score)
		if chunk.Cell != "" {
			fmt.Fprintf(out, "  @chunk-cell: %s\n", chunk.Cell)
		}
		fmt.Fprintf(out, "  @chunk-evidence-vector-ec: %.2f\n", chunk.PerSubstrate.VectorEC)
		fmt.Fprintf(out, "  @chunk-evidence-trigram-ec: %.2f\n", chunk.PerSubstrate.TrigramEC)
		fmt.Fprintf(out, "  @chunk-evidence-tag-vector: %.2f\n", chunk.PerSubstrate.TagVector)
		fmt.Fprintf(out, "  @chunk-evidence-tag-trigram: %.2f\n", chunk.PerSubstrate.TagTrigram)

		names := make([]string, 0, len(chunk.Tags))
		for _, t := range chunk.Tags {
			names = append(names, t.Tag)
		}
		fmt.Fprintf(out, "  @chunk-tags: %s\n", strings.Join(names, ", "))
		if len(chunk.ProposedTags) > 0 {
			parts := make([]string, len(chunk.ProposedTags))
			for j, name := range chunk.ProposedTags {
				if j < len(chunk.ProposedTagScores) {
					parts[j] = fmt.Sprintf("%s (%.2f)", name, chunk.ProposedTagScores[j])
				} else {
					parts[j] = name
				}
			}
			fmt.Fprintf(out, "  @chunk-proposed-tags: %s\n", strings.Join(parts, ", "))
		}
		for _, t := range chunk.Tags {
			if t.Value != "" {
				fmt.Fprintf(out, "  - @chunk-tag-value: %s: %s\n", t.Tag, t.Value)
			}
		}

		if chunk.Content != "" {
			lines := strings.Split(chunk.Content, "\n")
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			for _, line := range lines {
				fmt.Fprintf(out, "  > %s\n", line)
			}
		}
	}
}

// chunkScoresAcc is the per-chunk accumulator built up across the
// substrate passes inside Recall. Internal to the recall package
// flow; promoted to package level so the derivation pass can read
// it (R2668).
type chunkScoresAcc struct {
	vectorEC  float64
	trigramEC float64
	// tagVector / tagTrigram are the tag-axis components (R2906): the max
	// EV-cosine and max value-string-trigram across the top tag values
	// whose chunks include this one (R2905). Zero when no tag matched.
	tagVector  float64
	tagTrigram float64
	// tags is the post-filter tag list (discussed entries stripped).
	// Populated lazily on first encounter so the result-build phase
	// doesn't re-query AllTagsForChunk. Nil means "not yet looked
	// up" — only possible when the exclusion set is inactive AND
	// effective KeepTagless is true (admit short-circuited the lookup).
	tags []TagValue
	// hadTags records whether the chunk originally had any tags
	// (pre-exclusion). Used by the result-build phase's caller-
	// surfacing filter when admitTagless was forced true by Propose.
	// R2668
	hadTags bool
	// alreadyOn is the set of tagnames present on the chunk via any
	// source (inline F-records or @ext routing), pre-discussed-
	// exclusion. Populated only when Propose=true; used by the
	// derivation pass to filter candidates already attached
	// (R2671, R2672 unified via AllTagsForChunk's union).
	alreadyOn map[string]bool
}

// Recall retrieves the top-K chunks from the corpus relevant to a given set of inputs.
// CRC: crc-Librarian.md | Seq: seq-recall.md#1.3 | R2617, R2618, R2620, R2622, R2623, R2624, R2625, R2626, R2634, R2639, R2640, R2641, R2643, R2644, R2655, R2656, R2657, R2658, R2905, R2906, R3082
func (l *Librarian) Recall(inputs []ConnectionsInput, opts RecallOpts) (*RecallResult, error) {
	// 1. Clamping K. R2641
	k := opts.K
	if k <= 0 {
		k = 20
	}
	if k > 200 {
		k = 200
	}

	// 2. Normalization. R2618, R2639, R2640
	normInputs, _, err := l.normalizeInputs(inputs, true)
	if err != nil {
		return nil, err
	}

	embedAvail := l.EmbeddingAvailable()
	var warning string
	if !embedAvail {
		warning = "embedding unavailable"
	}

	// 3. Self-chunk exclusion map setup. R2623
	selfChunks := make(map[uint64]bool)
	for _, in := range normInputs {
		if in.chunkID != 0 {
			selfChunks[in.chunkID] = true
		}
	}

	// 3b. Build the discussed-tag exclusion set: union of the caller's
	// explicit list and the session's RD records (when opts.Session is
	// set). R2655
	discussed := opts.Discussed
	if opts.Session != "" {
		ttl := opts.DiscussedTTL
		if ttl == 0 {
			ttl = 24 * time.Hour
		}
		sessionEntries, lerr := l.db.store.ListDiscussed(opts.Session, 0, ttl)
		if lerr == nil {
			discussed = append(discussed, sessionEntries...)
		}
	}
	exclusion := newDiscussedExclusion(discussed)

	scoresMap := make(map[uint64]*chunkScoresAcc)

	// admitTagless decides whether the substrate admits chunks with
	// no V records. The caller's KeepTagless is the surfacing intent;
	// Propose forces admission so the derivation pass can also process
	// tagless chunks (R2668). The result-build phase re-applies the
	// caller's KeepTagless to drop tagless chunks from the surfaced
	// output.
	admitTagless := opts.KeepTagless || opts.Propose

	// admit returns the accumulator for chunkID, creating it on first
	// encounter. The tag fetch + discussed-filter decision is:
	//
	//   - exclusion inactive AND admitTagless AND !Propose: skip tag
	//     fetch entirely (fast path; result-build fills in lazily).
	//   - otherwise: fetch tags, capture hadTags + alreadyOn (Propose),
	//     apply the discussed filter, drop the chunk if either (a) it
	//     was originally tagless and admitTagless is false, or (b) it
	//     had tags but the exclusion stripped them all (R2656).
	//
	// The discussed filter runs before the admitTagless decision; `-all`
	// does not override discussed exclusion (R2658). When Propose is
	// set, admitTagless is forced true but the result-build phase will
	// still apply the caller's KeepTagless to the surfaced output.
	//
	// R2647, R2656, R2657, R2658, R2668
	needFetch := exclusion.active || !admitTagless || opts.Propose
	admit := func(chunkID uint64) *chunkScoresAcc {
		if acc, ok := scoresMap[chunkID]; ok {
			return acc
		}
		acc := &chunkScoresAcc{}
		if needFetch {
			rawTags, err := l.db.store.AllTagsForChunk(chunkID)
			if err != nil {
				return nil
			}
			acc.hadTags = len(rawTags) > 0
			if opts.Propose {
				acc.alreadyOn = make(map[string]bool, len(rawTags))
				for _, tv := range rawTags {
					acc.alreadyOn[tv.Tag] = true
				}
			}
			filtered := exclusion.filter(rawTags)
			switch {
			case !acc.hadTags && !admitTagless:
				return nil // originally tagless, surfacing forbids
			case acc.hadTags && len(filtered) == 0:
				return nil // emptied by exclusion — drops regardless (R2658)
			}
			acc.tags = filtered
		}
		scoresMap[chunkID] = acc
		return acc
	}

	// Tag-axis value universe (R2905), scanned once: every (tag, value)
	// attached to chunks, plus their EV vectors when embeddings are
	// available. The trigram leg works without a model (seq-recall #1.16).
	tagUniverse, _ := l.db.store.ScanVRecordTvids()
	var tagValueVecs map[uint64][]float32
	if embedAvail {
		tagValueVecs, _ = l.db.store.ScanTagValueEmbeddings()
	}

	// 4. Substrate passes. R2620, R2622, R2643, R2644
	// inputQueries collects each input's query vector + trigram set for the
	// chat funnel (R2910), which re-scores conversation sub-chunks below.
	var inputQueries []inputQuery
	for _, in := range normInputs {
		var queryVec []float32
		var queryText string

		if in.text != "" {
			queryText = in.text
			if embedAvail {
				v, err := l.EmbedQuery(in.text)
				if err != nil {
					// Treat embed failure as model unavailable for this run.
					warning = "embedding unavailable"
				} else {
					queryVec = v
				}
			}
		} else if in.chunkID != 0 {
			if embedAvail {
				v, err := l.db.store.ReadChunkEmbedding(in.chunkID)
				if err != nil {
					// Degrade gracefully, keep queryVec nil.
				} else {
					queryVec = v
				}
			}
			txt, terr := substrateChunkText(l.db, in.chunkID)
			if terr == nil {
				queryText = txt
			}
		}

		iq := inputQuery{vec: queryVec}
		if queryText != "" {
			iq.tris = queryTrigramSet(queryText)
		}
		inputQueries = append(inputQueries, iq)

		// Vector-EC pass
		if len(queryVec) > 0 {
			scores, err := l.SearchChunks(queryVec, 50) // substrateInternalK = 50
			if err == nil {
				for _, cs := range scores {
					if selfChunks[cs.ChunkID] {
						continue
					}
					acc := admit(cs.ChunkID)
					if acc == nil {
						continue // tagless chunk and KeepTagless=false
					}
					normalized := normalizeCos(cs.Score)
					if normalized > acc.vectorEC {
						acc.vectorEC = normalized
					}
				}
			}
		}

		// Trigram-EC pass: candidates from SearchFuzzy are re-scored as
		// Jaccard(Tq, Tc) with a query-coverage floor. R2643, R2644
		if queryText != "" && l.db.search != nil {
			hits, err := l.db.SearchFuzzy(queryText, SearchOpts{K: 50})
			if err == nil {
				queryTris := queryTrigramSet(queryText)
				for _, h := range hits {
					cid, ok := resolveSearchEntryChunkID(l.db, h)
					if !ok {
						continue
					}
					if selfChunks[cid] {
						continue
					}
					acc := admit(cid)
					if acc == nil {
						continue // tagless chunk and KeepTagless=false
					}
					chunkText, terr := substrateChunkText(l.db, cid)
					if terr != nil {
						continue
					}
					score := trigramJaccardWithFloor(queryTris, chunkText)
					if score == 0 {
						continue
					}
					if score > acc.trigramEC {
						acc.trigramEC = score
					}
				}
			}
		}

		// Tag-axis pass (R2905, R2906): value -> chunk retrieval. Score
		// each attached tag-value against this input — EV cosine (vector
		// leg) and on-the-fly trigram-Jaccard of the value string (trigram
		// leg) — take the top values and pull the chunks carrying them via
		// V records, each chunk taking the value's scores as its tag-axis
		// components. Discussed values are skipped so they cannot drive
		// retrieval (R2658).
		if len(tagUniverse) > 0 && (queryText != "" || len(queryVec) > 0) {
			var queryTris map[uint32]bool
			if queryText != "" {
				queryTris = queryTrigramSet(queryText)
			}
			type tagValScore struct {
				tag, value     string
				vec, tri, best float64
			}
			scoredVals := make([]tagValScore, 0, len(tagUniverse))
			for tvid, ta := range tagUniverse {
				if exclusion.excludes(ta.Tag, ta.Value) {
					continue
				}
				var vec, tri float64
				if len(queryVec) > 0 {
					if ev, ok := tagValueVecs[tvid]; ok && len(ev) == len(queryVec) {
						vec = normalizeCos(cosineSimilarity(queryVec, ev))
					}
				}
				if queryTris != nil {
					tri = trigramJaccardWithFloor(queryTris, ta.Value)
				}
				best := max(vec, tri)
				if best <= 0 {
					continue
				}
				scoredVals = append(scoredVals, tagValScore{ta.Tag, ta.Value, vec, tri, best})
			}
			sort.Slice(scoredVals, func(i, j int) bool { return scoredVals[i].best > scoredVals[j].best })
			if len(scoredVals) > 50 { // tag-axis internal K, mirrors the content passes
				scoredVals = scoredVals[:50]
			}
			for _, sv := range scoredVals {
				chunkIDs, cerr := l.db.store.TagValueChunks(sv.tag, sv.value)
				if cerr != nil {
					continue
				}
				for _, cid := range chunkIDs {
					if selfChunks[cid] {
						continue
					}
					acc := admit(cid)
					if acc == nil {
						continue
					}
					if sv.vec > acc.tagVector {
						acc.tagVector = sv.vec
					}
					if sv.tri > acc.tagTrigram {
						acc.tagTrigram = sv.tri
					}
				}
			}
		}
	}

	// 4a. Inject the live-conversation chunk set (source seed + recent-N
	// turns) into the scored map so the --propose compute earns proposals on
	// the conversation itself. admit() does not consult selfChunks, so A66
	// self-exclusion is bypassed by construction. These chunks carry no
	// search-hit scores, so the 2×2 does not surface them; section 5d appends
	// the ones that earned proposals (rendered tag-only by the watcher). R3082
	if opts.Propose {
		for _, cid := range opts.ConversationChunks {
			admit(cid)
		}
	}

	// 4b. Derivation pass (when --propose is set and embeddings are
	// available). Runs on the full scored set (tagless chunks included
	// via admitTagless). Compute-for-display (#36): computes per-chunk
	// proposals and returns them transiently for enrichProposedTags; it
	// writes no RC/RF records and authors nothing. R2667, R3079
	var derivedWork map[uint64]*chunkWork
	if opts.Propose && embedAvail {
		dw, dErr := l.runDerivationPass(scoresMap)
		if dErr != nil {
			log.Printf("recall: derivation pass: %v", dErr)
		}
		derivedWork = dw
	}

	// 5. Build the candidate set with the metadata the 2×2 allocation
	// needs — source via file strategy (chat-jsonl = conversation), byte
	// size for the tiebreak. Conversation splits by axis: its tag match
	// surfaces as the whole turn (tags attach to turns, R2905), its meaning
	// is funneled to sub-chunks (R2910); main-corpus chunks surface whole.
	// Tag + content resolution is deferred to the surfaced set. R2907, R2910
	var rc RecallConfig
	if cfg := l.db.Config(); cfg != nil {
		rc = cfg.Recall
	}
	var candidates []recallCandidate
	var convTurns []recallCandidate // conversation turns whose meaning the funnel refines
	strategyByPath := make(map[string]string)
	for cid, acc := range scoresMap {
		// Caller-surfacing filter: when Propose forced admitTagless,
		// drop tagless chunks from the surfaced result if the caller's
		// KeepTagless is false. The derivation pass already processed
		// them (their RC/RF records live in LMDB). R2668
		if !opts.KeepTagless && opts.Propose && needFetch && !acc.hadTags {
			continue
		}

		info, err := l.db.ChunkInfo(cid)
		if err != nil {
			continue
		}

		strat, ok := strategyByPath[info.Path]
		if !ok {
			strat = l.db.FileStrategy(info.Path)
			strategyByPath[info.Path] = strat
		}
		size := info.ByteEnd - info.ByteStart

		if strat == "chat-jsonl" {
			// Tag axis surfaces the whole turn; meaning goes to the funnel.
			if acc.tagVector > 0 || acc.tagTrigram > 0 {
				candidates = append(candidates, recallCandidate{
					acc:     &chunkScoresAcc{tagVector: acc.tagVector, tagTrigram: acc.tagTrigram},
					key:     info.Path + ":" + info.Range,
					chunkID: cid,
					path:    info.Path,
					rangeID: info.Range,
					fileID:  info.FileID,
					source:  sourceConversation,
					size:    size,
				})
			}
			if acc.vectorEC > 0 || acc.trigramEC > 0 {
				convTurns = append(convTurns, recallCandidate{
					acc:     acc,
					chunkID: cid,
					path:    info.Path,
					rangeID: info.Range,
					fileID:  info.FileID,
					source:  sourceConversation,
					size:    size,
				})
			}
			continue
		}

		candidates = append(candidates, recallCandidate{
			acc:     acc,
			key:     info.Path + ":" + info.Range,
			chunkID: cid,
			path:    info.Path,
			rangeID: info.Range,
			fileID:  info.FileID,
			source:  sourceMain,
			size:    size,
		})
	}

	// 5a. Funnel conversation meaning into sub-chunk candidates (R2910;
	// R2912 gate). Trigram-only when no model is available.
	if len(convTurns) > 0 {
		candidates = append(candidates,
			l.chatFunnel(convTurns, inputQueries, rc.EffectiveChatFunnelGate(), embedAvail)...)
	}

	// 5b. Allocate across the 2×2 (source × axis) grid — per-cell ranking
	// with the size tiebreak, ≤2/file within a cell, dedup dual-members to
	// their stronger cell, backfill starved cells to the 4×N target, then
	// cap at the caller's K. R2907, R2908
	perCell := rc.EffectivePerCellCount()
	surfaced := allocate2x2(candidates, perCell, k)

	// 5c. Resolve tags + content for the surfaced set only, then build the
	// result chunks. R2624, R2625
	cache := l.db.fts.NewChunkCache()
	recalled := make([]RecalledChunk, 0, len(surfaced))
	for _, sc := range surfaced {
		acc := sc.cand.acc
		// Reuse the cached tag lookup from admit when present
		// (KeepTagless=false path); otherwise query now. R2647
		allTags := acc.tags
		if allTags == nil {
			allTags, _ = l.db.store.AllTagsForChunk(sc.cand.chunkID)
		}
		var tags []RecallTag
		for _, tv := range allTags {
			tags = append(tags, RecallTag{
				Tag:   tv.Tag,
				Value: tv.Value,
			})
		}

		var content string
		if opts.IncludeContent {
			if sc.cand.content != "" {
				content = sc.cand.content // pre-resolved chat sub-chunk text (R2910)
			} else if txt, ok := cache.ChunkText(sc.cand.path, sc.cand.rangeID); ok {
				content = string(txt)
			}
		}

		recalled = append(recalled, RecalledChunk{
			ChunkID: sc.cand.chunkID,
			Path:    sc.cand.path,
			Range:   sc.cand.rangeID,
			Score:   sc.cand.overallScore(),
			Cell:    sc.cell,
			PerSubstrate: ChunkSubstrate{
				VectorEC:   acc.vectorEC,
				TrigramEC:  acc.trigramEC,
				TagVector:  acc.tagVector,
				TagTrigram: acc.tagTrigram,
			},
			Tags:    tags,
			Content: content,
		})
	}

	// 5d. Append the injected live-conversation chunks that earned computed
	// proposals but were not surfaced by the 2×2 (they carry no search-hit
	// scores). Their content is never surfaced — they are own-session,
	// rendered tag-only by the watcher (R2869); enrichProposedTags fills
	// ProposedTags below. R3082
	if opts.Propose && len(opts.ConversationChunks) > 0 {
		inResult := make(map[uint64]bool, len(recalled))
		for i := range recalled {
			inResult[recalled[i].ChunkID] = true
		}
		for _, cid := range opts.ConversationChunks {
			if inResult[cid] {
				continue
			}
			cw := derivedWork[cid]
			if cw == nil || len(cw.proposals) == 0 {
				continue // no computed proposals to surface
			}
			info, err := l.db.ChunkInfo(cid)
			if err != nil {
				continue
			}
			var tags []RecallTag
			if allTags, aerr := l.db.store.AllTagsForChunk(cid); aerr == nil {
				for _, tv := range allTags {
					tags = append(tags, RecallTag{Tag: tv.Tag, Value: tv.Value})
				}
			}
			recalled = append(recalled, RecalledChunk{
				ChunkID: cid,
				Path:    info.Path,
				Range:   info.Range,
				Tags:    tags,
			})
			inResult[cid] = true
		}
	}

	// 6. Enrich each surfaced chunk with this call’s computed derived-tag
	// proposals, ordered by similarity desc. R3080, R2684, R2685, R2686
	if opts.Propose && embedAvail {
		l.enrichProposedTags(recalled, derivedWork)
	}

	return &RecallResult{
		Chunks:  recalled,
		Warning: warning,
	}, nil
}

// recallSource classifies a candidate's corpus for the 2×2 grid:
// conversation (chat-jsonl strategy) vs the main corpus (everything
// else). R2907
type recallSource int

const (
	sourceMain recallSource = iota
	sourceConversation
)

// recallAxis is one of the grid's two scoring axes. R2907
type recallAxis int

const (
	axisMeaning recallAxis = iota
	axisTags
)

// Recall 2×2 cell labels, "{source}-{axis}" (R2907), surfaced on each
// result as RecalledChunk.Cell for per-result tuning logs (R2909).
const (
	cellMainMeaning         = "main-meaning"
	cellMainTags            = "main-tags"
	cellConversationMeaning = "conversation-meaning"
	cellConversationTags    = "conversation-tags"
)

// recallCandidate is a scored chunk plus the metadata the 2×2 allocation
// needs. The four similarity components live on acc. R2907
type recallCandidate struct {
	acc *chunkScoresAcc
	// key is the dedup/identity = the surfaced-unit locator (path:range,
	// or path:range:N for a chat sub-chunk). A conversation turn can
	// surface both as a whole turn (tags) and as a sub-chunk (meaning),
	// so identity is the surfaced unit, not the source chunkID. R2908, R2910
	key     string
	chunkID uint64
	path    string
	rangeID string
	fileID  uint64
	source  recallSource
	size    uint64
	// content is the pre-resolved text for chat sub-chunk candidates
	// (R2910); empty for ordinary chunks, whose content is resolved from
	// the cache in the surfaced set.
	content string
}

// axisScore returns the candidate's score on an axis and whether the
// vector substrate (vs trigram) produced it — the latter drives the size
// tiebreak (vector → larger first, trigram → smaller first; SIGNAL Q2.1).
func (c recallCandidate) axisScore(axis recallAxis) (score float64, vectorWon bool) {
	if axis == axisMeaning {
		if c.acc.vectorEC >= c.acc.trigramEC {
			return c.acc.vectorEC, true
		}
		return c.acc.trigramEC, false
	}
	if c.acc.tagVector >= c.acc.tagTrigram {
		return c.acc.tagVector, true
	}
	return c.acc.tagTrigram, false
}

// overallScore is the max across all four components — the flat
// presentation score (the per-result ordering, distinct from the
// per-cell axis ranking). R2906
func (c recallCandidate) overallScore() float64 {
	return max(c.acc.vectorEC, c.acc.trigramEC, c.acc.tagVector, c.acc.tagTrigram)
}

// surfacedChunk pairs a chosen candidate with the cell it filled. R2907
type surfacedChunk struct {
	cand recallCandidate
	cell string
}

// cellLabel composes the "{source}-{axis}" cell label. R2907
func cellLabel(source recallSource, axis recallAxis) string {
	switch {
	case source == sourceMain && axis == axisMeaning:
		return cellMainMeaning
	case source == sourceMain:
		return cellMainTags
	case axis == axisMeaning:
		return cellConversationMeaning
	default:
		return cellConversationTags
	}
}

// allocate2x2 distributes candidates across the 2×2 (source × axis) grid.
// Each cell ranks its eligible candidates (that axis's score > 0) by
// score with the size tiebreak, capped at ≤2 chunks per file within the
// cell. A candidate's primary cell is its stronger axis within its source
// (R2908 dedup); primaries fill first, up to perCell each. Remaining slots
// toward the 4×perCell target are backfilled round-robin across the cells
// from each cell's next-best unsurfaced candidate (R2908). The surfaced
// set is returned sorted by overall score desc, capped at k.
// CRC: crc-Librarian.md | R2907, R2908
func allocate2x2(cands []recallCandidate, perCell, k int) []surfacedChunk {
	type cellKey struct {
		source recallSource
		axis   recallAxis
	}
	cells := []cellKey{
		{sourceMain, axisMeaning}, {sourceMain, axisTags},
		{sourceConversation, axisMeaning}, {sourceConversation, axisTags},
	}

	// Per-cell ranked eligibility: candidates of the cell's source whose
	// axis score is > 0, sorted by (score desc, size tiebreak).
	ranked := make(map[cellKey][]recallCandidate, len(cells))
	for _, ck := range cells {
		var elig []recallCandidate
		for _, c := range cands {
			if c.source != ck.source {
				continue
			}
			if s, _ := c.axisScore(ck.axis); s > 0 {
				elig = append(elig, c)
			}
		}
		axis := ck.axis
		sort.Slice(elig, func(i, j int) bool {
			si, vi := elig[i].axisScore(axis)
			sj, vj := elig[j].axisScore(axis)
			if si != sj {
				return si > sj
			}
			switch {
			case vi && vj:
				return elig[i].size > elig[j].size // vector: larger first
			case !vi && !vj:
				return elig[i].size < elig[j].size // trigram: smaller first
			default:
				return elig[i].key < elig[j].key // mixed: stable
			}
		})
		ranked[ck] = elig
	}

	// primaryCell is a candidate's stronger axis within its source.
	primaryCell := func(c recallCandidate) (cellKey, bool) {
		m, _ := c.axisScore(axisMeaning)
		t, _ := c.axisScore(axisTags)
		if m <= 0 && t <= 0 {
			return cellKey{}, false
		}
		if m >= t {
			return cellKey{c.source, axisMeaning}, true
		}
		return cellKey{c.source, axisTags}, true
	}

	surfacedIDs := make(map[string]bool)
	fileInCell := make(map[cellKey]map[uint64]int)
	var out []surfacedChunk
	take := func(ck cellKey, c recallCandidate) {
		surfacedIDs[c.key] = true
		fc := fileInCell[ck]
		if fc == nil {
			fc = make(map[uint64]int)
			fileInCell[ck] = fc
		}
		fc[c.fileID]++
		out = append(out, surfacedChunk{cand: c, cell: cellLabel(ck.source, ck.axis)})
	}
	eligibleNow := func(ck cellKey, c recallCandidate) bool {
		return !surfacedIDs[c.key] && fileInCell[ck][c.fileID] < 2
	}

	// Primary pass: each cell takes up to perCell of its own primaries.
	for _, ck := range cells {
		n := 0
		for _, c := range ranked[ck] {
			if n >= perCell {
				break
			}
			if pc, ok := primaryCell(c); !ok || pc != ck {
				continue
			}
			if !eligibleNow(ck, c) {
				continue
			}
			take(ck, c)
			n++
		}
	}

	// Backfill pass: round-robin across the cells until the per-call
	// target (4×perCell) is met or no cell can contribute more.
	target := perCell * len(cells)
	for len(out) < target {
		added := 0
		for _, ck := range cells {
			if len(out) >= target {
				break
			}
			for _, c := range ranked[ck] {
				if !eligibleNow(ck, c) {
					continue
				}
				take(ck, c)
				added++
				break
			}
		}
		if added == 0 {
			break
		}
	}

	// Final flat order: overall score desc, tiebreak chunkID.
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].cand.overallScore(), out[j].cand.overallScore()
		if si != sj {
			return si > sj
		}
		return out[i].cand.key < out[j].cand.key
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

// inputQuery carries one recall input's query vector and trigram set,
// collected during the scoring passes for reuse by the chat funnel. R2910
type inputQuery struct {
	vec  []float32
	tris map[uint32]bool
}

// chatSubchunks re-chunks a chat-jsonl turn's indexed content into markdown
// sub-chunks in document order. The N-th element is the unit a path:range:N
// recall locator addresses; the funnel and `ark chunks PATH:RANGE:N` re-derive
// it identically because markdown chunking is deterministic. R2910
func chatSubchunks(content string) []microfts2.Chunk {
	var subs []microfts2.Chunk
	_ = microfts2.MarkdownChunker{}.Chunks("", []byte(content), func(c microfts2.Chunk) bool {
		subs = append(subs, c)
		return true
	})
	return subs
}

// ChatSubchunk returns the markdown sub-chunk of the chat turn at
// path:rangeLabel whose content contains the anchor snippet, re-derived from
// the indexed turn content. It is the resolve side of the recall
// path:range:"<snippet>" locator (R2914): the funnel emits the matched
// paragraph's first line as the anchor, and the same deterministic
// chatSubchunks here finds the sub-chunk carrying it. Dropping the snippet
// (fetching path:range via `ark chunks`) returns the whole turn instead — the
// zoom-out for fuller context. ok is false when the turn or anchor is absent.
// CRC: crc-DB.md | R2914
func (db *DB) ChatSubchunk(path, rangeLabel, anchor string) (string, bool) {
	if anchor == "" {
		return "", false
	}
	txt, ok := db.fts.NewChunkCache().ChunkText(path, rangeLabel)
	if !ok || len(txt) == 0 {
		return "", false
	}
	for _, sc := range chatSubchunks(string(txt)) {
		if strings.Contains(string(sc.Content), anchor) {
			return string(sc.Content), true
		}
	}
	return "", false
}

// firstLineSnippet is the first non-blank line of text, trimmed and capped —
// the string anchor in a chat sub-chunk's path:range:"<snippet>" locator. R2914
func firstLineSnippet(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			if r := []rune(s); len(r) > 60 {
				s = string(r[:60])
			}
			return s
		}
	}
	return ""
}

// chatFunnel implements the conversation sub-chunk funnel (R2910). It pools
// the markdown sub-chunks of every matched conversation turn, sorts the pool
// by trigram similarity to the inputs, embeds only the top-`gate` survivors
// (R2912, the cost bound), vector-checks each against the inputs, and returns
// sub-chunk candidates — meaning components only; the tag axis surfaces whole
// turns separately — located by path:range:"<snippet>". Trigram-only, no model.
// CRC: crc-Librarian.md | R2910, R2912
func (l *Librarian) chatFunnel(turns []recallCandidate, inputs []inputQuery, gate int, embedAvail bool) []recallCandidate {
	type sub struct {
		turn recallCandidate
		text string
		tri  float64
	}
	var pool []sub
	for _, t := range turns {
		content, err := substrateChunkText(l.db, t.chunkID)
		if err != nil || content == "" {
			continue
		}
		for _, sc := range chatSubchunks(content) {
			text := string(sc.Content)
			if text == "" {
				continue
			}
			var tri float64
			for _, iq := range inputs {
				if iq.tris == nil {
					continue
				}
				if s := trigramJaccardWithFloor(iq.tris, text); s > tri {
					tri = s
				}
			}
			if tri <= 0 {
				continue // trigram pre-filter: only lexical matches proceed to embed
			}
			pool = append(pool, sub{turn: t, text: text, tri: tri})
		}
	}
	if len(pool) == 0 {
		return nil
	}
	// Trigram-sort; the gate caps how many survivors get embedded (R2912).
	sort.Slice(pool, func(i, j int) bool { return pool[i].tri > pool[j].tri })
	if gate > 0 && len(pool) > gate {
		pool = pool[:gate]
	}

	// Embed the survivors once (off the model when available) and vector-check.
	var vecs [][]float32
	if embedAvail {
		texts := make([]string, len(pool))
		for i, s := range pool {
			texts[i] = s.text
		}
		vecs, _ = l.EmbedBatch(texts)
	}

	out := make([]recallCandidate, 0, len(pool))
	for i, s := range pool {
		var vec float64
		if i < len(vecs) {
			for _, iq := range inputs {
				if len(iq.vec) == len(vecs[i]) && len(iq.vec) > 0 {
					if c := normalizeCos(cosineSimilarity(vecs[i], iq.vec)); c > vec {
						vec = c
					}
				}
			}
		}
		rangeID := fmt.Sprintf("%s:%q", s.turn.rangeID, firstLineSnippet(s.text))
		out = append(out, recallCandidate{
			acc:     &chunkScoresAcc{vectorEC: vec, trigramEC: s.tri},
			key:     s.turn.path + ":" + rangeID,
			chunkID: s.turn.chunkID,
			path:    s.turn.path,
			rangeID: rangeID,
			fileID:  s.turn.fileID,
			source:  sourceConversation,
			size:    uint64(len(s.text)),
			content: s.text,
		})
	}
	return out
}

// scoredTag is one (tagname, similarity) entry. Used by both
// runDerivationPass (top-N candidate selection) and enrichProposedTags
// (proposal ordering).
type scoredTag struct {
	tag   string
	score float64
}

// chunkWork is one chunk's surviving derivation candidates, ordered
// by similarity desc.
type chunkWork struct {
	proposals []string           // tagnames to write (top-N, in order)
	scores    map[string]float64 // tagname → similarity for stencil
}

// runDerivationPass is the compute-for-display proposal pass (#36). For
// each chunk in the scored set it computes candidate tags via
// selectCandidates — cosine vs ED (+EV, R2911), top-N, the
// min-similarity floor, minus already-attached, ext-routed, and
// net-rejected tags — and returns them transiently for this recall call.
// It authors no @ext-candidate, writes no RC/RF, and runs no synchronous
// materialization: the calling agent is the sole author of durable
// candidates via `ark ext candidate` (R3081). RF freshness is retired
// with the durable RC cache, so the pass always computes for the chunks
// it is asked about (a repeat --propose recomputes rather than skipping).
// CRC: crc-Librarian.md | Seq: seq-derived-tags.md#1.6 | R3079, R3081, R2911
func (l *Librarian) runDerivationPass(scoresMap map[uint64]*chunkScoresAcc) (map[uint64]*chunkWork, error) {
	eds, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, err
	}
	// Part 5 (R2911): also score chunk-EC against tag-value (EV) embeddings,
	// so a chunk earns a tag for resembling an existing *value*, not only a
	// definition. Fold EV vectors into the candidate scan as additional
	// per-tag vectors (tag resolved via the TvidMap); selectCandidates'
	// per-tag max then spans definitions and values alike, under the same
	// min floor (R2911).
	if evs, eerr := l.db.store.ScanTagValueEmbeddings(); eerr == nil && len(evs) > 0 {
		tvids := l.db.store.TvidMap().Snapshot()
		for tvid, vec := range evs {
			if ta, ok := tvids[tvid]; ok {
				eds = append(eds, TagDefEmbedding{Tag: ta.Tag, Vec: vec})
			}
		}
	}
	if len(eds) == 0 {
		return nil, nil // nothing to derive against
	}

	const derivationK = 10
	minSim := l.db.Config().Recall.EffectiveMinProposeSimilarity()
	work := make(map[uint64]*chunkWork, len(scoresMap))

	// Compute-only: no RF freshness skip (RF retired), no durable write.
	// selectCandidates reads only the in-memory ExtMap and the passed
	// vectors, and ReadChunkEmbedding opens its own read txn, so no
	// wrapping View is needed. R3079
	for chunkID, acc := range scoresMap {
		chunkVec, err := l.db.store.ReadChunkEmbedding(chunkID)
		if err != nil || chunkVec == nil {
			continue // can't derive without an EC vector
		}
		work[chunkID] = l.selectCandidates(chunkID, chunkVec, eds, acc.alreadyOn, derivationK, minSim)
	}
	return work, nil
}

// selectCandidates computes per-tag max cosine vs the chunk vector,
// drops already-attached, rejected, and sub-threshold tags, and
// returns the top-k survivors as a chunkWork ready for the write
// phase. minSim is the chunk-EC ↔ tag-ED cosine floor (R2742). The
// net-rejected filter reads ExtMap.rejectByChunk (R3070), not an RJ
// key lookup.
// CRC: crc-Librarian.md | R2670, R2671, R2672, R2742, R3070
func (l *Librarian) selectCandidates(chunkID uint64, chunkVec []float32, eds []TagDefEmbedding, alreadyOn map[string]bool, k int, minSim float64) *chunkWork {
	// Per-tag max similarity across all ED records for that tag.
	perTag := make(map[string]float64)
	for _, ed := range eds {
		if len(ed.Vec) != len(chunkVec) {
			continue
		}
		score := cosineSimilarity(chunkVec, ed.Vec)
		if cur, ok := perTag[ed.Tag]; !ok || score > cur {
			perTag[ed.Tag] = score
		}
	}
	candidates := make([]scoredTag, 0, len(perTag))
	for tag, score := range perTag {
		if score < minSim {
			continue // R2742 — sub-threshold floor
		}
		if alreadyOn[tag] {
			continue // R2671 + R2672 (union via AllTagsForChunk)
		}
		if score := l.db.extmap.RejectScore(chunkID, tag); score < 0 {
			magnitude := uint64(-score)
			ceiling := l.db.Config().Recall.EffectiveRejectProposeCeiling()
			if ceiling == 0 || magnitude >= uint64(ceiling) {
				continue // R3070, R2765, R2878 — net-rejected suppresses when ceiling=0; magnitude gates when ceiling>0
			}
			// magnitude < ceiling: re-propose despite previous rejection
		}
		candidates = append(candidates, scoredTag{tag, score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > k {
		candidates = candidates[:k]
	}
	cw := &chunkWork{
		proposals: make([]string, 0, len(candidates)),
		scores:    make(map[string]float64, len(candidates)),
	}
	for _, c := range candidates {
		cw.proposals = append(cw.proposals, c.tag)
		cw.scores[c.tag] = c.score
	}
	return cw
}

// enrichProposedTags populates each surfaced chunk's ProposedTags /
// ProposedTagScores from this call's transient computed proposals
// (work) — already ordered by chunk-EC ↔ tag similarity descending by
// selectCandidates. The durable RC pool (Store.DerivedProposals) is no
// longer read on the recall path; it is retained as the forge-facing
// reader (#37).
// CRC: crc-Librarian.md | Seq: seq-derived-tags.md#1.8 | R3080, R2684, R2685, R2686
func (l *Librarian) enrichProposedTags(chunks []RecalledChunk, work map[uint64]*chunkWork) {
	for i := range chunks {
		cw := work[chunks[i].ChunkID]
		if cw == nil || len(cw.proposals) == 0 {
			continue
		}
		names := make([]string, len(cw.proposals))
		copy(names, cw.proposals)
		scoresOut := make([]float64, len(cw.proposals))
		for j, tag := range cw.proposals {
			scoresOut[j] = cw.scores[tag]
		}
		chunks[i].ProposedTags = names
		chunks[i].ProposedTagScores = scoresOut
	}
}

// HandleRecall serves HTTP POST /recall requests.
// CRC: crc-Librarian.md | Seq: seq-recall.md#1.3 | R2629
func (l *Librarian) HandleRecall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Inputs []ConnectionsInput `json:"inputs"`
		Opts   RecallOpts         `json:"opts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := l.Recall(body.Inputs, body.Opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, res)
}
