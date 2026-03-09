package ark

// CRC: crc-Server.md | Seq: seq-file-change.md

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	throttleWindow = 1 * time.Second
	maxWaitCeiling = 30 * time.Second
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

// watchDirRecursive adds a watch for dir and all its subdirectories.
func (srv *Server) watchDirRecursive(dir string) {
	if srv.watcher == nil {
		return
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() {
			// Skip hidden directories (except the root dir itself)
			if path != dir && strings.HasPrefix(filepath.Base(path), ".") {
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
// window. Events during the window are ignored. Window expiry triggers
// a single reconcile if events arrived. Max wait ceiling prevents
// event storms from starving the index.
func (srv *Server) watchLoop() {
	configPath := srv.db.ConfigPath()
	var (
		throttle     *time.Timer
		dirty        bool
		ceilingStart time.Time
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

			// New directory created — add a watch for it
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					srv.watchDirRecursive(event.Name)
				}
			}

			if event.Name == configPath || filepath.Base(event.Name) == "ark.toml" {
				// ark.toml changed — reload config + full reconcile
				log.Println("watch: ark.toml changed, reloading config")
				srv.ignoredPaths = make(map[string]struct{}) // invalidate negative cache
				if err := srv.db.ReloadConfig(); err != nil {
					log.Printf("watch: config reload failed: %v", err)
				} else {
					srv.db.Config().EnsureArkSource()
					srv.reconcile()
				}
				continue
			}

			// Skip non-indexable files (negative cache + pattern check)
			if _, ignored := srv.ignoredPaths[event.Name]; ignored {
				continue
			}
			if !srv.db.IsIndexable(event.Name) {
				srv.ignoredPaths[event.Name] = struct{}{}
				continue
			}

			// Source file changed
			if throttle == nil {
				// Immediate mode — process now, start throttle
				log.Printf("watch: file changed: %s", event.Name)
				srv.reconcile()
				throttle = time.NewTimer(throttleWindow)
				dirty = false
				ceilingStart = time.Now()
			} else {
				// In throttle window — just mark dirty
				dirty = true
				// Check max wait ceiling
				if time.Since(ceilingStart) >= maxWaitCeiling {
					log.Println("watch: max wait ceiling reached, forcing reconcile")
					throttle.Stop()
					srv.reconcile()
					throttle = time.NewTimer(throttleWindow)
					dirty = false
					ceilingStart = time.Now()
				}
			}

		case err, ok := <-srv.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watch: error: %v", err)

		case <-timerChan(throttle):
			if dirty {
				// Events arrived during window — reconcile + new window
				log.Println("watch: throttle expired with pending changes, reconciling")
				srv.reconcile()
				throttle = time.NewTimer(throttleWindow)
				dirty = false
				// Keep ceilingStart from the original burst
			} else {
				// No events during window — back to immediate mode
				throttle = nil
			}
		}
	}
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
