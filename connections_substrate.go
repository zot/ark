package ark

// CRC: crc-Librarian.md | Seq: seq-find-connections-substrate.md | R2567, R2568, R2569, R2570, R2571, R2572, R2573, R2574, R2575, R2576, R2577, R2578, R2579, R2580, R2581, R2582, R2583, R2584, R2585, R2586, R2587, R2588, R2589

import (
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatsuo/lmdb-go/lmdb"
)

// ConnectionsInput is one normalized input to the substrate pipeline.
// Exactly one of ChunkID / (Path, Range) / Text is populated in the
// raw form; normalizeInputs collapses Path+Range into ChunkID entries
// (one per overlapping chunk) so the substrate worker sees a flat list
// of ChunkID-or-Text entries. R2568, R2569, R2570, R2572
type ConnectionsInput struct {
	ChunkID uint64 `json:"chunkID,omitempty"`
	Path    string `json:"path,omitempty"`
	Range   string `json:"range,omitempty"`
	Text    string `json:"text,omitempty"`
}

// substrateInput is the post-normalization shape: a chunkID-or-text
// pair the per-input pipeline operates on. Carries the resolved path
// for evidence reporting on text inputs (where Path == "<query>") and
// chunkID inputs (where Path is the file's canonical path).
type substrateInput struct {
	chunkID uint64
	path    string
	text    string // non-empty only for bare-text inputs
}

// SubstrateScores collects per-substrate similarity scores for one
// (tag, input) pair. All values are normalized to [0, 1]. Zero means
// "this substrate did not contribute" (either skipped or no signal).
// R2582, R2586, R2587
type SubstrateScores struct {
	VectorED  float64 `json:"vectorEd"`
	TrigramED float64 `json:"trigramEd"`
	VectorEC  float64 `json:"vectorEc"`
	TrigramEC float64 `json:"trigramEc"`
}

// max returns the maximum of the four substrate scores for this entry.
// R2580
func (s SubstrateScores) max() float64 {
	m := s.VectorED
	if s.TrigramED > m {
		m = s.TrigramED
	}
	if s.VectorEC > m {
		m = s.VectorEC
	}
	if s.TrigramEC > m {
		m = s.TrigramEC
	}
	return m
}

// TagNameCandidate is one proposal in the substrate pipeline's output.
// Score is the cross-substrate, cross-input aggregate (max). PerSubstrate
// retains the four substrate scores after cross-input aggregation
// (max across inputs per substrate). R2582, R2583, R2584
type TagNameCandidate struct {
	Tag              string             `json:"tag"`
	Score            float64            `json:"score"`
	PerSubstrate     SubstrateScores    `json:"perSubstrate"`
	SupportingChunks []uint64           `json:"supportingChunks"`
	MotivatingFiles  []TagSuggestionRef `json:"motivatingFiles"`
}

// SubstrateResult is the full output of a single normal-mode pipeline
// run. The doc renderer turns this into the ## Proposals body.
// R2585
type SubstrateResult struct {
	Candidates []TagNameCandidate `json:"candidates"`
	Warning    string             `json:"warning,omitempty"` // R2588
}

const (
	// substrateInternalK is the per-substrate top-N retained before
	// the final cross-substrate merge clips to the user-facing k.
	// Larger than the default k so a tag that's not in any one
	// substrate's top-N but appears across multiple substrates can
	// still surface.
	substrateInternalK = 50

	// supportingChunkCap is the maximum number of supporting chunks
	// stored per candidate. R2583
	supportingChunkCap = 10
)

// normalizeInputs expands each raw ConnectionsInput into one or more
// substrateInputs. ChunkID entries pass through; existence-check is
// gated on strict (true for normal mode — the substrate needs each
// chunk's EC vector immediately; false for turbo, preserving R2324's
// deferred "surface at --fetch" semantics). PathRange entries resolve
// the path, intersect chunks with the line range, and expand into
// one substrateInput per overlapping chunk. Text entries pass through.
// R2568, R2569, R2570, R2571, R2572, R2573, R2574
func (l *Librarian) normalizeInputs(raw []ConnectionsInput, strict bool) ([]substrateInput, []uint64, error) {
	if len(raw) == 0 {
		return nil, nil, errors.New("chunkIDs/text/range empty")
	}
	// Read FileIDPaths once for chunkID-side path resolution. The
	// path:range branch uses PathFileID + FileInfoByID directly.
	paths, err := l.db.fts.FileIDPaths()
	if err != nil {
		return nil, nil, fmt.Errorf("file id paths: %w", err)
	}
	out := make([]substrateInput, 0, len(raw))
	originalChunkIDs := make([]uint64, 0, len(raw))
	err = l.db.fts.Env().View(func(txn *lmdb.Txn) error {
		for _, in := range raw {
			switch {
			case in.ChunkID != 0:
				var path string
				if strict {
					crec, rerr := l.db.fts.ReadCRecord(txn, in.ChunkID)
					if rerr != nil || len(crec.FileIDs) == 0 {
						return fmt.Errorf("unknown chunk %d", in.ChunkID)
					}
					path = paths[crec.FileIDs[0].FileID]
				}
				out = append(out, substrateInput{chunkID: in.ChunkID, path: path})
				originalChunkIDs = append(originalChunkIDs, in.ChunkID)
			case in.Path != "":
				if in.Range == "" {
					return fmt.Errorf(`path %q requires a range; use ":1-" for the whole file`, in.Path)
				}
				start, end, perr := parseLineRange(in.Range)
				if perr != nil {
					return fmt.Errorf("path:range parse error: %w", perr)
				}
				fileID, ok := l.db.PathFileID(in.Path)
				if !ok {
					return fmt.Errorf("path %q not found", in.Path)
				}
				info, ierr := l.db.fts.FileInfoByID(fileID)
				if ierr != nil {
					return fmt.Errorf("path %q: %w", in.Path, ierr)
				}
				for _, c := range info.Chunks {
					cs, ce, hasRange := parseChunkLineRange(c.Location)
					if !hasRange {
						continue
					}
					if rangesOverlap(cs, ce, start, end) {
						out = append(out, substrateInput{chunkID: c.ChunkID, path: in.Path})
						originalChunkIDs = append(originalChunkIDs, c.ChunkID)
					}
				}
			case in.Text != "":
				out = append(out, substrateInput{path: "<query>", text: in.Text})
			default:
				return errors.New("input must specify chunkID, path+range, or text")
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(out) == 0 {
		return nil, nil, errors.New("chunkIDs/text/range empty")
	}
	return out, originalChunkIDs, nil
}

// parseLineRange parses "N-M", "N-", or "N" into [start, end] (1-based
// inclusive). An open-ended "N-" returns end=math.MaxInt32 so any
// chunk with start >= N qualifies. R2570
func parseLineRange(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, errors.New("empty range")
	}
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return 0, 0, fmt.Errorf("invalid line number %q", s)
		}
		return n, n, nil
	}
	startS := strings.TrimSpace(s[:dash])
	endS := strings.TrimSpace(s[dash+1:])
	start, err := strconv.Atoi(startS)
	if err != nil || start <= 0 {
		return 0, 0, fmt.Errorf("invalid range start %q", startS)
	}
	end := math.MaxInt32
	if endS != "" {
		end, err = strconv.Atoi(endS)
		if err != nil || end < start {
			return 0, 0, fmt.Errorf("invalid range end %q", endS)
		}
	}
	return start, end, nil
}

// parseChunkLineRange parses a chunker's Location string of the form
// "N-M" into (start, end). Chunkers that don't encode lines (PDF,
// custom) return ok=false. R2570
func parseChunkLineRange(loc string) (int, int, bool) {
	dash := strings.IndexByte(loc, '-')
	if dash < 0 {
		n, err := strconv.Atoi(strings.TrimSpace(loc))
		if err != nil {
			return 0, 0, false
		}
		return n, n, true
	}
	start, err := strconv.Atoi(strings.TrimSpace(loc[:dash]))
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.Atoi(strings.TrimSpace(loc[dash+1:]))
	if err != nil {
		return 0, 0, false
	}
	return start, end, true
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

// runSubstrate is the in-process worker for normal mode. Opens one
// LMDB View txn, runs per-input substrate passes, merges, renders the
// proposal body, and flips the tmp:// doc to completed via the
// existing write actor. R2579, R2580, R2581, R2585, R2598, R2599
func (l *Librarian) runSubstrate(rec *ConnectionsRecord, inputs []substrateInput, k int) {
	result, err := l.computeSubstrate(inputs, k)
	if err != nil {
		if serr := l.SetConnectionsError(rec.ID, err.Error()); serr != nil {
			log.Printf("connections: runSubstrate error for %s: %v", rec.ID, serr)
		}
		return
	}
	if serr := l.SetSubstrateResult(rec.ID, result); serr != nil {
		log.Printf("connections: SetSubstrateResult for %s: %v", rec.ID, serr)
	}
}

// computeSubstrate executes the pipeline. Pure function over LMDB;
// no doc writes. R2579–R2585
func (l *Librarian) computeSubstrate(inputs []substrateInput, k int) (*SubstrateResult, error) {
	embedAvail := l.EmbeddingAvailable()
	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("connections: substrate path resolution: %v", perr)
	}

	// Note: spec R2579 calls for a single shared View txn across all
	// four substrate passes; the current helpers (EmbedQuery,
	// ReadChunkEmbedding, ScanTagDefEmbeddings, ListTagDefs,
	// SearchChunks, SearchFuzzy) each open their own txn. The shared-
	// txn optimization is a future perf pass once profiling justifies
	// it — tracked as a gap in design.md.
	perInput := make([]map[string]*candidateAcc, 0, len(inputs))
	for _, in := range inputs {
		acc, ierr := l.substrateForInput(in, embedAvail)
		if ierr != nil {
			return nil, ierr
		}
		perInput = append(perInput, acc)
	}

	candidates := mergeAcrossInputs(perInput, k, paths)
	out := &SubstrateResult{Candidates: candidates}
	if !embedAvail {
		out.Warning = "embedding unavailable"
	}
	return out, nil
}

// candidateAcc accumulates per-substrate scores and supporting evidence
// for one tag inside one input's substrate run.
type candidateAcc struct {
	scores           SubstrateScores
	supportingChunks map[uint64]struct{}
	motivatingFiles  map[uint64]float64 // fileID → best ED score
}

func newCandidateAcc() *candidateAcc {
	return &candidateAcc{
		supportingChunks: make(map[uint64]struct{}),
		motivatingFiles:  make(map[uint64]float64),
	}
}

// substrateForInput runs the four substrate passes for a single
// normalized input. R2575–R2578
func (l *Librarian) substrateForInput(in substrateInput, embedAvail bool) (map[string]*candidateAcc, error) {
	out := make(map[string]*candidateAcc)

	// Resolve the query vector and the query text used for trigram passes.
	var queryVec []float32
	var queryText string
	if in.text != "" {
		queryText = in.text
		if embedAvail {
			v, err := l.EmbedQuery(in.text)
			if err == nil {
				queryVec = v
			}
		}
	} else if in.chunkID != 0 {
		if embedAvail {
			v, err := l.db.store.ReadChunkEmbedding(in.chunkID)
			if err == nil {
				queryVec = v
			}
		}
		// Pull chunk text for trigram-side use.
		txt, terr := substrateChunkText(l.db, in.chunkID)
		if terr == nil {
			queryText = txt
		}
	}

	// Substrate 1: vector(input, ED). R2575
	if len(queryVec) > 0 {
		eds, err := l.db.store.ScanTagDefEmbeddings()
		if err == nil {
			for _, ed := range eds {
				if len(ed.Vec) != len(queryVec) {
					continue
				}
				score := normalizeCos(cosineSimilarity(queryVec, ed.Vec))
				acc := out[ed.Tag]
				if acc == nil {
					acc = newCandidateAcc()
					out[ed.Tag] = acc
				}
				if score > acc.scores.VectorED {
					acc.scores.VectorED = score
				}
				if score > acc.motivatingFiles[ed.FileID] {
					acc.motivatingFiles[ed.FileID] = score
				}
			}
		}
	}

	// Substrate 2: trigram(input, ED). Brute-force overlap. R2576
	if queryText != "" {
		queryTri := trigrams(strings.ToLower(queryText))
		if len(queryTri) > 0 {
			scanTagDefs(l.db.store, func(tag, defText string, fileID uint64) {
				score := trigramOverlap(queryTri, trigrams(strings.ToLower(defText)))
				if score == 0 {
					return
				}
				acc := out[tag]
				if acc == nil {
					acc = newCandidateAcc()
					out[tag] = acc
				}
				if score > acc.scores.TrigramED {
					acc.scores.TrigramED = score
				}
				if score > acc.motivatingFiles[fileID] {
					acc.motivatingFiles[fileID] = score
				}
			})
		}
	}

	// Substrate 3: vector(input, EC). R2577
	if len(queryVec) > 0 {
		scores, err := l.SearchChunks(queryVec, substrateInternalK)
		if err == nil {
			for _, cs := range scores {
				normalized := normalizeCos(cs.Score)
				addVotesFromChunk(l.db, cs.ChunkID, normalized, out, in.chunkID, func(acc *candidateAcc, n float64) {
					if n > acc.scores.VectorEC {
						acc.scores.VectorEC = n
					}
				})
			}
		}
	}

	// Substrate 4: trigram(input, EC). R2578
	// Skipped when the Searcher isn't wired (test setups without full DB).
	if queryText != "" && l.db.search != nil {
		hits, err := l.db.SearchFuzzy(queryText, SearchOpts{K: substrateInternalK})
		if err == nil {
			maxScore := 0.0
			for _, h := range hits {
				if h.Score > maxScore {
					maxScore = h.Score
				}
			}
			for _, h := range hits {
				cid, ok := l.resolveSearchEntryChunkID(h)
				if !ok {
					continue
				}
				normalized := 0.0
				if maxScore > 0 {
					normalized = h.Score / maxScore
				}
				addVotesFromChunk(l.db, cid, normalized, out, in.chunkID, func(acc *candidateAcc, n float64) {
					if n > acc.scores.TrigramEC {
						acc.scores.TrigramEC = n
					}
				})
			}
		}
	}

	return out, nil
}

// addVotesFromChunk reads V records for chunkID and votes for each
// tag with `score`. The selector closure routes the score onto the
// correct substrate field on the accumulator. Self-votes (the input's
// own chunkID, when applicable) are skipped to avoid trivial signal.
func addVotesFromChunk(db *DB, chunkID uint64, score float64, out map[string]*candidateAcc, selfID uint64, set func(*candidateAcc, float64)) {
	if chunkID == selfID || score <= 0 {
		return
	}
	pairs, err := db.store.AllTagsForChunk(chunkID)
	if err != nil || len(pairs) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(pairs))
	for _, tv := range pairs {
		if _, dup := seen[tv.Tag]; dup {
			continue
		}
		seen[tv.Tag] = struct{}{}
		acc := out[tv.Tag]
		if acc == nil {
			acc = newCandidateAcc()
			out[tv.Tag] = acc
		}
		set(acc, score)
		if len(acc.supportingChunks) < supportingChunkCap*4 {
			acc.supportingChunks[chunkID] = struct{}{}
		}
	}
}

// scanTagDefs iterates D records, invoking yield with (tag, defText,
// fileID) for each. Fetched via Store.ListTagDefs which manages its
// own View txn — separate from the substrate's outer txn, fine here
// since D records are stable across the substrate run. (Scan is
// small: < ~1k records.)
func scanTagDefs(store *Store, yield func(tag, defText string, fileID uint64)) {
	defs, err := store.ListTagDefs(nil)
	if err != nil {
		log.Printf("connections: scan tag defs: %v", err)
		return
	}
	for _, d := range defs {
		yield(d.Tag, d.Description, d.FileID)
	}
}

// resolveSearchEntryChunkID maps a SearchResultEntry's (FileID,
// ChunkNum) to the global chunkID via FileInfoByID. FileInfoByID is
// cached, so repeated lookups across the same file are cheap.
func (l *Librarian) resolveSearchEntryChunkID(h SearchResultEntry) (uint64, bool) {
	info, err := l.db.fts.FileInfoByID(h.FileID)
	if err != nil {
		return 0, false
	}
	if h.ChunkNum >= uint64(len(info.Chunks)) {
		return 0, false
	}
	return info.Chunks[h.ChunkNum].ChunkID, true
}

// normalizeCos maps a cosine similarity in [-1, 1] to [0, 1]. R2586
func normalizeCos(cos float64) float64 {
	v := (cos + 1) / 2
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return v
}

// substrateChunkText reads a chunk's content via the chunk cache.
// Returns empty string on any failure (substrate degrades gracefully).
func substrateChunkText(db *DB, chunkID uint64) (string, error) {
	info, err := db.ChunkInfo(chunkID)
	if err != nil {
		return "", err
	}
	cache := db.fts.NewChunkCache()
	content, ok := cache.ChunkText(info.Path, info.Range)
	if !ok {
		return "", errors.New("chunk content unavailable")
	}
	return string(content), nil
}

// mergeAcrossInputs implements the cross-input merge: per-substrate
// score is max across inputs; aggregate is max across substrates.
// Supporting chunks union with cap; motivating files retained with
// max-score per (tag, fileID). R2581–R2585
func mergeAcrossInputs(perInput []map[string]*candidateAcc, k int, paths map[uint64]string) []TagNameCandidate {
	type merged struct {
		scores           SubstrateScores
		supportingChunks map[uint64]struct{}
		motivatingFiles  map[uint64]float64
	}
	all := make(map[string]*merged)
	for _, perTag := range perInput {
		for tag, acc := range perTag {
			m := all[tag]
			if m == nil {
				m = &merged{
					supportingChunks: make(map[uint64]struct{}),
					motivatingFiles:  make(map[uint64]float64),
				}
				all[tag] = m
			}
			if acc.scores.VectorED > m.scores.VectorED {
				m.scores.VectorED = acc.scores.VectorED
			}
			if acc.scores.TrigramED > m.scores.TrigramED {
				m.scores.TrigramED = acc.scores.TrigramED
			}
			if acc.scores.VectorEC > m.scores.VectorEC {
				m.scores.VectorEC = acc.scores.VectorEC
			}
			if acc.scores.TrigramEC > m.scores.TrigramEC {
				m.scores.TrigramEC = acc.scores.TrigramEC
			}
			for c := range acc.supportingChunks {
				m.supportingChunks[c] = struct{}{}
			}
			for f, sc := range acc.motivatingFiles {
				if sc > m.motivatingFiles[f] {
					m.motivatingFiles[f] = sc
				}
			}
		}
	}
	cands := make([]TagNameCandidate, 0, len(all))
	for tag, m := range all {
		aggregate := m.scores.max()
		if aggregate == 0 {
			continue
		}
		chunks := make([]uint64, 0, len(m.supportingChunks))
		for c := range m.supportingChunks {
			chunks = append(chunks, c)
		}
		slices.Sort(chunks)
		if len(chunks) > supportingChunkCap {
			chunks = chunks[:supportingChunkCap]
		}
		files := make([]TagSuggestionRef, 0, len(m.motivatingFiles))
		for f, sc := range m.motivatingFiles {
			files = append(files, TagSuggestionRef{FileID: f, Score: sc, Path: paths[f]})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Score > files[j].Score })
		cands = append(cands, TagNameCandidate{
			Tag:              tag,
			Score:            aggregate,
			PerSubstrate:     m.scores,
			SupportingChunks: chunks,
			MotivatingFiles:  files,
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })
	if k > 0 && len(cands) > k {
		cands = cands[:k]
	}
	return cands
}
