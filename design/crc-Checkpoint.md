# Checkpoint
**Requirements:** R287, R3234, R3235, R3236, R3237, R3238, R3239, R3240, R3241

Fossil-backed app checkpoint machinery behind `ark ui checkpoint`
(save/list/rollback/diff/clear/baseline/count/update/local). Runs the
fossil binary against the per-app repository
`<app-dir>/checkpoint.fossil`, enforcing the checkout-binding
invariant at every step and surfacing every fossil failure.

## Knows
- fossil: string — path to the fossil binary (`~/.claude/bin/fossil`)
- appDir: string — the app directory (may be reached via symlink)
- repo: string — expected repository path, `<appDir>/checkpoint.fossil`

## Does
- verifyCheckout(fossil, appDir, repo) error: run `fossil info` in
  appDir, parse `repository:` and `local-root:`, compare both to the
  expected repo and appDir with symlinks resolved on both sides;
  error carries both paths on mismatch or when no checkout is open
  (R3234)
- fossilRun(fossil, dir, args...): execute one fossil command,
  return combined output + error (unchanged primitive)
- fossilStep(fossil, dir, args...): fossilRun + error wrapping that
  names the failing fossil command and includes fossil's output —
  the checked step every verb builds from (R3239)
- fossilInit: init + open `--nested --keep` + ignore-glob + add,
  every step checked, verify after open (R3236, R3235)
- checkpointSave: first call initializes the repo; later calls
  verify binding, addremove, commit to trunk (R3239)
- checkpointLocal / checkpointUpdate: branch save via
  `checkout <branch> --keep` → commit → `checkout trunk --keep`;
  never touches the working tree; missing branch → commit
  `--branch`; closed leaf → `tag cancel --raw closed <branch>` +
  one retry (R3237, R3238)
- checkpointBaseline: per-branch bundle export (one file per
  branch), close, delete repo, re-init, re-verify binding, commit
  disk as baseline, import bundles; verify before every destructive
  step (R3240, R3235)
- checkpointList / checkpointDiff / checkpointCount /
  checkpointRollback: read/restore verbs; verify binding at entry;
  errors surface (R3239)

## Collaborators
- CLI: cmdUICheckpoint dispatches verbs, prints errors to stderr,
  exits nonzero (crc-CLI.md, R287)
- fossil binary: all repository operations

## Sequences
- seq-checkpoint.md
