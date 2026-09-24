//go:build windows

package main

import (
	"context"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The tray icon, written against Shell_NotifyIcon directly rather than pulling in
// a tray library — the only one worth using drags in a D-Bus client for Linux that
// this build has no use for, and the Win32 surface needed here is small.
//
// It exists for two reasons, and neither is decoration:
//
//   - Discoverability. A service has no window. Without a tray icon the answer to
//     "I installed it, now what?" is "open a browser and type a port number you
//     have to remember", which is not an answer.
//   - State at a glance. The icon turns red when a target is down. For a link
//     monitor that is the one piece of information worth a permanent pixel.
//
// The hard constraint is Session 0 isolation: since Vista a Windows service runs
// in a session with no desktop and cannot display UI at all. So in service mode
// the tray is a SEPARATE PROCESS — `pingping.exe tray` — in the logged-in user's
// session, talking to the service over the same local HTTP API a browser uses. The
// single-binary promise holds; the process model does not.
//
// That split is also why the menu differs by mode. In portable mode "Exit" stops
// everything, because the tray process is the program. In service mode it must
// not: a user who thinks they closed the program while the service keeps probing
// is the single most predictable support complaint, so the two actions are spelled
// out separately and neither is called "Exit".

//go:embed packaging/icon/tray-ok.ico packaging/icon/tray-alert.ico
var trayIcons embed.FS

const (
	wmTrayIcon = 0x0400 + 1 // WM_APP + 1

	idOpenConsole  = 1001
	idStopService  = 1003
	idStartService = 1004
	idRestart      = 1005
	idDataFolder   = 1006
	idExit         = 1008
	idSettings     = 1009
	idQuitAll      = 1010
	idUpdates      = 1012
	idAbout        = 1013

	// The language submenu numbers itself from here, one per entry in langOrder,
	// with the automatic choice first.
	idLangBase = 1100

	trayPollInterval = 10 * time.Second
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessage         = user32.NewProc("PostMessageW")
	procCreateIconFromResEx = user32.NewProc("CreateIconFromResourceEx")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	procCreateMutex         = kernel32.NewProc("CreateMutexW")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	procFreeConsole         = kernel32.NewProc("FreeConsole")
	procGetConsoleWindow    = kernel32.NewProc("GetConsoleWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procMessageBox          = user32.NewProc("MessageBoxW")
	procRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
)

// taskbarCreated is broadcast by the shell when the notification area comes into
// existence — at sign-in, and again every time Explorer restarts. Re-adding the
// icon on it is not optional: without it, one Explorer crash removes the icon
// permanently, and a Startup entry that races the shell never gets one at all.
var taskbarCreated uint32

// detachConsole gets rid of the console window a console-subsystem binary is
// given when Explorer launches it. The tray has nothing to print and a black
// window that appears at every sign-in is worse than no icon at all.
//
// The alternative is to build the whole program for the GUI subsystem and call
// AttachConsole(ATTACH_PARENT_PROCESS) for the command-line verbs. That removes
// even the brief flash, at the cost of `pingping selftest` returning to the
// prompt before it finishes printing — a well-known quirk of that approach. For
// an operations tool the command line is worth more than the last few
// milliseconds of flicker, so: console subsystem, hidden here.
func detachConsole() {
	if h, _, _ := procGetConsoleWindow.Call(); h != 0 {
		procShowWindow.Call(h, 0 /* SW_HIDE */)
	}
	procFreeConsole.Call()
}

type point struct{ X, Y int32 }

type msg struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// notifyIconData is the ANSI/Unicode NOTIFYICONDATAW. Only the fields up to
// szInfoTitle are used; the struct is declared in full so its size matches what
// the shell expects for the version we report.
type notifyIconData struct {
	Size             uint32
	Wnd              windows.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	VersionOrTimeout uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         windows.GUID
	BalloonIcon      windows.Handle
}

const (
	nimAdd    = 0x0
	nimModify = 0x1
	nimDelete = 0x2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	nifInfo    = 0x10 // the balloon fields carry a notification

	niifWarning = 0x2
	niifInfo    = 0x1
)

const homepage = "https://github.com/githubflyideas/pingping"

// tray is one running tray icon.
type tray struct {
	hwnd     windows.Handle
	iconOK   windows.Handle
	iconBad  windows.Handle
	service  bool // true when a separate service owns the probing
	consoleU string
	cur      windows.Handle
	tip      string
	onExit   func()

	mu     sync.Mutex
	status health // what the last poll saw, shown in the menu
	m      msgs   // interface strings for the language in force
}

// setLang re-reads the catalogue and refreshes anything already on screen.
func (t *tray) setLang(tag string) {
	storeLang(tag)
	t.mu.Lock()
	t.m = messages(preferredLang())
	st := t.status
	t.mu.Unlock()
	t.tip = "" // force the tooltip to be rewritten in the new language
	t.setState(!st.reachable || st.Down > 0, "pingping - "+t.line(st))
}

// health is the console's own view, fetched from the loopback-only endpoint. The
// tray has no session, so counts are all it can have — and all it needs.
type health struct {
	Version    string `json:"version"`
	NeedsSetup bool   `json:"needs_setup"`
	Targets    int    `json:"targets"`
	Down       int    `json:"down"`
	Port       int    `json:"port"`
	Configured int    `json:"port_configured"`
	reachable  bool
}

// line describes the current state in the language in force.
func (t *tray) line(h health) string {
	m := t.msgsNow()
	switch {
	case !h.reachable:
		return m.NotResponding
	case h.NeedsSetup:
		return m.NeedsSetup
	case h.Targets == 0:
		return m.NoTargets
	case h.Down == 0:
		return fmt.Sprintf(m.AllUpFmt, h.Targets)
	default:
		return fmt.Sprintf(m.SomeDownFmt, h.Targets, h.Down)
	}
}

func (t *tray) msgsNow() msgs {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m
}

// runTray owns the calling goroutine for the lifetime of the icon: a Win32 message
// loop must run on the thread that created the window, and Go will happily move a
// goroutine between threads otherwise.
func runTray(ctx context.Context, consoleURL string, service bool, onExit func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	t := &tray{service: service, consoleU: consoleURL, onExit: onExit,
		m: messages(preferredLang())}
	var err error
	if t.iconOK, err = loadEmbeddedIcon("packaging/icon/tray-ok.ico"); err != nil {
		return err
	}
	if t.iconBad, err = loadEmbeddedIcon("packaging/icon/tray-alert.ico"); err != nil {
		return err
	}
	if err := t.createWindow(); err != nil {
		return err
	}
	defer t.remove()

	if m, _, _ := procRegisterWindowMsg.Call(
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("TaskbarCreated")))); m != 0 {
		taskbarCreated = uint32(m)
	}

	t.cur = t.iconOK
	t.tip = "pingping - " + t.msgsNow().Starting
	// A Startup entry runs while Explorer is still coming up, so the first add
	// routinely fails. Keep trying rather than exiting: an icon that appears a few
	// seconds late is the difference between working and "it never shows up".
	if err := t.notify(nimAdd); err != nil {
		log.Printf("tray: the notification area is not ready yet (%v) — retrying", err)
		go t.retryAdd(ctx)
	}

	go t.watch(ctx)
	go func() {
		<-ctx.Done()
		procPostMessage.Call(uintptr(t.hwnd), 0x0012 /* WM_QUIT */, 0, 0)
	}()

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (t *tray) createWindow() error {
	inst, _, _ := procGetModuleHandle.Call(0)
	cls := windows.StringToUTF16Ptr("pingpingTray")
	cursor, _, _ := procLoadCursor.Call(0, 32512 /* IDC_ARROW */)

	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   windows.NewCallback(t.wndProc),
		Instance:  windows.Handle(inst),
		Cursor:    windows.Handle(cursor),
		ClassName: cls,
	}
	if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %w", err)
	}
	// HWND_MESSAGE (-3) gives a message-only window: no taskbar button, no desktop
	// presence, just something with a window procedure to receive the callback.
	h, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("pingping"))),
		0, 0, 0, 0, 0, uintptr(^uintptr(2)), 0, inst, 0)
	if h == 0 {
		return fmt.Errorf("CreateWindowEx: %w", err)
	}
	t.hwnd = windows.Handle(h)
	return nil
}

func (t *tray) data() *notifyIconData {
	d := &notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Wnd:             t.hwnd,
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayIcon,
		Icon:            t.cur,
	}
	copyUTF16(d.Tip[:], t.tip)
	return d
}

func (t *tray) notify(action uintptr) error {
	r, _, err := procShellNotifyIcon.Call(action, uintptr(unsafe.Pointer(t.data())))
	if r == 0 {
		return fmt.Errorf("Shell_NotifyIcon(%d): %w", action, err)
	}
	return nil
}

// balloon raises a Windows notification. This is what a monitoring tray is
// actually for: nobody watches a 16-pixel icon, but everybody notices a
// notification the moment a link goes bad.
func (t *tray) balloon(title, text string, warning bool) {
	d := t.data()
	d.Flags |= nifInfo
	d.InfoFlags = niifInfo
	if warning {
		d.InfoFlags = niifWarning
	}
	copyUTF16(d.InfoTitle[:], title)
	copyUTF16(d.Info[:], text)
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(d)))
}

func (t *tray) remove() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(t.data())))
	if t.hwnd != 0 {
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
}

// retryAdd keeps offering the icon to a shell that was not listening yet.
func (t *tray) retryAdd(ctx context.Context) {
	for i := 0; i < 150; i++ { // ~5 minutes, then leave it to TaskbarCreated
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		if t.notify(nimAdd) == nil {
			log.Printf("tray: icon added")
			return
		}
	}
}

func (t *tray) setState(bad bool, tip string) {
	icon := t.iconOK
	if bad {
		icon = t.iconBad
	}
	if icon == t.cur && tip == t.tip {
		return
	}
	t.cur, t.tip = icon, tip
	if t.notify(nimModify) != nil {
		// Modify fails when the icon is not registered — which is the state a
		// shell that was not ready leaves us in. Treat it as a cue to add.
		t.notify(nimAdd)
	}
}

func (t *tray) wndProc(hwnd windows.Handle, m uint32, wparam, lparam uintptr) uintptr {
	if taskbarCreated != 0 && m == taskbarCreated {
		// The shell restarted. Our icon went with it; put it back.
		t.notify(nimAdd)
		return 0
	}
	switch m {
	case wmTrayIcon:
		switch uint32(lparam) {
		case 0x0203: // WM_LBUTTONDBLCLK
			t.openConsole()
		case 0x0205: // WM_RBUTTONUP
			t.showMenu()
		}
		return 0
	case 0x0111: // WM_COMMAND
		t.command(uint32(wparam & 0xffff))
		return 0
	case 0x0002: // WM_DESTROY
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(m), wparam, lparam)
	return r
}

func (t *tray) showMenu() {
	h, _, _ := procCreatePopupMenu.Call()
	if h == 0 {
		return
	}
	defer procDestroyMenu.Call(h)

	const (
		mfString    = 0x0
		mfGrayed    = 0x1
		mfDisabled  = 0x2
		mfChecked   = 0x8
		mfPopup     = 0x10
		mfSeparator = 0x800
		mfDefault   = 0x1000
	)
	add := func(menu uintptr, flags, id uintptr, text string) {
		procAppendMenu.Call(menu, flags, id,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}

	t.mu.Lock()
	st, m := t.status, t.m
	t.mu.Unlock()

	// A status line at the top, greyed and inert. Windows tray menus commonly
	// lead with one, and here it answers the only question most people have
	// without them opening a browser.
	add(h, mfString|mfGrayed|mfDisabled, 0, "pingping - "+t.line(st))
	add(h, mfSeparator, 0, "")
	add(h, mfString|mfDefault, idOpenConsole, m.OpenConsole)
	add(h, mfString, idSettings, m.SettingsItem)
	add(h, mfString, idDataFolder, m.DataFolder)

	if t.service {
		add(h, mfSeparator, 0, "")
		if st.reachable {
			// Restart sits above stop because it is the common case: a port
			// change in the console needs exactly this.
			add(h, mfString, idRestart, m.RestartService)
			add(h, mfString, idStopService, m.StopService)
		} else {
			add(h, mfString, idStartService, m.StartService)
		}
	}

	// Language. Following Windows is the default, but a server installed in a
	// language nobody on the team reads is common enough to need a way out.
	if sub, _, _ := procCreatePopupMenu.Call(); sub != 0 {
		cur := storedLang()
		mark := func(tag string) uintptr {
			if cur == tag {
				return mfChecked
			}
			return 0
		}
		add(sub, mfString|mark(langAuto), idLangBase, m.LangAuto)
		add(sub, mfSeparator, 0, "")
		for i, tag := range langOrder() {
			add(sub, mfString|mark(tag), uintptr(idLangBase+1+i), langName(tag))
		}
		add(h, mfSeparator, 0, "")
		add(h, mfPopup, sub, m.LanguageItem)
	}

	add(h, mfSeparator, 0, "")
	add(h, mfString, idUpdates, m.CheckUpdates)
	add(h, mfString, idAbout, m.AboutItem)
	// Both modes get a way out. Hiding it under a service was meant to avoid a
	// user believing they had stopped the monitoring — but an icon that cannot be
	// closed is a worse complaint than a mislabelled one, and it is worst exactly
	// when the service is down and the icon is useless. The label carries the
	// honesty instead: under a service this closes the icon and says so.
	add(h, mfSeparator, 0, "")
	if t.service {
		// Two different intentions, spelled out rather than guessed at. Closing
		// the icon and stopping the monitoring are not the same act, and an icon
		// that offers only the first leaves no way to answer "make it stop".
		add(h, mfString, idExit, m.CloseIconItem)
		add(h, mfString, idQuitAll, m.QuitAllItem)
	} else {
		add(h, mfString, idExit, m.ExitItem)
	}

	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	// Required by the shell, and the documented fix for a menu that will not
	// dismiss when the user clicks elsewhere.
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	const tpmRightButton = 0x2
	procTrackPopupMenu.Call(h, tpmRightButton, uintptr(p.X), uintptr(p.Y), 0,
		uintptr(t.hwnd), 0)
	procPostMessage.Call(uintptr(t.hwnd), 0x0000, 0, 0)
}

func (t *tray) command(id uint32) {
	// The language submenu is a contiguous block rather than named constants,
	// because langOrder decides how many entries there are.
	if order := langOrder(); id >= idLangBase && int(id) <= idLangBase+len(order) {
		tag := langAuto
		if id > idLangBase {
			tag = order[id-idLangBase-1]
		}
		t.setLang(tag)
		return
	}
	switch id {
	case idOpenConsole:
		t.openConsole()
	case idDataFolder:
		t.open(dataDirForDisplay())
	case idStartService:
		runElevated("cmd.exe", "/c net start "+svcName)
	case idStopService:
		runElevated("cmd.exe", "/c net stop "+svcName)
	case idRestart:
		// Re-point the firewall rule as well, because the reason to restart is
		// usually that the port or the bind address changed in the console. This
		// comment claimed that before the `firewall` verb existed to make it
		// true: the command was `net stop & net start`, which never touched the
		// firewall, while the console told the operator that restarting had
		// handled it.
		if exe, err := os.Executable(); err == nil {
			runElevated("cmd.exe", "/c net stop "+svcName+
				" & \""+exe+"\" firewall & net start "+svcName)
		} else {
			runElevated("cmd.exe", "/c net stop "+svcName+" & net start "+svcName)
		}
	case idSettings:
		t.open(stamped(t.consoleU + "/settings"))
	case idUpdates:
		t.open(homepage + "/releases")
	case idQuitAll:
		// Stopping a service needs elevation, so this asks Windows for consent
		// rather than failing with an access denied the user cannot act on. The
		// icon then waits for the console to actually stop answering before it
		// goes, so what the operator sees matches what happened — including
		// when they decline the prompt and nothing stops.
		runElevated("cmd.exe", "/c net stop "+svcName)
		go func() {
			client := &http.Client{Timeout: 2 * time.Second}
			deadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := client.Get(t.consoleU + "/api/health"); err != nil {
					break // it is down, or on its way
				}
				time.Sleep(time.Second)
			}
			t.quit()
		}()
	case idExit:
		// This had no case at all: the item was drawn in portable mode and did
		// nothing when clicked, and onExit was stored and never called.
		//
		// Portable mode: this process IS the program, so stop the probing too.
		// Service mode: only the icon goes, which is what its label promises.
		if t.onExit != nil && !t.service {
			t.onExit()
		}
		t.quit()
	case idAbout:
		t.mu.Lock()
		st, m := t.status, t.m
		t.mu.Unlock()
		v := st.Version
		if v == "" {
			v = version
		}
		body := "pingping " + v + "\n\n" + m.AboutTagline + "\n\n" +
			m.AboutConsole + ":  " + t.consoleU + "\n" +
			m.AboutStatus + ":  " + t.line(st) + "\n" +
			m.AboutData + ":  " + dataDirForDisplay() + "\n\n" +
			// The licence NAME is not translated — "Apache License 2.0" is what
			// the LICENSE file says and what a reader has to be able to match
			// against it. Only the label around it is. Without the name this
			// line read "License" and stopped.
			homepage + "\n\n" + m.AboutLicense + ":  " + licenceName
		// MessageBox blocks, and blocking here would freeze the message loop that
		// has to keep serving the icon.
		go procMessageBox.Call(0,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(body))),
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(m.AboutTitle))),
			0x40 /* MB_ICONINFORMATION */)
	}
}

// runElevated asks Windows for consent rather than inventing our own prompt, and
// rather than failing with an access-denied the user cannot act on. Controlling a
// service needs elevation; the tray itself deliberately does not have it.
func runElevated(exe, args string) {
	go func() {
		if err := windows.ShellExecute(0,
			windows.StringToUTF16Ptr("runas"),
			windows.StringToUTF16Ptr(exe),
			windows.StringToUTF16Ptr(args),
			nil, windows.SW_HIDE); err != nil && err != windows.ERROR_CANCELLED {
			log.Printf("elevated %s: %v", exe, err)
		}
	}()
}

func (t *tray) openConsole() { t.open(stamped(t.consoleU)) }

func (t *tray) open(target string) { shellOpen(target) }

// shellOpen hands a URL or a folder to whatever Windows has registered for it.
// Package-level because the console verb opens the same URLs without a tray.
func shellOpen(target string) {
	windows.ShellExecute(0, windows.StringToUTF16Ptr("open"),
		windows.StringToUTF16Ptr(target), nil, nil, windows.SW_SHOWNORMAL)
}

func dataDirForDisplay() string {
	if d := installedDataDir(); d != "" {
		return d
	}
	return systemDataDir()
}

// watch keeps the icon honest. It asks the console the same question a browser
// would, so the tray reports what the service actually believes rather than
// guessing from the outside.
func (t *tray) watch(ctx context.Context) {
	client := &http.Client{Timeout: 4 * time.Second}
	tick := time.NewTicker(trayPollInterval)
	defer tick.Stop()
	var prev health
	first := true
	for {
		st := fetchHealth(client, t.consoleU)
		if !st.reachable {
			// The port may have been changed in the console and the service
			// restarted onto it. Go and look before reporting a failure.
			if u, found := t.findConsole(client); found {
				t.consoleU = u
				rememberPort(portOfURL(u))
				st = fetchHealth(client, u)
			}
		}
		t.mu.Lock()
		t.status = st
		t.mu.Unlock()

		// Notify on transitions only. A balloon every poll would be noise, and
		// one at startup would fire on every sign-in.
		if !first {
			m := t.msgsNow()
			switch {
			case prev.reachable && !st.reachable:
				t.balloon(m.StoppedTitle, fmt.Sprintf(m.StoppedBodyFmt, t.consoleU), true)
			case !prev.reachable && st.reachable:
				t.balloon(m.BackTitle, t.line(st), false)
			case st.reachable && st.Down > prev.Down:
				t.balloon(m.DownTitle, fmt.Sprintf(m.DownBodyFmt, st.Down, st.Targets), true)
			case st.reachable && prev.Down > 0 && st.Down == 0:
				t.balloon(m.UpTitle, fmt.Sprintf(m.UpBodyFmt, st.Targets), false)
			}
		}
		prev, first = st, false
		// Red for anything an operator would want to act on: a target down, or a
		// service that has stopped answering.
		t.setState(!st.reachable || st.Down > 0, "pingping - "+t.line(st))
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// findConsole looks for the service on the ports it could plausibly be on: the
// one the console last told us it was moving to, the one recorded at install, the
// one that worked last time, and the default. Cheap, and it means a port change
// does not orphan the icon.
func (t *tray) findConsole(c *http.Client) (string, bool) {
	t.mu.Lock()
	pending := t.status.Configured
	t.mu.Unlock()

	seen := map[int]bool{}
	for _, p := range []int{pending, rememberedPort(), installedPort(), 8518} {
		if p < 1 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		u := fmt.Sprintf("http://localhost:%d", p)
		if u == t.consoleU {
			continue
		}
		if h := fetchHealth(c, u); h.reachable {
			log.Printf("tray: console found on port %d", p)
			return u, true
		}
	}
	return "", false
}

func portOfURL(u string) int {
	i := strings.LastIndex(u, ":")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(u[i+1:])
	return n
}

// The last port that worked, per user. HKCU because the tray runs unelevated and
// cannot write the machine-wide key the installer used.
func rememberPort(p int) {
	if p < 1 || p > 65535 {
		return
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `SOFTWARE\pingping`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetDWordValue("LastPort", uint32(p))
}

func rememberedPort() int {
	k, err := registry.OpenKey(registry.CURRENT_USER, `SOFTWARE\pingping`, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("LastPort")
	if err != nil {
		return 0
	}
	return int(v)
}

func fetchHealth(c *http.Client, base string) health {
	resp, err := c.Get(base + "/api/health")
	if err != nil {
		return health{}
	}
	defer resp.Body.Close()
	var h health
	if json.NewDecoder(resp.Body).Decode(&h) != nil {
		return health{}
	}
	h.reachable = true
	return h
}

// loadEmbeddedIcon turns an .ico file into an HICON. CreateIconFromResourceEx
// wants a single icon image, not the container, so the directory is walked for
// the largest entry at or below 32px — the only sizes the shell asks for here.
func loadEmbeddedIcon(name string) (windows.Handle, error) {
	b, err := trayIcons.ReadFile(name)
	if err != nil {
		return 0, err
	}
	if len(b) < 6 || binary.LittleEndian.Uint16(b[2:4]) != 1 {
		return 0, fmt.Errorf("%s: not an icon file", name)
	}
	count := int(binary.LittleEndian.Uint16(b[4:6]))
	bestOff, bestLen, bestW := 0, 0, -1
	for i := 0; i < count; i++ {
		e := 6 + i*16
		if e+16 > len(b) {
			break
		}
		w := int(b[e])
		if w == 0 {
			w = 256
		}
		size := int(binary.LittleEndian.Uint32(b[e+8 : e+12]))
		off := int(binary.LittleEndian.Uint32(b[e+12 : e+16]))
		if off+size > len(b) {
			continue
		}
		if w <= 32 && w > bestW {
			bestOff, bestLen, bestW = off, size, w
		}
	}
	if bestW < 0 {
		return 0, fmt.Errorf("%s: no image at 32px or below", name)
	}
	const version = 0x00030000
	h, _, err := procCreateIconFromResEx.Call(
		uintptr(unsafe.Pointer(&b[bestOff])), uintptr(bestLen),
		1 /* fIcon */, version, 0, 0, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateIconFromResourceEx(%s): %w", name, err)
	}
	return windows.Handle(h), nil
}

func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// startForegroundTray runs the icon alongside a portable/foreground instance, in
// the same process — there is no Session 0 boundary to cross here. Exiting from
// the menu stops the whole program, which in this mode is what the user means.
func startForegroundTray(consoleURL string, stop func()) {
	go func() {
		if err := runTray(context.Background(), consoleURL, false, stop); err != nil {
			log.Printf("tray: %v", err)
		}
	}()
}

// runTrayCompanion is the `pingping tray` subcommand: the separate process that
// accompanies an installed service. It exits quietly when there is nothing to
// accompany, so a stale Startup shortcut does not leave an icon pointing at
// software that has been uninstalled.
func runTrayCompanion(opt options) int {
	detachConsole()

	// One icon, however many times this is launched. The installer starts the
	// tray directly so the operator does not have to sign out and back in, and
	// the all-users Startup shortcut starts it again at the next sign-in — and a
	// failed install that was retried leaves one behind each time. Three copies
	// of the same icon appeared in the notification area, none of which could be
	// told apart or, before this release, closed.
	//
	// Global\ rather than Local\: the service runs in session 0 and the tray in
	// the user's session, and an installer running elevated is a third. The
	// handle is deliberately never released — the kernel drops it when the
	// process ends, which is exactly the lifetime being claimed.
	if !claimSingleInstance() {
		log.Printf("another pingping tray is already running")
		return 0
	}

	// The Startup shortcut carries no arguments, so the port has to come from
	// what install recorded. Defaulting to 8518 here is what made the icon
	// silently never appear on an instance installed on any other port.
	port := opt.port
	if port == 0 {
		port = rememberedPort()
	}
	if port == 0 {
		port = installedPort()
	}
	if port == 0 {
		cfg, _, err := configure(opt)
		if err != nil {
			return 2
		}
		port = portNumber(cfg.Listen)
	}
	url := fmt.Sprintf("http://localhost:%d", port)

	// The service may still be starting — it is registered delayed-auto-start, so
	// at sign-in it frequently is. Wait, rather than exiting and leaving no icon.
	client := &http.Client{Timeout: 4 * time.Second}
	for i := 0; ; i++ {
		if _, err := client.Get(url + "/api/health"); err == nil {
			break
		}
		if i >= 40 { // ~2 minutes
			// Show the icon anyway, in its "not responding" state. An icon that
			// says something is wrong is far more useful than no icon, which is
			// indistinguishable from software that was never installed.
			break
		}
		time.Sleep(3 * time.Second)
	}
	if err := runTray(context.Background(), url, true, nil); err != nil {
		log.Printf("tray: %v", err)
		return 1
	}
	return 0
}

func portNumber(listen string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(portOf(listen), ":"))
	if n == 0 {
		n = 8518
	}
	return n
}

// claimSingleInstance reports whether this process is the first tray. A named
// mutex is the ordinary Windows way to ask; it needs no file, no port and no
// cleanup path that can be skipped by a crash.
func claimSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Global\pingping-tray`)
	if err != nil {
		return true // cannot ask; better a second icon than none at all
	}
	h, _, lastErr := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return true
	}
	return lastErr != syscall.Errno(windows.ERROR_ALREADY_EXISTS)
}

// quit closes the icon from any goroutine.
//
// DestroyWindow may only be called by the thread that created the window. This
// one is created on a locked OS thread that then runs the message loop, so a
// call from anywhere else fails and does nothing — which is exactly what "Stop
// pingping and exit the tray" did: it stopped the service and left the icon
// sitting there, because its wait ran in a goroutine.
//
// PostMessage is the cross-thread half of the API and is explicitly safe to call
// from anywhere; WM_CLOSE reaches the loop and DefWindowProc turns it into the
// DestroyWindow that had to happen on that thread all along. Everything that
// wants the icon gone goes through here, so the unsafe call has nowhere left to
// come back from.
func (t *tray) quit() {
	const wmClose = 0x0010
	procPostMessage.Call(uintptr(t.hwnd), wmClose, 0, 0)
}
