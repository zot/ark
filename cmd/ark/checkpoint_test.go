package main

// CRC: crc-Checkpoint.md | test-checkpoint.md
// Tests run the real fossil binary in t.TempDir() sandboxes: the 2026-07-23
// incidents were interaction bugs between our command sequences and fossil's
// actual semantics, so a fake fossil would re-encode the wrong assumptions.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testFossil returns the fossil binary path or skips the test.
func testFossil(t *testing.T) string {
	t.Helper()
	fossil := fossilBin()
	if fossil == "" {
		t.Skip("fossil binary not installed at ~/.claude/bin/fossil")
	}
	return fossil
}

// newApp creates an app dir with one file and returns (appDir, repo).
func newApp(t *testing.T) (string, string) {
	t.Helper()
	appDir := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, appDir, "file.txt", "v1\n")
	return appDir, filepath.Join(appDir, "checkpoint.fossil")
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// catRev returns a file's content at a fossil revision.
func catRev(t *testing.T, fossil, appDir, name, rev string) string {
	t.Helper()
	out, err := fossilRun(fossil, appDir, "cat", name, "-r", rev)
	if err != nil {
		t.Fatalf("fossil cat %s -r %s: %v\n%s", name, rev, err, out)
	}
	return out
}

// newOuterCheckout creates an enclosing fossil checkout in a fresh temp dir
// and returns its root. The walk-up tests set the same trap: an app dir
// nested inside this checkout.
func newOuterCheckout(t *testing.T, fossil string) string {
	t.Helper()
	outer := t.TempDir()
	outerRepo := filepath.Join(outer, "outer.fossil")
	for _, args := range [][]string{
		{"init", outerRepo, "--project-name", "outer"},
		{"open", outerRepo, "--force"},
	} {
		if out, err := fossilRun(fossil, outer, args...); err != nil {
			t.Fatalf("outer setup %v: %v\n%s", args, err, out)
		}
	}
	return outer
}

// Test: branch save preserves working tree (R3237 — the second-incident
// clobber: the retired `update trunk` dance reverted disk to stale trunk).
func TestBranchSavePreservesTree(t *testing.T) {
	fossil := testFossil(t)
	appDir, repo := newApp(t)
	if err := checkpointSave(fossil, appDir, repo, "init"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, appDir, "file.txt", "v2\n")
	writeFile(t, appDir, "newfile.txt", "new\n")

	if err := checkpointLocal(fossil, appDir, repo, "save v2"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, appDir, "file.txt"); got != "v2\n" {
		t.Errorf("working tree clobbered: file.txt = %q, want %q", got, "v2\n")
	}
	if _, err := os.Stat(filepath.Join(appDir, "newfile.txt")); err != nil {
		t.Errorf("working tree clobbered: newfile.txt removed: %v", err)
	}
	if got := catRev(t, fossil, appDir, "file.txt", "local"); got != "v2\n" {
		t.Errorf("local branch snapshot = %q, want %q", got, "v2\n")
	}
	// Checkout must be back on trunk
	out, err := fossilRun(fossil, appDir, "info")
	if err != nil || !strings.Contains(out, "tags:         trunk") {
		t.Errorf("checkout not returned to trunk:\n%s", out)
	}
}

// Test: closed-leaf branch save reopens and commits (R3238 — the
// first-incident swallowed `cannot commit against a closed leaf`).
func TestClosedLeafBranchSave(t *testing.T) {
	fossil := testFossil(t)
	appDir, repo := newApp(t)
	if err := checkpointSave(fossil, appDir, repo, "init"); err != nil {
		t.Fatal(err)
	}
	// Build the incident's skeleton: a closed `local` leaf
	for _, args := range [][]string{
		{"commit", "--branch", "local", "-m", "skeleton", "--allow-empty", "--no-warnings"},
		{"amend", "current", "--close"},
		{"checkout", "trunk", "--keep"},
	} {
		if out, err := fossilRun(fossil, appDir, args...); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, appDir, "file.txt", "v2\n")

	if err := checkpointLocal(fossil, appDir, repo, "after reopen"); err != nil {
		t.Fatalf("closed-leaf save failed: %v", err)
	}
	if got := catRev(t, fossil, appDir, "file.txt", "local"); got != "v2\n" {
		t.Errorf("local branch snapshot = %q, want %q", got, "v2\n")
	}
	if got := readFile(t, appDir, "file.txt"); got != "v2\n" {
		t.Errorf("working tree clobbered: file.txt = %q", got)
	}
}

// Test: missing branch is created (R3238).
func TestMissingBranchCreated(t *testing.T) {
	fossil := testFossil(t)
	appDir, repo := newApp(t)
	if err := checkpointSave(fossil, appDir, repo, "init"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, appDir, "file.txt", "v2\n")

	if err := checkpointLocal(fossil, appDir, repo, "first local"); err != nil {
		t.Fatal(err)
	}
	if got := catRev(t, fossil, appDir, "file.txt", "local"); got != "v2\n" {
		t.Errorf("local branch snapshot = %q, want %q", got, "v2\n")
	}
	if got := readFile(t, appDir, "file.txt"); got != "v2\n" {
		t.Errorf("working tree clobbered: file.txt = %q", got)
	}
}

// Test: baseline commits current disk and preserves branches (R3240).
func TestBaselineSnapshotsDiskAndPreservesBranches(t *testing.T) {
	fossil := testFossil(t)
	appDir, repo := newApp(t)
	if err := checkpointSave(fossil, appDir, repo, "init"); err != nil {
		t.Fatal(err)
	}
	// local branch holds v2; updates branch exists too
	writeFile(t, appDir, "file.txt", "v2\n")
	if err := checkpointLocal(fossil, appDir, repo, "local v2"); err != nil {
		t.Fatal(err)
	}
	if out, err := fossilRun(fossil, appDir, "commit", "--branch", "updates", "-m", "upd", "--allow-empty", "--no-warnings"); err != nil {
		t.Fatalf("setup updates branch: %v\n%s", err, out)
	}
	if out, err := fossilRun(fossil, appDir, "checkout", "trunk", "--keep"); err != nil {
		t.Fatalf("back to trunk: %v\n%s", err, out)
	}
	// Disk moves on past every committed state
	writeFile(t, appDir, "file.txt", "v3\n")

	if err := checkpointBaseline(fossil, appDir, repo); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, appDir, "file.txt"); got != "v3\n" {
		t.Errorf("working tree clobbered: file.txt = %q, want v3", got)
	}
	if got := catRev(t, fossil, appDir, "file.txt", "trunk"); got != "v3\n" {
		t.Errorf("baseline snapshot source wrong: trunk file.txt = %q, want v3 (disk)", got)
	}
	if got := catRev(t, fossil, appDir, "file.txt", "local"); got != "v2\n" {
		t.Errorf("local branch not preserved: file.txt = %q, want v2", got)
	}
	out, err := fossilRun(fossil, appDir, "branch", "list")
	if err != nil || !strings.Contains(out, "updates") {
		t.Errorf("updates branch not preserved:\n%s", out)
	}
}

// Test: nested enclosing checkout — no walk-up (R3234/R3236, the
// first-incident damage to the enclosing project repo).
func TestNestedEnclosingCheckoutNoWalkup(t *testing.T) {
	fossil := testFossil(t)
	outer := newOuterCheckout(t, fossil)
	outerTimeline, _ := fossilRun(fossil, outer, "timeline", "-t", "ci", "--oneline")

	appDir := filepath.Join(outer, "app")
	if err := os.Mkdir(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, appDir, "file.txt", "v1\n")
	repo := filepath.Join(appDir, "checkpoint.fossil")

	if err := checkpointSave(fossil, appDir, repo, "init"); err != nil {
		t.Fatalf("save inside enclosing checkout failed: %v", err)
	}
	if err := verifyCheckout(fossil, appDir, repo); err != nil {
		t.Errorf("app checkout not bound to app repo: %v", err)
	}
	// The enclosing checkout is untouched: still open, no new commits
	if _, err := os.Stat(filepath.Join(outer, ".fslckout")); err != nil {
		t.Errorf("enclosing checkout was closed: %v", err)
	}
	after, _ := fossilRun(fossil, outer, "timeline", "-t", "ci", "--oneline")
	if after != outerTimeline {
		t.Errorf("enclosing repo timeline changed:\nbefore:\n%s\nafter:\n%s", outerTimeline, after)
	}
}

// Test: verify rejects a foreign binding (R3234 — the abort gate).
func TestVerifyRejectsForeignBinding(t *testing.T) {
	fossil := testFossil(t)
	outer := newOuterCheckout(t, fossil)
	appDir := filepath.Join(outer, "app")
	if err := os.Mkdir(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No nested checkout: fossil info walks up to the enclosing checkout
	err := verifyCheckout(fossil, appDir, filepath.Join(appDir, "checkpoint.fossil"))
	if err == nil {
		t.Fatal("verifyCheckout accepted the enclosing checkout's binding")
	}
	if !strings.Contains(err.Error(), "outer.fossil") {
		t.Errorf("mismatch error should name the reported repo, got: %v", err)
	}
}

// Test: fossil failure surfaces (R3239 — no swallowed errors, no
// unconditional success).
func TestFossilFailureSurfaces(t *testing.T) {
	fossil := testFossil(t)
	appDir, repo := newApp(t)
	// A repo path that exists but is not a fossil repository
	writeFile(t, appDir, "checkpoint.fossil", "not a repository\n")

	err := checkpointList(fossil, appDir, repo)
	if err == nil {
		t.Fatal("checkpointList succeeded against a corrupt repository")
	}
	if !strings.Contains(err.Error(), "fossil") {
		t.Errorf("error should name the failing fossil command, got: %v", err)
	}
}
