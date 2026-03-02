# Mini-spec Tool Notes

## Pending Enhancements

- **Add "A" (Approved) gap type** to `minispec update add-gap`. Currently only supports S, R, D, C, O. The skill documents A-type gaps ("Approved gap, never checked off to ensure they stay in place") but the tool rejects them. Had to add A1/A2 manually to design.md.

## Process Notes

- **Back-to-front gap analysis is not obvious.** The Gaps Phase focuses on forward traceability (specs→requirements→design→code) but doesn't explicitly call for reverse analysis: checking whether significant features in code are anchored in specs. In microfts2, three CLI commands (init, delete, reindex) ended up as "inferred" requirements because the spec described concepts but never enumerated the CLI interface or library API. Anything downstream consumers need to rely on — commands, API surface, struct shapes — should be spec-anchored, not inferred. The Gaps Phase should prompt for this explicitly.
