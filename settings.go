package main

import (
	"fmt"
	"net"
	"strconv"
)

// A tiny key/value table for the handful of things that are neither a target nor a
// measurement: the admin credential, and whatever setup state follows it. It lives
// in the same database so a portable copy is genuinely one directory, and so the
// installer has nothing to write outside it.

func (s *Store) Setting(key string) (string, bool) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err == nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// NeedsSetup reports whether there are no accounts at all — the state the console
// turns into its first-run screen.
func (s *Store) NeedsSetup() bool { return s.UserCount() == 0 }

// Settings that used to be flags. They live here rather than on the service
// command line so an operator can change them in the console, which is the only
// place they will think to look — and because a service command line lives in the
// registry, where changing it means re-registering the service.
const (
	settingPort = "console_port"
	settingBind = "console_bind"
)

// defaultBind is loopback on purpose, and it is a security decision rather than
// a preference.
//
// The first-run window is unauthenticated by construction: until a password
// exists, POST /api/setup creates the admin account for whoever asks, and
// sameOrigin() — a CSRF guard, not an authentication control — waves through any
// request with no Origin header, which is every request not made by a browser.
// The Windows installer opens a firewall rule and starts the service as its last
// act, so binding every interface by default meant that between the installer
// finishing and the operator reaching a browser, anyone on the subnet could
// claim the console with one curl. There is no recovery from that: the password
// is a PBKDF2-HMAC-SHA256 record with no backdoor, so the rightful operator's
// next request is "already set up" and they are locked out of their own machine.
//
// Loopback first, set a password, then open it up deliberately from the console.
// That is the order that has no window in it.
const defaultBind = "127.0.0.1"

// ConsolePort is the configured port, or 0 when nothing has been set and the
// caller should fall back to the flag or the default.
func (s *Store) ConsolePort() int {
	v, ok := s.Setting(settingPort)
	if !ok {
		return 0
	}
	n := 0
	fmt.Sscanf(v, "%d", &n)
	if n < 1 || n > 65535 {
		return 0
	}
	return n
}

// ConsoleBind is the configured bind address, or "" when nothing has been set.
func (s *Store) ConsoleBind() string {
	v, ok := s.Setting(settingBind)
	if !ok || v == "" {
		return ""
	}
	if err := validateBind(v); err != nil {
		// A stored value that cannot be bound would leave the service dead after
		// a restart, with no console to fix it from. Ignore it instead.
		logf("console: stored bind address %q is unusable (%v) — using %s", v, err, defaultBind)
		return ""
	}
	return v
}

func (s *Store) SetConsoleBind(addr string) error {
	if err := validateBind(addr); err != nil {
		return err
	}
	return s.SetSetting(settingBind, addr)
}

// validateBind rejects anything this machine could not actually listen on.
//
// The check is a real bind on port 0 rather than a parse, because the failure
// this prevents is not a malformed string — it is a perfectly well-formed
// address that belongs to some other machine. Storing one of those turns the
// next restart into a service that will not start, and the console that would
// have let someone fix it is the thing that did not start.
func validateBind(addr string) error {
	if addr == "" {
		return fmt.Errorf("bind address must not be empty")
	}
	if net.ParseIP(addr) == nil {
		return fmt.Errorf("%q is not an IP address — use 127.0.0.1 for this machine only, "+
			"0.0.0.0 for every interface, or the address of one interface", addr)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		return fmt.Errorf("this machine cannot listen on %s: %w", addr, err)
	}
	ln.Close()
	return nil
}

// isLoopbackBind reports whether a bind address keeps the console off the
// network. The firewall rule and the "reachable from" note both turn on it.
func isLoopbackBind(addr string) bool {
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

func validatePortValue(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}

func (s *Store) SetConsolePort(p int) error {
	if err := validatePortValue(p); err != nil {
		return err
	}
	return s.SetSetting(settingPort, strconv.Itoa(p))
}
