package ark

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestIsWatchableDirMatchesScannerDescent is the Sleeping-Sentry guard for
// R2952: (F1) the live watcher descends exactly the directories the Scanner
// walks. The bug was watchDirRecursive unconditionally skipping every
// dot-prefixed subdirectory, so with dotfiles=true an edit under .scratch/
// never auto-indexed (watch coverage ⊊ scan coverage). This marches the worst
// input — a non-excluded dot-directory under a source — past the predicate and
// asserts it is watchable, and that the predicate equals the Scanner's descent
// rule (Classify isDir=true != Excluded) for every case.
// CRC: crc-DB.md | Seq: seq-file-change.md#1.1.1 | R2952
func TestIsWatchableDirMatchesScannerDescent(t *testing.T) {
	const dir = "/repo"
	cfg := &Config{
		Dotfiles:       true,
		DefaultExclude: []string{".git/", "node_modules/"},
		Sources:        []Source{{Dir: dir}},
	}
	db := &DB{config: cfg, matcher: &Matcher{Dotfiles: cfg.Dotfiles}}

	// The Scanner descends a directory iff Classify(isDir=true) is not Excluded
	// (scanner.go). The watcher must make the identical decision.
	includes, excludes := cfg.EffectivePatterns(cfg.Sources[0])
	scannerDescends := func(path string) bool {
		return db.matcher.Classify(includes, excludes, path, dir, true) != Excluded
	}

	cases := []struct {
		sub  string
		want bool
		why  string
	}{
		{".scratch", true, "dot-dir, not excluded, dotfiles=true — the F1 bug: edits here went stale"},
		{".scratch/nested", true, "descendant of a watched dot-dir"},
		{"src", true, "ordinary subdir — Unresolved, the Scanner still descends"},
		{".git", false, "excluded as a directory (.git/)"},
		{"node_modules", false, "excluded as a directory (node_modules/)"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.sub)
		got := db.IsWatchableDir(path)
		if got != c.want {
			t.Errorf("IsWatchableDir(%q) = %v, want %v — %s", c.sub, got, c.want, c.why)
		}
		// Watch coverage == scan coverage (R2952): the watcher's descent
		// decision must equal the Scanner's for every directory.
		if got != scannerDescends(path) {
			t.Errorf("R2952 parity broken: IsWatchableDir(%q)=%v but scanner-descends=%v",
				c.sub, got, scannerDescends(path))
		}
	}

	// A path outside every source is never watchable.
	if db.IsWatchableDir("/elsewhere/.scratch") {
		t.Error("IsWatchableDir returned true for a path under no source")
	}
}

// A link event survives the indexability filter and is never cached. This is
// the half of R3390 that is invisible when it fails: a directory symlink
// matches no *file* pattern, so the filter guarding the refresh drops the very
// event that repoints a tracked link and then writes the path into the
// negative cache, after which no later event on it is classified at all. The
// single-hop tier is defined on that event (R3384), so the association simply
// keeps returning its old, once-correct answer.
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3.5 | R3390
func TestClassifyEventDivertsLinkPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	indexable := filepath.Join(src, "a.md")
	plain := filepath.Join(src, "notes.txt")
	link := filepath.Join(src, "link") // no extension: matches nothing
	gone := filepath.Join(src, "vanished.md")
	for _, p := range []string{indexable, plain} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Sources:        []Source{{Dir: src}},
		DefaultInclude: []string{"*.md"},
	}
	srv := &Server{
		db:           &DB{config: cfg, matcher: &Matcher{}},
		ignoredPaths: make(map[string]struct{}),
	}

	cases := []struct {
		path string
		want eventDisposition
		why  string
	}{
		{indexable, eventIndex, "an indexable file takes the ordinary refresh path"},
		{link, eventLink, "a directory symlink matches no file pattern — the filter dropped it"},
		{plain, eventIgnore, "a plain non-indexable file is still uninteresting"},
		{gone, eventIndex, "a deleted indexable file must still reach the removal path"},
	}
	for _, c := range cases {
		if got := srv.classifyEvent(c.path); got != c.want {
			t.Errorf("classifyEvent(%s) = %v, want %v — %s", filepath.Base(c.path), got, c.want, c.why)
		}
	}

	// A path that vanished without matching any pattern could still have been
	// a tracked link; only the actor's table knows, so it goes to the table.
	goneUnknown := filepath.Join(src, "vanished")
	if got := srv.classifyEvent(goneUnknown); got != eventLink {
		t.Errorf("classifyEvent(vanished) = %v, want eventLink — a deleted link must reach the table", got)
	}

	// The load-bearing assertion. Caching the link path is what makes the
	// second and every subsequent event on it disappear without being seen.
	if _, cached := srv.ignoredPaths[link]; cached {
		t.Errorf("%q was written to the negative cache; every later link event is now invisible", link)
	}
	if _, cached := srv.ignoredPaths[plain]; !cached {
		t.Errorf("%q was not cached — the negative cache stopped doing its job", plain)
	}
}

// TestWatchOverflowReconciles: an overflow means the OS dropped change events,
// so the watcher must re-scan rather than only log; any other watcher error
// must not trigger a scan. The wrapped case pins errors.Is, so a backend that
// wraps the sentinel still reconciles.
// CRC: crc-Server.md | Seq: seq-file-change.md#2.2 | R3625
func TestWatchOverflowReconciles(t *testing.T) {
	captureCurationLog(t) // keep the watcher's log lines out of test output
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"overflow", fsnotify.ErrEventOverflow, 1},
		{"wrapped overflow", fmt.Errorf("read changes: %w", fsnotify.ErrEventOverflow), 1},
		{"other error", errors.New("access denied"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handleWatchError(tc.err, func() { calls++ })
			if calls != tc.want {
				t.Errorf("reconcile called %d times for %v, want %d", calls, tc.err, tc.want)
			}
		})
	}
}

// watchRig is a server over a real database (Init + Open, so the DB actor
// runs) with a real fsnotify watcher set up as startWatching sets it up
// (ark.toml, then every source), and the config routes `ark config` uses.
// Every mutation reaches the watcher the production way: configMutate, the
// coalesced reconcile on the actor, doReconcile, the walk's apply step.
type watchRig struct {
	t   *testing.T
	db  *DB
	srv *Server
	w   *fsnotify.Watcher
	mux *http.ServeMux
}

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	captureCurationLog(t)
	db := encodingDB(t)
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := w.Add(db.ConfigPath()); err != nil {
		t.Fatal(err)
	}
	srv := &Server{db: db, watcher: w}
	srv.watchSourceDirs()
	// Registered after encodingDB's Close, so it runs first: a test ends once
	// its watch condition holds, while the reconcile it kicked may still be
	// running its tail, and closing the database under that tail panics outside
	// any test. The first Sync queues behind any reconcile already posted
	// (actor order), so the poll cannot pass before that reconcile has begun.
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			busy, _ := Sync(db, func(db *DB) (bool, error) {
				return db.reconcileWalking || db.reconcileWalkPending, nil
			})
			if !busy && len(srv.currentlyIndexing()) == 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("the reconcile did not finish before the database closed")
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /config/add-source", srv.handleConfigAddSource)
	mux.HandleFunc("POST /config/remove-source", srv.handleConfigRemoveSource)
	mux.HandleFunc("POST /config/add-exclude", srv.handleConfigAddExclude)
	mux.HandleFunc("POST /config/remove-pattern", srv.handleConfigRemovePattern)
	return &watchRig{t: t, db: db, srv: srv, w: w, mux: mux}
}

// post sends one config mutation through its route.
func (r *watchRig) post(route string, fields map[string]string) {
	r.t.Helper()
	body, _ := json.Marshal(fields)
	if rec := serve(r.t, r.mux, "POST", route, string(body)); rec.Code != http.StatusOK {
		r.t.Fatalf("%s %v → %d: %s", route, fields, rec.Code, rec.Body)
	}
}

func (r *watchRig) watched(dir string) bool {
	for _, p := range r.w.WatchList() {
		if filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// await waits for the reconcile a mutation kicked to bring dir's watch to want.
func (r *watchRig) await(dir string, want bool, why string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && r.watched(dir) != want {
		time.Sleep(20 * time.Millisecond)
	}
	if r.watched(dir) != want {
		r.t.Fatalf("%s: watched(%s) = %v, want %v; watch list: %v", why, dir, !want, want, r.w.WatchList())
	}
}

// tree makes dir with the named subdirectories.
func tree(t *testing.T, dir string, subs ...string) {
	t.Helper()
	for _, sub := range append([]string{""}, subs...) {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAddedSourceIsWatched: a concrete source added while the server runs is
// watched, not only scanned. test-WatchCoverage.md.
// CRC: crc-Server.md | R3670
func TestAddedSourceIsWatched(t *testing.T) {
	r := newWatchRig(t)
	added := filepath.Join(t.TempDir(), "proj")
	tree(t, added, "sub")
	r.post("/config/add-source", map[string]string{"dir": added})
	r.await(added, true, "after add-source")
	r.await(filepath.Join(added, "sub"), true, "after add-source")
}

// TestNewSourceWatchedBeforeItsScan: a new source is watched before its scan,
// so an edit made while the scan runs is caught, as at startup (R359). The walk's
// fill-in would also watch it, but only after the walk returns, which the
// add-source test cannot tell apart. Here the source is added and doReconcile run
// inside one actor closure: the walk's apply step needs the actor too, so what is
// watched when the closure ends was watched before the scan could finish.
// test-WatchCoverage.md.
// CRC: crc-Server.md | R3670
func TestNewSourceWatchedBeforeItsScan(t *testing.T) {
	r := newWatchRig(t)
	added := filepath.Join(t.TempDir(), "proj")
	tree(t, added, "sub")
	watched, err := Sync(r.db, func(db *DB) (bool, error) {
		if err := db.MutateConfig(func(c *Config) error { return c.AddSource(added) }); err != nil {
			return false, err
		}
		r.srv.doReconcile(db)
		return r.watched(added) && r.watched(filepath.Join(added, "sub")), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !watched {
		t.Errorf("the new source was not watched when doReconcile returned, so its scan ran unwatched; watch list: %v", r.w.WatchList())
	}
}

// TestRemovedSourceIsUnwatched: removing a source stops its watches, entry by
// entry, and never the ark.toml watch. test-WatchCoverage.md.
// CRC: crc-Server.md | R3671
func TestRemovedSourceIsUnwatched(t *testing.T) {
	r := newWatchRig(t)
	gone := filepath.Join(t.TempDir(), "gone")
	tree(t, gone, "sub")
	r.post("/config/add-source", map[string]string{"dir": gone})
	r.await(filepath.Join(gone, "sub"), true, "after add-source")
	r.post("/config/remove-source", map[string]string{"dir": gone})
	r.await(gone, false, "after remove-source")
	r.await(filepath.Join(gone, "sub"), false, "after remove-source")
	if !r.watched(r.db.ConfigPath()) {
		t.Errorf("the ark.toml watch was removed; watch list: %v", r.w.WatchList())
	}
}

// TestRemovedNestedSourceKeepsOuterWatches: removing a source that sits inside
// a remaining source leaves the shared directories watched, which a recursive
// unwatch of the inner source would not. test-WatchCoverage.md.
// CRC: crc-Server.md | R3671
func TestRemovedNestedSourceKeepsOuterWatches(t *testing.T) {
	r := newWatchRig(t)
	outer := filepath.Join(t.TempDir(), "outer")
	inner := filepath.Join(outer, "inner")
	tree(t, inner, "deep")
	other := filepath.Join(t.TempDir(), "other")
	tree(t, other)
	r.post("/config/add-source", map[string]string{"dir": outer})
	r.post("/config/add-source", map[string]string{"dir": inner})
	r.post("/config/add-source", map[string]string{"dir": other})
	r.await(filepath.Join(inner, "deep"), true, "after add-source")
	r.post("/config/remove-source", map[string]string{"dir": inner})
	// A second source, removed after, marks when the first removal's reconcile
	// has run: the sweep that would wrongly drop inner/ has had its chance.
	r.post("/config/remove-source", map[string]string{"dir": other})
	r.await(other, false, "after remove-source of other")
	for _, dir := range []string{outer, inner, filepath.Join(inner, "deep")} {
		if !r.watched(dir) {
			t.Errorf("%s lost its watch when a source nested in outer/ was removed", dir)
		}
	}
}

// TestExcludeChangeMovesWatches: an exclude stops watches on the subtree it
// covers, and removing it watches that subtree again. The second half is the
// scan walk's fill-in: the source root is already watched, so only the walked
// directory list can show that skip/ is missing. test-WatchCoverage.md.
// CRC: crc-Server.md | R3671, R3672
func TestExcludeChangeMovesWatches(t *testing.T) {
	r := newWatchRig(t)
	src := filepath.Join(t.TempDir(), "src")
	tree(t, src, "skip", "keep")
	r.post("/config/add-source", map[string]string{"dir": src})
	skip := filepath.Join(src, "skip")
	r.await(skip, true, "after add-source")
	r.post("/config/add-exclude", map[string]string{"pattern": "skip/", "source": src})
	r.await(skip, false, "after excluding skip/")
	if !r.watched(filepath.Join(src, "keep")) {
		t.Errorf("excluding skip/ also dropped keep/")
	}
	r.post("/config/remove-pattern", map[string]string{"pattern": "skip/", "source": src})
	r.await(skip, true, "after removing the exclude")
}
