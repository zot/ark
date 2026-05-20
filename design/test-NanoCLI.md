# Test Design: NanoCLI
**Source:** crc-NanoCLI.md

## Test: -m sets model
**Purpose:** R2511 — `-m` is the only way to set the model
**Input:** argv `-m flag-model hello`
**Expected:** Nano.Model == "flag-model"
**Refs:** crc-NanoCLI.md

## Test: missing model errors out
**Purpose:** R2519 — no model exits with the documented message
**Input:** argv `hello`
**Expected:** exit code 1; stderr contains `model not set: pass -m <model>`
**Refs:** crc-NanoCLI.md

## Test: --base-url overrides default
**Purpose:** R2561 — `--base-url` sets `Nano.BaseURL`
**Input:** argv `-m m --base-url http://other:9999`
**Expected:** Nano.BaseURL == "http://other:9999"
**Refs:** crc-NanoCLI.md

## Test: --max-steps sets MaxSteps
**Purpose:** R2562 — `--max-steps N` sets `Nano.MaxSteps`
**Input:** argv `-m m --max-steps 42`
**Expected:** Nano.MaxSteps == 42
**Refs:** crc-NanoCLI.md

## Test: --max-steps rejects non-integer
**Purpose:** R2562 — non-integer arg exits cleanly
**Input:** argv `-m m --max-steps abc`
**Expected:** exit code 1; stderr contains `--max-steps requires an integer`
**Refs:** crc-NanoCLI.md

## Test: --approve-all sets ApproveAll
**Purpose:** R2563 — `--approve-all` flips the auto-approve switch
**Input:** argv `-m m --approve-all hello`
**Expected:** Nano.ApproveAll == true
**Refs:** crc-NanoCLI.md

## Test: -c with no sessions
**Purpose:** R2514 — clean error when -c finds nothing in cwd
**Input:** fresh sessions file, argv `-m m -c`
**Expected:** exit code 1; stderr contains `no sessions in this directory`
**Refs:** crc-NanoCLI.md, seq-nano-session-resume.md#1.2

## Test: -m and -c combine in any order
**Purpose:** R2511, R2512 — flags compose
**Input:** sessions file with one entry in cwd; argv `-m m -c hello` and `-c -m m hello`
**Expected:** both orderings load the saved session, then run "hello"
**Refs:** crc-NanoCLI.md

## Test: -h and --help print usage and exit 0
**Purpose:** R2560 — help flag accepted at any position
**Input:** argv `-h`, `--help`, and `-m foo --help`
**Expected:** all three print the usage banner and exit 0
**Refs:** crc-NanoCLI.md

## Test: --stream sets Nano.Stream
**Purpose:** R2565 — `--stream` flips the Nano.Stream flag
**Input:** argv `-m m --stream hello`
**Expected:** Nano.Stream == true; chat request body has `stream: true`
**Refs:** crc-NanoCLI.md
