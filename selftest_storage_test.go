package main

import (
	"strings"
	"testing"
)

// The storage verdict is what CI greps for before it publishes anything, so the
// wording is a contract, not prose: if this line stops saying CANNOT STORE DATA
// on a broken driver, the release gate silently stops gating.
func TestStorageCheckReportsAVerdict(t *testing.T) {
	var b strings.Builder
	storageCheck(&b)
	out := b.String()

	if !strings.Contains(out, "storage") {
		t.Fatalf("no storage line in the output:\n%s", out)
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("the driver in this build works, so the check must pass:\n%s", out)
	}
	if strings.Contains(out, "CANNOT STORE DATA") {
		t.Fatalf("reported a failure against a working driver:\n%s", out)
	}
}

// CI greps for this exact string. Pin it here so a reword has to be deliberate.
func TestStorageFailureStringIsTheOneCIGrepsFor(t *testing.T) {
	src := readSource(t, "selftest.go")
	if !strings.Contains(src, "CANNOT STORE DATA") {
		t.Fatal("selftest.go no longer emits CANNOT STORE DATA; the release " +
			"smoke test in .github/workflows/ greps for it and would stop gating")
	}
	for _, wf := range []string{".github/workflows/build.yml", ".github/workflows/release.yml"} {
		if !strings.Contains(readSource(t, wf), "CANNOT STORE DATA") {
			t.Errorf("%s no longer checks the storage verdict", wf)
		}
	}
}
