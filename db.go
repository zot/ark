package ark

// CRC: crc-DB.md

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zot/microfts2"

	"github.com/BurntSushi/toml"
	"github.com/bmatsuo/lmdb-go/lmdb"
)

// Version is set by ldflags at build time from README.md.
// Fallback value for plain `go build`.
var Version = "dev"

// CRC: crc-DB.md | R1924, R1925 — microvec is no longer a dependency.
// Any pre-existing microvec records inside the LMDB env are orphaned
// blobs reclaimed on the next `ark init` / rebuild.
var _ = Version // anchor for the migration's removal Rn refs

// DB is the main ark facade. It coordinates microfts2, the
// Librarian/EC embedding pipeline, and the ark subdatabase. All
// operations are serialized through a closure actor (svc channel).
// CRC: crc-DB.md | R986, R1909, R1910, R1923
type DB struct {
	fts     *microfts2.DB
	store   *Store
	config  *Config
	matcher *Matcher

	indexer    *Indexer
	scanner    *Scanner
	search     *Searcher
	pdfChunker *PDFChunker // CRC: crc-PDFChunker.md | R1720, R1726

	dbPath   string
	tmpPaths map[string]uint64 // R664: tmp:// path → fileid tracking
	svc      chan func()       // R986: closure actor channel

	// Write actor: read/write path separation. R1051-R1068
	writeQueue      []func(*microfts2.DB) // queued write closures
	writing         bool                  // true while a write goroutine is in flight
	onWriteComplete func([]scheduleItem)  // callback for schedule items from write goroutines
}

// InitOpts are options for creating a new ark database.
type InitOpts struct {
	EmbedCmd        string
	QueryCmd        string
	CaseInsensitive bool
	Aliases         map[byte]byte
	TagsSeed        []byte // seed content for tags.md (falls back to built-in default)
	ConfigSeed      []byte // seed content for ark.toml (falls back to built-in default)
}

// Init creates a new ark database at the given path.
func Init(dbPath string, opts InitOpts) error {
	injectPath(dbPath)

	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	// Seed from existing ark.toml if present — CLI flags override seeded values
	configPath := filepath.Join(dbPath, "ark.toml")
	if data, err := os.ReadFile(configPath); err == nil {
		var seed Config
		if parseErr := toml.Unmarshal(data, &seed); parseErr == nil {
			if !opts.CaseInsensitive && seed.CaseInsensitive {
				opts.CaseInsensitive = seed.CaseInsensitive
			}
			if opts.EmbedCmd == "" && seed.EmbedCmd != "" {
				opts.EmbedCmd = seed.EmbedCmd
			}
			if opts.QueryCmd == "" && seed.QueryCmd != "" {
				opts.QueryCmd = seed.QueryCmd
			}
		}
	}

	// Ensure newline alias for line-start matching
	aliases := opts.Aliases
	if aliases == nil {
		aliases = make(map[byte]byte)
	}
	if _, ok := aliases['\n']; !ok {
		aliases['\n'] = '\x01'
	}

	// Initialize microfts2 (creates the LMDB environment).
	// CRC: crc-DB.md | R1911, R1912 — microfts2 owns its own subDBs; ark's
	// store shares the same env. No microvec subDB is allocated.
	ftsOpts := microfts2.Options{
		CaseInsensitive: opts.CaseInsensitive,
		Aliases:         aliases,
		MaxDBs:          8,
		MapSize:         8 << 30, // 8GB — conversation logs can be large
	}
	fts, err := microfts2.Create(dbPath, ftsOpts)
	if err != nil {
		return fmt.Errorf("init microfts2: %w", err)
	}
	defer fts.Close()

	// Initialize ark subdatabase
	store, err := OpenStore(fts.Env())
	if err != nil {
		return fmt.Errorf("init ark store: %w", err)
	}

	// CRC: crc-DB.md | R382
	// Register default chunking strategies. Markdown registers as the
	// AppendAwareChunker struct so AppendChunks merges paragraph
	// extensions cleanly instead of falling through to full reindex.
	// Other built-ins remain ChunkFunc for now (no AppendAware impl yet).
	if err := fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		return fmt.Errorf("register strategy markdown: %w", err)
	}
	funcStrategies := map[string]microfts2.ChunkFunc{
		"lines":      microfts2.LineChunkFunc,
		"chat-jsonl": JSONLChunkFunc,
	}
	for name, fn := range funcStrategies {
		if err := fts.AddStrategyFunc(name, fn); err != nil {
			return fmt.Errorf("register strategy %s: %w", name, err)
		}
	}
	externalStrategies := map[string]string{
		"lines-overlap": "microfts chunk-lines-overlap -lines 50",
		"words-overlap": "microfts chunk-words-overlap",
	}
	for name, cmd := range externalStrategies {
		if err := fts.AddStrategy(name, cmd); err != nil {
			return fmt.Errorf("register strategy %s: %w", name, err)
		}
	}

	// Write default config only if none exists (configPath declared above for seeding)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := WriteDefaultConfig(configPath, opts.ConfigSeed); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	}

	// Create starter tags.md from bundle seed if provided
	tagsPath := filepath.Join(dbPath, "tags.md")
	if len(opts.TagsSeed) > 0 {
		if _, err := os.Stat(tagsPath); os.IsNotExist(err) {
			if err := os.WriteFile(tagsPath, opts.TagsSeed, 0644); err != nil {
				return fmt.Errorf("write tags.md: %w", err)
			}
		}
	}

	// CRC: crc-DB.md | R1539
	// Write full config to I records
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config for I records: %w", err)
	}
	if err := store.WriteConfig(cfg); err != nil {
		return fmt.Errorf("write config I records: %w", err)
	}

	// CRC: crc-DB.md | R1879, R1882
	// Schema markers for fresh DB.
	if err := store.IPut("ec_version", "2"); err != nil {
		return fmt.Errorf("write ec_version: %w", err)
	}
	if err := store.IPut("tag_store_version", "1"); err != nil {
		return fmt.Errorf("write tag_store_version: %w", err)
	}

	return nil
}

// Open opens an existing ark database.
// injectPath inserts dbPath into PATH just before /usr/bin (if present),
// so user entries win but db companions beat system defaults.
func injectPath(dbPath string) {
	path := os.Getenv("PATH")
	parts := strings.Split(path, ":")
	var result []string
	inserted := false
	for _, p := range parts {
		if !inserted && (p == "/usr/bin" || p == "/usr/local/bin") {
			result = append(result, dbPath)
			inserted = true
		}
		result = append(result, p)
	}
	if !inserted {
		result = append(result, dbPath)
	}
	os.Setenv("PATH", strings.Join(result, ":"))
}

func Open(dbPath string) (*DB, error) {
	injectPath(dbPath)

	// Load config
	configPath := filepath.Join(dbPath, "ark.toml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	config.dbPath = dbPath

	// Open microfts2 (opens the LMDB environment)
	fts, err := microfts2.Open(dbPath, microfts2.Options{MaxDBs: 8, MapSize: 2 << 30})
	if err != nil {
		return nil, fmt.Errorf("open microfts2: %w", err)
	}

	// Open ark subdatabase
	store, err := OpenStore(fts.Env())
	if err != nil {
		fts.Close()
		return nil, fmt.Errorf("open ark store: %w", err)
	}
	tmpTags := NewTmpTagStore(store.TvidMap())
	store.SetTmpTagStore(tmpTags)

	matcher := &Matcher{Dotfiles: config.Dotfiles}

	// CRC: crc-DB.md | R382
	// Register built-in func strategies (must happen on every Open,
	// not just InitDB — func strategies aren't persisted in LMDB).
	// The chat-jsonl chunker is wrapped in an LRU cache — conversation logs
	// are append-only, so chunks stay valid until the file changes.
	// The cache is captured by the closure; microfts2 never sees it.
	jsonlCache := newChunkCache(64)
	if err := fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		fts.Close()
		return nil, fmt.Errorf("register markdown strategy: %w", err)
	}
	for name, fn := range map[string]microfts2.ChunkFunc{
		"lines":      microfts2.LineChunkFunc,
		"chat-jsonl": jsonlCache.wrap(JSONLChunkFunc),
	} {
		if err := fts.AddStrategyFunc(name, fn); err != nil {
			fts.Close()
			return nil, fmt.Errorf("register %s strategy: %w", name, err)
		}
	}

	// CRC: crc-DB.md | R624, R626, R627, R628, R629, R630, R636, R637
	// Register chunker strategies from ark.toml [[chunker]] entries
	registerChunkers(fts, config)

	// R1644, R1651: in-memory set of zero-byte files shared by Scanner
	// and the Indexer copies made via withFTS.
	emptyFiles := NewEmptyFiles()

	db := &DB{
		fts:     fts,
		store:   store,
		config:  config,
		matcher: matcher,
		scanner: &Scanner{config: config, matcher: matcher, fts: fts, emptyFiles: emptyFiles},
		search:  &Searcher{fts: fts, store: store, config: config},
		dbPath:  dbPath,
		svc:     make(chan func(), 8),
	}
	// CRC: crc-PDFChunker.md | R1641, R1720, R1726
	// PDFChunker holds a reference to ark.DB so it can stage per-page
	// blobs during FileChunks and resolve path→fileid in GetChunk.
	db.pdfChunker = NewPDFChunker(db)
	if err := fts.AddChunker("pdf", db.pdfChunker); err != nil {
		log.Printf("warning: register pdf chunker: %v", err)
	}
	db.indexer = &Indexer{fts: fts, store: store, config: config, pdfChunker: db.pdfChunker}

	// R1859, R1860: migrate EC records from (fileID, chunkIdx) to chunkID key format
	if v, _ := store.IGet("ec_version"); v != "2" {
		log.Println("migrate: dropping old EC/EF records (ec_version upgrade to 2)")
		store.DropChunkEmbeddings()
		store.IPut("ec_version", "2")
	}

	// R1879, R1880: refuse to start on a stale tag store schema. Init writes "1"
	// for fresh DBs (R1882). An old DB lacking the marker requires `ark rebuild`.
	if tv, _ := store.IGet("tag_store_version"); tv != "1" {
		return nil, fmt.Errorf("tag store schema upgrade required — run `ark rebuild` (tag_store_version=%q, want %q)", tv, "1")
	}

	// CRC: crc-DB.md | R1958, R1968, R1969
	if err := store.LoadTvidMap(); err != nil {
		return nil, fmt.Errorf("load tvid map: %w", err)
	}

	// R1887, R1888, R1889: wire bidirectional chunkID↔fileID resolvers.
	// Both run inside the caller's txn to avoid nested Views.
	// Overlay-issued ids (high bit set) route to TmpTagStore so the
	// chunkid↔fileid mapping for tmp:// content stays first-class.
	// CRC: crc-DB.md | R1948, R1950
	store.SetChunkResolver(
		func(txn *lmdb.Txn, chunkID uint64) []uint64 {
			if IsOverlayID(chunkID) {
				return tmpTags.FilesForChunk(chunkID)
			}
			crec, err := fts.ReadCRecord(txn, chunkID)
			if err != nil {
				return nil
			}
			ids := make([]uint64, 0, len(crec.FileIDs))
			for _, f := range crec.FileIDs {
				ids = append(ids, f.FileID)
			}
			return ids
		},
		func(fileID uint64) []uint64 {
			if IsOverlayID(fileID) {
				return tmpTags.ChunksForFile(fileID)
			}
			info, err := fts.FileInfoByID(fileID)
			if err != nil {
				return nil
			}
			ids := make([]uint64, 0, len(info.Chunks))
			for _, c := range info.Chunks {
				ids = append(ids, c.ChunkID)
			}
			return ids
		},
	)

	runSvc(db.svc)
	return db, nil
}

// Do sends a fire-and-forget operation to the DB actor.
// Used by the watcher for file changes and reconcile. R987
func (db *DB) Do(fn func(*DB)) {
	svc(db.svc, func() { fn(db) })
}

// Sync sends an operation to the DB actor and blocks until it completes.
// Used by HTTP handlers and CLI for operations that return results. R988, R989
func Sync[T any](db *DB, fn func(*DB) (T, error)) (T, error) {
	return svcSync(db.svc, func() (T, error) {
		return fn(db)
	})
}

// SyncVoid sends a void operation to the DB actor and blocks until it completes.
func SyncVoid(db *DB, fn func(*DB) error) error {
	return svcSyncVoid(db.svc, func() error {
		return fn(db)
	})
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R1053
// enqueueWrite appends a write closure to the write queue. If no write
// is in flight and the queue was empty, starts the write goroutine.
// Must be called from inside the actor.
func (db *DB) enqueueWrite(fn func(ftsCopy *microfts2.DB)) {
	db.writeQueue = append(db.writeQueue, fn)
	if !db.writing && len(db.writeQueue) == 1 {
		db.startNextWrite()
	}
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R1054, R1055, R1056, R1059
// startNextWrite dequeues the head of the write queue and runs it in
// a goroutine using a cache-less copy of the FTS database.
func (db *DB) startNextWrite() {
	if len(db.writeQueue) == 0 {
		return
	}
	fn := db.writeQueue[0]
	db.writeQueue = db.writeQueue[1:]
	db.writing = true

	ftsCopy := db.fts.Copy()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("write actor: panic: %v\n%s", r, debug.Stack())
				// R1059: send error closure back to actor
				svc(db.svc, func() {
					db.writing = false
					if len(db.writeQueue) > 0 {
						db.startNextWrite()
					}
				})
			}
		}()

		// Execute the write closure off the actor (file I/O here). R1055
		fn(ftsCopy)

		// R1056: send reconcile closure back to actor
		svc(db.svc, func() {
			db.fts.InvalidateCaches() // R1057, R1064
			db.writing = false
			if len(db.writeQueue) > 0 {
				db.startNextWrite() // R1058: continuation
			}
		})
	}()
}

// drainWriteSchedule drains accumulated schedule items from a write
// goroutine's indexer copy and sends them to the callback if set.
// Called from inside write goroutines (off the actor).
func (db *DB) drainWriteSchedule(idx *Indexer) {
	if db.onWriteComplete == nil {
		return
	}
	if items := idx.DrainSchedule(); len(items) > 0 {
		db.onWriteComplete(items)
	}
}

// Close stops the actor and closes the database in reverse order.
func (db *DB) Close() error {
	if db.svc != nil {
		close(db.svc)
		db.svc = nil
	}
	// store doesn't need explicit close (shares env with fts)
	return db.fts.Close()
}

// registerChunkers reads [[chunker]] entries from config and registers
// bracket/indent chunkers via microfts2.AddChunker.
// CRC: crc-DB.md | R624, R625, R626, R627, R628, R629, R630, R636, R637
func registerChunkers(fts *microfts2.DB, cfg *Config) {
	for _, cc := range cfg.Chunkers {
		if cc.Name == "" {
			log.Printf("warning: skipping chunker with empty name")
			continue
		}
		lang := buildBracketLang(cc)
		isIndent := cc.Type == "indent" || cc.Type == "indent-full"
		isBracket := cc.Type == "bracket" || cc.Type == "bracket-full"
		switch {
		case isBracket:
			if err := fts.AddChunker(cc.Name, microfts2.BracketChunker(lang)); err != nil {
				log.Printf("warning: register chunker %s: %v", cc.Name, err)
			}
		case isIndent:
			tabWidth := cc.TabWidth
			if tabWidth <= 0 {
				tabWidth = 4
			}
			if err := fts.AddChunker(cc.Name, microfts2.IndentChunker(lang, tabWidth)); err != nil {
				log.Printf("warning: register chunker %s: %v", cc.Name, err)
			}
		default:
			log.Printf("warning: unknown chunker type %q for %s", cc.Type, cc.Name)
		}
	}
}

// buildBracketLang converts a ChunkerConfig to a microfts2.BracketLang.
// Handles both easy form (flat pairs) and full form (struct defs).
func buildBracketLang(cc ChunkerConfig) microfts2.BracketLang {
	lang := microfts2.BracketLang{
		LineComments: cc.LineComments,
	}
	for _, pair := range cc.BlockComments {
		if len(pair) == 2 {
			lang.BlockComments = append(lang.BlockComments, [2]string{pair[0], pair[1]})
		}
	}
	isFull := cc.Type == "bracket-full" || cc.Type == "indent-full"
	if isFull {
		// Full form: string_defs and bracket_defs
		for _, sd := range cc.StringDefs {
			lang.StringDelims = append(lang.StringDelims, microfts2.StringDelim{
				Open: sd.Open, Close: sd.Close, Escape: sd.Escape,
			})
		}
		for _, bd := range cc.BracketDefs {
			lang.Brackets = append(lang.Brackets, microfts2.BracketGroup{
				Open: bd.Open, Separators: bd.Separators, Close: bd.Close,
			})
		}
	} else {
		// Easy form: flat pairs with default escape "\"
		for _, pair := range cc.Strings {
			if len(pair) == 2 {
				lang.StringDelims = append(lang.StringDelims, microfts2.StringDelim{
					Open: pair[0], Close: pair[1], Escape: `\`,
				})
			}
		}
		for _, pair := range cc.Brackets {
			if len(pair) == 2 {
				lang.Brackets = append(lang.Brackets, microfts2.BracketGroup{
					Open: []string{pair[0]}, Close: []string{pair[1]},
				})
			}
		}
	}
	return lang
}

// Path returns the database directory path.
func (db *DB) Path() string { return db.dbPath }

// Config returns the current configuration.
func (db *DB) Config() *Config { return db.config }
func (db *DB) Store() *Store   { return db.store }

// ConfigPath returns the path to ark.toml.
func (db *DB) ConfigPath() string { return filepath.Join(db.dbPath, "ark.toml") }

// SaveConfig writes the current config to disk and re-validates.
func (db *DB) SaveConfig() error { return db.config.SaveConfig(db.ConfigPath()) }

// ReloadConfig re-reads ark.toml from disk, diffs against stored I records,
// applies changes, and propagates to components.
// CRC: crc-DB.md | R1561, R1562, R1563, R1564
func (db *DB) ReloadConfig() error {
	cfg, err := LoadConfig(db.ConfigPath())
	if err != nil {
		return err
	}
	cfg.dbPath = db.dbPath
	db.config = cfg
	db.scanner.config = cfg
	db.search.config = cfg
	db.indexer.config = cfg
	db.matcher.Dotfiles = cfg.Dotfiles

	// Diff and apply config changes
	changes, err := db.DiffConfig()
	if err != nil {
		log.Printf("config diff on reload: %v", err)
		return nil
	}
	if len(changes) > 0 {
		deferred := db.ApplyConfigChanges(changes)
		for _, c := range deferred {
			log.Printf("ERROR: config change deferred — %s: %q → %q (restart required)",
				c.Field, c.OldValue, c.NewValue)
			db.store.WriteERecord(ECondIndexStale, map[string]string{
				"field":   c.Field,
				"stored":  c.OldValue,
				"current": c.NewValue,
			})
		}
	}
	return nil
}

// IsIndexable returns true if path would be indexed by any configured source.
// CRC: crc-DB.md | Seq: seq-file-change.md
func (db *DB) IsIndexable(path string) bool {
	for _, src := range db.config.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		relPath, err := filepath.Rel(src.Dir, path)
		if err != nil || strings.HasPrefix(relPath, "..") {
			continue // path not under this source
		}
		includes, excludes := db.config.EffectivePatterns(src)
		if db.matcher.Classify(includes, excludes, relPath, false) == Included {
			return true
		}
	}
	return false
}

// FTS returns the microfts2 database (for creating ChunkCaches).
func (db *DB) FTS() *microfts2.DB {
	return db.fts
}

// AddTmpFile indexes content in memory via the microfts2 overlay
// and writes the extracted tag values into the in-memory tag overlay
// via Store.UpdateTagValues.
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | R663, R666, R667, R1948
func (db *DB) AddTmpFile(path, strategy string, content []byte) (uint64, error) {
	acc := &chunkAccumulator{strategy: strategy}
	fid, err := db.fts.AddTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, err
	}
	if db.tmpPaths == nil {
		db.tmpPaths = make(map[string]uint64)
	}
	db.tmpPaths[path] = fid
	stampFileID(acc.chunkTags, fid)
	if err := db.store.UpdateTagValues(acc.chunkTags); err != nil {
		return fid, fmt.Errorf("write tmp tags: %w", err)
	}
	return fid, nil
}

// UpdateTmpFile replaces content of an existing tmp:// document and
// re-extracts its tag values into the in-memory tag overlay.
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | R666, R1948
func (db *DB) UpdateTmpFile(path, strategy string, content []byte) error {
	acc := &chunkAccumulator{strategy: strategy}
	if err := db.fts.UpdateTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback)); err != nil {
		return err
	}
	fid, ok := db.tmpPaths[path]
	if !ok {
		return nil
	}
	// UpdateTmpFile drops the file's prior overlay chunks; clear our
	// tag overlay first so UpdateTagValues writes a clean replacement.
	db.store.RemoveFileTagValues(fid)
	stampFileID(acc.chunkTags, fid)
	if err := db.store.UpdateTagValues(acc.chunkTags); err != nil {
		return fmt.Errorf("write tmp tags: %w", err)
	}
	return nil
}

// AppendTmpFile appends content to a tmp:// document, creating it if needed.
// Newly emitted chunks have their tag values added to the overlay via
// Store.AppendTagValues; existing chunks are untouched.
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | R1948
func (db *DB) AppendTmpFile(path, strategy string, content []byte) (uint64, error) {
	acc := &chunkAccumulator{strategy: strategy}
	fid, err := db.fts.AppendTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, err
	}
	if db.tmpPaths == nil {
		db.tmpPaths = make(map[string]uint64)
	}
	db.tmpPaths[path] = fid
	stampFileID(acc.chunkTags, fid)
	if err := db.store.AppendTagValues(acc.chunkTags); err != nil {
		return fid, fmt.Errorf("write tmp tags: %w", err)
	}
	return fid, nil
}

// RemoveTmpFile drops a tmp:// document from the overlay. Tag entries
// are cleared first so the trigram and tag overlays never disagree
// on which fileids exist.
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | R666, R1944
func (db *DB) RemoveTmpFile(path string) error {
	if fid, ok := db.tmpPaths[path]; ok {
		db.store.RemoveFileTagValues(fid)
	}
	if err := db.fts.RemoveTmpFile(path); err != nil {
		return err
	}
	delete(db.tmpPaths, path)
	return nil
}

// stampFileID populates ChunkTagValues.FileID for every entry so the
// Store dispatcher can route overlay groups to TmpTagStore by fileid.
// CRC: crc-DB.md | R1947
func stampFileID(chunkTags []ChunkTagValues, fileID uint64) {
	for i := range chunkTags {
		chunkTags[i].FileID = fileID
	}
}

// HasTmp returns true if any tmp:// documents exist.
// CRC: crc-DB.md | R682
func (db *DB) HasTmp() bool {
	return db.fts.HasTmp()
}

// TmpFiles returns all tmp:// paths.
// CRC: crc-DB.md | R664
func (db *DB) TmpFiles() []string {
	paths := make([]string, 0, len(db.tmpPaths))
	for p := range db.tmpPaths {
		paths = append(paths, p)
	}
	return paths
}

// FillChunks populates Text for each result with chunk content from disk.
func (db *DB) FillChunks(results []SearchResultEntry) ([]SearchResultEntry, error) {
	return db.search.FillChunks(results)
}

// FillChunksUsing populates Text using an external cache (session path).
func (db *DB) FillChunksUsing(results []SearchResultEntry, cache *microfts2.ChunkCache) ([]SearchResultEntry, error) {
	return db.search.FillChunksUsing(results, cache)
}

// FillFiles deduplicates results by file and populates Text with full content.
func (db *DB) FillFiles(results []SearchResultEntry) ([]SearchResultEntry, error) {
	return db.search.FillFiles(results)
}

// Add indexes files. If path is a directory, walks per config.
// If a file, adds directly with the given strategy.
func (db *DB) Add(paths []string, strategy string) error {
	if db.config.HasErrors() {
		return fmt.Errorf("config errors: %v", db.config.Errors)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("add: skipping %s: %v", path, err)
				continue
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if info.IsDir() {
			if err := db.addDirectory(path); err != nil {
				return err
			}
		} else {
			if _, err := db.indexer.AddFile(path, strategy); err != nil {
				return err
			}
		}
	}
	return nil
}

func (db *DB) addDirectory(dir string) error {
	results, err := db.scanner.Scan()
	if err != nil {
		return err
	}
	evictEmpty(db.fts, results.EmptyFiles) // R1648

	absDir, _ := filepath.Abs(dir)
	for _, f := range results.NewFiles {
		absPath, _ := filepath.Abs(f.Path)
		if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) && absPath != absDir {
			continue
		}
		if _, err := db.indexer.AddFile(f.Path, f.Strategy); err != nil {
			if errors.Is(err, microfts2.ErrNoChunks) {
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("add: skipping %s: %v", f.Path, err)
				continue
			}
			return fmt.Errorf("add %s: %w", f.Path, err)
		}
	}
	for _, u := range results.NewUnresolved {
		absPath, _ := filepath.Abs(u.Path)
		if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) && absPath != absDir {
			continue
		}
		if err := db.store.AddUnresolved(u.Path, u.Dir); err != nil {
			return fmt.Errorf("track unresolved %s: %w", u.Path, err)
		}
	}
	if err := db.store.CleanUnresolved(); err != nil {
		return fmt.Errorf("clean unresolved: %w", err)
	}
	return nil
}

// evictEmpty drops any previously-indexed path entries for empty
// files reported by Scanner. microfts2 owns chunk refcounting, so
// we only remove the path — chunks may still be referenced by other
// paths sharing the same content hash. The fts handle is a parameter
// so synchronous callers pass db.fts while async callers pass the
// write-goroutine's ftsCopy. R1648
func evictEmpty(fts *microfts2.DB, paths []string) {
	for _, p := range paths {
		status, err := fts.CheckFile(p)
		if err != nil {
			continue
		}
		if status.Status != "fresh" && status.Status != "stale" && status.Status != "missing" {
			continue
		}
		if err := fts.RemoveFile(p); err != nil {
			log.Printf("evict empty %s: %v", p, err)
		}
	}
}

// Remove removes files from both engines. Accepts paths or glob patterns.
func (db *DB) Remove(patterns []string) error {
	// Get all indexed files to match patterns against
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	for _, s := range statuses {
		for _, pat := range patterns {
			if db.matcher.Match(pat, s.Path, false) {
				if err := db.indexer.RemoveFile(s.Path); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// Scan walks configured directories, indexes new files, flags unresolved.
func (db *DB) Scan() (*ScanResults, error) {
	if db.config.HasErrors() {
		return nil, fmt.Errorf("config errors: %v", db.config.Errors)
	}

	results, err := db.scanner.Scan()
	if err != nil {
		return nil, err
	}
	evictEmpty(db.fts, results.EmptyFiles) // R1648

	for _, f := range results.NewFiles {
		if _, err := db.indexer.AddFile(f.Path, f.Strategy); err != nil {
			if errors.Is(err, microfts2.ErrNoChunks) || errors.Is(err, microfts2.ErrAlreadyIndexed) {
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("scan: skipping %s: %v", f.Path, err)
				continue
			}
			return results, fmt.Errorf("add %s: %w", f.Path, err)
		}
	}
	for _, u := range results.NewUnresolved {
		if err := db.store.AddUnresolved(u.Path, u.Dir); err != nil {
			return results, fmt.Errorf("track unresolved %s: %w", u.Path, err)
		}
	}
	if err := db.store.CleanUnresolved(); err != nil {
		return results, err
	}
	return results, nil
}

// Refresh re-indexes stale files, optionally scoped by patterns.
func (db *DB) Refresh(patterns []string) error {
	missing, err := db.indexer.RefreshStale(patterns, db.matcher)
	if err != nil {
		return err
	}
	for _, m := range missing {
		info, err := db.fts.FileInfoByID(m.FileID)
		if err != nil {
			continue
		}
		if err := db.store.AddMissing(m.FileID, info.Names[0], timeNow()); err != nil {
			return err
		}
	}
	return nil
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R1051, R1052, R1062
// ScanAsync walks directories and queues new file indexing through the
// write actor. Config files (ark.toml) are indexed synchronously in
// the actor; content files are batched into the write queue.
// Returns scan results (new files found) immediately; indexing
// continues in the background.
func (db *DB) ScanAsync() (*ScanResults, error) {
	if db.config.HasErrors() {
		return nil, fmt.Errorf("config errors: %v", db.config.Errors)
	}

	results, err := db.scanner.Scan()
	if err != nil {
		return nil, err
	}

	// Handle unresolved synchronously (small metadata writes)
	for _, u := range results.NewUnresolved {
		if err := db.store.AddUnresolved(u.Path, u.Dir); err != nil {
			return results, fmt.Errorf("track unresolved %s: %w", u.Path, err)
		}
	}
	if err := db.store.CleanUnresolved(); err != nil {
		return results, err
	}

	// R1052: config files indexed synchronously in the actor
	var contentFiles []FileEntry
	for _, f := range results.NewFiles {
		if filepath.Base(f.Path) == "ark.toml" {
			if _, err := db.indexer.AddFile(f.Path, f.Strategy); err != nil {
				if !errors.Is(err, microfts2.ErrNoChunks) && !errors.Is(err, microfts2.ErrAlreadyIndexed) && !errors.Is(err, os.ErrNotExist) {
					log.Printf("scan: config add %s: %v", f.Path, err)
				}
			}
		} else {
			contentFiles = append(contentFiles, f)
		}
	}

	// Nothing to queue if neither evictions nor content adds remain
	if len(contentFiles) == 0 && len(results.EmptyFiles) == 0 {
		return results, nil
	}

	// R1053, R1648: queue evictions + content adds for the write goroutine
	// so LMDB writes serialize behind any in-flight write, and the actor
	// returns immediately.
	files := contentFiles         // capture for closure
	empties := results.EmptyFiles // capture for closure
	db.enqueueWrite(func(ftsCopy *microfts2.DB) {
		evictEmpty(ftsCopy, empties)
		idx := db.indexer.withFTS(ftsCopy)
		for _, f := range files {
			if _, err := idx.AddFile(f.Path, f.Strategy); err != nil {
				if errors.Is(err, microfts2.ErrNoChunks) || errors.Is(err, microfts2.ErrAlreadyIndexed) {
					continue
				}
				if errors.Is(err, os.ErrNotExist) {
					log.Printf("scan: skipping %s: %v", f.Path, err)
					continue
				}
				log.Printf("scan: add %s: %v", f.Path, err)
			}
		}
		db.drainWriteSchedule(idx)
	})
	return results, nil
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R1051, R1053
// RefreshAsync finds stale files and queues their re-indexing through the
// write actor. The stale file check (LMDB read) happens synchronously;
// the actual re-indexing happens in the write goroutine.
func (db *DB) RefreshAsync() error {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return fmt.Errorf("stale files: %w", err)
	}

	// Partition into missing and stale
	var missing []microfts2.FileStatus
	var stale []microfts2.FileStatus
	for _, s := range statuses {
		if s.Status == "missing" {
			missing = append(missing, s)
		} else if s.Status == "stale" {
			stale = append(stale, s)
		}
	}

	// Track missing files synchronously (small metadata writes)
	for _, m := range missing {
		info, err := db.fts.FileInfoByID(m.FileID)
		if err != nil {
			continue
		}
		if err := db.store.AddMissing(m.FileID, info.Names[0], timeNow()); err != nil {
			return err
		}
	}

	if len(stale) == 0 {
		return nil
	}

	// R1053: queue refresh work for write goroutine
	files := stale // capture for closure
	db.enqueueWrite(func(ftsCopy *microfts2.DB) {
		idx := db.indexer.withFTS(ftsCopy)
		idx.refreshBatch(files)
		db.drainWriteSchedule(idx)
	})
	return nil
}

// NewSearchCache enables FRecord caching in microfts2 for the duration
// of a search operation. Call the returned function to release the cache.
func (db *DB) NewSearchCache() func() {
	return db.fts.NewSearchCache()
}

// SearchCombined performs a combined search across both engines.
func (db *DB) SearchCombined(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchCombined(query, opts)
}

// SearchSplit performs a targeted search with --about, --contains, --regex.
func (db *DB) SearchSplit(opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchSplit(opts)
}

// SearchMulti runs a query through multiple scoring strategies.
func (db *DB) SearchMulti(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchMulti(query, opts)
}

// SearchFuzzy runs a typo-tolerant search via microfts2.SearchFuzzy.
// CRC: crc-Searcher.md | R745
func (db *DB) SearchFuzzy(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchFuzzy(query, opts)
}

// SearchGrouped runs a search and groups results by file with rendered previews.
func (db *DB) SearchGrouped(query string, opts SearchOpts) ([]GroupedResult, error) {
	return db.search.SearchGrouped(query, opts)
}

// GetChunks returns the target chunk and its positional neighbors.
func (db *DB) GetChunks(fpath, targetRange string, before, after int) ([]microfts2.ChunkResult, error) {
	return db.fts.GetChunks(fpath, targetRange, before, after)
}

// ResolveLink resolves an @link: value to a /content/ URL target.
// UUID branch first (TvidMap.Lookup against tag "id" → V record →
// chunkid → fileid → path + chunk Location). Path branch second:
// microfts2.CheckFile reports whether the literal value names a known
// file. Returns ok=false when neither resolves. Anchor parsing and
// content-hash fallback are deferred.
// CRC: crc-DB.md | Seq: seq-ark-tag-click.md | R1976, R1977, R1978
func (db *DB) ResolveLink(value string) (path, location string, ok bool) {
	if value == "" {
		return "", "", false
	}
	if path, location, ok = db.resolveLinkUUID(value); ok {
		return path, location, true
	}
	if status, err := db.fts.CheckFile(value); err == nil && status.FileID != 0 {
		return value, "", true
	}
	return "", "", false
}

// resolveLinkUUID handles the @id-based branch of ResolveLink.
// CRC: crc-DB.md | R1976, R1977
func (db *DB) resolveLinkUUID(value string) (path, location string, ok bool) {
	tvid, hit := db.store.TvidMap().Lookup("id", value)
	if !hit {
		return "", "", false
	}
	chunkID, fileID := db.lookupIDChunk(value, tvid)
	if chunkID == 0 {
		return "", "", false
	}
	if IsOverlayID(chunkID) {
		return db.resolveOverlayChunk(chunkID)
	}
	if fileID == 0 {
		return "", "", false
	}
	return db.locateChunkInFile(chunkID, fileID)
}

// lookupIDChunk reads V[id][value][tvid], decodes the first chunkid,
// and (for persistent chunkids) resolves to the owning fileid in the
// same View. Returns zero values when the record is missing or empty.
// CRC: crc-DB.md | R1976, R1977
func (db *DB) lookupIDChunk(value string, tvid uint64) (chunkID, fileID uint64) {
	_ = db.fts.Env().View(func(txn *lmdb.Txn) error {
		blob, err := txn.Get(db.store.dbi, tagValueFullKey("id", value, tvid))
		if err != nil {
			return nil
		}
		ids := decodeVarints(blob)
		if len(ids) == 0 {
			return nil
		}
		chunkID = ids[0]
		if IsOverlayID(chunkID) {
			return nil
		}
		crec, err := db.fts.ReadCRecord(txn, chunkID)
		if err == nil && len(crec.FileIDs) > 0 {
			fileID = crec.FileIDs[0].FileID
		}
		return nil
	})
	return chunkID, fileID
}

// resolveOverlayChunk maps a tmp:// chunkid to its source path. Tmp
// content has no per-chunk Location, so the result has empty location.
// CRC: crc-DB.md | R1976
func (db *DB) resolveOverlayChunk(chunkID uint64) (path, location string, ok bool) {
	fileIDs := db.store.tmp.FilesForChunk(chunkID)
	if len(fileIDs) == 0 {
		return "", "", false
	}
	return db.tmpPathForFile(fileIDs[0])
}

// locateChunkInFile reads FileInfoByID and returns the file's path
// plus the chunk's Location. CRC: crc-DB.md | R1976, R1977
func (db *DB) locateChunkInFile(chunkID, fileID uint64) (path, location string, ok bool) {
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Names) == 0 {
		return "", "", false
	}
	for _, c := range info.Chunks {
		if c.ChunkID == chunkID {
			return info.Names[0], c.Location, true
		}
	}
	return info.Names[0], "", true
}

// ResolveExtTarget returns chunkids identified by an @ext target.
// UUID branch first (TvidMap → V record's full chunkid blob, every
// chunk carrying that id); path branch second (CheckFile + FileInfoByID,
// first chunk only by preamble convention). Empty result means broken
// or unknown — callers treat that as a no-op annotation.
// CRC: crc-DB.md | R1985, R1986
func (db *DB) ResolveExtTarget(target string) []uint64 {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	if chunks := db.resolveExtUUID(target); len(chunks) > 0 {
		return chunks
	}
	return db.resolveExtPath(target)
}

// resolveExtUUID returns every chunkid carrying @id == value.
// CRC: crc-DB.md | R1985
func (db *DB) resolveExtUUID(value string) []uint64 {
	tvid, ok := db.store.TvidMap().Lookup("id", value)
	if !ok {
		return nil
	}
	var chunks []uint64
	_ = db.fts.Env().View(func(txn *lmdb.Txn) error {
		blob, err := txn.Get(db.store.dbi, tagValueFullKey("id", value, tvid))
		if err == nil {
			chunks = decodeVarints(blob)
		}
		return nil
	})
	return chunks
}

// resolveExtPath returns the file's first chunkid (preamble), or nil
// when the path is not indexed. Anchored target forms (path:line,
// path:string, path:/regex/, path[N]:anchor) are deferred — when they
// land they branch off resolveExtPath before the CheckFile probe.
// CRC: crc-DB.md | R1985, R1987
func (db *DB) resolveExtPath(path string) []uint64 {
	status, err := db.fts.CheckFile(path)
	if err != nil || status.FileID == 0 {
		return nil
	}
	info, err := db.fts.FileInfoByID(status.FileID)
	if err != nil || len(info.Chunks) == 0 {
		return nil
	}
	return []uint64{info.Chunks[0].ChunkID}
}

// tmpPathForFile resolves a tmp:// fileid to its source path. The
// overlay does not record per-chunk Locations, so location is empty.
// CRC: crc-DB.md | R1976
func (db *DB) tmpPathForFile(fileID uint64) (path, location string, ok bool) {
	for p, fid := range db.tmpPaths {
		if fid == fileID {
			return p, "", true
		}
	}
	return "", "", false
}

// QueryTrigramCounts returns trigram counts for a query string.
func (db *DB) QueryTrigramCounts(query string) ([]microfts2.TrigramCount, error) {
	return db.fts.QueryTrigramCounts(query)
}

// ConfigChangeAction classifies how a config field change is handled.
// CRC: crc-DB.md | R1550, R1551, R1552, R1553, R1554, R1555
type ConfigChangeAction int

const (
	ActionBenign     ConfigChangeAction = iota // update I records, proceed
	ActionDefer                                // write E record, defer to restart
	ActionFixMinimal                           // apply targeted fix, update I records
)

// ConfigChange describes one changed config field.
type ConfigChange struct {
	Field    string
	Action   ConfigChangeAction
	OldValue string
	NewValue string
}

// classifyField returns the action for a changed config field.
func classifyField(field string) ConfigChangeAction {
	switch field {
	case IFieldCaseInsensitive, IFieldChunkers:
		return ActionDefer
	case IFieldTagModel:
		return ActionFixMinimal
	default:
		return ActionBenign
	}
}

// DiffConfig compares loaded config against stored I records.
// Returns nil if no stored config exists (first run after rebuild).
// CRC: crc-DB.md | R1540, R1550-R1555
func (db *DB) DiffConfig() ([]ConfigChange, error) {
	stored, err := db.store.ReadConfig()
	if err != nil {
		return nil, err
	}
	if stored == nil {
		// No stored config — first run, write current config
		return nil, db.store.WriteConfig(db.config)
	}

	var changes []ConfigChange
	check := func(field, oldVal, newVal string) {
		if oldVal != newVal {
			changes = append(changes, ConfigChange{
				Field:    field,
				Action:   classifyField(field),
				OldValue: oldVal,
				NewValue: newVal,
			})
		}
	}
	checkJSON := func(field string, oldVal, newVal any) {
		oldJSON, _ := json.Marshal(oldVal)
		newJSON, _ := json.Marshal(newVal)
		if string(oldJSON) != string(newJSON) {
			changes = append(changes, ConfigChange{
				Field:    field,
				Action:   classifyField(field),
				OldValue: string(oldJSON),
				NewValue: string(newJSON),
			})
		}
	}

	check(IFieldDotfiles, strconv.FormatBool(stored.Dotfiles), strconv.FormatBool(db.config.Dotfiles))
	check(IFieldCaseInsensitive, strconv.FormatBool(stored.CaseInsensitive), strconv.FormatBool(db.config.CaseInsensitive))
	check(IFieldEmbedCmd, stored.EmbedCmd, db.config.EmbedCmd)
	check(IFieldQueryCmd, stored.QueryCmd, db.config.QueryCmd)
	check(IFieldTagModel, stored.TagModel, db.config.TagModel)
	check(IFieldSessionTTL, stored.SessionTTL, db.config.SessionTTL)
	checkJSON(IFieldGlobalInclude, stored.GlobalInclude, db.config.GlobalInclude)
	checkJSON(IFieldGlobalExclude, stored.GlobalExclude, db.config.GlobalExclude)
	checkJSON(IFieldStrategies, stored.Strategies, db.config.Strategies)
	checkJSON(IFieldSources, stored.Sources, db.config.Sources)
	checkJSON(IFieldChunkers, stored.Chunkers, db.config.Chunkers)
	checkJSON(IFieldSearchExclude, stored.SearchExclude, db.config.SearchExclude)
	checkJSON(IFieldSchedule, stored.Schedule, db.config.Schedule)
	checkJSON(IFieldEmbedTiers, stored.EmbedTiers, db.config.EmbedTiers)

	// Check for catastrophe: all sources gone
	if len(stored.Sources) > 0 && len(db.config.Sources) == 0 {
		changes = append(changes, ConfigChange{
			Field:  "sources_catastrophe",
			Action: ActionDefer,
		})
	}

	return changes, nil
}

// ApplyConfigChanges processes classified changes.
// Returns deferred changes (caller decides how to handle).
// CRC: crc-DB.md | R1553, R1554, R1555
func (db *DB) ApplyConfigChanges(changes []ConfigChange) []ConfigChange {
	var deferred []ConfigChange
	for _, c := range changes {
		switch c.Action {
		case ActionBenign:
			db.store.IPut(c.Field, c.NewValue)
		case ActionFixMinimal:
			if c.Field == IFieldTagModel {
				log.Printf("config: tag_model changed from %q to %q — dropping embeddings", c.OldValue, c.NewValue)
				db.store.DropEmbeddings()
				db.store.DropChunkEmbeddings() // R1620
				db.store.IPut(c.Field, c.NewValue)
			}
		case ActionDefer:
			deferred = append(deferred, c)
		}
	}
	return deferred
}

// ChunkStatsRow holds statistics for one strategy (or "all").
// CRC: crc-DB.md | R1521, R1522
type ChunkStatsRow struct {
	Strategy string
	Count    int
	Min      int
	Max      int
	Mean     int
	Median   int
	P90      int
	P95      int
	P99      int
}

// ChunkStatsResult holds overall and per-strategy chunk size stats.
// CRC: crc-DB.md | R1517, R1518, R1519, R1520, R1521, R1522
type ChunkStatsResult struct {
	Rows []ChunkStatsRow // first is "all", rest per-strategy alphabetically
}

// ChunkStats collects chunk size statistics across indexed files.
// filterFiles/excludeFiles scope the file set. sizeFn returns the size
// of a chunk's content (len for bytes, tokenize for tokens).
// CRC: crc-DB.md | R1517, R1518, R1519, R1520, R1521, R1522
func (db *DB) ChunkStats(filterFiles, excludeFiles []string, sizeFn func(string) int) (*ChunkStatsResult, error) {
	stale, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}

	matcher := &Matcher{Dotfiles: true}
	hasFilter := len(filterFiles) > 0 || len(excludeFiles) > 0
	useContentLens := sizeFn == nil // nil sizeFn = use CRecord ContentLen (fast path)

	// Collect sizes per strategy
	stratSizes := make(map[string][]int)
	var allSizes []int

	for _, s := range stale {
		if hasFilter {
			if len(filterFiles) > 0 {
				matched := false
				for _, pat := range filterFiles {
					if matcher.Match(pat, s.Path, false) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			excluded := false
			for _, pat := range excludeFiles {
				if matcher.Match(pat, s.Path, false) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}

		var sizes []int
		if useContentLens {
			// Fast path: read ContentLen from C records, no disk I/O
			lens, err := db.fts.ChunkContentLens(s.FileID)
			if err != nil || len(lens) == 0 {
				continue
			}
			sizes = lens
		} else {
			// Slow path: read content for tokenization
			chunks := db.AllChunks(s.Path)
			if chunks == nil {
				continue
			}
			sizes = make([]int, len(chunks))
			for i, c := range chunks {
				sizes[i] = sizeFn(c.Content)
			}
		}
		allSizes = append(allSizes, sizes...)
		stratSizes[s.Strategy] = append(stratSizes[s.Strategy], sizes...)
	}

	if len(allSizes) == 0 {
		return &ChunkStatsResult{}, nil
	}

	result := &ChunkStatsResult{}
	result.Rows = append(result.Rows, computeStatsRow("all", allSizes))

	// Per-strategy rows, sorted alphabetically
	strats := make([]string, 0, len(stratSizes))
	for k := range stratSizes {
		strats = append(strats, k)
	}
	sort.Strings(strats)
	for _, s := range strats {
		result.Rows = append(result.Rows, computeStatsRow(s, stratSizes[s]))
	}

	return result, nil
}

func computeStatsRow(strategy string, sizes []int) ChunkStatsRow {
	sort.Ints(sizes)
	n := len(sizes)
	sum := 0
	for _, s := range sizes {
		sum += s
	}
	return ChunkStatsRow{
		Strategy: strategy,
		Count:    n,
		Min:      sizes[0],
		Max:      sizes[n-1],
		Mean:     sum / n,
		Median:   percentile(sizes, 50),
		P90:      percentile(sizes, 90),
		P95:      percentile(sizes, 95),
		P99:      percentile(sizes, 99),
	}
}

func percentile(sorted []int, p int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (p * (n - 1)) / 100
	return sorted[idx]
}

// Status returns database status counts.
func (db *DB) Status() (*StatusInfo, error) {
	stale, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}

	missing, err := db.store.ListMissing()
	if err != nil {
		return nil, err
	}

	unresolved, err := db.store.ListUnresolved()
	if err != nil {
		return nil, err
	}

	var staleCount, totalChunks int
	var totalSize int64
	strategies := make(map[string]int)
	for _, s := range stale {
		if s.Status == "stale" {
			staleCount++
		}
		strategies[s.Strategy]++
		info, err := db.fts.FileInfoByID(s.FileID)
		if err == nil {
			totalChunks += len(info.Chunks)
			totalSize += info.FileLength
		}
	}

	// DB format version
	dbFormat, _ := db.fts.Version()

	// LMDB map usage
	var mapUsed, mapTotal int64
	env := db.fts.Env()
	if envInfo, err := env.Info(); err == nil {
		mapTotal = envInfo.MapSize
		if stat, err := env.Stat(); err == nil {
			mapUsed = (envInfo.LastPNO + 1) * int64(stat.PSize)
		}
	}

	return &StatusInfo{
		Version:    Version,
		DBFormat:   dbFormat,
		Files:      len(stale),
		TotalSize:  totalSize,
		Stale:      staleCount,
		Missing:    len(missing),
		Unresolved: len(unresolved),
		Chunks:     totalChunks,
		Sources:    len(db.config.Sources),
		Strategies: strategies,
		MapUsed:    mapUsed,
		MapTotal:   mapTotal,
		TmpFiles:   len(db.tmpPaths),
	}, nil
}

// RecordCount is one line in the --db output.
type RecordCount struct {
	Prefix     string `json:"prefix"`
	Purpose    string `json:"purpose"`
	Count      int64  `json:"count"`
	KeyBytes   int64  `json:"keyBytes"`
	ValueBytes int64  `json:"valueBytes"`
}

// RecordStats holds aggregate statistics for one record prefix.
// Matches microfts2.RecordStats so Store can return the same shape.
type RecordStats struct {
	Count      int64
	KeyBytes   int64
	ValueBytes int64
}

// DBRecordCounts holds per-subdatabase record counts.
type DBRecordCounts struct {
	Microfts2 []RecordCount `json:"microfts2"`
	Ark       []RecordCount `json:"ark"`
}

// StatusInfo holds database status counts and LMDB map usage.
type StatusInfo struct {
	Version    string         `json:"version"`
	DBFormat   string         `json:"dbFormat,omitempty"`
	Files      int            `json:"files"`
	TotalSize  int64          `json:"totalSize"`
	Stale      int            `json:"stale"`
	Missing    int            `json:"missing"`
	Unresolved int            `json:"unresolved"`
	Chunks     int            `json:"chunks"`
	Sources    int            `json:"sources"`
	Strategies map[string]int `json:"strategies"`
	MapUsed    int64          `json:"mapUsed"`
	MapTotal   int64          `json:"mapTotal"`
	TmpFiles   int            `json:"tmpFiles,omitempty"` // R676: tmp:// document count
	// UI fields — populated by server when ui-engine is running
	UIRunning  bool `json:"uiRunning"`
	UIPort     int  `json:"uiPort,omitempty"`
	UIIndexing bool `json:"uiIndexing"`
	// R1249: spectral search availability
	Spectral bool `json:"spectral"`
}

// StatusDB returns per-prefix record counts for both subdatabases.
// CRC: crc-DB.md | R899, R904, R905
func (db *DB) StatusDB() (*DBRecordCounts, error) {
	ftsLabels := map[string]string{
		"C": "chunks",
		"F": "files",
		"H": "hashes",
		"I": "config",
		"N": "paths",
		"T": "trigrams",
		"W": "tokens",
	}
	arkLabels := map[string]string{
		"D":  "tag-defs",
		"F":  "file-tags",
		"I":  "settings",
		"M":  "missing",
		"T":  "tag-totals",
		"U":  "unresolved",
		"V":  "tag-values",
		"E:": "errors",
		"EV": "tag-value-embeds",
		"EC": "chunk-embeds",
		"EF": "file-centroids",
		"PC": "page-content",
	}

	result := &DBRecordCounts{}

	// microfts2 records — single-byte prefixes only
	ftsCounts, err := db.fts.RecordCounts()
	if err != nil {
		return nil, fmt.Errorf("microfts2 record counts: %w", err)
	}
	ftsStats := make(map[string]RecordStats, len(ftsCounts))
	for k, v := range ftsCounts {
		ftsStats[string([]byte{k})] = RecordStats{Count: v.Count, KeyBytes: v.KeyBytes, ValueBytes: v.ValueBytes}
	}
	result.Microfts2 = buildRecordCounts(ftsStats, ftsLabels)

	// ark records — full-prefix keys
	arkCounts, err := db.store.RecordCounts()
	if err != nil {
		return nil, fmt.Errorf("ark record counts: %w", err)
	}
	result.Ark = buildRecordCounts(arkCounts, arkLabels)

	return result, nil
}

// buildRecordCounts converts raw prefix stats into sorted RecordCount slices.
func buildRecordCounts(stats map[string]RecordStats, labels map[string]string) []RecordCount {
	var recs []RecordCount
	// Include all known labels, even if count is 0
	for prefix, label := range labels {
		s := stats[prefix]
		recs = append(recs, RecordCount{
			Prefix:     prefix,
			Purpose:    label,
			Count:      s.Count,
			KeyBytes:   s.KeyBytes,
			ValueBytes: s.ValueBytes,
		})
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].Prefix < recs[j].Prefix
	})
	return recs
}

// Files returns all indexed file paths, including tmp:// documents.
// R671
func (db *DB) Files() ([]string, error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(statuses)+len(db.tmpPaths))
	for _, s := range statuses {
		paths = append(paths, s.Path)
	}
	for p := range db.tmpPaths {
		paths = append(paths, p)
	}
	return paths, nil
}

// Stale returns files that need re-indexing.
func (db *DB) Stale() ([]string, error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, s := range statuses {
		if s.Status == "stale" {
			paths = append(paths, s.Path)
		}
	}
	return paths, nil
}

// Missing returns missing file records.
func (db *DB) Missing() ([]MissingRecord, error) {
	return db.store.ListMissing()
}

// Dismiss removes missing files by path or pattern.
// Removes from the missing list and from both search engines.
func (db *DB) Dismiss(patterns []string) error {
	dismissed, err := db.store.DismissByPattern(patterns, db.matcher)
	if err != nil {
		return err
	}
	for _, rec := range dismissed {
		if err := db.indexer.RemoveByID(rec.FileID); err != nil {
			return fmt.Errorf("remove dismissed %s: %w", rec.Path, err)
		}
	}
	return nil
}

// Unresolved returns unresolved file records.
func (db *DB) Unresolved() ([]UnresolvedRecord, error) {
	return db.store.ListUnresolved()
}

// Resolve dismisses unresolved files by pattern.
func (db *DB) Resolve(patterns []string) error {
	return db.store.ResolveByPattern(patterns, db.matcher)
}

// Fetch returns the full content of an indexed file.
// The file must be known to microfts2 (in the index).
func (db *DB) Fetch(path string) ([]byte, error) {
	// R692: tmp:// paths read from overlay's stored content
	if strings.HasPrefix(path, "tmp://") {
		r, err := db.fts.TmpContent(path)
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if !db.IsIndexed(absPath) {
		return nil, fmt.Errorf("not indexed: %s", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return data, nil
}

// ReadSourceFile reads any file within a configured source directory.
// Unlike Fetch, this does not require the file to be indexed — it only
// checks that the path falls under a source directory.
// CRC: crc-DB.md | R1154, R1156
func (db *DB) ReadSourceFile(path string) ([]byte, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if !db.config.IsInSource(absPath) {
		return nil, fmt.Errorf("not in source: %s", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return data, nil
}

// FileStrategy returns the indexing strategy for a file, or "" if not indexed.
// CRC: crc-DB.md | R1158
func (db *DB) FileStrategy(path string) string {
	info, err := db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return ""
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil {
		return ""
	}
	return finfo.Strategy
}

// ChunkAttrs returns the attrs slice for a single chunk. Uses a fresh
// ChunkCache (same as ChunkText). Returns nil on lookup failure.
// CRC: crc-DB.md | R1705
func (db *DB) ChunkAttrs(path, rangeLabel string) []microfts2.Pair {
	cache := db.fts.NewChunkCache()
	chunks, err := cache.GetChunks(path, rangeLabel, 0, 0)
	if err != nil || len(chunks) == 0 {
		return nil
	}
	return chunks[0].Attrs
}

// ChunkText resolves a chunk range label to its text content.
// Uses a one-off chunk cache. Returns nil if the range is unresolvable.
// CRC: crc-DB.md | R1424
func (db *DB) ChunkText(path, rangeLabel string) []byte {
	cache := db.fts.NewChunkCache()
	text, ok := cache.ChunkText(path, rangeLabel)
	if !ok {
		return nil
	}
	return text
}

// AllChunks returns all chunk texts for a file, in order.
// Uses the FRecord to find the first range, then GetChunks with a large window.
// Returns nil if the file is not indexed or has no chunks.
// CRC: crc-DB.md | R1504
func (db *DB) AllChunks(path string) []microfts2.ChunkResult {
	info, err := db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return nil
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil || len(finfo.Chunks) == 0 {
		return nil
	}
	cache := db.fts.NewChunkCache()
	firstRange := finfo.Chunks[0].Location
	chunks, err := cache.GetChunks(path, firstRange, 0, len(finfo.Chunks))
	if err != nil {
		return nil
	}
	return chunks
}

// ChunkSizes returns byte sizes for all chunks of a file from CRecord ContentLen.
// No disk I/O — reads directly from the index. Returns nil if not indexed.
func (db *DB) ChunkSizes(path string) []int {
	info, err := db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return nil
	}
	lens, err := db.fts.ChunkContentLens(info.FileID)
	if err != nil {
		return nil
	}
	return lens
}

// AllChunkIDs returns the set of all unique chunkIDs referenced by F-records.
// CRC: crc-CLI.md | Seq: seq-embed-validate.md | R1856
func (db *DB) AllChunkIDs() (map[uint64]bool, error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}
	result := make(map[uint64]bool)
	for _, s := range statuses {
		info, err := db.fts.FileInfoByID(s.FileID)
		if err != nil {
			continue
		}
		for _, fce := range info.Chunks {
			result[fce.ChunkID] = true
		}
	}
	return result, nil
}

// AllChunkIDsPartitioned returns chunkIDs split by search_exclude. Embeddable
// contains chunkIDs referenced by at least one non-excluded file. Excluded
// contains chunkIDs referenced only by excluded files.
// CRC: crc-CLI.md | Seq: seq-embed-validate.md | R1865, R1866
func (db *DB) AllChunkIDsPartitioned(excludePatterns []string) (embeddable, excluded map[uint64]bool, err error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, nil, err
	}
	embeddable = make(map[uint64]bool)
	allExcluded := make(map[uint64]bool)
	for _, s := range statuses {
		info, err := db.fts.FileInfoByID(s.FileID)
		if err != nil {
			continue
		}
		isExcluded := matchesAnyGlob(s.Path, excludePatterns)
		for _, fce := range info.Chunks {
			if isExcluded {
				allExcluded[fce.ChunkID] = true
			} else {
				embeddable[fce.ChunkID] = true
			}
		}
	}
	excluded = make(map[uint64]bool)
	for id := range allExcluded {
		if !embeddable[id] {
			excluded[id] = true
		}
	}
	return embeddable, excluded, nil
}

// FileChunkCounts returns fileID → chunk count for all indexed files.
// CRC: crc-CLI.md | Seq: seq-embed-validate.md | R1802
func (db *DB) FileChunkCounts() (map[uint64]int, error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}
	result := make(map[uint64]int, len(statuses))
	for _, s := range statuses {
		lens, err := db.fts.ChunkContentLens(s.FileID)
		if err != nil {
			continue
		}
		result[s.FileID] = len(lens)
	}
	return result, nil
}

// IsIndexed returns true if the given file path is in the index.
func (db *DB) IsIndexed(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	status, err := db.fts.CheckFile(absPath)
	if err != nil {
		return false
	}
	return status.FileID != 0
}

// SourcesCheck expands glob sources and reconciles with concrete sources.
func (db *DB) SourcesCheck() (*SourcesCheckResult, error) {
	result, err := db.config.ResolveGlobs()
	if err != nil {
		return nil, err
	}
	if len(result.Added) > 0 {
		if err := db.SaveConfig(); err != nil {
			return result, fmt.Errorf("save config: %w", err)
		}
	}
	return result, nil
}

// TagList returns all known tags with their total counts.
func (db *DB) TagList() ([]TagCount, error) {
	return db.store.ListTags()
}

// TagCounts returns counts for specific tags.
func (db *DB) TagCounts(tags []string) ([]TagCount, error) {
	return db.store.TagCounts(tags)
}

// TagFileInfo is a file with tag occurrence info.
type TagFileInfo struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Tag   string `json:"tag"`
	Count uint32 `json:"count"`
}

// TagFiles returns files containing the specified tags with file size.
// CRC: crc-DB.md | R1948
func (db *DB) TagFiles(tags []string) ([]TagFileInfo, error) {
	records, err := db.store.TagFiles(tags)
	if err != nil {
		return nil, err
	}
	var results []TagFileInfo
	for _, rec := range records {
		path, ok := db.resolveFilePath(rec.FileID)
		if !ok {
			continue
		}
		var size int64
		if !IsOverlayID(rec.FileID) {
			if fi, err := os.Stat(path); err == nil {
				size = fi.Size()
			}
		}
		results = append(results, TagFileInfo{
			Path:  path,
			Size:  size,
			Tag:   rec.Tag,
			Count: rec.Count,
		})
	}
	return results, nil
}

// inboxFields collects the tag-block fields the inbox displays.
// CRC: crc-DB.md | R1952
type inboxFields struct {
	status, to, from, requestID, summary, kind  string
	responseHandled, requestHandled, statusDate string
}

// readInboxFields collects the tag values the inbox needs. Persistent
// files use ParseTagBlock on disk content (preserves canonical block
// semantics for `--message`-style messages). Tmp:// files read via
// Store.FileTagValues, which dispatches to the in-memory tag overlay.
// Returns ok=false when the @status tag is absent — the candidate is
// not an inboxable message.
// CRC: crc-DB.md | R1147, R1149, R1952
func (db *DB) readInboxFields(fileID uint64, path string) (inboxFields, bool) {
	var f inboxFields
	if IsOverlayID(fileID) {
		got, err := db.store.FileTagValues(fileID, []string{
			"status", "to-project", "from-project",
			"ark-request", "ark-response", "issue",
			"response-handled", "request-handled", "status-date",
		})
		if err != nil {
			return f, false
		}
		f.status = got["status"]
		if f.status == "" {
			return f, false
		}
		f.to = got["to-project"]
		if i := strings.IndexByte(f.to, ','); i >= 0 {
			f.to = strings.TrimSpace(f.to[:i])
		}
		f.from = got["from-project"]
		if v := got["ark-request"]; v != "" {
			f.requestID = v
			if f.to == f.from {
				f.kind = "self"
			} else {
				f.kind = "request"
			}
			f.summary = got["issue"]
		} else if v := got["ark-response"]; v != "" {
			f.requestID = v
			f.kind = "response"
			f.summary = got["issue"]
			if f.summary == "" {
				f.summary = "ark-response:" + v
			}
		}
		f.responseHandled = got["response-handled"]
		f.requestHandled = got["request-handled"]
		f.statusDate = got["status-date"]
		return f, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return f, false
	}
	tb := ParseTagBlock(data)
	var ok bool
	f.status, ok = tb.Get("status")
	if !ok {
		return f, false
	}
	f.to, _ = tb.Get("to-project")
	if i := strings.IndexByte(f.to, ','); i >= 0 {
		f.to = strings.TrimSpace(f.to[:i])
	}
	f.from, _ = tb.Get("from-project")
	if v, ok := tb.Get("ark-request"); ok {
		f.requestID = v
		if f.to == f.from {
			f.kind = "self"
		} else {
			f.kind = "request"
		}
		if iss, ok := tb.Get("issue"); ok {
			f.summary = iss
		}
	} else if v, ok := tb.Get("ark-response"); ok {
		f.requestID = v
		f.kind = "response"
		if iss, ok := tb.Get("issue"); ok {
			f.summary = iss
		} else {
			f.summary = "ark-response:" + v
		}
	}
	f.responseHandled, _ = tb.Get("response-handled")
	f.requestHandled, _ = tb.Get("request-handled")
	f.statusDate, _ = tb.Get("status-date")
	return f, true
}

// resolveFilePath returns the path for a fileid. Persistent fileids
// resolve via microfts2's FileInfoByID; overlay fileids resolve via
// the in-memory tmp paths map.
// CRC: crc-DB.md | R1948
func (db *DB) resolveFilePath(fileID uint64) (string, bool) {
	if IsOverlayID(fileID) {
		for path, fid := range db.tmpPaths {
			if fid == fileID {
				return path, true
			}
		}
		return "", false
	}
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Names) == 0 {
		return "", false
	}
	return info.Names[0], true
}

// InboxEntry is a message from the cross-project messaging system.
// R563-R568, R617, R618, R619, R621, R622
type InboxEntry struct {
	Status          string `json:"status"`
	To              string `json:"to"`
	From            string `json:"from"`
	Summary         string `json:"summary"`
	Path            string `json:"path"`
	RequestID       string `json:"requestId"`
	Kind            string `json:"kind"`            // "request", "response", or "self"
	ResponseHandled string `json:"responseHandled"` // @response-handled: tag value
	RequestHandled  string `json:"requestHandled"`  // @request-handled: tag value
	StatusDate      string `json:"statusDate"`      // @status-date: tag value (R765)
}

// Inbox returns cross-project messages from the tag index.
// CRC: crc-DB.md | Seq: seq-message.md | R563-R568, R617-R622, R1145-R1150
// V records filter candidates (status, archived); ParseTagBlock on survivors
// gives precise header values. Hybrid: cheap LMDB filtering, correct file reads.
// If showAll is false, completed/done/denied messages are excluded.
// If includeArchived is false, archived messages are excluded.
func (db *DB) Inbox(showAll, includeArchived bool) ([]InboxEntry, error) {
	// R1145: get candidate fileids with @status: tag
	records, err := db.store.TagFiles([]string{"status"})
	if err != nil {
		return nil, err
	}

	// R1148: build exclusion set from V records for cheap filtering.
	// Post-migration TagValueFiles returns chunkids; resolve to fileids
	// via microfts2 C-records so excludeIDs stays file-level.
	addExcluded := func(excludeIDs map[uint64]bool, chunkIDs []uint64) error {
		return db.fts.Env().View(func(txn *lmdb.Txn) error {
			for _, cid := range chunkIDs {
				crec, err := db.fts.ReadCRecord(txn, cid)
				if err != nil {
					continue
				}
				for _, f := range crec.FileIDs {
					excludeIDs[f.FileID] = true
				}
			}
			return nil
		})
	}
	var excludeIDs map[uint64]bool
	if !showAll {
		excludeIDs = make(map[uint64]bool)
		for _, status := range []string{"completed", "denied"} {
			ids, err := db.store.TagValueFiles("status", status)
			if err != nil {
				return nil, err
			}
			if err := addExcluded(excludeIDs, ids); err != nil {
				return nil, err
			}
		}
	}
	if !includeArchived {
		ids, err := db.store.TagValueFiles("archived", "true")
		if err != nil {
			return nil, err
		}
		if excludeIDs == nil {
			excludeIDs = make(map[uint64]bool)
		}
		if err := addExcluded(excludeIDs, ids); err != nil {
			return nil, err
		}
	}

	seen := make(map[uint64]bool)
	var entries []InboxEntry
	for _, rec := range records {
		if seen[rec.FileID] {
			continue
		}
		seen[rec.FileID] = true

		if excludeIDs != nil && excludeIDs[rec.FileID] {
			continue
		}

		// R1146: persistent files filter to /requests/ paths. Tmp://
		// messages bypass the path filter — any tmp:// document with
		// a @status: tag is inboxable. R1952
		path, ok := db.resolveFilePath(rec.FileID)
		if !ok {
			continue
		}
		if !IsOverlayID(rec.FileID) && !strings.Contains(path, "/requests/") {
			continue
		}

		f, ok := db.readInboxFields(rec.FileID, path)
		if !ok {
			continue
		}
		statusVal := f.status
		toVal := f.to
		fromVal := f.from
		requestID := f.requestID
		summary := f.summary
		kind := f.kind
		responseHandled := f.responseHandled
		requestHandled := f.requestHandled
		statusDate := f.statusDate
		entries = append(entries, InboxEntry{
			Status:          statusVal,
			To:              toVal,
			From:            fromVal,
			Summary:         summary,
			Path:            path,
			RequestID:       requestID,
			Kind:            kind,
			ResponseHandled: responseHandled,
			RequestHandled:  requestHandled,
			StatusDate:      statusDate,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Status == "open") != (entries[j].Status == "open") {
			return entries[i].Status == "open"
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// TagDefInfo is a tag definition with its source file path.
type TagDefInfo struct {
	Tag         string `json:"tag"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// TagDefs returns tag definitions from LMDB, resolving fileids to paths.
func (db *DB) TagDefs(tags []string) ([]TagDefInfo, error) {
	records, err := db.store.ListTagDefs(tags)
	if err != nil {
		return nil, err
	}
	var results []TagDefInfo
	for _, rec := range records {
		info, err := db.fts.FileInfoByID(rec.FileID)
		if err != nil {
			continue
		}
		results = append(results, TagDefInfo{
			Tag:         rec.Tag,
			Description: rec.Description,
			Path:        info.Names[0],
		})
	}
	return results, nil
}

// TagValues returns values for a tag, optionally filtered by prefix, with counts.
func (db *DB) TagValues(tag, prefix string) ([]TagValueCount, error) {
	return db.store.QueryTagValues(tag, prefix)
}

// TagValueFileInfo is a (tag, value) pair with the files that have it.
type TagValueFileInfo struct {
	Value string   `json:"value"`
	Count int      `json:"count"`
	Files []string `json:"files,omitempty"`
}

// TagValuesWithFiles returns values for a tag with resolved file paths.
func (db *DB) TagValuesWithFiles(tag, prefix string) ([]TagValueFileInfo, error) {
	values, err := db.store.QueryTagValues(tag, prefix)
	if err != nil {
		return nil, err
	}
	var results []TagValueFileInfo
	for _, v := range values {
		ids, err := db.store.TagValueFiles(tag, v.Value)
		if err != nil {
			continue
		}
		var paths []string
		for _, id := range ids {
			info, err := db.fts.FileInfoByID(id)
			if err != nil {
				continue
			}
			paths = append(paths, info.Names[0])
		}
		results = append(results, TagValueFileInfo{
			Value: v.Value,
			Count: v.Count,
			Files: paths,
		})
	}
	return results, nil
}

// TagContextEntry is a tag occurrence with its context line.
type TagContextEntry struct {
	Path string `json:"path"`
	Tag  string `json:"tag"`
	Line string `json:"line"`
}

// TagContext returns tag occurrences with context (tag to end of line).
func (db *DB) TagContext(tags []string) ([]TagContextEntry, error) {
	records, err := db.store.TagFiles(tags)
	if err != nil {
		return nil, err
	}

	// Group by fileid to avoid re-reading the same file
	type fileGroup struct {
		path string
		tags []string
	}
	groups := make(map[uint64]*fileGroup)
	for _, rec := range records {
		g, ok := groups[rec.FileID]
		if !ok {
			info, err := db.fts.FileInfoByID(rec.FileID)
			if err != nil {
				continue
			}
			g = &fileGroup{path: info.Names[0]}
			groups[rec.FileID] = g
		}
		g.tags = append(g.tags, rec.Tag)
	}

	var entries []TagContextEntry
	for _, g := range groups {
		data, err := os.ReadFile(g.path)
		if err != nil {
			continue
		}
		// Build needles once, scan each line against all tags
		needles := make([]string, len(g.tags))
		for i, tag := range g.tags {
			needles[i] = "@" + tag + ":"
		}
		for _, line := range strings.Split(string(data), "\n") {
			for i, needle := range needles {
				idx := strings.Index(line, needle)
				if idx < 0 {
					continue
				}
				entries = append(entries, TagContextEntry{
					Path: g.path,
					Tag:  g.tags[i],
					Line: strings.TrimSpace(line[idx:]),
				})
			}
		}
	}
	return entries, nil
}

// chunkCache is an LRU cache for chunker output. The cache key is (path, content length) —
// if the file size changed, the entry is stale. The closure captures this cache so
// microfts2's verify path gets cached chunks without knowing about the cache.
type chunkCache struct {
	maxSize int
	entries map[chunkCacheKey]*chunkCacheEntry
	order   []*chunkCacheEntry // LRU order, most recent last
}

type chunkCacheKey struct {
	path       string
	contentLen int
}

type chunkCacheEntry struct {
	key    chunkCacheKey
	chunks []microfts2.Chunk
}

func newChunkCache(maxSize int) *chunkCache {
	return &chunkCache{
		maxSize: maxSize,
		entries: make(map[chunkCacheKey]*chunkCacheEntry),
	}
}

// wrap returns a ChunkFunc that checks the cache before calling the underlying chunker.
func (cc *chunkCache) wrap(fn microfts2.ChunkFunc) microfts2.ChunkFunc {
	return func(path string, content []byte, yield func(microfts2.Chunk) bool) error {
		key := chunkCacheKey{path, len(content)}

		if entry, ok := cc.entries[key]; ok {
			cc.touch(entry)
			for _, c := range entry.chunks {
				if !yield(c) {
					return nil
				}
			}
			return nil
		}

		// Cache miss — run the real chunker and collect results
		var chunks []microfts2.Chunk
		err := fn(path, content, func(c microfts2.Chunk) bool {
			// Copy to decouple from the content buffer
			cp := microfts2.Chunk{
				Range:   append([]byte{}, c.Range...),
				Content: append([]byte{}, c.Content...),
				Attrs:   microfts2.CopyPairs(c.Attrs),
			}
			chunks = append(chunks, cp)
			return true
		})
		if err != nil {
			return err
		}

		cc.put(key, chunks)

		// Now yield the cached chunks to the caller
		for _, c := range chunks {
			if !yield(c) {
				return nil
			}
		}
		return nil
	}
}

func (cc *chunkCache) touch(entry *chunkCacheEntry) {
	for i, e := range cc.order {
		if e == entry {
			cc.order = append(cc.order[:i], cc.order[i+1:]...)
			break
		}
	}
	cc.order = append(cc.order, entry)
}

func (cc *chunkCache) put(key chunkCacheKey, chunks []microfts2.Chunk) {
	// Evict oldest if at capacity
	for len(cc.order) >= cc.maxSize {
		oldest := cc.order[0]
		cc.order = cc.order[1:]
		delete(cc.entries, oldest.key)
	}
	entry := &chunkCacheEntry{key: key, chunks: chunks}
	cc.entries[key] = entry
	cc.order = append(cc.order, entry)
}

// CRC: crc-DB.md | R236, R237, R238, R239, R240, R241, R242, R243, R244, R245, R247
// JSONLChunkFunc is a content-aware chunker for Claude conversation logs.
// Parses each line as JSON and extracts only human-readable text
// (user/assistant text blocks and thinking blocks). Skips tool_use,
// tool_result, signatures, metadata, and non-message record types.
func JSONLChunkFunc(_ string, content []byte, yield func(microfts2.Chunk) bool) error {
	lineNum := 0
	start := 0
	for i := 0; i <= len(content); i++ {
		if i < len(content) && content[i] != '\n' {
			continue
		}
		lineNum++
		line := content[start:i]
		start = i + 1

		if len(line) == 0 {
			continue
		}

		text := extractJSONLTextFast(line)
		if len(text) == 0 {
			continue
		}

		r := fmt.Sprintf("%d-%d", lineNum, lineNum)
		chunk := microfts2.Chunk{Range: []byte(r), Content: text}
		// R1507-R1508: extract role and skill attrs from JSONL metadata.
		var attrs []microfts2.Pair
		if ts := extractJSONLTimestamp(line); ts != nil {
			attrs = append(attrs, microfts2.Pair{Key: []byte("timestamp"), Value: ts})
		}
		if role := extractJSONLRole(line); role != "" {
			attrs = append(attrs, microfts2.Pair{Key: []byte("role"), Value: []byte(role)})
			if role == "skill" {
				if name := extractSkillName(text); name != "" {
					attrs = append(attrs, microfts2.Pair{Key: []byte("skill"), Value: []byte(name)})
				}
			}
		}
		chunk.Attrs = attrs
		if !yield(chunk) {
			return nil
		}
	}
	return nil
}

// JSONLChunkFuncOld is the json.Unmarshal-based chunker, kept for comparison.
func JSONLChunkFuncOld(_ string, content []byte, yield func(microfts2.Chunk) bool) error {
	lineNum := 0
	start := 0
	for i := 0; i <= len(content); i++ {
		if i < len(content) && content[i] != '\n' {
			continue
		}
		lineNum++
		line := content[start:i]
		start = i + 1

		if len(line) == 0 {
			continue
		}

		text := extractJSONLText(line)
		if text == "" {
			continue
		}

		r := fmt.Sprintf("%d-%d", lineNum, lineNum)
		if !yield(microfts2.Chunk{Range: []byte(r), Content: []byte(text)}) {
			return nil
		}
	}
	return nil
}

// extractJSONLTimestamp finds "timestamp":"..." in a JSONL line, parses it
// as RFC3339, and returns Unix nanoseconds as a byte string (matching the
// format microfts2.chunkTimestamp expects via strconv.ParseInt).
func extractJSONLTimestamp(line []byte) []byte {
	valByte, pos := findKeyValue(line, []byte("timestamp"))
	if pos < 0 || valByte != '"' {
		return nil
	}
	// Extract the string value (pos points to the opening quote)
	start := pos + 1
	end := bytes.IndexByte(line[start:], '"')
	if end < 0 {
		return nil
	}
	tsStr := string(line[start : start+end])
	t, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		t, err = time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return nil
		}
	}
	return []byte(strconv.FormatInt(t.UnixNano(), 10))
}

// extractJSONLTextFast extracts searchable text using a DFT byte scanner.
// Scans for the "content" key, then handles two cases:
//   - string value: the entire string is the chunk text
//   - array value: extracts "text" and "thinking" fields from blocks
//
// extractJSONLRole derives a role from the JSONL record's top-level
// "type" and "isMeta" fields. Returns "human", "assistant", "skill", or "".
// Uses depth-aware scanning because nested content blocks also have
// "type" keys (e.g. "type":"text") that would shadow the top-level one.
// CRC: crc-DB.md | R1507
func extractJSONLRole(line []byte) string {
	typ := findTopLevelString(line, `"type":`)
	switch typ {
	case "assistant":
		return "assistant"
	case "user":
		// isMeta is always top-level, but a simple contains check is safe
		// because no nested object uses this key.
		if bytes.Contains(line, []byte(`"isMeta":true`)) {
			return "skill"
		}
		return "human"
	default:
		return ""
	}
}

// findTopLevelString finds a key at brace depth 1 and returns its string value.
// The key must include the colon, e.g. `"type":`. Returns "" if not found.
func findTopLevelString(line []byte, key string) string {
	keyBytes := []byte(key)
	depth := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '{':
			depth++
		case '}':
			depth--
		case '"':
			if depth == 1 && bytes.HasPrefix(line[i:], keyBytes) {
				// Found at top level — extract the string value after the key.
				valStart := i + len(keyBytes)
				if valStart < len(line) && line[valStart] == '"' {
					valStart++ // skip opening quote
					valEnd := scanStringEnd(line, valStart)
					if valEnd >= 0 {
						return string(line[valStart:valEnd])
					}
				}
				return ""
			}
			// Skip past this string.
			end := scanStringEnd(line, i+1)
			if end < 0 {
				return ""
			}
			i = end // loop increments past closing quote
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return ""
}

// extractSkillName parses the skill name from extracted chunk text.
// Looks for "Base directory for this skill: PATH" and returns the
// last path component.
// CRC: crc-DB.md | R1508
func extractSkillName(text []byte) string {
	prefix := []byte("Base directory for this skill: ")
	if !bytes.HasPrefix(text, prefix) {
		return ""
	}
	rest := text[len(prefix):]
	end := bytes.IndexByte(rest, '\n')
	if end < 0 {
		end = len(rest)
	}
	path := string(bytes.TrimRight(rest[:end], " \t\r"))
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func extractJSONLTextFast(line []byte) []byte {
	// Quick skip: check for known non-message types before full scan.
	if bytes.Contains(line, []byte(`"type":"progress"`)) ||
		bytes.Contains(line, []byte(`"type":"file-history-snapshot"`)) ||
		bytes.Contains(line, []byte(`"type":"queue-operation"`)) ||
		bytes.Contains(line, []byte(`"type":"system"`)) ||
		bytes.Contains(line, []byte(`"type":"last-prompt"`)) {
		return nil
	}

	// Find "content" key and its value
	contentVal, contentStart := findKeyValue(line, []byte("content"))
	if contentStart < 0 {
		return nil
	}

	// Case 1: "content":"string"
	if contentVal == '"' {
		valStart := contentStart + 1
		valEnd := scanStringEnd(line, valStart)
		if valEnd < 0 {
			return nil
		}
		return unescapeJSON(line[valStart:valEnd])
	}

	// Case 2: "content":[...blocks...]
	if contentVal != '[' {
		return nil
	}

	// Scan blocks inside the array for "text" and "thinking" values
	var parts []byte
	i := contentStart + 1 // past '['
	for i < len(line) {
		if line[i] == ']' {
			break
		}
		if line[i] == '"' {
			keyStart := i + 1
			keyEnd := scanStringEnd(line, keyStart)
			if keyEnd < 0 {
				break
			}
			key := line[keyStart:keyEnd]
			i = keyEnd + 1

			// Skip to colon
			for i < len(line) && line[i] != ':' && line[i] != ',' && line[i] != '}' {
				i++
			}
			if i >= len(line) || line[i] != ':' {
				continue
			}
			i++ // past colon
			for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				i++
			}

			isText := bytes.Equal(key, []byte("text"))
			isThinking := bytes.Equal(key, []byte("thinking"))

			if (isText || isThinking) && i < len(line) && line[i] == '"' {
				valStart := i + 1
				valEnd := scanStringEnd(line, valStart)
				if valEnd < 0 {
					break
				}
				val := unescapeJSON(line[valStart:valEnd])
				if len(val) > 0 {
					if len(parts) > 0 {
						parts = append(parts, '\n')
					}
					parts = append(parts, val...)
				}
				i = valEnd + 1
			} else {
				i = skipJSONValue(line, i)
			}
		} else {
			i++
		}
	}
	return parts
}

// findKeyValue scans for a JSON key and returns the first byte of its value
// and the position of that byte. Returns (-1, -1) if not found.
func findKeyValue(data, key []byte) (byte, int) {
	target := make([]byte, 0, len(key)+2)
	target = append(target, '"')
	target = append(target, key...)
	target = append(target, '"')

	i := 0
	for i < len(data) {
		if data[i] == '"' {
			// Check if this is our key
			if bytes.HasPrefix(data[i:], target) {
				i += len(target)
				// Skip to colon
				for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
					i++
				}
				if i >= len(data) || data[i] != ':' {
					continue
				}
				i++ // past colon
				for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
					i++
				}
				if i < len(data) {
					return data[i], i
				}
				return 0, -1
			}
			// Not our key — skip past this string
			end := scanStringEnd(data, i+1)
			if end < 0 {
				return 0, -1
			}
			i = end + 1
		} else {
			i++
		}
	}
	return 0, -1
}

// scanStringEnd finds the closing quote of a JSON string, handling escapes.
// start points to the first character after the opening quote.
// Returns the index of the closing quote, or -1 if not found.
func scanStringEnd(data []byte, start int) int {
	i := start
	for i < len(data) {
		if data[i] == '\\' {
			i += 2 // skip escaped char
		} else if data[i] == '"' {
			return i
		} else {
			i++
		}
	}
	return -1
}

// skipJSONValue skips over a JSON value starting at position i.
// Handles strings, numbers, booleans, null, arrays, and objects.
func skipJSONValue(data []byte, i int) int {
	if i >= len(data) {
		return i
	}
	switch data[i] {
	case '"':
		end := scanStringEnd(data, i+1)
		if end < 0 {
			return len(data)
		}
		return end + 1
	case '{', '[':
		// Track nesting with a counter (no stack needed — just depth)
		open := data[i]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		depth := 1
		i++
		for i < len(data) && depth > 0 {
			if data[i] == '"' {
				end := scanStringEnd(data, i+1)
				if end < 0 {
					return len(data)
				}
				i = end + 1
			} else if data[i] == open {
				depth++
				i++
			} else if data[i] == close {
				depth--
				i++
			} else {
				i++
			}
		}
		return i
	default:
		// number, bool, null — scan to next structural char
		for i < len(data) && data[i] != ',' && data[i] != '}' && data[i] != ']' {
			i++
		}
		return i
	}
}

// unescapeJSON handles basic JSON string escapes.
func unescapeJSON(data []byte) []byte {
	if bytes.IndexByte(data, '\\') < 0 {
		return data // fast path: no escapes
	}
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == '\\' && i+1 < len(data) {
			switch data[i+1] {
			case '"', '\\', '/':
				out = append(out, data[i+1])
			case 'n':
				out = append(out, '\n')
			case 't':
				out = append(out, '\t')
			case 'r':
				out = append(out, '\r')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'u':
				if i+5 < len(data) {
					r := hexToRune(data[i+2 : i+6])
					if r >= 0 {
						var buf [4]byte
						n := utf8.EncodeRune(buf[:], r)
						out = append(out, buf[:n]...)
						i += 6
						continue
					}
				}
				out = append(out, data[i], data[i+1])
			default:
				out = append(out, data[i], data[i+1])
			}
			i += 2
		} else {
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// hexToRune converts 4 hex bytes to a rune. Returns -1 on invalid input.
func hexToRune(h []byte) rune {
	if len(h) != 4 {
		return -1
	}
	var r rune
	for _, b := range h {
		r <<= 4
		switch {
		case b >= '0' && b <= '9':
			r |= rune(b - '0')
		case b >= 'a' && b <= 'f':
			r |= rune(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			r |= rune(b - 'A' + 10)
		default:
			return -1
		}
	}
	return r
}

// extractJSONLText extracts searchable text using json.Unmarshal (old, slow).
func extractJSONLText(line []byte) string {
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return ""
	}

	// Skip non-message record types
	switch record.Type {
	case "progress", "file-history-snapshot", "queue-operation", "system", "last-prompt":
		return ""
	}

	if len(record.Message.Content) == 0 {
		return ""
	}

	// Content can be a string or an array of blocks
	var str string
	if json.Unmarshal(record.Message.Content, &str) == nil {
		return str
	}

	var blocks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	}
	if json.Unmarshal(record.Message.Content, &blocks) != nil {
		return ""
	}

	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "thinking":
			if b.Thinking != "" {
				parts = append(parts, b.Thinking)
			}
		}
	}
	return strings.Join(parts, "\n")
}
