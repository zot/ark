package ark

// CRC: crc-Librarian.md | Seq: seq-recall.md#1.4 | R2617, R2618, R2620, R2622, R2623, R2624, R2625, R2626, R2629, R2634, R2639, R2640, R2641, R2643, R2644

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// RecallOpts configures top-K retrieval, content loading, and the
// tagless-chunk filter.
// CRC: crc-Librarian.md | R2627, R2647, R2655, R2660
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
// CRC: crc-Librarian.md | R2624
type RecalledChunk struct {
	ChunkID      uint64         `json:"chunkID"`
	Path         string         `json:"path"`
	Range        string         `json:"range"`
	Score        float64        `json:"score"`
	PerSubstrate ChunkSubstrate `json:"perSubstrate"`
	Tags         []RecallTag    `json:"tags"`
	Content      string         `json:"content,omitempty"`
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

	type chunkScoresAcc struct {
		vectorEC  float64
		trigramEC float64
		// tags is the post-filter tag list (discussed entries stripped).
		// Populated lazily on first encounter so the result-build phase
		// doesn't re-query AllTagsForChunk. Nil means "not yet looked
		// up" — only possible when the exclusion set is inactive AND
		// KeepTagless=true (admit short-circuited the lookup).
		tags []TagValue
	}
	scoresMap := make(map[uint64]*chunkScoresAcc)

	// admit returns the accumulator for chunkID, creating it on first
	// encounter. The tag fetch + discussed-filter decision is:
	//
	//   - exclusion inactive AND KeepTagless: skip tag fetch entirely
	//     (existing fast path; result-build phase fills in tags lazily).
	//   - otherwise: fetch tags, apply the discussed filter, drop the
	//     chunk if either (a) it was originally tagless and KeepTagless
	//     is false, or (b) it had tags but the exclusion stripped them
	//     all (R2656). The discussed filter runs before the
	//     KeepTagless decision, and -all does not override it (R2658).
	//
	// R2647, R2656, R2657, R2658
	admit := func(chunkID uint64) *chunkScoresAcc {
		if acc, ok := scoresMap[chunkID]; ok {
			return acc
		}
		acc := &chunkScoresAcc{}
		if exclusion.active || !opts.KeepTagless {
			tags, err := l.db.store.AllTagsForChunk(chunkID)
			if err != nil {
				return nil
			}
			hadTags := len(tags) > 0
			tags = exclusion.filter(tags)
			switch {
			case !hadTags && !opts.KeepTagless:
				return nil // originally tagless, KeepTagless=false
			case hadTags && len(tags) == 0:
				return nil // emptied by exclusion — drops even with KeepTagless (R2658)
			}
			acc.tags = tags
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

	// 5. Build results, resolve metadata, tags, and content. R2624, R2625
	var recalled []RecalledChunk
	cache := l.db.fts.NewChunkCache()

	for cid, acc := range scoresMap {
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

	return &RecallResult{
		Chunks:  recalled,
		Warning: warning,
	}, nil
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
