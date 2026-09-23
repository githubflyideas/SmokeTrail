//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
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

	// Windows grants a per-service SID, NT SERVICE\SmokeTrail, when the service
	// declares one. ACLing the data directory to that SID rather than to
	// LocalService means every other LocalService process on the box — and there
	// are many — cannot read or write our database. It costs one line and one
	// flag, and it is the difference between "low privilege" and "low privilege,
	// isolated".
	svcSID = `NT SERVICE\` + svcName

	// One name for the firewall rule, used to add it and to take it away again.
	fwRule = "SmokeTrail console"
)

// Event IDs. A Windows administrator filters and alerts on these, so they are a
// public interface: stable numbers, grouped by severity, never renumbered.
//
//	1xxx  informational lifecycle
//	2xxx  warnings — degraded but running
//	3xxx  errors — did not start, or stopped unexpectedly
const (
	evStarted      = 1000
	evStopped      = 1001
	evTargetChange = 1100
	evGeneric      = 1900 // anything routed through the standard logger

	evProbeFailed     = 2000
	evInstallDegraded = 2100

	evStartupFailed = 3000
	evConfigInvalid = 3001
)

// Service-specific exit codes, reported to the SCM so `sc query` and a recovery
// policy can tell "the port was taken" from "the database would not open".
const (
	exitConfig  = 10
	exitStartup = 11
)

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// ---- running ----

type handler struct{ opt options }

var elog *eventlog.Log

func runAsService(opt options) error {
	l, err := eventlog.Open(svcName)
	if err == nil {
		elog = l
		defer l.Close()
		// A service has no console. Everything the foreground build prints goes to
		// the Application event log instead, so
		// `Get-WinEvent -ProviderName SmokeTrail` tells the same story a terminal
		// would — but with event IDs an administrator can filter on.
		log.SetOutput(eventWriter{l})
		log.SetFlags(0)
	}
	return svc.Run(svcName, &handler{opt: opt})
}

func evInfo(id uint32, format string, a ...any) {
	if elog != nil {
		elog.Info(id, fmt.Sprintf(format, a...))
		return
	}
	log.Printf(format, a...)
}

func evError(id uint32, format string, a ...any) {
	if elog != nil {
		elog.Error(id, fmt.Sprintf(format, a...))
		return
	}
	log.Printf(format, a...)
}

// setLogOutput is the one place the standard logger is redirected, so the
// elevation path and the service path cannot disagree about it.
func setLogOutput(w io.Writer) {
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags)
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	cfg, portable, err := configure(h.opt)
	if err != nil {
		evError(evConfigInvalid, "configuration rejected: %v", err)
		return true, exitConfig
	}
	a, err := startApp(cfg)
	if err != nil {
		evError(evStartupFailed, "startup failed: %v", err)
		return true, exitStartup
	}
	a.banner(portable)
	evInfo(evStarted, "SmokeTrail %s started · %d targets · %s · data in %s",
		version, len(a.run.Targets()), cfg.Listen, cfg.DataDir)

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			// WaitHint keeps the SCM from declaring us hung while the final flush
			// and the graceful HTTP drain complete.
			status <- svc.Status{State: svc.StopPending, WaitHint: 8000}
			a.shutdown()
			evInfo(evStopped, "SmokeTrail stopped cleanly")
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
	w.l.Info(evGeneric, strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

// ---- install / uninstall ----

// runServiceVerb is the whole privileged path: elevate if we must, run the verb,
// and report an exit code. Three shapes of invocation land here — an operator in a
// normal prompt, the elevated child that operator's consent produced, and the
// installer, which is already elevated — and each takes exactly one branch.
func runServiceVerb(verb string, opt options) int {
	if opt.logFile != "" { // we are the elevated child
		done := redirectToLogFile(opt.logFile)
		code := doServiceVerb(verb, opt)
		done(code)
		return code
	}
	if !isElevated() {
		code, err := elevateSelf(verb, stripFlags(opt.rawArgs, "log-file"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "smoketrail: %v\n", err)
			return 1
		}
		return code
	}
	return doServiceVerb(verb, opt)
}

func doServiceVerb(verb string, opt options) int {
	var err error
	if verb == "install" {
		err = installService(opt)
	} else {
		err = uninstallService()
	}
	if err != nil {
		log.Printf("%s failed: %v", verb, err)
		return 1
	}
	return 0
}

// installService registers the service with the flags it was given, so
// `SmokeTrail install --port 9000 --days 300` is replayed verbatim on every boot.
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

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(svcName); err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists — run `SmokeTrail uninstall` first", svcName)
	}

	// The data directory is pinned explicitly: a service starts with an arbitrary
	// working directory, so a relative default would land somewhere surprising.
	args := append([]string{"run", "--data", cfg.DataDir},
		stripFlags(opt.rawArgs, "data", "log-file")...)

	s, err := m.CreateService(svcName, exe, mgr.Config{
		DisplayName:      svcName,
		Description:      svcDesc,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: svcAccount,
		// Probing is pointless before the stack is up, and nothing else on the
		// machine is waiting on us, so we yield the boot path to services that
		// are. Delayed start is the polite default for this kind of workload.
		DelayedAutoStart: true,
		Dependencies:     []string{"Tcpip", "Dnscache"},
		// Ask for a per-service SID so the data directory can be locked to this
		// service rather than to every LocalService process on the box.
		SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}, args...)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// A monitor that quietly stays down after a transient failure is worse than
	// useless: the flat line in the chart looks like a healthy link. Restart, with
	// a backoff, and reset the counter daily.
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 20 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400); err != nil {
		log.Printf("warning: recovery actions not set — the service will stay down after a crash: %v", err)
	}

	// The service SID exists only once the service does, so the ACL comes after
	// CreateService. (OI)(CI)M = modify, inherited by files and subdirectories.
	if out, err := run("icacls", cfg.DataDir, "/grant", svcSID+":(OI)(CI)M"); err != nil {
		log.Printf("warning: could not grant %s write access to %s (%v %s)", svcSID, cfg.DataDir, err, out)
		log.Printf("         falling back to LocalService, which is broader than intended")
		if out, err := run("icacls", cfg.DataDir, "/grant", `*S-1-5-19:(OI)(CI)M`); err != nil {
			return fmt.Errorf("the service cannot write to %s: %v %s", cfg.DataDir, err, out)
		}
	}

	if err := eventlog.InstallAsEventCreate(svcName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		log.Printf("warning: event log source: %v", err)
	}

	port := strings.TrimPrefix(portOf(cfg.Listen), ":")
	if out, err := run("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+fwRule, "dir=in", "action=allow", "protocol=TCP",
		"localport="+port, "profile=private,domain",
		"description=SmokeTrail web console"); err != nil {
		log.Printf("warning: firewall rule not added — the console will only answer on this machine (%v %s)", err, out)
	}

	// Real-time scanning of an actively-written SQLite file shows up as I/O jitter
	// in exactly the measurements this tool exists to make. Best-effort: Defender
	// may be absent, replaced, or policy-locked.
	if out, err := run("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Add-MpPreference -ExclusionPath '"+cfg.DataDir+"'"); err != nil {
		log.Printf("note: no Defender exclusion for %s (%v %s)", cfg.DataDir, err, out)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("service registered but would not start: %w", err)
	}
	log.Printf("installed %s", svcName)
	log.Printf("  account   %s (per-service SID %s)", svcAccount, svcSID)
	log.Printf("  start     automatic (delayed)")
	log.Printf("  recovery  restart after 5s, 20s, 60s")
	log.Printf("  data      %s", cfg.DataDir)
	log.Printf("  console   http://localhost:%s   ← open this now to set the admin password", port)
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
		"name="+fwRule); err != nil {
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

// stripFlags removes the named flags and their values. `install` uses it to drop
// --data (it resolves the directory itself and puts it back at the front) and
// --log-file (an artefact of elevation that must never reach the boot-time
// command line).
func stripFlags(args []string, names ...string) []string {
	drop := map[string]bool{}
	for _, n := range names {
		drop["--"+n] = true
		drop["-"+n] = true
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if drop[a] {
			i++ // also skip its value
			continue
		}
		if k, _, ok := strings.Cut(a, "="); ok && drop[k] {
			continue
		}
		out = append(out, a)
	}
	return out
}
