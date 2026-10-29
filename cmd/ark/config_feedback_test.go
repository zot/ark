package main

// CRC: crc-CLI.md | Test: test-ConfigFeedback.md | R3677, R3679

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

// feedbackHome is a throwaway ark home with a default ark.toml and no server
// (no socket under it), so every command takes its local path, and a hermetic
// HOME so the built-in sources resolve under the test's own directory.
func feedbackHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)
	dir := filepath.Join(home, ".ark")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ark.WriteDefaultConfig(filepath.Join(dir, "ark.toml"), nil); err != nil {
		t.Fatal(err)
	}
	old := arkDir
	arkDir = dir
	t.Cleanup(func() { arkDir = old })
	return dir
}

// runArk runs one command through the real command tree and returns its stdout.
func runArk(t *testing.T, args ...string) string {
	t.Helper()
	root := &ucli.Command{Name: "ark", Commands: arkCommands()}
	var runErr error
	out := captureStdout(t, func() {
		runErr = root.Run(context.Background(), append([]string{"ark"}, args...))
	})
	if runErr != nil {
		t.Fatalf("ark %s: %v", strings.Join(args, " "), runErr)
	}
	return out
}

// TestConfigMutationsSayWhatTheyDid: every config mutation prints one line
// saying what changed and where, and the change is in ark.toml.
func TestConfigMutationsSayWhatTheyDid(t *testing.T) {
	dir := feedbackHome(t)
	src := t.TempDir()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"config", "add-source", src}, "added source " + src},
		{[]string{"config", "add-exclude", "*.bak"}, "added exclude *.bak (global)"},
		{[]string{"config", "add-include", "--source", src, "*.org"}, "added include *.org (source " + src + ")"},
		{[]string{"config", "remove-pattern", "*.bak"}, "removed pattern *.bak (global)"},
		{[]string{"config", "add-strategy", "*.org", "lines"}, "added strategy *.org = lines"},
		{[]string{"config", "remove-source", src}, "removed source " + src},
	}
	for _, tc := range cases {
		if got := strings.TrimSpace(runArk(t, tc.args...)); got != tc.want {
			t.Errorf("ark %s printed %q, want %q", strings.Join(tc.args, " "), got, tc.want)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "ark.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"*.org" = "lines"`) {
		t.Errorf("the strategy the command reported is not in ark.toml")
	}
}

// TestBareSourcesListsReadOnly: bare `ark sources` lists the sources, the
// built-in ark home among them, marks a missing directory, and leaves ark.toml
// exactly as it was. It ran `check`, which can add sources, until 2026-10-28.
// The home has no index on purpose: listing is read-only and must not need
// one. A path that opens the index ends in fatal, which exits, so the command
// runs in a child copy of this test binary and an exit is reported as that.
func TestBareSourcesListsReadOnly(t *testing.T) {
	if os.Getenv("ARK_SOURCES_CHILD") != "" {
		arkDir = os.Getenv("ARK_SOURCES_DIR")
		root := &ucli.Command{Name: "ark", Commands: arkCommands()}
		if err := root.Run(context.Background(), []string{"ark", "sources"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	dir := feedbackHome(t)
	src := t.TempDir()
	runArk(t, "config", "add-source", src)
	cfgPath := filepath.Join(dir, "ark.toml")
	gone := filepath.Join(t.TempDir(), "gone")
	data, _ := os.ReadFile(cfgPath)
	data = append(data, []byte("\n[[source]]\ndir = \""+filepath.ToSlash(gone)+"\"\n")...)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

	cmd := exec.Command(os.Args[0], "-test.run=^TestBareSourcesListsReadOnly$")
	cmd.Env = append(os.Environ(), "ARK_SOURCES_CHILD=1", "ARK_SOURCES_DIR="+dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	outBytes, err := cmd.Output()
	if err != nil {
		t.Fatalf("bare ark sources did not list without an index (a read-only listing needs none): %v\n%s", err, stderr.String())
	}
	out := string(outBytes)

	for _, want := range []string{"# Sources", "`" + src + "`", "`" + dir + "`"} {
		if !strings.Contains(out, want) {
			t.Errorf("ark sources does not list %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "`"+filepath.FromSlash(gone)+"` — missing") && !strings.Contains(out, "`"+filepath.ToSlash(gone)+"` — missing") {
		t.Errorf("the missing source is not marked missing:\n%s", out)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Errorf("bare ark sources changed ark.toml")
	}
}

// TestFormatSourceListMarks: a glob source shows as its pattern, a glob-made
// source names its glob, and a log source names the source it records.
func TestFormatSourceListMarks(t *testing.T) {
	out := formatSourceList([]listedSource{
		{Dir: "~/projects/*/memory"},
		{Dir: "/p/a/memory", FromGlob: "~/projects/*/memory"},
		{Dir: "/chats/a", LogsFor: "a"},
	}, func(string) bool { return true })
	for _, want := range []string{
		"- `~/projects/*/memory` — glob",
		"- `/p/a/memory` — from glob `~/projects/*/memory`",
		"- `/chats/a` — logs for a",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
