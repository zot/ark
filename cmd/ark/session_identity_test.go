package main

// CRC: crc-CLITree.md | Test: test-SessionIdentity.md | R3313, R3314, R3315, R3316, R3317, R3318, R3320

import (
	"context"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"
)

// clientSessionNodes are the verbs that act *as* the calling session. Kept in
// one place because two tests assert complementary halves: each declares the
// need, and none still carries a `--session` flag for identity.
var clientSessionNodes = [][]string{
	{"bloodhound", "listen"},
	{"connections", "recall", "listen"},
	{"connections", "recall", "next"},
	{"luhmann", "next"},
	{"luhmann", "events"},
}

// TestResolveClientSession covers the whole resolver. The case that matters most
// is the fork: a session re-hosted with `--fork-session --resume` replays skill
// text rendered before the fork, so a typed id can be stale while the live
// process variable is right. That failure is invisible at runtime — a consumer
// subscribed under a stale id blocks forever on a channel nobody publishes to,
// which looks exactly like a corpus with nothing to say. There is no failing
// call to notice, so this table is the only thing that will.
// Refs: R3314, R3315
func TestResolveClientSession(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		force   string
		want    string
		wantErr bool
	}{
		{"no force resolves from the environment", "live-1", "", "live-1", false},
		// State A's flag was decorative: with the environment set, no flag value
		// could change the outcome. This is the case proving the replacement
		// accepts the input it advertises, and it restores the deliberate
		// cross-session use state A removed silently.
		{"force wins over a set environment", "live-1", "other-2", "other-2", false},
		{"force with an empty environment", "", "manual-1", "manual-1", false},
		{"neither resolving is an error", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(claudeSessionEnv, tc.env)
			got, err := resolveClientSession(tc.force)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("session = %q, want %q", got, tc.want)
			}
			// The remedy is the whole reason this errors instead of passing an
			// empty value through: state A let each body's own required-flag
			// message fire, which here would name a flag that no longer exists.
			if tc.wantErr && !strings.Contains(err.Error(), "--"+forceSessionFlag) {
				t.Errorf("error must name --%s as the remedy; got %q", forceSessionFlag, err)
			}
		})
	}
}

// TestClientSessionNodesDeclareTheNeed replaces state A's guard, which had to
// grep ark's own source for `applyOwnSession(` call sites. That proxy could not
// see a verb which simply never called the resolver, and `ark luhmann events`
// was missed exactly that way — R3145 routes events onto the *caller's* own
// tube, so its `--session` was always an own-session claim.
// Refs: R3313
func TestClientSessionNodesDeclareTheNeed(t *testing.T) {
	for _, path := range clientSessionNodes {
		name := strings.Join(path, " ")
		if !declaresClientSession(findCmd(t, path...)) {
			t.Errorf("`ark %s` no longer declares a client-session need — a stale id there is silent, "+
				"and the verb keeps working in an ordinary session, failing only after a fork (R3313)", name)
		}
	}
}

// TestClientSessionNodesTakeNoSessionFlag is the migration's completion test in
// executable form. The trap is a `--session` flag creeping back onto a declaring
// node, where it would read as an accepted input and be silently ignored — which
// is precisely the decorative flag state A shipped.
// Refs: R3313, R3314, R3318
func TestClientSessionNodesTakeNoSessionFlag(t *testing.T) {
	for _, path := range clientSessionNodes {
		name := strings.Join(path, " ")
		for _, f := range findCmd(t, path...).Flags {
			for _, n := range f.Names() {
				if n == "session" {
					t.Errorf("`ark %s` declares --session again; identity is a node property, "+
						"and a flag here is decorative (R3313/R3314)", name)
				}
			}
		}
	}
}

// TestForceSessionIsGlobal — a per-node flag would have to be added by hand to
// each declaring node, which is R3311's enumeration one layer down.
// Refs: R3316
func TestForceSessionIsGlobal(t *testing.T) {
	root := buildArkCommand()
	found := false
	for _, f := range root.Flags {
		for _, n := range f.Names() {
			if n == forceSessionFlag {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("--%s must be a global flag on the root, beside --dir (R3316)", forceSessionFlag)
	}
}

// TestRecallNextTube guards the renamed flag. Losing it entirely would leave a
// pool secretary with no way to name its own tube; keeping it spelled `session`
// would preserve the overload the rename ends.
// Refs: R3318
func TestRecallNextTube(t *testing.T) {
	got := map[string]bool{}
	for _, f := range findCmd(t, "connections", "recall", "next").Flags {
		for _, n := range f.Names() {
			got[n] = true
		}
	}
	if !got["tube"] {
		t.Error("recall next must declare --tube (a pool secretary's composite subscription address, R3318)")
	}
	if got["pool"] {
		t.Error("--pool is retired (T273); the watcher addresses work instead of the secretary declaring a role")
	}
}

// TestRecallNextURLSeparatesTubeFromIdentity pins the wire split. If both values
// arrived under one parameter the server could not tell which was which, and the
// field separation would exist only in the flag names.
// Refs: R3317, R3319
func TestRecallNextURLSeparatesTubeFromIdentity(t *testing.T) {
	url := recallNextURL(7, "luhmann-9-3", "live-1")
	for _, want := range []string{"nonce=7", "tube=luhmann-9-3", clientSessionWireField + "=live-1"} {
		if !strings.Contains(url, want) {
			t.Errorf("url %q missing %q", url, want)
		}
	}
	// A bare `session=` would be the overload back on the wire, where the
	// completion test cannot see it.
	if strings.Contains(url, "session=luhmann-9-3") || strings.Contains(url, "&session=") {
		t.Errorf("url must not carry the tube under a `session` parameter: %q", url)
	}
	// The legacy bare-curate path has no subscription address; identity is
	// still sent, because a declaring node always has one (R3315).
	bare := recallNextURL(7, "", "live-1")
	if strings.Contains(bare, "tube=") {
		t.Errorf("empty tube must be omitted, not sent blank: %q", bare)
	}
	if !strings.Contains(bare, clientSessionWireField+"=live-1") {
		t.Errorf("identity must be sent even with no tube: %q", bare)
	}
}

// TestDeclaringNodeBodyNeverRunsWithoutIdentity is R3320 — the property that
// makes state A's exclusion list unnecessary rather than merely shorter. Under
// state A the rule was applied by remembering to call the resolver in each body,
// so an omission was silent and shaped exactly like the bug it prevents.
// Refs: R3315, R3320
func TestDeclaringNodeBodyNeverRunsWithoutIdentity(t *testing.T) {
	t.Setenv(claudeSessionEnv, "")
	ran := false
	leaf := needsClientSession(&ucli.Command{
		Name:   "leaf",
		Action: func(context.Context, *ucli.Command) error { ran = true; return nil },
	})
	root := &ucli.Command{
		Name:     "ark",
		Flags:    []ucli.Flag{&ucli.StringFlag{Name: forceSessionFlag}},
		Commands: []*ucli.Command{leaf},
	}
	err := root.Run(context.Background(), []string{"ark", "leaf"})
	if err == nil {
		t.Error("a declaring node with no resolvable identity must fail (R3315)")
	}
	if ran {
		t.Error("the Action ran with no identity; resolution must complete before the body, " +
			"or a body can silently proceed with an empty session (R3320)")
	}

	// And the positive half: with an identity available, the body runs and reads
	// back exactly what was resolved.
	ran = false
	var seen string
	leaf2 := needsClientSession(&ucli.Command{
		Name: "leaf",
		Action: func(_ context.Context, c *ucli.Command) error {
			ran = true
			seen = clientSession(c)
			return nil
		},
	})
	root2 := &ucli.Command{
		Name:     "ark",
		Flags:    []ucli.Flag{&ucli.StringFlag{Name: forceSessionFlag}},
		Commands: []*ucli.Command{leaf2},
	}
	if err := root2.Run(context.Background(), []string{"ark", "--" + forceSessionFlag, "forced-1", "leaf"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ran {
		t.Error("the Action did not run with a resolvable identity")
	}
	if seen != "forced-1" {
		t.Errorf("clientSession() = %q, want %q", seen, "forced-1")
	}
}
