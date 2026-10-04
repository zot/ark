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
