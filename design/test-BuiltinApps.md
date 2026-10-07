# Test Design: Built-in App Names
**Source:** crc-Server.md

## Test: builtin app names are the top-level dirs under the bundle's apps/
**Purpose:** the names ark passes as `flib.Config.BuiltinApps` are exactly the apps its bundle ships (R3515)
**Input:** a bundle listing with files under `apps/mcp/`, `apps/ark/`, a file directly in `apps/`, and files outside `apps/`; then an empty listing
**Expected:** `[ark mcp]`; empty listing gives nil
**Refs:** crc-Server.md
**Code:** builtinapps_test.go
**Fire alarm:** count a file directly in `apps/` as an app (drop the nested check). Red: `apps/README.md` becomes an app named `README.md`.
**Inject:** server.go:builtinAppNames
**Pulled:** 2026-10-07 — rang: `builtinAppNames = [README.md ark mcp], want [ark mcp]` (first attempt dropped `isNested` outright and broke the build; the recorded injection keeps it referenced)
