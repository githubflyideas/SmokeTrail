package main

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

// NeedsSetup reports whether no admin password has been set yet — the state the
// console turns into its first-run screen.
func (s *Store) NeedsSetup() bool {
	h, ok := s.Setting(settingAdminHash)
	return !ok || h == ""
}
