package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// logf is the one log entry point the store layer uses, so a future move to
// structured logging touches one place.
func logf(format string, v ...any) { log.Printf(format, v...) }

// app is one running instance: store, probe runner, housekeeping and the web
// console. Foreground mode and the Windows service both drive it through exactly
// this pair of calls, so the two paths cannot drift apart.
type app struct {
	cfg   *Config
	store *Store
	run   *Runner
	srv   *http.Server
	lns   []net.Listener
	stopC chan struct{}
}

// demoTarget is seeded into a brand-new database so the very first launch shows smoke.
var demoTarget = TargetCfg{Name: "Demo", Type: "icmp", Host: "www.google.com", Pace: "fast"}

// The listener is bound here rather than inside the serving goroutine on purpose:
// a Windows service that reported Running while its port was already taken would
// sit in the service list looking healthy with no console to open. Binding first
// means that failure is a startup error the user sees, in the Event Log or on the
// terminal, at the moment it happens.
// startApp brings everything up. portOverride is the --port flag, which wins over
// the stored setting; 0 means "whatever the console was configured with".
func startApp(cfg *Config, portOverride int, bindOverridden bool) (*app, error) {
	store, err := NewStore(cfg.DataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("store init failed: %w", err)
	}
	// The port is a stored setting rather than a service command-line argument,
	// so an operator can change it where they will look for it. A flag still wins,
	// which is what keeps a portable copy and `--localhost` predictable.
	// The bind address is a stored setting for the same reason the port is: on
	// Windows the alternative is a service command line, which lives in the
	// registry, is invisible from the console, and can only be changed by
	// re-registering the service. `--localhost` used to be baked in there, where
	// nothing in the interface could show it and nothing could undo it.
	//
	// A flag still wins, so a foreground or portable run stays predictable.
	if portOverride == 0 {
		if p := store.ConsolePort(); p != 0 {
			cfg.Listen = hostOf(cfg.Listen) + ":" + strconv.Itoa(p)
		}
	}
	if !bindOverridden {
		if b := store.ConsoleBind(); b != "" {
			cfg.Listen = b + portOf(cfg.Listen)
		}
	}
	if n, err := store.TargetRows(); err == nil && n == 0 {
		if _, err := store.CreateTarget(demoTarget); err == nil {
			log.Printf("new database — seeded a demo target (www.google.com)")
		}
	}

	detector := NewDetector(store)
	run := NewRunner(cfg.Probe, store, detector)
	if err := run.Reload(); err != nil {
		store.Close()
		return nil, fmt.Errorf("load targets: %w", err)
	}

	lns, err := listenOn(cfg.Listen)
	if err != nil {
		store.Close()
		return nil, err
	}

	a := &app{
		cfg:   cfg,
		store: store,
		run:   run,
		lns:   lns,
		stopC: make(chan struct{}),
		srv:   &http.Server{Handler: newMux(cfg, store, run)},
	}
	go store.flushLoop(a.stopC)
	go housekeeping(cfg, store, a.stopC)
	for _, ln := range lns {
		go func(ln net.Listener) {
			if err := a.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("web server on %s: %v", ln.Addr(), err)
			}
		}(ln)
	}
	return a, nil
}

// listenOn opens the console's listeners. Loopback needs two of them, and that
// is the whole reason this function exists.
//
// Go picks the socket family from the address: a wildcard like 0.0.0.0 is a
// wildcard in favoriteAddrFamily, so Go opens AF_INET6 with ipv6only=false and
// one socket serves both families. 127.0.0.1 is not a wildcard, so Go opens
// AF_INET and the socket serves IPv4 only — which means a browser that resolves
// localhost to ::1, as Windows does, gets connection refused from a console that
// is running perfectly well.
//
// That is not a theoretical concern: changing the default from 0.0.0.0 to
// 127.0.0.1 turned a working install into one that could not be opened at all,
// on a machine whose own netstat had been showing [::1] connections the whole
// time. "This machine only" means both of this machine's loopback addresses, so
// bind both and require only that one of them succeeds — a host with IPv6
// disabled is normal, and so is one with no IPv4 loopback.
// bindTargets expands a bind address into the addresses actually opened. It is
// separate from listenOn so the decision can be tested on a host that has no
// IPv6 at all, which is where this fix was written.
func bindTargets(host string) []string {
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return []string{"127.0.0.1", "::1"}
	}
	return []string{host}
}

func listenOn(addr string) ([]net.Listener, error) {
	host, port := hostOf(addr), portOf(addr)

	var lns []net.Listener
	var firstErr error
	for _, h := range bindTargets(host) {
		ln, err := net.Listen("tcp", net.JoinHostPort(h, port[1:]))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		lns = append(lns, ln)
	}
	if len(lns) == 0 {
		return nil, fmt.Errorf("cannot listen on %s: %w", addr, firstErr)
	}
	return lns, nil
}

// shutdown stops probing and housekeeping, drains the console, and makes the final
// flush. Safe to call once.
func (a *app) shutdown() {
	close(a.stopC) // housekeeping: rollup, retention, reclaim
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.srv.Shutdown(ctx)

	// Then the probe loops, and wait for them. This line was missing: stopC is
	// the app's own channel and the Runner owns a separate stop per target, so
	// the store below was being closed while probes were still writing to it.
	// The order matters — probes, then console, then the final flush — because
	// each one can still use what the next one closes.
	a.run.Stop()

	if err := a.store.Close(); err != nil { // final flush; waits for in-flight queries
		log.Printf("close: %v", err)
	}
}

// banner is what an operator reads to confirm the thing is doing what they meant.
func (a *app) banner(portable bool) {
	where := a.cfg.DataDir
	if portable {
		where += " (portable)"
	}
	log.Printf("pingping %s up · %d targets · listening on %s · data in %s · %d-day retention",
		version, len(a.run.Targets()), a.cfg.Listen, where, a.cfg.RetentionDays)
	if a.store.NeedsSetup() {
		log.Printf("➜  FIRST RUN: open http://localhost%s to set the admin password", portOf(a.cfg.Listen))
		return
	}
	log.Printf("➜  open http://localhost%s for the smoke graph", portOf(a.cfg.Listen))
	if len(a.run.Targets()) == 0 {
		log.Printf("no active targets — add them in the web console")
	}
}

// hostOf returns everything before the final colon, so --port can replace the port
// without discarding a bind address.
func hostOf(listen string) string {
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			return listen[:i]
		}
	}
	return "0.0.0.0"
}

// housekeeping: rollup on start and every 5 minutes; retention nightly at 00:05.
func housekeeping(cfg *Config, store *Store, stop chan struct{}) {
	sched := &rollupSched{hot: time.Duration(cfg.HotDays) * 24 * time.Hour}
	sched.tick(store, time.Now())
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	last := ""
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		sched.tick(store, now)
		if day := now.Format("2006-01-02"); now.Hour() == 0 && now.Minute() == 5 && last != day {
			last = day
			store.Retention(now, cfg.HotDays, cfg.RetentionDays)
		}
	}
}

func portOf(listen string) string {
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			return listen[i:]
		}
	}
	return ":8518"
}
