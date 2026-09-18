//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	svcName = "SmokeTrail"
	svcDesc = "SmokeTrail — link quality monitoring (latency distribution and packet loss)"

	// LocalService is a low-privilege built-in account with no password. We can use
	// it, rather than LocalSystem, only because the prober goes through
	// iphlpapi!IcmpSendEcho2 instead of a raw socket — see icmp_windows.go. The
	// service therefore runs with about the rights of a guest: it can open a
	// listening socket, send ICMP, and write to its own data directory, and nothing
	// else. See docs/adr/0003-windows-service.md.
	svcAccount = `NT AUTHORITY\LocalService`
)

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// ---- running ----

type handler struct{ opt options }

func runAsService(opt options) error {
	elog, err := eventlog.Open(svcName)
	if err == nil {
		defer elog.Close()
		// A service has no console. Everything the foreground build prints goes to
		// the Application event log instead, so `Get-EventLog -Source SmokeTrail`
		// tells the same story as the terminal would.
		log.SetOutput(eventWriter{elog})
		log.SetFlags(0)
	}
	return svc.Run(svcName, &handler{opt: opt})
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	cfg, portable, err := configure(h.opt)
	if err != nil {
		log.Printf("config: %v", err)
		return true, 1
	}
	a, err := startApp(cfg)
	if err != nil {
		log.Printf("startup: %v", err)
		return true, 1
	}
	a.banner(portable)

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			a.shutdown()
			log.Printf("SmokeTrail shut down")
			return false, 0
		default:
			// Pause/Continue are not accepted, so anything else is the SCM being
			// curious; ignoring it is correct.
		}
	}
	return false, 0
}

type eventWriter struct{ l *eventlog.Log }

func (w eventWriter) Write(p []byte) (int, error) {
	w.l.Info(1, strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

// ---- install / uninstall ----

// installService registers the service with the flags it was given, so
// `smoketrail install --port 9000 --days 300` is replayed verbatim on every boot.
// No credential is ever passed here: a service command line lives in the registry
// where every account can read it. The admin password is set in the console on
// first run instead.
//
// Everything after the service itself is best-effort. A missing firewall rule or
// Defender exclusion is worth a warning, not a rollback of a service that is
// otherwise installed and running.
func installService(opt options) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	cfg, _, err := configure(opt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cfg.DataDir, err)
	}
	// LocalService owns nothing by default; without this it cannot create the
	// database. (OI)(CI)M = modify, inherited by files and subdirectories.
	if out, err := run("icacls", cfg.DataDir, "/grant", `*S-1-5-19:(OI)(CI)M`); err != nil {
		log.Printf("warning: could not grant LocalService write access to %s: %v %s", cfg.DataDir, err, out)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run this from an elevated prompt): %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(svcName); err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists — run `smoketrail uninstall` first", svcName)
	}

	// The data directory is pinned explicitly: a service starts with an arbitrary
	// working directory, so a relative default would land somewhere surprising.
	args := append([]string{"run", "--data", cfg.DataDir}, stripDataFlag(opt.rawArgs)...)
	s, err := m.CreateService(svcName, exe, mgr.Config{
		DisplayName:      svcName,
		Description:      svcDesc,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: svcAccount,
		Dependencies:     []string{"Tcpip"},
	}, args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	if err := eventlog.InstallAsEventCreate(svcName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		log.Printf("warning: event log source: %v", err)
	}

	port := strings.TrimPrefix(portOf(cfg.Listen), ":")
	if out, err := run("netsh", "advfirewall", "firewall", "add", "rule",
		"name=SmokeTrail console", "dir=in", "action=allow", "protocol=TCP",
		"localport="+port); err != nil {
		log.Printf("warning: firewall rule not added: %v %s", err, out)
	}

	// Real-time scanning of an actively-written SQLite file shows up as I/O jitter
	// in exactly the measurements this tool exists to make. Best-effort: Defender
	// may be absent, replaced, or policy-locked.
	if out, err := run("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Add-MpPreference -ExclusionPath '"+cfg.DataDir+"'"); err != nil {
		log.Printf("note: no Defender exclusion for %s (%v %s)", cfg.DataDir, err, out)
	}

	if err := s.Start(); err != nil {
		log.Printf("warning: service registered but did not start: %v", err)
	}
	log.Printf("installed %s, running as %s", svcName, svcAccount)
	log.Printf("data in %s · console on http://localhost:%s", cfg.DataDir, port)
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (run this from an elevated prompt): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", svcName)
	}
	defer s.Close()
	if _, err := s.Control(svc.Stop); err != nil {
		log.Printf("note: stop: %v", err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if err := eventlog.Remove(svcName); err != nil {
		log.Printf("note: event log source: %v", err)
	}
	if out, err := run("netsh", "advfirewall", "firewall", "delete", "rule",
		"name=SmokeTrail console"); err != nil {
		log.Printf("note: firewall rule: %v %s", err, out)
	}
	// The data directory is deliberately left in place: uninstalling the service is
	// not a request to throw away the history it collected.
	log.Printf("removed %s (data kept)", svcName)
	return nil
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// stripDataFlag drops any --data the user passed, because install resolves the
// directory itself and puts it back at the front of the service command line.
func stripDataFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--data" || a == "-data" {
			i++ // also skip its value
			continue
		}
		if strings.HasPrefix(a, "--data=") || strings.HasPrefix(a, "-data=") {
			continue
		}
		out = append(out, a)
	}
	return out
}
