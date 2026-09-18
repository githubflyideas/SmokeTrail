package main

import (
	"os"
	"path/filepath"
)

// Where the database lives, decided by one rule the user never has to read about:
//
//	--data <dir>              explicit, wins over everything
//	<exe dir>/data exists  →  portable mode, keep everything beside the binary
//	otherwise              →  the platform's system location
//
// The portable zip ships an empty data/ directory, the installer does not. That
// single difference is what makes an unzipped copy leave no trace outside its own
// folder while an installed service writes where a service should, with no flag
// and no explanation required of the user.
//
// The Unix system location stays "./data", which is what fogping always used: a
// tarball upgraded in place must keep finding its own history.

func resolveDataDir(override string) (dir string, portable bool) {
	if override != "" {
		return override, false
	}
	if d, ok := portableDir(); ok {
		return d, true
	}
	return systemDataDir(), false
}

// portableDir reports the data directory next to the executable, if it exists.
// A symlinked or unreadable executable path simply means "not portable".
func portableDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	d := filepath.Join(filepath.Dir(exe), "data")
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d, true
	}
	return "", false
}
