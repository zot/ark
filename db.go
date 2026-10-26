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
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zot/microfts2"

	"github.com/BurntSushi/toml"
	"go.etcd.io/bbolt"
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
	extmap     *ExtMap     // CRC: crc-ExtMap.md | R1992
	pdfChunker *PDFChunker // CRC: crc-PDFChunker.md | R1720, R1726
	// R3214: bible — staged book-index records, flushed after each file
	// commits; also the per-source hook's receiver (R3217).
	bibleChunker *bibleChunker

	dbPath        string
	tmpMu         sync.RWMutex      // protects tmpPaths against concurrent write-actor and actor reads
	tmpPaths      map[string]uint64 // R664: tmp:// path → fileid tracking
	svc           *ChanSvc          // R986: the closure actor (crc-ChanSvc.md)
	pubsub        *PubSub           // R2281: set by SetPubSub; nil disables centralized tmp:// publish
	chunkerByName map[string]any    // R2386, R2389: mirror of microfts2's chunker registry for ChunkerMetadata lookup by strategy name

	// Write actor: read/write path separation.
	// CRC: crc-DB.md | R1051-R1068
	writeQueue       []func(*microfts2.DB) // queued write closures
	writing          bool                  // true while a write goroutine is in flight
	onWriteComplete  func([]scheduleItem)  // callback for schedule items from write goroutines
	onFileStored     func(path string)     // R3579: on the actor, after a scan's index writes for path landed
	writeIdleWaiters []chan struct{}       // R2989: one-shot signals when the write queue drains; actor-only

	// Per-file scan actor registry (R3420). One entry per changed path with an
	// in-flight scan, each carrying the changeNum stamp of its authoritative
	// scan. Actor-owned — read and written only inside DB-actor closures, so it
	// needs no mutex (the actor serializes access). Replaces the R3005
	// pendingRefresh coalescing set (retired T280): a new event for a path with a
	// scan in flight bumps the changeNum, superseding the older scan's store.
	// CRC: crc-FileScanActor.md | R3420
	fileScans map[string]*fileScan
	scanSeq   uint64 // R3421: process-global monotonic changeNum counter, never reset per entry (ABA guard)
	// scanBytesSinceRelease counts bytes chunked by stored scans since the last
	// memory release; actor-only. R3602
	scanBytesSinceRelease int64
	// scanSem bounds concurrent off-slot file preparations to runtime.NumCPU(),
	// so a reconcile of thousands of stale files never spawns thousands of
	// concurrent computes. Enforces R519 under the per-file scan-actor model.
	// CRC: crc-FileScanActor.md | R3438
	scanSem chan struct{}

	// reconcilePending is set while a reconcile scan is queued but has not yet
	// begun, so further requests merge into it instead of queueing scans of
	// their own (R346, R3351). Actor-owned — read and written only inside
	// closures, which is what lets it be a plain bool needing no lock. Cleared
	// as the scan *starts*, so a request
	// arriving mid-scan queues a fresh one rather than being swallowed by a
	// scan that may already have walked past whatever changed.
	reconcilePending bool

	// reconcileWalking is set while the reconcile scan enumeration runs off the
	// DB actor in a goroutine (R3433); reconcileWalkPending records that a
	// reconcile arrived during that window. Both are actor-owned — read and set
	// only inside DB-actor closures, so they need no lock (changeNum's spirit).
	// The walk is off-actor but the enumeration is coalesced to one at a time:
	// a reconcile finding reconcileWalking set marks pending and returns, and the
	// walk's completion re-triggers one reconcile if pending, so a request made
	// during a walk is delayed rather than lost. reconcileWalking also holds
	// writeIdle open so a rebuild's WaitWritesIdle cannot return mid-walk. R3434
	reconcileWalking     bool
	reconcileWalkPending bool

	// reconcileStats holds the stale/missing/unresolved counts the most recent
	// reconcile computed, with the wall-clock time it computed them. Written on
	// the DB actor by captureReconcileStats (at the end of RefreshAsync) and read
	// via the bindStatusRead snapshot for `ark status`; actor-owned, a plain
	// struct needing no mutex (changeNum's spirit). The zero value (at.IsZero())
	// means no reconcile has completed in this process — status renders `-`. This
	// is what lets `ark status` report staleness without re-walking the corpus.
	// CRC: crc-DB.md | R3435
	reconcileStats reconcileCounts
}

// reconcileCounts is the snapshot of a reconcile's file counts that `ark status`
// reports off the actor. All fields are set together on the DB actor by
// captureReconcileStats; the zero value means no reconcile has run yet.
// CRC: crc-DB.md | R3435
type reconcileCounts struct {
	stale      int
	missing    int
	unresolved int
	byReason   map[string]int // unresolved broken down by reason (R3370)
	at         time.Time      // wall-clock time the counts were computed; zero = never
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

// IndexFileName is the bbolt database file inside the ark home directory.
// microfts2 owns the file (it holds both the `fts` and `ark` buckets); ark
// passes IndexPath(dbPath) — not the directory — to microfts2.Create/Open
// because bbolt opens a single file, not an LMDB-style directory.
// R28: ark stores everything in one directory — index.db here, ark.toml
// (ConfigPath), and the ark.sock unix socket all live under dbPath.
// CRC: crc-Store.md | R28, R2974, R2975
const IndexFileName = "index.db"

// IndexPath returns the bbolt database file path for an ark home directory.
func IndexPath(dbPath string) string { return filepath.Join(dbPath, IndexFileName) }

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
	// CRC: crc-DB.md | R34
	aliases := opts.Aliases
	if aliases == nil {
		aliases = make(map[byte]byte)
	}
	if _, ok := aliases['\n']; !ok {
		aliases['\n'] = '\x01'
	}

	// Initialize microfts2 (creates the bbolt database).
	// CRC: crc-DB.md | R3, R31, R33, R1911, R1912, R2978 — microfts2 owns the bbolt file and
	// its `fts` bucket; ark opens its `ark` bucket in the same DB. bbolt has no
	// MaxDBs/MapSize ceiling, so those Options fields are gone (R2978).
	// R31/R33: microfts2 is created with character set, case-insensitivity, and
	// aliases; these are fixed at Create and immutable thereafter.
	// R3630: on Windows two spellings of a path are one file, so the index
	// keys on a folded path and keeps the spelling given.
	ftsOpts := microfts2.Options{
		CaseInsensitive:          opts.CaseInsensitive,
		Aliases:                  aliases,
		CaseInsensitiveFilenames: foldFilenames,
	}
	fts, err := microfts2.Create(IndexPath(dbPath), ftsOpts)
	if err != nil {
		return fmt.Errorf("init microfts2: %w", err)
	}
	defer fts.Close()

	// Initialize ark subdatabase
	store, err := OpenStore(fts.DB())
	if err != nil {
		return fmt.Errorf("init ark store: %w", err)
	}

	// CRC: crc-DB.md | R382
	// Register default chunking strategies. Markdown and chat-jsonl
	// register as AppendAwareChunker structs so AppendChunks handles
	// boundary-spanning edits (markdown: paragraph extensions; chat-jsonl:
	// growing JSONL records) cleanly instead of falling through to full
	// reindex. (R2273)
	if err := fts.AddChunker("markdown", microfts2.MarkdownChunker{}); err != nil {
		return fmt.Errorf("register strategy markdown: %w", err)
	}
	if err := fts.AddChunker("chat-jsonl", JSONLChunker{}); err != nil {
		return fmt.Errorf("register strategy chat-jsonl: %w", err)
	}
	// R3172: bible — paragraph chunks carrying chapter/verse attributes.
	// Read-only (R3178), so it is not internal-tag wrapped. Unbound here:
	// InitDB creates the index and exits, so nothing is chunked in this
	// process and there is no book index to stage (R3214). Open binds a
	// DB-carrying instance for the passes that do index.
	if err := fts.AddChunker(bibleStrategy, &bibleChunker{}); err != nil {
		return fmt.Errorf("register strategy %s: %w", bibleStrategy, err)
	}
	if err := fts.AddStrategyFunc("lines", microfts2.LineChunkFunc); err != nil {
		return fmt.Errorf("register strategy lines: %w", err)
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

	// CRC: crc-DB.md | R127, R128, R129
	// Create starter tags.md from bundle seed if provided
	tagsPath := filepath.Join(dbPath, "tags.md")
	if len(opts.TagsSeed) > 0 {
		if _, err := os.Stat(tagsPath); os.IsNotExist(err) {
			if err := os.WriteFile(tagsPath, opts.TagsSeed, 0644); err != nil {
				return fmt.Errorf("write tags.md: %w", err)
			}
		}
	}

	// CRC: crc-DB.md | R1539, R1542
	// Write full config to I records. On rebuild cmdInit removes the index
	// file first, so this writes fresh ark.toml config into a db cleared of
	// all prior I and E records.
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

// Open opens the database at dbPath, blocking on the bbolt file lock
// until it is available. The server (sole index owner) uses this.
func Open(dbPath string) (*DB, error) {
	return OpenWithTimeout(dbPath, 0)
}

// OpenWithTimeout opens the database, bounding the bbolt lock wait by
// timeout (zero blocks forever, like Open). The CLI's local arm passes a
// non-zero timeout so a direct open fails fast (microfts2 R672 →
// bbolt.ErrTimeout) instead of hanging while a server holds the
// single-process index.
// CRC: crc-DB.md | R2994
func OpenWithTimeout(dbPath string, timeout time.Duration) (*DB, error) {
	injectPath(dbPath)

	// Load config
	configPath := filepath.Join(dbPath, "ark.toml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	config.dbPath = dbPath

	// Open microfts2 (opens the bbolt database with bbolt's exclusive
	// single-process file lock — R63).
	// CRC: crc-DB.md | R63, R2994
	fts, err := microfts2.Open(IndexPath(dbPath), microfts2.Options{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("open microfts2: %w", err)
	}

	// Open ark subdatabase
	store, err := OpenStore(fts.DB())
	if err != nil {
		fts.Close()
		return nil, fmt.Errorf("open ark store: %w", err)
	}
	tmpTags := NewTmpTagStore(store.TvidMap())
	store.SetTmpTagStore(tmpTags)

	matcher := &Matcher{Dotfiles: config.Dotfiles}

	// R1644, R1651: in-memory set of zero-byte files shared by Scanner
	// and the Indexer copies made via withFTS.
	emptyFiles := NewEmptyFiles()

	// One table, shared: the Scanner writes it on the actor and the Searcher
	// reads it at the filter door (R3359). Built here rather than inside
	// NewScanner so the sharing is visible at the assembly point — two
	// tables would mean the doors resolving against something the scan never
	// updated, which is the silent-disagreement failure R3359 names.
	scanner := NewScanner(config, matcher, fts, emptyFiles)
	db := &DB{
		fts:           fts,
		store:         store,
		config:        config,
		matcher:       matcher,
		scanner:       scanner,
		search:        &Searcher{fts: fts, store: store, config: config, links: scanner.Links()},
		dbPath:        dbPath,
		svc:           newChanSvc(chanSvcSlots),
		chunkerByName: make(map[string]any),
		scanSem:       make(chan struct{}, runtime.NumCPU()),
	}

	// CRC: crc-DB.md | R370, R382, R2273, R2386, R2389
	// Register built-in chunkers (must happen on every Open, not just
	// InitDB — chunker registrations aren't persisted in LMDB). Markdown
	// and chat-jsonl register as AppendAwareChunker structs (R2273) so
	// growing files index incrementally via microfts2's drop-and-replace
	// append protocol. addChunker mirrors each registration into
	// db.chunkerByName so ChunkInfo can look up ChunkerMetadata by strategy.
	if err := db.addChunker("markdown", wrapInternalTagChunker(microfts2.MarkdownChunker{}, stencilMarkdown)); err != nil {
		fts.Close()
		return nil, fmt.Errorf("register markdown strategy: %w", err)
	}
	if err := db.addChunker("chat-jsonl", JSONLChunker{}); err != nil {
		fts.Close()
		return nil, fmt.Errorf("register chat-jsonl strategy: %w", err)
	}
	// R3172: bible — paragraph chunks carrying chapter/verse attributes.
	// Not internal-tag wrapped: R3178 makes it read-only, so annotation
	// routes to the external disposition rather than into the file body.
	// Bound to db so FlushBookIndex can persist the staged book index (R3214).
	db.bibleChunker = newBibleChunker(db)
	if err := db.addChunker(bibleStrategy, db.bibleChunker); err != nil {
		fts.Close()
		return nil, fmt.Errorf("register %s strategy: %w", bibleStrategy, err)
	}
	if err := db.addStrategyFunc("lines", microfts2.LineChunkFunc); err != nil {
		fts.Close()
		return nil, fmt.Errorf("register lines strategy: %w", err)
	}

	// CRC: crc-DB.md | R624, R626, R627, R628, R629, R630, R636, R637
	// Register chunker strategies from ark.toml [[chunker]] entries
	db.registerConfigChunkers(config)

	// CRC: crc-PDFChunker.md | R1641, R1720, R1726
	// PDFChunker holds a reference to ark.DB so it can stage per-page
	// blobs during FileChunks and resolve path→fileid in GetChunk.
	db.pdfChunker = NewPDFChunker(db)
	if err := db.addChunker("pdf", db.pdfChunker); err != nil {
		log.Printf("warning: register pdf chunker: %v", err)
	}
	db.indexer = &Indexer{fts: fts, store: store, config: config, pdfChunker: db.pdfChunker, bibleChunker: db.bibleChunker}

	// R3217: every chunker is registered, so the per-source hook can run.
	// A failure here is a source misconfiguration (the bible chunker's
	// reserved-path collision, R3219) — report it and leave the source's
	// strategy unregistered rather than refusing to open the whole DB.
	if err := db.activateSourceChunkers(config); err != nil {
		log.Printf("ERROR: %v", err)
	}

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

	// CRC: crc-ExtMap.md | R1993
	db.extmap = NewExtMap()
	if err := db.extmap.Rebuild(db); err != nil {
		return nil, fmt.Errorf("rebuild ext map: %w", err)
	}
	db.indexer.extmap = db.extmap
	db.indexer.db = db
	store.SetExtMap(db.extmap)
	if store.tmp != nil {
		// Overlay-source @ext cleanup hook. (R2023)
		store.tmp.SetExtMap(db.extmap, db)
	}

	// R1887, R1888, R1889: wire bidirectional chunkID↔fileID resolvers.
	// Both run inside the caller's txn to avoid nested Views. The resolver
	// takes microfts2's *bbolt.Tx and reads C-records via fts.ReadCRecord(tx)
	// — the consumed bbolt API (R2977). Overlay-issued ids (high bit set)
	// route to TmpTagStore so the chunkid↔fileid mapping for tmp:// content
	// stays first-class.
	// CRC: crc-DB.md | R1948, R1950, R2977
	store.SetChunkResolver(
		func(txn *bbolt.Tx, chunkID uint64) []uint64 {
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

	// R3383: re-derive the link table from the persisted facts before anyone
	// can ask a door a question. Without this a process that never scanned —
	// every CLI command that finds no server — resolves against an empty
	// table while a server that happened to scan resolves correctly, and the
	// two disagree about whether a file exists (R3359).
	db.rebuildLinkTable()

	db.svc.start()
	return db, nil
}

// Do sends a fire-and-forget operation to the DB actor.
// Used by the watcher for file changes and reconcile.
// CRC: crc-DB.md | R987
func (db *DB) Do(fn func(*DB)) {
	svc(db.svc, func() { fn(db) })
}

// postCoalescedReconcile posts scan so that **at most one reconcile is active
// and one pending** (R346, R3351): a request arriving while one already waits
// merges into it instead of queueing a scan of its own. Merging loses nothing,
// because the waiting scan has not begun and will therefore observe everything
// each merged request was asking about — and it matters because a reconcile is a
// full pass over every source, so N requests during one run would otherwise buy
// N further full passes to reach a state one pass already reaches.
//
// The gate is a **separate, earlier closure** than the scan, and that is the
// mechanism rather than a style choice. The pending mark has to clear when the
// scan *begins* so a request arriving mid-scan queues a fresh one: a running
// scan cannot be relied on to have seen a change made after it walked past that
// part of the tree. Deciding in one closure and scanning in the next is what
// makes "clears at the start" expressible at all. That is the
// post-the-follow-up-behind-the-message idiom, and it is sound only because the
// actor delivers in order (R3344).
//
// A merged request's scan closure is **discarded**, so callers must pass work
// that is equivalent on every call — a reconcile is, by construction.
// CRC: crc-DB.md | R346, R3351
func (db *DB) postCoalescedReconcile(scan func(*DB)) {
	db.Do(func(db *DB) {
		if db.reconcilePending {
			return // one already waits; it will start after this request
		}
		db.reconcilePending = true
		db.Do(func(db *DB) {
			db.reconcilePending = false // cleared as the scan BEGINS
			scan(db)
		})
	})
}

// launchReconcileWalk runs the reconcile scan enumeration (walk) off the DB
// actor and applies its result back on the actor, coalescing so at most one walk
// is in flight. Must be called on the DB actor.
//
//   - If a walk is already in flight, it records the pending mark and returns
//     without launching a second walk (walk is NOT called).
//   - Otherwise it sets reconcileWalking, runs walk() in a plain goroutine (off
//     the actor, so the actor stays free to serve interleaving writes), then
//     posts apply() back on the actor. apply() runs before the flag clears; when
//     it returns the flag clears, write-idle is re-checked, and if a reconcile
//     arrived during the walk, relaunch() re-triggers exactly one.
//
// The off-actor property and the coalescing are both invisible when violated
// (the actor would merely be held, or a second walk would merely duplicate
// work), so the split into this primitive is what makes them testable with a
// blocking fake walk and a marker on a zero-value DB. R3433, R3434
// CRC: crc-DB.md | Seq: seq-reconcile.md#2.2 | R3433, R3434
func (db *DB) launchReconcileWalk(walk, apply, relaunch func()) {
	if db.reconcileWalking {
		db.reconcileWalkPending = true // one is in flight; re-run after it
		return
	}
	db.reconcileWalking = true
	go func() {
		walk() // off the actor — no write transaction, private read view
		SyncVoid(db, func(db *DB) error {
			apply()
			db.reconcileWalking = false
			db.checkWriteIdle() // R2989: the walk may have added nothing
			if db.reconcileWalkPending {
				db.reconcileWalkPending = false
				relaunch()
			}
			return nil
		})
	}()
}

// Sync sends an operation to the DB actor and blocks until it completes.
// Used by HTTP handlers and CLI for operations that return results. R988
// CRC: crc-DB.md | R989
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

// withFTS returns a shallow, read-only DB view bound to an alternate
// fts — typically fts.Copy() — for an off-actor read operation that must
// not touch the shared original's Go-side caches
// (pathCache/pathToID/frecordCache), which the write actor nils via
// InvalidateCaches. The copy carries its own lazily-loaded caches, so
// the operation's FileIDPaths / FileInfoByID / SearchFuzzy reads are
// isolated from the invalidate. The overlay pointer is shared — it has
// its own mutex and InvalidateCaches leaves it alone — so tmp://
// documents still resolve through the view. The ExtMap is shared for the
// same reason (own mutex, untouched by InvalidateCaches), so a read view
// resolves @ext routings without reaching back to the live DB (R3165).
// Shares store, config, and
// the chunker-backed helpers, and rebinds the Searcher to the same fts.
// The view carries no actor (svc nil) and no write queue: reads only.
// See specs/db-concurrency.md "Protected Resources".
// CRC: crc-DB.md | R995, R3163
func (db *DB) withFTS(fts *microfts2.DB) *DB {
	cp := &DB{
		fts:        fts,
		store:      db.store,
		config:     db.config,
		matcher:    db.matcher,
		pdfChunker: db.pdfChunker,
		dbPath:     db.dbPath,
		extmap:     db.extmap,
	}
	if db.search != nil {
		cp.search = db.search.withFTS(fts)
	}
	return cp
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R1053, R1067
// enqueueWrite appends a write closure to the write queue. If no write
// is in flight and the queue was empty, starts the write goroutine. The
// `!db.writing` guard is the one-at-a-time serialization (R1067): a new
// write goroutine starts only when none is running, so write closures run
// strictly serially — the contract finalizeConnectionsDoc's dedup relies on.
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
	fn := popFront(&db.writeQueue) // R3607
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
					} else {
						db.checkWriteIdle() // R2989
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
			} else {
				db.checkWriteIdle() // R2989
			}
		})
	}()
}

// writeIdle reports whether every enqueued write has committed AND every
// per-file scan has retired. The file-scan registry is included (R3420) because
// a rebuild's WaitWritesIdle must not return while off-slot compute is still in
// flight — those goroutines have not enqueued their store closures yet, so the
// write queue can be momentarily empty while indexing is unfinished.
// R3434: reconcileWalking is included because the reconcile scan enumeration
// now runs off the actor in a goroutine that has not yet dispatched its per-file
// scans — the write queue and registry can be momentarily empty while a walk is
// still enumerating, so a rebuild's WaitWritesIdle must wait for it too.
// CRC: crc-FileScanActor.md | R2989, R3420, R3434
func (db *DB) writeIdle() bool {
	return !db.writing && len(db.writeQueue) == 0 && len(db.fileScans) == 0 && !db.reconcileWalking
}

// checkWriteIdle signals write-idle waiters only when writeIdle() holds. Called
// from the write-completion path and from file-scan retirement.
func (db *DB) checkWriteIdle() {
	if db.writeIdle() {
		db.signalWriteIdle()
	}
}

// CRC: crc-DB.md | Seq: seq-rebuild-read-serve.md#2.6 | R2989
// signalWriteIdle closes and clears all write-idle waiter channels.
// Called from inside the actor when the write queue has just drained.
func (db *DB) signalWriteIdle() {
	for _, ch := range db.writeIdleWaiters {
		close(ch)
	}
	db.writeIdleWaiters = nil
}

// CRC: crc-DB.md | Seq: seq-rebuild-read-serve.md#2.6 | R2986, R2989
// WaitWritesIdle blocks until the write queue has drained — every
// enqueued write committed (!writing && empty queue). The completion
// signal a rebuild's read-only server waits on before exiting. If
// already idle it returns at once; otherwise it registers a one-shot
// waiter inside the actor that the write-completion path closes.
func (db *DB) WaitWritesIdle() {
	ch := make(chan struct{})
	SyncVoid(db, func(db *DB) error {
		if db.writeIdle() {
			close(ch)
			return nil
		}
		db.writeIdleWaiters = append(db.writeIdleWaiters, ch)
		return nil
	})
	<-ch
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
		db.svc.stop()
		db.svc = nil
	}
	// store doesn't need explicit close (shares env with fts)
	return db.fts.Close()
}

// addChunker registers c with microfts2 under name and mirrors it into
// db.chunkerByName so ChunkInfo / SuggestExtLocator can look up
// ChunkerMetadata by strategy name. microfts2's chunker map is private
// and exposes no accessor, so ark keeps its own parallel registry.
// microfts2 indexes content verbatim (the trigram index is full-text);
// ark strips tags only at embed time, so no content transform is
// registered here. CRC: crc-DB.md | R2386, R2389, R2913
func (db *DB) addChunker(name string, c any) error {
	if err := db.fts.AddChunker(name, c); err != nil {
		return err
	}
	db.chunkerByName[name] = c
	return nil
}

// addStrategyFunc registers a ChunkFunc as a strategy and mirrors a
// FuncChunker into db.chunkerByName. FuncChunker doesn't implement
// ChunkerMetadata, so the ChunkerMetadata defaults (writable=true,
// commentSyntax="") apply automatically. CRC: crc-DB.md | R2386, R2389
func (db *DB) addStrategyFunc(name string, fn microfts2.ChunkFunc) error {
	return db.addChunker(name, microfts2.FuncChunker{Fn: fn})
}

// registerConfigChunkers reads [[chunker]] entries from cfg and
// registers bracket/indent chunkers via db.addChunker.
// CRC: crc-DB.md | R624, R625, R626, R627, R628, R629, R630, R636, R637
func (db *DB) registerConfigChunkers(cfg *Config) {
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
			if err := db.addChunker(cc.Name, wrapInternalTagChunker(microfts2.BracketChunker(lang), stencilBracket)); err != nil {
				log.Printf("warning: register chunker %s: %v", cc.Name, err)
			}
		case isIndent:
			tabWidth := cc.TabWidth
			if tabWidth <= 0 {
				tabWidth = 4
			}
			if err := db.addChunker(cc.Name, wrapInternalTagChunker(microfts2.IndentChunker(lang, tabWidth), stencilIndent)); err != nil {
				log.Printf("warning: register chunker %s: %v", cc.Name, err)
			}
		default:
			log.Printf("warning: unknown chunker type %q for %s", cc.Type, cc.Name)
		}
	}
}

// SourceChunker is the optional interface a chunker implements when it needs
// per-source setup (R3217). It is called once per source that maps the chunker
// through a **per-source** strategy entry; a chunker reached only through the
// global strategy map is never called, because a global mapping has no
// particular source to set up for. `register` adds entries to the in-memory
// global strategy map; a returned error fails that source's load.
// CRC: crc-DB.md | R3217
type SourceChunker interface {
	ActivateForSource(src *Source, register func(pattern, strategy string) error) error
}

// activateSourceChunkers runs the per-source chunker hook at config-resolve —
// on every startup and on every reload, since nothing it establishes is
// persisted (R3217). Each source's own strategy entries name the chunkers that
// get the call; each chunker is called at most once per source even when the
// source maps it through several patterns.
// CRC: crc-DB.md | Seq: seq-bible-resolve.md#1.1 | R3217
func (db *DB) activateSourceChunkers(cfg *Config) error {
	// R3218: the pass re-derives the whole set, so it starts from empty.
	// ReloadConfig already hands over a freshly loaded Config, but clearing
	// here makes "re-derived, never accumulated" a property of the function
	// rather than of every caller.
	cfg.derivedStrategies = nil

	// declared[strategy] is every source that maps that strategy locally —
	// including any whose activation failed, since declaring a strategy is
	// what the reconcile below keys on, and a misconfigured source is no
	// reason to delete its persisted data.
	declared := make(map[string][]*Source)
	// failed[source.Dir] is the message for a source whose hook returned an
	// error, and becomes the E record payload below (R3219).
	failed := make(map[string]string)
	var errs []error
	for i := range cfg.Sources {
		src := &cfg.Sources[i]
		called := make(map[string]bool, len(src.Strategies))
		for _, strategy := range src.Strategies {
			if called[strategy] {
				continue
			}
			called[strategy] = true
			sc, ok := db.chunkerByName[strategy].(SourceChunker)
			if !ok {
				continue
			}
			declared[strategy] = append(declared[strategy], src)
			if err := sc.ActivateForSource(src, cfg.AddDerivedStrategy); err != nil {
				// One bad source must not stop the rest from activating. The
				// source keeps indexing; only its hook-registered addressing
				// is left unregistered, which is why R3219 makes the failure
				// loud and durable rather than trusting the log alone.
				errs = append(errs, fmt.Errorf("source %s: %s strategy: %w", src.Dir, strategy, err))
				failed[src.Dir] = fmt.Sprintf("%s strategy: %v", strategy, err)
			}
		}
	}

	// R3221: the tail — persisted per-source state is reconciled against the
	// config just resolved. Runs unconditionally, since an empty list is the
	// source-removed case and is exactly what needs sweeping.
	if db.bibleChunker != nil {
		if err := db.bibleChunker.ReconcileBookIndex(declared[bibleStrategy]); err != nil {
			errs = append(errs, fmt.Errorf("reconcile book index: %w", err))
		}
	}
	db.recordActivationFailures(failed)
	return errors.Join(errs...)
}

// recordActivationFailures keeps the `source_activation` E record in step with
// the config just resolved (R3219): written when any source's hook failed,
// deleted when none did.
//
// A failure here is otherwise silent — the source keeps indexing and only its
// virtual addresses stop resolving, which a user reads as a reference that
// returns nothing — so it needs a durable home beyond the log line.
// Re-deriving it per config load is what keeps the record honest: fixing the
// source and reloading clears it, with no dismissal step and no way for a
// stale condition to outlive the problem it described. That is R3223's rule,
// and the witness here is the source set itself — the condition is about
// intent, so comparing intent is the right test. A condition about a *built
// artifact* needs a stamp on that artifact instead; see R3223.
// CRC: crc-DB.md | R3219, R3223
func (db *DB) recordActivationFailures(failed map[string]string) {
	if db.store == nil {
		return
	}
	if len(failed) == 0 {
		db.store.DeleteERecord(ECondSourceActivation)
		return
	}
	if err := db.store.WriteERecord(ECondSourceActivation, failed); err != nil {
		log.Printf("warning: record %s: %v", ECondSourceActivation, err)
	}
}

// buildBracketLang converts a ChunkerConfig to a microfts2.BracketLang.
// Handles both easy form (flat pairs) and full form (struct defs).
// Strings and brackets unify into BracketGroup: strings get a non-nil
// AllowedInner (scan-restricted mode), brackets leave it nil (code mode).
// CRC: crc-DB.md | R2147, R2148, R2149, R2150
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
		for _, sd := range cc.StringDefs {
			lang.Brackets = append(lang.Brackets, microfts2.BracketGroup{
				Open:         []string{sd.Open},
				Close:        []string{sd.Close},
				Escape:       sd.Escape,
				AllowedInner: []string{},
			})
		}
		for _, bd := range cc.BracketDefs {
			grp := microfts2.BracketGroup{
				Open:          bd.Open,
				Separators:    bd.Separators,
				Close:         bd.Close,
				Escape:        bd.Escape,
				AllowedParent: bd.AllowedParent,
			}
			if bd.AllowedInner != nil {
				grp.AllowedInner = *bd.AllowedInner
				if grp.AllowedInner == nil {
					grp.AllowedInner = []string{}
				}
			}
			lang.Brackets = append(lang.Brackets, grp)
		}
	} else {
		for _, pair := range cc.Strings {
			if len(pair) == 2 {
				lang.Brackets = append(lang.Brackets, microfts2.BracketGroup{
					Open:         []string{pair[0]},
					Close:        []string{pair[1]},
					Escape:       `\`,
					AllowedInner: []string{},
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

// SetPubSub wires the centralized tmp:// publish path. After this is
// called, AddTmpFile / UpdateTmpFile / AppendTmpFile / RemoveTmpFile
// all signal the pubsub on tag changes. Also threads the back-pointer
// (ps.SetDB) so the file-tag pass can resolve path → fileID through
// PubSub.
// CRC: crc-DB.md | R2281, R2464
func (db *DB) SetPubSub(ps *PubSub) {
	db.pubsub = ps
	if ps != nil {
		ps.SetDB(db)
	}
}

// Config returns the current configuration.
func (db *DB) Config() *Config { return db.config }
func (db *DB) Store() *Store   { return db.store }

// ConfigPath returns the path to ark.toml.
// CRC: crc-DB.md | R24 — config file lives in the database directory
func (db *DB) ConfigPath() string { return filepath.Join(db.dbPath, "ark.toml") }

// SaveConfig writes the current config to disk and re-validates.
func (db *DB) SaveConfig() error { return db.config.SaveConfig(db.ConfigPath()) }

// ReloadConfig re-reads ark.toml from disk, diffs against stored I records,
// applies changes, and propagates to components.
// CRC: crc-DB.md | R1561, R1562, R1563, R1564, R1021
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

	// R3381: re-derive the track/propose predicate against the new sources.
	// The predicate is a test over (target, source config) with no index
	// lookup, so a config change is the whole trigger — which is what lets a
	// user add links and sources in either order and reach the same state.
	// Rebinding first is not optional: the table's cfg is the source
	// membership the predicate *is*, so rebuilding against the old one
	// classifies links correctly for a configuration nobody is running.
	// CRC: crc-DB.md, crc-LinkTable.md | R3381
	//
	// Rebuild is given the tracked links *and* the links that proposed the
	// symlink-reason candidates, which is what makes the promotion direction
	// work: a proposed link is not in the table at all (Observe records only
	// the tracked branch), so its identity lives solely in the U record it
	// wrote. Without those paths the reload could only demote, and "add the
	// link first, add the source later" — the case R3381 exists for — would
	// wait for a full rescan while looking like the proposal was ignored.
	links := db.scanner.Links()
	links.SetConfig(cfg)
	// The L records are the complete set of known directory symlinks
	// (R3383), tracked and proposed alike, so one list is Rebuild's whole
	// input. Before they existed this had to dig proposals out of the U
	// records, which only worked for proposals this process had seen.
	known, err := db.store.ListLinks()
	if err != nil {
		log.Printf("reload: list links: %v", err)
		known = links.Links()
	}
	candidates, _ := db.store.ListUnresolved()

	stillProposed := make(map[string]bool)
	for _, entry := range links.Rebuild(known) {
		stillProposed[entry.Path] = true
		// Still outside every source: re-record, so a candidate already
		// offered stays offered with its proposer intact.
		if err := db.store.AddUnresolvedRecord(UnresolvedRecord{
			Path: entry.Resolved, Dir: entry.Resolved,
			Reason: UnresolvedReasonSymlink, Symlink: entry.Path,
		}); err != nil {
			log.Printf("re-record symlink candidate %s: %v", entry.Resolved, err)
		}
	}
	// Withdraw the candidates whose links are no longer proposing. The test
	// is on the *link*, not the target: a promoted link still resolves to
	// exactly the same directory, so a liveness test asked of the target
	// would keep the record forever. Not-proposed covers both ways a
	// proposal ends — the target became a source, or the link went away.
	for _, rec := range candidates {
		if rec.Reason != UnresolvedReasonSymlink || rec.Symlink == "" {
			continue
		}
		if !stillProposed[rec.Symlink] {
			if err := db.store.RemoveUnresolved(rec.Path); err != nil {
				log.Printf("withdraw symlink candidate %s: %v", rec.Path, err)
			}
		}
	}

	// R3217: the fresh config carries no derived strategy entries — nothing
	// the hook establishes is persisted — so re-run it against the new
	// sources before anything resolves a path against them.
	if err := db.activateSourceChunkers(cfg); err != nil {
		log.Printf("ERROR: %v", err)
	}

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
// CRC: crc-DB.md | Seq: seq-file-change.md | R388, R392, R2133
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
		if db.matcher.Classify(includes, excludes, path, src.Dir, false) == Included {
			return true
		}
	}
	return false
}

// IsWatchableDir reports whether the live watcher should descend into and
// watch dir. It is the directory analog of IsIndexable: it applies the same
// directory-classification rule the Scanner uses to descend (Classify with
// isDir=true is not Excluded), so watch coverage equals scan coverage. Unlike
// IsIndexable it passes isDir=true (directory patterns) and accepts Unresolved
// as well as Included — the Scanner descends into Unresolved directories. With
// dotfiles=true a non-excluded dot-directory (e.g. .scratch/) is watchable
// while a directory excluded as a directory (e.g. .git/) is not.
// CRC: crc-DB.md | Seq: seq-file-change.md#1.1.1 | R2952
func (db *DB) IsWatchableDir(path string) bool {
	for _, src := range db.config.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		relPath, err := filepath.Rel(src.Dir, path)
		if err != nil || strings.HasPrefix(relPath, "..") {
			continue // path not under this source
		}
		includes, excludes := db.config.EffectivePatterns(src)
		if db.matcher.Classify(includes, excludes, path, src.Dir, true) != Excluded {
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
// via Store.UpdateTagValues. Overlay-source @ext routings are
// applied to ExtMap state (in-memory only — no LMDB writes).
// After the write completes, calls pubsub.PublishTmpDiff so
// subscribers see the new tag-set (prior set is empty for a fresh
// path → every present tag fires, R2285).
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | Seq: seq-ext-routing.md | Seq: seq-tmp-subscription.md | R663, R665, R666, R667, R668, R1948, R2012, R2016, R2281, R2285
func (db *DB) AddTmpFile(path, strategy string, content []byte) (uint64, error) {
	acc := &chunkAccumulator{}
	fid, err := db.fts.AddTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, err
	}
	db.tmpMu.Lock()
	if db.tmpPaths == nil {
		db.tmpPaths = make(map[string]uint64)
	}
	db.tmpPaths[path] = fid
	db.tmpMu.Unlock()
	stampFileID(acc.chunkTags, fid)
	if err := db.store.UpdateTagValues(acc.chunkTags); err != nil {
		return fid, fmt.Errorf("write tmp tags: %w", err)
	}
	if db.indexer != nil {
		if err := db.indexer.runOverlayExtRouting(fid, acc.chunkTags); err != nil {
			return fid, fmt.Errorf("overlay ext routing: %w", err)
		}
	}
	if db.pubsub != nil {
		db.pubsub.PublishTmpDiff("", path, content, strategy)
	}
	return fid, nil
}

// UpdateTmpFile replaces content of an existing tmp:// document and
// re-extracts its tag values into the in-memory tag overlay. Overlay
// @ext cleanup runs as a side effect of TmpTagStore's removal hook;
// new routings apply via runOverlayExtRouting. After the write
// completes, calls pubsub.PublishTmpDiff so subscribers see only the
// (tag, value) pairs that changed since the prior write (R2284).
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | Seq: seq-ext-routing.md | Seq: seq-tmp-subscription.md | R666, R1948, R2012, R2016, R2023, R2281, R2284
func (db *DB) UpdateTmpFile(path, strategy string, content []byte) error {
	acc := &chunkAccumulator{}
	if err := db.fts.UpdateTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback)); err != nil {
		return err
	}
	db.tmpMu.RLock()
	fid, ok := db.tmpPaths[path]
	db.tmpMu.RUnlock()
	if !ok {
		return nil
	}
	// UpdateTmpFile drops the file's prior overlay chunks; clear our
	// tag overlay first so UpdateTagValues writes a clean replacement.
	// RemoveFileTagValues → TmpTagStore.RemoveFile drives @ext cleanup.
	db.store.RemoveFileTagValues(fid)
	stampFileID(acc.chunkTags, fid)
	if err := db.store.UpdateTagValues(acc.chunkTags); err != nil {
		return fmt.Errorf("write tmp tags: %w", err)
	}
	if db.indexer != nil {
		if err := db.indexer.runOverlayExtRouting(fid, acc.chunkTags); err != nil {
			return fmt.Errorf("overlay ext routing: %w", err)
		}
	}
	if db.pubsub != nil {
		db.pubsub.PublishTmpDiff("", path, content, strategy)
	}
	return nil
}

// AppendTmpFile appends content to a tmp:// document, creating it if needed.
// Newly emitted chunks have their tag values added to the overlay via
// Store.AppendTagValues; existing chunks are untouched. Overlay-source
// @ext routings on the new chunks are applied via runOverlayExtRouting.
// After the write completes, calls pubsub.PublishTmpAppend so newly
// introduced (tag, value) pairs fire while tags already published
// from prior content stay quiet (R2286).
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | Seq: seq-ext-routing.md | Seq: seq-tmp-subscription.md | R1948, R2012, R2016, R2281, R2286
func (db *DB) AppendTmpFile(path, strategy string, content []byte) (uint64, error) {
	acc := &chunkAccumulator{}
	fid, err := db.fts.AppendTmpFile(path, strategy, content, microfts2.WithIndexedChunkCallback(acc.indexedCallback))
	if err != nil {
		return 0, err
	}
	db.tmpMu.Lock()
	if db.tmpPaths == nil {
		db.tmpPaths = make(map[string]uint64)
	}
	db.tmpPaths[path] = fid
	db.tmpMu.Unlock()
	stampFileID(acc.chunkTags, fid)
	if err := db.store.AppendTagValues(acc.chunkTags); err != nil {
		return fid, fmt.Errorf("write tmp tags: %w", err)
	}
	if db.indexer != nil {
		if err := db.indexer.runOverlayExtRouting(fid, acc.chunkTags); err != nil {
			return fid, fmt.Errorf("overlay ext routing: %w", err)
		}
	}
	if db.pubsub != nil {
		db.pubsub.PublishTmpAppend("", path, content, strategy)
	}
	return fid, nil
}

// RemoveTmpFile drops a tmp:// document from the overlay. Tag entries
// are cleared first so the trigram and tag overlays never disagree
// on which fileids exist. Clears the pubsub tag-set cache so the
// next AddTmpFile on the same path treats it as new (R2287).
// CRC: crc-DB.md | Seq: seq-tmp-tag-overlay.md | Seq: seq-tmp-subscription.md | R666, R1944, R2287
func (db *DB) RemoveTmpFile(path string) error {
	db.tmpMu.RLock()
	fid, ok := db.tmpPaths[path]
	db.tmpMu.RUnlock()
	if ok {
		db.store.RemoveFileTagValues(fid)
	}
	if err := db.fts.RemoveTmpFile(path); err != nil {
		return err
	}
	db.tmpMu.Lock()
	delete(db.tmpPaths, path)
	db.tmpMu.Unlock()
	if db.pubsub != nil {
		db.pubsub.ClearTagSetCache(path)
	}
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

// hasTmpPath returns true if path is registered in the in-memory
// tmp:// tracking map. Locked.
func (db *DB) hasTmpPath(path string) bool {
	db.tmpMu.RLock()
	defer db.tmpMu.RUnlock()
	_, ok := db.tmpPaths[path]
	return ok
}

// TmpFiles returns all tmp:// paths.
// CRC: crc-DB.md | R664
func (db *DB) TmpFiles() []string {
	db.tmpMu.RLock()
	defer db.tmpMu.RUnlock()
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

// ErrFileOutsideSource is returned by Add when a single file with no
// explicit strategy lies outside every configured source — there is no
// source to resolve a chunking strategy from. handleAdd maps it to a
// 400 client error: the caller must pass --strategy. (R2955)
var ErrFileOutsideSource = errors.New("file is outside every configured source; pass --strategy")

// Add indexes files. If path is a directory, walks per config.
// If a file, adds directly with the given strategy.
//
// CRC: crc-DB.md | Seq: seq-add.md | R2954, R2955
// CRC: crc-DB.md | R35 — Add walks source directories per config (addDirectory
// applies the include/exclude classification during the walk)
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
			fileStrategy, err := db.resolveAddStrategy(path, strategy)
			if err != nil {
				return err
			}
			if _, err := db.indexer.AddFile(path, fileStrategy); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveAddStrategy picks the chunking strategy for an explicit single-file
// Add. An explicit strategy is used as-is. An empty strategy is resolved from
// the file's enclosing source the way the watcher does (findSourceForPath +
// Config.StrategyForFile, default "lines"); a file outside every source has
// nothing to resolve against and yields ErrFileOutsideSource.
//
// CRC: crc-DB.md | Seq: seq-add.md | R2954, R2955
func (db *DB) resolveAddStrategy(path, strategy string) (string, error) {
	if strategy != "" {
		return strategy, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	src, rel, ok := db.findSourceForPath(abs)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrFileOutsideSource, path)
	}
	return db.config.StrategyForFile(rel, src.Strategies), nil
}

func (db *DB) addDirectory(dir string) error {
	results, err := db.scanner.Scan()
	if err != nil {
		return err
	}
	evictEmpty(db.fts, results.EmptyFiles) // R1648
	db.recordScannedLinks(results)         // R3383

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
		if err := db.store.AddUnresolvedRecord(u); err != nil {
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
// CRC: crc-DB.md | R41 — Remove takes paths or glob patterns and removes
// matched files from both engines (Indexer.RemoveFile drops microfts2 + store)
func (db *DB) Remove(patterns []string) error {
	// Get all indexed files to match patterns against
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	for _, s := range statuses {
		for _, pat := range patterns {
			if db.matcher.Match(pat, s.Path, "", false) {
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
	db.recordScannedLinks(results)         // R3383

	for _, f := range results.NewFiles {
		if _, err := db.indexer.AddFile(f.Path, f.Strategy); err != nil {
			// R3358: a file the patterns admitted, handed to a chunker, that
			// yielded nothing. That is a statement about ark's current
			// chunkers rather than about the file — so it is *recorded*, not
			// dropped, and adding a chunker for that kind of file revives
			// every such candidate with no user action. Empty files are a
			// different thing and are already handled by EmptyFiles (R1648),
			// so this reaches only files with content nothing could chunk.
			// CRC: crc-DB.md | R3358
			if errors.Is(err, microfts2.ErrNoChunks) {
				results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
					Path:   f.Path,
					Dir:    f.Path,
					Reason: UnresolvedReasonUnindexable,
				})
				continue
			}
			if errors.Is(err, microfts2.ErrAlreadyIndexed) {
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("scan: skipping %s: %v", f.Path, err)
				continue
			}
			return results, fmt.Errorf("add %s: %w", f.Path, err)
		}
		// The file indexed, so any candidate recorded for it — an
		// `unindexable` from a run before its chunker existed — is stale.
		// Nothing else drops one: CleanUnresolved tests whether the *path*
		// still exists, and an indexable file's path exists.
		// CRC: crc-DB.md | R3358
		if err := db.store.RemoveUnresolved(f.Path); err != nil {
			return results, fmt.Errorf("clear unresolved %s: %w", f.Path, err)
		}
	}
	for _, u := range results.NewUnresolved {
		if err := db.store.AddUnresolvedRecord(u); err != nil {
			return results, fmt.Errorf("track unresolved %s: %w", u.Path, err)
		}
	}
	if err := db.store.CleanUnresolved(); err != nil {
		return results, err
	}
	return results, nil
}

// Refresh re-indexes stale files, optionally scoped by patterns.
// CRC: crc-DB.md | R43, R45 — Refresh re-indexes stale files via microfts2
// staleness (modtime + content hash, in RefreshStale); vanished files are not
// auto-deleted but added to the missing list for review (R45).
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

// ForceRefresh re-chunks every indexed file the patterns match, stale or not,
// in place (R3568). It runs on the actor only to list and dispatch: each file is
// its own forced scan, chunked off the actor and stored on it (R3419, R3433), so
// the server stays responsive however many files match. Returns how many were
// queued. It refuses without a pattern (R3569).
// CRC: crc-DB.md | R3568, R3569
func (db *DB) ForceRefresh(patterns []string) (int, error) {
	if len(patterns) == 0 {
		return 0, errForceRefreshNeedsPattern
	}
	paths, err := db.fts.FileIDPaths()
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, p := range paths {
		if strings.HasPrefix(p, "tmp://") {
			continue
		}
		for _, pat := range patterns {
			if db.matcher.Match(pat, p, "", false) {
				db.startFileScanWith(db.indexer, p, true)
				queued++
				break
			}
		}
	}
	return queued, nil
}

// errForceRefreshNeedsPattern is ForceRefresh's refusal. R3569
var errForceRefreshNeedsPattern = fmt.Errorf("refresh --force needs a pattern: forcing the whole index is rarely meant (use /** to ask for it on purpose)")

// SweepResult lists paths removed during a sweep pass.
type SweepResult struct {
	Removed []string
}

// Sweep walks every indexed file and removes any that no longer
// classify as Included against current config. Files whose claiming
// source has been removed are also dropped. Removal goes through the
// canonical Indexer.RemoveFile path so chunks/tag values/ext routings
// drop consistently.
//
// Sync version — used by tests and cold-start CLI. The async wrapper
// (SweepAsync) routes through the write queue.
//
// CRC: crc-DB.md | Seq: seq-reconcile.md | R2138, R2139, R2140, R2141
func (db *DB) Sweep() (*SweepResult, error) {
	return sweepWith(db.fts, db.indexer, db.matcher, db.config)
}

// CRC: crc-DB.md | Seq: seq-write-actor.md | R2138, R2141
// SweepAsync queues the sweep through the write goroutine so removals
// serialize behind any in-flight write, mirroring RefreshAsync.
func (db *DB) SweepAsync() error {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(statuses) == 0 {
		return nil
	}
	cfg := db.config
	matcher := db.matcher
	db.enqueueWrite(func(ftsCopy *microfts2.DB) {
		idx := db.indexer.withFTS(ftsCopy)
		for _, s := range statuses {
			if shouldSweep(s.Path, matcher, cfg) {
				if err := idx.RemoveFile(s.Path); err != nil {
					log.Printf("sweep: remove %s: %v", s.Path, err)
				}
			}
		}
	})
	return nil
}

// sweepWith is the shared sweep body, parameterized by fts/indexer/etc.
// so sync and async paths can share logic.
// CRC: crc-DB.md | R2139, R2140, R2141
func sweepWith(fts *microfts2.DB, idx *Indexer, matcher *Matcher, cfg *Config) (*SweepResult, error) {
	statuses, err := fts.StaleFiles()
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	result := &SweepResult{}
	for _, s := range statuses {
		if !shouldSweep(s.Path, matcher, cfg) {
			continue
		}
		if err := idx.RemoveFile(s.Path); err != nil {
			log.Printf("sweep: remove %s: %v", s.Path, err)
			continue
		}
		result.Removed = append(result.Removed, s.Path)
	}
	return result, nil
}

// shouldSweep returns true when the path should be removed from the
// index — either no source claims it (R2140) or its claiming source
// classifies it as not-Included (R2139).
// CRC: crc-DB.md | R2139, R2140
func shouldSweep(path string, matcher *Matcher, cfg *Config) bool {
	for _, src := range cfg.Sources {
		if IsGlob(src.Dir) {
			continue
		}
		rel, err := filepath.Rel(src.Dir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		// Path is under this source. Re-classify.
		includes, excludes := cfg.EffectivePatterns(src)
		return matcher.Classify(includes, excludes, path, src.Dir, false) != Included
	}
	// No source claims it — sweep.
	return true
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
	// R3383: record the links this walk met. Missing here for as long as the
	// async path has existed, and invisible the whole time — this is the path
	// the *server* takes (doReconcile, runRebuildScan), so every startup and
	// every reconcile walked past every symlink and persisted none of them,
	// while the synchronous Scan() beside it recorded them correctly. Two
	// paths, one behaviour, and the divergence surfaced only as a cold CLI
	// resolving aliases differently from a running server — which is the exact
	// failure R3359 exists to prevent (gap O208).
	//
	// Ahead of the unresolved loop deliberately: that loop returns early on a
	// write error, and losing the link records as collateral damage to an
	// unrelated failure is how a partial scan becomes a silently wrong table.
	db.recordScannedLinks(results)

	// Handle unresolved synchronously (small metadata writes)
	for _, u := range results.NewUnresolved {
		if err := db.store.AddUnresolvedRecord(u); err != nil {
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

	// R1648: evictions are a short metadata write — queue them on the write
	// actor. R3419: each new content file becomes its own per-file scan, so its
	// chunk+trigram compute runs off the write slot.
	if empties := results.EmptyFiles; len(empties) > 0 {
		db.enqueueWrite(func(ftsCopy *microfts2.DB) {
			evictEmpty(ftsCopy, empties)
		})
	}
	for _, f := range contentFiles {
		db.startFileScan(db.indexer, f.Path)
	}
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

	// R3435: record what this reconcile computed (the stale partition, plus the
	// M/U record counts) so `ark status` reports staleness off the actor with an
	// as-of time and never re-walks the corpus. Runs before the early return so
	// a reconcile that finds nothing stale still stamps a fresh, zeroed count.
	db.captureReconcileStats(len(stale))

	if len(stale) == 0 {
		return nil
	}

	// R3419: each stale file is its own scan — compute off-slot, store on-slot.
	for _, s := range stale {
		db.startFileScan(db.indexer, DiskSpelling(s.Path)) // R3631: stored spelling in, disk spelling out
	}
	return nil
}

// captureReconcileStats records, on the DB actor, the counts the just-finished
// reconcile computed, so `ark status` can report stale/missing/unresolved off
// the actor with an as-of time — never re-walking the corpus (R3435). staleCount
// is the reconcile's stale-file partition; missing and unresolved come from the
// M/U record scans (cheap bbolt reads, no file I/O), matching what Status
// reported when it read them live. On the DB actor.
// CRC: crc-DB.md | Seq: seq-reconcile.md#2.3 | R3435
func (db *DB) captureReconcileStats(staleCount int) {
	rc := reconcileCounts{stale: staleCount, at: timeNow()}
	if missing, err := db.store.ListMissing(); err == nil {
		rc.missing = len(missing)
	}
	if unresolved, err := db.store.ListUnresolved(); err == nil {
		rc.unresolved = len(unresolved)
		rc.byReason = unresolvedReasonCounts(unresolved)
	}
	db.reconcileStats = rc
}

// unresolvedReasonCounts breaks an unresolved-record list down by reason for the
// status line (R3370). A record written before reasons existed counts as
// `uncovered`, which is what it was.
// CRC: crc-DB.md | R3370
func unresolvedReasonCounts(unresolved []UnresolvedRecord) map[string]int {
	byReason := make(map[string]int, 3)
	for _, u := range unresolved {
		reason := u.Reason
		if reason == "" {
			reason = UnresolvedReasonUncovered
		}
		byReason[reason]++
	}
	return byReason
}

// applyReconcileScan applies an off-actor reconcile walk's results back on the
// DB actor: the deferred directory-symlink Observe, the batched metadata writes,
// the synchronous config-file adds, the empty-file evictions, and the per-file
// scan dispatches. It mirrors ScanAsync's post-walk body — the difference is only
// that the walk (and its per-file classification) already ran off the actor. On
// the DB actor. R3433
// CRC: crc-DB.md | Seq: seq-reconcile.md#2.3 | R3383, R3433
func (db *DB) applyReconcileScan(results *ScanResults) {
	// Directory-symlink classification was deferred off the actor (the LinkTable
	// is actor-owned); run Observe on the collected paths here, in walk order,
	// exactly as the inline walk did. R3433
	for _, linkPath := range results.PendingLinks {
		db.observeScannedLink(results, linkPath)
	}

	// R3383: record the links this walk met, deliberately AHEAD of the unresolved
	// loop — that loop can fail mid-way, and losing the link records as collateral
	// damage to an unrelated failure is how a partial scan becomes a silently wrong
	// table. (Here the loop logs and continues rather than returning, since the
	// reconcile walk is fire-and-forget; the ordering is preserved regardless.)
	db.recordScannedLinks(results)

	for _, u := range results.NewUnresolved {
		if err := db.store.AddUnresolvedRecord(u); err != nil {
			log.Printf("reconcile: track unresolved %s: %v", u.Path, err)
		}
	}
	if err := db.store.CleanUnresolved(); err != nil {
		log.Printf("reconcile: clean unresolved: %v", err)
	}

	// R1052: config files indexed synchronously in the actor; content files become
	// per-file scans.
	var contentFiles []FileEntry
	for _, f := range results.NewFiles {
		if filepath.Base(f.Path) == "ark.toml" {
			if _, err := db.indexer.AddFile(f.Path, f.Strategy); err != nil {
				if !errors.Is(err, microfts2.ErrNoChunks) && !errors.Is(err, microfts2.ErrAlreadyIndexed) && !errors.Is(err, os.ErrNotExist) {
					log.Printf("reconcile: config add %s: %v", f.Path, err)
				}
			}
		} else {
			contentFiles = append(contentFiles, f)
		}
	}

	// R1648: evictions are a short metadata write. R3419: each new content file
	// becomes its own per-file scan.
	if empties := results.EmptyFiles; len(empties) > 0 {
		db.enqueueWrite(func(ftsCopy *microfts2.DB) {
			evictEmpty(ftsCopy, empties)
		})
	}
	for _, f := range contentFiles {
		db.startFileScan(db.indexer, f.Path)
	}
}

// observeScannedLink runs the deferred directory-symlink classification for one
// path on the shared, actor-owned LinkTable, appending to results.TrackedLinks or
// results.NewUnresolved exactly as the inline walk did before the enumeration
// moved off the actor. On the DB actor. R3433
// CRC: crc-DB.md | R3352, R3355, R3357, R3433
func (db *DB) observeScannedLink(results *ScanResults, path string) {
	if linksToRegularFile(path) {
		return // a link to a regular file keeps today's handling
	}
	entry, status := db.scanner.Links().Observe(path)
	switch status {
	case LinkTracked:
		results.TrackedLinks = append(results.TrackedLinks, entry)
	case LinkProposed:
		// The link proposes its target; the candidate is keyed on the target, so
		// it needs the source the link lives in to say where the proposal came
		// from. R3355, R3357
		src, _, ok := findSourceIn(db.config, entry.Path)
		if !ok {
			return
		}
		results.NewUnresolved = append(results.NewUnresolved, UnresolvedRecord{
			Path:    entry.Resolved,
			Dir:     src.Dir,
			Reason:  UnresolvedReasonSymlink,
			Symlink: entry.Path,
		})
	}
}

// IndexPathsAsync schedules per-path index updates through the per-file scan
// actor. Each path's expensive chunk+trigram compute runs off the write slot;
// the slot runs only the store step. Must be called from inside the actor.
//
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.1 | R991, R3419
func (db *DB) IndexPathsAsync(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		// Settle each path's link status here, on the actor that owns the table,
		// before its scan is dispatched. Order matters as well as goroutine: a
		// link replaced by an indexable file must stop being an alias prefix
		// before its replacement is indexed, or the two answers disagree. R3390
		db.SyncLinkPath(path)
		db.startFileScan(db.indexer, path)
	}
	return nil
}

// fileScan is one path's in-flight scan state in the write actor's registry. It
// carries the changeNum stamp (R3421) and, since indexing-latency #131 Item 1b,
// a per-path chunk-hash → grams reuse map (R3429). The map survives across
// superseded scans of the path so a rapidly-edited file's finished per-chunk
// grams are reused instead of recomputed. Both fields are DB-actor state (no
// mutex); the off-slot compute reaches the map only through reuseLookup/reuseKeep.
//
// The map needs no runtime invalidation: grams are a pure function of content and
// the trigram config, and ark's trigram config (CaseInsensitive + Aliases) is
// fixed at microfts2.Create and immutable for the process (a case-fold change is
// ActionDefer → restart), so a cached gram can never observe a config change. A
// chunker change alters boundaries and thus hashes → reuse misses, not stale hits.
// R3432
// CRC: crc-FileScanActor.md | R3420, R3421, R3429, R3432
type fileScan struct {
	changeNum uint64
	reuse     map[[32]byte]*microfts2.PreparedChunk // R3429: nil until first deposit
}

// reuseResult is one reuse-map consultation on the DB actor: superseded reports
// the scan lost to a newer event (abandon between chunks); cached is reusable
// grams for the hash, nil on a miss.
type reuseResult struct {
	superseded bool
	cached     *microfts2.PreparedChunk
}

// reuseLookup consults path's per-chunk reuse map on the DB actor. It reports
// supersession (a newer event bumped the entry's changeNum past my, or the entry
// was retired) so the off-slot loop abandons between chunks, and otherwise returns
// the cached grams for hash (nil on a miss). R3429, R3430
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.5.3.2 | R3429, R3430
func (db *DB) reuseLookup(path string, my uint64, hash [32]byte) reuseResult {
	fs := db.fileScans[path]
	if fs == nil || fs.changeNum != my {
		return reuseResult{superseded: true}
	}
	if fs.reuse == nil {
		return reuseResult{}
	}
	return reuseResult{cached: fs.reuse[hash]}
}

// reuseKeep caches freshly computed grams under hash for path's scan on the DB
// actor. It deposits whenever the entry still exists — even for a scan whose
// changeNum has since been bumped — because grams are content-pure, so the
// deposit is valid for the newer scan and is what makes a rapidly-edited file
// converge instead of starving. R3431
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.5.3.4 | R3429, R3431
func (db *DB) reuseKeep(path string, hash [32]byte, pc *microfts2.PreparedChunk) {
	fs := db.fileScans[path]
	if fs == nil {
		return
	}
	if fs.reuse == nil {
		fs.reuse = make(map[[32]byte]*microfts2.PreparedChunk)
	}
	fs.reuse[hash] = pc
}

// dbScanGate is the production scanGate: its methods hop onto the DB actor to
// consult and update the path's reuse map, so the map stays actor-owned (no
// mutex) while the expensive PrepareChunk runs off-actor between hops. Those hops
// are also buildEntries' changeNum preemption checkpoints.
// CRC: crc-FileScanActor.md | R3429, R3430, R3431
type dbScanGate struct {
	db   *DB
	path string
	my   uint64
}

func (g dbScanGate) reuse(hash [32]byte) (bool, *microfts2.PreparedChunk) {
	res, _ := Sync(g.db, func(db *DB) (reuseResult, error) {
		return db.reuseLookup(g.path, g.my, hash), nil
	})
	return res.superseded, res.cached
}

func (g dbScanGate) keep(hash [32]byte, pc *microfts2.PreparedChunk) {
	_ = SyncVoid(g.db, func(db *DB) error {
		db.reuseKeep(g.path, hash, pc)
		return nil
	})
}

// storeOutcome is what to do after a store attempt.
type storeOutcome struct {
	retire bool
	reEmit bool
	force  bool // R3599: re-emit as a forced scan, which never appends
}

// storeResultAction decides the post-store action: always retire the entry;
// re-emit one bounded change event only on failure (never an inline reindex).
// A pure function so the "re-scan on failure, exactly once" property is testable
// without a live store. An append refused because the file was chunked under
// another chunker version re-emits a forced scan: a plain one would prepare the
// same append and meet the same refusal on every retry. R3425, R3599
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.10.2 | R3425, R3599
func storeResultAction(err error) storeOutcome {
	return storeOutcome{
		retire: true,
		reEmit: err != nil,
		force:  errors.Is(err, microfts2.ErrChunkerVersionStale),
	}
}

// stampFileScan looks up or creates path's registry entry and stamps it with the
// next changeNum from the global monotonic counter. On the DB actor. The counter
// is never reset per entry, so a stale compute from a retired-and-recreated
// entry can never match a fresh stamp (ABA guard). R3421
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.2 | R3420, R3421
func (db *DB) stampFileScan(path string) uint64 {
	if db.fileScans == nil {
		db.fileScans = make(map[string]*fileScan)
	}
	db.scanSeq++
	fs := db.fileScans[path]
	if fs == nil {
		fs = &fileScan{}
		db.fileScans[path] = fs
	}
	fs.changeNum = db.scanSeq
	return db.scanSeq
}

// fileScanCurrent reports whether path's entry still carries stamp my — i.e. no
// newer event superseded it and it has not been retired. On the DB actor. R3422
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.7 | R3422
func (db *DB) fileScanCurrent(path string, my uint64) bool {
	fs := db.fileScans[path]
	return fs != nil && fs.changeNum == my
}

// retireFileScanIfCurrent removes path's entry only if it still carries stamp my
// — a newer scan keeps its entry. On the DB actor, which closes the
// retire-vs-new-event race. Signals write-idle when the registry empties. R3424
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.10 | R3424
func (db *DB) retireFileScanIfCurrent(path string, my uint64) {
	if fs := db.fileScans[path]; fs != nil && fs.changeNum == my {
		delete(db.fileScans, path)
		db.checkWriteIdle()
		if len(db.fileScans) == 0 {
			db.releaseAfterScanBurst()
		}
	}
}

// releaseAfterScanBurst releases memory once no scan is in flight and enough
// has been chunked since the last release. The release runs on a goroutine so
// the actor never waits on the collection. On the DB actor.
// CRC: crc-DB.md | Seq: seq-file-scan-actor.md#1.10.3 | R3602
func (db *DB) releaseAfterScanBurst() {
	if db.scanBytesSinceRelease < releaseAfterScanBytes {
		return
	}
	db.scanBytesSinceRelease = 0
	go releaseMemory("file scans")
}

// runScanBounded runs fn under the per-file dispatch semaphore, so at most
// cap(scanSem) preparations execute at once. Always called from a goroutine the
// DB actor spawned, never on the actor itself: the spawn returns immediately and
// fn blocks here for a slot, so the bound never blocks the actor. Realizes R519's
// worker-count bound under the per-file scan-actor model. A nil scanSem (a
// zero-value *DB) runs unbounded, which only a test constructs.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.2.2 | R3438
//
// R3612: fn receives the slot's release and must see it called when the scan is
// over, which for a stored scan is after its store, not after preparing. The
// release is once-only, so every exit path can call it safely.
func (db *DB) runScanBounded(fn func(release func())) {
	release := func() {}
	if db.scanSem != nil {
		db.scanSem <- struct{}{}
		var once sync.Once
		release = func() { once.Do(func() { <-db.scanSem }) }
	}
	fn(release)
}

// startFileScan stamps path's registry entry with the next changeNum and
// launches its off-slot scan. Only the stamp here and the registry touches the
// off-slot goroutine posts back run on the DB actor; the per-file os.Stat +
// CheckFile + classification move off-slot into prepareFileScan (R3433). Config
// files (ark.toml) index fully synchronously on the actor, unchanged — they are
// small and must complete before dependent writes. On the DB actor.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.2 | R3419, R3421, R3433
func (db *DB) startFileScan(idx *Indexer, path string) {
	db.startFileScanWith(idx, path, false)
}

// startFileScanWith is startFileScan, optionally forced: a forced scan re-chunks
// the whole file in place even when it is unchanged or has only grown (R3568).
func (db *DB) startFileScanWith(idx *Indexer, path string, force bool) {
	my := db.stampFileScan(path)

	// Config files stay fully synchronous on the actor (R1052) — dependent content
	// writes must see the chunker registered first. Their os.Stat + CheckFile stay
	// on the actor (rare, cheap). R3433
	if filepath.Base(path) == "ark.toml" {
		db.scanConfigOnActor(idx, path, my)
		return
	}

	// Content file: everything — os.Stat, CheckFile (on a private copy),
	// classification, and the chunk+gram compute — runs off the slot. The Config
	// captured here on the actor is the snapshot the goroutine classifies against
	// (Config is replaced by pointer swap, never mutated in place). R3433
	cfg := db.config
	prepIdx := idx.withFTS(db.fts.Copy())
	// Bound the dispatch: the actor's spawn returns immediately, and the
	// goroutine acquires scanSem before the heavy prepareScan, so at most
	// runtime.NumCPU() preparations run at once and the actor never blocks. R3438
	go db.runScanBounded(func(release func()) {
		db.prepareFileScan(prepIdx, cfg, path, my, force, release)
	})
}

// scanConfigOnActor indexes a config file (ark.toml) synchronously on the DB
// actor. A dependent content write must see the chunker registered first
// (R1052), so a config file keeps its full on-actor path — os.Stat, CheckFile,
// classification, and add/refresh/remove — rather than moving off-slot. On the
// DB actor. R1052, R3433
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.2.1 | R1052, R3433
func (db *DB) scanConfigOnActor(idx *Indexer, path string, my uint64) {
	fi, statErr := os.Stat(path)
	status, checkErr := db.fts.CheckFile(path)
	known := checkErr == nil && status.FileID != 0

	if statErr != nil {
		if known {
			db.enqueueWrite(func(ftsCopy *microfts2.DB) {
				if err := idx.withFTS(ftsCopy).RemoveFile(path); err != nil {
					log.Printf("watch: remove %s: %v", path, err)
				}
				svc(db.svc, func() { db.retireFileScanIfCurrent(path, my) })
			})
		} else {
			db.retireFileScanIfCurrent(path, my)
		}
		return
	}
	if fi.IsDir() {
		db.retireFileScanIfCurrent(path, my)
		return
	}
	src, rel, ok := db.findSourceForPath(path)
	if !ok {
		db.retireFileScanIfCurrent(path, my)
		return
	}
	strategy := db.config.StrategyForFile(rel, src.Strategies)
	if known {
		if err := idx.RefreshFile(path, strategy); err != nil {
			log.Printf("scan: config refresh %s: %v", path, err)
		}
	} else if _, err := idx.AddFile(path, strategy); err != nil {
		if !errors.Is(err, microfts2.ErrNoChunks) && !errors.Is(err, microfts2.ErrAlreadyIndexed) && !errors.Is(err, os.ErrNotExist) {
			log.Printf("scan: config add %s: %v", path, err)
		}
	}
	db.retireFileScanIfCurrent(path, my)
}

// prepareFileScan classifies a content file and runs its index compute off the
// write slot (no write transaction), then posts the registry touches (delete
// enqueue, retire, or the store via finishFileScan) back through the DB actor.
// The os.Stat + CheckFile (on the private copy) + deleted/dir/outside-source
// classification all run here off the actor (R3433); cfg is the Config snapshot
// startFileScan captured on the actor. idx is copy-bound (idx.fts is a private
// fts.Copy).
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.3 | R3419, R3426, R3433
func (db *DB) prepareFileScan(idx *Indexer, cfg *Config, path string, my uint64, force bool, release func()) {
	// R3612: every path but the hand-off to finishFileScan ends the scan here.
	handedOff := false
	defer func() {
		if !handedOff {
			release()
		}
	}()
	fi, statErr := os.Stat(path)
	status, checkErr := idx.fts.CheckFile(path) // private copy — off-actor safe
	known := checkErr == nil && status.FileID != 0

	// Deleted on disk: remove if known (a short write), then retire. Both the
	// enqueue and the retire are registry touches, so they post back to the
	// actor. R3419, R3433
	if statErr != nil {
		SyncVoid(db, func(db *DB) error {
			// R45, R3568: a forced refresh reports missing files and never removes
			// them, so it only retires the entry.
			if known && !force {
				db.enqueueWrite(func(ftsCopy *microfts2.DB) {
					if err := db.indexer.withFTS(ftsCopy).RemoveFile(path); err != nil {
						log.Printf("watch: remove %s: %v", path, err)
					}
					svc(db.svc, func() { db.retireFileScanIfCurrent(path, my) })
				})
			} else {
				db.retireFileScanIfCurrent(path, my)
			}
			return nil
		})
		return
	}
	if fi.IsDir() {
		SyncVoid(db, func(db *DB) error { db.retireFileScanIfCurrent(path, my); return nil })
		return
	}
	src, rel, ok := findSourceIn(cfg, path)
	if !ok {
		SyncVoid(db, func(db *DB) error { db.retireFileScanIfCurrent(path, my); return nil })
		return
	}
	strategy := cfg.StrategyForFile(rel, src.Strategies)

	// R3428-R3431: the gate carries per-chunk reuse + changeNum preemption for the
	// full-file compute; its map hops run on the DB actor.
	gate := dbScanGate{db: db, path: path, my: my}
	stale := checkErr == nil && status.Status == "stale"
	prep, err := idx.prepareScan(path, strategy, status.FileID, known, stale, force, gate)
	if err != nil {
		// errScanSuperseded is a clean abandon (a newer scan owns the entry), not a
		// failure — don't log it. R3430
		if !errors.Is(err, errScanSuperseded) && !errors.Is(err, microfts2.ErrNoChunks) && !errors.Is(err, os.ErrNotExist) {
			log.Printf("index: prepare %s: %v", path, err)
		}
		// Nothing to store — retire so a later event re-scans cleanly. When
		// superseded this is a no-op (changeNum != my); the newer scan keeps the
		// entry and its already-cached grams.
		SyncVoid(db, func(db *DB) error {
			db.retireFileScanIfCurrent(path, my)
			return nil
		})
		return
	}
	SyncVoid(db, func(db *DB) error {
		handedOff = true
		db.finishFileScan(path, prep, my, release)
		return nil
	})
}

// finishFileScan applies the changeNum redo-escape and, if the scan is still
// current, enqueues the store step. On the DB actor.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.7 | R3422
func (db *DB) finishFileScan(path string, prep *scanPrep, my uint64, release func()) {
	// R3422: a newer event bumped the changeNum (or the entry was retired) — drop
	// without storing. The newer scan owns the entry and will store + retire.
	if !db.fileScanCurrent(path, my) {
		release() // R3612
		return
	}
	db.enqueueWrite(func(ftsCopy *microfts2.DB) {
		defer release() // R3612: after the store, even one that fails or panics
		idx := db.indexer.withFTS(ftsCopy)
		err := idx.storeScan(prep)
		db.drainWriteSchedule(idx)
		// R3424/R3425: retire on the actor; re-emit one bounded change event on
		// failure. Fire-and-forget back to the actor is safe from a write
		// goroutine (the loop is free to receive).
		svc(db.svc, func() {
			act := storeResultAction(err)
			if err == nil {
				db.scanBytesSinceRelease += prep.chunkedBytes() // R3602
			}
			if act.reEmit {
				log.Printf("index: store %s failed (%v) — re-scanning once", path, err)
			}
			if act.retire {
				db.retireFileScanIfCurrent(path, my)
			}
			// R3579: the file's new chunks now exist, so it can be embedded.
			if err == nil && db.onFileStored != nil {
				db.onFileStored(path)
			}
			if act.reEmit {
				db.reEmitFileChange(path, act.force)
			}
		})
	})
}

// reEmitFileChange posts one change event for path back through the scan
// machinery so a failed store is re-scanned once — bounded, never an inline
// full reindex (R3425). On the DB actor; the post defers to the queue tail.
// CRC: crc-FileScanActor.md | Seq: seq-file-scan-actor.md#1.10.1 | R3425
func (db *DB) reEmitFileChange(path string, force bool) {
	db.Do(func(db *DB) { db.startFileScanWith(db.indexer, path, force) })
}

// SyncLinkPath brings the link table in line with one path, and records a
// proposed link's target as an unresolved candidate naming the link that
// proposed it — the same disposal the scan makes, so the two surfaces cannot
// answer differently about the same link.
//
// Actor-only: the link table is DB-actor state, and the write actor runs its
// closures on a separate goroutine (specs/db-concurrency.md).
//
// Reached two ways, and they are not redundant. IndexPathsAsync calls it for a
// path that *is* indexable, which is how a link replaced by an indexable file
// stops being an alias prefix. The watch loop calls it directly for a path
// that is not — which is every ordinary link event, since a directory symlink
// matches no file pattern and would otherwise never reach the actor at all.
//
// CRC: crc-DB.md | Seq: seq-file-change.md#1.3.5.2 | R3390, R3355, R3357
func (db *DB) SyncLinkPath(path string) {
	entry, status := db.scanner.Links().SyncPath(path)
	// R3384: the single-hop tier's entire invalidation mechanism. This event
	// names the link's own path — repointed, replaced by a real file, or
	// deleted — and nothing else re-derives a premise spelled through it.
	// Flagged for every disposition including LinkUnresolvable, because a link
	// that vanished invalidates exactly as surely as one that moved; the
	// recompute at the next retrieval is what decides the difference.
	//
	// Verified here rather than deferred to a reader, because this runs on the
	// actor owning ext state and single-hop is the one tier whose schedule is
	// its event: "recompute only on changes to their target path itself."
	// The other two tiers are the ones a retrieval has to look at.
	if db.extmap != nil {
		if flagged := db.extmap.MarkLinkChanged(path); len(flagged) > 0 {
			if changed := db.extmap.verifyLinkAssumptions(db, flagged); len(changed) > 0 {
				// R3388: the premise moving is only half the work — without
				// this the in-memory premise is corrected while the X records
				// still point at the old file, which is a disagreement no
				// event would ever resolve.
				if err := db.indexer.reresolveMovedPremises(changed); err != nil {
					log.Printf("link %s: re-resolve moved routings: %v", path, err)
				}
			}
		}
	}
	// R3383: record (or forget) the *fact* that this path is a directory
	// symlink, so a process that has not scanned can still rebuild the table.
	// Both tracked and proposed links are directory symlinks; which one a link
	// is today is re-derived, never stored.
	if status == LinkUnresolvable || entry == nil {
		if err := db.store.RemoveLink(path); err != nil {
			log.Printf("watch: forget link %s: %v", path, err)
		}
	} else if err := db.store.AddLink(entry.Path); err != nil {
		log.Printf("watch: record link %s: %v", entry.Path, err)
	}
	if status != LinkProposed || entry == nil {
		return
	}
	// The link proposes its target; it never admits it. The candidate is keyed
	// on the target, so it needs the source the *link* lives in to say where
	// the proposal came from. R3355, R3357
	src, _, ok := db.findSourceForPath(entry.Path)
	if !ok {
		return
	}
	// The whole record, not just path and dir: a proposal with no reason and
	// no proposer can neither explain itself nor be withdrawn (R3356, R3357).
	if err := db.store.AddUnresolvedRecord(UnresolvedRecord{
		Path: entry.Resolved, Dir: src.Dir,
		Reason: UnresolvedReasonSymlink, Symlink: entry.Path,
	}); err != nil {
		log.Printf("watch: unresolved %s (proposed by %s): %v", entry.Resolved, entry.Path, err)
	}
}

// findSourceForPath locates the non-glob source whose Dir is a prefix of path.
// Returns the source, the path relative to src.Dir, and ok=true on hit.
func (db *DB) findSourceForPath(path string) (*Source, string, bool) {
	return findSourceIn(db.config, path)
}

// findSourceIn is findSourceForPath against an explicit config snapshot, so the
// off-slot content scan can classify a path against the Config captured on the
// actor without reading the live db.config off the actor. R3433
func findSourceIn(cfg *Config, path string) (*Source, string, bool) {
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if IsGlob(s.Dir) {
			continue
		}
		rel, err := filepath.Rel(s.Dir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		return s, rel, true
	}
	return nil, "", false
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
// CRC: crc-Searcher.md | R738, R745
func (db *DB) SearchFuzzy(query string, opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchFuzzy(query, opts)
}

// SearchGrouped runs a search and groups results by file with rendered previews.
func (db *DB) SearchGrouped(query string, opts SearchOpts) ([]GroupedResult, error) {
	return db.search.SearchGrouped(query, opts)
}

// GroupTagChunks builds GroupedResult straight from a tag-derived chunkID set,
// bypassing FTS. CRC: crc-Searcher.md | R2442
func (db *DB) GroupTagChunks(chunkIDs []uint64, routed map[uint64]bool, opts SearchOpts) ([]GroupedResult, error) {
	return db.search.GroupTagChunks(chunkIDs, routed, opts)
}

// SearchTagChunks returns a flat SearchResultEntry list from a tag-derived
// chunkID set, bypassing FTS. CRC: crc-Searcher.md | R2442
func (db *DB) SearchTagChunks(chunkIDs []uint64, routed map[uint64]bool, opts SearchOpts) ([]SearchResultEntry, error) {
	return db.search.SearchTagChunks(chunkIDs, routed, opts)
}

// PathFileID resolves a path to its fileID. tmp:// paths route
// through the DB's own tmpPaths map; persistent paths go through
// microfts2's CheckFile. Returns (0, false) when the path is not
// currently indexed.
// CRC: crc-DB.md | R2463, R2464
func (db *DB) PathFileID(path string) (uint64, bool) {
	if strings.HasPrefix(path, "tmp://") {
		db.tmpMu.RLock()
		fid, ok := db.tmpPaths[path]
		db.tmpMu.RUnlock()
		if !ok || fid == 0 {
			return 0, false
		}
		return fid, true
	}
	if db.fts == nil {
		return 0, false
	}
	status, err := db.fts.CheckFile(path)
	if err != nil || status.FileID == 0 {
		return 0, false
	}
	return status.FileID, true
}

// ResolveTagPredicateChunks resolves a sigil-form MatchPredicate to
// the chunkID slice that satisfies it. When fileTag is true, the
// returned slice contains every chunkID belonging to a file that has
// at least one matching tag (file-scoped match). When false, only
// the chunks that directly carry the matching tag are returned.
// CRC: crc-Searcher.md | R2442, R2453, R2456
func (db *DB) ResolveTagPredicateChunks(p MatchPredicate, fileTag bool) []uint64 {
	chunkIDs, fileIDs := resolvePredicateLocations(p, db.store)
	if !fileTag {
		out := make([]uint64, 0, len(chunkIDs))
		for cid := range chunkIDs {
			out = append(out, cid)
		}
		return out
	}
	out := make([]uint64, 0)
	for fid := range fileIDs {
		info, err := db.fts.FileInfoByID(fid)
		if err != nil {
			continue
		}
		for _, ch := range info.Chunks {
			out = append(out, ch.ChunkID)
		}
	}
	return out
}

// RoutedTagPredicateChunks returns the candidates a routed value matching p
// reached, which the noise filter spares in a tag primary. The cold-path
// twin of the server's tag primary.
// CRC: crc-Searcher.md | R3587
func (db *DB) RoutedTagPredicateChunks(p MatchPredicate, candidates []uint64) map[uint64]bool {
	return routedPrimaryChunks(p, db.store, candidates)
}

// GetChunks returns the target chunk and its positional neighbors.
// R484: calls microfts2.DB.GetChunks() directly — no re-implementation of
// chunk retrieval. R488: the targetRange label is opaque, passed through
// verbatim to microfts2. R483: the target chunk and its neighbors come back
// in positional (ascending-index) order.
// CRC: crc-DB.md | R483, R484, R488, R693 — tmp:// paths are handled internally by microfts2.GetChunks
func (db *DB) GetChunks(fpath, targetRange string, before, after int) ([]microfts2.ChunkResult, error) {
	return db.fts.GetChunks(fpath, targetRange, before, after)
}

// ChunkFetchResult backs `ark chunks` content retrieval. The CLI's local
// arm and the POST /chunks handler both build it via FetchChunkContent, so
// their output cannot drift. R2998
type ChunkFetchResult struct {
	Subchunk string                  `json:"subchunk,omitempty"`
	HasSub   bool                    `json:"hasSub"`
	Chunks   []microfts2.ChunkResult `json:"chunks,omitempty"`
}

// FetchChunkContent resolves a chunks-command target and returns its
// content. anchor != "" → chat sub-chunk (R2914); path == "" → chunkID-only
// form (rng is the decimal id, resolved via ChunkInfo); otherwise
// path:range. Shared by the CLI local arm and POST /chunks. R2998
// R486: the target file must be indexed — ChunkInfo/GetChunks return an
// error when it is not in the database, propagated to the caller here.
// CRC: crc-DB.md | R486, R2998, R3572
func (db *DB) FetchChunkContent(path, rng, anchor string, before, after int) (ChunkFetchResult, error) {
	if anchor != "" {
		text, ok := db.ChatSubchunk(path, rng, anchor)
		if !ok {
			return ChunkFetchResult{}, fmt.Errorf("no sub-chunk matching %q at %s:%s", anchor, path, rng)
		}
		return ChunkFetchResult{Subchunk: text, HasSub: true}, nil
	}
	if path == "" {
		cid, _ := strconv.ParseUint(rng, 10, 64)
		info, err := db.ChunkInfo(cid)
		if err != nil {
			return ChunkFetchResult{}, err
		}
		path = info.Path
		rng = info.Range
	}
	chunks, err := db.GetChunks(path, rng, before, after)
	if err != nil {
		return ChunkFetchResult{}, err
	}
	for i := range chunks {
		if chunks[i].Missing {
			chunks[i].Attrs = nil // R3572: as stale as its text
		}
	}
	return ChunkFetchResult{Chunks: chunks}, nil
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
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		blob, err := bGet(txn, tagValueFullKey("id", value, tvid))
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

// ResolveExtTarget returns chunkids identified by an @ext TARGET.
// Two-phase: decompose the target into (base, modifier, anchor) per
// the grammar in specs/at-ext-parsing.md, then resolve. `sourceDir`
// is the source file's absolute directory, used to absolutize
// relative PATH bases; pass "" when no source context is available
// (relative paths then degenerate to literal-as-written lookups).
// Empty result means broken or unknown — callers treat that as a
// no-op annotation.
// CRC: crc-DB.md | R1985, R1986, R2366, R2367, R2368, R2369, R2370, R2371, R2372, R2373, R2374, R2375, R2376, R2377
func (db *DB) ResolveExtTarget(target, sourceDir string) []uint64 {
	return db.ResolveExtTargetVia(target, sourceDir).Chunks
}

// ExtResolution is what an @ext TARGET resolved to, together with the premise
// that resolution rests on when the path crossed tracked links. One walk
// produces both, so the premise can never describe a resolution other than
// the one actually returned — the drift R3388 exists to prevent, applied a
// layer earlier.
// CRC: crc-DB.md | R3382
type ExtResolution struct {
	// Chunks is the routing's target chunks — exactly what ResolveExtTarget
	// has always returned.
	Chunks []uint64
	// FileID is the file the TARGET named. Zero when the TARGET is UUID-based
	// (it names chunks across many files, so no single fileid is the premise)
	// or when it resolved to nothing at all.
	FileID uint64
	// Hops is the link chain the path crossed, nil when it crossed none —
	// which is precisely the case needing no premise record (R3372).
	Hops []string
}

// ResolveExtTargetVia resolves a TARGET and reports the premise its
// resolution rests on, so a caller recording a LinkAssumption gets the
// fileid and the chain from the same walk that produced the chunks.
// CRC: crc-DB.md | Seq: seq-ext-tag-retrieval.md#1.4 | R3382, R3384
func (db *DB) ResolveExtTargetVia(target, sourceDir string) ExtResolution {
	parts, ok := ParseExtTargetParts(target, sourceDir)
	if !ok || parts.Invalid {
		return ExtResolution{}
	}
	var res ExtResolution
	if parts.BaseKind == "uuid" {
		res.Chunks = db.resolveExtUUIDBase(parts)
	} else {
		res = db.resolveExtPathBase(parts)
	}
	if parts.ModifierN > 0 {
		res.Chunks = applyExtModifier(res.Chunks, parts.ModifierN)
	}
	return res
}

// resolveExtUUIDBase resolves a UUID-base TARGET to chunkids.
// Bare UUID → every chunk carrying the @id value. Anchored UUID →
// those chunks further filtered by string/regex match against
// chunk content.
// CRC: crc-DB.md | R2376
func (db *DB) resolveExtUUIDBase(parts ExtTargetParts) []uint64 {
	chunks := db.resolveExtUUID(parts.BaseValue)
	if len(chunks) == 0 {
		return nil
	}
	if parts.AnchorKind == "" {
		return chunks
	}
	return db.filterChunksByAnchor(chunks, parts.AnchorKind, parts.AnchorText)
}

// resolveExtPathBase resolves a PATH-base TARGET to chunkids, dispatching by
// the target's strategy (R3220) before anything else: the bible resolver owns
// the whole bible address path, including the virtual `BIBLE/<Book>` form that
// has no indexed file for resolveExtFilePath's CheckFile to find.
// CRC: crc-DB.md | Seq: seq-bible-resolve.md#3.2 | R2376, R2377, R3220
func (db *DB) resolveExtPathBase(parts ExtTargetParts) ExtResolution {
	// R3367: ask the strategy about the path the file is actually indexed
	// under. A bible file spelled through a link would otherwise miss the
	// bible branch here and fall to the file resolver, which resolves — so
	// the address would find the right file by the wrong reader.
	if db.targetStrategy(db.resolvePath(parts.BaseValue)) == bibleStrategy {
		// A bible address names a virtual location rather than an indexed
		// file, so there is no fileid to hold a premise and no chain to
		// classify.
		return ExtResolution{Chunks: db.resolveBibleTarget(parts)}
	}
	return db.resolveExtFilePath(parts)
}

// targetStrategy reports the strategy governing a TARGET's path. An indexed
// file answers from the index; anything else — notably a virtual
// `<source>/BIBLE/<Book>` address, which is deliberately not a file — falls
// back to matching the global strategy map against the absolute path, where
// the hook-derived source-prefixed entry (R3218) classifies it. Returns "" for
// a path no rule claims.
// CRC: crc-DB.md | R3218, R3220
func (db *DB) targetStrategy(path string) string {
	if s := db.FileStrategy(path); s != "" {
		return s
	}
	if db.config == nil {
		return "" // no config, no rules to classify an unindexed path by
	}
	return db.config.StrategyForPath(path)
}

// resolveBibleTarget decodes a bible address in two stages (R3220).
//
// Stage one (R3216): a `<source>/BIBLE/<Book>` path is virtual, so the anchor's
// chapter and the book index name the real file, and BASE is rewritten to it.
// Stage two (R3179): a CHAPTER.VERSE anchor selects the one chunk whose
// `chapter` matches and whose `verses` span contains the verse.
//
// A bible target whose anchor is not CHAPTER.VERSE — a bare path, quoted text,
// a regex, a line range — is ordinary path resolution against the real file,
// so it falls through to the generic resolver. A *virtual* path with such an
// anchor resolves to nothing: without a chapter there is no way to know which
// of a book's several files it means.
// CRC: crc-DB.md | Seq: seq-bible-resolve.md#3.3 | R3179, R3180, R3216, R3220
func (db *DB) resolveBibleTarget(parts ExtTargetParts) []uint64 {
	chapter, verse := 0, 0
	isVerseRef := false
	if parts.AnchorKind == "range" {
		chapter, verse, isVerseRef = parseChapterVerse(parts.AnchorText)
	}

	if source, book, ok := bibleVirtualTarget(parts.BaseValue); ok {
		if !isVerseRef {
			return nil
		}
		path, err := db.lookupBookFile(source, book, chapter)
		// R3180: a book or chapter with no entry resolves to nothing, with no
		// fall-through — a reference that named a verse and silently annotated
		// an unrelated paragraph would be worse than one that annotated none.
		if err != nil || path == "" {
			return nil
		}
		parts.BaseValue = path
	}
	if !isVerseRef {
		// A bible address resolves to a real indexed file here; only the
		// chunks matter, since the caller already knows a bible target holds
		// no link premise.
		return db.resolveExtFilePath(parts).Chunks
	}

	status, err := db.fts.CheckFile(parts.BaseValue)
	if err != nil || status.FileID == 0 {
		return nil
	}
	info, err := db.fts.FileInfoByID(status.FileID)
	if err != nil || len(info.Chunks) == 0 {
		return nil
	}
	return db.chunkForVerse(parts.BaseValue, info, chapter, verse)
}

// lookupBookFile reads the book-index record for (source, book, chapter),
// returning "" when there is none. Exact key — chapters do not span files.
// CRC: crc-DB.md | Seq: seq-bible-resolve.md#3.5 | R3214, R3216
func (db *DB) lookupBookFile(source, book string, chapter int) (string, error) {
	return db.store.ReadBookIndex(source, book, chapter)
}

// resolveExtFilePath resolves a PATH-base TARGET against a real indexed file.
// Bare path → first chunk (preamble convention). Anchored path →
// chunks in the file filtered by string/regex/range match.
// CRC: crc-DB.md | Seq: seq-ext-tag-retrieval.md#1.4 | R2376, R2377, R3367, R3382
func (db *DB) resolveExtFilePath(parts ExtTargetParts) ExtResolution {
	// R3367: the TARGET is stored as the user wrote it (absolutized but
	// unresolved), so a path spelled through a symlink names a file the index
	// only knows canonically. Resolving here — at the lookup, never at the key
	// — is what lets that routing fire, while an @ext written canonically is
	// unaffected because a canonical path crosses no tracked prefix.
	//
	// R3382: the same walk reports the chain it crossed, so the premise this
	// resolution is recorded under cannot describe a different resolution.
	base, hops := db.resolvePathVia(parts.BaseValue)
	status, err := db.fts.CheckFile(base)
	if err != nil || status.FileID == 0 {
		// R3386: unresolved, never last-known-good. The caller records a
		// premise carrying the chain but no fileid, so the routing reads as
		// an absence someone can see rather than a tag pointing at whatever
		// it used to mean.
		return ExtResolution{Hops: hops}
	}
	res := ExtResolution{FileID: status.FileID, Hops: hops}
	info, err := db.fts.FileInfoByID(status.FileID)
	if err != nil || len(info.Chunks) == 0 {
		return res
	}
	if parts.AnchorKind == "" {
		res.Chunks = []uint64{info.Chunks[0].ChunkID}
		return res
	}
	if parts.AnchorKind == "range" {
		for _, c := range info.Chunks {
			if c.Location == parts.AnchorText {
				res.Chunks = []uint64{c.ChunkID}
				return res
			}
		}
		return res
	}
	// string or regex — load file's chunks with content and filter by
	// index correlation with info.Chunks. Reads the same resolved path
	// CheckFile just answered for, so content and fileid cannot disagree.
	all := db.AllChunks(base)
	pattern := compileAnchorRegex(parts.AnchorKind, parts.AnchorText)
	if parts.AnchorKind == "regex" && pattern == nil {
		return res
	}
	for i, c := range all {
		if i >= len(info.Chunks) {
			break
		}
		if c.Missing { // R3571: an empty stale chunk must not match ^$
			continue
		}
		if anchorMatches(c.Content, parts.AnchorKind, parts.AnchorText, pattern) {
			res.Chunks = append(res.Chunks, info.Chunks[i].ChunkID)
		}
	}
	return res
}

// filterChunksByAnchor takes a list of chunkIDs (possibly spanning
// multiple files) and filters them to those whose content matches
// the anchor. Used by UUID-base resolution after the @id lookup.
// Order is preserved.
// CRC: crc-DB.md | R2376
func (db *DB) filterChunksByAnchor(chunks []uint64, kind, text string) []uint64 {
	if len(chunks) == 0 || kind == "" {
		return chunks
	}
	pattern := compileAnchorRegex(kind, text)
	if kind == "regex" && pattern == nil {
		return nil
	}
	var out []uint64
	for _, cid := range chunks {
		content, ok := db.chunkContent(cid)
		if !ok {
			continue
		}
		if anchorMatches(content, kind, text, pattern) {
			out = append(out, cid)
		}
	}
	return out
}

// chunkContent fetches the text for a single chunkID. Resolves
// chunkID → fileID → path, then queries microfts2 for that chunk's
// content via its Location. Returns ("", false) when any step fails.
// CRC: crc-DB.md | R2028
func (db *DB) chunkContent(chunkID uint64) (string, bool) {
	var fileID uint64
	var ok bool
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		fileID, ok = db.chunkFileID(txn, chunkID)
		return nil
	})
	if !ok {
		return "", false
	}
	path, ok := db.fileIDPath(fileID)
	if !ok {
		return "", false
	}
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil {
		return "", false
	}
	for _, c := range info.Chunks {
		if c.ChunkID != chunkID {
			continue
		}
		results, err := db.fts.GetChunks(path, c.Location, 0, 0)
		if err != nil || len(results) == 0 || results[0].Missing { // R3571
			return "", false
		}
		return results[0].Content, true
	}
	return "", false
}

// compileAnchorRegex returns a compiled regex for the regex anchor
// kind, or nil for other kinds / invalid patterns.
// CRC: crc-DB.md | R2376
func compileAnchorRegex(kind, text string) *regexp.Regexp {
	if kind != "regex" {
		return nil
	}
	p, err := regexp.Compile(text)
	if err != nil {
		return nil
	}
	return p
}

// anchorMatches reports whether content matches the anchor.
// CRC: crc-DB.md | R2376
func anchorMatches(content, kind, text string, pattern *regexp.Regexp) bool {
	switch kind {
	case "string":
		return strings.Contains(content, text)
	case "regex":
		return pattern != nil && pattern.MatchString(content)
	}
	return false
}

// applyExtModifier post-filters chunks by MODIFIER. Returns
// chunks[n-1:n] for 1-based N, or nil if out of range.
// CRC: crc-DB.md | R2371
func applyExtModifier(chunks []uint64, n int) []uint64 {
	if n <= 0 || n > len(chunks) {
		return nil
	}
	return []uint64{chunks[n-1]}
}

// resolveExtUUID returns every chunkid carrying @id == value.
// CRC: crc-DB.md | R1985
func (db *DB) resolveExtUUID(value string) []uint64 {
	tvid, ok := db.store.TvidMap().Lookup(tagID, value)
	if !ok {
		return nil
	}
	var chunks []uint64
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		blob, err := bGet(txn, tagValueFullKey(tagID, value, tvid))
		if err == nil {
			chunks = decodeVarints(blob)
		}
		return nil
	})
	return chunks
}

// chunkFileID returns the fileid that owns chunkID. Branches on
// IsOverlayID — overlay chunkids resolve via Store.filesForChunk
// (routed to TmpTagStore.FilesForChunk), persistent chunkids read
// the C record inside the supplied txn. ok=false when the chunk is
// unknown or has no file linkage.
// CRC: crc-DB.md | R1990, R2028
func (db *DB) chunkFileID(txn *bbolt.Tx, chunkID uint64) (uint64, bool) {
	if IsOverlayID(chunkID) {
		if db.store != nil && db.store.filesForChunk != nil {
			if fids := db.store.filesForChunk(txn, chunkID); len(fids) > 0 {
				return fids[0], true
			}
		}
		return 0, false
	}
	crec, err := db.fts.ReadCRecord(txn, chunkID)
	if err != nil || len(crec.FileIDs) == 0 {
		return 0, false
	}
	return crec.FileIDs[0].FileID, true
}

// ChunkFileID resolves chunkID to its owning fileid outside a caller-supplied
// txn, opening its own read view — the same shape ChunkInfo uses. For callers
// that need only the fileid, this skips ChunkInfo's FileInfoByID plus the
// linear scan of the file's chunk list; the recall substrate's per-candidate
// path-scope gate is the motivating caller.
// CRC: crc-DB.md | R3186
func (db *DB) ChunkFileID(chunkID uint64) (uint64, bool) {
	var fileID uint64
	var ok bool
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		fileID, ok = db.chunkFileID(txn, chunkID)
		return nil
	})
	return fileID, ok
}

// fileIDPath returns the canonical path for a fileid (Names[0]).
// CRC: crc-DB.md | R2000
func (db *DB) fileIDPath(fileID uint64) (string, bool) {
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Names) == 0 {
		return "", false
	}
	return info.Names[0], true
}

// ChunkInfo is the per-chunk metadata bundle the curation workshop UI
// renders into a chunk card. Returned by DB.ChunkInfo and surfaced to
// Lua via mcp.chunkInfo. CRC: crc-DB.md | R2386, R2387
type ChunkInfo struct {
	ChunkID       uint64
	FileID        uint64
	Path          string // canonical absolute path (FRecord.Names[0])
	Range         string // chunker-specific range identifier (FileChunkEntry.Location)
	ByteStart     uint64 // half-open [ByteStart, ByteEnd) byte range in the file
	ByteEnd       uint64
	Writable      bool   // false for read-only chunkers or hardcoded read-only zones
	CommentSyntax string // line-comment delimiter for inline tag insertion ("" if n/a)
}

// ChunkInfo assembles the per-chunk metadata bundle the workshop UI
// needs. Resolves chunkID → fileID → canonical path, finds the chunk's
// FileChunkEntry to read Range and the byte-range Locator, looks up the
// file's chunker via db.chunkerByName, queries ChunkerMetadata when
// implemented (else defaults to writable=true, commentSyntax=""), and
// folds in the hardcoded read-only zone check (paths under
// ~/.claude/projects/** force writable=false). Unknown chunks return
// (ChunkInfo{}, "chunk not found"). CRC: crc-DB.md | R2386, R2387, R2389
func (db *DB) ChunkInfo(chunkID uint64) (ChunkInfo, error) {
	var fileID uint64
	var ok bool
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		fileID, ok = db.chunkFileID(txn, chunkID)
		return nil
	})
	if !ok {
		return ChunkInfo{}, errors.New("chunk not found")
	}
	info, err := db.fts.FileInfoByID(fileID)
	if err != nil || len(info.Names) == 0 {
		return ChunkInfo{}, errors.New("chunk not found")
	}
	path := info.Names[0]
	var entry microfts2.FileChunkEntry
	found := false
	for _, c := range info.Chunks {
		if c.ChunkID == chunkID {
			entry = c
			found = true
			break
		}
	}
	if !found {
		return ChunkInfo{}, errors.New("chunk not found")
	}
	// byteStart / byteEnd come from the chunker's
	// EncodeByteRangeLocator output stored in FileChunkEntry.Locator.
	// Chunkers that don't encode byte ranges (e.g. PDFChunker) leave
	// these zero; the workshop UI gates on Writable in those cases.
	// CRC: crc-DB.md | R2387
	var byteStart, byteEnd uint64
	if start, end, decoded := microfts2.DecodeByteRangeLocator(entry.Locator); decoded {
		byteStart = uint64(start)
		byteEnd = uint64(end)
	}
	writable, commentSyntax := db.chunkerMetadata(info.Strategy)
	if writable && isReadOnlyPath(path) {
		writable = false
	}
	return ChunkInfo{
		ChunkID:       chunkID,
		FileID:        fileID,
		Path:          path,
		Range:         entry.Location,
		ByteStart:     byteStart,
		ByteEnd:       byteEnd,
		Writable:      writable,
		CommentSyntax: commentSyntax,
	}, nil
}

// chunkerMetadata returns the (writable, commentSyntax) pair for a
// strategy by type-asserting against microfts2.ChunkerMetadata. Strategy
// names absent from the registry, or chunkers that don't implement the
// interface, return the safe defaults (true, ""). CRC: crc-DB.md | R2388
func (db *DB) chunkerMetadata(strategy string) (bool, string) {
	c, ok := db.chunkerByName[strategy]
	if !ok {
		return true, ""
	}
	if cm, ok := c.(microfts2.ChunkerMetadata); ok {
		return cm.IsWritable(), cm.CommentSyntax()
	}
	return true, ""
}

// isReadOnlyPath returns true when path falls under a hardcoded
// read-only zone. v1 zone is ~/.claude/projects/** — chat-log
// transcripts are conceptually read-only even when the underlying
// chunker reports IsWritable()=true. CRC: crc-DB.md | R2389
func isReadOnlyPath(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	prefix := filepath.Join(home, ".claude", "projects") + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}

// ReplaceRegion atomically replaces the byte range [byteStart, byteEnd)
// in path with newText. Direct file I/O matching mcp.setTags's
// precedent for Lua-driven file mutation: validates the path is indexed
// (rejects tmp://), bounds-checks the range, then writes via
// temp+rename. The watcher picks up the change and triggers reindex —
// this primitive does not enqueue an explicit reindex.
// CRC: crc-DB.md | R2390, R2391
func (db *DB) ReplaceRegion(path string, byteStart, byteEnd uint64, newText []byte) error {
	if strings.HasPrefix(path, "tmp://") {
		return errors.New("tmp:// paths use tmp_update")
	}
	if byteEnd < byteStart {
		return fmt.Errorf("invalid range: byteEnd %d < byteStart %d", byteEnd, byteStart)
	}
	if _, err := db.fts.CheckFile(path); err != nil {
		return fmt.Errorf("file not indexed: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if byteEnd > uint64(len(data)) {
		return fmt.Errorf("range %d-%d out of bounds (file size %d)", byteStart, byteEnd, len(data))
	}
	out := make([]byte, 0, uint64(len(data))-byteEnd+byteStart+uint64(len(newText)))
	out = append(out, data[:byteStart]...)
	out = append(out, newText...)
	out = append(out, data[byteEnd:]...)
	return atomicWriteFile(path, out, 0644)
}

// atomicWriteFile writes data to path via a sibling .tmp file +
// rename, ensuring readers always see either the pre- or post-state.
// CRC: crc-DB.md | R2391
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	return nil
}

// resolveExtTargetFile resolves an `@ext` targetSpec to the canonical
// absolute path of its target file. UUID bases resolve to the file
// containing the chunk with that @id; path bases resolve to the
// absolutized path itself. CRC: crc-DB.md | R2395
func (db *DB) resolveExtTargetFile(targetSpec string) (string, error) {
	parts, ok := ParseExtTargetParts(targetSpec, "")
	if !ok || parts.Invalid {
		return "", fmt.Errorf("malformed targetSpec: %q", targetSpec)
	}
	if parts.BaseKind == "path" {
		if parts.BaseValue == "" {
			return "", fmt.Errorf("targetSpec resolves to empty path")
		}
		return parts.BaseValue, nil
	}
	chunks := db.resolveExtUUIDBase(parts)
	if len(chunks) == 0 {
		return "", fmt.Errorf("no chunk found for UUID %q", parts.BaseValue)
	}
	var fileID uint64
	var found bool
	_ = db.fts.DB().View(func(txn *bbolt.Tx) error {
		fileID, found = db.chunkFileID(txn, chunks[0])
		return nil
	})
	if !found {
		return "", fmt.Errorf("chunk %d has no file linkage", chunks[0])
	}
	path, ok := db.fileIDPath(fileID)
	if !ok {
		return "", fmt.Errorf("fileID %d has no canonical path", fileID)
	}
	return path, nil
}

// extMirrorPath returns the mirror-file path for a target file under
// sourceRoot. The trailing `.md` is always appended so mirror files are
// markdown-indexed regardless of the target's extension.
//
// Default layout is `~/.ark/external/<slug>/<target-path-within-source>.md`,
// where source-slug is path-as-slug (every `/` → `-`, leading `-` stripped).
// When extMirror is non-empty (the source's ext_mirror config), the base
// moves in-tree to `<sourceRoot>/<extMirror>/<target-path>.md` so routings
// travel with the source; a target already inside that dir has no mirror of
// its own (it would nest mirrors/mirrors/…) and is rejected.
// CRC: crc-DB.md | R2392, R3171
func extMirrorPath(sourceRoot, targetFile, extMirror string) (string, error) {
	rel, err := filepath.Rel(sourceRoot, targetFile)
	if err != nil {
		return "", fmt.Errorf("rel %s under %s: %w", targetFile, sourceRoot, err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("target %s not under source root %s", targetFile, sourceRoot)
	}
	// R3171: in-tree mirror override. Base moves under the source itself.
	if extMirror != "" {
		if rel == extMirror || strings.HasPrefix(rel, extMirror+string(filepath.Separator)) {
			return "", fmt.Errorf("target %s is inside its source's ext_mirror dir %q; no mirror", targetFile, extMirror)
		}
		return filepath.Join(sourceRoot, extMirror, rel+".md"), nil
	}
	slug := strings.ReplaceAll(strings.TrimPrefix(sourceRoot, string(filepath.Separator)), string(filepath.Separator), "-")
	if slug == "" {
		return "", fmt.Errorf("empty slug from source root %q", sourceRoot)
	}
	dbPath := arkHomeDir()
	if dbPath == "" {
		return "", errors.New("ark home directory unavailable")
	}
	return filepath.Join(dbPath, "external", slug, rel+".md"), nil
}

// arkHomeDir returns the canonical ~/.ark directory path. Used by
// extMirrorPath to compute the mirror tree root.
// CRC: crc-DB.md | R2392
func arkHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ark")
	}
	return ""
}

// resolveExtMirror resolves targetSpec to its mirror file path under
// `~/.ark/external/<slug>/`. Shared preamble for SetExtTag and
// RemoveExtTag. CRC: crc-DB.md | R2392, R2395, R2396
func (db *DB) resolveExtMirror(targetSpec string) (mirrorPath string, err error) {
	targetFile, err := db.resolveExtTargetFile(targetSpec)
	if err != nil {
		return "", err
	}
	src, ok := db.config.SourceForPath(targetFile)
	if !ok {
		return "", fmt.Errorf("no source root contains %s", targetFile)
	}
	// R3171: src.ExtMirror redirects the base in-tree when set.
	return extMirrorPath(src.Dir, targetFile, src.ExtMirror)
}

// pathWithin reports whether path is root itself or lies beneath it.
// CRC: crc-DB.md | R3264
func pathWithin(root, path string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// IsExtMirrorPath reports whether path lies inside a mirror tree — the
// global `~/.ark/external/`, or a source-local `<source>/<ext_mirror>/`
// for any source that sets `ext_mirror` (R3171). It is the inverse of
// extMirrorPath, and the authority check behind the rule that only a
// mirror tree may author `@ext`-family directives.
//
// The rule exists because an `@ext` tag is an instruction, not a note:
// without it, a transcript that merely *quotes* an `@ext-candidate` files
// a real proposal into the human's review queue. Observed live
// 2026-07-26 — a quoted example whose path had been elided to `…` for
// readability turned up in the pending ledger as a genuine candidate.
// CRC: crc-DB.md | Seq: seq-ext-routing.md | R3264
func (db *DB) IsExtMirrorPath(path string) bool {
	if path == "" {
		return false
	}
	if home := arkHomeDir(); home != "" && pathWithin(filepath.Join(home, "external"), path) {
		return true
	}
	for i := range db.config.Sources {
		s := &db.config.Sources[i]
		if s.ExtMirror == "" || IsGlob(s.Dir) {
			continue
		}
		if pathWithin(filepath.Join(s.Dir, s.ExtMirror), path) {
			return true
		}
	}
	return false
}

// readExtMirror reads a target's mirror file. A missing file yields nil
// data and no error (the caller creates it on the subsequent write); a
// genuine read failure is wrapped. Shared by the create-if-missing
// authoring methods (upsertExtTag and CandidateExtTag).
func readExtMirror(mirrorPath string) ([]byte, error) {
	data, err := os.ReadFile(mirrorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", mirrorPath, err)
	}
	return data, nil
}

// upsertExtTag reads the target's mirror file, applies op (set or
// add) via applyExtMirrorEdit, appends a fresh single-tag line when
// nothing matched, and writes atomically. Shared by SetExtTag
// (collapse-all) and AddExtTag (append-if-absent) — the op is the
// only difference.
// CRC: crc-DB.md | R2392, R2393, R2394, R2395, R3047
func (db *DB) upsertExtTag(targetSpec, tag, value string, op extOp) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	mirrorPath, err := db.resolveExtMirror(targetSpec)
	if err != nil {
		return err
	}
	data, err := readExtMirror(mirrorPath)
	if err != nil {
		return err
	}
	// applyExtMirrorEdit (set/add) plus append-when-nothing-matched, on
	// the committed class. (R2394)
	newData := upsertExtLine(data, targetSpec, tag, value, op, extClassCommitted)
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(mirrorPath), err)
	}
	return atomicWriteFile(mirrorPath, newData, 0644)
}

// SetExtTag authors an `@ext` routing into the mirror tree under
// `~/.ark/external/`, collapsing every existing (TARGET,tag) value to
// the one new value (append when none exist). See CRC crc-DB.md and
// R2392-R2395 for the full algorithm.
// CRC: crc-DB.md | R2392, R2393, R2394, R2395
func (db *DB) SetExtTag(targetSpec, tag, value string) error {
	return db.upsertExtTag(targetSpec, tag, value, extOpSet)
}

// AddExtTag appends an `@ext` routing to the target's mirror file,
// leaving existing values in place so a (TARGET,tag) can carry
// multiple values; an exact (TARGET,tag,value) duplicate is a no-op.
// CRC: crc-DB.md | R3047
func (db *DB) AddExtTag(targetSpec, tag, value string) error {
	return db.upsertExtTag(targetSpec, tag, value, extOpAdd)
}

// RemoveExtTag removes `@ext` routing(s) from the mirror tree. An
// empty value removes every (TARGET,tag) span; a non-empty value
// removes only spans whose value matches. Missing mirror file or no
// matching line is a silent no-op.
// CRC: crc-DB.md | R2396
func (db *DB) RemoveExtTag(targetSpec, tag, value string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	mirrorPath, err := db.resolveExtMirror(targetSpec)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(mirrorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", mirrorPath, err)
	}
	newData, matched, _ := applyExtMirrorEdit(data, targetSpec, tag, value, extOpRemove, extClassCommitted)
	if !matched {
		return nil
	}
	return atomicWriteFile(mirrorPath, newData, 0644)
}

// CandidateExtTag authors an `@ext-candidate` (proposed routing) into the
// target's mirror file, carrying an optional quoted insight placed first
// (no @ sigil) and a disposition (internal|external, default external)
// naming where an eventual accept writes the tag. The line's `@count` is
// the repetition tally: a new (TARGET, tag, value, insight, disposition)
// identity is written at `@count: 1`, stamped with today's first-seen
// date; an exact-identity repeat increments the existing line's `@count`
// and preserves its original date (a differing insight, disposition, or
// add-vs-replace is a distinct proposal with its own tally). The
// read-modify-write runs as one closure-actor op so concurrent bumps cannot
// lose an update.
// (R3051, R3074, R3075, R3090, R3092, R3104, R3105)
// CRC: crc-DB.md | Seq: seq-ext-author.md#4.2 | R3051, R3074, R3075, R3090, R3092, R3104, R3105
func (db *DB) CandidateExtTag(targetSpec, tag, value, insight, disposition string, replace bool) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	mirrorPath, err := db.resolveExtMirror(targetSpec)
	if err != nil {
		return err
	}
	data, err := readExtMirror(mirrorPath)
	if err != nil {
		return err
	}
	date := time.Now().Format("2006-01-02")
	data = upsertCountLine(data, candidateLine(targetSpec, tag, value, insight, disposition, replace), date, 1)
	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(mirrorPath), err)
	}
	return atomicWriteFile(mirrorPath, data, 0644)
}

// AcceptExtTag commits matching `@ext-candidate`(s) on targetSpec, resolving
// each per its own disposition: `external` routes to the mirror as an `@ext`
// edge (source untouched); `internal` writes the tag into the target file's
// own body via the chunker's insertion stencil, falling back to the external
// route when the file type cannot host an inline tag or the file is not
// writable. Every accept also writes a positive `@ext-judgment @count:+1`
// (deduped per tag) — the accept-time producer of the signed-RJ axis; the
// R3070 net-rejected filter fires on negative counts only, so a positive
// judgment reinforces without suppressing. The candidate(s) are consumed
// (leading date and insight dropped). Missing file or no match is a silent
// no-op. The source-file write runs on the DB actor (the same goroutine that
// serializes the mirror write), so concurrent accepts to one file serialize.
// The (disposition, replace) pair gives four cells: external-add appends the
// @ext edge, external-replace collapses the mirror's values (extOpSet),
// internal-add inserts an inline tag, internal-replace rewrites the existing
// inline tag (R3106, R3107). `applied` reports the disposition actually
// landed — "internal" when any internal write succeeded, "external" when only
// external writes landed, "" when nothing matched — so callers (the
// mcp.extAccept binding) can be honest about the capability degrade (R3248).
// CRC: crc-DB.md | Seq: seq-ext-author.md#4.3 | R3054, R3071, R3100, R3101, R3102, R3103, R3106, R3107, R3248
func (db *DB) AcceptExtTag(targetSpec, tag, value string) (applied string, err error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", errors.New("tag must not be empty")
	}
	mirrorPath, err := db.resolveExtMirror(targetSpec)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(mirrorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", mirrorPath, err)
	}
	accepted := collectAcceptedCandidates(data, targetSpec, tag, value)
	if len(accepted) == 0 {
		return "", nil
	}
	// Consume the accepted candidate spans; branch each per its disposition.
	newData, _, _ := applyExtMirrorEdit(data, targetSpec, tag, value, extOpRemove, extClassCandidate)
	date := time.Now().Format("2006-01-02")
	judged := make(map[string]bool)
	applied = extDispositionExternal
	for _, ac := range accepted {
		external := true
		if ac.disposition == extDispositionInternal {
			wrote, werr := db.writeInternalTag(targetSpec, ac.tag, ac.value, ac.replace)
			if werr != nil {
				return "", werr
			}
			external = !wrote // internal write landed → do not also route external
			if wrote {
				applied = extDispositionInternal
			}
		}
		if external {
			// The (disposition, replace) axes give four accept cells; here the
			// two external ones: replace collapses the mirror's values of the
			// tag (extOpSet), add appends the edge (extOpAdd). (R3106)
			op := extOpAdd
			if ac.replace {
				op = extOpSet
			}
			newData = upsertExtLine(newData, targetSpec, ac.tag, ac.value, op, extClassCommitted)
		}
		if !judged[ac.tag] {
			judged[ac.tag] = true
			newData = upsertCountLine(newData, judgmentIdentity(targetSpec, ac.tag), date, 1)
		}
	}
	return applied, atomicWriteFile(mirrorPath, newData, 0644)
}

// writeInternalTag writes `@tag: value` into the source file body of the
// resolved target and returns wrote=true. It returns false (the caller falls
// back to the external mirror route) whenever the target does not resolve to a
// single writable, tag-insertable location: a bare-path target inserts a
// file-level tag at the top of the file; a single-chunk address inserts a
// chunk-level tag under the chunk's opener; a multi-chunk match, an incapable
// chunker type (lines/jsonl/pdf), a comment-less code chunk, a read-only zone,
// or a file unwritable on disk all degrade to external. When replace is set,
// InsertTag rewrites an existing inline `@tag:` line in scope instead of
// adding a new one, degrading to a fresh insert when none is present (R3107).
// The write is a temp+rename on the DB actor; the normal reindex the file
// change triggers materializes the tag (no new DB mutation path).
// CRC: crc-DB.md | R3101, R3102, R3107
func (db *DB) writeInternalTag(targetSpec, tag, value string, replace bool) (bool, error) {
	parts, ok := ParseExtTargetParts(targetSpec, "")
	if !ok || parts.Invalid {
		return false, nil
	}
	// File-level only for a bare PATH base; a bare UUID spans many chunks.
	fileLevel := parts.BaseKind == "path" && parts.AnchorKind == "" && parts.ModifierN == 0
	chunkIDs := db.ResolveExtTarget(targetSpec, "")
	if len(chunkIDs) == 0 {
		return false, nil
	}
	if !fileLevel && len(chunkIDs) != 1 {
		return false, nil // multi-chunk (or unresolved) internal → external
	}
	info, err := db.ChunkInfo(chunkIDs[0])
	if err != nil || !info.Writable {
		return false, nil // unknown chunk or read-only zone → external
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil {
		return false, nil
	}
	inserter, ok := db.chunkerByName[finfo.Strategy].(tagInserter)
	if !ok {
		return false, nil // incapable chunker type → external
	}
	st, err := os.Stat(info.Path)
	if err != nil || st.Mode().Perm()&0o200 == 0 {
		return false, nil // unwritable on disk (layer b) → external
	}
	fileBytes, err := os.ReadFile(info.Path)
	if err != nil {
		return false, nil
	}
	scope := tagScopeChunk
	var chunk microfts2.Chunk
	if fileLevel {
		scope = tagScopeFile
	} else {
		chunk = microfts2.Chunk{Locator: microfts2.EncodeByteRangeLocator(int(info.ByteStart), int(info.ByteEnd))}
	}
	newBytes, ok := inserter.InsertTag(fileBytes, chunk, tag, value, scope, replace)
	if !ok {
		return false, nil // comment-less code chunk → external
	}
	if err := atomicWriteFile(info.Path, newBytes, st.Mode().Perm()); err != nil {
		return false, nil // write failed → external
	}
	return true, nil
}

// RemoveInternalTag is the internal-remove cell: delete the inline `@tag:`
// line from the target file's own body via the chunker's RemoveTag stencil.
// Scope resolution mirrors writeInternalTag (bare path → file-level tag
// block, single-chunk address → chunk scope), and the write is the same
// temp+rename on the DB actor. Unlike accept it never degrades: every gate
// writeInternalTag silently falls back on is an error here, because a
// fallback would delete a different fact (a mirror routing instead of the
// inline line the caller named) while reporting success. Writes no judgment.
// CRC: crc-DB.md | Seq: seq-ext-author.md#5.4.2 | R3244, R3245
func (db *DB) RemoveInternalTag(targetSpec, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	parts, ok := ParseExtTargetParts(targetSpec, "")
	if !ok || parts.Invalid {
		return fmt.Errorf("invalid target: %s", targetSpec)
	}
	fileLevel := parts.BaseKind == "path" && parts.AnchorKind == "" && parts.ModifierN == 0
	chunkIDs := db.ResolveExtTarget(targetSpec, "")
	if len(chunkIDs) == 0 {
		return fmt.Errorf("target resolves to no chunk: %s", targetSpec)
	}
	if !fileLevel && len(chunkIDs) != 1 {
		return fmt.Errorf("target resolves to %d chunks — internal remove needs exactly one: %s", len(chunkIDs), targetSpec)
	}
	info, err := db.ChunkInfo(chunkIDs[0])
	if err != nil {
		return err
	}
	if !info.Writable {
		return fmt.Errorf("%s is read-only", info.Path)
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil {
		return err
	}
	remover, ok := db.chunkerByName[finfo.Strategy].(tagInserter)
	if !ok {
		return fmt.Errorf("file type %q cannot host inline tags", finfo.Strategy)
	}
	st, err := os.Stat(info.Path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o200 == 0 {
		return fmt.Errorf("%s is not writable", info.Path)
	}
	fileBytes, err := os.ReadFile(info.Path)
	if err != nil {
		return err
	}
	scope := tagScopeChunk
	var chunk microfts2.Chunk
	if fileLevel {
		scope = tagScopeFile
	} else {
		chunk = microfts2.Chunk{Locator: microfts2.EncodeByteRangeLocator(int(info.ByteStart), int(info.ByteEnd))}
	}
	newBytes, ok := remover.RemoveTag(fileBytes, chunk, tag, scope)
	if !ok {
		return fmt.Errorf("no inline @%s: line in %s", tag, targetSpec)
	}
	return atomicWriteFile(info.Path, newBytes, st.Mode().Perm())
}

// ExtAmendOpts is the carry-forward half of AmendExtCandidate's signature.
// Each field is a pointer so **nil means "keep the old line's value"** —
// a plain string or bool could not say that, since an empty Insight and an
// unset Replace are both meaningful revisions. The tag/value positionals
// stay non-pointer because the subject is always stated: were they
// inherited too, `amend <tvid> <tag>` could not distinguish "make this
// name-only" from "leave the value alone".
// CRC: crc-DB.md | R3255
type ExtAmendOpts struct {
	Target      *string // retarget the proposal
	Insight     *string // edit the rationale
	Disposition *string // internal | external
	Replace     *bool   // on accept, collapse existing values vs. add
}

// orElse returns *p when p is non-nil, else the carried-forward old value.
// The whole of ExtAmendOpts' nil-means-inherit rule, in one place.
// CRC: crc-DB.md | R3255
func orElse[T any](p *T, old T) T {
	if p == nil {
		return old
	}
	return *p
}

// ptrTo is orElse's inverse, for callers building ExtAmendOpts: it turns a
// value the caller actually supplied into the non-nil pointer that says
// "revise this field" rather than "inherit it".
// CRC: crc-DB.md | R3255
func ptrTo[T any](v T) *T { return &v }

// AmendExtCandidate revises one `@ext-candidate` line in place, as a single
// closure-actor operation. The line is addressed by its **source tvid**
// because a (target, tag, value) triple can match several distinct
// candidates — identity is the six-tuple (target, disposition, replace,
// insight, tag, value) — so only the tvid names exactly one line.
//
// Retire-then-author is what makes an edited proposal *revise* rather than
// orphan: without it, firing an edited widget authors a new candidate and
// leaves the original pending with nobody to judge it. Because the revision
// goes out through CandidateExtTag's own `upsertCountLine(candidateLine(…))`
// path, the two settled tally rules fall out rather than needing their own
// code — a new identity lands at `@count: 1` (the tally measured agreement
// on a proposal the human just changed), and a revision colliding with an
// existing line merges into that line's tally.
//
// Unlike accept/reject, a tvid that resolves to nothing — or to a tag that
// is not `@ext-candidate` — is an **error**, not a silent no-op: the caller
// named one specific line, so silence would report a revision that never
// happened.
// CRC: crc-DB.md | Seq: seq-ext-author.md#6.3 | R3255
func (db *DB) AmendExtCandidate(sourceTvid uint64, tag, value string, opts ExtAmendOpts) error {
	if tag = strings.TrimSpace(tag); tag == "" {
		return errors.New("tag must not be empty")
	}
	if db.store == nil || db.store.tvids == nil {
		return errors.New("tvid map unavailable")
	}
	_, valuePart, resolved := db.store.tvids.Resolve(sourceTvid)
	old, ok := db.store.decodeCandidateRow(sourceTvid)
	if !resolved || !ok {
		return fmt.Errorf("tvid %d is not a live @ext-candidate", sourceTvid)
	}
	// The DROP identity must use the disposition as *written*, not the
	// external default decodeCandidateRow supplies for display: a line
	// authored without a disposition token has none in the file, and an
	// identity carrying one would not match it. Carry-forward, by contrast,
	// wants the normalized value — a revision is written afresh.
	oldLine := candidateLine(old.Target, old.Tagname, old.Value, old.Insight,
		peelExtCandidateDisposition(valuePart), old.Replace)

	oldMirror, err := db.resolveExtMirror(old.Target)
	if err != nil {
		return err
	}
	oldData, err := os.ReadFile(oldMirror)
	if err != nil {
		return fmt.Errorf("read %s: %w", oldMirror, err)
	}
	oldData, dropped := dropCountLine(oldData, oldLine)
	if !dropped {
		return fmt.Errorf("tvid %d: no matching @ext-candidate line in %s", sourceTvid, oldMirror)
	}

	newTarget := orElse(opts.Target, old.Target)
	newLine := candidateLine(newTarget, tag, value,
		orElse(opts.Insight, old.Insight),
		orElse(opts.Disposition, old.Disposition),
		orElse(opts.Replace, old.Replace))
	newMirror, err := db.resolveExtMirror(newTarget)
	if err != nil {
		return err
	}
	date := time.Now().Format("2006-01-02")
	// Retargeting can move the proposal to a different mirror file, so the
	// drop and the author are not always the same write.
	if newMirror == oldMirror {
		return atomicWriteFile(oldMirror, upsertCountLine(oldData, newLine, date, 1), 0644)
	}
	if err := atomicWriteFile(oldMirror, oldData, 0644); err != nil {
		return err
	}
	newData, err := readExtMirror(newMirror)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newMirror), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(newMirror), err)
	}
	return atomicWriteFile(newMirror, upsertCountLine(newData, newLine, date, 1), 0644)
}

// RejectExtTag rewrites matching `@ext-candidate` line(s) to a single
// tag-name-only `@ext-judgment`, a durable rejection consuming the
// candidate(s). Missing file or no match is a silent no-op.
// CRC: crc-DB.md | Seq: seq-ext-author.md#4.4 | R3055
func (db *DB) RejectExtTag(targetSpec, tag, value string) error {
	return db.transitionExtCandidate(targetSpec, tag, value, extClassJudgment)
}

// transitionExtCandidate removes matching @ext-candidate spans and
// re-emits them under toClass: committed keeps each distinct (tag,
// value) as an @ext line; judgment collapses to one tag-name-only
// @ext-judgment line per distinct tag, decrementing that line's signed
// @count by one (creating it — stamped with today's first-seen date — at
// -1 when absent) so repeated rejects accumulate magnitude. The
// candidate's leading date, insight, and @count are dropped by the
// rewrite. (R3054, R3055, R3075, R3090)
// CRC: crc-DB.md | Seq: seq-ext-author.md#4.3 | R3054, R3055, R3075, R3090
func (db *DB) transitionExtCandidate(targetSpec, tag, value string, toClass extClass) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag must not be empty")
	}
	mirrorPath, err := db.resolveExtMirror(targetSpec)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(mirrorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", mirrorPath, err)
	}
	newData, matched, removed := applyExtMirrorEdit(data, targetSpec, tag, value, extOpRemove, extClassCandidate)
	if !matched {
		return nil
	}
	date := time.Now().Format("2006-01-02")
	seen := make(map[string]bool, len(removed))
	for _, tv := range removed {
		if toClass == extClassJudgment {
			if seen[tv.Tag] {
				continue
			}
			seen[tv.Tag] = true
			newData = upsertCountLine(newData, judgmentIdentity(targetSpec, tv.Tag), date, -1)
			continue
		}
		key := tv.Tag + "\x00" + tv.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		newData = upsertExtLine(newData, targetSpec, tv.Tag, tv.Value, extOpAdd, toClass)
	}
	return atomicWriteFile(mirrorPath, newData, 0644)
}

// chunkIDValues returns the list of @id values registered against
// chunkID, derived from F[chunkID][id]. Used by ExtMap candidate
// collection to drive the "appearing UUID" lookup.
// CRC: crc-DB.md | R2000
func (db *DB) chunkIDValues(txn *bbolt.Tx, chunkID uint64) []string {
	key := tagFileKey(chunkID, "id")
	v, err := bGet(txn, key)
	if err != nil || len(v) <= 4 {
		return nil
	}
	tvids := decodeVarints(v[4:])
	out := make([]string, 0, len(tvids))
	for _, tv := range tvids {
		if _, val, ok := db.store.tvids.Resolve(tv); ok {
			out = append(out, val)
		}
	}
	return out
}

// tmpPathForFile resolves a tmp:// fileid to its source path. The
// overlay does not record per-chunk Locations, so location is empty.
// CRC: crc-DB.md | R1976
func (db *DB) tmpPathForFile(fileID uint64) (path, location string, ok bool) {
	db.tmpMu.RLock()
	defer db.tmpMu.RUnlock()
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

// GramCount pairs a decoded trigram with its corpus document frequency.
// R2999
type GramCount struct {
	Gram  string `json:"gram"`
	Count int    `json:"count"`
}

// GramCounts returns decoded trigram document-frequency counts for a
// query — the shared data behind `ark grams` (CLI local arm) and POST
// /grams, so the two cannot drift. R2999
func (db *DB) GramCounts(query string) ([]GramCount, error) {
	tris, err := db.QueryTrigramCounts(query)
	if err != nil {
		return nil, err
	}
	out := make([]GramCount, 0, len(tris))
	for _, t := range tris {
		out = append(out, GramCount{Gram: microfts2.DecodeTrigram(t.Trigram), Count: t.Count})
	}
	return out, nil
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
	check(IFieldTagModel, stored.Embedding.Model, db.config.Embedding.Model)
	check(IFieldSessionTTL, stored.SessionTTL, db.config.SessionTTL)
	checkJSON(IFieldDefaultInclude, stored.DefaultInclude, db.config.DefaultInclude)
	checkJSON(IFieldDefaultExclude, stored.DefaultExclude, db.config.DefaultExclude)
	checkJSON(IFieldStrategies, stored.Strategies, db.config.Strategies)
	checkJSON(IFieldSources, stored.Sources, db.config.Sources)
	checkJSON(IFieldChunkers, stored.Chunkers, db.config.Chunkers)
	// R3589: stored values are effective lists; compare against the effective ones.
	checkJSON(IFieldSearchExclude, stored.SearchExclude, db.config.SearchExcludes())
	checkJSON(IFieldSystemExclude, stored.SystemExclude, db.config.SystemExcludes())
	checkJSON(IFieldSchedule, stored.Schedule, db.config.Schedule)
	checkJSON(IFieldEmbedTiers, stored.Embedding.Tiers, db.config.Embedding.Tiers)

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

	hasFilter := len(filterFiles) > 0 || len(excludeFiles) > 0
	useContentLens := sizeFn == nil // nil sizeFn = use CRecord ContentLen (fast path)

	// Collect sizes per strategy
	stratSizes := make(map[string][]int)
	var allSizes []int

	for _, s := range stale {
		// R3195: one matcher. Globs arrive cwd-anchored from the -files
		// stack (R3204), so they carry the absolute form.
		if hasFilter && !MatchPathFilters(s.Path, filterFiles, excludeFiles) {
			continue
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
			sizes = make([]int, 0, len(chunks))
			for _, c := range chunks {
				if c.Missing { // R3571: unknown size, not size 0
					continue
				}
				sizes = append(sizes, sizeFn(c.Content))
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

// statusBinding is the actor-captured state a status read needs to run off the
// DB actor: a read-only view over an fts.Copy(), the last reconcile's counts,
// and the tmp-file count (the view carries no tmpPaths of its own).
// CRC: crc-DB.md | R3436
type statusBinding struct {
	view     *DB
	counts   reconcileCounts
	tmpCount int
}

// bindStatusRead captures, on the DB actor, everything a status read needs so
// the heavy record enumeration runs OFF the actor. A trivial in-memory hop — it
// holds the actor only for the capture, never for the walk. Must be called on
// the DB actor.
// CRC: crc-DB.md | Seq: seq-reconcile.md#3.1 | R3436
func (db *DB) bindStatusRead() statusBinding {
	db.tmpMu.RLock()
	tmpCount := len(db.tmpPaths)
	db.tmpMu.RUnlock()
	return statusBinding{
		view:     db.withFTS(db.fts.Copy()),
		counts:   db.reconcileStats,
		tmpCount: tmpCount,
	}
}

// readStatusOffActor binds the status view on the DB actor (one trivial Sync via
// bindStatusRead) and then runs read(binding) OFF the actor, on the caller's
// goroutine, so the index-wide file-record enumeration never holds the actor.
// The off-actor property is invisible when violated — the actor would merely be
// held — so this is the single seam a fire alarm injects against (moving read
// inside the bind Sync). R3436
// CRC: crc-DB.md | Seq: seq-reconcile.md#3.1 | R3436
func readStatusOffActor[T any](db *DB, read func(b statusBinding) (T, error)) (T, error) {
	b, err := Sync(db, func(db *DB) (statusBinding, error) {
		return db.bindStatusRead(), nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return read(b) // OFF the actor — the enumeration runs here, not on the actor
}

// StatusRead returns database status counts, running the read off the DB actor
// on a bound fts.Copy() view (R3436). This is the exported entry point for the
// cold CLI `ark status` path; the server's statusOp calls readStatusOffActor
// directly because it also runs StatusDB conditionally on the same view.
// CRC: crc-DB.md | R3435, R3436
func (db *DB) StatusRead() (*StatusInfo, error) {
	return readStatusOffActor(db, func(b statusBinding) (*StatusInfo, error) {
		return b.view.Status(b.counts, b.tmpCount)
	})
}

// Status returns database status counts. It runs off the DB actor on a bound
// fts.Copy() view (readStatusOffActor) and NEVER walks the corpus (R3435): it
// does not call StaleFiles and stats/hashes no indexed file. The
// stale/missing/unresolved counts and their as-of time come from rc — the last
// reconcile's captured counts; tmpCount is snapshotted on the actor at bind time
// (the view carries no tmpPaths). Everything else is a read over the file
// records via the bound view.
// CRC: crc-DB.md | R252, R253, R254, R258, R3435, R3436
func (db *DB) Status(rc reconcileCounts, tmpCount int) (*StatusInfo, error) {
	// R252, R253, R254, R258 — enumerate the file records (no os.Stat/hash of any
	// corpus file) for the chunk total (sum of per-file ChunkRanges), the total
	// size (sum of FileLength), and the per-strategy file tally.
	paths, err := db.fts.FileIDPaths()
	if err != nil {
		return nil, err
	}
	var totalChunks int
	var totalSize int64
	strategies := make(map[string]int)
	for id := range paths {
		info, err := db.fts.FileInfoByID(id)
		if err != nil {
			continue
		}
		strategies[info.Strategy]++
		totalChunks += len(info.Chunks)
		totalSize += info.FileLength
	}

	// DB format version
	dbFormat, _ := db.fts.Version()

	// Database file size on disk. bbolt has no LMDB-style preallocated map
	// ceiling — the file itself is the storage — so used and total both
	// report the on-disk size of microfts2's bbolt file. One stat of the DB
	// file, not the corpus.
	// CRC: crc-DB.md | R2982
	var mapUsed, mapTotal int64
	if fi, err := os.Stat(db.fts.DB().Path()); err == nil {
		mapTotal = fi.Size()
		mapUsed = fi.Size()
	}

	// CRC: crc-DB.md | R3395 — the warnings travel with the status rather
	// than depending on the caller opening the index itself, which is why a
	// proxied `ark status` used to print none at all.
	warnings, _ := db.store.Warnings()

	return &StatusInfo{
		Version:    Version,
		DBFormat:   dbFormat,
		Files:      len(paths),
		TotalSize:  totalSize,
		Stale:      rc.stale,
		Missing:    rc.missing,
		Unresolved: rc.unresolved,
		Chunks:     totalChunks,
		Sources:    len(db.config.Sources),
		Strategies: strategies,
		MapUsed:    mapUsed,
		MapTotal:   mapTotal,
		TmpFiles:   tmpCount,
		Warnings:   warnings,

		UnresolvedByReason: rc.byReason,
		ReconcileAt:        rc.at,
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
	// ReconcileAt is the wall-clock time the last reconcile computed the
	// Stale/Missing/Unresolved counts above (R3435). Zero means no reconcile has
	// completed in this process, which `ark status` renders as `-`. Status never
	// walks the corpus, so those three counts are as-of this time, not live.
	ReconcileAt time.Time `json:"reconcileAt,omitempty"`
	// UnresolvedByReason breaks the unresolved total down (R3370). One
	// reason normally dominates — `unindexable` is usually the great
	// majority — so a bare total lets a handful of actionable entries hide
	// behind thousands no user action can clear, and a count nobody can act
	// on trains its reader to ignore the line.
	UnresolvedByReason map[string]int `json:"unresolvedByReason,omitempty"`
	// Warnings carries the outstanding E-record conditions, keyed by
	// condition name (R3395). It rides in the status so a proxied
	// `ark status` reports the same warnings a local one does; they used
	// to be read from the index in whatever local open the CLI happened
	// to make, so the proxied form printed none.
	Warnings map[string]string `json:"warnings,omitempty"`
	// Memory describes the serving process; nil when no server answered. R3605
	Memory *MemoryStatus `json:"memory,omitempty"`
	// Scans is the serving process's scan backlog; nil when unserved. R3613
	Scans *ScanBacklog `json:"scans,omitempty"`
}

// ScanBacklog is how much file-scan work is outstanding, read on the DB actor.
// Holding against Slots shows the dispatch bound (R3612); WritesQueued shows a
// backlog behind the single writer.
type ScanBacklog struct {
	InFlight     int `json:"inFlight"`     // registry entries: scans not yet retired
	Holding      int `json:"holding"`      // scans holding a dispatch slot
	Slots        int `json:"slots"`        // the dispatch bound
	WritesQueued int `json:"writesQueued"` // write closures waiting for the writer
}

// scanBacklog reads the backlog. On the DB actor. R3613
// CRC: crc-Server.md | R3613
func (db *DB) scanBacklog() *ScanBacklog {
	return &ScanBacklog{
		InFlight:     len(db.fileScans),
		Holding:      len(db.scanSem),
		Slots:        cap(db.scanSem),
		WritesQueued: len(db.writeQueue),
	}
}

// arkLabels maps every ark-bucket record prefix that `status -db` displays to
// its human label. It mirrors the ark table in specs/record-formats.md minus any
// class a spec marks "no status display" (currently only S — see
// vector-freshness.md). buildRecordCounts iterates this map, so a record class
// missing here is silently omitted from the listing: add a class here whenever
// you add it to record-formats.md.
// CRC: crc-DB.md | R3078
var arkLabels = map[string]string{
	"B":  "bible-books", // R3214
	"D":  "tag-defs",
	"F":  "file-tags",
	"I":  "settings",
	"M":  "missing",
	"T":  "tag-totals",
	"L":  "links", // R3383
	"U":  "unresolved",
	"V":  "tag-values",
	"X":  "ext-routings",
	"E:": "errors",
	"EV": "tag-value-embeds",
	"EC": "chunk-embeds",
	"EF": "file-centroids",
	"ED": "tag-def-embeds", // R2162
	"PC": "page-content",
	"RM": "recall-cooldown", // R2882
	"HC": "hot-correlations",
	"RC": "recall-candidate",
	"RD": "recall-discussed",
	"RF": "recall-freshness",
	"RJ": "recall-judgment",
}

// StatusDB returns per-prefix record counts for both subdatabases.
// CRC: crc-DB.md | R2473, R2478, R2479, R2482
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
	db.tmpMu.RLock()
	defer db.tmpMu.RUnlock()
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

// recallTTL returns the configured [recall].discussed_ttl, falling
// back to 24h on empty/unparseable values.
// CRC: crc-DB.md | R2659, R2663
func (db *DB) recallTTL() time.Duration {
	if cfg := db.Config(); cfg != nil {
		ttl, _ := cfg.Recall.DiscussedTTLDuration()
		return ttl
	}
	return 24 * time.Hour
}

// AddDiscussed records `(session, tag, value)` as discussed in the
// recall dedup state, stamped with NOW.
// CRC: crc-DB.md | R2650
func (db *DB) AddDiscussed(session, tag, value string) error {
	return db.store.AddDiscussed(session, tag, value)
}

// ListDiscussed returns the unexpired RD entries for one session.
// Uses the configured TTL for lazy expiry.
// CRC: crc-DB.md | R2651, R2659
func (db *DB) ListDiscussed(session string, since time.Duration) ([]Discussed, error) {
	return db.store.ListDiscussed(session, since, db.recallTTL())
}

// ClearDiscussed deletes all RD entries for one session.
// CRC: crc-DB.md | R2652
func (db *DB) ClearDiscussed(session string) (int, error) {
	return db.store.ClearDiscussed(session)
}

// PruneDiscussed sweeps RD records across all sessions, deleting
// entries older than `ttlOverride` (or the configured TTL when
// `ttlOverride == 0`).
// CRC: crc-DB.md | R2653, R2659
func (db *DB) PruneDiscussed(ttlOverride time.Duration) (int, error) {
	ttl := ttlOverride
	if ttl == 0 {
		ttl = db.recallTTL()
	}
	return db.store.PruneDiscussed(ttl)
}

// ClearAllDiscussed deletes every RD record across every session,
// routing through the write actor. Returns the deleted count.
// CRC: crc-DB.md | R2744
func (db *DB) ClearAllDiscussed() (int, error) {
	var count int
	err := SyncVoid(db, func(_ *DB) error {
		c, err := db.store.ClearAllDiscussed()
		count = c
		return err
	})
	return count, err
}

// MarkSurfaced records that a chunk was surfaced to a session,
// stamping the RM record with NOW (routes through the write actor).
// CRC: crc-DB.md | R2894
func (db *DB) MarkSurfaced(session string, chunkID uint64) error {
	return SyncVoid(db, func(_ *DB) error {
		return db.store.MarkSurfaced(session, chunkID)
	})
}

// LastSurfaced reads the RM surface-cooldown timestamp for
// (session, chunk). Absent -> (0, false, nil).
// CRC: crc-DB.md | R2884
func (db *DB) LastSurfaced(session string, chunkID uint64) (int64, bool, error) {
	return db.store.LastSurfaced(session, chunkID)
}

// ClearSurfaceCooldown deletes all RM (surface-cooldown) entries for
// one session. Mirrors ClearDiscussed.
// CRC: crc-DB.md | R2887
func (db *DB) ClearSurfaceCooldown(session string) (int, error) {
	return db.store.ClearSurfaceCooldown(session)
}

// ClearAllSurfaceCooldown deletes every RM record across every session,
// routing through the write actor. Returns the deleted count.
// CRC: crc-DB.md | R2887
func (db *DB) ClearAllSurfaceCooldown() (int, error) {
	var count int
	err := SyncVoid(db, func(_ *DB) error {
		c, err := db.store.ClearAllSurfaceCooldown()
		count = c
		return err
	})
	return count, err
}

// ClearAllDerivedProposals wipes every RC record across the corpus,
// routing through the write actor. Returns the deleted count.
// CRC: crc-DB.md | R2744
func (db *DB) ClearAllDerivedProposals() (int, error) {
	var count int
	err := SyncVoid(db, func(_ *DB) error {
		c, err := db.store.ClearAllDerivedProposals()
		count = c
		return err
	})
	return count, err
}

// ClearAllDerivedFreshness wipes every RF record across the corpus,
// routing through the write actor. Returns the deleted count.
// CRC: crc-DB.md | R2744
func (db *DB) ClearAllDerivedFreshness() (int, error) {
	var count int
	err := SyncVoid(db, func(_ *DB) error {
		c, err := db.store.ClearAllDerivedFreshness()
		count = c
		return err
	})
	return count, err
}

// ClearAllDerivedRejections wipes every RJ record across the corpus,
// routing through the write actor. Returns the deleted count.
// CRC: crc-DB.md | R2744
func (db *DB) ClearAllDerivedRejections() (int, error) {
	var count int
	err := SyncVoid(db, func(_ *DB) error {
		c, err := db.store.ClearAllDerivedRejections()
		count = c
		return err
	})
	return count, err
}

// CheckpointFile advances the indexer's stored FileLength for `path`
// to the file's current on-disk size. Routes through the write actor.
// Returns the new FileLength.
// CRC: crc-DB.md | R2745
func (db *DB) CheckpointFile(path string) (int64, error) {
	var size int64
	err := SyncVoid(db, func(_ *DB) error {
		s, err := db.indexer.Checkpoint(path)
		size = s
		return err
	})
	return size, err
}

// SessionJSONLs returns absolute paths of every indexed chat-jsonl
// file under ~/.claude/projects/. When sessions is non-empty, restricts
// to files whose basename matches `<UUID>.jsonl` for one of the listed
// UUIDs. Used by `ark connections clean -checkpoint`.
// CRC: crc-DB.md | R2745
func (db *DB) SessionJSONLs(sessions []string) ([]string, error) {
	all, err := db.Files()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	projectsRoot := filepath.Join(home, ".claude", "projects") + string(filepath.Separator)
	wanted := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		wanted[s+".jsonl"] = true
	}
	var out []string
	for _, p := range all {
		if !strings.HasPrefix(p, projectsRoot) {
			continue
		}
		base := filepath.Base(p)
		if !strings.HasSuffix(base, ".jsonl") {
			continue
		}
		if len(wanted) > 0 && !wanted[base] {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Resolve dismisses unresolved files by pattern.
func (db *DB) Resolve(patterns []string) error {
	return db.store.ResolveByPattern(patterns, db.matcher)
}

// Fetch returns the full content of an indexed file.
// The file must be known to microfts2 (in the index).
// CRC: crc-DB.md | R161, R162, R163
// resolvePath translates a path arriving from outside ark into the canonical
// namespace (R3359).
//
// Every door calls this, and that is the whole point: a surface that resolves
// and a surface that does not will disagree about whether the same file
// exists, and the disagreement is silent — it presents as a plausible "not
// indexed". Partial adoption is worse than none, which is why this is one
// helper and not five open-coded calls.
//
// Absolute is not resolved. `filepath.Abs` on `~/x`-expanded input yields a
// *lexically* absolute path whether or not a component is a link, which is
// exactly how an @ext target came to be stored under a spelling its own file
// would never match. A path that traverses no tracked link comes back
// unchanged, and a dangling one falls back to its literal form so the answer
// stays ark's ordinary not-found rather than a resolution error (R3361).
//
// Read off the actor without synchronizing, which the table's published
// snapshots make safe (crc-LinkTable.md).
// CRC: crc-DB.md | Seq: seq-link-resolve.md#1.2 | R3359, R3360, R3361
// recordScannedLinks persists the directory symlinks a scan met, so the table
// can be rebuilt by a process that never scanned (R3383).
//
// Both dispositions are recorded, because both *are* directory symlinks — a
// proposed link is one whose target happens to lie outside every source today,
// and that is re-derived at load, never stored. Recording only the tracked
// ones would make the promotion in ReloadConfig impossible after a restart,
// since nothing would remember the proposal existed.
// CRC: crc-DB.md | R3383, R3381
func (db *DB) recordScannedLinks(results *ScanResults) {
	for _, entry := range results.TrackedLinks {
		if err := db.store.AddLink(entry.Path); err != nil {
			log.Printf("scan: record link %s: %v", entry.Path, err)
		}
	}
	for _, u := range results.NewUnresolved {
		if u.Reason == UnresolvedReasonSymlink && u.Symlink != "" {
			if err := db.store.AddLink(u.Symlink); err != nil {
				log.Printf("scan: record link %s: %v", u.Symlink, err)
			}
		}
	}
}

// rebuildLinkTable re-derives the whole table from the persisted L records,
// one readlink chain each (R3383). Called at Open, which is what stops a cold
// process — every CLI command that finds no server — from resolving its
// doors against an empty table while a server that happened to scan resolves
// them correctly. The two would disagree about whether a file exists, which
// is the failure R3359 exists to prevent.
// CRC: crc-DB.md | R3383, R3359
func (db *DB) rebuildLinkTable() {
	paths, err := db.store.ListLinks()
	if err != nil {
		log.Printf("rebuild link table: %v", err)
		return
	}
	if len(paths) == 0 {
		return
	}
	for _, entry := range db.scanner.Links().Rebuild(paths) {
		// Still outside every source: keep the candidate current.
		if err := db.store.AddUnresolvedRecord(UnresolvedRecord{
			Path: entry.Resolved, Dir: entry.Resolved,
			Reason: UnresolvedReasonSymlink, Symlink: entry.Path,
		}); err != nil {
			log.Printf("rebuild link table: candidate %s: %v", entry.Resolved, err)
		}
	}
}

func (db *DB) resolvePath(path string) string {
	resolved, _ := db.resolvePathVia(path)
	return resolved
}

// VerifyExtPremisesForFile runs the update half of the sewn retrieval for one
// file — steps 1.1 through 1.4 of seq-ext-tag-retrieval.md.
//
// Called **once at the start of a bulk retrieval**, never per chunk: "at the
// start of a bulk operation that retrieves ext tags, retrieve all the ext link
// associations for that fileid". The per-chunk `ExtRoutingsForTargetChunk`
// reads that follow then see verified state and pay nothing for it, which is
// what keeps this affordable on a render touching hundreds of chunks.
//
// Only the two *cached-or-uncacheable* tiers can have work here.
// `verifyLinkAssumptions` skips a clean single-hop premise, because that tier
// is recomputed at its own event (DB.SyncLinkPath) rather than at retrieval —
// so on the common path this walks a short list and does nothing at all.
//
// **Must run on the DB actor** (R3387): the verification mutates ExtMap's
// premise records, and a moved premise writes X records, so a read path cannot
// do this work itself without touching actor-owned state off-actor. Callers
// reach it through Sync/Do. It posts nothing on its own behalf, so it is safe
// to call from inside a closure that is already running — the R3349 trap is
// nested *posting*, not nested calling.
// CRC: crc-DB.md | Seq: seq-ext-tag-retrieval.md#1.1 | R3384, R3387
func (db *DB) VerifyExtPremisesForFile(fileID uint64) {
	if db.extmap == nil || db.indexer == nil || fileID == 0 {
		return
	}
	tvids := db.extmap.tvidsForFile(fileID)
	if len(tvids) == 0 {
		return
	}
	changed := db.extmap.verifyLinkAssumptions(db, tvids)
	if len(changed) == 0 {
		return
	}
	// R3388: the premise moving is half the work; without this the corrected
	// premise and the stale X records disagree, and nothing would reconcile
	// them — a link moving changes neither file, so no file event ever fires.
	if err := db.indexer.reresolveMovedPremises(changed); err != nil {
		log.Printf("verify ext premises for file %d: %v", fileID, err)
	}
}

// expandAliases returns every address naming the file at a canonical path,
// the canonical path itself included. The inverse of resolvePath, and the
// half of R3367 that runs when a *file* changes: the routing was keyed under
// the spelling its author wrote, so asking only the canonical name finds
// nothing when that author wrote a path through a link.
// CRC: crc-DB.md | R3367
func (db *DB) expandAliases(canonical string) []string {
	if db.scanner == nil {
		return []string{canonical}
	}
	return db.scanner.Links().Expand(canonical)
}

// resolvePathVia resolves like resolvePath and additionally reports the link
// chain the walk traversed, so a caller recording a premise can classify that
// chain's verification tier (R3384) and re-derive it on every recomputation
// (R3385). Nil hops when there is no table or when the address crossed no
// tracked link — which is precisely the case needing no premise at all.
// CRC: crc-DB.md | R3382, R3384, R3385
func (db *DB) resolvePathVia(path string) (string, []string) {
	if db.scanner == nil {
		return path, nil
	}
	return db.scanner.Links().ResolveVia(path)
}

// LinkTable exposes the one shared table so out-of-package callers — the CLI,
// which builds its own chunk filters against a cold-opened DB — can resolve
// path-shaped inputs against the same table the scan writes. Nil when no
// scanner was assembled, which BuildChunkFilters tolerates the way
// Searcher.normalizeOpts does.
// CRC: crc-DB.md | R3400
func (db *DB) LinkTable() *LinkTable {
	if db.scanner == nil {
		return nil
	}
	return db.scanner.Links()
}

func (db *DB) Fetch(path string) ([]byte, error) {
	// R692: tmp:// paths read from overlay's stored content
	if strings.HasPrefix(path, "tmp://") {
		return db.TmpContent(path)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	absPath = db.resolvePath(absPath) // R3359
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
	// R3359. This is also the `/content` door: the URL handler reaches the
	// bytes through here, so resolving once covers both.
	absPath = db.resolvePath(absPath)
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
// ChunkCache (same as ChunkText). Returns nil on lookup failure, and for a
// missing chunk, whose stored attrs are as stale as its text.
// CRC: crc-DB.md | R1705, R3571
func (db *DB) ChunkAttrs(path, rangeLabel string) []microfts2.Pair {
	cache := db.fts.NewChunkCache()
	chunks, err := cache.GetChunks(path, rangeLabel, 0, 0)
	if err != nil || len(chunks) == 0 || chunks[0].Missing {
		return nil
	}
	return chunks[0].Attrs
}

// ChunkMissing reports whether the chunk at path:rangeLabel is in the index
// but no longer produced by the file's current chunker. A failed ChunkText
// cannot tell that from a range the file never had; this can.
// CRC: crc-DB.md | R3574
func (db *DB) ChunkMissing(path, rangeLabel string) bool {
	chunks, err := db.fts.NewChunkCache().GetChunks(path, rangeLabel, 0, 0)
	return err == nil && len(chunks) == 1 && chunks[0].Missing
}

// The stale-chunk placeholder, in parts so the HTML form can set the command
// in <code> and still say the same thing. R3572
const (
	staleChunkLead = "This chunk's text is unavailable: the file was indexed under older chunking rules."
	staleChunkTail = "re-chunks it."
)

// staleChunkCommand is the command that repairs path, quoted for a shell
// when the path needs it.
func staleChunkCommand(path string) string {
	if strings.ContainsAny(path, " '\"$`\\") {
		path = "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return "ark refresh --force " + path
}

// StaleChunkPlaceholder is what a reader sees where a missing chunk would be.
// CRC: crc-DB.md | R3572
func StaleChunkPlaceholder(path string) string {
	return staleChunkLead + " `" + staleChunkCommand(path) + "` " + staleChunkTail
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

// ChunkTextByID resolves a chunkID to its text bytes by routing through
// ChunkInfo for the (path, range) pair and then ChunkText. Backs the
// mcp.chunkText Lua bridge.
// CRC: crc-DB.md | R2403
func (db *DB) ChunkTextByID(chunkID uint64) ([]byte, error) {
	info, err := db.ChunkInfo(chunkID)
	if err != nil {
		return nil, err
	}
	text := db.ChunkText(info.Path, info.Range)
	if text == nil {
		return nil, fmt.Errorf("chunk text unavailable")
	}
	return text, nil
}

// TmpContent reads the stored content of a tmp:// document from the
// microfts2 overlay. Validates the tmp:// prefix; absent overlay entries
// surface microfts2's underlying error.
// CRC: crc-DB.md | R2407
func (db *DB) TmpContent(path string) ([]byte, error) {
	if !strings.HasPrefix(path, "tmp://") {
		return nil, fmt.Errorf("not a tmp:// path")
	}
	r, err := db.fts.TmpContent(path)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
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

// ChunkIDsForPath returns the path's chunk IDs in chunk order, or
// nil if the file is not indexed.
// CRC: crc-DB.md | R2065, R2079
func (db *DB) ChunkIDsForPath(path string) []uint64 {
	info, err := db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return nil
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil || len(finfo.Chunks) == 0 {
		return nil
	}
	out := make([]uint64, len(finfo.Chunks))
	for i, c := range finfo.Chunks {
		out[i] = c.ChunkID
	}
	return out
}

// ChunkIDByLocation returns the chunk ID for a given range/location
// string in a file, or 0 if not found.
// CRC: crc-DB.md | R2065, R2079
func (db *DB) ChunkIDByLocation(path, loc string) uint64 {
	info, err := db.fts.CheckFile(path)
	if err != nil || info.FileID == 0 {
		return 0
	}
	finfo, err := db.fts.FileInfoByID(info.FileID)
	if err != nil {
		return 0
	}
	for _, c := range finfo.Chunks {
		if c.Location == loc {
			return c.ChunkID
		}
	}
	return 0
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
	absPath = db.resolvePath(absPath) // R3359
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
// CRC: crc-DB.md | R525, R1147, R1149, R1952
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
		db.tmpMu.RLock()
		defer db.tmpMu.RUnlock()
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
	// CRC: crc-DB.md | R765
	StatusDate string `json:"statusDate"` // @status-date: tag value
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
	// Post-migration TagValueChunks returns chunkids; resolve to fileids
	// via microfts2 C-records so excludeIDs stays file-level.
	addExcluded := func(excludeIDs map[uint64]bool, chunkIDs []uint64) error {
		return db.fts.DB().View(func(txn *bbolt.Tx) error {
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
			ids, err := db.store.TagValueChunks("status", status)
			if err != nil {
				return nil, err
			}
			if err := addExcluded(excludeIDs, ids); err != nil {
				return nil, err
			}
		}
	}
	if !includeArchived {
		ids, err := db.store.TagValueChunks("archived", "true")
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
// V records hold chunkids, so each value's chunkids resolve to their
// owning fileids via Store.FilesForChunks (overlay-aware) before each
// fileid is mapped to a path.
// CRC: crc-DB.md | Seq: seq-tag-value-index.md | R1887
func (db *DB) TagValuesWithFiles(tag, prefix string) ([]TagValueFileInfo, error) {
	values, err := db.store.QueryTagValues(tag, prefix)
	if err != nil {
		return nil, err
	}
	var results []TagValueFileInfo
	for _, v := range values {
		chunkids, err := db.store.TagValueChunks(tag, v.Value)
		if err != nil {
			continue
		}
		chunkSet := make(map[uint64]bool, len(chunkids))
		for _, cid := range chunkids {
			chunkSet[cid] = true
		}
		var paths []string
		for fid := range db.store.FilesForChunks(chunkSet) {
			if path, ok := db.resolveFilePath(fid); ok {
				paths = append(paths, path)
			}
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

// CRC: crc-DB.md | R236, R238, R239, R240, R241, R242, R243, R244, R245, R247, R2271, R2272, R2275
// JSONLChunkFunc is a content-aware chunker for Claude conversation logs.
// Extracts human-readable text (user/assistant text blocks, thinking
// blocks) where available; emits a chunk with raw line bytes when the
// line is partial JSON at the file tail (R2272). Complete lines whose
// content is operational/tool-only (R240-R243) are skipped — the
// extractor returns empty and we drop them. Each chunk's Locator
// carries its byte range in the source content (R2275); AppendAware
// uses this to resume across calls.
func JSONLChunkFunc(_ string, content []byte, yield func(microfts2.Chunk) bool) error {
	lineNum := 0
	start := 0
	for i := 0; i <= len(content); i++ {
		if i < len(content) && content[i] != '\n' {
			continue
		}
		lineNum++
		lineStart := start
		lineEnd := i
		line := content[lineStart:lineEnd]
		start = i + 1

		if len(line) == 0 {
			continue
		}

		atEOF := i == len(content)
		// R3553, R3564: the line is read through the transcript reader, with no
		// fast path of its own. Records ark
		// does not use (attachments, API request copies, bookkeeping) yield
		// no text and are not indexed.
		rec, used := ParseTranscriptLine(line, lineNum, nil)
		var text string
		if used {
			text = chatChunkText(rec)
		}
		var chunkText []byte
		switch {
		case text != "":
			chunkText = []byte(text)
		case atEOF && !used:
			// Partial trailing line — no terminating newline yet. Emit a
			// chunk with the raw bytes so the content is searchable;
			// AppendAware drop-and-replace will correct it once the line
			// completes on a later append. (R2271, R2272, R2274)
			chunkText = line
		default:
			// Complete line with no extracted content — operational or
			// tool-only record. Skip per R240-R243.
			continue
		}

		r := fmt.Sprintf("%d-%d", lineNum, lineNum)
		chunk := microfts2.Chunk{
			Range:   []byte(r),
			Locator: microfts2.EncodeByteRangeLocator(lineStart, lineEnd),
			Content: chunkText,
		}
		// R1507-R1508: role and skill attrs, and the timestamp.
		var attrs []microfts2.Pair
		if !rec.Time.IsZero() {
			attrs = append(attrs, microfts2.Pair{Key: []byte("timestamp"), Value: []byte(strconv.FormatInt(rec.Time.UnixNano(), 10))})
		}
		if role := chatChunkRole(rec); role != "" {
			attrs = append(attrs, microfts2.Pair{Key: []byte("role"), Value: []byte(role)})
			if role == "skill" {
				if name := extractSkillName(chunkText); name != "" {
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

// Voice tags wrap a chat chunk's thinking and narration text, so every reader of
// a chunk can tell them from what was said (R3608).
const (
	thinkingOpen   = "<thinking>"
	thinkingClose  = "</thinking>"
	narrationOpen  = "<narration>"
	narrationClose = "</narration>"
)

// chatChunkText is a chat turn's chunk text: its text, thinking and narration
// blocks, one per line. Tool calls and tool results are not indexed. The rule
// the byte scanner applied, kept when the chunker moved onto the transcript
// reader (measured identical on every user and assistant turn, 2026-10-22).
// Thinking and narration are wrapped in voice tags flush against their text, so
// the first line of a block never stands alone as an HTML block (R3608).
// CRC: crc-DB.md | R240, R3553, R3567, R3608
func chatChunkText(rec TranscriptRecord) string {
	if rec.Kind != TurnUser && rec.Kind != TurnAssistant {
		return ""
	}
	var parts []string
	for _, b := range rec.Blocks {
		if b.Text == "" {
			continue
		}
		switch b.Kind {
		case BlockText:
			parts = append(parts, b.Text)
		case BlockThinking:
			parts = append(parts, thinkingOpen+b.Text+thinkingClose)
		case BlockNarration:
			parts = append(parts, narrationOpen+b.Text+narrationClose)
		}
	}
	return strings.Join(parts, "\n")
}

// chatChunkRole is a chat chunk's role attribute: "assistant" for an assistant
// turn; "skill" for a user turn carrying an injection, a skill or an agent
// report; "human" for any other user turn. Matches the scanner's rule except
// where the reader knows better: a person's mid-turn message and a task
// notification, both of which carry isMeta, are no longer "skill".
// CRC: crc-DB.md | R1507
func chatChunkRole(rec TranscriptRecord) string {
	switch rec.Kind {
	case TurnAssistant:
		return "assistant"
	case TurnUser:
		if rec.Speaker == SpeakerSkill || rec.Speaker == SpeakerAgentReport {
			return "skill"
		}
		return "human"
	}
	return ""
}

// CRC: crc-DB.md | R2273, R2274
// JSONLChunker implements microfts2.Chunker and microfts2.AppendAwareChunker
// for the chat-jsonl strategy. Per-file resume state lives in the chunk
// locator stored in microfts2's F record (R2275), so the struct itself
// carries no state.
type JSONLChunker struct{}

// chatChunkerVersion is the chat-jsonl chunker's rules version. Bump it when
// what the chunker indexes changes: microfts2 then reports every chat file
// chunked under another version stale, and the ordinary refresh re-chunks it.
// Version 2 introduced the voice tags.
// CRC: crc-DB.md | R3609
const chatChunkerVersion = 2

var _ microfts2.VersionedChunker = JSONLChunker{}

// ChunkerVersion implements microfts2.VersionedChunker. R3609
func (JSONLChunker) ChunkerVersion() uint32 { return chatChunkerVersion }

// Chunks delegates to JSONLChunkFunc.
func (JSONLChunker) Chunks(path string, content []byte, yield func(microfts2.Chunk) bool) error {
	return JSONLChunkFunc(path, content, yield)
}

// AppendChunks re-chunks chat-jsonl from the byte offset encoded in
// lastLocator through end-of-file (R2274). The first emitted chunk's
// byte range decides replacedLast: same range as the previous last
// chunk means the boundary was clean (drop, don't yield); a different
// range means the previous last chunk has been extended or replaced
// (typically a partial trailing line whose record has now grown or
// completed) and we yield it as a replacement with replacedLast=true.
// Subsequent chunks are yielded normally with line and byte ranges
// adjusted to absolute positions.
func (JSONLChunker) AppendChunks(path string, lastLocator []byte, _ []byte, yield func(microfts2.Chunk) bool) (bool, error) {
	if len(lastLocator) == 0 {
		// Legacy F record from before chat-jsonl emitted locators.
		// Error so the indexer falls through to executeFullRefresh,
		// which replaces all chunks (including locator-bearing ones)
		// in a single transaction. One-time per-file migration cost.
		return false, fmt.Errorf("chat-jsonl append: no locator on previous last chunk; needs full reindex")
	}
	oldStart, oldEnd, ok := microfts2.DecodeByteRangeLocator(lastLocator)
	if !ok {
		return false, fmt.Errorf("chat-jsonl append: malformed last-chunk locator")
	}
	full, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("chat-jsonl append: read file: %w", err)
	}
	if oldStart < 0 || oldStart > len(full) || oldEnd < oldStart || oldEnd > len(full) {
		return false, fmt.Errorf("chat-jsonl append: locator [%d,%d) outside file (len %d)", oldStart, oldEnd, len(full))
	}
	baseLine := bytes.Count(full[:oldStart], []byte{'\n'})
	oldLen := oldEnd - oldStart

	var firstSeen bool
	var replacedLast bool
	wrap := func(c microfts2.Chunk) bool {
		relStart, relEnd, okLoc := microfts2.DecodeByteRangeLocator(c.Locator)
		if !firstSeen {
			firstSeen = true
			if okLoc && relStart == 0 && relEnd == oldLen {
				return true // clean boundary — drop, don't yield
			}
			replacedLast = true
		}
		adjustedRange := adjustChunkRangeLines(string(c.Range), baseLine)
		var adjustedLoc []byte
		if okLoc {
			adjustedLoc = microfts2.EncodeByteRangeLocator(oldStart+relStart, oldStart+relEnd)
		} else {
			adjustedLoc = c.Locator
		}
		return yield(microfts2.Chunk{
			Range:   []byte(adjustedRange),
			Locator: adjustedLoc,
			Content: c.Content,
			Attrs:   c.Attrs,
		})
	}
	if err := JSONLChunkFunc(path, full[oldStart:], wrap); err != nil {
		return replacedLast, err
	}
	return replacedLast, nil
}

// adjustChunkRangeLines parses an "N-M" range and adds baseLine to both
// endpoints. Returns the input unchanged if it doesn't match the format.
func adjustChunkRangeLines(rangeStr string, baseLine int) string {
	idx := strings.IndexByte(rangeStr, '-')
	if idx < 1 || idx == len(rangeStr)-1 {
		return rangeStr
	}
	startN, errS := strconv.Atoi(rangeStr[:idx])
	endN, errE := strconv.Atoi(rangeStr[idx+1:])
	if errS != nil || errE != nil {
		return rangeStr
	}
	return strconv.Itoa(startN+baseLine) + "-" + strconv.Itoa(endN+baseLine)
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

// chunkForVerse resolves a CHAPTER.VERSE reference to the single paragraph
// chunk that contains it: the chunk whose `chapter` attribute matches and
// whose `verses` span covers the verse (R3179). Attributes come from
// AllChunks — the FRecord entries carry locations and ids but not attrs — so
// the two lists are correlated by index, the same correlation the string and
// regex anchors below use.
//
// Returns nil when nothing matches (R3180): a reference naming a chapter or
// verse the file does not have annotates nothing rather than falling back to
// some other chunk.
// CRC: crc-DB.md | R3179, R3180
func (db *DB) chunkForVerse(path string, info microfts2.FRecord, chapter, verse int) []uint64 {
	want := strconv.Itoa(chapter)
	for i, c := range db.AllChunks(path) {
		if i >= len(info.Chunks) {
			break
		}
		if c.Missing { // R3571: its chapter/verses attrs are stale
			continue
		}
		if ch, ok := microfts2.PairGet(c.Attrs, "chapter"); !ok || string(ch) != want {
			continue
		}
		span, ok := microfts2.PairGet(c.Attrs, "verses")
		if !ok || !verseSpanContains(string(span), verse) {
			continue
		}
		return []uint64{info.Chunks[i].ChunkID}
	}
	return nil
}
