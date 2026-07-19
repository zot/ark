package ark

// CRC: crc-Librarian.md | Seq: seq-spectral-expand.md
// R1246, R1270-R1273

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
	"github.com/zot/microfts2"
	"go.etcd.io/bbolt"
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

	// Embedding model (R1594)
	// CRC: crc-Librarian.md | R1277, R1278, R1593
	model      *embedModel
	modelCtx   *embedContext // default context for tags/queries (2048/8)
	tiers      []EmbedTier   // sorted by byte limit ascending
	modelPath  string        // full path to GGUF file
	libDir     string        // dir holding the runtime-loaded llama.cpp libs (R2966)
	modelTimer *time.Timer
	modelTTL   time.Duration
	ctxSize    int // embedding context window size override (bench only) R1793
	parallel   int // parallel sequences override (bench only) R1793

	// Find Connections (1G) — second lotto tube + orchestrator state.
	// CRC: crc-Librarian.md | Seq: seq-find-connections.md
	// R2319-R2321
	pendingConnections     []ConnectionsRequest
	connectionsWaiters     []chan struct{}
	connectionsResults     map[string]*ConnectionsRecord
	connectionsLastWait    time.Time
	connectionsAvailWindow time.Duration
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

// NewLibrarian creates a Librarian. The constructor succeeds whether
// or not `claude` is on PATH; the `available` flag records claude's
// presence so Available() can report it. Recall, embed, substrate,
// and tag-embedding paths do not require claude.
// R1248, R1250, R1274, R2642
func NewLibrarian(db *DB, dbPath string) *Librarian {
	_, err := exec.LookPath("claude")
	cfg := db.Config()
	l := &Librarian{
		available:              err == nil,
		db:                     db,
		results:                make(map[string]*ExpandResult),
		modelTTL:               5 * time.Minute,
		ctxSize:                2048,
		tiers:                  cfg.Embedding.Tiers, // R1594: sorted at config load
		libDir:                 cfg.Embedding.ResolveLibDir(dbPath),
		connectionsResults:     make(map[string]*ConnectionsRecord),
		connectionsAvailWindow: 60 * time.Second, // R2320
	}
	// R2964: resolve the embedding model path. R1275: the model path is
	// relative to the database directory (~/.ark/). R1276: if [embedding]
	// model is empty or the file does not exist, modelPath stays "" so
	// EmbeddingAvailable reports disabled and search falls back to trigram
	// fuzzy.
	// CRC: crc-Librarian.md | R1275, R1276
	if model := cfg.Embedding.Model; model != "" {
		modelPath := filepath.Join(dbPath, model)
		if _, err := os.Stat(modelPath); err == nil {
			l.modelPath = modelPath
		}
	}
	return l
}

// SetCtxSize sets the embedding context window size. R1793
func (l *Librarian) SetCtxSize(n int) { l.ctxSize = n }

// SetParallel sets the number of parallel sequences per batch. R1793
func (l *Librarian) SetParallel(n int) { l.parallel = n }

// Available returns whether spectral search is possible (i.e., whether
// `claude` was on PATH at construction). Recall, embed, and substrate
// callers do not need to gate on this.
// R1249, R2642
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
// CRC: crc-Librarian.md | R1378, R1379
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
// CRC: crc-Librarian.md | R1380
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
// CRC: crc-Librarian.md | R1381
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
// CRC: crc-Librarian.md | R1382
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
	// R3165: HandleExpandSearch runs on the HTTP goroutine, not the actor,
	// and SearchGrouped resolves result paths through the fts Go-side
	// caches (FileIDPaths / FileInfoByID) that the write actor nils via
	// InvalidateCaches. One private copy for the whole expansion loop —
	// the same read seam as substrateOp and recallOp.
	rdb := l.db.withFTS(l.db.fts.Copy())

	var results []searchResult
	for _, alt := range alts {
		regex := `@` + alt.Tag + `:.*` + alt.Value
		opts := SearchOpts{Regex: []string{regex}}
		groups, err := rdb.SearchGrouped("@"+alt.Tag+": "+alt.Value, opts)
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

// resolveTagValuePaths resolves a (tag, value)'s V-record chunkids to
// the file paths that own them, filtering out paths that match the
// default search exclude patterns. The V blob holds chunkids, so it
// resolves chunkid → fileid via Store.FilesForChunks (overlay-aware)
// before mapping each fileid → path.
// CRC: crc-Librarian.md | Seq: seq-tag-value-index.md | R1887
func (l *Librarian) resolveTagValuePaths(tag, value string) []string {
	chunkids, err := l.db.store.TagValueChunks(tag, value)
	if err != nil {
		return nil
	}
	chunkSet := make(map[uint64]bool, len(chunkids))
	for _, cid := range chunkids {
		chunkSet[cid] = true
	}
	excludes := l.db.Config().SearchExclude
	var paths []string
	for fid := range l.db.store.FilesForChunks(chunkSet) {
		path, ok := l.db.resolveFilePath(fid)
		if !ok {
			continue
		}
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

// --- Embedding (R1296-R1300) ---
// CRC: crc-Librarian.md | R1299, R1301

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

	err := l.db.store.ViewChunkEmbeddings(func(txn *bbolt.Tx, chunkID uint64, raw []byte) (bool, error) {
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
		if err := l.db.fts.DB().View(func(txn *bbolt.Tx) error {
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

// TagSuggestion is a tag-name candidate ranked against a chunk's EC
// vector. Score is the best (max) cosine across the tag's ED records;
// MotivatingFiles ranks every contributing file's score descending.
// CRC: crc-Librarian.md | R2166
type TagSuggestion struct {
	Tag             string
	Score           float64
	MotivatingFiles []TagSuggestionRef
}

// TagSuggestionRef identifies one definition file that contributed to
// a tag suggestion. Path is empty if the fileid has no FTS path entry.
// CRC: crc-Librarian.md | R2166, R2167
type TagSuggestionRef struct {
	FileID uint64
	Path   string
	Score  float64
}

// SuggestTagNames returns up to k tag names whose ED vectors are
// nearest the chunk's EC vector. Pure cosine math; no model call.
// Returns (nil, nil) for: k <= 0, missing EC, embedding unavailable,
// empty ED prefix. Dimension-mismatched ED records are skipped.
// CRC: crc-Librarian.md | Seq: seq-suggest-tags.md | R2163, R2164, R2165, R2166, R2167, R2168, R2169, R2170, R2171, R2172, R2173
func (l *Librarian) SuggestTagNames(chunkID uint64, k int) ([]TagSuggestion, error) {
	if k <= 0 || !l.EmbeddingAvailable() {
		return nil, nil
	}
	chunkVec, err := l.db.store.ReadChunkEmbedding(chunkID)
	if err != nil {
		return nil, fmt.Errorf("read chunk embedding: %w", err)
	}
	if chunkVec == nil {
		return nil, nil
	}
	eds, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	if len(eds) == 0 {
		return nil, nil
	}

	// Aggregate per tag: best score wins, every contributing file kept.
	perTag := make(map[string]*TagSuggestion)
	for _, ed := range eds {
		if len(ed.Vec) != len(chunkVec) {
			continue
		}
		score := cosineSimilarity(chunkVec, ed.Vec)
		ref := TagSuggestionRef{FileID: ed.FileID, Score: score}
		if cur, ok := perTag[ed.Tag]; ok {
			if score > cur.Score {
				cur.Score = score
			}
			cur.MotivatingFiles = append(cur.MotivatingFiles, ref)
		} else {
			perTag[ed.Tag] = &TagSuggestion{
				Tag:             ed.Tag,
				Score:           score,
				MotivatingFiles: []TagSuggestionRef{ref},
			}
		}
	}
	if len(perTag) == 0 {
		return nil, nil
	}

	out := make([]TagSuggestion, 0, len(perTag))
	for _, s := range perTag {
		sort.Slice(s.MotivatingFiles, func(i, j int) bool {
			return s.MotivatingFiles[i].Score > s.MotivatingFiles[j].Score
		})
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}

	// Resolve paths once. Failure is non-fatal — degraded results
	// (empty Path) beat refusing to surface tag candidates — but it
	// shouldn't pass silently if microfts2 itself errors.
	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: SuggestTagNames path resolution: %v", perr)
	}
	for i := range out {
		for j := range out[i].MotivatingFiles {
			out[i].MotivatingFiles[j].Path = paths[out[i].MotivatingFiles[j].FileID]
		}
	}
	return out, nil
}

// ChunkSuggestion is a chunk-candidate ranked against a tag's ED
// vectors. Score is the best (max) cosine across the tag's defs;
// MotivatingDefs ranks every contributing def file's score descending.
// CRC: crc-Librarian.md | R2205
type ChunkSuggestion struct {
	ChunkID        uint64
	FileID         uint64
	Path           string
	Score          float64
	MotivatingDefs []DefMatch
}

// DefMatch identifies one tag-definition file that contributed to a
// chunk suggestion. Path is empty if the fileid has no FTS path entry.
// CRC: crc-Librarian.md | R2206
type DefMatch struct {
	FileID uint64
	Path   string
	Score  float64
}

// chunkAgg is a per-chunk aggregate kept on the heap during the EC
// walk. perDef is index-parallel to the def slice passed to
// chunksForDefs; it lets surviving chunks reconstruct MotivatingDefs
// with each def file's score after the walk.
type chunkAgg struct {
	chunkID uint64
	score   float64
	perDef  []float64
}

type chunkAggHeap []chunkAgg

func (h chunkAggHeap) Len() int           { return len(h) }
func (h chunkAggHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h chunkAggHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *chunkAggHeap) Push(x any)        { *h = append(*h, x.(chunkAgg)) }
func (h *chunkAggHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// ChunksForTag returns up to k chunks whose EC vectors are nearest
// any of the named tag's ED vectors. Aggregate per chunk is the max
// cosine across the tag's defs. Read-only; no model invocation.
// Returns (nil, nil) for: k <= 0, embedding unavailable, tag with no
// ED records, empty EC prefix, or no surviving chunks after CRecord
// resolution.
// CRC: crc-Librarian.md | Seq: seq-chunks-for-tag.md | R2194, R2196, R2197, R2202, R2207, R2208, R2209, R2211, R2212, R2213, R2214, R2215
func (l *Librarian) ChunksForTag(tag string, k int) ([]ChunkSuggestion, error) {
	if k <= 0 || !l.EmbeddingAvailable() {
		return nil, nil
	}
	all, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	var defs []TagDefEmbedding
	for _, ed := range all {
		if ed.Tag == tag {
			defs = append(defs, ed)
		}
	}
	if len(defs) == 0 {
		return nil, nil
	}
	return l.chunksForDefs(defs, k)
}

// ChunksForTagDef returns up to k chunks whose EC vectors are nearest
// the single ED[tag, fileid] record. Useful for reconciling divergent
// definitions of the same tag across files. Each result chunk's
// MotivatingDefs is a single-entry slice with the requested
// (fileid, path, score). Read-only; no model invocation.
// CRC: crc-Librarian.md | Seq: seq-chunks-for-tag.md | R2195, R2198, R2200, R2201, R2203, R2204, R2207, R2208, R2210, R2211, R2212, R2213, R2214, R2215
func (l *Librarian) ChunksForTagDef(tag string, fileid uint64, k int) ([]ChunkSuggestion, error) {
	if k <= 0 || !l.EmbeddingAvailable() {
		return nil, nil
	}
	vec, err := l.db.store.ReadTagDefEmbedding(tag, fileid)
	if err != nil {
		return nil, fmt.Errorf("read tag-def embedding: %w", err)
	}
	if vec == nil {
		return nil, nil
	}
	return l.chunksForDefs([]TagDefEmbedding{{Tag: tag, FileID: fileid, Vec: vec}}, k)
}

// chunksForDefs runs the EC walk against a fixed set of ED query
// vectors and returns the top-k chunks ranked by max cosine across
// those defs. Shared backbone for ChunksForTag (multi-def) and
// ChunksForTagDef (single def).
// CRC: crc-Librarian.md | R2196, R2197, R2198, R2199, R2200, R2201, R2202
func (l *Librarian) chunksForDefs(defs []TagDefEmbedding, k int) ([]ChunkSuggestion, error) {
	dimBytes := len(defs[0].Vec) * 4
	queryNorms := make([]float64, len(defs))
	for i, d := range defs {
		var ns float64
		for _, x := range d.Vec {
			ns += float64(x) * float64(x)
		}
		queryNorms[i] = math.Sqrt(ns)
	}

	h := &chunkAggHeap{}
	heap.Init(h)

	err := l.db.store.ViewChunkEmbeddings(func(_ *bbolt.Tx, chunkID uint64, raw []byte) (bool, error) {
		if len(raw) != dimBytes {
			return true, nil
		}
		perDef := make([]float64, len(defs))
		var maxScore float64
		first := true
		for i, d := range defs {
			if queryNorms[i] == 0 {
				continue
			}
			s := cosineFromBytes(raw, d.Vec, queryNorms[i])
			perDef[i] = s
			if first || s > maxScore {
				maxScore = s
				first = false
			}
		}
		if h.Len() < k {
			heap.Push(h, chunkAgg{chunkID: chunkID, score: maxScore, perDef: perDef})
		} else if maxScore > (*h)[0].score {
			(*h)[0] = chunkAgg{chunkID: chunkID, score: maxScore, perDef: perDef}
			heap.Fix(h, 0)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if h.Len() == 0 {
		return nil, nil
	}

	drained := make([]chunkAgg, h.Len())
	for j := range drained {
		c := heap.Pop(h).(chunkAgg)
		drained[len(drained)-1-j] = c
	}

	type resolvedChunk struct {
		chunkAgg
		fileID uint64
	}
	var resolved []resolvedChunk
	if err := l.db.fts.DB().View(func(txn *bbolt.Tx) error {
		for _, c := range drained {
			crec, rerr := l.db.fts.ReadCRecord(txn, c.chunkID)
			if rerr != nil || len(crec.FileIDs) == 0 {
				continue
			}
			resolved = append(resolved, resolvedChunk{chunkAgg: c, fileID: crec.FileIDs[0].FileID})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, nil
	}

	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: chunksForDefs path resolution: %v", perr)
	}

	out := make([]ChunkSuggestion, 0, len(resolved))
	for _, r := range resolved {
		defMatches := make([]DefMatch, len(defs))
		for i, d := range defs {
			defMatches[i] = DefMatch{
				FileID: d.FileID,
				Path:   paths[d.FileID],
				Score:  r.perDef[i],
			}
		}
		sort.Slice(defMatches, func(i, j int) bool {
			return defMatches[i].Score > defMatches[j].Score
		})
		out = append(out, ChunkSuggestion{
			ChunkID:        r.chunkID,
			FileID:         r.fileID,
			Path:           paths[r.fileID],
			Score:          r.score,
			MotivatingDefs: defMatches,
		})
	}
	return out, nil
}

// K_TOP_HC is the per-tag top-K bound enforced by the hot-correlations
// sweep. Fixed for this slice; configurability is a future tuning
// question.
// CRC: crc-Librarian.md | R2228
const K_TOP_HC = 20

// HCSweepResult is the summary returned by SweepHotCorrelations and
// also reflected as @sweep-* tags in the tmp:// progress doc. (Named
// HCSweep- to distinguish from db.go's file-removal SweepResult.)
// CRC: crc-Librarian.md | R2217
type HCSweepResult struct {
	StartedAt   time.Time
	CompletedAt time.Time
	DurationMS  int64
	ChangedEDs  int
	ChangedECs  int
	TagsRebuilt int
	TagsTouched int
	OrphanTotal int
	FromScratch bool
}

// TagSimilarity is one tag-pair score for RelatedTags / TagPairConflict.
// Tag is empty when both tags are inputs to the call (TagPairConflict).
// CRC: crc-Librarian.md | R2224
type TagSimilarity struct {
	Tag       string
	Score     float64
	SrcFileID uint64
	DstFileID uint64
	SrcPath   string
	DstPath   string
}

// DriftPair is one within-tag def-vs-def cosine for TagDrift. FileIDA <
// FileIDB by convention so pairs are canonical.
// CRC: crc-Librarian.md | R2225
type DriftPair struct {
	FileIDA uint64
	FileIDB uint64
	PathA   string
	PathB   string
	Score   float64
}

// edsByTag groups ED records by tag. Used by the sweep and tag-tag
// queries so the cosine math can iterate per tag without re-scanning.
func edsByTag(eds []TagDefEmbedding) map[string][]TagDefEmbedding {
	out := make(map[string][]TagDefEmbedding)
	for _, ed := range eds {
		out[ed.Tag] = append(out[ed.Tag], ed)
	}
	return out
}

// vecNorm computes the Euclidean norm of a float32 vector as float64.
func vecNorm(v []float32) float64 {
	var ns float64
	for _, x := range v {
		ns += float64(x) * float64(x)
	}
	return math.Sqrt(ns)
}

// maxCosineAgainstDefs returns the max cosine of vec against every def's
// vector. Skips defs whose dimension differs from vec. Returns -1 if no
// def can be scored (caller treats as "skip this chunk-tag pair").
func maxCosineAgainstDefs(vec []float32, defs []TagDefEmbedding) float64 {
	best := math.Inf(-1)
	for _, d := range defs {
		if len(d.Vec) != len(vec) {
			continue
		}
		s := cosineSimilarity(vec, d.Vec)
		if s > best {
			best = s
		}
	}
	return best
}

// RelatedTags returns up to k tags whose ED vectors are nearest the
// named tag's. Per-other-tag aggregate is the max cosine across all
// (def_self, def_other) pairs. Live; no cache.
// CRC: crc-Librarian.md | R2221, R2224
func (l *Librarian) RelatedTags(tag string, k int) ([]TagSimilarity, error) {
	if k <= 0 || !l.EmbeddingAvailable() {
		return nil, nil
	}
	all, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	groups := edsByTag(all)
	selfDefs, ok := groups[tag]
	if !ok || len(selfDefs) == 0 {
		return nil, nil
	}

	out := make([]TagSimilarity, 0, len(groups))
	for otherTag, otherDefs := range groups {
		if otherTag == tag {
			continue
		}
		var best float64
		var bestSrc, bestDst uint64
		first := true
		for _, sd := range selfDefs {
			for _, od := range otherDefs {
				if len(sd.Vec) != len(od.Vec) {
					continue
				}
				s := cosineSimilarity(sd.Vec, od.Vec)
				if first || s > best {
					best = s
					bestSrc = sd.FileID
					bestDst = od.FileID
					first = false
				}
			}
		}
		if first {
			continue
		}
		out = append(out, TagSimilarity{
			Tag:       otherTag,
			Score:     best,
			SrcFileID: bestSrc,
			DstFileID: bestDst,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: RelatedTags path resolution: %v", perr)
	}
	for i := range out {
		out[i].SrcPath = paths[out[i].SrcFileID]
		out[i].DstPath = paths[out[i].DstFileID]
	}
	return out, nil
}

// TagPairConflict returns the max-pair cosine between two tags and the
// (SrcFileID, DstFileID) defs that scored it.
// CRC: crc-Librarian.md | R2222, R2224
func (l *Librarian) TagPairConflict(tagA, tagB string) (TagSimilarity, error) {
	if !l.EmbeddingAvailable() {
		return TagSimilarity{}, nil
	}
	all, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return TagSimilarity{}, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	groups := edsByTag(all)
	defsA, okA := groups[tagA]
	defsB, okB := groups[tagB]
	if !okA || !okB || len(defsA) == 0 || len(defsB) == 0 {
		return TagSimilarity{}, nil
	}
	var best float64
	var bestA, bestB uint64
	first := true
	for _, da := range defsA {
		for _, db := range defsB {
			if len(da.Vec) != len(db.Vec) {
				continue
			}
			s := cosineSimilarity(da.Vec, db.Vec)
			if first || s > best {
				best = s
				bestA = da.FileID
				bestB = db.FileID
				first = false
			}
		}
	}
	if first {
		return TagSimilarity{}, nil
	}
	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: TagPairConflict path resolution: %v", perr)
	}
	return TagSimilarity{
		Score:     best,
		SrcFileID: bestA,
		DstFileID: bestB,
		SrcPath:   paths[bestA],
		DstPath:   paths[bestB],
	}, nil
}

// TagDrift returns pairwise within-tag def cosines, sorted descending.
// For a tag with n defs the result has n*(n-1)/2 pairs. FileIDA <
// FileIDB in each pair to canonicalize the ordering.
// CRC: crc-Librarian.md | R2223, R2225
func (l *Librarian) TagDrift(tag string) ([]DriftPair, error) {
	if !l.EmbeddingAvailable() {
		return nil, nil
	}
	all, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	defs := edsByTag(all)[tag]
	if len(defs) < 2 {
		return nil, nil
	}
	var pairs []DriftPair
	for i := range defs {
		for j := i + 1; j < len(defs); j++ {
			a, b := defs[i], defs[j]
			if len(a.Vec) != len(b.Vec) {
				continue
			}
			fidA, fidB := a.FileID, b.FileID
			if fidA > fidB {
				fidA, fidB = fidB, fidA
			}
			pairs = append(pairs, DriftPair{
				FileIDA: fidA,
				FileIDB: fidB,
				Score:   cosineSimilarity(a.Vec, b.Vec),
			})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Score > pairs[j].Score })
	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: TagDrift path resolution: %v", perr)
	}
	for i := range pairs {
		pairs[i].PathA = paths[pairs[i].FileIDA]
		pairs[i].PathB = paths[pairs[i].FileIDB]
	}
	return pairs, nil
}

// TopKChunksForTag reads the cached top-K chunks for a tag from the HC
// cache, applying the alibi-stamp freshness filter at read time. Same
// ChunkSuggestion shape as ChunksForTag — callers can treat them
// interchangeably. MotivatingDefs is empty (the cache doesn't preserve
// per-def winners; ChunksForTag is the live path that does).
// CRC: crc-Librarian.md | Seq: seq-hot-correlations.md | R2218, R2219, R2220, R2249, R2250, R2252, R2253, R2257
func (l *Librarian) TopKChunksForTag(tag string, k int) ([]ChunkSuggestion, error) {
	if k <= 0 || !l.EmbeddingAvailable() {
		return nil, nil
	}
	entries, err := l.db.store.ReadHotCorrelations(tag)
	if err != nil {
		return nil, fmt.Errorf("read hot-correlations: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	maxEDSerial, err := l.db.store.MaxTagDefSerial(tag)
	if err != nil {
		return nil, fmt.Errorf("max ed serial: %w", err)
	}
	if maxEDSerial == 0 {
		// All defs deleted or never existed — entries are stranded.
		return nil, nil
	}

	type survivor struct {
		chunkID uint64
		score   float64
		fileID  uint64
	}
	var survivors []survivor
	hcPrefix := []byte(prefixHotCorrelation)
	ecPrefix := []byte(prefixEmbedChunk)

	for _, e := range entries {
		hcKeyTail := hotCorrKey(tag, e.ChunkID)[len(prefixHotCorrelation):]
		hcSerial, hcFound, err := l.db.store.RecordSerial(hcPrefix, hcKeyTail)
		if err != nil {
			return nil, fmt.Errorf("hc serial: %w", err)
		}
		if !hcFound {
			continue
		}
		// EC freshness: existence + serial.
		ecKeyTail := chunkEmbedKey(e.ChunkID)[len(prefixEmbedChunk):]
		ecSerial, ecFound, err := l.db.store.RecordSerial(ecPrefix, ecKeyTail)
		if err != nil {
			return nil, fmt.Errorf("ec serial: %w", err)
		}
		if !ecFound {
			continue
		}
		if hcSerial < ecSerial {
			continue
		}
		// ED freshness: any tag def moved past this entry's stamp.
		if hcSerial < maxEDSerial {
			continue
		}
		survivors = append(survivors, survivor{chunkID: e.ChunkID, score: e.Score})
	}
	if len(survivors) == 0 {
		return nil, nil
	}

	// Resolve primary FileID per chunk via fts.ReadCRecord.
	if err := l.db.fts.DB().View(func(txn *bbolt.Tx) error {
		filtered := survivors[:0]
		for _, s := range survivors {
			crec, rerr := l.db.fts.ReadCRecord(txn, s.chunkID)
			if rerr != nil || len(crec.FileIDs) == 0 {
				continue
			}
			s.fileID = crec.FileIDs[0].FileID
			filtered = append(filtered, s)
		}
		survivors = filtered
		return nil
	}); err != nil {
		return nil, err
	}
	if len(survivors) == 0 {
		return nil, nil
	}

	paths, perr := l.db.fts.FileIDPaths()
	if perr != nil {
		log.Printf("librarian: TopKChunksForTag path resolution: %v", perr)
	}

	sort.Slice(survivors, func(i, j int) bool { return survivors[i].score > survivors[j].score })
	if len(survivors) > k {
		survivors = survivors[:k]
	}
	out := make([]ChunkSuggestion, 0, len(survivors))
	for _, s := range survivors {
		out = append(out, ChunkSuggestion{
			ChunkID:        s.chunkID,
			FileID:         s.fileID,
			Path:           paths[s.fileID],
			Score:          s.score,
			MotivatingDefs: nil,
		})
	}
	return out, nil
}

// sweepProgress carries throttled progress state for the hot-correlations
// sweep. Updates the tmp://sweep/hot-correlations.md document at most
// every 250 ms, with terminal-state flushes immediate.
// CRC: crc-Librarian.md | R2240, R2241, R2242, R2243, R2244
type sweepProgress struct {
	db        *DB
	docPath   string
	throttle  time.Duration
	started   time.Time
	lastFlush time.Time

	status      string
	phase       string
	progress    float64
	changedEDs  int
	changedECs  int
	tagsRebuilt int
	tagsTouched int
	orphanTotal int
	durationMS  int64
	completed   time.Time
	errMsg      string
}

func newSweepProgress(db *DB) *sweepProgress {
	return &sweepProgress{
		db:       db,
		docPath:  "tmp://sweep/hot-correlations.md",
		throttle: 250 * time.Millisecond,
		status:   "idle",
		phase:    "",
	}
}

// start records the sweep's start time and immediately flushes a
// `running` snapshot.
func (sp *sweepProgress) start(changedEDs, changedECs int) {
	sp.started = time.Now()
	sp.status = "running"
	sp.phase = "tag-rebuild"
	sp.progress = 0
	sp.changedEDs = changedEDs
	sp.changedECs = changedECs
	sp.tagsRebuilt = 0
	sp.tagsTouched = 0
	sp.orphanTotal = 0
	sp.durationMS = 0
	sp.completed = time.Time{}
	sp.errMsg = ""
	sp.flushNow()
}

// tick updates progress fraction and phase. Honors the throttle —
// writes to the doc only if the previous flush was at least throttle
// ago.
func (sp *sweepProgress) tick(phase string, frac float64) {
	sp.phase = phase
	sp.progress = frac
	if time.Since(sp.lastFlush) >= sp.throttle {
		sp.flushNow()
	}
}

// complete flips status to "complete", populates the result fields, and
// flushes immediately.
func (sp *sweepProgress) complete(r *HCSweepResult) {
	sp.status = "complete"
	sp.phase = "done"
	sp.progress = 1.0
	sp.tagsRebuilt = r.TagsRebuilt
	sp.tagsTouched = r.TagsTouched
	sp.orphanTotal = r.OrphanTotal
	sp.durationMS = r.DurationMS
	sp.completed = r.CompletedAt
	sp.flushNow()
}

// fail flips status to "error" and flushes immediately.
func (sp *sweepProgress) fail(err error) {
	sp.status = "error"
	sp.errMsg = err.Error()
	sp.completed = time.Now()
	sp.durationMS = sp.completed.Sub(sp.started).Milliseconds()
	sp.flushNow()
}

// flushNow renders the current state and writes it to the tmp:// doc,
// regardless of throttle. Called from inside the sweep's write
// goroutine so the writes coordinate with all other DB mutators.
func (sp *sweepProgress) flushNow() {
	content := sp.render()
	if sp.db.hasTmpPath(sp.docPath) {
		if err := sp.db.UpdateTmpFile(sp.docPath, "markdown", content); err != nil {
			log.Printf("sweep progress update: %v", err)
		}
	} else {
		if _, err := sp.db.AddTmpFile(sp.docPath, "markdown", content); err != nil {
			log.Printf("sweep progress add: %v", err)
		}
	}
	sp.lastFlush = time.Now()
}

// render produces the markdown body with @sweep-* tags reflecting
// current state. R2241.
func (sp *sweepProgress) render() []byte {
	var b strings.Builder
	b.WriteString("@sweep: hot-correlations\n")
	fmt.Fprintf(&b, "@sweep-status: %s\n", sp.status)
	if !sp.started.IsZero() {
		fmt.Fprintf(&b, "@sweep-started: %s\n", sp.started.UTC().Format(time.RFC3339))
	}
	if sp.status == "running" || sp.status == "complete" || sp.status == "error" {
		fmt.Fprintf(&b, "@sweep-phase: %s\n", sp.phase)
		fmt.Fprintf(&b, "@sweep-progress: %.2f\n", sp.progress)
		fmt.Fprintf(&b, "@sweep-changed-eds: %d\n", sp.changedEDs)
		fmt.Fprintf(&b, "@sweep-changed-ecs: %d\n", sp.changedECs)
		fmt.Fprintf(&b, "@sweep-tags-rebuilt: %d\n", sp.tagsRebuilt)
		fmt.Fprintf(&b, "@sweep-tags-touched: %d\n", sp.tagsTouched)
		fmt.Fprintf(&b, "@sweep-orphan-total: %d\n", sp.orphanTotal)
	}
	if !sp.completed.IsZero() {
		fmt.Fprintf(&b, "@sweep-completed: %s\n", sp.completed.UTC().Format(time.RFC3339))
		fmt.Fprintf(&b, "@sweep-duration-ms: %d\n", sp.durationMS)
	}
	if sp.errMsg != "" {
		fmt.Fprintf(&b, "@sweep-error: %s\n", sp.errMsg)
	}
	return []byte(b.String())
}

// SweepHotCorrelations runs the incremental corpus-wide cosine sweep
// against the HC cache. Reads I:hcsweep, walks SED/SEC for changes,
// rebuilds affected tags (phase 3), displaces against unchanged tags
// (phase 4), advances the bookmark on success. Per-tag write txns
// for crash safety. Progress is published through the tmp:// doc.
// CRC: crc-Librarian.md | Seq: seq-hot-correlations.md | R2216, R2217, R2230, R2232, R2233, R2234, R2235, R2236, R2237, R2238, R2239, R2245, R2251, R2252, R2253, R2254, R2255, R2256, R2257
func (l *Librarian) SweepHotCorrelations() (*HCSweepResult, error) {
	if !l.EmbeddingAvailable() {
		return nil, nil
	}

	progress := newSweepProgress(l.db)

	bookmark, err := l.db.store.IGetCounter("hcsweep")
	if err != nil {
		return nil, fmt.Errorf("read hcsweep bookmark: %w", err)
	}
	fromScratch := bookmark == 0

	// Survey changed work via the S substrate.
	type edChange struct {
		tag    string
		fileID uint64
		serial uint64
	}
	var changedEDs []edChange
	var maxSeen uint64 = bookmark
	if err := l.db.store.WalkRecordsSinceSerial([]byte(prefixEmbedDef), bookmark,
		func(origKey []byte, serial uint64) error {
			// origKey = ED + tag + fileid:8
			if len(origKey) < len(prefixEmbedDef)+8 {
				return nil
			}
			tag := string(origKey[len(prefixEmbedDef) : len(origKey)-8])
			fid := binary.BigEndian.Uint64(origKey[len(origKey)-8:])
			changedEDs = append(changedEDs, edChange{tag: tag, fileID: fid, serial: serial})
			if serial > maxSeen {
				maxSeen = serial
			}
			return nil
		}); err != nil {
		progress.fail(err)
		return nil, fmt.Errorf("walk SED: %w", err)
	}

	type ecChange struct {
		chunkID uint64
		serial  uint64
	}
	var changedECs []ecChange
	if err := l.db.store.WalkRecordsSinceSerial([]byte(prefixEmbedChunk), bookmark,
		func(origKey []byte, serial uint64) error {
			if len(origKey) < len(prefixEmbedChunk) {
				return nil
			}
			rest := origKey[len(prefixEmbedChunk):]
			cid, _ := binary.Uvarint(rest)
			changedECs = append(changedECs, ecChange{chunkID: cid, serial: serial})
			if serial > maxSeen {
				maxSeen = serial
			}
			return nil
		}); err != nil {
		progress.fail(err)
		return nil, fmt.Errorf("walk SEC: %w", err)
	}

	progress.start(len(changedEDs), len(changedECs))

	// Pre-load all defs grouped by tag.
	allDefs, err := l.db.store.ScanTagDefEmbeddings()
	if err != nil {
		progress.fail(err)
		return nil, fmt.Errorf("scan tag-def embeddings: %w", err)
	}
	defsByTag := edsByTag(allDefs)

	// Identify tags needing full rebuild.
	tagsToRebuild := make(map[string]struct{})
	for _, c := range changedEDs {
		tagsToRebuild[c.tag] = struct{}{}
	}

	result := &HCSweepResult{
		StartedAt:   progress.started,
		ChangedEDs:  len(changedEDs),
		ChangedECs:  len(changedECs),
		FromScratch: fromScratch,
	}

	// Phase 3: tag rebuild.
	totalRebuilds := len(tagsToRebuild)
	rebuildIdx := 0
	for tag := range tagsToRebuild {
		rebuildIdx++
		progress.tick("tag-rebuild", float64(rebuildIdx)/float64(totalRebuilds+len(changedECs)))

		defs := defsByTag[tag]
		if len(defs) == 0 {
			// All defs for this tag were deleted — drop the tag's HC.
			if err := l.db.store.ReplaceHotCorrelations(tag, nil); err != nil {
				progress.fail(err)
				return nil, fmt.Errorf("replace HC for %q: %w", tag, err)
			}
			result.TagsRebuilt++
			continue
		}
		topK, err := l.computeTagTopK(defs)
		if err != nil {
			progress.fail(err)
			return nil, fmt.Errorf("compute top-K for %q: %w", tag, err)
		}
		if err := l.db.store.ReplaceHotCorrelations(tag, topK); err != nil {
			progress.fail(err)
			return nil, fmt.Errorf("replace HC for %q: %w", tag, err)
		}
		result.TagsRebuilt++
	}

	// Phase 4: chunk displace against unaffected tags.
	if len(changedECs) > 0 {
		// Pre-load unaffected tags' HC into memory; track which changed.
		type hcBucket struct {
			entries []HotCorrelation // sorted desc by score
			changed bool
		}
		buckets := make(map[string]*hcBucket)
		for tag := range defsByTag {
			if _, rebuilt := tagsToRebuild[tag]; rebuilt {
				continue
			}
			entries, err := l.db.store.ReadHotCorrelations(tag)
			if err != nil {
				progress.fail(err)
				return nil, fmt.Errorf("read HC for phase 4 %q: %w", tag, err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Score > entries[j].Score })
			buckets[tag] = &hcBucket{entries: entries}
		}

		for i, c := range changedECs {
			progress.tick("chunk-displace",
				float64(totalRebuilds+i+1)/float64(totalRebuilds+len(changedECs)))
			vec, err := l.db.store.ReadChunkEmbedding(c.chunkID)
			if err != nil {
				progress.fail(err)
				return nil, fmt.Errorf("read EC %d: %w", c.chunkID, err)
			}
			if vec == nil {
				continue
			}
			for tag, b := range buckets {
				defs := defsByTag[tag]
				score := maxCosineAgainstDefs(vec, defs)
				if math.IsInf(score, -1) {
					continue
				}
				// Drop existing entry for this chunkid if present (it was
				// scored against an older EC).
				for j := range b.entries {
					if b.entries[j].ChunkID == c.chunkID {
						b.entries = append(b.entries[:j], b.entries[j+1:]...)
						b.changed = true
						break
					}
				}
				if len(b.entries) < K_TOP_HC {
					b.entries = append(b.entries, HotCorrelation{ChunkID: c.chunkID, Score: score})
					sort.Slice(b.entries, func(i, j int) bool { return b.entries[i].Score > b.entries[j].Score })
					b.changed = true
				} else if score > b.entries[len(b.entries)-1].Score {
					b.entries[len(b.entries)-1] = HotCorrelation{ChunkID: c.chunkID, Score: score}
					sort.Slice(b.entries, func(i, j int) bool { return b.entries[i].Score > b.entries[j].Score })
					b.changed = true
				}
			}
		}

		for tag, b := range buckets {
			if !b.changed {
				continue
			}
			if err := l.db.store.ReplaceHotCorrelations(tag, b.entries); err != nil {
				progress.fail(err)
				return nil, fmt.Errorf("replace HC for %q in phase 4: %w", tag, err)
			}
			result.TagsTouched++
		}
	}

	// Phase 5: bookmark.
	if err := l.db.store.ISetCounter("hcsweep", maxSeen); err != nil {
		progress.fail(err)
		return nil, fmt.Errorf("write hcsweep bookmark: %w", err)
	}

	// Compute orphan total — count of all HC entries across all tags.
	orphanTotal := 0
	for tag := range defsByTag {
		entries, err := l.db.store.ReadHotCorrelations(tag)
		if err != nil {
			progress.fail(err)
			return nil, fmt.Errorf("count HC for %q: %w", tag, err)
		}
		orphanTotal += len(entries)
	}
	result.OrphanTotal = orphanTotal

	// Phase 6: complete.
	result.CompletedAt = time.Now()
	result.DurationMS = result.CompletedAt.Sub(progress.started).Milliseconds()
	progress.complete(result)
	return result, nil
}

// SweepHotCorrelationsAsync enqueues the same closure SweepHotCorrelations
// runs through, but returns immediately. The caller observes progress and
// terminal state via tmp://sweep/hot-correlations.md (existing pubsub path).
// Used by the curation workshop's sweep-button retrofit so the Lua VM is
// not held for the duration of the sweep.
// CRC: crc-Librarian.md | R2409
func (l *Librarian) SweepHotCorrelationsAsync() {
	if err := SyncVoid(l.db, func(db *DB) error {
		db.enqueueWrite(func(_ *microfts2.DB) {
			if _, err := l.SweepHotCorrelations(); err != nil {
				log.Printf("async hot-correlations sweep: %v", err)
			}
		})
		return nil
	}); err != nil {
		log.Printf("enqueue async hot-correlations sweep: %v", err)
	}
}

// HandleSweepCorrelations runs the hot-correlations sweep through the
// write goroutine and returns the result as JSON. POST /sweep/correlations.
//
// All sweep mutation (HC writes, bookmark, tmp:// progress) routes
// through enqueueWrite so it serializes with every other DB writer
// per the actor architecture. The HTTP handler enters the closure
// actor briefly to enqueue, then waits on a channel for the sweep
// goroutine's result.
// CRC: crc-Librarian.md | R2247
func (l *Librarian) HandleSweepCorrelations(w http.ResponseWriter, r *http.Request) {
	type outcome struct {
		result *HCSweepResult
		err    error
	}
	ch := make(chan outcome, 1)
	if err := SyncVoid(l.db, func(db *DB) error {
		db.enqueueWrite(func(_ *microfts2.DB) {
			res, err := l.SweepHotCorrelations()
			ch <- outcome{res, err}
		})
		return nil
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	o := <-ch
	if o.err != nil {
		http.Error(w, o.err.Error(), http.StatusInternalServerError)
		return
	}
	if o.result == nil {
		writeJSON(w, map[string]string{"status": "embedding-unavailable"})
		return
	}
	writeJSON(w, o.result)
}

// computeTagTopK walks every EC record and returns the top-K chunks for
// the given tag (max-cosine across defs). Used by Phase 3 of the sweep.
// CRC: crc-Librarian.md | R2234
func (l *Librarian) computeTagTopK(defs []TagDefEmbedding) ([]HotCorrelation, error) {
	if len(defs) == 0 {
		return nil, nil
	}
	dimBytes := len(defs[0].Vec) * 4
	queryNorms := make([]float64, len(defs))
	for i, d := range defs {
		queryNorms[i] = vecNorm(d.Vec)
	}

	h := &chunkAggHeap{}
	heap.Init(h)

	err := l.db.store.ViewChunkEmbeddings(func(_ *bbolt.Tx, chunkID uint64, raw []byte) (bool, error) {
		if len(raw) != dimBytes {
			return true, nil
		}
		var maxScore float64
		first := true
		for i, d := range defs {
			if queryNorms[i] == 0 {
				continue
			}
			s := cosineFromBytes(raw, d.Vec, queryNorms[i])
			if first || s > maxScore {
				maxScore = s
				first = false
			}
		}
		if first {
			return true, nil
		}
		if h.Len() < K_TOP_HC {
			heap.Push(h, chunkAgg{chunkID: chunkID, score: maxScore})
		} else if maxScore > (*h)[0].score {
			(*h)[0] = chunkAgg{chunkID: chunkID, score: maxScore}
			heap.Fix(h, 0)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]HotCorrelation, h.Len())
	for j := range out {
		c := heap.Pop(h).(chunkAgg)
		out[len(out)-1-j] = HotCorrelation{ChunkID: c.chunkID, Score: c.score}
	}
	return out, nil
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

	vec, err := l.modelCtx.embed(text)
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

	vecs, err := l.modelCtx.embedBatch(texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}
	return vecs, nil
}

// EmbedSimilarTagValues does two-step narrowing: cosine scan T record
// embeddings to find top-K tags, then cosine scan EV records only for
// those tags. Single query embedding for both steps (hybrid approach).
// Multiply tag × value scores. R1297, R1316
// CRC: crc-Librarian.md | R1298, R1315
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

	// Batch embed tag names (hyphens → spaces). R1285: hyphens→spaces
	// (`design-decision` → "design decision"). R1287: the embedding is stored
	// inline in the T record via WriteTagNameEmbedding(tag, vec) — keyed by tag
	// name, no separate ET prefix or tag-name-id. R1288: this hyphens→spaces
	// conversion applies to both T (here) and EV (the tag-value block below).
	// CRC: crc-Librarian.md | R1285, R1287, R1288
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

	// Batch embed tag values ("tag: value" with hyphens → spaces in tag).
	// R1286: tag-value compounds are embedded as "tagname: value" — colon
	// preserved, hyphens in the tag name converted to spaces (R1288).
	// CRC: crc-Librarian.md | R1286, R1288
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

// embedWithCtx embeds a batch using the given context.
// CRC: crc-Librarian.md | R1614
func (l *Librarian) embedWithCtx(ctx *embedContext, texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetModelTimer()
	vecs, err := ctx.embedBatch(texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}
	return vecs, nil
}

// createTierCtx creates a temporary context for one embedding tier.
// Caller must close() when done. R1594, R2962
func (l *Librarian) createTierCtx(tier EmbedTier) (*embedContext, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.model == nil {
		return nil, fmt.Errorf("model not loaded")
	}
	return l.model.newContext(embedParams{
		ctx:        tier.Ctx,
		parallel:   tier.Parallel,
		embeddings: true,
	})
}

// BatchEmbedChunks embeds all chunks missing EC records, using tier
// contexts for adaptive batching. Called post-reconcile after BatchEmbed.
// chunkid is the embedding key; chunkids arrive via the indexed
// callback delivered to the indexer at write time. The meaning axis is
// tag-free: each chunk's content is stripped of ark tags (stripArkTags)
// before embedding, and an all-@tag chunk (empty after strip) is skipped.
// CRC: crc-Librarian.md | R1609-R1617, R1913, R1914, R2913
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

	// Priority sort: tag-bearing files first, then non-JSONL, then JSONL.
	// CRC: crc-Librarian.md | R1611
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

	// --- Pass 1: classify chunks into tier buckets by chunkID (R1862, R1863) ---
	// CRC: crc-Librarian.md | R1846, R1847

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
			// R1604: a chunk with a C record (it's in finfo.Chunks) but no EC
			// record (existing == nil) is a "missing embedding" — queue it. This
			// inlines the former standalone MissingChunkEmbeddings() scan.
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
				// R3004: chunk fits no tier — nil sentinel EC so the queue
				// check (R1846) treats it as handled and it isn't re-queued.
				// CRC: crc-Librarian.md | R3004
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

	// --- Pass 2: embed one tier at a time ---
	// CRC: crc-Librarian.md | R1830

	var totalEmbedded int
	var totalEmptyStripped int // R3004: all-@tag chunks sentineled in Pass 2
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
		var cachedStrategy string
		chunkCache := l.db.fts.NewChunkCache()
		for i := 0; i < len(refs); i += tier.Parallel {
			end := min(i+tier.Parallel, len(refs))
			batch := refs[i:end]

			texts := make([]string, 0, len(batch))
			valid := make([]chunkRef, 0, len(batch))
			for _, ref := range batch {
				if ref.fileID != cachedFileID {
					cachedChunks = nil
					cachedStrategy = ""
					finfo, err := l.db.fts.FileInfoByID(ref.fileID)
					if err == nil && len(finfo.Chunks) > 0 {
						cachedChunks, _ = chunkCache.GetChunks(ref.path, finfo.Chunks[0].Location, 0, len(finfo.Chunks))
						cachedStrategy = finfo.Strategy
					}
					cachedFileID = ref.fileID
				}
				if ref.chunkIdx < len(cachedChunks) {
					// R2913: the meaning axis is tag-free — strip ark tags
					// before embedding (every text strategy, pdf included: a
					// pdf chunk's content is extracted text). An all-@tag chunk
					// strips to empty and is skipped (the tag axis carries it);
					// retrieval and the trigram index keep the original content.
					stripped := string(stripArkTags([]byte(cachedChunks[ref.chunkIdx].Content), cachedStrategy))
					// CRC: crc-Librarian.md | R2992
					// NUL bytes abort the embedding tokenizer; tokenizeText
					// replaces them with spaces before tokenizing. Name the
					// offending chunk here (we have the path/id) so a malformed
					// source file can be found — NULs seen so far originate
					// upstream in PDF text extraction (pdftext).
					if strings.IndexByte(stripped, 0) >= 0 {
						log.Printf("chunk embed: NUL byte(s) in %s (chunkID=%d) — replaced with spaces before embedding", ref.path, ref.chunkID)
					}
					if stripped != "" {
						texts = append(texts, stripped)
						valid = append(valid, ref)
					} else {
						// R3004: an all-@tag chunk strips to empty and has no
						// meaning vector. Record a nil sentinel EC so the queue
						// check (R1846) treats it as handled and it isn't
						// re-queued every reconcile (the tag axis carries it).
						// CRC: crc-Librarian.md | R3004
						if err := l.db.store.WriteChunkEmbedding(ref.chunkID, nil); err != nil {
							log.Printf("chunk embed: tag-only sentinel write chunkID=%d: %v", ref.chunkID, err)
						}
						totalEmptyStripped++
					}
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

		ctx.close()
	}

	// R1608, R1618, R1848: recompute EF centroids from scratch for files that
	// got new embeddings — read every chunk vec and accumulate the running sum
	// (R1618: sum += vec, count++; centroid is sum/count). A full re-index of a
	// file lands here.
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

	if totalEmbedded > 0 || totalSkipped > 0 || totalDeduped > 0 || totalEmptyStripped > 0 { // R1864, R3004
		log.Printf("librarian: chunk embed: %d embedded, %d skipped, %d deduped, %d tag-only", totalEmbedded, totalSkipped, totalDeduped, totalEmptyStripped)
	}
	return nil
}

// flushBucket embeds all chunks in a bucket and writes EC records. R1614, R1615
// Tokenizer wraps a llama model for tokenization only. yzma tokenizes
// from the model vocab directly, so no inference context is needed.
// CRC: crc-Librarian.md | R1529, R1530
type Tokenizer struct {
	model     *embedModel
	modelPath string
}

// NewTokenizer loads a GGUF model for tokenization only.
// Caller must call Close() when done.
// CRC: crc-Librarian.md | R1529, R1530
func NewTokenizer(libDir, modelPath string) (*Tokenizer, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("no embedding model configured")
	}
	model, err := loadEmbedModel(libDir, modelPath)
	if err != nil {
		return nil, err
	}
	return &Tokenizer{model: model, modelPath: modelPath}, nil
}

// CountTokens returns the number of tokens in text.
func (t *Tokenizer) CountTokens(text string) int {
	return t.model.countTokens(text)
}

// Close releases the tokenizer's model.
func (t *Tokenizer) Close() {
	if t.model != nil {
		t.model.close()
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

// CRC: crc-Librarian.md | R64 — loads the embedding model once and keeps it
// warm on the Librarian (l.model); reused across queries until unloadModel's
// TTL (R1596) drops it.
func (l *Librarian) ensureModel() error {
	if l.model != nil {
		return nil
	}
	// R2970: a configured model with no provisioned libs fails with a
	// clear error naming the provisioning command — never a silent drop.
	if err := requireLlamaLibs(l.libDir); err != nil {
		return err
	}
	model, err := loadEmbedModel(l.libDir, l.modelPath)
	if err != nil {
		return err
	}
	// Default context for tags/queries (or bench override) R1595, R2962
	// CRC: crc-Librarian.md | R1597
	ctxSize := l.ctxSize
	if ctxSize <= 0 {
		ctxSize = 2048
	}
	par := l.parallel
	if par <= 0 {
		par = 8
	}
	ctx, err := model.newContext(embedParams{ctx: ctxSize, parallel: par, embeddings: true})
	if err != nil {
		model.close()
		return fmt.Errorf("create context: %w", err)
	}
	l.model = model
	l.modelCtx = ctx

	log.Printf("librarian: loaded embedding model %s (%d embed tiers configured)",
		filepath.Base(l.modelPath), len(l.tiers))
	return nil
}

// R1279: when the modelTTL elapses this timer fires unloadModel (l.model →
// nil); the next query's ensureModel sees nil and reloads the model.
// CRC: crc-Librarian.md | R1279
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

// unloadModel releases the embedding model and all contexts; the model
// TTL timer (resetModelTimer) fires this when the embed queue goes idle.
// CRC: crc-Librarian.md | R1596
func (l *Librarian) unloadModel() {
	if l.modelCtx != nil {
		l.modelCtx.close()
		l.modelCtx = nil
	}
	if l.model != nil {
		l.model.close()
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
