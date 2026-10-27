package main

// CRC: crc-CLI.md, crc-DB.md | Test: test-ConfigErrors.md

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

const brokenToml = "dotfiles = = true\n"

func writeArkToml(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ark.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// R3645: missing and valid files pass; an unparseable one names the repair.
func TestConfigLoadError(t *testing.T) {
	dir := t.TempDir()
	if err := configLoadError(dir); err != nil {
		t.Errorf("missing ark.toml: got %v, want nil", err)
	}
	writeArkToml(t, dir, "dotfiles = true\n")
	if err := configLoadError(dir); err != nil {
		t.Errorf("valid ark.toml: got %v, want nil", err)
	}
	writeArkToml(t, dir, brokenToml)
	err := configLoadError(dir)
	if err == nil || !strings.Contains(err.Error(), "ark config recover") {
		t.Errorf("broken ark.toml: got %v, want an error naming `ark config recover`", err)
	}
}

// R3645, R3646: guarded nodes refuse without running their body; the exempt
// paths and a help request still run.
func TestGuardConfigActions(t *testing.T) {
	dir := t.TempDir()
	writeArkToml(t, dir, brokenToml)
	withArkDir(t, dir)

	ran := map[string]bool{}
	leaf := func(name string) *ucli.Command {
		return &ucli.Command{Name: name, Action: func(context.Context, *ucli.Command) error {
			ran[name] = true
			return nil
		}}
	}
	search := leaf("search")
	search.SkipFlagParsing = true // receives --help raw, like the real node
	root := &ucli.Command{Name: "ark", Commands: []*ucli.Command{
		search, leaf("stop"), leaf("status"),
		{Name: "config", Commands: []*ucli.Command{leaf("recover"), leaf("show")}},
	}}
	guardConfigActions(root.Commands, "")
	run := func(args ...string) error {
		return root.Run(context.Background(), append([]string{"ark"}, args...))
	}

	for _, args := range [][]string{{"search", "foo"}, {"config", "show"}} {
		name := args[len(args)-1]
		if name == "foo" {
			name = "search"
		}
		if err := run(args...); err == nil || ran[name] {
			t.Errorf("%v: err=%v ran=%v, want the config error and no run", args, err, ran[name])
		}
	}
	for _, args := range [][]string{{"stop"}, {"status"}, {"config", "recover"}, {"search", "--help"}} {
		name := args[len(args)-1]
		if name == "--help" {
			name = "search"
		}
		if err := run(args...); err != nil || !ran[name] {
			t.Errorf("%v: err=%v ran=%v, want it to run", args, err, ran[name])
		}
	}
}

// R3646: an exempt path that no longer exists would silently lose its
// exemption, so every one must name a node with an Action in the real tree.
func TestConfigExemptPathsExist(t *testing.T) {
	found := map[string]bool{}
	var walk func(cmds []*ucli.Command, prefix string)
	walk = func(cmds []*ucli.Command, prefix string) {
		for _, c := range cmds {
			path := strings.TrimSpace(prefix + " " + c.Name)
			if c.Action != nil {
				found[path] = true
			}
			walk(c.Commands, path)
		}
	}
	walk(buildArkCommand().Commands, "")
	for path := range configExempt {
		if !found[path] {
			t.Errorf("exempt path %q has no Action in the command tree", path)
		}
	}
}

// R3648, R3649: recover reads the I records past an unparseable ark.toml,
// keeps the broken file, and writes one that loads.
func TestConfigRecoverPastBrokenToml(t *testing.T) {
	dir := t.TempDir()
	if err := ark.Init(dir, ark.InitOpts{ConfigSeed: []byte("dotfiles = true\n")}); err != nil {
		t.Fatal(err)
	}
	writeArkToml(t, dir, brokenToml)
	withArkDir(t, dir)

	if err := configRecoverAction(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(filepath.Join(dir, "ark.toml.broken"))
	if err != nil || string(kept) != brokenToml {
		t.Errorf("ark.toml.broken = %q, %v; want the broken text", kept, err)
	}
	cfg, err := ark.LoadConfig(filepath.Join(dir, "ark.toml"))
	if err != nil {
		t.Fatalf("recovered ark.toml does not load: %v", err)
	}
	if !cfg.Dotfiles {
		t.Error("recovered config lost dotfiles = true")
	}
}

// R3650: only a missing ark.toml falls back to the default collection.
func TestPatternsCollectionForBrokenToml(t *testing.T) {
	dir := t.TempDir()
	p, err := patternsCollectionFor(dir)
	if err != nil || p.Dir() != filepath.Join(dir, "personal-patterns") {
		t.Errorf("missing ark.toml: %v, %v; want the default dir", p, err)
	}
	writeArkToml(t, dir, brokenToml)
	if p, err := patternsCollectionFor(dir); err == nil {
		t.Errorf("broken ark.toml: got %q, want an error", p.Dir())
	}
}

// R3647: with no server, status reports config_parse, says the index was not
// read, and exits 1. cmdStatus ends in fatal, which exits, so it runs in a
// child copy of this test binary.
func TestStatusWarnsOnBrokenToml(t *testing.T) {
	if dir := os.Getenv("ARK_STATUS_CHILD"); dir != "" {
		arkDir = dir
		cmdStatus(nil)
		os.Exit(0) // not reached: the broken config must end in fatal
	}
	dir := t.TempDir()
	writeArkToml(t, dir, brokenToml)
	cmd := exec.Command(os.Args[0], "-test.run=^TestStatusWarnsOnBrokenToml$")
	cmd.Env = append(os.Environ(), "ARK_STATUS_CHILD="+dir)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("status exit = %v, want 1; output:\n%s", err, out)
	}
	for _, want := range []string{configParseWarning + ":", "index not read"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("status output lacks %q:\n%s", want, out)
		}
	}
}
