# UI App Checkpoints — Fossil Safety

Go. Linux.

## Problem

`ark ui checkpoint` manages a per-app Fossil repository
(`<app-dir>/checkpoint.fossil`) through multi-step command dances:
initialize-and-open, save-to-side-branch, destroy-and-recreate for
baseline. Three defects combined into two same-day incidents
(2026-07-23) that destroyed working-tree state and committed damage
into an unrelated enclosing repository:

- **Walk-up.** `fossil open` without `--nested` fails inside an
  enclosing checkout (app directories can sit inside a project's own
  fossil checkout). The failure was ignored, and every subsequent
  fossil verb resolved to the *enclosing* checkout — closing it,
  committing snapshots to its trunk, grafting branches into it.
- **Tree clobber.** Branch saves (`local`, `update`) committed the
  branch snapshot and then ran `fossil update trunk`, which *extracts
  trunk content over the working tree* — silently reverting the app
  to the stale baseline. `baseline` then honestly committed the
  clobbered disk as the new baseline.
- **Swallowed errors.** No fossil command's result was checked. Verbs
  printed success unconditionally, so a refused commit (`cannot
  commit against a closed leaf`) masqueraded as a saved checkpoint —
  and the masking hid the tree clobber, because a *failed* branch
  commit left uncommitted changes that `update trunk` merged forward
  instead of reverting.

## The invariant

Before the checkpoint machinery runs any fossil verb for an app, the
app directory must have **its own checkout bound to that app's
`checkpoint.fossil`** — verified against fossil's own report, never
assumed. The invariant holds **at every step** of a multi-step dance,
not just at entry: the baseline dance deletes and recreates the
checkout mid-sequence, and each destructive step must re-establish
that it is still operating on the app's repository.

## Behavior

### Binding verification

Verification asks fossil (`fossil info` in the app directory) which
repository and checkout root it would act on, and compares both to
the expected `checkpoint.fossil` path and app directory. Paths are
compared symlink-resolved — app directories are routinely reached
through symlinks, and fossil reports resolved paths. On mismatch (or
no open checkout where one is required) the verb aborts loudly with
both paths in the message; it never falls through to fossil verbs
that would resolve to an enclosing checkout.

Verification runs: at entry to every verb that operates on an
existing repository, after every `fossil open`, and immediately
before every destructive step (`close`, repository deletion, forced
checkout, and the baseline re-init's first commit).

### Opens are nested and never extract

Every `fossil open` passes `--nested`, so opening inside an enclosing
checkout succeeds and binds to the app repository instead of failing
into the walk-up. Opening an **existing** repository additionally
passes `--keep`: the working tree is the source of truth, and an open
must never extract repository content over it. (A freshly-initialized
repository has only the empty check-in, but `--keep` is passed there
too — uniformity costs nothing.)

### Branch saves never touch the working tree

Saving to a side branch (`local`, `updates`) switches the checkout
with `fossil checkout <branch> --keep`, commits the disk state, and
returns with `fossil checkout trunk --keep`. `--keep` retargets the
checkout without modifying files, so the working tree is preserved —
the committed snapshot is exactly the disk state, and returning to
trunk does not revert the tree. The `fossil update`-based dance is
retired: `update` merges/extracts file content and is exactly the
clobber. No checkpoint verb modifies the working tree except
`rollback` and `clear`, whose purpose is to restore it.

Branch edge cases: if the branch does not exist yet, the save commits
with `--branch <name>`. If the branch leaf is **closed** (the
refused-commit incident), the save reopens it with `fossil tag cancel
--raw closed <branch>` and retries once — the raw tag name matters;
the non-raw form cancels a `sym-closed` tag and leaves the leaf
closed.

### Change detection never trusts the stat cache

The app checkout runs with fossil's `mtime-changes` setting off, so
commits hash file content instead of trusting modification times. The
default stat cache misses a same-size edit made within the same
mtime second as the last recorded state — exactly the cadence of an
agent editing a file and checkpointing immediately — and a missed
change makes the commit record stale content and fail fossil's own
consistency check. The setting (with the ignore-glob) is applied
after every open, not only at repository creation, so repositories
created before this contract get it on their next use.

### Errors surface

Every fossil invocation's result is checked. When one fails, the verb
reports the failing fossil command and fossil's own output on stderr
and exits nonzero, having stopped **before** the next destructive
step. Success messages are printed only when the operations they
describe actually succeeded. There are no best-effort fossil calls in
the checkpoint machinery.

### Baseline snapshots the disk

`baseline` (and `clear`) rebuilds the repository so trunk holds a
single baseline commit of the **current disk state**, preserving the
`updates` and `local` branches: each existing side branch is exported
to its own bundle file (one file per branch — a shared file would let
the second export overwrite the first), the repository is closed,
deleted, re-initialized, and re-opened (`--nested`), the binding is
re-verified, the disk state is committed as the baseline, and the
bundles are imported. Any step's failure aborts the dance before the
next destructive step, with the tree untouched beyond what already
completed.
