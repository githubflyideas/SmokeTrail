package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"time"
)

// selftest is the M0 gate for the Windows port, kept in the shipping binary so any
// user can re-run it on their own hardware.
//
// pingping's whole claim is that it shows the SHAPE of a latency distribution,
// not an average. That claim rests on two host properties that are free on Linux
// and were, before this was measured, merely assumed on Windows:
//
//   - the clock must resolve well below one millisecond, or sub-ms RTTs quantise
//     into a staircase and the distribution is an artifact of the timer;
//   - the scheduler must wake a sleeping goroutine close to when it was asked, or
//     the gap between packets within a round drifts and the samples stop being
//     evenly spaced in time.
//
// Windows historically ran a ~15.6ms default timer tick, which would fail the
// second test badly. Go asks for a high-resolution waitable timer on Windows 10
// 1803 and later, so the expectation is that both pass — but "expected to pass" is
// not a measurement, and if they fail on a given box the RTT oscilloscope premise
// does not hold there. Hence a command rather than a comment.
func runSelftest(w io.Writer) {
	fmt.Fprintf(w, "pingping %s selftest — %s/%s, %d CPUs, Go %s\n\n",
		version, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())

	res := clockResolution()
	fmt.Fprintf(w, "clock resolution      %v\n", res)
	fmt.Fprintf(w, "  smallest non-zero step time.Now() can report. RTTs below this are unmeasurable.\n")
	verdict(w, res <= 10*time.Microsecond,
		"fine enough for sub-millisecond RTT",
		"TOO COARSE — LAN-range RTTs will quantise; the distribution would be an artifact")

	fmt.Fprintln(w)
	for _, d := range []time.Duration{time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond} {
		o := sleepOvershoot(d, 60)
		fmt.Fprintf(w, "sleep %-6v overshoot  p50 %-10v p90 %-10v p99 %-10v max %v\n",
			d, o.p50, o.p90, o.p99, o.max)
	}
	fmt.Fprintf(w, "  how late a goroutine actually wakes. This is the jitter between packets in a round.\n")

	o := sleepOvershoot(50*time.Millisecond, 60)
	verdict(w, o.p99 <= 5*time.Millisecond,
		"pacing is tight; packets in a round are evenly spaced",
		"COARSE — likely a 15.6ms timer tick; intra-round spacing will be uneven")

	fmt.Fprintln(w)
	t := tickerDrift(20*time.Millisecond, 100)
	fmt.Fprintf(w, "ticker drift over %-4d ticks of 20ms   %v total (%v per tick)\n", 100, t.total, t.perTick)
	fmt.Fprintf(w, "  accumulated error of a long-running ticker. This is probe interval accuracy.\n")
	verdict(w, absDur(t.perTick) <= 2*time.Millisecond,
		"probe intervals will hold over days",
		"DRIFTING — probe timestamps will wander from wall clock")

	fmt.Fprintln(w)
	storageCheck(w)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Read the verdicts, not just the numbers. Two PASS lines on clock resolution\n")
	fmt.Fprintf(w, "and sleep overshoot are what the RTT distribution depends on, and the\n")
	fmt.Fprintf(w, "storage line is whether this build can record anything at all.\n")
}

// storageCheck opens a database, writes, reads back and deletes it.
//
// This exists because a build can compile, link, start, serve its first page and
// still be incapable of storing a single measurement: the SQLite driver is
// selected at build time, and a binary built against the wrong one fails only
// when something first touches the database — which, on Windows, is during
// service registration, where it surfaces as "the service could not be
// registered (exit 1)" and says nothing about a database at all.
//
// `go build` succeeding proves the program compiles. It does not prove the
// program works. This is the cheapest check that tells those apart, it ships
// inside the binary, and it is one command for anyone holding a copy they are
// unsure about.
func storageCheck(w io.Writer) {
	dir, err := os.MkdirTemp("", "pingping-selftest-")
	if err != nil {
		fmt.Fprintf(w, "storage              cannot create a temporary directory: %v\n", err)
		verdict(w, false, "", "CANNOT TEST — no writable temp directory")
		return
	}
	defer os.RemoveAll(dir)

	start := time.Now()
	st, err := NewStore(dir, nil)
	if err != nil {
		fmt.Fprintf(w, "storage              %v\n", err)
		verdict(w, false, "",
			"THIS BUILD CANNOT STORE DATA — the SQLite driver is not working. "+
				"Do not use this binary; get one from the project's releases.")
		return
	}
	defer st.Close()

	// A round trip through the same table the console uses, so this exercises the
	// driver rather than merely opening a file.
	const probe = "selftest"
	if err := st.SetSetting(probe, "ok"); err != nil {
		fmt.Fprintf(w, "storage              write failed: %v\n", err)
		verdict(w, false, "", "THIS BUILD CANNOT STORE DATA — writes fail")
		return
	}
	got, ok := st.Setting(probe)
	fmt.Fprintf(w, "storage              opened, wrote and read back in %v\n", time.Since(start).Round(time.Millisecond))
	fmt.Fprintf(w, "  whether this binary can record measurements at all.\n")
	verdict(w, ok && got == "ok",
		"the database works; this build can record measurements",
		"THIS BUILD CANNOT STORE DATA — the value did not read back")
}

func verdict(w io.Writer, ok bool, yes, no string) {
	if ok {
		fmt.Fprintf(w, "  PASS  %s\n", yes)
		return
	}
	fmt.Fprintf(w, "  FAIL  %s\n", no)
}

// clockResolution finds the smallest observable tick by sampling until the value
// changes, repeatedly, and keeping the smallest delta seen.
func clockResolution() time.Duration {
	best := time.Hour
	for i := 0; i < 20; i++ {
		t0 := time.Now()
		for {
			d := time.Since(t0)
			if d > 0 {
				if d < best {
					best = d
				}
				break
			}
		}
	}
	return best
}

type dist struct{ p50, p90, p99, max time.Duration }

// sleepOvershoot measures how much later than requested a sleep actually returns.
func sleepOvershoot(d time.Duration, n int) dist {
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		time.Sleep(d)
		if over := time.Since(t0) - d; over > 0 {
			out = append(out, over)
		} else {
			out = append(out, 0)
		}
	}
	return summarize(out)
}

type drift struct{ total, perTick time.Duration }

// tickerDrift measures accumulated error across a run of ticks — the thing that
// decides whether a 15-second pace is still 15 seconds after a week.
func tickerDrift(period time.Duration, ticks int) drift {
	tk := time.NewTicker(period)
	defer tk.Stop()
	t0 := time.Now()
	for i := 0; i < ticks; i++ {
		<-tk.C
	}
	total := time.Since(t0) - period*time.Duration(ticks)
	return drift{total: total, perTick: total / time.Duration(ticks)}
}

func summarize(v []time.Duration) dist {
	if len(v) == 0 {
		return dist{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	at := func(p float64) time.Duration { return v[int(float64(len(v)-1)*p)] }
	return dist{p50: at(.5), p90: at(.9), p99: at(.99), max: v[len(v)-1]}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
