# Test Design: Anchor (LocatorSuggestion.Target + ark chunks -anchor)
**Source:** crc-DB.md

## Test: LocatorSuggestion.Target — each kind assembles
**Purpose:** Verify Target() produces the right @ext TARGET string per LocatorKind, escaping the narrower delimiter.
**Input:** LocatorSuggestion values for bare uuid/path, absolute (range), string, regex, and quote/slash-bearing string/regex.
**Expected:** bare → BaseValue; absolute → `base:range`; string → `base:"text"` (embedded `"` → `\"`); regex → `base:/text/` (embedded `/` → `\/`); empty kind → BaseValue.
**Refs:** crc-DB.md, R3077

## Test: Target — parse round-trip
**Purpose:** The assembled target parses back to the intended base + anchor kind (the escape keeps the closing delimiter findable).
**Input:** Target() outputs for bare / absolute / string-with-quote / regex-with-slash → ParseExtTargetParts.
**Expected:** ok=true, not Invalid, BaseKind + AnchorKind match the intended kind.
**Refs:** crc-DB.md, R3077

## Test: bloodhound crank-handle wraps chunk reads (Sleeping Sentry, #33)
**Purpose:** The weak secretary's chunk-read instructions carry `--wrap` (Baby Food), guarding against a silent revert to a bare `ark chunks <path:range>`.
**Input:** the `searchCrankHandle` const and `renderBloodhoundSeed` output.
**Expected:** both contain `ark chunks --wrap`; `searchCrankHandle` has no bare `ark chunks <path:range>`.
**Refs:** crc-RecallAgentBuilder.md, crc-RecallWatcher.md
