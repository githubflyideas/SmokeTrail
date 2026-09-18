//go:build !windows

package main

import "fmt"

// SmokeTrail's service integration is Windows-specific by design: on Linux the job
// belongs to systemd, which needs a unit file rather than code in the binary.
// See deploy/smoketrail.service.

func isService() bool { return false }

func runAsService(options) error {
	return fmt.Errorf("service mode is Windows-only; on Linux use the systemd unit in deploy/")
}

func installService(options) error {
	return fmt.Errorf("`install` is Windows-only; on Linux copy deploy/smoketrail.service to /etc/systemd/system/")
}

func uninstallService() error {
	return fmt.Errorf("`uninstall` is Windows-only")
}
