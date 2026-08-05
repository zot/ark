package main

// CRC: crc-CLITree.md | Test: test-LinkTable.md | R3375, R3377, R3378, R3379

import (
	"strings"
	"testing"

	ark "github.com/zot/ark"
)

// addressEmittingNodes are the verbs that answer with paths *as addresses*, and
// therefore consult the link-view selector. Kept in one place because two tests
// assert complementary halves — these declare the need, and the
// content-emitting verbs below must not.
var addressEmittingNodes = [][]string{
	{"files"},
}

// contentEmittingNodes must NOT consult the selector (R3377). `search` is the
// one that matters: an alias is a way to *reach* a file rather than something
// the file says, and a chunk has exactly one home, so propagating aliases into
// results would return the same passage once per address — seven times over for
// files in ark's most-aliased directory. Near-duplicates differing only by path
// prefix are exactly what dedup cannot collapse, since the content is identical
// while the file records are not.
var contentEmittingNodes = [][]string{
	{"search"},
	{"fetch"},
	{"chunks"},
}

// TestAddressEmittingNodesDeclareTheNeed asserts against the real command tree
// rather than grepping ark's own source, for the reason the session-identity
// guard does: a source-grep proxy cannot see a verb that simply never calls the
// resolver, which is how `ark luhmann events` was missed (R3313's lesson).
// Refs: R3379
func TestAddressEmittingNodesDeclareTheNeed(t *testing.T) {
	for _, path := range addressEmittingNodes {
		name := strings.Join(path, " ")
		if !declaresLinkView(findCmd(t, path...)) {
			t.Errorf("`ark %s` no longer declares that it emits addresses, so --links/--nolinks "+
				"are silently ignored there — a listing that quietly answers in the wrong "+
				"address space, which is the R3359 disagreement (R3379)", name)
		}
	}
}

// TestContentEmittingNodesDoNotDeclare is the negative half, and it is the one
// worth having. The failure it guards is invisible: wrapping `search` would not
// error, it would return each passage once per address, and a caller reading
// results has no way to tell duplicated hits from a corpus that genuinely says
// the same thing twice.
// Refs: R3377
func TestContentEmittingNodesDoNotDeclare(t *testing.T) {
	for _, path := range contentEmittingNodes {
		name := strings.Join(path, " ")
		if declaresLinkView(findCmd(t, path...)) {
			t.Errorf("`ark %s` emits content, not addresses, and must not consult the link-view "+
				"selector — propagating aliases into results returns one passage once per "+
				"address, and dedup cannot collapse them (R3377)", name)
		}
	}
}

// TestLinkViewFlagsAreGlobal — a per-node flag has to be added by hand to each
// declaring node and will eventually miss one, which is the enumeration problem
// R3316 already solved for --force-session. R3379 names it as the precedent.
// Refs: R3379
func TestLinkViewFlagsAreGlobal(t *testing.T) {
	root := buildArkCommand()
	found := map[string]bool{}
	for _, f := range root.Flags {
		for _, n := range f.Names() {
			found[n] = true
		}
	}
	for _, want := range []string{linksFlag, nolinksFlag} {
		if !found[want] {
			t.Errorf("--%s must be a global flag on the root, beside --dir and --force-session (R3379)", want)
		}
	}
}

// TestResolveLinkView covers the axis, including the contradiction. Giving both
// ends is not a preference to resolve but a question with no answer, so it
// errors — the same choice R3354 made for a symlinked source `dir`: where two
// readings are equally defensible, refuse and make the caller say which.
// Refs: R3375, R3378
func TestResolveLinkView(t *testing.T) {
	cases := []struct {
		name           string
		links, nolinks bool
		want           ark.LinkView
		wantErr        bool
	}{
		{"neither is the default view", false, false, ark.LinkViewDefault, false},
		{"--links expands", true, false, ark.LinkViewExpand, false},
		{"--nolinks is canonical-only", false, true, ark.LinkViewCanonical, false},
		{"both ends of one axis is an error", true, true, ark.LinkViewDefault, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLinkView(tc.links, tc.nolinks)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("view = %v, want %v", got, tc.want)
			}
			// The error must name both ends. A message naming one reads as
			// "that flag is wrong" when the truth is that the pair is.
			if tc.wantErr {
				for _, f := range []string{linksFlag, nolinksFlag} {
					if !strings.Contains(err.Error(), "--"+f) {
						t.Errorf("error must name --%s; got %q", f, err)
					}
				}
			}
		})
	}
}

// TestUndeclaredNodeHasDefaultView pins the deliberate asymmetry: the resolved
// view lives in the node's Metadata, and a node that never declared the need has
// none — so it cannot consume the flag by accident. That is the property
// keeping R3377 true structurally rather than by everyone remembering.
// Refs: R3377, R3379
func TestUndeclaredNodeHasDefaultView(t *testing.T) {
	if got := linkViewOf(findCmd(t, "search")); got != ark.LinkViewDefault {
		t.Errorf("an undeclared node resolved view %v, want the default — the selector must not "+
			"reach a command that never said it emits addresses (R3377)", got)
	}
	if got := linkViewOf(nil); got != ark.LinkViewDefault {
		t.Errorf("linkViewOf(nil) = %v, want the default", got)
	}
}
