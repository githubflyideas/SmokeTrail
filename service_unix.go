//go:build !windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
)

// SmokeTrail's service integration is Windows-specific by design: on Linux the job
// belongs to systemd, which needs a unit file rather than code in the binary.
// See deploy/smoketrail.service.

func isService() bool { return false }

func runAsService(options) error {
	return fmt.Errorf("service mode is Windows-only; on Linux use the systemd unit in deploy/")
}

func runServiceVerb(verb string, _ options) int {
	fmt.Fprintf(os.Stderr,
		"smoketrail: `%s` is Windows-only.\nOn Linux, copy deploy/smoketrail.service to /etc/systemd/system/ and use systemctl.\n",
		verb)
	return 2
}

func setLogOutput(w io.Writer) {
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags)
}
