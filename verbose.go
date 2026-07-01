package ark

// CRC: crc-CLI.md

import (
	"log"
	"strings"
)

// verbosity is the global verbose level (0–4).
// R724: Set from -v flags parsed before subcommand dispatch.
var verbosity int

// SetVerbosity sets the global verbose level.
func SetVerbosity(level int) {
	verbosity = level
}

// Logv logs a message if the global verbosity >= level.
// R728, R729, R730: Format is [vN] message via log.Printf, so output flows
// through the default logger's MultiWriter (stderr + ~/.ark/logs) when the
// server is running.
// R733, R734: the single `verbosity >= level` gate realizes every tier —
// deeper levels 3 (-vvv) and 4 (-vvvv) run through this same check, with no
// separate mechanism; call sites emit at those tiers for finer detail.
func Logv(level int, format string, args ...any) {
	if verbosity >= level {
		indent := strings.Repeat(" ", level)
		log.Printf("[v%d]%s "+format, append([]any{level, indent}, args...)...)
	}
}
