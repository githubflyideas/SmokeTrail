package main

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Two roles, and no plans for a third.
//
//	admin   may change what this host probes, and manage accounts
//	viewer  may look at the graphs, and nothing else
//
// The line is drawn there because adding a target makes this machine send
// packets to an address the requester chose. That is not a capability to hand
// out with a "here, have a look" URL, which is exactly what people do with a
// monitoring console.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

var (
	errUserExists   = errors.New("a user with that name already exists")
	errUserNotFound = errors.New("no such user")
	errLastAdmin    = errors.New("this is the only admin — promote another account first")
)

type User struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Created int64  `json:"created"`
}

func validRole(r string) bool { return r == RoleAdmin || r == RoleViewer }

// migrateUsers moves the single account created by an older first-run setup into
// the users table. Upgrading must not lock anybody out of their own console.
func (s *Store) migrateUsers() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	name, ok1 := s.Setting(settingAdminUser)
	hash, ok2 := s.Setting(settingAdminHash)
	if !ok1 || !ok2 || name == "" || hash == "" {
		return nil // genuinely fresh: first run will create the account
	}
	_, err := s.db.Exec(`INSERT INTO users(name,hash,role,created) VALUES(?,?,?,?)`,
		name, hash, RoleAdmin, time.Now().Unix())
	if err == nil {
		// The old keys stay put. They cost nothing, and a half-finished upgrade
		// that can still authenticate is better than one that cannot.
		logf("upgraded: moved the admin account %q into the users table", name)
	}
	return err
}

func (s *Store) UserCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// Authenticate returns the role on success. A wrong name and a wrong password are
// indistinguishable from outside, on purpose.
func (s *Store) Authenticate(name, pass string) (string, bool) {
	var hash, role string
	err := s.db.QueryRow(`SELECT hash, role FROM users WHERE name=?`, name).Scan(&hash, &role)
	if err != nil {
		// Still spend the time a real verification would, so the response time
		// does not say whether the account exists.
		verifyPassword("pbkdf2$sha256$600000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", pass)
		return "", false
	}
	if !verifyPassword(hash, pass) {
		return "", false
	}
	return role, true
}

func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT name, role, created FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Name, &u.Role, &u.Created); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, rows.Err()
}

func (s *Store) CreateUser(name, pass, role string) error {
	if err := validateUsername(name); err != nil {
		return err
	}
	if err := validatePassword(pass); err != nil {
		return err
	}
	if !validRole(role) {
		return fmt.Errorf("role must be %q or %q", RoleAdmin, RoleViewer)
	}
	h, err := hashPassword(pass)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users(name,hash,role,created) VALUES(?,?,?,?)`,
		name, h, role, time.Now().Unix())
	if isUniqueViolation(err) {
		return errUserExists
	}
	return err
}

func (s *Store) SetPassword(name, pass string) error {
	if err := validatePassword(pass); err != nil {
		return err
	}
	h, err := hashPassword(pass)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET hash=? WHERE name=?`, h, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errUserNotFound
	}
	return nil
}

// SetRole refuses to demote the last admin, which would leave the console with
// nobody who can add a target or create an account.
func (s *Store) SetRole(name, role string) error {
	if !validRole(role) {
		return fmt.Errorf("role must be %q or %q", RoleAdmin, RoleViewer)
	}
	if role != RoleAdmin {
		if last, err := s.isLastAdmin(name); err != nil {
			return err
		} else if last {
			return errLastAdmin
		}
	}
	res, err := s.db.Exec(`UPDATE users SET role=? WHERE name=?`, role, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errUserNotFound
	}
	return nil
}

func (s *Store) DeleteUser(name string) error {
	if last, err := s.isLastAdmin(name); err != nil {
		return err
	} else if last {
		return errLastAdmin
	}
	res, err := s.db.Exec(`DELETE FROM users WHERE name=?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errUserNotFound
	}
	return nil
}

func (s *Store) isLastAdmin(name string) (bool, error) {
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE name=?`, name).Scan(&role); err != nil {
		return false, nil // not found; the caller reports that
	}
	if role != RoleAdmin {
		return false, nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role=?`, RoleAdmin).Scan(&n); err != nil {
		return false, err
	}
	return n <= 1, nil
}
