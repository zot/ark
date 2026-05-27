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

	"github.com/bmatsuo/lmdb-go/lmdb"
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
	// Propose runs the statistical derivation pass on the substrate's
	// full scored chunk set as a side effect. Surviving candidates
	// land as RC records; each processed chunk's RF stamp advances to
	// the current max ED serial. The caller's surfaced result is not
	// changed except by the ProposedTags enrichment on RecalledChunk.
	// R2667, R2668
	Propose bool `json:"propose,omitempty"`
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
// CRC: crc-Librarian.md | R2624, R2686
type RecalledChunk struct {
	ChunkID      uint64         `json:"chunkID"`
	Path         string         `json:"path"`
	Range        string         `json:"range"`
	Score        float64        `json:"score"`
	PerSubstrate ChunkSubstrate `json:"perSubstrate"`
	Tags         []RecallTag    `json:"tags"`
	Content      string         `json:"content,omitempty"`
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

// ChunkSubstrate carries similarity scores per substrate.
// CRC: crc-Librarian.md | R2620
type ChunkSubstrate struct {
	VectorEC  float64 `json:"vectorEc"`
	TrigramEC float64 `json:"trigramEc"`
}

// RenderRecallChunks writes the per-chunk markdown block for a recall
// result, matching the stencil documented in specs/recall.md. The
// caller writes the surrounding header (`## Chunks`, `## Recalled
// chunks`, etc.). One blank line precedes each chunk.
//
// Shared between `ark connections recall` (via cmd/ark/main.go's
// printRecallResult) and the simple-recall watcher (recall_watcher.go).
// CRC: crc-Librarian.md | R2645, R2684, R2685, R2704
func RenderRecallChunks(out io.Writer, chunks []RecalledChunk) {
	for _, chunk := range chunks {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "- @chunk-id: %d\n", chunk.ChunkID)
		fmt.Fprintf(out, "  @chunk-path: %s\n", chunk.Path)
		fmt.Fprintf(out, "  @chunk-range: %s\n", chunk.Range)
		fmt.Fprintf(out, "  @chunk-score: %.2f\n", chunk.Score)
		fmt.Fprintf(out, "  @chunk-evidence-vector-ec: %.2f\n", chunk.PerSubstrate.VectorEC)
		fmt.Fprintf(out, "  @chunk-evidence-trigram-ec: %.2f\n", chunk.PerSubstrate.TrigramEC)

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
// CRC: crc-Librarian.md | Seq: seq-recall.md#1.3 | R2617, R2618, R2620, R2622, R2623, R2624, R2625, R2626, R2634, R2639, R2640, R2641, R2643, R2644, R2655, R2656, R2657, R2658
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

	// 4. Substrate passes. R2620, R2622, R2643, R2644
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
					cid, ok := l.resolveSearchEntryChunkID(h)
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
	}

	// 4b. Derivation pass (when --propose is set and embeddings are
	// available). Runs on the full scored set (tagless chunks included
	// via admitTagless). Writes RC + RF records as a side effect; the
	// derived similarity scores are returned for stencil ordering.
	// R2667, R2669, R2670, R2671, R2672, R2673, R2674, R2675, R2676
	var derivedScores map[uint64]map[string]float64
	if opts.Propose && embedAvail {
		ds, dErr := l.runDerivationPass(scoresMap)
		if dErr != nil {
			log.Printf("recall: derivation pass: %v", dErr)
		}
		derivedScores = ds
	}

	// 5. Build results, resolve metadata, tags, and content. R2624, R2625
	var recalled []RecalledChunk
	cache := l.db.fts.NewChunkCache()

	for cid, acc := range scoresMap {
		// Caller-surfacing filter: when Propose forced admitTagless,
		// drop tagless chunks from the surfaced result if the caller's
		// KeepTagless is false. The derivation pass already processed
		// them (their RC/RF records live in LMDB). R2668
		if !opts.KeepTagless && opts.Propose && needFetch && !acc.hadTags {
			continue
		}

		overallScore := acc.vectorEC
		if acc.trigramEC > overallScore {
			overallScore = acc.trigramEC
		}

		info, err := l.db.ChunkInfo(cid)
		if err != nil {
			continue
		}

		// Reuse the cached tag lookup from admit when present
		// (KeepTagless=false path); otherwise query now. R2647
		allTags := acc.tags
		if allTags == nil {
			allTags, _ = l.db.store.AllTagsForChunk(cid)
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
			if txt, ok := cache.ChunkText(info.Path, info.Range); ok {
				content = string(txt)
			}
		}

		recalled = append(recalled, RecalledChunk{
			ChunkID: cid,
			Path:    info.Path,
			Range:   info.Range,
			Score:   overallScore,
			PerSubstrate: ChunkSubstrate{
				VectorEC:  acc.vectorEC,
				TrigramEC: acc.trigramEC,
			},
			Tags:    tags,
			Content: content,
		})
	}

	// Sort descending by score. R2626
	sort.Slice(recalled, func(i, j int) bool {
		if recalled[i].Score == recalled[j].Score {
			return recalled[i].ChunkID < recalled[j].ChunkID
		}
		return recalled[i].Score > recalled[j].Score
	})

	if len(recalled) > k {
		recalled = recalled[:k]
	}

	// 6. Enrich each surfaced chunk with accumulated derived-tag
	// candidates ordered by similarity desc. R2684, R2685, R2686
	if opts.Propose && embedAvail {
		l.enrichProposedTags(recalled, derivedScores)
	}

	return &RecallResult{
		Chunks:  recalled,
		Warning: warning,
	}, nil
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

// runDerivationPass is the read+write side effect of --propose. For
// each chunk in the scored set: skip via RF freshness, else compute
// cosine vs ED, top-N, filter, write RC + RF in one batched txn.
// Returns the per-chunk per-tag similarity map for stencil ordering.
// CRC: crc-Librarian.md | Seq: seq-derived-tags.md#1.6 | R2669, R2670, R2671, R2672, R2673, R2674, R2675
func (l *Librarian) runDerivationPass(scoresMap map[uint64]*chunkScoresAcc) (map[uint64]map[string]float64, error) {
	maxED, err := l.db.store.MaxEDSerial()
	if err != nil {
		return nil, err
	}
	eds, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, err
	}
	if len(eds) == 0 {
		// Nothing to derive against — leave RC/RF alone, return empty.
		return nil, nil
	}

	const derivationK = 10
	minSim := l.db.Config().Recall.EffectiveMinProposeSimilarity()
	work := make(map[uint64]*chunkWork, len(scoresMap))

	// Read phase: freshness check, candidate generation, filtering.
	if err := l.db.store.env.View(func(txn *lmdb.Txn) error {
		for chunkID, acc := range scoresMap {
			rf, _, _ := l.db.store.ReadDerivedFreshness(txn, chunkID)
			if rf >= maxED {
				continue // fresh — no write
			}
			chunkVec, err := l.db.store.ReadChunkEmbedding(chunkID)
			if err != nil || chunkVec == nil {
				continue // can't derive without an EC vector
			}
			cw := l.selectCandidates(txn, chunkID, chunkVec, eds, acc.alreadyOn, derivationK, minSim)
			work[chunkID] = cw
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Write phase: batched RC + RF writes. Routes through the actor
	// per the all-mutation-through-write-actor rule.
	if err := SyncVoid(l.db, func(_ *DB) error {
		return l.db.store.env.Update(func(txn *lmdb.Txn) error {
			for chunkID, cw := range work {
				for _, tag := range cw.proposals {
					if err := l.db.store.WriteDerivedProposal(txn, chunkID, tag); err != nil {
						return err
					}
				}
				if err := l.db.store.WriteDerivedFreshness(txn, chunkID, maxED); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	// Surface this-call scores for stencil ordering. Empty maps are
	// omitted — enrichProposedTags treats missing entries as "no
	// fresh score, compute on demand."
	out := make(map[uint64]map[string]float64, len(work))
	for chunkID, cw := range work {
		if len(cw.scores) > 0 {
			out[chunkID] = cw.scores
		}
	}
	return out, nil
}

// selectCandidates computes per-tag max cosine vs the chunk vector,
// drops already-attached, rejected, and sub-threshold tags, and
// returns the top-k survivors as a chunkWork ready for the write
// phase. minSim is the chunk-EC ↔ tag-ED cosine floor (R2742).
// CRC: crc-Librarian.md | R2670, R2671, R2672, R2673, R2674, R2742
func (l *Librarian) selectCandidates(txn *lmdb.Txn, chunkID uint64, chunkVec []float32, eds []TagDefEmbedding, alreadyOn map[string]bool, k int, minSim float64) *chunkWork {
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
		rejected, counter, _ := l.db.store.HasDerivedRejection(txn, chunkID, tag)
		if rejected {
			ceiling := l.db.Config().Recall.EffectiveRejectProposeCeiling()
			if ceiling == 0 || counter >= uint64(ceiling) {
				continue // R2673, R2765 — existence suppresses when ceiling=0; counter gates when ceiling>0
			}
			// counter < ceiling: re-propose despite previous rejection
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

// enrichProposedTags populates RecalledChunk.ProposedTags with the
// accumulated RC entries for each surfaced chunk, ordered by chunk-EC
// ↔ tag-ED cosine similarity descending. Reuses this-call scores
// from derivedScores for chunks the pass derived; computes on-demand
// for fresh-skip chunks. When neither path yields a score (no EC
// vector, no ED records), proposals fall back to DerivedProposals'
// tally-desc order, which sort.SliceStable preserves.
// CRC: crc-Librarian.md | Seq: seq-derived-tags.md#1.8 | R2684, R2685, R2686
func (l *Librarian) enrichProposedTags(chunks []RecalledChunk, derivedScores map[uint64]map[string]float64) {
	if len(chunks) == 0 {
		return
	}
	var edCache []TagDefEmbedding
	edCacheLoaded := false
	for i := range chunks {
		props, err := l.db.store.DerivedProposals(chunks[i].ChunkID)
		if err != nil || len(props) == 0 {
			continue
		}
		thisCall := derivedScores[chunks[i].ChunkID]
		scored := make([]scoredTag, 0, len(props))
		var chunkVec []float32 // loaded lazily on first on-demand miss
		for _, p := range props {
			if s, ok := thisCall[p.Tagname]; ok {
				scored = append(scored, scoredTag{p.Tagname, s})
				continue
			}
			if chunkVec == nil {
				v, vErr := l.db.store.ReadChunkEmbedding(chunks[i].ChunkID)
				if vErr == nil && v != nil {
					chunkVec = v
				}
			}
			if !edCacheLoaded {
				if eds, eErr := l.db.store.ScanTagDefEmbeddings(); eErr == nil {
					edCache = eds
				}
				edCacheLoaded = true
			}
			scored = append(scored, scoredTag{p.Tagname, bestEDSim(chunkVec, edCache, p.Tagname)})
		}
		sort.SliceStable(scored, func(a, b int) bool {
			return scored[a].score > scored[b].score
		})
		names := make([]string, 0, len(scored))
		scoresOut := make([]float64, 0, len(scored))
		for _, sp := range scored {
			names = append(names, sp.tag)
			scoresOut = append(scoresOut, sp.score)
		}
		chunks[i].ProposedTags = names
		chunks[i].ProposedTagScores = scoresOut
	}
}

// bestEDSim returns max cosine similarity between chunkVec and any
// ED record for tag. Returns 0 if either input is empty or no ED
// record matches — callers treat 0 as "no score" and rely on the
// stable sort to preserve tally-desc order. R2685
func bestEDSim(chunkVec []float32, eds []TagDefEmbedding, tag string) float64 {
	if len(chunkVec) == 0 {
		return 0
	}
	var best float64
	for _, ed := range eds {
		if ed.Tag != tag || len(ed.Vec) != len(chunkVec) {
			continue
		}
		if s := cosineSimilarity(chunkVec, ed.Vec); s > best {
			best = s
		}
	}
	return best
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
