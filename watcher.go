package ark

// CRC: crc-Server.md | Seq: seq-file-change.md

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	throttleWindow       = 1 * time.Second
	maxWaitCeiling       = 30 * time.Second
	configDebounceWindow = 500 * time.Millisecond // ark.toml fsnotify burst coalescing
)

// startWatching creates an fsnotify watcher for ark.toml and all source
// directories (recursive). Starts the event loop goroutine. If watching
// fails, logs a warning and continues — watching is optional.
func (srv *Server) startWatching() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("watch: failed to create watcher: %v", err)
		return
	}
	srv.watcher = w
	srv.ignoredPaths = make(map[string]struct{})

	// Watch ark.toml
	configPath := srv.db.ConfigPath()
	if err := w.Add(configPath); err != nil {
		log.Printf("watch: failed to watch %s: %v", configPath, err)
	}

	// Watch source directories (recursive)
	srv.watchSourceDirs()

	go srv.watchLoop()
	log.Println("watch: started")
}

// watchSourceDirs adds fsnotify watches for all resolved source directories
// and their subdirectories. Safe to call multiple times — fsnotify deduplicates.
// CRC: crc-Server.md | Seq: seq-file-change.md | R349
func (srv *Server) watchSourceDirs() {
	if srv.watcher == nil {
		return
	}
	for _, src := range srv.db.Config().Sources {
		if IsGlob(src.Dir) {
			continue
		}
		srv.watchDirRecursive(src.Dir)
	}
}

// watchDirRecursive adds a watch for dir and all its watchable subdirectories.
// R350, R2952: descent matches the Scanner — a subtree is watched iff
// DB.IsWatchableDir (Classify isDir=true is not Excluded), so dot-dirs like
// .scratch/ are watched (dotfiles=true) while directory excludes like .git/
// are skipped. Replaces the prior unconditional dot-prefix skip, which made
// edits under .scratch/ never auto-index (watch coverage ⊊ scan coverage).
func (srv *Server) watchDirRecursive(dir string) {
	if srv.watcher == nil {
		return
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			if !srv.db.IsWatchableDir(path) {
				return filepath.SkipDir
			}
			if watchErr := srv.watcher.Add(path); watchErr != nil {
				log.Printf("watch: failed to watch %s: %v", path, watchErr)
			}
		}
		return nil
	})
}

// unwatchDir removes watches for a directory and its subdirectories.
func (srv *Server) unwatchDir(dir string) {
	if srv.watcher == nil {
		return
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			srv.watcher.Remove(path)
		}
		return nil
	})
}

// watchLoop is the event processing goroutine. Implements throttled
// on-notify: immediate response to the first event, then a throttle
// window. Events during the window accumulate path identities and
// trigger one per-path re-index on expiry. Max wait ceiling prevents
// event storms from starving the index.
//
// Anchors throughout this function reference steps in the "Throttled
// On-Notify" diagram of seq-file-change.md.
//
// CRC: crc-Server.md | Seq: seq-file-change.md#1 | R348, R352, R353, R354, R355, R356, R357, R387, R389, R390, R391, R393, R394, R395, R991, R992
func (srv *Server) watchLoop() {
	configPath := srv.db.ConfigPath()
	var (
		throttle       *time.Timer
		pending        map[string]struct{}
		ceilingStart   time.Time
		configDebounce *time.Timer // debounces ark.toml ReloadConfig+reconcile
	)

	for {
		select {
		case event, ok := <-srv.watcher.Events:
			if !ok {
				return // watcher closed
			}
			if !relevant(event) {
				continue
			}

			// New directory created — add a watch for it.
			//
			// Lstat, so a directory *symlink* is not a directory here. A link
			// is recorded and not descended (R3373), and the target is either
			// already watched through its real path or deliberately outside
			// every source (R3355). watchDirRecursive would in fact decline
			// anyway — filepath.Walk lstats its own root — but stating it here
			// is what keeps the invariant readable at the branch that decides.
			// Seq: seq-file-change.md#1.1
			if event.Has(fsnotify.Create) {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() {
					srv.watchDirRecursive(event.Name) // Seq: seq-file-change.md#1.1.1
				}
			}

			// ark.toml changed — debounce to coalesce fsnotify event
			// bursts (a single editor save commonly fires 3-7 events)
			// and consecutive saves. Without this, each event triggers
			// a full ReloadConfig + reconcile sweep/scan/refresh cycle,
			// which queues behind itself and blocks the actor for tens
			// of seconds while the user is still typing. (R992)
			// Seq: seq-file-change.md#1.2
			if event.Name == configPath || filepath.Base(event.Name) == "ark.toml" {
				if configDebounce == nil {
					configDebounce = time.NewTimer(configDebounceWindow)
				} else {
					if !configDebounce.Stop() {
						<-configDebounce.C
					}
					configDebounce.Reset(configDebounceWindow)
				}
				continue
			}

			// Indexability filter — skip non-indexable files via negative
			// cache + pattern check, and divert what is the link table's
			// business rather than a file's.
			// Seq: seq-file-change.md#1.3
			switch srv.classifyEvent(event.Name) {
			case eventIgnore:
				continue
			case eventLink:
				srv.syncLink(event.Name) // Seq: seq-file-change.md#1.3.5.2
				continue
			}

			// Source file changed — per-path index update (R991).
			// Seq: seq-file-change.md#1.4
			if throttle == nil {
				// Immediate mode — process this path now, start throttle.
				// Seq: seq-file-change.md#1.4.1
				log.Printf("watch: file changed: %s", event.Name)
				srv.indexPaths([]string{event.Name})     // Seq: seq-file-change.md#1.4.1.1
				throttle = time.NewTimer(throttleWindow) // Seq: seq-file-change.md#1.4.1.2
				pending = nil
				ceilingStart = time.Now()
			} else {
				// In throttle window — accumulate the path; filesystem has the truth.
				// Seq: seq-file-change.md#1.4.2
				if pending == nil {
					pending = make(map[string]struct{})
				}
				pending[event.Name] = struct{}{}
				// Max wait ceiling — force a re-index regardless of further events.
				// Seq: seq-file-change.md#1.5
				if time.Since(ceilingStart) >= maxWaitCeiling {
					log.Println("watch: max wait ceiling reached, forcing re-index")
					throttle.Stop()
					srv.indexPaths(pathSetToSlice(pending))
					pending = nil
					throttle = time.NewTimer(throttleWindow)
					ceilingStart = time.Now()
				}
			}

		case err, ok := <-srv.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watch: error: %v", err)

		case <-timerChan(configDebounce):
			// Quiet period elapsed since the last ark.toml event —
			// reload config and force a full schedule re-arm. Drop
			// every queue entry, then re-arm from the sources of
			// truth: disk schedule chunks (via ScanScheduleLogs's
			// arm-from-chunk pass) + chimes.md (via
			// ArmChimesFromFile). Strictly stronger than per-case
			// pruning: a tag removed from `[schedule.tag.X]` falls
			// out (no chunk passes chunkInCurrentConfig), a tag
			// newly suppressed falls out (EnsureUpcoming no-ops), a
			// chime config flip is re-arming uniformly. Also prune
			// the watchdog tmp docs so a belatedly-declared tag
			// clears its stale orphan/typo warnings.
			log.Println("watch: ark.toml settled, reloading config")
			srv.ignoredPaths = make(map[string]struct{}) // Seq: seq-file-change.md#1.2.2
			if err := srv.db.ReloadConfig(); err != nil {
				log.Printf("watch: config reload failed: %v", err)
			} else {
				srv.db.Config().EnsureArkSource()
				srv.db.Config().EnsureLuhmannSource() // R3135
				if srv.pubsub != nil {                // R3595: subscriptions follow the reload
					cfg := srv.db.Config()
					srv.pubsub.SetExcludes(cfg.SystemExcludes(), cfg.SearchExcludes())
				}
				if srv.scheduler != nil {
					// Re-point scheduler at the new config pointer.
					// ReloadConfig swaps db.config; the scheduler's
					// own pointer (captured at NewEventScheduler)
					// would otherwise stay stale.
					srv.scheduler.SetConfig(srv.db.Config())
					if n := srv.scheduler.DropAll(); n > 0 {
						log.Printf("watch: dropped %d queue entry/entries; re-arming", n)
					}
					srv.scheduler.WriteTmpLog = func(path string, content []byte) error {
						return SyncVoid(srv.db, func(db *DB) error {
							err := db.UpdateTmpFile(path, "markdown", content)
							if err != nil {
								_, err = db.AddTmpFile(path, "markdown", content)
							}
							return err
						})
					}
					srv.scheduler.ReadTmpLog = func(path string) ([]byte, error) {
						return Sync(srv.db, func(db *DB) ([]byte, error) {
							return db.TmpContent(path)
						})
					}
					if err := srv.scheduler.ScanScheduleLogs(); err != nil {
						log.Printf("watch: ScanScheduleLogs error: %v", err)
					}
					if err := srv.scheduler.ArmChimesFromFile(srv.db.Path()); err != nil {
						log.Printf("watch: ArmChimesFromFile error: %v", err)
					}
					srv.scheduler.WriteTmpLog = nil
					srv.scheduler.ReadTmpLog = nil
				}
				if srv.pubsub != nil {
					srv.pubsub.PruneWatchdog(srv.db.Config())
				}
				srv.reconcile() // Seq: seq-file-change.md#1.2.1
			}
			configDebounce = nil

		case <-timerChan(throttle):
			if len(pending) > 0 {
				// Events arrived during window — single re-index of accumulated paths.
				// Seq: seq-file-change.md#1.4.3.1
				log.Println("watch: throttle expired with pending changes, re-indexing")
				srv.indexPaths(pathSetToSlice(pending)) // Seq: seq-file-change.md#1.4.3.1.1
				pending = nil
				throttle = time.NewTimer(throttleWindow) // Seq: seq-file-change.md#1.4.3.1.2
				// ceilingStart unchanged — keep the original burst
			} else {
				// No events during window — back to immediate mode.
				// Seq: seq-file-change.md#1.4.3.2
				throttle = nil // Seq: seq-file-change.md#1.4.3.2.1
			}
		}
	}
}

// eventDisposition is what the watch loop does with one event path.
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3 | R3390
type eventDisposition int

const (
	// eventIndex: an indexable file — throttle and refresh it.
	eventIndex eventDisposition = iota
	// eventLink: the link table's business, not a file's. R3390
	eventLink
	// eventIgnore: nothing here for ark.
	eventIgnore
)

// classifyEvent decides what the watch loop does with a path.
//
// The link arm is the reason this is three-way rather than the boolean it
// used to be. A directory symlink matches no *file* pattern, so the
// indexability check alone drops the very event that repoints a tracked link
// — and then caches the path as uninteresting for the life of the process, so
// no later event on it is examined either. The single-hop tier is defined on
// that event (R3384), which makes the cheapest and most-used tier verify
// against a premise nothing re-derives. A link path is therefore never added
// to the negative cache.
//
// Runs on the watch-loop goroutine, which is why linkCandidate is made of
// nothing but lstat: the link table is DB-actor state (R3349), so a test
// needing the table could not be asked here.
//
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3 | R388, R392, R3390
func (srv *Server) classifyEvent(path string) eventDisposition {
	if _, ignored := srv.ignoredPaths[path]; ignored {
		return eventIgnore // Seq: seq-file-change.md#1.3.1
	}
	if srv.db.IsIndexable(path) { // Seq: seq-file-change.md#1.3.2
		return eventIndex // Seq: seq-file-change.md#1.3.4
	}
	if linkCandidate(path) {
		return eventLink // Seq: seq-file-change.md#1.3.5.1
	}
	srv.ignoredPaths[path] = struct{}{} // Seq: seq-file-change.md#1.3.3
	return eventIgnore
}

// linkCandidate reports whether a path might be the link table's business,
// using only the filesystem.
//
// It answers the two transitions lstat can state on its own: the path **is** a
// symlink (created or repointed), and the path is **gone** (deleted), where
// only the actor's table knows whether what vanished was a tracked link. A
// real directory is included for the same reason and at the same near-zero
// cost — a link replaced by one leaves no other trace.
//
// What it cannot see is a link replaced in place by a non-indexable regular
// file: nothing distinguishes that file from any other uninteresting file.
// Gap O196.
//
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3.5 | R3390
func linkCandidate(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return true
	}
	return !fi.Mode().IsRegular()
}

// syncLink hands one path to the link table on the actor that owns it.
// Fire-and-forget: the watch loop does not need the result, and the work is
// one readlink chain rather than anything worth coalescing.
// CRC: crc-Server.md | Seq: seq-file-change.md#1.3.5.2 | R3390
func (srv *Server) syncLink(path string) {
	srv.db.Do(func(db *DB) { db.SyncLinkPath(path) })
}

// pathSetToSlice converts a deduplicated path set to a slice.
func pathSetToSlice(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

// timerChan returns the timer's channel, or a nil channel if timer is nil.
// A nil channel blocks forever in select, which is exactly what we want
// when no throttle is active.
func timerChan(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// relevant returns true if the fsnotify event should trigger processing.
func relevant(event fsnotify.Event) bool {
	return event.Has(fsnotify.Create) ||
		event.Has(fsnotify.Write) ||
		event.Has(fsnotify.Remove) ||
		event.Has(fsnotify.Rename)
}
