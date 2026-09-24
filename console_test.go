package main

import (
	"strings"
	"testing"
)

// Opening a console must not demand administrator rights.
//
// The first version of the console verb ran `net start` whenever the port did
// not answer, which put a UAC prompt in front of someone who had asked for
// nothing more than a web page — and an unexplained elevation prompt is what
// teaches people not to trust a program. It also fired in cases where the
// service was fine and merely slow.
//
// The rule: exactly one elevation in this file, and it happens only after the
// person has been asked. Counting is the check, because the failure is a dialog
// nobody can see in a test.
func TestConsoleNeverElevatesWithoutAsking(t *testing.T) {
	src := readSource(t, "console_windows.go")

	if n := strings.Count(src, "runElevated("); n != 1 {
		t.Fatalf("found %d elevation calls, want exactly 1 — every one of them "+
			"puts a UAC prompt in front of someone opening a web page", n)
	}
	ask := strings.Index(src, "if askToStartService() {")
	if ask < 0 {
		t.Fatal("the elevation is no longer gated on asking the person first")
	}
	if strings.Index(src, "runElevated(") < ask {
		t.Error("runElevated is called before askToStartService: the prompt " +
			"appears whether or not the person agreed to it")
	}

	// And the question that decides whether to elevate must not itself need
	// elevation, or the check can never succeed on the machine it is for.
	q := src[strings.Index(src, "func serviceIsStopped()"):]
	if end := strings.Index(q, "\nfunc "); end > 0 {
		q = q[:end]
	}
	if strings.Contains(q, "mgr.Connect()") {
		t.Error("serviceIsStopped uses mgr.Connect, which asks for " +
			"SC_MANAGER_ALL_ACCESS and fails without elevation")
	}
	for _, want := range []string{"SC_MANAGER_CONNECT", "SERVICE_QUERY_STATUS"} {
		if !strings.Contains(q, want) {
			t.Errorf("serviceIsStopped does not open with %s; it needs the "+
				"least-privileged access that answers the question", want)
		}
	}
}
