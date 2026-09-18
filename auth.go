package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Authentication the way a Windows service has to do it: nothing on the command
// line, nothing in a config file, no read-only mode to fall back on.
//
// fogping took `user=admin passwd=secret` as arguments and, when no login was
// given, ran an open console in read-only mode. Neither idiom survives the move to
// a service. A service's command line lives in the registry, readable by every
// account on the box, so a password there is a password published; and nobody is
// at a terminal to type one. So SmokeTrail does what Technitium does: the first
// browser to reach the console sets the admin password, it is stored hashed, and
// there is no unauthenticated state afterwards. See docs/adr/0004-auth.md.
//
// PBKDF2-HMAC-SHA256 comes from the standard library (Go 1.24). bcrypt or argon2
// would be better against offline cracking, but both are third-party, and the
// threat here is a LAN-reachable console rather than a leaked password database.
// A stdlib KDF with a real iteration count is the right trade for a zero-dependency
// binary; the format below is versioned so it can be raised later.

const (
	kdfIterations = 600_000 // OWASP's 2023 floor for PBKDF2-HMAC-SHA256
	kdfKeyLen     = 32
	saltLen       = 16

	settingAdminHash = "admin_password"
	settingAdminUser = "admin_user"
)

// hashPassword returns "pbkdf2$sha256$<iters>$<salt-b64>$<key-b64>".
func hashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, kdfIterations, kdfKeyLen)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s", kdfIterations, b64(salt), b64(key)), nil
}

// verifyPassword is constant-time in the comparison. It reads the iteration count
// from the stored record, so raising kdfIterations does not lock anyone out.
func verifyPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iters, err := strconv.Atoi(parts[2])
	if err != nil || iters < 1000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iters, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// validatePassword is deliberately a floor, not a policy. Complexity rules push
// people toward Passw0rd!; length is what actually helps, and this console is
// usually not internet-facing.
func validatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if utf8.RuneCountInString(pw) > 256 {
		return fmt.Errorf("password must be at most 256 characters")
	}
	return nil
}

func validateUsername(u string) error {
	if n := utf8.RuneCountInString(u); n < 1 || n > 64 {
		return fmt.Errorf("username must be 1-64 characters")
	}
	if strings.ContainsAny(u, "\x00\r\n") {
		return fmt.Errorf("username contains a control character")
	}
	return nil
}
