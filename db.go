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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zot/microfts2"

	"github.com/BurntSushi/toml"
	"github.com/zot/microvec"
)

// Version is set by ldflags at build time from README.md.
// Fallback value for plain `go build`.
var Version = "dev"

// DB is the main ark facade. It coordinates microfts2, microvec,
// and the ark subdatabase.
type DB struct {
	fts     *microfts2.DB
	vec     *microvec.DB
	store   *Store
	config  *Config
	matcher *Matcher

	indexer *Indexer
	scanner *Scanner
	search  *Searcher

	dbPath   string
	tmpPaths map[string]uint64 // R664: tmp:// path → fileid tracking
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

	// Initialize microfts2 (creates the LMDB environment)
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

	// Initialize microvec (receives the LMDB env)
	vecOpts := microvec.Options{
		EmbedCmd: opts.EmbedCmd,
		QueryCmd: opts.QueryCmd,
	}
	vec, err := microvec.Create(fts.Env(), vecOpts)
	if err != nil {
		return fmt.Errorf("init microvec: %w", err)
	}
	defer vec.Close()

	// Initialize ark subdatabase
	store, err := OpenStore(fts.Env())
	if err != nil {
		return fmt.Errorf("init ark store: %w", err)
	}

	// CRC: crc-DB.md | R382
	// Register default chunking strategies
	// Func strategies avoid external process overhead and scanner buffer limits
	funcStrategies := map[string]microfts2.ChunkFunc{
		"lines":      microfts2.LineChunkFunc,
		"markdown":   microfts2.MarkdownChunkFunc,
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

	// Write default settings
	if err := store.PutSettings(ArkSettings{Dotfiles: true}); err != nil {
		return fmt.Errorf("write settings: %w", err)
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

	// Open microvec (receives the LMDB env)
	vec, err := microvec.Open(fts.Env(), microvec.Options{})
	if err != nil {
		fts.Close()
		return nil, fmt.Errorf("open microvec: %w", err)
	}

	// Open ark subdatabase
	store, err := OpenStore(fts.Env())
	if err != nil {
		vec.Close()
		fts.Close()
		return nil, fmt.Errorf("open ark store: %w", err)
	}

	// Read settings for matcher config
	settings, err := store.GetSettings()
	if err != nil {
		vec.Close()
		fts.Close()
		return nil, fmt.Errorf("read settings: %w", err)
	}

	matcher := &Matcher{Dotfiles: settings.Dotfiles}

	// CRC: crc-DB.md | R382
	// Register built-in func strategies (must happen on every Open,
	// not just InitDB — func strategies aren't persisted in LMDB).
	// The chat-jsonl chunker is wrapped in an LRU cache — conversation logs
	// are append-only, so chunks stay valid until the file changes.
	// The cache is captured by the closure; microfts2 never sees it.
	jsonlCache := newChunkCache(64)
	for name, fn := range map[string]microfts2.ChunkFunc{
		"lines":      microfts2.LineChunkFunc,
		"markdown":   microfts2.MarkdownChunkFunc,
		"chat-jsonl": jsonlCache.wrap(JSONLChunkFunc),
	} {
		if err := fts.AddStrategyFunc(name, fn); err != nil {
			vec.Close()
			fts.Close()
			return nil, fmt.Errorf("register %s strategy: %w", name, err)
		}
	}

	// CRC: crc-DB.md | R624, R626, R627, R628, R629, R630, R636, R637
	// Register chunker strategies from ark.toml [[chunker]] entries
	registerChunkers(fts, config)

	db := &DB{
		fts:     fts,
		vec:     vec,
		store:   store,
		config:  config,
		matcher: matcher,
		indexer: &Indexer{fts: fts, vec: vec, store: store},
		scanner: &Scanner{config: config, matcher: matcher, fts: fts},
		search:  &Searcher{fts: fts, vec: vec, store: store, config: config},
		dbPath:  dbPath,
	}
	return db, nil
}

// Close closes the database in reverse order.
func (db *DB) Close() error {
	// store doesn't need explicit close (shares env with fts)
	if err := db.vec.Close(); err != nil {
		return err
	}
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

// ConfigPath returns the path to ark.toml.
func (db *DB) ConfigPath() string { return filepath.Join(db.dbPath, "ark.toml") }

// SaveConfig writes the current config to disk and re-validates.
func (db *DB) SaveConfig() error { return db.config.SaveConfig(db.ConfigPath()) }

// ReloadConfig re-reads ark.toml from disk and propagates to components.
func (db *DB) ReloadConfig() error {
	cfg, err := LoadConfig(db.ConfigPath())
	if err != nil {
		return err
	}
	cfg.dbPath = db.dbPath
	db.config = cfg
	db.scanner.config = cfg
	db.search.config = cfg
	db.matcher.Dotfiles = cfg.Dotfiles
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

// AddTmpFile indexes content in memory via the microfts2 overlay.
// CRC: crc-DB.md | R663, R666, R667
func (db *DB) AddTmpFile(path, strategy string, content []byte) (uint64, error) {
	fid, err := db.fts.AddTmpFile(path, strategy, content)
	if err != nil {
		return 0, err
	}
	if db.tmpPaths == nil {
		db.tmpPaths = make(map[string]uint64)
	}
	db.tmpPaths[path] = fid
	return fid, nil
}

// UpdateTmpFile replaces content of an existing tmp:// document.
// CRC: crc-DB.md | R666
func (db *DB) UpdateTmpFile(path, strategy string, content []byte) error {
	return db.fts.UpdateTmpFile(path, strategy, content)
}

// RemoveTmpFile removes a tmp:// document from the overlay.
// CRC: crc-DB.md | R666
func (db *DB) RemoveTmpFile(path string) error {
	err := db.fts.RemoveTmpFile(path)
	if err != nil {
		return err
	}
	delete(db.tmpPaths, path)
	return nil
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

// SearchGrouped runs a search and groups results by file with rendered previews.
func (db *DB) SearchGrouped(query string, opts SearchOpts) ([]GroupedResult, error) {
	return db.search.SearchGrouped(query, opts)
}

// GetChunks returns the target chunk and its positional neighbors.
func (db *DB) GetChunks(fpath, targetRange string, before, after int) ([]microfts2.ChunkResult, error) {
	return db.fts.GetChunks(fpath, targetRange, before, after)
}

// QueryTrigramCounts returns trigram counts for a query string.
func (db *DB) QueryTrigramCounts(query string) ([]microfts2.TrigramCount, error) {
	return db.fts.QueryTrigramCounts(query)
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
func (db *DB) TagFiles(tags []string) ([]TagFileInfo, error) {
	records, err := db.store.TagFiles(tags)
	if err != nil {
		return nil, err
	}
	var results []TagFileInfo
	for _, rec := range records {
		info, err := db.fts.FileInfoByID(rec.FileID)
		if err != nil {
			continue
		}
		var size int64
		if fi, err := os.Stat(info.Names[0]); err == nil {
			size = fi.Size()
		}
		results = append(results, TagFileInfo{
			Path:  info.Names[0],
			Size:  size,
			Tag:   rec.Tag,
			Count: rec.Count,
		})
	}
	return results, nil
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
}

// Inbox returns cross-project messages from the tag index.
// CRC: crc-DB.md | Seq: seq-message.md | R563-R568, R617, R618, R619, R621, R622
// If showAll is false, completed/done/denied messages are excluded.
// If includeArchived is false, archived messages are excluded.
func (db *DB) Inbox(showAll, includeArchived bool) ([]InboxEntry, error) {
	files, err := db.TagFiles([]string{"status"})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var entries []InboxEntry
	for _, f := range files {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		if !strings.Contains(f.Path, "/requests/") {
			continue
		}
		data, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		tb := ParseTagBlock(data)
		statusVal, ok := tb.Get("status")
		if !ok {
			continue
		}
		if !showAll && (statusVal == "completed" || statusVal == "denied") {
			continue
		}
		if !includeArchived {
			if _, archived := tb.Get("archived"); archived {
				continue
			}
		}
		toVal, _ := tb.Get("to-project")
		// Handle comma-separated multi-target: take first project
		if i := strings.IndexByte(toVal, ','); i >= 0 {
			toVal = strings.TrimSpace(toVal[:i])
		}
		fromVal, _ := tb.Get("from-project")
		var summary, requestID, kind string
		if v, ok := tb.Get("ark-request"); ok {
			requestID = v
			if toVal == fromVal {
				kind = "self"
			} else {
				kind = "request"
			}
			if iss, ok := tb.Get("issue"); ok {
				summary = iss
			}
		} else if v, ok := tb.Get("ark-response"); ok {
			requestID = v
			kind = "response"
			if iss, ok := tb.Get("issue"); ok {
				summary = iss
			} else {
				summary = "ark-response:" + v
			}
		}
		responseHandled, _ := tb.Get("response-handled")
		requestHandled, _ := tb.Get("request-handled")
		entries = append(entries, InboxEntry{
			Status:          statusVal,
			To:              toVal,
			From:            fromVal,
			Summary:         summary,
			Path:            f.Path,
			RequestID:       requestID,
			Kind:            kind,
			ResponseHandled: responseHandled,
			RequestHandled:  requestHandled,
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
		if ts := extractJSONLTimestamp(line); ts != nil {
			chunk.Attrs = []microfts2.Pair{{Key: []byte("timestamp"), Value: ts}}
		}
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
