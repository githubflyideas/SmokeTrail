//go:build !windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
)

// pingping's service integration is Windows-specific by design: on Linux the job
// belongs to systemd, which needs a unit file rather than code in the binary.
// See deploy/pingping.service.

func isService() bool { return false }

func runAsService(options) error {
	return fmt.Errorf("service mode is Windows-only; on Linux use the systemd unit in deploy/")
}

func runServiceVerb(verb string, _ options) int {
	fmt.Fprintf(os.Stderr,
		"pingping: `%s` is Windows-only.\nOn Linux, copy deploy/pingping.service to /etc/systemd/system/ and use systemctl.\n",
		verb)
	return 2
}

// No tray on Unix: the desktop conventions differ per environment and a link
// monitor there lives under systemd, not in a notification area.
func runTrayCompanion(options) int { return 2 }

func startForegroundTray(string, func()) {}

func setLogOutput(w io.Writer) {
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags)
}
