//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// systemDataDir is %ProgramData%\pingping — writable by a service running as
// LocalService once the installer grants it, and outside any user profile so the
// data survives the account that installed it.
func systemDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "pingping")
}

// legacySystemDirs are the system data directories earlier names used. Only
// SmokeTrail had one on Windows: fogping never shipped a Windows build, so its
// database can only ever turn up beside a portable copy, which the same-directory
// check already covers.
func legacySystemDirs() []string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return []string{filepath.Join(base, "SmokeTrail")}
}
