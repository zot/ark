package ark

// CRC: crc-Scanner.md | Seq: seq-empty-file-skip.md | R1644, R1650, R1651

import "time"

// EmptyFiles remembers paths whose size on disk is zero so repeat
// scans can skip them without reading or invoking the chunker.
// Empty files produce no chunks; without this set, every scan
// re-attempts them and the file-based chunkers (notably PDF) log
// a parse error for each retry.
//
// Access is serialized through the DB actor: Scanner.Scan runs on
// the actor goroutine, and evictions that touch LMDB are routed
// through the write queue. No mutex is required. R1644, R1650, R1651
type EmptyFiles struct {
	known map[string]time.Time
}

func NewEmptyFiles() *EmptyFiles {
	return &EmptyFiles{known: make(map[string]time.Time)}
}

// Has reports whether path is recorded with the given mtime. R1646
func (e *EmptyFiles) Has(path string, mtime time.Time) bool {
	prev, ok := e.known[path]
	return ok && prev.Equal(mtime)
}

// Record stores path with the given mtime, replacing any prior entry. R1647
func (e *EmptyFiles) Record(path string, mtime time.Time) {
	e.known[path] = mtime
}

// Forget removes path from the set. Used when the file transitions
// away from empty (size > 0 on a later scan) so that a subsequent
// truncation-to-zero is re-detected.
func (e *EmptyFiles) Forget(path string) {
	delete(e.known, path)
}
