package ark

// CRC: crc-Session.md | Seq: seq-session-search.md

import (
	"strings"
	"time"

	"github.com/zot/microfts2"
)

const defaultSessionTTL = 30 * time.Second

// sessionState is owned exclusively by the actor goroutine.
type sessionState struct {
	cache     *microfts2.ChunkCache
	lastQuery string
	timer     *time.Timer
	ttl       time.Duration
	fts       *microfts2.DB
}

// Session is a named closure actor that carries a ChunkCache across commands.
// R640, R641, R642, R643, R644, R645, R646, R647, R648
type Session struct {
	name string
	ch   chan func(*sessionState)
}

// NewSession creates a session and starts its actor loop.
func NewSession(name string, fts *microfts2.DB, ttl time.Duration) *Session {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	s := &Session{
		name: name,
		ch:   make(chan func(*sessionState), 1),
	}
	go s.loop(&sessionState{
		ttl: ttl,
		fts: fts,
	})
	return s
}

// RunSearch submits a search closure to the session actor.
// Applies the prefix test: if query is not a prefix of lastQuery
// and lastQuery is not a prefix of query, the cache is evicted.
// R647
func (s *Session) RunSearch(query string, fn func(*microfts2.ChunkCache) error) error {
	errCh := make(chan error, 1)
	s.ch <- func(st *sessionState) {
		// Prefix test: evict if queries diverge
		if st.lastQuery != "" && query != "" {
			if !strings.HasPrefix(query, st.lastQuery) && !strings.HasPrefix(st.lastQuery, query) {
				st.evictCache()
			}
		}
		st.ensureCache()
		err := fn(st.cache)
		st.lastQuery = query
		st.resetTimer(s.ch)
		errCh <- err
	}
	return <-errCh
}

func (s *Session) loop(st *sessionState) {
	for fn := range s.ch {
		fn(st)
	}
}

func (st *sessionState) ensureCache() {
	if st.cache == nil {
		st.cache = st.fts.NewChunkCache()
	}
}

func (st *sessionState) evictCache() {
	st.cache = nil
	st.lastQuery = ""
}

func (st *sessionState) resetTimer(ch chan func(*sessionState)) {
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(st.ttl, func() {
		ch <- func(st *sessionState) {
			st.evictCache()
		}
	})
}
