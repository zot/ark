package main

// CRC: crc-CLI.md | Test: test-BloodhoundCLI.md

import (
	"context"
	"testing"

	ucli "github.com/urfave/cli/v3"
)

// findCmd walks the command tree by path, e.g. findCmd(t, "bloodhound", "listen").
func findCmd(t *testing.T, path ...string) *ucli.Command {
	t.Helper()
	cmds := arkCommands()
	var cur *ucli.Command
	for _, want := range path {
		cur = nil
		for _, c := range cmds {
			if c.Name == want {
				cur = c
				break
			}
		}
		if cur == nil {
			t.Fatalf("command %q not found", want)
		}
		cmds = cur.Commands
	}
	return cur
}

// TestBloodhoundListenAliasResolves is the wiring Sentry for R3296. The listen
// action reads the flag as "ambient" while the node declares it as "recall" with
// "ambient" an alias — so if urfave ever stopped resolving aliases through
// Bool(), `--recall` would parse fine and quietly fail to subscribe. Ambient
// recall would then simply never fire, with nothing reporting why: the failure
// class this whole carve exists to eliminate.
//
// Asserts against a real parse rather than the flag declarations, because
// declaring the alias and *resolving* it are different things.
func TestBloodhoundListenAliasResolves(t *testing.T) {
	for _, spelling := range []string{"--recall", "--ambient"} {
		t.Run(spelling, func(t *testing.T) {
			var sawAmbient, sawRecall bool
			cmd := &ucli.Command{
				Name:  "listen",
				Flags: findCmd(t, "bloodhound", "listen").Flags,
				Action: func(_ context.Context, c *ucli.Command) error {
					sawAmbient = c.Bool("ambient")
					sawRecall = c.Bool("recall")
					return nil
				},
			}
			if err := cmd.Run(context.Background(), []string{"listen", spelling}); err != nil {
				t.Fatalf("parse %s: %v", spelling, err)
			}
			// The action reads "ambient"; both spellings must reach it, or the
			// ambient opt-in silently never happens.
			if !sawAmbient {
				t.Errorf("%s did not resolve through Bool(\"ambient\") — ambient would never subscribe", spelling)
			}
			if !sawRecall {
				t.Errorf("%s did not resolve through Bool(\"recall\")", spelling)
			}
		})
	}
}

// TestRecallNextDeclaresPool guards R3295's declared-not-inferred rule at the
// CLI edge: if the node stops declaring --pool, urfave rejects it before the
// action runs and a pool secretary silently reverts to per-session scope.
func TestRecallNextDeclaresPool(t *testing.T) {
	got := map[string]bool{}
	for _, f := range findCmd(t, "connections", "recall", "next").Flags {
		for _, n := range f.Names() {
			got[n] = true
		}
	}
	for _, want := range []string{"pool", "session"} {
		if !got[want] {
			t.Errorf("recall next must declare --%s", want)
		}
	}
}
