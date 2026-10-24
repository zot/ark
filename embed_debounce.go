package ark

// CRC: crc-Server.md | R3579, R3580

import "time"

// Windows for embedding files stored between reconciles (R3579): the pending
// set is embedded once it has been quiet this long, and never later than
// embedMaxWait after its first path joined, so a file that changes every ten
// seconds cannot hold the set off forever.
const (
	embedQuietWindow = 15 * time.Second
	embedMaxWait     = time.Minute
)

// embedDebounce is the pending set of stored files awaiting a limited embed
// pass, and the timing that decides when to run it. It is server state owned
// by the DB actor, like the embed-pass guard, so it holds no lock. At most one
// timer is outstanding: a note while one is armed only moves the due time, and
// a timer that fires early re-arms for the remainder.
// CRC: crc-Server.md | R3579
type embedDebounce struct {
	quiet, maxWait time.Duration
	pending        map[string]struct{}
	first, last    time.Time // when the first and the latest pending path joined
	armed          bool      // a timer is outstanding; survives clear, since that timer still fires
}

// due is when the pending set should be embedded: quiet after the last path,
// but no later than maxWait after the first. R3579
func (d *embedDebounce) due() time.Time {
	quiet, ceiling := d.last.Add(d.quiet), d.first.Add(d.maxWait)
	if ceiling.Before(quiet) {
		return ceiling
	}
	return quiet
}

// note adds path at now. It reports whether the caller must arm a timer, and
// for how long; while one is outstanding it only records the path. R3579
func (d *embedDebounce) note(path string, now time.Time) (bool, time.Duration) {
	if d.pending == nil {
		d.pending = make(map[string]struct{})
		d.first = now
	}
	d.pending[path] = struct{}{}
	d.last = now
	if d.armed {
		return false, 0
	}
	d.armed = true
	return true, d.due().Sub(now)
}

// fire handles the outstanding timer at now. Before the set is due it returns
// the delay to re-arm for; once due it takes the set. An empty set (cleared by
// a full pass) yields neither. R3579
func (d *embedDebounce) fire(now time.Time) ([]string, time.Duration) {
	d.armed = false
	if d.pending == nil {
		return nil, 0
	}
	if due := d.due(); now.Before(due) {
		d.armed = true
		return nil, due.Sub(now)
	}
	paths := make([]string, 0, len(d.pending))
	for p := range d.pending {
		paths = append(paths, p)
	}
	d.clear()
	return paths, 0
}

// clear drops the pending set. A full pass clears it as it starts, since it
// covers every file stored before it began. R3580
func (d *embedDebounce) clear() {
	d.pending = nil
	d.first, d.last = time.Time{}, time.Time{}
}
