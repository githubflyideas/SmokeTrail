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
	ln    net.Listener
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
func startApp(cfg *Config, portOverride int) (*app, error) {
	store, err := NewStore(cfg.DataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("store init failed: %w", err)
	}
	// The port is a stored setting rather than a service command-line argument,
	// so an operator can change it where they will look for it. A flag still wins,
	// which is what keeps a portable copy and `--localhost` predictable.
	if portOverride == 0 {
		if p := store.ConsolePort(); p != 0 {
			cfg.Listen = hostOf(cfg.Listen) + ":" + strconv.Itoa(p)
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

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("cannot listen on %s: %w", cfg.Listen, err)
	}

	a := &app{
		cfg:   cfg,
		store: store,
		run:   run,
		ln:    ln,
		stopC: make(chan struct{}),
		srv:   &http.Server{Handler: newMux(cfg, store, run)},
	}
	go store.flushLoop(a.stopC)
	go housekeeping(cfg, store, a.stopC)
	go func() {
		if err := a.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("web server: %v", err)
		}
	}()
	return a, nil
}

// shutdown stops probing and housekeeping, drains the console, and makes the final
// flush. Safe to call once.
func (a *app) shutdown() {
	close(a.stopC)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.srv.Shutdown(ctx)
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
