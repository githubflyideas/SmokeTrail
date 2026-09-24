package main

import (
	"net"
	"strings"
	"testing"
)

// The console must not be on the network before it has a password.
//
// Until a password exists, POST /api/setup hands the admin account to whoever
// asks: sameOrigin() is a CSRF guard and returns true when there is no Origin
// header, which is every request not made by a browser. The Windows installer
// opens a firewall rule and starts the service as its last act, so a default of
// 0.0.0.0 meant that between the installer finishing and the operator reaching a
// browser, anyone on the subnet could claim the console with one curl — and
// there is no recovery, because the password is a PBKDF2 record with no
// backdoor. The rightful operator's next request is "already set up".
//
// So the default bind is loopback, and this is the test that says so.
func TestDefaultBindIsLoopback(t *testing.T) {
	cfg := defaultConfig()
	host := hostOf(cfg.Listen)
	if !isLoopbackBind(host) {
		t.Fatalf("the default listen address is %q, which puts the unauthenticated "+
			"first-run window on the network", cfg.Listen)
	}
}

// And a store with nothing set must not talk the caller out of that default.
func TestUnsetBindStaysLoopback(t *testing.T) {
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.ConsoleBind(); got != "" {
		t.Fatalf("a fresh store reports a bind of %q; it should report nothing and "+
			"leave the loopback default in place", got)
	}
}

// Opening it up is a deliberate act, and it has to survive a restart — that is
// the whole point of it being a setting rather than a flag on an invisible
// service command line.
func TestBindRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetConsoleBind("0.0.0.0"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	again, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := again.ConsoleBind(); got != "0.0.0.0" {
		t.Fatalf("after a restart the stored bind is %q, want 0.0.0.0", got)
	}
}

// A bind address that parses but belongs to another machine is the dangerous
// case: the service starts, fails to listen, and exits — and the console that
// would let someone undo it is the thing that did not come up. So the check is a
// real bind, not a parse.
func TestValidateBindRefusesWhatCannotBeBound(t *testing.T) {
	for _, ok := range []string{"127.0.0.1", "0.0.0.0"} {
		if err := validateBind(ok); err != nil {
			t.Errorf("validateBind(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com", "localhost", "999.1.1.1", "10.99.99.99"} {
		if err := validateBind(bad); err == nil {
			t.Errorf("validateBind(%q) = nil; a value this machine cannot listen on "+
				"would leave the service dead after the next restart", bad)
		}
	}
}

func TestIsLoopbackBind(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "127.0.0.53": true, "::1": true,
		"0.0.0.0": false, "::": false, "192.168.1.10": false, "": false, "nonsense": false,
	} {
		if got := isLoopbackBind(addr); got != want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", addr, got, want)
		}
	}
}

// A stored value that cannot be bound must be ignored rather than obeyed. The
// machine it was set on may have been renumbered, or the setting may have come
// from a copied database — and neither is a reason to leave a host with no
// console.
func TestUnusableStoredBindIsIgnored(t *testing.T) {
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSetting(settingBind, "10.99.99.99"); err != nil {
		t.Fatal(err)
	}
	if got := s.ConsoleBind(); got != "" {
		t.Fatalf("ConsoleBind returned %q for an address this machine has no "+
			"interface for; the service would fail to start", got)
	}
}

// The one that started this: --localhost must not end up on the service command
// line, where the console cannot show it, the console cannot change it, and the
// only way to find it is `sc qc`. It is a stored setting like the port.
func TestLocalhostIsNotBakedIntoTheServiceCommandLine(t *testing.T) {
	src := readSource(t, "service_windows.go")

	// The service argument list specifically — not the elevation relaunch a few
	// lines above, which strips only --log-file because it has to re-run the
	// operator's own `install` command verbatim.
	i := strings.Index(src, `[]string{"run", "--data", cfg.DataDir}`)
	if i < 0 {
		t.Fatal("the service argument list is no longer built here")
	}
	i = strings.Index(src[i:], "stripFlags(") + i
	line := src[i:]
	if end := strings.Index(line, ")...)"); end > 0 {
		line = line[:end]
	}
	if !strings.Contains(line, `"localhost"`) {
		t.Errorf("--localhost survives into the service command line (%s): it becomes "+
			"invisible and permanent, and one support round went firewall -> network "+
			"profile -> port 80 before the answer turned out to be a word in the registry", line)
	}
}

// The console tells the operator that restarting the service re-points the
// firewall rule. That sentence was false for several releases: the tray ran
// `net stop & net start`, which does not go near the firewall, so a port change
// left the rule on the old port with the interface claiming otherwise. A wrong
// instruction is worse than none — it sends the next hour somewhere else.
func TestRestartActuallyRepointsTheFirewall(t *testing.T) {
	web := readSource(t, "web.go")
	if !strings.Contains(web, "re-points the firewall rule") {
		t.Skip("the console no longer promises this, so there is nothing to keep true")
	}
	tray := readSource(t, "tray_windows.go")

	restart := tray[strings.Index(tray, "case idRestart:"):]
	if end := strings.Index(restart, "\tcase id"); end > 0 {
		restart = restart[:end]
	}
	if !strings.Contains(restart, "firewall") {
		t.Error("the console promises that a restart re-points the firewall rule, " +
			"but the tray's restart never invokes the firewall verb")
	}
}

// Whatever else changes, a loopback console must get no firewall rule: there is
// nothing for it to admit, and a leftover allow rule reads to whoever audits it
// later like an opening that is in use.
func TestLoopbackGetsNoFirewallRule(t *testing.T) {
	src := readSource(t, "service_windows.go")

	fn := src[strings.Index(src, "func applyFirewallRule("):]
	if end := strings.Index(fn, "\n// stripFlags"); end > 0 {
		fn = fn[:end]
	}
	guard := strings.Index(fn, "isLoopbackBind(bind)")
	add := strings.Index(fn, `"add", "rule"`)
	if guard < 0 {
		t.Fatal("applyFirewallRule does not check for a loopback bind")
	}
	if add > 0 && add < guard {
		t.Error("applyFirewallRule opens the port before it checks whether the " +
			"console is even on the network")
	}
}

// Sanity: the address the tests above call loopback is one Go agrees about.
func TestLoopbackMatchesTheStdlib(t *testing.T) {
	if !net.ParseIP(defaultBind).IsLoopback() {
		t.Fatalf("defaultBind %q is not a loopback address", defaultBind)
	}
}

// An installed Windows program is configured in its own interface. Carrying the
// Unix idiom over — re-run the installer with a flag — is what put the bind
// address on a service command line that nothing could show and nothing could
// change. Accepting the flag and quietly ignoring it would be the same trap
// facing the other way, so install refuses it and says where the setting is.
func TestInstallRefusesTheBindFlag(t *testing.T) {
	src := readSource(t, "service_windows.go")

	fn := src[strings.Index(src, "func installService("):]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}
	if !strings.Contains(fn, "opt.localOnly") {
		t.Fatal("installService no longer mentions --localhost: it either accepts " +
			"it silently or bakes it in again")
	}
	guard := strings.Index(fn, "if opt.localOnly {")
	if guard < 0 || !strings.Contains(fn[guard:min(guard+400, len(fn))], "return fmt.Errorf") {
		t.Error("install does not refuse --localhost; a flag that appears to work " +
			"and does nothing is worse than one that is rejected")
	}
	if strings.Contains(fn, "SetConsoleBind") {
		t.Error("install writes the bind setting from a flag — the console owns " +
			"that setting on Windows")
	}
}

// hostHasLoopback reports whether this machine can listen on a given loopback
// address at all. CI has IPv6; the sandbox this was written in does not; a
// hardened host may have neither. The test below adapts rather than assuming.
func hostHasLoopback(addr string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// "This machine only" means every loopback address this machine has.
//
// Go picks the socket family from the address: 0.0.0.0 is a wildcard, so it
// opens AF_INET6 with ipv6only=false and one socket serves both families.
// 127.0.0.1 is not a wildcard, so it opens AF_INET and serves IPv4 alone. That
// difference turned a working install into one that could not be opened at all
// the moment the default changed from the first to the second, because Windows
// resolves localhost to ::1 — on a machine whose own netstat had been showing
// [::1] connections the whole time.
func TestLoopbackListensOnEveryLoopbackFamily(t *testing.T) {
	lns, err := listenOn("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenOn(loopback): %v", err)
	}
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()

	got := map[string]bool{}
	for _, ln := range lns {
		host, _, _ := net.SplitHostPort(ln.Addr().String())
		got[host] = true
	}
	for _, want := range []string{"127.0.0.1", "::1"} {
		if !hostHasLoopback(want) {
			continue // this host does not have that family; nothing to expect
		}
		if !got[want] {
			t.Errorf("a loopback console does not listen on %s, which this machine has: "+
				"a client resolving localhost to that address gets connection refused "+
				"from a console that is running perfectly well", want)
		}
	}
}

// The port has to be the same one on both, or "the console is on 8518" stops
// being true depending on which address you reach it by.
func TestLoopbackListenersShareThePort(t *testing.T) {
	lns, err := listenOn("127.0.0.1:8793")
	if err != nil {
		t.Fatalf("listenOn: %v", err)
	}
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()
	for _, ln := range lns {
		if _, port, _ := net.SplitHostPort(ln.Addr().String()); port != "8793" {
			t.Errorf("listener on %s is on port %s, want 8793", ln.Addr(), port)
		}
	}
}

// A specific interface address is one listener, not a family sweep: asking for
// 0.0.0.0 or for one NIC means what it says.
func TestNonLoopbackBindIsExactlyWhatWasAsked(t *testing.T) {
	lns, err := listenOn("0.0.0.0:0")
	if err != nil {
		t.Fatalf("listenOn(wildcard): %v", err)
	}
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()
	if len(lns) != 1 {
		t.Fatalf("a wildcard bind opened %d listeners, want 1 — Go already makes "+
			"that socket dual-stack", len(lns))
	}
}

// And a bind nothing can satisfy still fails, rather than quietly serving on
// nothing at all.
func TestListenOnFailsWhenNothingCanBind(t *testing.T) {
	if _, err := listenOn("10.99.99.99:0"); err == nil {
		t.Fatal("listenOn accepted an address this machine has no interface for")
	}
}

// The decision, separated from the network so it can be checked on a host with
// no IPv6 — which is where this fix was written, and exactly the kind of host
// that would have let the bug through a second time.
func TestBindTargetsExpandsLoopbackToBothFamilies(t *testing.T) {
	for host, want := range map[string][]string{
		"127.0.0.1":    {"127.0.0.1", "::1"},
		"127.0.0.53":   {"127.0.0.1", "::1"},
		"::1":          {"127.0.0.1", "::1"},
		"0.0.0.0":      {"0.0.0.0"},
		"::":           {"::"},
		"192.168.1.10": {"192.168.1.10"},
	} {
		got := bindTargets(host)
		if len(got) != len(want) {
			t.Errorf("bindTargets(%q) = %v, want %v", host, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("bindTargets(%q) = %v, want %v", host, got, want)
				break
			}
		}
	}
}
