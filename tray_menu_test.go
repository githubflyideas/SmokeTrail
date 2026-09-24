package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The notification-area menu is Win32 and cannot be clicked from a test, so this
// checks the one thing that went wrong without anyone noticing: an item was drawn
// in the menu and the dispatcher had no case for it. Clicking "Exit pingping" did
// nothing at all, in the one mode where it was offered, for every release so far.
//
// Nothing here compiles tray_windows.go — the file is Windows-only and this test
// reads it as text — so it runs on every platform and on every CI job, which is
// the point. A menu you cannot click from a test can still be read.

var (
	reMenuAdd  = regexp.MustCompile(`add\(h,\s*mf\w+,\s*(id\w+)\s*,`)
	reMenuCase = regexp.MustCompile(`case\s+(id\w+)\s*:`)
)

func trayMenuSource(t *testing.T) string { return readSource(t, "tray_windows.go") }

// readSource reads a file from the package directory. Several tests here check
// things the compiler cannot: that a menu item has a handler, that a workflow
// still greps for the string a check emits.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

func TestEveryTrayMenuItemIsHandled(t *testing.T) {
	src := trayMenuSource(t)

	handled := map[string]bool{}
	for _, m := range reMenuCase.FindAllStringSubmatch(src, -1) {
		handled[m[1]] = true
	}

	drawn := map[string]bool{}
	for _, m := range reMenuAdd.FindAllStringSubmatch(src, -1) {
		drawn[m[1]] = true
	}
	if len(drawn) < 5 {
		t.Fatalf("only found %d menu items; the pattern that finds them has drifted "+
			"from the code and this test is no longer checking anything", len(drawn))
	}

	for id := range drawn {
		// A separator carries no command, and the language block is dispatched
		// by range rather than by a case, because langOrder decides its size.
		if id == "idLangBase" || id == "idNone" {
			continue
		}
		if !handled[id] {
			t.Errorf("%s is drawn in the menu but command() has no case for it: "+
				"clicking it does nothing", id)
		}
	}
}

// Whatever else changes about the menu, there has to be a way out of it. An icon
// with no exit is at its worst precisely when the service is down and the icon is
// useless — which is when someone most wants it gone.
func TestTrayAlwaysOffersAWayOut(t *testing.T) {
	src := trayMenuSource(t)

	menu := src[strings.Index(src, "func (t *tray) showMenu()"):]
	if end := strings.Index(menu, "\nfunc "); end > 0 {
		menu = menu[:end]
	}
	if !strings.Contains(menu, "idExit") {
		t.Fatal("showMenu draws no exit item")
	}

	// It must not be behind the service check. That is how it went missing: the
	// item existed, and an installed copy never saw it.
	for _, line := range strings.Split(menu, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "if !t.service") || strings.HasPrefix(s, "if t.service {") {
			rest := menu[strings.Index(menu, line):]
			block := rest
			if end := strings.Index(rest, "\n\t}"); end > 0 {
				block = rest[:end]
			}
			// A mode-dependent LABEL is fine; a mode-dependent presence is not.
			if strings.Contains(block, "idExit") && !strings.Contains(block, "else") {
				t.Fatal("the exit item is inside a one-sided service check, so one " +
					"mode gets a menu with no way out")
			}
		}
	}
}

// Two menu items sharing a command id means clicking one silently performs the
// other. The compiler only notices when both have a case in the same switch —
// idQuitAll was given 1009, which idSettings already held, and that collision
// was caught by a duplicate-case error rather than by anything checking ids.
func TestTrayMenuIdsAreUnique(t *testing.T) {
	src := trayMenuSource(t)

	block := src[strings.Index(src, "idOpenConsole"):]
	if end := strings.Index(block, "idLangBase"); end > 0 {
		block = block[:end]
	}

	re := regexp.MustCompile(`(id\w+)\s*=\s*(\d+)`)
	seen := map[string]string{}
	found := 0
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		name, val := m[1], m[2]
		found++
		if prev, dup := seen[val]; dup {
			t.Errorf("%s and %s are both %s: one of them silently runs the other", prev, name, val)
		}
		seen[val] = name
	}
	if found < 5 {
		t.Fatalf("only found %d menu ids; the pattern has drifted from the code", found)
	}
}
