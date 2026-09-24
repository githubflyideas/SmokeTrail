package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// The Start Menu shortcut named "pingping console" ran pingping.exe with no
// arguments, and on an installed machine a bare launch means "serve in the
// foreground" — a second copy of the whole program, binding the port the service
// is already listening on. It fails on that bind and log.Fatal exits before
// anyone can read the message, so what the operator sees is a console window
// that flashes and vanishes. From the outside that is indistinguishable from a
// program that is simply broken, and it was the only visible way back in after
// "Stop pingping and exit the tray" had taken away both the service and the icon.
//
// Double-clicking something called "console" means "show me the console". So
// that is what it now does: put back whatever is missing — the service, the icon
// — and open the browser. Foreground serving is still there under `pingping run`,
// which is where someone who wants it will look.

// openConsoleForInstalled brings the console back up and opens it. The bool
// reports whether this is an installed copy at all; false means the caller
// should go on and serve in the foreground, which is what a portable copy in a
// folder is for.
func openConsoleForInstalled(opt options) (int, bool) {
	port := opt.port
	if port == 0 {
		port = installedPort()
	}
	if port == 0 {
		return 0, false // no service registered here — this is a portable copy
	}
	base := fmt.Sprintf("http://localhost:%d", port)

	// Ask the console itself rather than the SCM. It is the same question the
	// tray icon asks, it needs no privilege, and "the service says it is running
	// but nothing answers on the port" is a state that should send us down the
	// restart path too.
	client := &http.Client{Timeout: 2 * time.Second}
	if !answers(client, base) {
		log.Printf("the %s service is not answering on %s — starting it", svcName, base)
		runElevated("cmd.exe", "/c net start "+svcName)
	}

	// And the icon. Spawning this unconditionally is safe: the tray claims a
	// named mutex at startup and a second copy exits immediately, so this is
	// either the way back from an exited icon or a no-op.
	startTrayDetached()

	// Give the service a moment, so the browser does not open on a connection
	// error and teach the operator that the shortcut does not work. If it never
	// comes up, open anyway — an error page in the browser at least says which
	// address was tried, which the flashing window never did.
	for i := 0; i < 30 && !answers(client, base); i++ {
		time.Sleep(time.Second)
	}
	openInBrowser(stamped(base))
	return 0, true
}

func answers(c *http.Client, base string) bool {
	resp, err := c.Get(base + "/api/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// startTrayDetached launches the notification-area process without a console
// window and without waiting for it. CREATE_NO_WINDOW rather than SW_HIDE,
// because the tray is a console-subsystem binary and a hidden window still
// flashes on creation.
func startTrayDetached() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "tray")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	if err := cmd.Start(); err != nil {
		log.Printf("starting the tray: %v", err)
		return
	}
	// Not waited on deliberately: this process is about to exit and the tray
	// outlives it. Release the handle so it is not left in the table.
	go func() { _ = cmd.Wait() }()
}

func openInBrowser(target string) { shellOpen(target) }

// stamped attaches the build version to a console URL.
//
// Until 0.5.5 the console served its pages with no Cache-Control, no ETag and no
// Last-Modified, and a browser given no validator is free to invent a lifetime
// and reuse the page without asking. Those already-cached copies do not learn
// about the new headers — a stored response is reused under the rules it was
// stored with — so an upgrade from 0.5.4 or earlier can still show the old page
// until that invented lifetime runs out.
//
// A URL the browser has never seen has no stored response to reuse, and the
// version changes on every release, so this is self-clearing: after one launch
// on a new version the browser holds a page that carries a validator, and every
// upgrade after that is handled by revalidation alone.
func stamped(u string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "v=" + url.QueryEscape(version)
}
