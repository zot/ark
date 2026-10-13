// CRC: crc-CLI.md | Test: test-AppActivation.md
package main

import (
	"bytes"
	"testing"

	cli "github.com/zot/ui-engine/cli"
)

// Test: conflicts print one entry per losing copy, or a none line (R3523)
func TestPrintViewdefConflicts(t *testing.T) {
	var buf bytes.Buffer
	printViewdefConflicts(&buf, nil)
	if got := buf.String(); got != "no viewdef conflicts\n" {
		t.Errorf("empty list printed %q", got)
	}
	buf.Reset()
	printViewdefConflicts(&buf, []cli.ViewdefCollision{{
		Key: "T.DEFAULT", Winner: "aa", WinnerPath: "/w/T.DEFAULT.html",
		Loser: "zz", LoserPath: "/l/T.DEFAULT.html",
	}})
	want := "T.DEFAULT: aa wins over zz\n  /w/T.DEFAULT.html\n  /l/T.DEFAULT.html\n"
	if got := buf.String(); got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}
