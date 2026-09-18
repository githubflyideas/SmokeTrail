# 1. Pure-Go SQLite (modernc.org/sqlite), not mattn/go-sqlite3

Status: accepted · 2026-09

## Context

fogping used `github.com/mattn/go-sqlite3`, which is a cgo binding to the SQLite C
amalgamation. On Linux that costs nothing: a C toolchain is always there.

Producing a Windows build changes the arithmetic. cgo means `CGO_ENABLED=1`, which
means cross-compiling from Linux CI needs a mingw-w64 toolchain, and the resulting
binary links against a C runtime. Both work, and both are a permanent tax on every
release: a CI image to maintain, a class of build failures that only appear on one
platform, and a binary that is no longer obviously self-contained.

## Decision

Link `modernc.org/sqlite`, a pure-Go translation of SQLite, and build everything
with `CGO_ENABLED=0`.

## Consequences

Good:

- `GOOS=windows GOARCH=amd64 go build` is the entire Windows build. No toolchain,
  no container, no per-platform CI job. The same command produces linux, darwin,
  windows/amd64, windows/386 and windows/arm64.
- The .exe has no DLL beside it and no runtime to install. This is the claim the
  whole Windows story rests on, and cgo would have quietly made it false.

Bad, and accepted:

- Writes are roughly 1.5–2x slower than the C library. Irrelevant here: a probe
  round is a handful of rows every 15–300 seconds, batched into one transaction,
  and the hourly rollup runs in the background off the probe path.
- The binary grows by roughly 10 MB. A ~20 MB single file is still a better
  install experience than a 3 MB file plus a runtime.

## Notes

The driver name is behind `sqlDriver` in `sqlite.go`, and the one place that cared
about a driver-specific error type (unique-constraint detection in `targets.go`)
now goes through `isUniqueViolation`. Swapping drivers again should touch only
that file.
