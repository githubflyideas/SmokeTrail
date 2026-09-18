//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// systemDataDir is %ProgramData%\SmokeTrail — writable by a service running as
// LocalService once the installer grants it, and outside any user profile so the
// data survives the account that installed it.
func systemDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "SmokeTrail")
}
