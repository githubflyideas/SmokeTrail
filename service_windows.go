//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	svcName = "pingping"
	svcDesc = "pingping — link quality monitoring (latency distribution and packet loss)"

	// LocalService is a low-privilege built-in account with no password. We can use
	// it, rather than LocalSystem, only because the prober goes through
	// iphlpapi!IcmpSendEcho2 instead of a raw socket — see icmp_windows.go. The
	// service therefore runs with about the rights of a guest: it can open a
	// listening socket, send ICMP, and write to its own data directory, and nothing
	// else. See docs/adr/0003-windows-service.md.
	svcAccount = `NT AUTHORITY\LocalService`

	// Windows grants a per-service SID, NT SERVICE\pingping, when the service
	// declares one. ACLing the data directory to that SID rather than to
	// LocalService means every other LocalService process on the box — and there
	// are many — cannot read or write our database. It costs one line and one
	// flag, and it is the difference between "low privilege" and "low privilege,
	// isolated".
	svcSID = `NT SERVICE\` + svcName

	// One name for the firewall rule, used to add it and to take it away again.
	fwRule = "pingping console"

	// Where install records what it chose. The tray process starts from a Startup
	// shortcut with no arguments and has no other way to learn which port the
	// service is listening on — hardcoding the default is exactly the bug that
	// made the icon never appear.
	regKey = `SOFTWARE\pingping`
)

// publishInstallInfo records the settings a separate process needs to find the
// service. HKLM, because the tray may run as any signed-in user.
func publishInstallInfo(port int, dataDir string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, regKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetDWordValue("Port", uint32(port)); err != nil {
		return err
	}
	return k.SetStringValue("DataDir", dataDir)
}

// installedPort reports the console port of an installed service, or 0.
func installedPort() int {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regKey, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("Port")
	if err != nil || v == 0 || v > 65535 {
		return 0
	}
	return int(v)
}

func installedDataDir() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("DataDir")
	return v
}

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
		// `Get-WinEvent -ProviderName pingping` tells the same story a terminal
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
	a, err := startApp(cfg, h.opt.port, h.opt.localOnly)
	if err != nil {
		evError(evStartupFailed, "startup failed: %v", err)
		return true, exitStartup
	}
	a.banner(portable)
	evInfo(evStarted, "pingping %s started · %d targets · %s · data in %s",
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
			evInfo(evStopped, "pingping stopped cleanly")
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
			fmt.Fprintf(os.Stderr, "pingping: %v\n", err)
			return 1
		}
		return code
	}
	return doServiceVerb(verb, opt)
}

// afterRegistration marks a failure that happened once the service already
// existed. The distinction is not cosmetic: for three releases the installer
// reported "the service could not be registered" for a service that was
// registered, and every attempt to diagnose it went looking at registration.
// Whatever else is true, a message must not name the wrong step.
type afterRegistration struct{ err error }

func (e afterRegistration) Error() string { return e.err.Error() }
func (e afterRegistration) Unwrap() error { return e.err }

// exitRegisteredNotStarted is the installer's cue to say so. Any other non-zero
// code means the registration itself did not happen.
const exitRegisteredNotStarted = 11

func doServiceVerb(verb string, opt options) int {
	var err error
	if verb == "install" {
		err = installService(opt)
	} else {
		err = uninstallService()
	}
	if err != nil {
		log.Printf("%s failed: %v", verb, err)
		var after afterRegistration
		if errors.As(err, &after) {
			return exitRegisteredNotStarted
		}
		return 1
	}
	return 0
}

// installService registers the service with the flags it was given, so
// `pingping install --port 9000 --days 300` is replayed verbatim on every boot.
// No credential is ever passed here: a service command line lives in the registry
// where every account can read it. The admin password is set in the console on
// first run instead.
//
// Everything after the service itself is best-effort. A missing firewall rule or
// Defender exclusion is worth a warning, not a rollback of a service that is
// otherwise installed and running.
func installService(opt options) error {
	// An installed Windows program is configured in its own interface, not by
	// re-running the installer with a flag — that is a Unix idiom, and carrying it
	// over is what put the bind address on a service command line where nothing
	// could show it and nothing could change it. So --localhost is a flag for a
	// foreground or portable run only. Refusing it here is deliberate: accepting
	// it and quietly doing nothing would be the same trap in the other direction.
	if opt.localOnly {
		return fmt.Errorf("--localhost is not an install option on Windows: the console's " +
			"bind address is a setting.\nInstall first, then set it under Settings -> Console " +
			"(127.0.0.1 is the default, and that is loopback already)")
	}
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
	// The port is recorded in the database, not baked into the service command
	// line. A flag on that command line would outrank the console setting, so
	// changing the port in the console would silently do nothing.
	if opt.port > 0 {
		st, err := NewStore(cfg.DataDir, nil)
		if err != nil {
			return fmt.Errorf("open %s: %w", cfg.DataDir, err)
		}
		err = st.SetConsolePort(opt.port)
		st.Close()
		if err != nil {
			return err
		}
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	// The data directory is pinned explicitly: a service starts with an arbitrary
	// working directory, so a relative default would land somewhere surprising.
	args := append([]string{"run", "--data", cfg.DataDir},
		stripFlags(opt.rawArgs, "data", "log-file", "port", "localhost")...)

	s, reinstalled, err := createOrUpdateService(m, exe, args, mgr.Config{
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
	})
	if err != nil {
		return err
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
			return afterRegistration{fmt.Errorf(
				"the service is registered but cannot write to %s: %v %s", cfg.DataDir, err, out)}
		}
	}

	if err := eventlog.InstallAsEventCreate(svcName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		log.Printf("warning: event log source: %v", err)
	}

	port := strings.TrimPrefix(portOf(cfg.Listen), ":")
	applyFirewallRule(hostOf(cfg.Listen), port)

	// Real-time scanning of an actively-written SQLite file shows up as I/O jitter
	// in exactly the measurements this tool exists to make. Best-effort: Defender
	// may be absent, replaced, or policy-locked.
	if out, err := run("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Add-MpPreference -ExclusionPath '"+cfg.DataDir+"'"); err != nil {
		log.Printf("note: no Defender exclusion for %s (%v %s)", cfg.DataDir, err, out)
	}

	// Record the port before starting: the tray reads this, and an install that
	// started fine but published nothing would leave the icon looking for 8518.
	portNum, _ := strconv.Atoi(port)
	if err := publishInstallInfo(portNum, cfg.DataDir); err != nil {
		log.Printf("warning: could not record the console port for the tray icon: %v", err)
	}

	// Judge by outcome, not by the return code of one call.
	//
	// Start can be refused while the SCM is still reconciling a previous stop —
	// and the installer force-kills the running copy before copying files, which
	// SCM sees as a crash and answers with the recovery action configured above.
	// The service then comes up by itself a few seconds later, and an install
	// that reported failure was looking at the wrong moment: the machine ended up
	// exactly as intended, and the operator was told it had not.
	//
	// So a refused Start is a question, not a verdict. The verdict is whether the
	// service is running shortly afterwards.
	if err := s.Start(); err != nil && !alreadyRunning(err) {
		if running, werr := waitRunning(s, 20*time.Second); !running {
			if werr != nil {
				err = fmt.Errorf("%w (and its state could not be read: %v)", err, werr)
			}
			return afterRegistration{fmt.Errorf(
				"the %s service is registered but would not start: %w", svcName, err)}
		}
		log.Printf("note: start was refused (%v) but the service is running — "+
			"the recovery action brought it up", err)
	}
	if reinstalled {
		log.Printf("updated the existing %s service", svcName)
	} else {
		log.Printf("installed %s", svcName)
	}
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
	registry.DeleteKey(registry.LOCAL_MACHINE, regKey)
	// The data directory is deliberately left in place: uninstalling the service is
	// not a request to throw away the history it collected.
	log.Printf("removed %s (data kept)", svcName)
	return nil
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// applyFirewallRule points the inbound rule at the port the console is actually
// on, or removes it when the console is not on the network at all.
//
// It is called on install and by the `firewall` verb, which is what the tray's
// restart runs. Before that verb existed, the rule was added once at install and
// never touched again — while both the tray and the console told the operator
// that restarting the service "fixes the firewall rule at the same time". It did
// not. Change the port in the console, restart as instructed, and the rule still
// named the old port, with the interface saying the opposite.
//
// A loopback bind gets no rule: there is nothing for it to admit, and a stale
// allow rule for a port nobody can reach is worse than none — it reads, to
// whoever audits it later, like an opening that is in use.
func applyFirewallRule(bind, port string) {
	if isLoopbackBind(bind) {
		if out, err := run("netsh", "advfirewall", "firewall", "delete", "rule",
			"name="+fwRule); err != nil {
			log.Printf("note: no firewall rule to remove (%v %s)", err, out)
		} else {
			log.Printf("console is bound to %s, so the firewall rule was removed", bind)
		}
		return
	}
	// Delete first: netsh adds a second rule of the same name rather than
	// replacing, and a leftover rule for the previous port would keep it open.
	run("netsh", "advfirewall", "firewall", "delete", "rule", "name="+fwRule)
	if out, err := run("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+fwRule, "dir=in", "action=allow", "protocol=TCP",
		"localport="+port, "profile=private,domain",
		"description=pingping web console"); err != nil {
		log.Printf("warning: firewall rule not added — the console will only answer on this machine (%v %s)", err, out)
	}
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

// createOrUpdateService registers the service, or points an existing
// registration at this binary.
//
// It used to refuse outright when the service already existed. That made the
// installer unable to run over itself: the files land, the registration does
// not, and the user is shown "the service could not be registered (exit 1)"
// while the old registration still points at the old executable. Every upgrade
// failed at its last step, and the only way out was to know about a command the
// dialog did not mention. An installer that cannot be run twice is not an
// installer.
//
// Reinstalling is now the same code path as installing, which also means the
// graphical route and `pingping install` cannot diverge.
func createOrUpdateService(m *mgr.Mgr, exe string, args []string, c mgr.Config) (*mgr.Service, bool, error) {
	// CreateService fills these in when they are zero; UpdateConfig does not —
	// it hands the struct straight to ChangeServiceConfig, where a zero service
	// type is not "leave it alone", it is invalid, and Windows answers "the
	// parameter is incorrect" without saying which one.
	//
	// So the config that worked for every fresh install failed for every upgrade,
	// from the moment this function learned to update rather than refuse. Both
	// paths take their defaults from here now, so they cannot disagree again.
	if c.ServiceType == 0 {
		c.ServiceType = windows.SERVICE_WIN32_OWN_PROCESS
	}
	if c.StartType == 0 {
		c.StartType = mgr.StartManual
	}

	s, err := m.OpenService(svcName)
	if err != nil {
		s, err = createWaitingOutDeletion(m, exe, c, args)
		if err != nil {
			return nil, false, err
		}
		return s, false, nil
	}

	// Stop before repointing. A running service holds its executable open, and
	// Windows will accept a new path that then quietly does not take effect
	// until something restarts it — which on an upgrade means the old binary
	// keeps running while the console reports the new version.
	if err := stopAndWait(s, 20*time.Second); err != nil {
		log.Printf("warning: %v", err)
	}

	// UpdateConfig takes the command line as one string, composed the way
	// CreateService composes it from its variadic args.
	c.BinaryPathName = syscall.EscapeArg(exe)
	for _, a := range args {
		c.BinaryPathName += " " + syscall.EscapeArg(a)
	}
	if err := s.UpdateConfig(c); err != nil {
		s.Close()
		if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			// The registration is a corpse: deleted, but not gone until the last
			// handle to it closes. Reconfiguring it is not possible; creating it
			// again is, once Windows lets go.
			ns, cerr := createWaitingOutDeletion(m, exe, c, args)
			if cerr != nil {
				return nil, false, cerr
			}
			return ns, false, nil
		}
		return nil, false, fmt.Errorf("update the existing %s service: %w", svcName, err)
	}
	return s, true, nil
}

// createWaitingOutDeletion registers the service, waiting out a previous
// registration that is still being deleted.
//
// A deleted service does not disappear until every handle to it is closed, and
// until then it exists enough to refuse being recreated: CreateService returns
// ERROR_SERVICE_MARKED_FOR_DELETE. An open Services.msc is all it takes.
//
// There is no way to clear this from user mode. The handles belong to other
// processes, and forcing them shut means reaching into those processes with
// undocumented calls — not something an installer gets to do to a machine. So
// the only honest options are to avoid creating the state and to wait it out.
//
// Avoiding it is the important half, and it is already done elsewhere: this
// program never deletes its own registration to replace it, it reconfigures in
// place. The state comes from someone running `sc delete` by hand.
//
// Waiting is this. It clears within seconds once the handle goes, so twenty
// seconds covers a Services.msc that is about to refresh and a dozen exited
// sc.exe processes. What it cannot cover is a window someone leaves open, and
// the message for that case says what to do in the words of someone who has
// never heard of a handle — because the person running an installer has not.
func createWaitingOutDeletion(m *mgr.Mgr, exe string, c mgr.Config, args []string) (*mgr.Service, error) {
	deadline := time.Now().Add(20 * time.Second)
	for attempt := 1; ; attempt++ {
		s, err := m.CreateService(svcName, exe, c, args...)
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return nil, fmt.Errorf("create service: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"Windows is still removing the previous %s service and will not let "+
					"a new one be created until it finishes. This usually takes a few "+
					"seconds and completes on its own. Close the Services window and "+
					"Task Manager if they are open, then run the installer again. If it "+
					"keeps happening, restart the computer and install once more", svcName)
		}
		if attempt == 1 {
			log.Printf("the previous %s registration is still being deleted; waiting", svcName)
		}
		time.Sleep(time.Second)
	}
}

// stopAndWait asks a service to stop and waits for it to actually be stopped,
// rather than for the stop request to be accepted. Those are not the same
// moment, and the gap is where "the new binary did not take effect" comes from.
func stopAndWait(s *mgr.Service, timeout time.Duration) error {
	if st, err := s.Query(); err == nil && st.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil {
		// Already stopped, never started, or refusing the control. Nothing is
		// holding the binary in the first two cases, and the third surfaces as
		// a failed UpdateConfig with a better message than this one.
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			return nil
		}
	}
	return fmt.Errorf("%s did not stop within %s; the new settings take effect at its next restart", svcName, timeout)
}

func alreadyRunning(err error) bool {
	return errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING)
}

// waitRunning reports whether the service reaches Running within the timeout.
// A service that is starting reports StartPending first, and how long it stays
// there is not ours to decide.
func waitRunning(s *mgr.Service, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		st, err := s.Query()
		if err != nil {
			last = err
		} else {
			switch st.State {
			case svc.Running:
				return true, nil
			case svc.Stopped:
				// Stopped and staying stopped is an answer; a recovery restart
				// would move it to StartPending, so keep looking until the
				// deadline rather than concluding from one sample.
			}
		}
		if time.Now().After(deadline) {
			return false, last
		}
		time.Sleep(time.Second)
	}
}
