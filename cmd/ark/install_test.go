package main

// CRC: crc-CLI.md | Test: test-Install.md

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Test: test-Install.md — installBundledSkillsAndAgent copies every
// ~/.ark/agents/*.md, not a single hardcoded ark.md.
// CRC: crc-CLI.md | Seq: seq-install.md | R325
func TestInstallBundledAgentsGlobsAll(t *testing.T) {
	arkDir := t.TempDir()
	claudeDir := t.TempDir()

	// Bundle three agents, none named ark.md (the split state that
	// exposed the bug: ark.md no longer exists).
	agentsSrc := filepath.Join(arkDir, "agents")
	if err := os.MkdirAll(agentsSrc, 0755); err != nil {
		t.Fatal(err)
	}
	want := []string{"ark-franklin.md", "ark-messenger.md", "ark-searcher.md"}
	for _, name := range want {
		if err := os.WriteFile(filepath.Join(agentsSrc, name), []byte("agent "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	installed := installBundledSkillsAndAgent(arkDir, claudeDir)

	// Every bundled agent must land in ~/.claude/agents/.
	agentsDst := filepath.Join(claudeDir, "agents")
	var got []string
	for _, name := range want {
		dst := filepath.Join(agentsDst, name)
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Errorf("agent %s not installed: %v", name, err)
			continue
		}
		if string(data) != "agent "+name {
			t.Errorf("agent %s content = %q, want %q", name, data, "agent "+name)
		}
		got = append(got, name)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("installed %d agents, want %d", len(got), len(want))
	}

	// The installed slice must report each agent's destination path.
	for _, name := range want {
		dst := filepath.Join(agentsDst, name)
		found := false
		for _, p := range installed {
			if p == dst {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("installed slice missing %s", dst)
		}
	}
}
