//go:build !windows

package main

// On Unix a bare `pingping` serves in the foreground, which is what a person at
// a terminal means by it and what a systemd unit invokes. There is no shortcut
// to double-click and no notification area to put back, so the console verb has
// nothing to do here and the caller goes on to serve.
func openConsoleForInstalled(options) (int, bool) { return 0, false }
