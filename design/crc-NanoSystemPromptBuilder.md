# NanoSystemPromptBuilder
**Requirements:** R2555, R2556, R2557, R2558

Builds the system prompt sent on every fresh `Run`. Two side-quests:
walk the cwd looking for project docs, and walk well-known skill
directories looking for SKILL.md / SKILLS.md files. The lists become
context the model can ask about.

## Knows
- The doc filenames (case-insensitive): claude.md, agent.md, agents.md,
  readme.md
- The skill filenames (case-insensitive): skill.md, skills.md
- The skill roots: `.claude/skills`, `~/.claude/skills`,
  `~/.codex/skills`, `~/.codex/plugins`
- The directories to skip while walking: `.git`, `.venv`, `__pycache__`,
  `node_modules`, `venv`
- A per-call result cap (40 entries) to keep the prompt bounded

## Does
- Walk the cwd looking for the doc filenames (R2556)
- Walk each skill root (expanding `~` to the home directory) looking for
  skill files (R2557)
- Skip skipDirs while descending (R2558)
- Render a found path relative to `~/` if it lives under the home
  directory, otherwise relative to cwd, otherwise as the absolute path
- Deduplicate, sort, and join the result list with `, `; produce
  `none` when nothing matched
- Format the system prompt with cwd, GOOS/GOARCH, $SHELL, the doc list,
  and the skill list (R2555–R2557)

## Collaborators
- Nano: source of Cwd; consumer of the rendered prompt
