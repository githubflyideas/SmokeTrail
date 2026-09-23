package main

import (
	"fmt"
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
)

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

func (s *Store) SetConsolePort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return s.SetSetting(settingPort, strconv.Itoa(p))
}
