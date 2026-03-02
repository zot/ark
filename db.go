package ark

// CRC: crc-DB.md

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"microfts2"

	"github.com/anthropics/microvec"
)

// DB is the main ark facade. It coordinates microfts2, microvec,
// and the ark subdatabase.
type DB struct {
	fts     *microfts2.DB
	vec     *microvec.DB
	store   *Store
	config  *Config
	matcher *Matcher

	indexer  *Indexer
	scanner *Scanner
	search  *Searcher

	dbPath string
}

// InitOpts are options for creating a new ark database.
type InitOpts struct {
	EmbedCmd        string
	QueryCmd        string
	CharSet         string
	CaseInsensitive bool
	Aliases         map[rune]rune
}

// Init creates a new ark database at the given path.
func Init(dbPath string, opts InitOpts) error {
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	// Ensure newline alias for line-start matching
	aliases := opts.Aliases
	if aliases == nil {
		aliases = make(map[rune]rune)
	}
	if _, ok := aliases['\n']; !ok {
		aliases['\n'] = '\x01'
	}

	// Initialize microfts2 (creates the LMDB environment)
	ftsOpts := microfts2.Options{
		CharSet:         opts.CharSet,
		CaseInsensitive: opts.CaseInsensitive,
		Aliases:         aliases,
		MaxDBs:          8,
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

	// Register default chunking strategies
	defaultStrategies := map[string]string{
		"lines":         "microfts chunk-lines",
		"lines-overlap": "microfts chunk-lines-overlap -lines 50",
		"words-overlap": "microfts chunk-words-overlap",
	}
	for name, cmd := range defaultStrategies {
		if err := fts.AddStrategy(name, cmd); err != nil {
			return fmt.Errorf("register strategy %s: %w", name, err)
		}
	}

	// Write default settings
	if err := store.PutSettings(ArkSettings{Dotfiles: true}); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	// Write default config
	configPath := filepath.Join(dbPath, "ark.toml")
	if err := WriteDefaultConfig(configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// Open opens an existing ark database.
func Open(dbPath string) (*DB, error) {
	// Load config
	configPath := filepath.Join(dbPath, "ark.toml")
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Open microfts2 (opens the LMDB environment)
	fts, err := microfts2.Open(dbPath, microfts2.Options{MaxDBs: 8})
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

	db := &DB{
		fts:     fts,
		vec:     vec,
		store:   store,
		config:  config,
		matcher: matcher,
		indexer: &Indexer{fts: fts, vec: vec},
		scanner: &Scanner{config: config, matcher: matcher, fts: fts},
		search:  &Searcher{fts: fts, vec: vec},
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

// Config returns the current configuration.
func (db *DB) Config() *Config { return db.config }

// Add indexes files. If path is a directory, walks per config.
// If a file, adds directly with the given strategy.
func (db *DB) Add(paths []string, strategy string) error {
	if db.config.HasErrors() {
		return fmt.Errorf("config errors: %v", db.config.Errors)
	}

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
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
		if err := db.store.AddMissing(m.FileID, info.Filename, timeNow()); err != nil {
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

	var staleCount int
	for _, s := range stale {
		if s.Status == "stale" {
			staleCount++
		}
	}

	return &StatusInfo{
		Files:      len(stale), // total indexed (fresh + stale + missing in fts)
		Stale:      staleCount,
		Missing:    len(missing),
		Unresolved: len(unresolved),
	}, nil
}

// StatusInfo holds database status counts.
type StatusInfo struct {
	Files      int `json:"files"`
	Stale      int `json:"stale"`
	Missing    int `json:"missing"`
	Unresolved int `json:"unresolved"`
}

// Files returns all indexed file paths.
func (db *DB) Files() ([]string, error) {
	statuses, err := db.fts.StaleFiles()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(statuses))
	for _, s := range statuses {
		paths = append(paths, s.Path)
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
