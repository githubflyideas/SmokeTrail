package main

import (
	"strings"

	_ "modernc.org/sqlite"
)

// pingping links the pure-Go SQLite driver rather than mattn/go-sqlite3, so the
// whole program builds with CGO_ENABLED=0. That is what makes
// `GOOS=windows go build` a one-liner and the result a single .exe with no MSVC
// runtime, no mingw in CI, and no DLL beside it. The cost is roughly 1.5-2x on
// write throughput, which is irrelevant here: a round is a handful of rows every
// 15 to 300 seconds, batched, and the rollup runs in the background.
// See docs/adr/0001-sqlite-driver.md.
const sqlDriver = "sqlite"

// isUniqueViolation keeps the driver's error taxonomy out of the rest of the code.
// The previous driver exported a typed error for this; modernc reports it as a
// message, and a future swap should only have to touch this function. The targets
// table has exactly one UNIQUE constraint (name), so matching on the class of
// error is enough — no need to parse which column.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "constraint failed: UNIQUE")
}
