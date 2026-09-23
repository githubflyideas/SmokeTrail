package main

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests exist because migrateLegacyData moves the only copy of up to 300
// days of measurements. Every case below is a way to lose it.

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	return string(b)
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected %s to be gone", path)
	}
}

func TestMigrateFreshInstallDoesNothing(t *testing.T) {
	dir := t.TempDir()
	from, err := migrateLegacyData(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if from != "" {
		t.Fatalf("reported a migration from %q on a fresh install", from)
	}
	absent(t, filepath.Join(dir, dbFileName))
}

// The one that would destroy data: a current database and a leftover legacy one
// side by side. The current database must win, untouched.
func TestMigrateNeverOverwritesExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, dbFileName), "CURRENT")
	write(t, filepath.Join(dir, "smoketrail.db"), "OLD")

	from, err := migrateLegacyData(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if from != "" {
		t.Fatalf("migrated %q over a live database", from)
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "CURRENT" {
		t.Fatalf("live database was modified: %q", got)
	}
	if got := read(t, filepath.Join(dir, "smoketrail.db")); got != "OLD" {
		t.Fatalf("legacy database was modified: %q", got)
	}
}

// A -wal holds committed transactions not yet checkpointed into the main file.
// Moving the .db without it silently drops the most recent rounds.
func TestMigrateCarriesWalAndShm(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "smoketrail.db"), "MAIN")
	write(t, filepath.Join(dir, "smoketrail.db-wal"), "WAL")
	write(t, filepath.Join(dir, "smoketrail.db-shm"), "SHM")

	from, err := migrateLegacyData(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if from == "" {
		t.Fatal("did not migrate an obvious legacy database")
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "MAIN" {
		t.Fatalf("main file: %q", got)
	}
	if got := read(t, filepath.Join(dir, dbFileName+"-wal")); got != "WAL" {
		t.Fatalf("wal: %q", got)
	}
	if got := read(t, filepath.Join(dir, dbFileName+"-shm")); got != "SHM" {
		t.Fatalf("shm: %q", got)
	}
	absent(t, filepath.Join(dir, "smoketrail.db"))
	absent(t, filepath.Join(dir, "smoketrail.db-wal"))
}

func TestMigrateFromFogping(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "fogping.db"), "FOG")

	if _, err := migrateLegacyData(dir, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "FOG" {
		t.Fatalf("got %q", got)
	}
}

// An install that went fogping -> SmokeTrail leaves both files behind. The newer
// one is the one with the history; the older must be left alone rather than
// merged or deleted, so the operator can still see it.
func TestMigratePrefersTheNewerLegacyName(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "fogping.db"), "FOG")
	write(t, filepath.Join(dir, "smoketrail.db"), "SMOKE")

	if _, err := migrateLegacyData(dir, false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "SMOKE" {
		t.Fatalf("took the older database: %q", got)
	}
	if got := read(t, filepath.Join(dir, "fogping.db")); got != "FOG" {
		t.Fatalf("older database should be left untouched, got %q", got)
	}
}

// Running twice must be identical to running once — the service restarts.
func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "smoketrail.db"), "DATA")

	if _, err := migrateLegacyData(dir, false); err != nil {
		t.Fatal(err)
	}
	from, err := migrateLegacyData(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if from != "" {
		t.Fatalf("second run migrated again from %q", from)
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "DATA" {
		t.Fatalf("got %q", got)
	}
}

// A portable copy must not adopt an installed service's database. On Unix there
// are no legacy system directories at all, so this asserts the weaker but still
// meaningful property: portable mode looks only where it is allowed to.
func TestMigratePortableStaysInItsOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "smoketrail.db"), "MINE")

	from, err := migrateLegacyData(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if from == "" {
		t.Fatal("portable mode should still migrate its own directory")
	}
	if got := read(t, filepath.Join(dir, dbFileName)); got != "MINE" {
		t.Fatalf("got %q", got)
	}
}
