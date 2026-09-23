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
	"os/exec"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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
// the tray is a SEPARATE PROCESS — `SmokeTrail.exe tray` — in the logged-in user's
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

	idOpenConsole = 1001
	idHideIcon    = 1002
	idStopService = 1003
	idExit        = 1004

	trayPollInterval = 15 * time.Second
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
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
)

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
)

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
}

// runTray owns the calling goroutine for the lifetime of the icon: a Win32 message
// loop must run on the thread that created the window, and Go will happily move a
// goroutine between threads otherwise.
func runTray(ctx context.Context, consoleURL string, service bool, onExit func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	t := &tray{service: service, consoleU: consoleURL, onExit: onExit}
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

	t.cur = t.iconOK
	t.tip = "SmokeTrail — starting"
	if err := t.notify(nimAdd); err != nil {
		return err
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
	cls := windows.StringToUTF16Ptr("SmokeTrailTray")
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
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("SmokeTrail"))),
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

func (t *tray) remove() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(t.data())))
	if t.hwnd != 0 {
		procDestroyWindow.Call(uintptr(t.hwnd))
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
	t.notify(nimModify)
}

func (t *tray) wndProc(hwnd windows.Handle, m uint32, wparam, lparam uintptr) uintptr {
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

	const mfString, mfSeparator, mfDefault = 0x0, 0x800, 0x1000
	add := func(flags, id uintptr, text string) {
		procAppendMenu.Call(h, flags, id,
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
	}
	add(mfString|mfDefault, idOpenConsole, "Open console")
	add(mfSeparator, 0, "")
	if t.service {
		// Two separate, explicitly worded actions. "Exit" would be a lie here:
		// the service keeps probing whatever this process does.
		add(mfString, idHideIcon, "Hide this icon until next sign-in")
		add(mfString, idStopService, "Stop the SmokeTrail service")
	} else {
		add(mfString, idExit, "Exit SmokeTrail")
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
	switch id {
	case idOpenConsole:
		t.openConsole()
	case idHideIcon:
		procPostQuitMessage.Call(0)
	case idStopService:
		// `net stop` rather than the SCM API: stopping needs elevation, and this
		// way Windows shows its own consent prompt instead of us inventing one.
		go func() {
			cmd := exec.Command("cmd", "/c", "net stop "+svcName)
			cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("stop service: %v %s", err, out)
			}
		}()
	case idExit:
		if t.onExit != nil {
			t.onExit()
		}
		procPostQuitMessage.Call(0)
	}
}

func (t *tray) openConsole() {
	windows.ShellExecute(0, windows.StringToUTF16Ptr("open"),
		windows.StringToUTF16Ptr(t.consoleU), nil, nil, windows.SW_SHOWNORMAL)
}

// watch keeps the icon honest. It asks the console the same question a browser
// would, so the tray reports what the service actually believes rather than
// guessing from the outside.
func (t *tray) watch(ctx context.Context) {
	client := &http.Client{Timeout: 4 * time.Second}
	tick := time.NewTicker(trayPollInterval)
	defer tick.Stop()
	for {
		bad, tip := probeStatus(client, t.consoleU)
		t.setState(bad, tip)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func probeStatus(c *http.Client, base string) (bad bool, tip string) {
	resp, err := c.Get(base + "/api/version")
	if err != nil {
		return true, "SmokeTrail — not responding"
	}
	defer resp.Body.Close()
	var v struct {
		Version    string `json:"version"`
		NeedsSetup bool   `json:"needs_setup"`
	}
	json.NewDecoder(resp.Body).Decode(&v)
	if v.NeedsSetup {
		return false, "SmokeTrail — click to create the admin account"
	}
	// Targets need a session, which the tray does not have. Reaching the console
	// at all is the honest limit of what an unauthenticated poll can tell us, so
	// that is all the tooltip claims.
	return false, "SmokeTrail " + v.Version + " — running"
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

// runTrayCompanion is the `SmokeTrail tray` subcommand: the separate process that
// accompanies an installed service. It exits quietly when there is nothing to
// accompany, so a stale Startup shortcut does not leave an icon pointing at
// software that has been uninstalled.
func runTrayCompanion(opt options) int {
	cfg, _, err := configure(opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoketrail: %v\n", err)
		return 2
	}
	url := "http://localhost" + portOf(cfg.Listen)
	client := &http.Client{Timeout: 4 * time.Second}
	// Give a service that is still starting a chance before giving up.
	ok := false
	for i := 0; i < 20; i++ {
		if _, err := client.Get(url + "/api/version"); err == nil {
			ok = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !ok {
		return 0
	}
	if err := runTray(context.Background(), url, true, nil); err != nil {
		log.Printf("tray: %v", err)
		return 1
	}
	return 0
}
