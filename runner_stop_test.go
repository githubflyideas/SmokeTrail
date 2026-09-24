package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// probeGoroutines counts goroutines actually inside a probe loop.
//
// runtime.NumGoroutine() counts the whole process, and comparing it before and
// after attributes every goroutine anyone starts in between to the Runner. That
// is what this test did, and it failed on Windows — where the SQLite driver and
// the HTTP stack start their own — for a leak that was not there. The invariant
// was never "the process returned to its previous count"; it is "no probe loop
// is still running", so count those.
func probeGoroutines() int {
	buf := make([]byte, 1<<20)
	dump := string(buf[:runtime.Stack(buf, true)])
	n := 0
	for _, g := range strings.Split(dump, "\n\n") {
		// Two things to match, for two reasons.
		//
		// The symbol is qualified by the module path, not by "main": the package
		// is main but the stack reads
		// github.com/githubflyideas/pingping.probeLoop. Matching "main.probeLoop"
		// found nothing, and the test reported that as "Reload started no
		// goroutines" rather than as its own mistake.
		//
		// And a goroutine that has been created but not yet scheduled has no
		// probeLoop frame at all — only the "created by ... apply" line. Counting
		// only the frame makes this test depend on the scheduler having run,
		// which is the kind of flake that gets a test deleted rather than fixed.
		if strings.Contains(g, ".probeLoop(") || strings.Contains(g, "(*Runner).apply") {
			n++
		}
	}
	return n
}

// A leaked probe goroutine is invisible on Linux and fatal on Windows: it keeps
// the database open, and a file that is open cannot be deleted there, so
// t.TempDir()'s own cleanup fails and takes the test with it — for reasons that
// have nothing to do with what the test was checking.
//
// Two tests leaked one each for every release so far.
func TestRunnerStopLeavesNoProbeGoroutines(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(ProbeCfg{Packets: 1, TimeoutMs: 50, IntervalSec: 1}, s, NewDetector(s))

	if n := probeGoroutines(); n != 0 {
		t.Fatalf("%d probe goroutines were already running before this test", n)
	}
	for _, n := range []string{"a", "b", "c"} {
		if _, err := s.CreateTarget(TargetCfg{Name: n, Type: "tcp", Host: "127.0.0.1", Port: 1, IntervalSec: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := len(r.Targets()); got != 3 {
		t.Fatalf("expected 3 probe loops running, got %d", got)
	}
	if n := probeGoroutines(); n == 0 {
		t.Fatal("Reload started no probe goroutines; this test is not testing anything")
	}

	r.Stop()

	// Stop waits, so this needs no sleep — that is the property being checked.
	// A Stop that only asked would leave these behind and the count would settle
	// later, or not at all.
	if got := len(r.Targets()); got != 0 {
		t.Fatalf("%d targets still live after Stop", got)
	}
	if n := probeGoroutines(); n != 0 {
		t.Errorf("Stop returned with %d probe goroutines still running", n)
	}

	// The Windows condition, as closely as Linux can state it: everything that
	// held the database has let go, so the directory can be taken away.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("something still holds the data directory: %v", err)
	}
}

// Stop has to be safe to call when nothing is running, because shutdown paths
// reach it from more than one direction.
func TestRunnerStopIsSafeWhenIdle(t *testing.T) {
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := NewRunner(ProbeCfg{Packets: 1, TimeoutMs: 50}, s, NewDetector(s))

	done := make(chan struct{})
	go func() { r.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked with no probes running")
	}
}
