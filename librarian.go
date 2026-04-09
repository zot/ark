package ark

// CRC: crc-Librarian.md | Seq: seq-spectral-expand.md
// R1235-R1254, R1268-R1273

import (
	"crypto/rand"
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
	llama "github.com/godeps/gollama"
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

	// Embedding model (R1277, R1278)
	model      *llama.Model
	modelCtx   *llama.Context
	modelPath  string // full path to GGUF file
	modelTimer *time.Timer
	modelTTL   time.Duration
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
	l := &Librarian{
		available: true,
		db:        db,
		results:   make(map[string]*ExpandResult),
		modelTTL:  5 * time.Minute,
	}
	// R1274: resolve tag_model path
	if tagModel := db.Config().TagModel; tagModel != "" {
		modelPath := filepath.Join(dbPath, tagModel)
		if _, err := os.Stat(modelPath); err == nil {
			l.modelPath = modelPath
		}
	}
	return l
}

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

// HandleExpand queues an expansion request and returns the request ID.
// POST /search/expand
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
// GET /search/expand/wait
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
// POST /search/expand/result
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
// GET /search/expand/result/{id}
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
	tagValues, err := l.db.store.ScanVRecordTvids()
	if err != nil {
		return nil, fmt.Errorf("scan V record tvids: %w", err)
	}

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

// BatchEmbed scans for missing tag name and tag value embeddings,
// embeds them in batches, and writes the results to LMDB.
// Called post-reconcile from the write goroutine. R1292, R1293, R1295
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
	if len(missingTags) == 0 && len(missingTvids) == 0 {
		return nil
	}
	log.Printf("librarian: embedding %d tag names + %d tag values", len(missingTags), len(missingTvids))

	// Resolve tvids to text for embedding
	var tvidMap map[uint64]TagAlt
	if len(missingTvids) > 0 {
		tvidMap, err = l.db.store.ScanVRecordTvids()
		if err != nil {
			return fmt.Errorf("scan V record tvids: %w", err)
		}
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

	log.Printf("librarian: embedding complete")
	return nil
}

func (l *Librarian) ensureModel() error {
	if l.model != nil {
		return nil
	}
	model, err := llama.LoadModel(l.modelPath)
	if err != nil {
		return fmt.Errorf("load model %s: %w", l.modelPath, err)
	}
	ctx, err := model.NewContext(
		llama.WithEmbeddings(),
		llama.WithContext(2048),
	)
	if err != nil {
		model.Close()
		return fmt.Errorf("create context: %w", err)
	}
	l.model = model
	l.modelCtx = ctx
	log.Printf("librarian: loaded embedding model %s", filepath.Base(l.modelPath))
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
