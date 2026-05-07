package ark

// CRC: crc-Librarian.md | Seq: seq-spectral-expand.md
// R1235-R1254, R1268-R1273

import (
	"container/heap"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/bmatsuo/lmdb-go/lmdb"
	llama "github.com/godeps/gollama"
	"github.com/zot/microfts2"
)

// Librarian manages the expansion request queue for spectral search.
// Requests are queued by HTTP handlers and picked up by a sidecar
// agent via a lotto tube endpoint.
type Librarian struct {
	mu        sync.Mutex
	available bool
	db        *DB

	// Request queue (lotto tube)
	pending []ExpandRequest
	waiters []chan struct{} // signaled when a request is queued

	// Result store
	results map[string]*ExpandResult // requestID → result

	// Embedding model (R1277, R1278, R1593, R1594)
	model      *llama.Model
	modelCtx   *llama.Context // default context for tags/queries (2048/8)
	tiers      []EmbedTier    // sorted by byte limit ascending
	modelPath  string         // full path to GGUF file
	modelTimer *time.Timer
	modelTTL   time.Duration
	ctxSize    int // embedding context window size override (bench only) R1587
	parallel   int // parallel sequences override (bench only) R1587
}

// ExpandRequest is a queued expansion request.
type ExpandRequest struct {
	ID    string `json:"id"`
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// ExpandResult holds the result of an expansion.
type ExpandResult struct {
	ID      string `json:"id"`
	Results any    `json:"results"` // curated search results
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done"`
}

// TagAlt is an alternative tag name+value suggested by the sidecar.
type TagAlt struct {
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// TagMatch is a fuzzy-matched tag/value from V records.
type TagMatch struct {
	Tag   string   `json:"tag"`
	Value string   `json:"value"`
	Count int      `json:"count"`
	Score float64  `json:"score"`
	Paths []string `json:"paths,omitempty"`
}

// NewLibrarian creates a Librarian. Returns nil if claude is not on PATH.
// R1248, R1250, R1274
func NewLibrarian(db *DB, dbPath string) *Librarian {
	_, err := exec.LookPath("claude")
	if err != nil {
		return nil
	}
	cfg := db.Config()
	l := &Librarian{
		available: true,
		db:        db,
		results:   make(map[string]*ExpandResult),
		modelTTL:  5 * time.Minute,
		ctxSize:   2048,
		tiers:     cfg.EmbedTiers, // R1594: sorted at config load
	}
	// R1274: resolve tag_model path
	if tagModel := cfg.TagModel; tagModel != "" {
		modelPath := filepath.Join(dbPath, tagModel)
		if _, err := os.Stat(modelPath); err == nil {
			l.modelPath = modelPath
		}
	}
	return l
}

// SetCtxSize sets the embedding context window size. R1587
func (l *Librarian) SetCtxSize(n int) { l.ctxSize = n }

// SetParallel sets the number of parallel sequences per batch. R1587
func (l *Librarian) SetParallel(n int) { l.parallel = n }

// Available returns whether spectral search is possible.
// R1249
func (l *Librarian) Available() bool {
	return l != nil && l.available
}

// QueueExpand adds an expansion request to the queue.
// Returns a request ID the client can use to retrieve the result.
func (l *Librarian) QueueExpand(tag, value string) string {
	id := randomID()
	l.mu.Lock()
	l.pending = append(l.pending, ExpandRequest{ID: id, Tag: tag, Value: value})
	l.results[id] = &ExpandResult{ID: id}
	// Signal all waiters
	for _, ch := range l.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	l.waiters = nil
	l.mu.Unlock()
	return id
}

// DrainPending atomically returns and clears the pending request queue.
// Called by the lotto tube endpoint.
func (l *Librarian) DrainPending() []ExpandRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	reqs := l.pending
	l.pending = nil
	return reqs
}

// WaitForRequest blocks until a request is queued or timeout expires.
// Returns true if requests are available, false on timeout.
func (l *Librarian) WaitForRequest(timeout time.Duration) bool {
	l.mu.Lock()
	if len(l.pending) > 0 {
		l.mu.Unlock()
		return true
	}
	ch := make(chan struct{}, 1)
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()

	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// SetResult stores the result for a request ID.
// Called by the sidecar agent after processing.
func (l *Librarian) SetResult(id string, results any, errMsg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.results[id]; ok {
		r.Results = results
		r.Error = errMsg
		r.Done = true
	}
}

// WaitForResult blocks until the result is ready or timeout expires.
func (l *Librarian) WaitForResult(id string, timeout time.Duration) *ExpandResult {
	deadline := time.After(timeout)
	for {
		l.mu.Lock()
		r := l.results[id]
		l.mu.Unlock()
		if r != nil && r.Done {
			return r
		}
		select {
		case <-deadline:
			return r
		case <-time.After(200 * time.Millisecond):
			// Poll — simple and correct. The result store is small.
		}
	}
}

// CleanResults caps the result store size, removing completed results.
func (l *Librarian) CleanResults() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.results) > 100 {
		// Remove oldest completed results
		for id, r := range l.results {
			if r.Done {
				delete(l.results, id)
			}
			if len(l.results) <= 50 {
				break
			}
		}
	}
}

// FuzzyMatchTags matches alternatives against V records using trigram similarity.
// Exported so the sidecar agent can call it via a CLI command.
// R1271
func (l *Librarian) FuzzyMatchTags(alternatives []TagAlt) []TagMatch {
	tagCounts, err := l.db.store.ListTags()
	if err != nil {
		log.Printf("librarian: ListTags error: %v", err)
		return nil
	}
	tagNames := make([]string, len(tagCounts))
	for i, tc := range tagCounts {
		tagNames[i] = tc.Tag
	}

	var matches []TagMatch
	seen := make(map[string]bool)

	for _, alt := range alternatives {
		matchedTags := fuzzyMatch(alt.Tag, tagNames, 0.3)
		for _, mt := range matchedTags {
			values, err := l.db.store.QueryTagValues(mt.text, "")
			if err != nil {
				continue
			}
			if alt.Value == "" {
				for _, v := range values {
					key := mt.text + ":" + v.Value
					if seen[key] {
						continue
					}
					seen[key] = true
					paths := l.resolveTagValuePaths(mt.text, v.Value)
					if len(paths) == 0 {
						continue
					}
					matches = append(matches, TagMatch{
						Tag:   mt.text,
						Value: v.Value,
						Count: len(paths),
						Score: mt.score,
						Paths: paths,
					})
				}
			} else {
				// Match each query word independently against values
				matchedValues := fuzzyMatchWords(alt.Value, values, 0.2)
				for _, mv := range matchedValues {
					key := mt.text + ":" + mv.text
					if seen[key] {
						continue
					}
					seen[key] = true
					combined := mt.score * mv.score
					if combined < 0.15 {
						continue
					}
					paths := l.resolveTagValuePaths(mt.text, mv.text)
					if len(paths) == 0 {
						continue
					}
					matches = append(matches, TagMatch{
						Tag:   mt.text,
						Value: mv.text,
						Count: len(paths),
						Score: combined,
						Paths: paths,
					})
				}
			}
		}
	}
	return matches
}

// --- HTTP Handlers (called from Server) ---

// HandleExpand queues a curation request and returns the request ID.
// POST /search/curate
func (l *Librarian) HandleExpand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag   string `json:"tag"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Tag == "" {
		http.Error(w, "tag is required", http.StatusBadRequest)
		return
	}
	id := l.QueueExpand(req.Tag, req.Value)
	writeJSON(w, map[string]string{"requestId": id})
}

// HandleExpandWait is the lotto tube — blocks until requests are available.
// GET /search/curate/wait
func (l *Librarian) HandleExpandWait(w http.ResponseWriter, r *http.Request) {
	timeout := 120 * time.Second
	if l.WaitForRequest(timeout) {
		reqs := l.DrainPending()
		writeJSON(w, reqs)
	} else {
		writeJSON(w, []ExpandRequest{})
	}
}

// HandleExpandResult receives results from the sidecar agent.
// POST /search/curate/result
func (l *Librarian) HandleExpandResult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Results any    `json:"results"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l.SetResult(req.ID, req.Results, req.Error)
	w.WriteHeader(http.StatusOK)
}

// HandleExpandGet retrieves the result for a request ID, blocking until ready.
// GET /search/curate/result/{id}
func (l *Librarian) HandleExpandGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing request id", http.StatusBadRequest)
		return
	}
	result := l.WaitForResult(id, 60*time.Second)
	if result == nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	writeJSON(w, result)
}

// HandleFuzzyMatch runs the fuzzy matching step for the sidecar agent.
// Returns matches with file paths resolved from V record fileids.
// POST /search/expand/fuzzy
func (l *Librarian) HandleFuzzyMatch(w http.ResponseWriter, r *http.Request) {
	var alts []TagAlt
	if err := json.NewDecoder(r.Body).Decode(&alts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	matches := l.FuzzyMatchTags(alts)
	writeJSON(w, matches)
}

// HandleExpandSearch runs a grouped search for curated tags and returns
// chunk-level results. Called after the agent curates fuzzy matches.
// POST /search/expand/search
func (l *Librarian) HandleExpandSearch(w http.ResponseWriter, r *http.Request) {
	var alts []TagAlt
	if err := json.NewDecoder(r.Body).Decode(&alts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	type searchResult struct {
		Tag    string          `json:"tag"`
		Value  string          `json:"value"`
		Groups []GroupedResult `json:"groups"`
	}
	var results []searchResult
	for _, alt := range alts {
		regex := `@` + alt.Tag + `:.*` + alt.Value
		opts := SearchOpts{Regex: []string{regex}}
		groups, err := l.db.SearchGrouped("@"+alt.Tag+": "+alt.Value, opts)
		if err != nil {
			log.Printf("librarian: search for @%s: %s failed: %v", alt.Tag, alt.Value, err)
			continue
		}
		if len(groups) > 0 {
			results = append(results, searchResult{Tag: alt.Tag, Value: alt.Value, Groups: groups})
		}
	}
	writeJSON(w, results)
}

// HandleEmbedMatch runs embedding similarity search for tag values.
// POST /search/expand/embed
// CRC: crc-Librarian.md | R1297, R1300
func (l *Librarian) HandleEmbedMatch(w http.ResponseWriter, r *http.Request) {
	if !l.EmbeddingAvailable() {
		http.Error(w, "embedding model not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}
	if req.K <= 0 {
		req.K = 20
	}
	matches, err := l.EmbedSimilarTagValues(req.Query, req.K)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, matches)
}

// resolveTagValuePaths resolves V record fileids to file paths,
// filtering out paths that match the default search exclude patterns.
func (l *Librarian) resolveTagValuePaths(tag, value string) []string {
	fileids, err := l.db.store.TagValueFiles(tag, value)
	if err != nil {
		return nil
	}
	excludes := l.db.Config().SearchExclude
	var paths []string
	for _, fid := range fileids {
		info, err := l.db.fts.FileInfoByID(fid)
		if err != nil || len(info.Names) == 0 {
			continue
		}
		path := info.Names[0]
		if matchesAnyGlob(path, excludes) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// matchesAnyGlob returns true if path matches any of the glob patterns.
func matchesAnyGlob(path string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, _ := filepath.Match(pat, filepath.Base(path)); matched {
			return true
		}
		// Also try matching against the full path for ** patterns
		if strings.Contains(pat, "/") || strings.Contains(pat, "**") {
			if matched, _ := doublestar.Match(pat, path); matched {
				return true
			}
		}
	}
	return false
}

// --- Embedding (R1296-R1301) ---

// EmbeddingAvailable returns whether the embedding model is configured.
func (l *Librarian) EmbeddingAvailable() bool {
	return l != nil && l.modelPath != ""
}

// ChunkScore pairs a chunkID and its primary fileID with the cosine
// similarity score against a query vector. Returned by SearchChunks.
// FileID is the first FileID listed in the chunk's CRecord.
// CRC: crc-Librarian.md | R1917
type ChunkScore struct {
	ChunkID uint64
	FileID  uint64
	Score   float64
}

// chunkScoreHeap is a min-heap of ChunkScore by Score, used by
// SearchChunks to retain the top-k highest-scoring chunks.
type chunkScoreHeap []ChunkScore

func (h chunkScoreHeap) Len() int           { return len(h) }
func (h chunkScoreHeap) Less(i, j int) bool { return h[i].Score < h[j].Score }
func (h chunkScoreHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *chunkScoreHeap) Push(x any)        { *h = append(*h, x.(ChunkScore)) }
func (h *chunkScoreHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// AboutRequest is one query in a multi-query EC walk. There is one
// shape — top-K — because empirically a cosine-threshold reducer is
// no-op against the nomic embedding distribution.
// CRC: crc-Librarian.md | R1928
type AboutRequest struct {
	QueryVec []float32
	K        int
}

// AboutResult is the per-request top-K reduction. Index-parallel to
// the request slice returned by SearchChunksMulti.
// CRC: crc-Librarian.md | R1929
type AboutResult struct {
	TopK []ChunkScore
}

// SearchChunks ranks every EC record by cosine similarity against
// queryVec and returns the top-k highest-scoring chunks. Thin
// wrapper over SearchChunksMulti.
// CRC: crc-Librarian.md | Seq: seq-search.md | R1915, R1922, R1931
func (l *Librarian) SearchChunks(queryVec []float32, k int) ([]ChunkScore, error) {
	if k <= 0 || len(queryVec) == 0 {
		return nil, nil
	}
	res, err := l.SearchChunksMulti([]AboutRequest{{QueryVec: queryVec, K: k}})
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return res[0].TopK, nil
}

// SearchChunksMulti walks EC records once, scoring each chunk against
// every request's QueryVec and pushing onto that request's min-heap
// of size K. After the walk, every surviving chunk's FileID is
// resolved via fts.ReadCRecord inside one shared txn.
//
// The returned slice is index-parallel to reqs. EC records whose
// dimension does not match the first request's QueryVec are skipped
// (all requests must use the same model and therefore the same dim).
//
// CRC: crc-Librarian.md | Seq: seq-search.md | R1930
func (l *Librarian) SearchChunksMulti(reqs []AboutRequest) ([]AboutResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	dim := 0
	queryNorms := make([]float64, len(reqs))
	heaps := make([]*chunkScoreHeap, len(reqs))
	for i, r := range reqs {
		if len(r.QueryVec) == 0 {
			return nil, fmt.Errorf("about request %d: empty query vector", i)
		}
		if dim == 0 {
			dim = len(r.QueryVec) * 4
		}
		var ns float64
		for _, x := range r.QueryVec {
			ns += float64(x) * float64(x)
		}
		queryNorms[i] = math.Sqrt(ns)
		h := &chunkScoreHeap{}
		heap.Init(h)
		heaps[i] = h
	}

	err := l.db.store.ViewChunkEmbeddings(func(txn *lmdb.Txn, chunkID uint64, raw []byte) (bool, error) {
		if len(raw) != dim {
			return true, nil
		}
		for i, r := range reqs {
			if queryNorms[i] == 0 || r.K <= 0 {
				continue
			}
			score := cosineFromBytes(raw, r.QueryVec, queryNorms[i])
			h := heaps[i]
			if h.Len() < r.K {
				heap.Push(h, ChunkScore{ChunkID: chunkID, Score: score})
			} else if score > (*h)[0].Score {
				(*h)[0] = ChunkScore{ChunkID: chunkID, Score: score}
				heap.Fix(h, 0)
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]AboutResult, len(reqs))
	type pending struct {
		reqIdx  int
		chunkID uint64
		score   float64
	}
	var topQueue []pending
	for i := range reqs {
		h := heaps[i]
		drained := make([]ChunkScore, h.Len())
		for j := range drained {
			c := heap.Pop(h).(ChunkScore)
			drained[len(drained)-1-j] = c
		}
		results[i].TopK = make([]ChunkScore, 0, len(drained))
		for _, c := range drained {
			topQueue = append(topQueue, pending{reqIdx: i, chunkID: c.ChunkID, score: c.Score})
		}
	}

	if len(topQueue) > 0 {
		if err := l.db.fts.Env().View(func(txn *lmdb.Txn) error {
			for _, p := range topQueue {
				crec, err := l.db.fts.ReadCRecord(txn, p.chunkID)
				if err != nil || len(crec.FileIDs) == 0 {
					continue
				}
				results[p.reqIdx].TopK = append(results[p.reqIdx].TopK, ChunkScore{
					ChunkID: p.chunkID,
					FileID:  crec.FileIDs[0].FileID,
					Score:   p.score,
				})
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// cosineFromBytes computes cosine similarity between a query vector
// and a packed []float32 stored in LMDB byte form, without allocating
// an intermediate float32 slice.
func cosineFromBytes(raw []byte, query []float32, queryNorm float64) float64 {
	var dot, normSq float64
	for i, q := range query {
		bits := binary.LittleEndian.Uint32(raw[i*4:])
		x := float64(math.Float32frombits(bits))
		dot += x * float64(q)
		normSq += x * x
	}
	denom := queryNorm * math.Sqrt(normSq)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// EmbedQuery embeds a text string using the warm model.
// Loads the model on first call. Resets TTL.
// R1296
func (l *Librarian) EmbedQuery(text string) ([]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.modelPath == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}
	if err := l.ensureModel(); err != nil {
		return nil, err
	}
	l.resetModelTimer()

	vec, err := l.modelCtx.GetEmbeddings(text)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	return vec, nil
}

// EmbedBatch embeds multiple texts in one GPU dispatch.
// Much more efficient than calling EmbedQuery repeatedly.
func (l *Librarian) EmbedBatch(texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.modelPath == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}
	if err := l.ensureModel(); err != nil {
		return nil, err
	}
	l.resetModelTimer()

	vecs, err := l.modelCtx.GetEmbeddingsBatch(texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}
	return vecs, nil
}

// EmbedSimilarTagValues does two-step narrowing: cosine scan T record
// embeddings to find top-K tags, then cosine scan EV records only for
// those tags. Single query embedding for both steps (hybrid approach).
// Multiply tag × value scores. R1297, R1298, R1315, R1316
func (l *Librarian) EmbedSimilarTagValues(query string, k int) ([]TagMatch, error) {
	queryVec, err := l.EmbedQuery(query)
	if err != nil {
		return nil, err
	}

	// Step 1: cosine scan T record embeddings (~270 tags)
	tagEmbeds, err := l.db.store.ScanTagNameEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan T embeddings: %w", err)
	}
	type tagScored struct {
		tag   string
		score float64
	}
	var tagScores []tagScored
	for tag, vec := range tagEmbeds {
		s := cosineSimilarity(queryVec, vec)
		if s > 0.2 {
			tagScores = append(tagScores, tagScored{tag, s})
		}
	}
	sort.Slice(tagScores, func(i, j int) bool {
		return tagScores[i].score > tagScores[j].score
	})
	// Keep top tags for narrowing (generous limit — actual filtering is by combined score)
	maxTags := max(k*3, 20)
	if len(tagScores) > maxTags {
		tagScores = tagScores[:maxTags]
	}
	matchedTags := make(map[string]float64, len(tagScores))
	for _, ts := range tagScores {
		matchedTags[ts.tag] = ts.score
	}

	// Step 2: cosine scan EV records only for matched tags
	evs, err := l.db.store.ScanTagValueEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan EV records: %w", err)
	}
	tagValues := l.db.store.TvidMap().Snapshot()

	type scored struct {
		tvid  uint64
		score float64
	}
	var scores []scored
	for tvid, vec := range evs {
		tv, ok := tagValues[tvid]
		if !ok {
			continue
		}
		tagScore, inSet := matchedTags[tv.Tag]
		if !inSet {
			continue
		}
		valScore := cosineSimilarity(queryVec, vec)
		combined := tagScore * valScore // R1316
		if combined > 0.1 {
			scores = append(scores, scored{tvid, combined})
		}
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})
	if len(scores) > k {
		scores = scores[:k]
	}

	// Resolve tvid → tag, value, paths
	var matches []TagMatch
	for _, s := range scores {
		tv, ok := tagValues[s.tvid]
		if !ok {
			continue
		}
		paths := l.resolveTagValuePaths(tv.Tag, tv.Value)
		if len(paths) == 0 {
			continue
		}
		matches = append(matches, TagMatch{
			Tag:   tv.Tag,
			Value: tv.Value,
			Count: len(paths),
			Score: s.score,
			Paths: paths,
		})
	}
	return matches, nil
}

// BatchEmbed scans for missing tag name, tag value, and tag-definition
// embeddings, embeds them in batches, and writes the results to LMDB.
// Called post-reconcile from the write goroutine — also the rebuild
// regeneration path, since rebuild drops ED via DropEmbeddings and
// the next pass repopulates from the surviving D records.
// CRC: crc-Librarian.md | Seq: seq-tag-embed.md | R1292, R1293, R1295, R2152, R2158, R2161
func (l *Librarian) BatchEmbed() error {
	if !l.EmbeddingAvailable() {
		return nil
	}

	// Scan for missing embeddings
	missingTags, err := l.db.store.MissingTagNameEmbeddings()
	if err != nil {
		return fmt.Errorf("scan missing tag embeddings: %w", err)
	}
	missingTvids, err := l.db.store.MissingTagValueEmbeddings()
	if err != nil {
		return fmt.Errorf("scan missing value embeddings: %w", err)
	}
	missingDefs, err := l.db.store.MissingTagDefEmbeddings()
	if err != nil {
		return fmt.Errorf("scan missing tag-def embeddings: %w", err)
	}
	if len(missingTags) == 0 && len(missingTvids) == 0 && len(missingDefs) == 0 {
		return nil
	}
	log.Printf("librarian: embedding %d tag names + %d tag values + %d tag defs",
		len(missingTags), len(missingTvids), len(missingDefs))

	// Resolve tvids to text for embedding
	var tvidMap map[uint64]TagAlt
	if len(missingTvids) > 0 {
		tvidMap = l.db.store.TvidMap().Snapshot()
	}

	// Batch embed tag names (hyphens → spaces)
	batchSize := 50
	for i := 0; i < len(missingTags); i += batchSize {
		end := min(i+batchSize, len(missingTags))
		batch := missingTags[i:end]
		texts := make([]string, len(batch))
		for j, tag := range batch {
			texts[j] = strings.ReplaceAll(tag, "-", " ")
		}
		vecs, err := l.EmbedBatch(texts)
		if err != nil {
			return fmt.Errorf("embed tag names batch: %w", err)
		}
		for j, tag := range batch {
			if err := l.db.store.WriteTagNameEmbedding(tag, vecs[j]); err != nil {
				log.Printf("librarian: write tag embedding %q: %v", tag, err)
			}
		}
	}

	// Batch embed tag values ("tag: value" with hyphens → spaces in tag)
	for i := 0; i < len(missingTvids); i += batchSize {
		end := min(i+batchSize, len(missingTvids))
		batch := missingTvids[i:end]
		texts := make([]string, 0, len(batch))
		validTvids := make([]uint64, 0, len(batch))
		for _, tvid := range batch {
			tv, ok := tvidMap[tvid]
			if !ok {
				continue
			}
			text := strings.ReplaceAll(tv.Tag, "-", " ") + ": " + tv.Value
			texts = append(texts, text)
			validTvids = append(validTvids, tvid)
		}
		if len(texts) == 0 {
			continue
		}
		vecs, err := l.EmbedBatch(texts)
		if err != nil {
			return fmt.Errorf("embed tag values batch: %w", err)
		}
		for j, tvid := range validTvids {
			if err := l.db.store.WriteTagValueEmbedding(tvid, vecs[j]); err != nil {
				log.Printf("librarian: write value embedding tvid=%d: %v", tvid, err)
			}
		}
	}

	// Batch embed tag definitions. The embed text is the description
	// alone — no tag name, no hyphen-to-space rewrite. ED is queried
	// chunk → tag, so name-as-cue would bias the vector. R2152, R2158
	for i := 0; i < len(missingDefs); i += batchSize {
		end := min(i+batchSize, len(missingDefs))
		batch := missingDefs[i:end]
		texts := make([]string, len(batch))
		for j, ref := range batch {
			texts[j] = ref.Description
		}
		vecs, err := l.EmbedBatch(texts)
		if err != nil {
			return fmt.Errorf("embed tag defs batch: %w", err)
		}
		for j, ref := range batch {
			if err := l.db.store.WriteTagDefEmbedding(ref.Tag, ref.FileID, vecs[j]); err != nil {
				log.Printf("librarian: write tag-def embedding %q@%d: %v", ref.Tag, ref.FileID, err)
			}
		}
	}

	log.Printf("librarian: tag embedding complete (%d names, %d values, %d defs)",
		len(missingTags), len(missingTvids), len(missingDefs))
	return nil
}

// embedWithCtx embeds a batch using the given context. R1614
func (l *Librarian) embedWithCtx(ctx *llama.Context, texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetModelTimer()
	vecs, err := ctx.GetEmbeddingsBatch(texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}
	return vecs, nil
}

// createTierCtx creates a temporary context for one embedding tier.
// Caller must Close() when done. R1594
func (l *Librarian) createTierCtx(tier EmbedTier) (*llama.Context, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.model == nil {
		return nil, fmt.Errorf("model not loaded")
	}
	return l.model.NewContext(
		llama.WithEmbeddings(),
		llama.WithContext(tier.Ctx),
		llama.WithBatch(tier.Ctx),
		llama.WithParallel(tier.Parallel),
	)
}

// BatchEmbedChunks embeds all chunks missing EC records, using tier
// contexts for adaptive batching. Called post-reconcile after BatchEmbed.
// chunkid is the embedding key; chunkids arrive via the indexed
// callback delivered to the indexer at write time.
// CRC: crc-Librarian.md | R1609-R1617, R1913, R1914
func (l *Librarian) BatchEmbedChunks() error {
	if !l.EmbeddingAvailable() || len(l.tiers) == 0 {
		return nil
	}

	// Ensure model and tier contexts are loaded
	l.mu.Lock()
	err := l.ensureModel()
	l.mu.Unlock()
	if err != nil {
		return fmt.Errorf("load embedding model: %w", err)
	}

	// Collect all indexed file paths
	files, err := l.db.Files()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}

	// Priority sort R1611: tag-bearing files first, then non-JSONL, then JSONL.
	// Build tag-bearing file ID set from F records.
	tagFileIDs := make(map[uint64]bool)
	allTags, err := l.db.TagList()
	if err != nil {
		log.Printf("librarian: chunk embed: tag list: %v", err)
	}
	tagNames := make([]string, len(allTags))
	for i, tc := range allTags {
		tagNames[i] = tc.Tag
	}
	if len(tagNames) > 0 {
		recs, err := l.db.store.TagFiles(tagNames)
		if err != nil {
			log.Printf("librarian: chunk embed: tag files: %v", err)
		}
		for _, r := range recs {
			tagFileIDs[r.FileID] = true
		}
	}

	// Classify and sort files by priority, stash fileID to avoid redundant lookups
	type filePriority struct {
		path     string
		fileID   uint64
		priority int // 0 = tag-bearing, 1 = non-JSONL, 2 = JSONL
	}
	var sorted []filePriority
	excludePatterns := l.db.Config().SearchExclude
	for _, fpath := range files {
		if matchesAnyGlob(fpath, excludePatterns) {
			continue
		}
		info, err := l.db.fts.CheckFile(fpath)
		if err != nil || info.FileID == 0 {
			continue
		}
		pri := 1
		if strings.HasSuffix(fpath, ".jsonl") {
			pri = 2
		}
		if tagFileIDs[info.FileID] {
			pri = 0
		}
		sorted = append(sorted, filePriority{path: fpath, fileID: info.FileID, priority: pri})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].priority < sorted[j].priority
	})

	// --- Pass 1: classify chunks into tier buckets by chunkID (R1846, R1847, R1862, R1863) ---

	type chunkRef struct {
		chunkID  uint64
		fileID   uint64
		chunkIdx int
		path     string
	}
	tierRefs := make([][]chunkRef, len(l.tiers))
	filesWithNewEmbeddings := make(map[uint64]bool)
	seen := make(map[uint64]bool) // R1862: in-batch dedup
	var totalSkipped int
	var totalDeduped int

	for _, fp := range sorted {
		fileID := fp.fileID

		finfo, err := l.db.fts.FileInfoByID(fileID)
		if err != nil || len(finfo.Chunks) == 0 {
			continue
		}

		chunkLens, err := l.db.fts.ChunkContentLens(fileID)
		if err != nil || len(chunkLens) == 0 {
			continue
		}

		var fileQueued int
		for i, fce := range finfo.Chunks {
			if seen[fce.ChunkID] { // R1863: already queued from another file
				totalDeduped++
				continue
			}
			existing, _ := l.db.store.ReadChunkEmbedding(fce.ChunkID)
			if existing != nil {
				continue
			}
			if i >= len(chunkLens) || chunkLens[i] == 0 {
				continue
			}
			seen[fce.ChunkID] = true
			placed := false
			for ti, tier := range l.tiers {
				if chunkLens[i] <= tier.ByteLimit() {
					tierRefs[ti] = append(tierRefs[ti], chunkRef{
						chunkID: fce.ChunkID, fileID: fileID, chunkIdx: i, path: fp.path,
					})
					placed = true
					break
				}
			}
			if placed {
				fileQueued++
			} else {
				if err := l.db.store.WriteChunkEmbedding(fce.ChunkID, nil); err != nil {
					log.Printf("chunk embed: sentinel write chunkID=%d: %v", fce.ChunkID, err)
				}
				totalSkipped++
			}
		}
		if fileQueued > 0 {
			filesWithNewEmbeddings[fileID] = true
			log.Printf("chunk embed: queue %s (id=%d): %d new, %d total", fp.path, fileID, fileQueued, len(finfo.Chunks))
		}
	}

	// --- Pass 2: embed one tier at a time (R1830) ---

	var totalEmbedded int
	for tierIdx, refs := range tierRefs {
		if len(refs) == 0 {
			continue
		}
		tier := l.tiers[tierIdx]

		sort.Slice(refs, func(i, j int) bool {
			if refs[i].fileID != refs[j].fileID {
				return refs[i].fileID < refs[j].fileID
			}
			return refs[i].chunkIdx < refs[j].chunkIdx
		})

		ctx, err := l.createTierCtx(tier)
		if err != nil {
			log.Printf("librarian: skip tier %d/%d: %v", tier.Ctx, tier.Parallel, err)
			totalSkipped += len(refs)
			continue
		}
		log.Printf("librarian: embedding %d chunks via tier %d/%d", len(refs), tier.Ctx, tier.Parallel)

		var cachedFileID uint64
		var cachedChunks []microfts2.ChunkResult
		chunkCache := l.db.fts.NewChunkCache()
		for i := 0; i < len(refs); i += tier.Parallel {
			end := min(i+tier.Parallel, len(refs))
			batch := refs[i:end]

			texts := make([]string, 0, len(batch))
			valid := make([]chunkRef, 0, len(batch))
			for _, ref := range batch {
				if ref.fileID != cachedFileID {
					cachedChunks = nil
					finfo, err := l.db.fts.FileInfoByID(ref.fileID)
					if err == nil && len(finfo.Chunks) > 0 {
						cachedChunks, _ = chunkCache.GetChunks(ref.path, finfo.Chunks[0].Location, 0, len(finfo.Chunks))
					}
					cachedFileID = ref.fileID
				}
				if ref.chunkIdx < len(cachedChunks) && cachedChunks[ref.chunkIdx].Content != "" {
					texts = append(texts, cachedChunks[ref.chunkIdx].Content)
					valid = append(valid, ref)
				}
			}
			if len(texts) == 0 {
				continue
			}

			vecs, err := l.embedWithCtx(ctx, texts)
			if err != nil {
				log.Printf("librarian: embed tier %d/%d batch: %v", tier.Ctx, tier.Parallel, err)
				continue
			}

			// R1833: write EC records keyed by chunkID
			cvs := make([]ChunkVec, len(valid))
			for j, ref := range valid {
				cvs[j] = ChunkVec{ChunkID: ref.chunkID, Vec: vecs[j]}
			}
			if err := l.db.store.WriteChunkEmbeddingBatch(cvs); err != nil {
				log.Printf("librarian: write EC batch: %v", err)
				continue
			}
			totalEmbedded += len(valid)
		}

		ctx.Close()
	}

	// R1848: recompute EF centroids for files that got new embeddings
	for fileID := range filesWithNewEmbeddings {
		finfo, err := l.db.fts.FileInfoByID(fileID)
		if err != nil || len(finfo.Chunks) == 0 {
			continue
		}
		chunkIDs := make([]uint64, len(finfo.Chunks))
		for i, fce := range finfo.Chunks {
			chunkIDs[i] = fce.ChunkID
		}
		vecs := l.db.store.ReadChunkEmbeddings(chunkIDs)
		var sum []float32
		var count uint32
		for _, vec := range vecs {
			if len(vec) == 0 {
				continue
			}
			if sum == nil {
				sum = make([]float32, len(vec))
			}
			for i, v := range vec {
				sum[i] += v
			}
			count++
		}
		if count > 0 {
			if err := l.db.store.WriteFileCentroid(fileID, sum, count); err != nil {
				log.Printf("librarian: write centroid fileID=%d: %v", fileID, err)
			}
		}
	}

	if totalEmbedded > 0 || totalSkipped > 0 || totalDeduped > 0 { // R1864
		log.Printf("librarian: chunk embed: %d embedded, %d skipped, %d deduped", totalEmbedded, totalSkipped, totalDeduped)
	}
	return nil
}

// flushBucket embeds all chunks in a bucket and writes EC records. R1614, R1615
// Tokenizer wraps a llama model+context for tokenization only.
// CRC: crc-Librarian.md | R1529, R1530
type Tokenizer struct {
	model     *llama.Model
	ctx       *llama.Context
	modelPath string
}

// NewTokenizer loads a GGUF model and creates a minimal context for
// tokenization only (no embeddings, tiny KV cache).
// Caller must call Close() when done.
// CRC: crc-Librarian.md | R1529, R1530
func NewTokenizer(modelPath string) (*Tokenizer, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("no embedding model configured (tag_model)")
	}
	model, err := llama.LoadModel(modelPath)
	if err != nil {
		return nil, fmt.Errorf("load model %s: %w", modelPath, err)
	}
	ctx, err := model.NewContext(llama.WithContext(64))
	if err != nil {
		model.Close()
		return nil, fmt.Errorf("create tokenizer context: %w", err)
	}
	return &Tokenizer{model: model, ctx: ctx, modelPath: modelPath}, nil
}

// CountTokens returns the number of tokens in text.
func (t *Tokenizer) CountTokens(text string) int {
	tokens, err := t.ctx.Tokenize(text)
	if err != nil {
		return 0
	}
	return len(tokens)
}

// Close releases the tokenizer's model and context.
func (t *Tokenizer) Close() {
	if t.ctx != nil {
		t.ctx.Close()
	}
	if t.model != nil {
		t.model.Close()
	}
}

// ModelName returns the base filename of the model (without extension).
func (t *Tokenizer) ModelName() string {
	base := filepath.Base(t.modelPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return base
}

func (l *Librarian) ensureModel() error {
	if l.model != nil {
		return nil
	}
	model, err := llama.LoadModel(l.modelPath)
	if err != nil {
		return fmt.Errorf("load model %s: %w", l.modelPath, err)
	}
	// Default context for tags/queries (or bench override) R1595, R1597
	ctxSize := l.ctxSize
	if ctxSize <= 0 {
		ctxSize = 2048
	}
	par := l.parallel
	if par <= 0 {
		par = 8
	}
	ctx, err := model.NewContext(
		llama.WithEmbeddings(),
		llama.WithContext(ctxSize),
		llama.WithBatch(ctxSize),
		llama.WithParallel(par),
	)
	if err != nil {
		model.Close()
		return fmt.Errorf("create context: %w", err)
	}
	l.model = model
	l.modelCtx = ctx

	log.Printf("librarian: loaded embedding model %s (%d embed tiers configured)",
		filepath.Base(l.modelPath), len(l.tiers))
	return nil
}

func (l *Librarian) resetModelTimer() {
	if l.modelTimer != nil {
		l.modelTimer.Stop()
	}
	l.modelTimer = time.AfterFunc(l.modelTTL, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.unloadModel()
	})
}

func (l *Librarian) unloadModel() {
	if l.modelCtx != nil {
		l.modelCtx.Close()
		l.modelCtx = nil
	}
	if l.model != nil {
		l.model.Close()
		l.model = nil
		log.Printf("librarian: unloaded embedding model")
	}
	if l.modelTimer != nil {
		l.modelTimer.Stop()
		l.modelTimer = nil
	}
}

// cosineSimilarity computes cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// --- Helpers ---

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Word-level fuzzy matching for tag values ---

// fuzzyMatchWords splits the query into words and scores each value by
// how many query words appear (in any order). A value that contains all
// words scores highest regardless of word order.
func fuzzyMatchWords(query string, values []TagValueCount, threshold float64) []fuzzyResult {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}

	var results []fuzzyResult
	for _, v := range values {
		valueLower := strings.ToLower(v.Value)
		matched := 0
		for _, w := range words {
			if strings.Contains(valueLower, w) {
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		score := float64(matched) / float64(len(words))
		if score >= threshold {
			results = append(results, fuzzyResult{text: v.Value, score: score})
		}
	}

	// Sort by score descending
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results
}

// --- Trigram fuzzy matching ---

type fuzzyResult struct {
	text  string
	score float64
}

// fuzzyMatch returns candidates from corpus that are similar to query,
// scored by trigram overlap (or substring containment for short queries).
func fuzzyMatch(query string, corpus []string, threshold float64) []fuzzyResult {
	if query == "" {
		results := make([]fuzzyResult, len(corpus))
		for i, c := range corpus {
			results[i] = fuzzyResult{text: c, score: 1.0}
		}
		return results
	}
	queryLower := strings.ToLower(query)

	var results []fuzzyResult
	for _, candidate := range corpus {
		candidateLower := strings.ToLower(candidate)
		var score float64

		if len([]rune(queryLower)) < 3 {
			if strings.Contains(candidateLower, queryLower) {
				score = float64(len(queryLower)) / float64(len(candidateLower))
				if score < 0.1 {
					score = 0.1
				}
			} else if strings.Contains(queryLower, candidateLower) {
				score = float64(len(candidateLower)) / float64(len(queryLower))
			}
		} else {
			queryTrigrams := trigrams(queryLower)
			candidateTrigrams := trigrams(candidateLower)
			if len(candidateTrigrams) == 0 {
				continue
			}
			score = trigramOverlap(queryTrigrams, candidateTrigrams)
		}

		if score >= threshold {
			results = append(results, fuzzyResult{text: candidate, score: score})
		}
	}

	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results
}

func trigrams(s string) map[string]bool {
	t := make(map[string]bool)
	runes := []rune(s)
	if len(runes) < 3 {
		if utf8.RuneCountInString(s) > 0 {
			t[s] = true
		}
		return t
	}
	for i := 0; i <= len(runes)-3; i++ {
		t[string(runes[i:i+3])] = true
	}
	return t
}

func trigramOverlap(a, b map[string]bool) float64 {
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
