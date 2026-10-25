package ark

import (
	"fmt"
	"time"
)

// popFront removes and returns the head of a queue, clearing the vacated slot
// first. Advancing with q = q[1:] alone leaves the popped element reachable
// through the backing array until an append reallocates it, which keeps alive
// everything a popped closure captured (measured 2026-10-25: 1.36 GB of scan
// data after a re-chunk burst).
// CRC: crc-DB.md | R3607
func popFront[T any](q *[]T) T {
	head := (*q)[0]
	var zero T
	(*q)[0] = zero
	*q = (*q)[1:]
	return head
}

// timeNow returns the current time. Test seam: reassign for deterministic tests.
var timeNow = time.Now

// ParseDate parses a date string: "2006-01-02", "2006-01-02T15:04:05", or
// a duration suffix like "24h", "7d" (meaning that long ago from now).
// CRC: crc-DB.md | R1020 — handles the "2006-01-02 15:04" space-separated date+time form.
func ParseDate(s string) (time.Time, error) {
	if len(s) > 1 {
		suffix := s[len(s)-1]
		numStr := s[:len(s)-1]
		switch suffix {
		case 'h', 'm', 's':
			d, err := time.ParseDuration(s)
			if err == nil {
				return time.Now().Add(-d), nil
			}
		case 'd':
			var days int
			if _, err := fmt.Sscanf(numStr, "%d", &days); err == nil {
				return time.Now().AddDate(0, 0, -days), nil
			}
		}
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %s (use 2006-01-02, 2006-01-02T15:04:05, or 24h/7d)", s)
}
