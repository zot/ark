package main

// CRC: crc-CLITree.md | Test: test-SessionIdentity.md | R3309, R3310

import (
	"os"
	"strings"
	"testing"
)

// TestResolveOwnSession covers the whole resolver. The case that matters is
// "flag disagrees": that is the shape a `--fork-session --resume` replay
// produces, and it fails invisibly at runtime — a consumer subscribed under a
// stale id blocks forever on a channel nobody publishes to, which looks exactly
// like a corpus with nothing to say. There is no failing call to notice, so this
// table is the only thing that will.
// Refs: R3309, R3310
func TestResolveOwnSession(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		flag       string
		want       string
		wantReport bool
	}{
		{"flag omitted resolves from env", "live-1", "", "live-1", false},
		{"flag agreeing is silent", "live-1", "live-1", "live-1", false},
		{"flag disagreeing loses and reports", "live-2", "stale-1", "live-2", true},
		{"empty env falls back to flag", "", "manual-1", "manual-1", false},
		{"neither resolves to empty", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(claudeSessionEnv, tc.env)
			got, report := resolveOwnSession(tc.flag)
			if got != tc.want {
				t.Errorf("session = %q, want %q", got, tc.want)
			}
			if (report != "") != tc.wantReport {
				t.Errorf("report presence = %v, want %v (report=%q)", report != "", tc.wantReport, report)
			}
			// A correction with no report is as bad as no correction: it repairs
			// the call and hides that sessions are being re-hosted.
			if tc.wantReport {
				if !strings.Contains(report, tc.flag) || !strings.Contains(report, tc.env) {
					t.Errorf("report must name both the stale flag %q and the live id %q; got %q",
						tc.flag, tc.env, report)
				}
			}
		})
	}
}

// TestOwnSessionResolverCallSites pins *where* the resolver is wired, because
// R3311's exclusion cannot be tested behaviourally without standing a server up
// and the regression it prevents is silent: `connections recall next --session`
// carries a pool secretary's composite tube identity, so resolving it from the
// environment would point every pool secretary at its parent's tube and it would
// quietly drain another agent's work.
//
// This is a structural assertion rather than a behavioural one, which is a
// deliberate trade: the two intended call sites are few and stable, and the
// alternative is no guard at all on the one mistake that matters most.
// Refs: R3311
func TestOwnSessionResolverCallSites(t *testing.T) {
	want := map[string]bool{
		"connRecallListenAction": true, // bloodhound listen delegates here (R3296)
		"luhmannNextAction":      true,
	}
	found := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		fn := ""
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "func ") {
				fn = strings.TrimPrefix(line, "func ")
				if i := strings.IndexAny(fn, "("); i >= 0 {
					fn = fn[:i]
				}
			}
			if strings.Contains(line, "applyOwnSession(") && fn != "applyOwnSession" {
				found[fn] = true
			}
		}
	}

	for fn := range want {
		if !found[fn] {
			t.Errorf("%s no longer resolves its own-session flag — a stale id there is silent (R3309)", fn)
		}
	}
	for fn := range found {
		if !want[fn] {
			t.Errorf("%s applies the own-session resolver; if that is `recall next`, it redirects every pool secretary to its parent's tube (R3311)", fn)
		}
	}
}
