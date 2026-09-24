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
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
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

	// Opening a console must not demand administrator rights. The first version
	// of this verb went straight to `net start` whenever the port did not answer,
	// which put a UAC prompt in front of someone who had asked for nothing more
	// than a web page — and an elevation prompt with no explanation attached is
	// exactly what teaches people not to trust a program.
	//
	// So: ask the console first, because that costs nothing and answers the
	// common case. Only if it is silent, ask the SCM whether the service is
	// actually stopped — a query any authenticated user may make — and only if
	// it is, ask the person before elevating. A service that claims to be
	// running while nothing answers is a different fault, and restarting it is
	// not this command's decision to make.
	client := &http.Client{Timeout: 3 * time.Second}
	if !answers(client, base) {
		if stopped, known := serviceIsStopped(); known && stopped {
			if askToStartService() {
				runElevated("cmd.exe", "/c net start "+svcName)
			}
		} else {
			log.Printf("nothing is answering on %s, and the %s service does not "+
				"report itself stopped", base, svcName)
		}
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

// serviceIsStopped reports whether the service is registered and not running.
//
// It opens the SCM with SC_MANAGER_CONNECT and the service with
// SERVICE_QUERY_STATUS rather than going through mgr.Connect, which asks for
// SC_MANAGER_ALL_ACCESS and therefore fails without elevation. Asking a
// question in order to decide whether elevation is needed must not itself need
// elevation.
//
// known is false when the question could not be answered at all — no service,
// no access — and the caller then does nothing rather than guessing.
func serviceIsStopped() (stopped, known bool) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, false
	}
	defer windows.CloseServiceHandle(scm)

	name, err := windows.UTF16PtrFromString(svcName)
	if err != nil {
		return false, false
	}
	h, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, false
	}
	s := &mgr.Service{Name: svcName, Handle: h}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return false, false
	}
	// StartPending counts as running: it is on its way up and starting it again
	// would only produce an error dialog.
	return st.State != svc.Running && st.State != svc.StartPending, true
}

// askToStartService puts the elevation in context before Windows asks for it.
// The UAC prompt says nothing about why, so this says it first.
func askToStartService() bool {
	m := messages(preferredLang())
	const mbYesNo, mbIconQuestion, idYes = 0x4, 0x20, 6
	r, _, _ := procMessageBox.Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(m.StartPrompt))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(m.StartPromptTitle))),
		mbYesNo|mbIconQuestion)
	return r == idYes
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
