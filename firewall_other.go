//go:build !windows

package main

import (
	"fmt"
	"os"
)

// There is no firewall for pingping to manage on Unix: a package or an operator
// owns those rules, and a program that edited nftables behind their back would
// be a worse neighbour than one that does nothing.
func runFirewallVerb(options) int {
	fmt.Fprintln(os.Stderr, "pingping: the firewall verb is Windows-only; "+
		"open the console port with whatever manages rules on this machine.")
	return 2
}
