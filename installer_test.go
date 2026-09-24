package main

import (
	"regexp"
	"strings"
	"testing"
)

// The installer is a 32-bit NSIS executable and the program it installs is
// 64-bit, so the two halves do not see the same registry unless one of them says
// which view it means. Windows redirects a 32-bit process writing to
// HKLM\SOFTWARE\pingping into HKLM\SOFTWARE\WOW6432Node\pingping, and both
// appear as `SOFTWARE\pingping` in every screenshot, every error message and
// every `reg query` that does not pass /reg:32 or /reg:64.
//
// The consequence was two keys of the same name: the installer put InstallDir in
// one, `pingping.exe install` put Port and DataDir in the other, and each half
// read a key the other had never written. Nothing errored, because a missing
// value is indistinguishable from a value that was never set.
//
// So the invariant is SetRegView 64 before any HKLM write, and it is checked
// here rather than left to a comment, because the failure is invisible on the
// machine where it happens.
func TestInstallerPinsTheRegistryView(t *testing.T) {
	src := readSource(t, "packaging/pingping.nsi")

	for _, sec := range []string{`Section "pingping" SecMain`, `Section "Uninstall"`} {
		i := strings.Index(src, sec)
		if i < 0 {
			t.Fatalf("%s is gone from the installer", sec)
		}
		body := src[i:]
		if end := strings.Index(body, "\nSectionEnd"); end > 0 {
			body = body[:end]
		}

		view := strings.Index(body, "SetRegView 64")
		if view < 0 {
			t.Errorf("%s never pins the registry view, so a 32-bit installer and a "+
				"64-bit program write two keys of the same name", sec)
			continue
		}
		// Every HKLM touch must come after it. A SetRegView 32 block in between is
		// allowed — it is how leftovers from before this fix are cleaned up — as
		// long as the view is put back, which the last-index check below covers.
		for _, m := range regexp.MustCompile(`(?m)^\s*(Write|Delete)Reg\w*\s+HKLM`).FindAllStringIndex(body, -1) {
			if m[0] < view {
				t.Errorf("%s touches HKLM at offset %d, before SetRegView 64 at %d: "+
					"that write lands in whichever view Windows chooses", sec, m[0], view)
			}
		}
		// A SetRegView 32 must never be the last one standing.
		if last32, last64 := strings.LastIndex(body, "SetRegView 32"), strings.LastIndex(body, "SetRegView 64"); last32 > last64 {
			t.Errorf("%s leaves the registry view set to 32 for everything after "+
				"offset %d", sec, last32)
		}
	}
}

// Add/Remove Programs shows an entry only if the entry is complete. A missing
// DisplayName or UninstallString is not an error anywhere — the program simply
// is not in the list, which reads as "it did not install" and sends everyone to
// look at the wrong thing.
//
// The list was in fact empty for several releases, and the reason was neither of
// those: the install section aborted at the file copy, before it got this far, so
// a failed install left no trace of itself to uninstall. That is fixed upstream
// of here by stopping the service before overwriting its executable. This test
// guards the other half — that when the section does run to completion, what it
// wrote is enough to be listed.
func TestUninstallEntryIsComplete(t *testing.T) {
	src := readSource(t, "packaging/pingping.nsi")

	i := strings.Index(src, `Section "pingping" SecMain`)
	if i < 0 {
		t.Fatal("the main section is gone")
	}
	body := src[i:]
	if end := strings.Index(body, "\nSectionEnd"); end > 0 {
		body = body[:end]
	}

	for _, want := range []string{"DisplayName", "DisplayVersion", "UninstallString", "DisplayIcon", "Publisher"} {
		if !strings.Contains(body, `"`+want+`"`) {
			t.Errorf("the uninstall entry has no %s; Windows hides an incomplete entry", want)
		}
	}
	if !strings.Contains(body, "WriteUninstaller") {
		t.Error("no uninstaller is written, so UninstallString points at nothing")
	}

	// These are the values that hide an entry, or move it out of the list, and not
	// one of them should ever appear here.
	for _, hides := range []string{"SystemComponent", "ParentKeyName", "ParentDisplayName", "ReleaseType", "WindowsInstaller"} {
		if strings.Contains(body, `"`+hides+`"`) {
			t.Errorf("the uninstall entry sets %s, which takes it out of the "+
				"Add/Remove Programs list", hides)
		}
	}
}

// Overwriting a running executable is refused by Windows, and NSIS answers that
// refusal with a retry/abort dialog. Abort stops the section — which is upstream
// of the uninstall entry, the shortcuts and the service registration, so the
// install leaves a half-written directory and nothing in Add/Remove Programs.
//
// The service and every notification-area process hold the exe, so both have to
// be let go of before the copy. This checks the order, because the whole point is
// that the stop comes first.
func TestInstallerReleasesTheExeBeforeOverwritingIt(t *testing.T) {
	src := readSource(t, "packaging/pingping.nsi")

	i := strings.Index(src, `Section "pingping" SecMain`)
	body := src[i:]
	if end := strings.Index(body, "\nSectionEnd"); end > 0 {
		body = body[:end]
	}

	copyAt := strings.Index(body, "File /oname=pingping.exe")
	if copyAt < 0 {
		t.Fatal("the section no longer copies pingping.exe")
	}
	stopAt := strings.Index(body, "sc stop pingping")
	killAt := strings.Index(body, "taskkill /F /IM pingping.exe")

	if stopAt < 0 || stopAt > copyAt {
		t.Error("the service is not stopped before the executable is overwritten: " +
			"the copy is refused, NSIS offers Abort, and Abort skips everything below")
	}
	if killAt < 0 || killAt > copyAt {
		t.Error("the tray processes are not closed before the executable is " +
			"overwritten; they hold the same file the service does")
	}
	if stopAt > killAt {
		t.Error("taskkill runs before sc stop, so the service process is killed " +
			"rather than stopped — the SCM reads that as a crash and applies the " +
			"recovery action, which brings the service back mid-install")
	}
}
