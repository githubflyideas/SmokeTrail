//go:build !windows

package main

// systemDataDir on Unix stays "./data": that is where every fogping release kept
// its history, and an in-place upgrade must not silently start a new database
// somewhere else. A packaged service overrides it with --data.
func systemDataDir() string { return "./data" }
