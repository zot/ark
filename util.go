package ark

import (
	"fmt"
	"time"
)

// timeNow returns the current time. Test seam: reassign for deterministic tests.
var timeNow = time.Now

// ParseDate parses a date string: "2006-01-02", "2006-01-02T15:04:05", or
// a duration suffix like "24h", "7d" (meaning that long ago from now).
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
		"2006-01-02",
	} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %s (use 2006-01-02, 2006-01-02T15:04:05, or 24h/7d)", s)
}
