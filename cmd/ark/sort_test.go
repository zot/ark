package main

import (
	"testing"

	ucli "github.com/urfave/cli/v3"
)

// TestSortCommandTree guards R2953: subcommand lists must be ordered
// alphabetically at every depth so help is easy to scan. A future change that
// drops the sort (reverting to urfave's declaration order) trips this.
// CRC: crc-CLITree.md | R2953
func TestSortCommandTree(t *testing.T) {
	root := []*ucli.Command{
		{Name: "config", Commands: []*ucli.Command{
			{Name: "show-why"}, {Name: "add-source"}, {Name: "add-exclude"},
		}},
		{Name: "status"},
		{Name: "add"},
		{Name: "chunks"},
	}
	sortCommandTree(root)

	wantTop := []string{"add", "chunks", "config", "status"}
	for i, w := range wantTop {
		if root[i].Name != w {
			t.Errorf("top[%d] = %q, want %q", i, root[i].Name, w)
		}
	}
	// Recursion: the nested config subcommands are sorted too.
	wantSub := []string{"add-exclude", "add-source", "show-why"}
	sub := root[2].Commands // config, now at index 2
	for i, w := range wantSub {
		if sub[i].Name != w {
			t.Errorf("config sub[%d] = %q, want %q", i, sub[i].Name, w)
		}
	}
}
