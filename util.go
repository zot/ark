package ark

import "time"

// timeNow returns the current time. Test seam: reassign for deterministic tests.
var timeNow = time.Now
