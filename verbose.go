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
// R728, R729: Format is [vN] message, using log.Printf.
func Logv(level int, format string, args ...any) {
	if verbosity >= level {
		indent := strings.Repeat(" ", level)
		log.Printf("[v%d]%s "+format, append([]any{level, indent}, args...)...)
	}
}
