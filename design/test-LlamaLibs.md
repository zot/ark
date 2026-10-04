# Test Design: LlamaLibs
**Source:** crc-LlamaLibs.md

Archive extraction over temp files: no network, no yzma load. The failure is
invisible when violated: an extractor that drops every entry still returns
nil, so provisioning "completes" and only the post-check notices an empty lib
dir. Found live on Windows 2026-10-04: ggml-org's Windows zips are flat
(`llama.dll` at the root), unlike the Linux tarballs' `llama-bXXXX/` wrapper.

## Test: a flat zip extracts its entries as-is
**Purpose:** extractZip keeps top-level entries when the archive has no shared release directory (R2969)
**Input:** a zip whose entries are `llama.dll` and `ggml.dll` at the root
**Expected:** both files exist in dest with their contents
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make extractZip flatten unconditionally (the pre-fix behavior); the test goes red because dest is empty
**Inject:** llamalibs.go:extractZip

## Test: a wrapped zip strips its release directory
**Purpose:** extractZip still flattens a zip whose entries all sit under one top-level directory (R2969)
**Input:** a zip with `llama-b1/` and `llama-b1/llama.dll`
**Expected:** `llama.dll` lands directly in dest, with no `llama-b1/` directory created
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: the lib dir joins the Windows DLL search path
**Purpose:** addDllSearchDir makes libDir the process DLL directory, so `lib\ggml.dll` resolves `lib\ggml-base.dll` beside it (R3485). Invisible when violated off the VM: nothing on Linux loads a DLL, and on Windows the failure only reads as "The specified module could not be found" at embed time.
**Input:** Windows only (`//go:build windows`). A temp dir passed to addDllSearchDir; the default search order is restored afterwards with `SetDllDirectoryW(NULL)`.
**Expected:** addDllSearchDir returns nil and `GetDllDirectoryW` reports the temp dir
**Refs:** crc-LlamaLibs.md
**Code:** dllpath_windows_test.go
**Fire alarm:** make the Windows addDllSearchDir return nil without calling SetDllDirectory (the pre-fix behavior); the test goes red with GetDllDirectoryW reporting an empty directory instead of the temp dir
**Inject:** dllpath_windows.go:addDllSearchDir

## Test: the MSVC runtime is missing only when in neither directory
**Purpose:** missingMSVCRuntime reports a runtime DLL as missing only when neither the system directory nor libDir holds it (R3486). Invisible when violated in the over-reporting direction: an extra fetch still succeeds.
**Input:** temp sysDir and libDir: (a) both empty; (b) `msvcp140.dll` in sysDir, `vcruntime140.dll` in libDir; (c) all three spread across the two
**Expected:** (a) all three names; (b) only `vcruntime140_1.dll`; (c) empty
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** make missingMSVCRuntime ignore sysDir (prefix the sysDir check with `false &&`); red at case (b) with `msvcp140.dll` reported missing although System32 holds it
**Inject:** llamalibs.go:missingMSVCRuntime
**Pulled:** 2026-10-04 — rang: "two present: got [msvcp140.dll vcruntime140_1.dll], want [vcruntime140_1.dll]"

## Test: a verified runtime zip lands in the lib dir
**Purpose:** fetchMSVCRuntime downloads, verifies and extracts the flat runtime zip into libDir and leaves no zip or temp file behind (R3486)
**Input:** an httptest server serving a flat zip of the three DLL names with dummy contents; the correct SHA-256 of that zip
**Expected:** nil error; the three DLLs exist in libDir with their contents; libDir holds nothing else
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go

## Test: a runtime zip with the wrong hash writes nothing
**Purpose:** a checksum mismatch aborts with an error naming the remedy and extracts no DLL into libDir (R3486). Invisible when violated: an unverified zip extracts fine, so only the hash check stands between a tampered download and a DLL Windows will load.
**Input:** the same server; a SHA-256 of other bytes
**Expected:** an error mentioning `ark embed install` and the Visual C++ Redistributable; libDir holds no DLL and no leftover file
**Refs:** crc-LlamaLibs.md
**Code:** llamalibs_test.go
**Fire alarm:** skip the hash check: drop the checksum-mismatch return in downloadFileVerified, so the unverified zip is renamed into place and extracted; the test goes red with a nil error and the three DLLs present in libDir
**Inject:** modelprovision.go:downloadFileVerified
**Pulled:** 2026-10-04 — rang: TestFetchMSVCRuntimeBadHash "wrong hash accepted" (injected `if false && !strings.EqualFold(got, want)`)
