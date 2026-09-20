# Test Design: Install

**Source:** crc-CLI.md

Covers the bundled-agent install glob (R325 global copy path in
`installBundledSkillsAndAgent`; the `cmdUIInstall` symlink path R432
shares the same glob logic).

## Test: install copies every bundled agent, not a hardcoded ark.md

**Purpose:** Prove the global install (`installBundledSkillsAndAgent`)
installs *every* `~/.ark/agents/*.md` via a directory glob, so it never
drifts from the bundled agent set. Guards against the regression where a
hardcoded `ark.md` meant no agent installed once the ark agent was split
into `ark-franklin.md` / `ark-messenger.md` / `ark-searcher.md`.
**Input:** A temp `arkDir` with `agents/{ark-franklin,ark-messenger,ark-searcher}.md`
(none named `ark.md`), and a temp `claudeDir`.
**Expected:** All three files appear under `claudeDir/agents/` with their
content intact, and each destination path is in the returned `installed` slice.
**Refs:** crc-CLI.md, seq-install.md
**Code:** cmd/ark/install_test.go
**Fire alarm:** Restore the original bug — replace the glob with a single
hardcoded `filepath.Join(arkDir, "agents", "ark.md")` lookup. With no
`ark.md` in the bundle the stat/glob finds nothing, zero agents install,
and the test goes red on the first missing-agent `ReadFile` error.
**Inject:** cmd/ark/main.go:installBundledSkillsAndAgent
**Pulled:** 2026-09-19 — rang. Injected the hardcoded `ark.md` lookup;
test failed with "agent ark-franklin.md not installed" (and the other two)
plus "installed 0 agents, want 3". Restored; green.
