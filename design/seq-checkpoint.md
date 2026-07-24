# Checkpoint sequences

Fossil command dances for `ark ui checkpoint`. Every step that runs
fossil is checked (R3239); "verify" is verifyCheckout (R3234), the
binding check that aborts the verb on mismatch.

## Branch save (local / update) — R3237, R3238

Diagram 1: repo exists (the repo-absent path is fossilInit + trunk
baseline + empty branch commit, diagram 3 steps 3.1–3.4).

```
1. checkpointLocal/checkpointUpdate
   1.1. verify — binding at entry (R3235)
   1.2. checkout <branch> --keep — retarget checkout, disk untouched
        1.2.1. on "cannot resolve name": branch missing — addremove,
               commit --branch <name>, checkout trunk --keep, done (R3238)
   1.3. addremove
   1.4. commit — snapshot of disk onto <branch>
        1.4.1. on "closed leaf": tag cancel --raw closed <branch>,
               retry commit once (R3238)
   1.5. checkout trunk --keep — return, disk untouched (R3237)
```

## Baseline / clear — R3240

```
2. checkpointBaseline
   2.1. verify — binding at entry (R3235)
   2.2. for each of updates, local present in branch list:
        2.2.1. bundle export to per-branch file (own file each — a
               shared path lets the second export overwrite the first)
   2.3. verify — binding before destruction (R3235)
   2.4. close --force
   2.5. delete checkpoint.fossil
   2.6. re-init + open --nested --keep (fossilInit, diagram 3)
   2.7. verify — binding after re-open, before first commit (R3235)
   2.8. commit disk as "=== BASELINE ==="
   2.9. import per-branch bundles, delete bundle files
```

## fossilInit — R3236

```
3. fossilInit
   3.1. fossil init <repo>
   3.2. open <repo> --force --nested --keep
   3.3. verify — binding after open (R3235)
   3.4. settings ignore-glob; add .
```
