package main

import (
	"fmt"
	"os"
	"strconv"
)

// runFirewallVerb re-points the inbound rule at whatever the console is
// configured for now. It needs elevation, which is why it is a verb the tray can
// launch rather than something the service does for itself — the service runs as
// NT AUTHORITY\LocalService and cannot touch the firewall at all.
//
// This is the half that was missing. The console's answer to a port change said
// "restart the service — the tray icon can do that, and it fixes the firewall
// rule at the same time", and the tray's restart ran `net stop & net start`,
// which does not go near the firewall. The instruction was confidently wrong,
// which is worse than silence: it sends the next hour of diagnosis somewhere
// else.
func runFirewallVerb(opt options) int {
	cfg, _, err := configure(opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pingping:", err)
		return 2
	}
	st, err := NewStore(cfg.DataDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pingping: open %s: %v\n", cfg.DataDir, err)
		return 2
	}
	bind, port := hostOf(cfg.Listen), portOf(cfg.Listen)
	if b := st.ConsoleBind(); b != "" {
		bind = b
	}
	if p := st.ConsolePort(); p != 0 {
		port = ":" + strconv.Itoa(p)
	}
	st.Close()

	applyFirewallRule(bind, port[1:])
	publishInstallInfo(atoiOr(port[1:], 8518), cfg.DataDir)
	return 0
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}
